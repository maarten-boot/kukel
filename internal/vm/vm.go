// Package vm executes compiled Kukel programs on a stack machine.
//
// Each call has a frame. A frame's slots (parameters, locals, hidden slots)
// sit on the value stack at frame.base, followed by its operand stack. The
// callee value sits just below the slots.
package vm

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// MaxFrames bounds the call depth; deeper recursion is a stack overflow.
const MaxFrames = 10000

// TraceEntry is one line of a stack trace.
type TraceEntry struct {
	Func string
	File string
	Line int
}

// RuntimeError is a thrown exception: the thrown value and the stack trace
// at the throw. It is what Run returns for an exception nobody caught.
type RuntimeError struct {
	Value value.Value  // the thrown Error object (nil for the step limit, cancellation, exit)
	Kind  string       // its kind (never "": an empty kind becomes the class name)
	Msg   string       // its message ("" without one)
	Trace []TraceEntry // innermost call first
	Fatal bool         // try cannot catch it (exit, the step limit, cancellation)
	Exit  *int         // set by exit(code): not an error, the program ends
}

// Error returns the message and the stack trace. Long traces (deep
// recursion) show only the first and last traceEnds calls.
func (e *RuntimeError) Error() string {
	const traceEnds = 10
	var b strings.Builder
	switch {
	case e.Msg == "":
		b.WriteString(e.Kind)
	default:
		fmt.Fprintf(&b, "%s: %s", e.Kind, e.Msg)
	}
	for i, t := range e.Trace {
		if n := len(e.Trace); n > 2*traceEnds+1 && i >= traceEnds && i < n-traceEnds {
			if i == traceEnds {
				fmt.Fprintf(&b, "\n  ... %d more calls ...", n-2*traceEnds)
			}
			continue
		}
		fmt.Fprintf(&b, "\n  at %s (%s:%d)", t.Func, t.File, t.Line)
	}
	return b.String()
}

type frame struct {
	fn   *bytecode.Function
	ip   int               // next instruction (saved while the frame calls another)
	base int               // first slot
	argc int               // arguments passed (for JUMP_IF_ARG)
	this *value.ObjectObj  // the object of a member function call (nil without one)
	ctor bool              // a constructor called by new: it returns this
	cl   *value.ClosureObj // a closure call: its captured variables
}

// handler is a registered try handler.
type handler struct {
	frame   int  // index of the frame that registered it
	pc      int  // where to continue
	sp      int  // stack depth to restore
	finally bool // a finally handler gets the pending exception, not the value
}

// VM runs one program.
type VM struct {
	prog       *bytecode.Program
	globals    []value.Value
	classes    []*value.ClassObj
	caches     [][]icEntry // inline caches per function (index: Function.Index)
	initCache  []icEntry   // the init function's
	handlers   []handler
	errorClass *value.ClassObj // the prelude's Error class

	// StepLimit, when positive, stops the program with a fatal error after
	// that many backward jumps, calls of Kukel functions and thrown
	// exceptions: for tests, and for Go programs that run untrusted code
	// (the kukel package resets the count for every call from Go).
	StepLimit int
	steps     int
	stack     []value.Value
	sp        int // first free stack entry
	frames    []frame
	stdout    io.Writer
	stdin     *bufio.Reader
	rand      *rand.Rand
	started   time.Time

	stderr   io.Writer   // what eprint writes to (os.Stderr unless SetStderr)
	locals   map[any]any // per-program state of native modules (Local)
	builtins Builtins    // how New found the built-ins (for workers)
	config   *native.Config
	inited   bool       // Init has run
	initErr  error      // why Init failed
	cancel   *Canceller // set by Cancellable; nil: the program cannot be cancelled

	// Debug, when set, runs at every BREAK instruction: at the start of each
	// source line of code that the debugger made a copy of (Debuggable).
	// The VM's frames are then as at the start of that line (Frames). An
	// error stops the program (a fatal one, such as exit, ends it).
	Debug func(vm *VM) error

	// DebugUncaught, when set, runs when an exception is thrown that no
	// try of the program catches (a finally does not count), before the
	// frames are dropped (Frames). An error it gives ends the program
	// instead (a fatal one, such as exit).
	DebugUncaught func(vm *VM, re *RuntimeError) error

	// Last, so that the fields above keep their places (and the run
	// loop its speed):
	prof *profState // set while profiling (StartProfile)
	// watched: step has something to do (a step limit, a canceller or
	// profiling), so that it returns at once with one test when not (it
	// runs at every backward jump). Set by execute, Cancellable,
	// ShareCanceller and StartProfile.
	watched bool
}

// Builtins resolves built-in functions by name.
type Builtins func(name string) (*value.BuiltinObj, bool)

// New prepares prog for running: it binds the functions and built-ins to
// their global slots.
func New(prog *bytecode.Program, builtins Builtins, stdout io.Writer, stdin io.Reader) (*VM, error) {
	return NewWith(prog, builtins, nil, stdout, stdin)
}

// NewWith is New for a program limited by config (see native.Config): it
// refuses a program that uses a built-in or module config does not allow.
func NewWith(prog *bytecode.Program, builtins Builtins, config *native.Config, stdout io.Writer, stdin io.Reader) (*VM, error) {
	vm := &VM{
		config:   config,
		builtins: builtins,
		prog:     prog,
		globals:  make([]value.Value, len(prog.Globals)),
		stack:    make([]value.Value, 1024),
		stdout:   stdout,
		stdin:    bufio.NewReader(stdin),
		rand:     rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
		started:  time.Now(),
		stderr:   os.Stderr,
	}
	if err := vm.link(0, 0); err != nil {
		return nil, err
	}
	vm.initCache = make([]icEntry, prog.Init.NumCaches)
	for _, c := range vm.classes {
		if c.Name == "Error" && c.Base == nil {
			vm.errorClass = c
			break
		}
	}
	if vm.errorClass == nil {
		return nil, fmt.Errorf("the program has no Error class")
	}
	for _, field := range []string{"message", "kind", "trace"} {
		if _, ok := vm.errorClass.FieldIndex[field]; !ok {
			return nil, fmt.Errorf("class Error has no data member %s", field)
		}
	}
	return vm, nil
}

// link binds the globals of the program from index globals on, makes its
// classes from index classes on, and sets up the inline caches of the
// functions it does not have yet.
func (vm *VM) link(globals, classes int) error {
	prog := vm.prog
	vm.globals = append(vm.globals, make([]value.Value, len(prog.Globals)-len(vm.globals))...)
	for i := globals; i < len(prog.Globals); i++ {
		g := prog.Globals[i]
		switch g.Kind {
		case bytecode.GlobalFunc:
			if g.Func < 0 || g.Func >= len(prog.Funcs) {
				return fmt.Errorf("global %s: bad function index %d", g.Name, g.Func)
			}
			vm.globals[i] = value.MakeFunction(prog.Funcs[g.Func])
		case bytecode.GlobalBuiltin:
			if vm.config.BuiltinDenied(g.Name) {
				return fmt.Errorf("built-in function %s is not available in this program", g.Name)
			}
			b, ok := vm.builtins(g.Name)
			if !ok {
				return fmt.Errorf("unknown built-in function %s", g.Name)
			}
			vm.globals[i] = value.MakeBuiltin(b)
		case bytecode.GlobalNative:
			v, ok := vm.config.Global(g.Name)
			if mod, _, _ := strings.Cut(g.Name, "::"); !ok && vm.config.Disallowed(mod) {
				return fmt.Errorf("module %s is not available in this program", mod)
			}
			if !ok {
				return fmt.Errorf("%s is not available: its module written in Go is missing (a module of the Go program that runs Kukel must be given again)", g.Name)
			}
			vm.globals[i] = v
		}
	}
	if err := vm.makeClasses(classes); err != nil {
		return err
	}
	for i := globals; i < len(prog.Globals); i++ {
		if g := prog.Globals[i]; g.Kind == bytecode.GlobalClass {
			if g.Class < 0 || g.Class >= len(vm.classes) {
				return fmt.Errorf("global %s: bad class index %d", g.Name, g.Class)
			}
			vm.globals[i] = value.MakeClass(vm.classes[g.Class])
		}
	}
	for i := len(vm.caches); i < len(prog.Funcs); i++ {
		vm.caches = append(vm.caches, make([]icEntry, prog.Funcs[i].NumCaches))
	}
	return nil
}

// Extend continues with prog, a program that adds to the VM's program
// (compiler.Session): globals, classes and functions after the ones the VM
// has, and member functions defined since for the classes it has. The
// values of the globals stay.
func (vm *VM) Extend(prog *bytecode.Program) error {
	globals, classes := len(vm.globals), len(vm.classes)
	vm.prog = prog
	for i, k := range vm.classes {
		for _, m := range prog.Classes[i].Methods {
			if mo := k.Methods[m.Name]; m.Func >= 0 && mo != nil && mo.Class == k && mo.Fn == nil {
				mo.Fn = prog.Funcs[m.Func]
			}
		}
	}
	return vm.link(globals, classes)
}

// makeClasses builds the run-time classes from index from on: an object's data members are the
// base class's followed by the class's own; the method table holds the
// inherited member functions with the class's own ones overriding them.
func (vm *VM) makeClasses(from int) error {
	for i := from; i < len(vm.prog.Classes); i++ {
		c := vm.prog.Classes[i]
		k := &value.ClassObj{
			Name:        c.Name,
			FieldIndex:  map[string]int{},
			Methods:     map[string]*value.MethodObj{},
			Statics:     make([]value.Value, len(c.Statics)),
			StaticNames: c.Statics,
		}
		if c.Native != "" {
			// A native base class, linked by name like the other names of
			// modules written in Go.
			v, ok := vm.config.Global(c.Native)
			if !ok || v.Kind() != value.Class || !v.AsClass().Native {
				return fmt.Errorf("class %s: its base class %s is not available (a module written in Go is missing)", c.Name, c.Native)
			}
			k.Base = v.AsClass()
			for name, m := range k.Base.Methods {
				k.Methods[name] = m
			}
		}
		if c.Base >= 0 {
			if c.Base >= i {
				return fmt.Errorf("class %s: base class %d is not defined before it", c.Name, c.Base)
			}
			k.Base = vm.classes[c.Base]
			k.FieldNames = append(k.FieldNames, k.Base.FieldNames...)
			for name, m := range k.Base.Methods {
				k.Methods[name] = m
			}
		}
		k.FieldNames = append(k.FieldNames, c.Fields...)
		for j, name := range k.FieldNames {
			k.FieldIndex[name] = j
		}
		for _, m := range c.Methods {
			mo := &value.MethodObj{Name: m.Name, Class: k, Static: m.Static}
			if m.Func >= 0 {
				if m.Func >= len(vm.prog.Funcs) {
					return fmt.Errorf("class %s: bad function index %d", c.Name, m.Func)
				}
				mo.Fn = vm.prog.Funcs[m.Func]
			}
			k.Methods[m.Name] = mo
			if m.Name == bytecode.LocalName(c.Name) && !m.Static {
				k.Ctor = mo
			}
		}
		if k.Ctor == nil && k.Base != nil {
			k.Ctor = k.Base.Ctor // a class without a constructor uses its base's (Q33)
		}
		vm.classes = append(vm.classes, k)
	}
	return nil
}

// Stdout, Stderr, Stdin, Call, Rand, Started, Local, Thrown, Done and Err
// implement value.Env for the built-ins and the native modules.
func (vm *VM) Stdout() io.Writer { return vm.stdout }
func (vm *VM) Stderr() io.Writer { return vm.stderr }

// SetStderr sets where eprint writes.
func (vm *VM) SetStderr(w io.Writer) { vm.stderr = w }
func (vm *VM) Stdin() *bufio.Reader  { return vm.stdin }
func (vm *VM) Rand() *rand.Rand      { return vm.rand }
func (vm *VM) Started() time.Time    { return vm.started }

// Local implements value.Env.
func (vm *VM) Local(key any, init func() any) any {
	if vm.locals == nil {
		vm.locals = map[any]any{}
	}
	v, ok := vm.locals[key]
	if !ok {
		v = init()
		vm.locals[key] = v
	}
	return v
}

// Thrown implements value.Env.
func (vm *VM) Thrown(err error) (value.Value, bool) {
	if re, ok := err.(*RuntimeError); ok && !re.Fatal && re.Exit == nil {
		return re.Value, true
	}
	return value.NilValue, false
}

// Call calls fn from a built-in (sort's comparator, for example). The
// built-in's own arguments stay on the stack below the new call.
func (vm *VM) Call(fn value.Value, args []value.Value) (value.Value, error) {
	return vm.call(fn, args)
}

// Run runs the global initializers, then main(args). It returns main's
// result as the exit code (an int result; anything else is 0).
//
// When main returns, the program ends: if workers share the output
// (ShareOutput), it is flushed and closed, so that workers still running
// cannot write any more.
func (vm *VM) Run(args []string) (int, error) {
	defer vm.closeOutput()
	elems := make([]value.Value, len(args))
	for i, a := range args {
		elems[i] = value.MakeString(a)
	}
	result, err := vm.Start(value.NilValue, []value.Value{value.MakeVector(elems)})
	if re, ok := err.(*RuntimeError); ok && re.Exit != nil {
		return *re.Exit, nil // exit(code)
	}
	if err != nil {
		return 1, err
	}
	if result.Kind() == value.Int {
		return int(result.AsInt()), nil
	}
	return 0, nil
}

// Start runs the global initializers, then calls fn(args...) and gives its
// result. With fn nil it calls main, with args[0] as its parameter if main
// has one. exit(code) gives a *RuntimeError with Exit set.
func (vm *VM) Start(fn value.Value, args []value.Value) (value.Value, error) {
	if err := vm.Init(); err != nil {
		return value.NilValue, err
	}
	if fn.IsNil() {
		fn = vm.globals[vm.prog.Main]
		if f, ok := fn.Obj().(*bytecode.Function); !ok || f.ParamSlots() == 0 {
			args = nil
		} else if len(args) > 1 {
			args = args[:1]
		}
	}
	return vm.call(fn, args)
}

// Init runs the global initializers, the first time only. If they throw an
// exception, every later Init gives the same error: the globals are not
// usable. If the step limit or a cancellation stopped them, the next Init
// runs them again from the start.
func (vm *VM) Init() error {
	if !vm.inited {
		vm.inited = true
		_, vm.initErr = vm.call(value.MakeFunction(vm.prog.Init), nil)
		if re, ok := vm.initErr.(*RuntimeError); ok && re.Fatal && re.Exit == nil {
			err := vm.initErr
			vm.inited, vm.initErr = false, nil
			return err
		}
	}
	return vm.initErr
}

// CloseOutput flushes the output shared with workers (ShareOutput) and
// drops what they write later. Run does it when main returns.
func (vm *VM) CloseOutput() { vm.closeOutput() }

// Global returns the value of the global called name: a top-level name of
// the program ("main"), or a module's ("util::f", "json::encode").
func (vm *VM) Global(name string) (value.Value, bool) {
	for i, g := range vm.prog.Globals {
		if g.Name == name {
			return vm.globals[i], true
		}
	}
	return value.NilValue, false
}

// SetGlobal gives the global variable called name (as Global names it) the
// value v, and reports whether there is one; functions and classes cannot
// be changed.
func (vm *VM) SetGlobal(name string, v value.Value) bool {
	for i, g := range vm.prog.Globals {
		if g.Name == name && g.Kind == bytecode.GlobalVar {
			vm.globals[i] = v
			return true
		}
	}
	return false
}

// Globals gives the names of the program's global variables (not its
// functions and classes), in the order of the program.
func (vm *VM) Globals() []string {
	var names []string
	for _, g := range vm.prog.Globals {
		if g.Kind == bytecode.GlobalVar {
			names = append(names, g.Name)
		}
	}
	return names
}

// ResetSteps starts counting steps for StepLimit from zero again.
func (vm *VM) ResetSteps() { vm.steps = 0 }

// Config is what the program may use (nil: everything).
func (vm *VM) Config() *native.Config { return vm.config }

// Canceller stops a running program from another goroutine. Cancellers
// form a tree: cancelling one cancels the ones made from it (Child).
type Canceller struct {
	flag atomic.Bool
	once sync.Once
	done chan struct{}
	err  error
	keep atomic.Bool // workers use it: Release keeps it in its parent

	mu       sync.Mutex
	children map[*Canceller]bool
}

// NewCanceller makes a Canceller that nothing has cancelled yet.
func NewCanceller() *Canceller { return &Canceller{done: make(chan struct{})} }

// Cancel makes the programs that use c (and its children) stop at their
// next step with a CancelledError that try cannot catch; Go functions that
// wait (sleep, receive) return at once. It may be called from any
// goroutine, and more than once.
func (c *Canceller) Cancel(reason error) {
	c.once.Do(func() {
		c.err = reason
		c.flag.Store(true)
		close(c.done)
		c.mu.Lock()
		children := c.children
		c.children = nil
		c.mu.Unlock()
		for child := range children {
			child.Cancel(reason)
		}
	})
}

// Child makes a Canceller that is cancelled when c is (at once if c
// already is).
func (c *Canceller) Child() *Canceller {
	child := NewCanceller()
	c.mu.Lock()
	cancelled := c.flag.Load()
	if !cancelled {
		if c.children == nil {
			c.children = map[*Canceller]bool{}
		}
		c.children[child] = true
	}
	c.mu.Unlock()
	if cancelled {
		child.Cancel(c.err)
	}
	return child
}

// Release forgets child (made by Child) when it is no longer needed, unless
// workers use it: they must still be cancelled with c.
func (c *Canceller) Release(child *Canceller) {
	if child.keep.Load() {
		return
	}
	c.mu.Lock()
	delete(c.children, child)
	c.mu.Unlock()
}

// Keep marks c as used by a worker (see Release).
func (c *Canceller) Keep() { c.keep.Store(true) }

// Cancellable makes the program cancellable from now on with the
// returned Canceller (replacing an earlier one). Workers started
// afterwards share it: cancelling the program cancels them too.
func (vm *VM) Cancellable() *Canceller {
	vm.cancel = NewCanceller()
	vm.watched = true
	return vm.cancel
}

// CurrentCanceller is the VM's Canceller, or nil.
func (vm *VM) CurrentCanceller() *Canceller { return vm.cancel }

// ShareCanceller makes vm stop when c is cancelled (for a worker).
func (vm *VM) ShareCanceller(c *Canceller) { vm.cancel, vm.watched = c, true }

// Done implements value.Env: closed when the program is cancelled (nil if
// it cannot be).
func (vm *VM) Done() <-chan struct{} {
	if vm.cancel == nil {
		return nil
	}
	return vm.cancel.done
}

// Err implements value.Env: the error for a cancelled program, else nil.
func (vm *VM) Err() error {
	if vm.cancel == nil || !vm.cancel.flag.Load() {
		return nil
	}
	msg := "the program was cancelled"
	if vm.cancel.err != nil {
		msg += ": " + vm.cancel.err.Error()
	}
	return &RuntimeError{Kind: "CancelledError", Msg: msg, Trace: vm.trace(), Fatal: true}
}

// Program is the program the VM runs.
func (vm *VM) Program() *bytecode.Program { return vm.prog }

// BuiltinLookup is how the VM found its built-ins (New's builtins).
func (vm *VM) BuiltinLookup() Builtins { return vm.builtins }

// ShareOutput makes the VM's standard output and error safe to use from
// several goroutines (workers) and returns them; other VMs can write to
// them too. Calling it again gives the same writers.
func (vm *VM) ShareOutput() (stdout, stderr io.Writer) {
	if _, ok := vm.stdout.(*sharedWriter); !ok {
		vm.stdout = &sharedWriter{w: vm.stdout}
		vm.stderr = &sharedWriter{w: vm.stderr}
	}
	return vm.stdout, vm.stderr
}

func (vm *VM) closeOutput() {
	for _, w := range []io.Writer{vm.stdout, vm.stderr} {
		if s, ok := w.(*sharedWriter); ok {
			s.close()
		}
	}
}

// sharedWriter is an output several goroutines write to. After close,
// writes are dropped.
type sharedWriter struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
}

func (s *sharedWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return len(p), nil
	}
	return s.w.Write(p)
}

// Flush flushes the underlying writer if it buffers (eprint flushes the
// standard output first).
func (s *sharedWriter) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.w.(interface{ Flush() error }); ok && !s.closed {
		return f.Flush()
	}
	return nil
}

func (s *sharedWriter) close() {
	s.Flush()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// call calls fn with args from Go and runs it to completion. An exception
// that is not caught inside the call is returned as a *RuntimeError.
func (vm *VM) call(fn value.Value, args []value.Value) (value.Value, error) {
	sp0 := vm.sp
	vm.ensure(len(args) + 1)
	vm.stack[vm.sp] = fn
	vm.sp++
	for _, a := range args {
		vm.stack[vm.sp] = a
		vm.sp++
	}
	depth := len(vm.frames)
	if err := vm.callValue(len(args)); err != nil {
		vm.sp = sp0
		return value.NilValue, vm.wrap(err)
	}
	if len(vm.frames) == depth { // a built-in: already done
		vm.sp--
		return vm.stack[vm.sp], nil
	}
	r, err := vm.execute(depth)
	if err != nil {
		vm.sp = sp0
	}
	return r, err
}

// ensure makes room for n more values on the stack.
func (vm *VM) ensure(n int) {
	if vm.sp+n <= len(vm.stack) {
		return
	}
	size := 2 * len(vm.stack)
	for size < vm.sp+n {
		size *= 2
	}
	s := make([]value.Value, size)
	copy(s, vm.stack[:vm.sp])
	vm.stack = s
}

// callValue calls the value below the top argc stack entries. For a
// function it pushes a frame (the caller's ip must be saved already); a
// built-in runs at once and its result replaces callee and arguments.
func (vm *VM) callValue(argc int) error {
	callee := vm.stack[vm.sp-argc-1]
	switch callee.Kind() {
	case value.Function:
		return vm.callFunction(callee.Obj().(*bytecode.Function), nil, nil, argc, false)
	case value.BoundMethod:
		bm := callee.AsBoundMethod()
		return vm.invoke(bm.Method, bm.Recv, argc)
	case value.Closure:
		cl := callee.AsClosure()
		return vm.callFunction(cl.Fn.(*bytecode.Function), cl.This, cl, argc, false)
	case value.Builtin:
		return vm.callBuiltin(callee.AsBuiltin(), argc)
	case value.Class:
		name := callee.AsClass().Name
		return value.Errorf(value.TypeError, "cannot call class %s (use new %s(...))", name, name)
	}
	return value.Errorf(value.TypeError, "cannot call %s", callee.Kind())
}

// callGo runs a function written in Go (a built-in, or a member function
// of a native class) on the top argc stack entries; its result replaces
// them and the callee's place below them.
//
// The arguments are passed as a view of the stack, not a copy: the function
// must not keep the slice after it returns. The capacity is capped, so
// appending to it cannot overwrite the stack.
func (vm *VM) callGo(argc int, fn func(args []value.Value) (value.Value, error)) error {
	result, err := fn(vm.stack[vm.sp-argc : vm.sp : vm.sp])
	if err != nil {
		return err
	}
	vm.sp -= argc + 1
	vm.stack[vm.sp] = result
	vm.sp++
	return nil
}

// callBuiltin calls built-in b (also a function of a native module).
// (Profiling is done here and in invokeGo, around callGo: callGo stays
// small enough to be inlined, with fn.)
func (vm *VM) callBuiltin(b *value.BuiltinObj, argc int) error {
	if s := vm.prof; s != nil {
		s.countGo(b, nil)
		err := vm.callGo(argc, func(args []value.Value) (value.Value, error) { return b.Fn(vm, args) })
		vm.afterGo()
		return err
	}
	return vm.callGo(argc, func(args []value.Value) (value.Value, error) { return b.Fn(vm, args) })
}

// afterGo takes a sample after a call of a Go function while profiling, if
// one is wanted: its time, also of waiting, goes to the line that called
// it.
func (vm *VM) afterGo() {
	if s := vm.prof; s != nil && s.want.Load() {
		vm.sample(pcCaller)
	}
}

// invoke calls member function m on recv (a static one without this).
func (vm *VM) invoke(m *value.MethodObj, recv value.Value, argc int) error {
	if m.Fn != nil {
		var this *value.ObjectObj
		if !m.Static {
			this = objectOf(recv)
		}
		return vm.callFunction(m.Fn.(*bytecode.Function), this, nil, argc, false)
	}
	return vm.invokeGo(m, recv, argc)
}

// invokeGo calls a member function of a native class (kept out of invoke,
// whose path for Kukel code is the common one).
func (vm *VM) invokeGo(m *value.MethodObj, recv value.Value, argc int) error {
	if s := vm.prof; s != nil {
		s.countGo(nil, m)
		err := vm.invokeGoNow(m, recv, argc)
		vm.afterGo()
		return err
	}
	return vm.invokeGoNow(m, recv, argc)
}

func (vm *VM) invokeGoNow(m *value.MethodObj, recv value.Value, argc int) error {
	switch {
	case m.Native != nil:
		this := objectOf(recv)
		if err := nativePart(m, this); err != nil {
			return err
		}
		return vm.callGo(argc, func(args []value.Value) (value.Value, error) { return m.Native(vm, this, args) })
	case m.Builtin != nil:
		return vm.callGo(argc, func(args []value.Value) (value.Value, error) { return m.Builtin.Fn(vm, args) })
	}
	return value.Errorf(value.MemberError, "%s is declared but not defined", m.FullName())
}

// nativePart checks that an object of a class derived from a native class
// has its Go part before a member function written in Go uses it: the
// derived constructor must call the native one (Base::Base(...)).
func nativePart(m *value.MethodObj, this *value.ObjectObj) error {
	if this == nil || this.State != nil || this.Class.Native || m == m.Class.Ctor {
		return nil
	}
	return value.Errorf(value.ValueError, "%s: the %s part of this %s object is not set up: its constructor must call %s(...)",
		m.FullName(), m.Class.Name, this.Class.Name, m.Class.Ctor.FullName())
}

// newObject runs new: the class is below the argc arguments; it is replaced
// by the new object, and the constructor (if any) is called on it.
func (vm *VM) newObject(argc int) error {
	cv := vm.stack[vm.sp-argc-1]
	if cv.Kind() != value.Class {
		return value.Errorf(value.TypeError, "cannot create an object of %s with new", cv.Kind())
	}
	c := cv.AsClass()
	if c.Native && c.Ctor == nil {
		return value.Errorf(value.TypeError, "native class %s has no constructor: its objects come from the functions of its module", c.Name)
	}
	obj := value.NewObject(c)
	vm.stack[vm.sp-argc-1] = obj
	if c.Ctor == nil {
		if argc > 0 {
			return value.Errorf(value.ArgumentError, "class %s has no constructor, so new %s() takes no arguments (got %d)", c.Name, c.Name, argc)
		}
		return nil
	}
	if c.Ctor.Native != nil {
		// A native constructor fills in the object's Go value; new gives
		// the object, whatever the constructor returns.
		if _, err := c.Ctor.Native(vm, obj.AsObject(), vm.stack[vm.sp-argc:vm.sp:vm.sp]); err != nil {
			return err
		}
		vm.sp -= argc
		return nil
	}
	if c.Ctor.Fn == nil {
		return value.Errorf(value.MemberError, "%s is declared but not defined", c.Ctor.FullName())
	}
	return vm.callFunction(c.Ctor.Fn.(*bytecode.Function), obj.AsObject(), nil, argc, true)
}

// callOp performs a call instruction; ip points just past its opcode. The
// caller reloads its state from the (possibly new) top frame.
func (vm *VM) callOp(op bytecode.Op, f *frame, ip, sp int) error {
	code := f.fn.Code
	var argc, operand, cache int
	switch op {
	case bytecode.OpCall, bytecode.OpNew:
		argc = bytecode.ReadU16(code, ip)
		ip += 2
	case bytecode.OpCallDirect:
		operand, argc = bytecode.ReadU16(code, ip), bytecode.ReadU16(code, ip+2)
		ip += 4
	case bytecode.OpCallNative:
		operand, cache, argc = bytecode.ReadU16(code, ip), bytecode.ReadU16(code, ip+2), bytecode.ReadU16(code, ip+4)
		ip += 6
	case bytecode.OpCallMethod:
		operand, argc, cache = bytecode.ReadU16(code, ip), bytecode.ReadU16(code, ip+2), bytecode.ReadU16(code, ip+4)
		ip += 6
	case bytecode.OpCallMethodV:
		operand, cache = bytecode.ReadU16(code, ip), bytecode.ReadU16(code, ip+2)
		ip += 4
	}
	f.ip = ip
	vm.sp = sp
	if op == bytecode.OpCallV || op == bytecode.OpCallMethodV || op == bytecode.OpNewV {
		var err error
		if argc, err = vm.spread(); err != nil {
			return err
		}
	}
	switch op {
	case bytecode.OpCallMethod, bytecode.OpCallMethodV:
		recv := vm.stack[vm.sp-argc-1]
		c := &vm.cacheOf(f.fn)[cache]
		if recv.Kind() == value.Object && recv.AsObject().Class == c.class && c.method != nil {
			return vm.invoke(c.method, recv, argc) // the cached method of this class
		}
		m, err := value.Method(recv, f.fn.Consts[operand].AsString())
		if err != nil {
			return err
		}
		c.class, c.method, c.field = recv.AsObject().Class, m, -1 // a method entry has no data member slot
		return vm.invoke(m, recv, argc)
	case bytecode.OpCallDirect:
		return vm.callFunction(vm.prog.Funcs[operand], objectOf(vm.stack[vm.sp-argc-1]), nil, argc, false)
	case bytecode.OpCallNative: // cache holds the member function's name
		k := vm.globals[operand]
		name := f.fn.Consts[cache].AsString()
		if k.Kind() != value.Class || !k.AsClass().Native {
			return bad(f, "CALL_NATIVE of something that is not a native class")
		}
		m, ok := k.AsClass().Methods[name]
		if !ok || m.Native == nil {
			return value.Errorf(value.MemberError, "native class %s has no member function %s", k.AsClass().Name, name)
		}
		// The Go code expects its own kind of Go value in this.
		if this := objectOf(vm.stack[vm.sp-argc-1]); this == nil || !this.Class.IsSubclassOf(k.AsClass()) {
			return bad(f, "CALL_NATIVE on an object that is not a "+k.AsClass().Name)
		}
		return vm.invokeGo(m, vm.stack[vm.sp-argc-1], argc)
	case bytecode.OpNew, bytecode.OpNewV:
		return vm.newObject(argc)
	}
	return vm.callValue(argc)
}

// callFast sets up the frame of a call of a function or closure written in
// Kukel (callee below the argc arguments at sp; ip just past the
// instruction) in the common case, and reports whether it did; else
// callOp must make the call. The run loop reloads its state either way.
func (vm *VM) callFast(f *frame, ip, sp, argc int) bool {
	callee := vm.stack[sp-argc-1]
	switch callee.Kind() {
	case value.Function:
		return vm.enter(f, ip, sp, callee.Obj().(*bytecode.Function), nil, nil, argc)
	case value.Closure:
		cl := callee.AsClosure()
		return vm.enter(f, ip, sp, cl.Fn.(*bytecode.Function), cl.This, cl, argc)
	}
	return false
}

// callMethodFast is callFast for CALL_METHOD whose inline cache holds the
// method of the receiver's class, written in Kukel.
func (vm *VM) callMethodFast(f *frame, ip, sp, argc, cache int) bool {
	recv := vm.stack[sp-argc-1]
	if recv.Kind() != value.Object {
		return false
	}
	c := &vm.cacheOf(f.fn)[cache]
	if m := c.method; m != nil && recv.AsObject().Class == c.class && m.Fn != nil {
		var this *value.ObjectObj
		if !m.Static {
			this = recv.AsObject()
		}
		return vm.enter(f, ip, sp, m.Fn.(*bytecode.Function), this, nil, argc)
	}
	return false
}

// enter pushes the frame for fn as callFunction does, when nothing needs
// its slower checks: the number of arguments is right and there is no
// rest parameter, the stack and the frame list have room, there is no
// step limit to count and no cancellation has been asked for (step would
// then fail; a program that can be cancelled, as every one the Go API
// runs, still takes this path). It reports whether it did.
func (vm *VM) enter(f *frame, ip, sp int, fn *bytecode.Function, this *value.ObjectObj, cl *value.ClosureObj, argc int) bool {
	n := len(vm.frames)
	if fn.HasRest || argc < fn.NumParams || argc > fn.NumParams+fn.NumOptional || vm.StepLimit > 0 || vm.cancel != nil && vm.cancel.flag.Load() ||
		n >= MaxFrames || n == cap(vm.frames) || sp+fn.NumSlots+fn.MaxStack > len(vm.stack) {
		return false
	}
	if p := vm.prof; p != nil {
		if p.want.Load() {
			return false // callFunction takes the sample
		}
		p.count(fn)
	}
	base := sp - argc
	for i := sp; i < base+fn.NumSlots; i++ {
		vm.stack[i] = value.NilValue
	}
	f.ip = ip
	vm.sp = base + fn.NumSlots
	vm.frames = vm.frames[:n+1]
	fr := &vm.frames[n]
	fr.fn, fr.ip, fr.base, fr.argc, fr.this, fr.ctor, fr.cl = fn, 0, base, argc, this, false, cl
	return true
}

// callFunction pushes a frame for fn; its arguments are the top argc stack
// entries, below them is the callee's place.
func (vm *VM) callFunction(fn *bytecode.Function, this *value.ObjectObj, cl *value.ClosureObj, argc int, ctor bool) error {
	if err := vm.step(pcCaller); err != nil {
		return err
	}
	if s := vm.prof; s != nil {
		s.count(fn)
	}
	{
		maxArgs := fn.NumParams + fn.NumOptional
		if argc < fn.NumParams || (argc > maxArgs && !fn.HasRest) {
			return argCountError(fn, argc)
		}
		if len(vm.frames) >= MaxFrames {
			return value.Errorf(value.StackOverflow, "stack overflow (more than %d nested calls)", MaxFrames)
		}
		base := vm.sp - argc
		var rest []value.Value
		if fn.HasRest {
			rest = []value.Value{}
			if argc > maxArgs {
				rest = append(rest, vm.stack[base+maxArgs:vm.sp]...)
				vm.sp = base + maxArgs
			}
		}
		vm.ensure(fn.NumSlots + fn.MaxStack)
		for i := vm.sp; i < base+fn.NumSlots; i++ {
			vm.stack[i] = value.NilValue
		}
		if fn.HasRest {
			vm.stack[base+maxArgs] = value.MakeVector(rest)
		}
		vm.sp = base + fn.NumSlots
		// Fill the new frame in place (cheaper than building and copying one).
		n := len(vm.frames)
		if n == cap(vm.frames) {
			vm.frames = append(vm.frames, frame{})[:n]
		}
		vm.frames = vm.frames[:n+1]
		fr := &vm.frames[n]
		fr.fn, fr.ip, fr.base, fr.argc, fr.this, fr.ctor, fr.cl = fn, 0, base, argc, this, ctor, cl
		return nil
	}
}

func makeFunction(fn value.Named) value.Value { return value.MakeFunction(fn) }

// cacheField remembers the data member slot obj->name had (a method, or a
// value that is not an object, leaves the cache empty).
func cacheField(c *icEntry, obj value.Value, name string) {
	if obj.Kind() != value.Object {
		return
	}
	k := obj.AsObject().Class
	if i, ok := k.FieldIndex[name]; ok {
		c.class, c.method, c.field = k, nil, i
	} else {
		c.class, c.method, c.field = nil, nil, -1
	}
}

// icEntry is an inline cache of a member instruction: the class of the
// object it saw last, and what it found there.
type icEntry struct {
	class  *value.ClassObj
	method *value.MethodObj // CALL_METHOD
	field  int              // GET_MEMBER, SET_MEMBER: the data member's slot, or -1
}

// cacheOf returns the inline caches of a function.
func (vm *VM) cacheOf(fn *bytecode.Function) []icEntry {
	if fn.Index < 0 {
		return vm.initCache
	}
	return vm.caches[fn.Index]
}

// objectOf returns the object v holds, or nil.
func objectOf(v value.Value) *value.ObjectObj {
	if v.Kind() == value.Object {
		return v.AsObject()
	}
	return nil
}

// step counts a backward jump, a call or a throw against StepLimit.
// step counts a step for the step limit, and sees whether the program is
// cancelled or a sample is wanted (profiling); pc is where the top frame is
// (pcTop, pcCaller: see sample).
func (vm *VM) step(pc int) error {
	if !vm.watched {
		return nil
	}
	if s := vm.prof; s != nil && s.want.Load() {
		vm.sample(pc)
	}
	if vm.cancel != nil && vm.cancel.flag.Load() {
		return vm.Err()
	}
	if vm.StepLimit <= 0 {
		return nil
	}
	if vm.steps++; vm.steps > vm.StepLimit {
		return &RuntimeError{Kind: "StepLimitError", Msg: fmt.Sprintf("more than %d steps", vm.StepLimit),
			Trace: vm.trace(), Fatal: true}
	}
	return nil
}

// addTo is ADD_LOCAL's general case: *v = *v + x. (Out of the loop of
// run, which is faster with less code in it.)
//
//go:noinline
func addTo(v *value.Value, x value.Value) error {
	r, err := value.Arith(value.Add, *v, x)
	if err == nil {
		*v = r
	}
	return err
}

// bad reports bytecode that the verifier cannot rule out but that is wrong
// at run time (a value of the wrong kind in a slot, for example).
func bad(f *frame, what string) error {
	return value.Errorf(value.InternalError, "invalid bytecode in %s: %s", f.fn.FuncName, what)
}

// argCountError describes a call with the wrong number of arguments.
func argCountError(fn *bytecode.Function, argc int) error {
	switch {
	case fn.HasRest:
		return value.Errorf(value.ArgumentError, "%s expects at least %s, got %d", fn.FuncName, value.Arguments(fn.NumParams), argc)
	case fn.NumOptional > 0:
		return value.Errorf(value.ArgumentError, "%s expects %d to %d arguments, got %d",
			fn.FuncName, fn.NumParams, fn.NumParams+fn.NumOptional, argc)
	}
	return value.Errorf(value.ArgumentError, "%s expects %s, got %d", fn.FuncName, value.Arguments(fn.NumParams), argc)
}

// spread replaces the vector on top of the stack by its elements and
// returns how many there are.
func (vm *VM) spread() (int, error) {
	v := vm.stack[vm.sp-1]
	if v.Kind() != value.Vector {
		return 0, value.Errorf(value.TypeError, "cannot spread %s (a vector is needed)", v.Kind())
	}
	elems := v.AsVector().Elems
	if len(elems) > math.MaxUint16 {
		return 0, value.Errorf(value.ArgumentError, "too many arguments (%d) in a spread call", len(elems))
	}
	vm.sp--
	vm.ensure(len(elems))
	vm.sp += copy(vm.stack[vm.sp:], elems)
	return len(elems), nil
}

// trace returns the stack trace of the current frames. The innermost
// frame's ip must point at the current instruction.
func (vm *VM) trace() []TraceEntry {
	var t []TraceEntry
	for i := len(vm.frames) - 1; i >= 0; i-- {
		f := vm.frames[i]
		pc := f.ip
		if i < len(vm.frames)-1 {
			pc-- // a caller's ip is just past its CALL instruction
		}
		t = append(t, TraceEntry{Func: f.fn.FuncName, File: f.fn.File, Line: f.fn.LineAt(pc)})
	}
	return t
}

// wrap turns an error raised by an operation or built-in into a thrown
// exception: a runtime error becomes an Error object with message, kind and
// trace. An exception thrown inside a call made by a built-in passes through.
func (vm *VM) wrap(err error) error {
	if re, ok := err.(*RuntimeError); ok {
		return re
	}
	if ex, ok := err.(*value.ExitError); ok {
		return &RuntimeError{Kind: "Exit", Fatal: true, Exit: &ex.Code}
	}
	kind, msg := value.InternalError, err.Error()
	if ve, ok := err.(*value.Error); ok {
		kind, msg = ve.Kind, ve.Msg
	}
	obj := value.NewObject(vm.errorClass)
	vm.setField(obj, "message", value.MakeString(msg))
	vm.setField(obj, "kind", value.MakeString(kind))
	return vm.throwValue(obj)
}

// checkErrorObject checks an object new has made: an Error (or an object
// of a class derived from it) must have a string or nil as its message and
// kind.
func (vm *VM) checkErrorObject(o *value.ObjectObj) error {
	if !o.Class.IsSubclassOf(vm.errorClass) {
		return nil
	}
	for _, field := range [...]string{"message", "kind"} {
		if v := o.Fields[o.Class.FieldIndex[field]]; v.Kind() != value.Nil && v.Kind() != value.String {
			return value.Errorf(value.TypeError, "%s: the %s must be a string or nil, not %s", o.Class.Name, field, v.Kind())
		}
	}
	return nil
}

func (vm *VM) setField(obj value.Value, name string, v value.Value) {
	o := obj.AsObject()
	o.Fields[o.Class.FieldIndex[name]] = v
}

func (vm *VM) field(obj value.Value, name string) value.Value {
	o := obj.AsObject()
	return o.Fields[o.Class.FieldIndex[name]]
}

// throwValue makes the exception for "throw v". An Error object (of class
// Error or a class derived from it) gets its trace, and its kind if it has
// none (the name of its class), the first time it is thrown; throwing it
// again keeps the original trace.
// v is an Error object: THROW refuses anything else, and runtime errors
// are made into Error objects (wrap).
func (vm *VM) throwValue(v value.Value) *RuntimeError {
	re := &RuntimeError{Value: v}
	o := v.AsObject()
	if t, ok := o.Native.([]TraceEntry); ok {
		re.Trace = t
	} else {
		re.Trace = vm.trace()
		o.Native = re.Trace
		lines := make([]value.Value, len(re.Trace))
		for i, t := range re.Trace {
			lines[i] = value.MakeString(fmt.Sprintf("%s (%s:%d)", t.Func, t.File, t.Line))
		}
		vm.setField(v, "trace", value.MakeVector(lines))
	}
	if k := vm.field(v, "kind"); k.IsNil() || (k.Kind() == value.String && k.AsString() == "") {
		vm.setField(v, "kind", value.MakeString(o.Class.Name))
	}
	re.Kind = vm.field(v, "kind").String()
	if m := vm.field(v, "message"); !m.IsNil() {
		re.Msg = m.String()
	}
	return re
}

// execute runs frames until the frame count drops back to stopDepth, and
// returns the value the last frame returned. An exception goes to the
// innermost handler registered by one of these frames; without one, the
// frames are dropped and the exception is returned.
func (vm *VM) execute(stopDepth int) (value.Value, error) {
	vm.watched = vm.StepLimit > 0 || vm.cancel != nil || vm.prof != nil
	for {
		r, err := vm.run(stopDepth)
		if err == nil {
			return r, nil
		}
		re := err.(*RuntimeError)
		if !re.Fatal {
			if e := vm.step(pcTop); e != nil { // a throw/catch cycle is a loop too
				re = e.(*RuntimeError)
			}
		}
		if vm.DebugUncaught != nil && !re.Fatal && !slices.ContainsFunc(vm.handlers, func(h handler) bool { return !h.finally }) {
			if e := vm.DebugUncaught(vm, re); e != nil {
				re = vm.wrap(e).(*RuntimeError)
			}
		}
		if !vm.catch(re, stopDepth) {
			return value.NilValue, re
		}
	}
}

// catch hands an exception to the innermost handler that belongs to a frame
// at or above stopDepth. It reports whether there was one.
func (vm *VM) catch(re *RuntimeError, stopDepth int) bool {
	if n := len(vm.handlers); n > 0 && !re.Fatal {
		h := vm.handlers[n-1]
		if h.frame >= stopDepth {
			vm.handlers = vm.handlers[:n-1]
			vm.frames = vm.frames[:h.frame+1]
			vm.frames[h.frame].ip = h.pc
			vm.sp = h.sp
			v := re.Value
			if h.finally {
				v = value.MakeInternal(re)
			}
			vm.ensure(1)
			vm.stack[vm.sp] = v
			vm.sp++
			return true
		}
	}
	// Not caught here (or fatal): the frames above stopDepth are gone, and
	// so are their handlers, so that a VM used again (an embedded
	// interpreter after a step limit) has no stale ones.
	for n := len(vm.handlers); n > 0 && vm.handlers[n-1].frame >= stopDepth; n-- {
		vm.handlers = vm.handlers[:n-1]
	}
	vm.frames = vm.frames[:stopDepth]
	return false
}

// fail records the position of the failing instruction and the stack
// depth, and returns the exception. (A method, not a closure in run: a
// closure capturing sp and f would keep them out of registers.)
func (vm *VM) fail(f *frame, start, sp int, e error) (value.Value, error) {
	f.ip = start
	vm.sp = sp
	return value.NilValue, vm.wrap(e)
}

// run executes the top frame (and the frames it calls) until the frame
// count drops back to stopDepth, or an exception is thrown. It returns the
// exception without unwinding.
func (vm *VM) run(stopDepth int) (result value.Value, err error) {
	f := &vm.frames[len(vm.frames)-1]
	code, consts, base := f.fn.Code, f.fn.Consts, f.base
	ip := f.ip
	stack := vm.stack
	sp := vm.sp

	for {
		start := ip
		op := bytecode.Op(code[ip])
		ip++
	dispatch:
		switch op {
		case bytecode.OpNop:

		case bytecode.OpBreak: // a line start in code the debugger copied
			if vm.Debug != nil {
				f.ip, vm.sp = start, sp // for the hook's look at the frames
				if e := vm.Debug(vm); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			op = f.fn.Saved[start]
			goto dispatch

		case bytecode.OpConst:
			stack[sp] = consts[bytecode.ReadU16(code, ip)]
			ip += 2
			sp++
		case bytecode.OpNil:
			stack[sp] = value.NilValue
			sp++
		case bytecode.OpTrue:
			stack[sp] = value.True
			sp++
		case bytecode.OpFalse:
			stack[sp] = value.False
			sp++
		case bytecode.OpPop:
			sp--
		case bytecode.OpDup:
			stack[sp] = stack[sp-1]
			sp++
		case bytecode.OpDup2:
			stack[sp] = stack[sp-2]
			stack[sp+1] = stack[sp-1]
			sp += 2

		case bytecode.OpLoadLocal:
			stack[sp] = stack[base+bytecode.ReadU16(code, ip)]
			ip += 2
			sp++
		case bytecode.OpStoreLocal:
			sp--
			stack[base+bytecode.ReadU16(code, ip)] = stack[sp]
			ip += 2
		case bytecode.OpLoadGlobal:
			stack[sp] = vm.globals[bytecode.ReadU16(code, ip)]
			ip += 2
			sp++
		case bytecode.OpStoreGlobal:
			sp--
			vm.globals[bytecode.ReadU16(code, ip)] = stack[sp]
			ip += 2

		case bytecode.OpAdd, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod,
			bytecode.OpBitAnd, bytecode.OpBitOr, bytecode.OpBitXor, bytecode.OpShl, bytecode.OpShr:
			a, b := stack[sp-2], stack[sp-1]
			var r value.Value
			if a.Kind() == value.Int && b.Kind() == value.Int && op <= bytecode.OpMul {
				// fast path for the common integer cases
				x, y := a.AsInt(), b.AsInt()
				switch op {
				case bytecode.OpAdd:
					r = value.MakeInt(x + y)
				case bytecode.OpSub:
					r = value.MakeInt(x - y)
				default:
					r = value.MakeInt(x * y)
				}
			} else {
				var e error
				if r, e = value.Arith(value.ArithOp(op-bytecode.OpAdd), a, b); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			sp--
			stack[sp-1] = r
		case bytecode.OpNeg:
			r, e := value.Neg(stack[sp-1])
			if e != nil {
				return vm.fail(f, start, sp, e)
			}
			stack[sp-1] = r
		case bytecode.OpNot:
			stack[sp-1] = value.MakeBool(!stack[sp-1].Truthy())
		case bytecode.OpBitNot:
			r, e := value.BitNot(stack[sp-1])
			if e != nil {
				return vm.fail(f, start, sp, e)
			}
			stack[sp-1] = r
		case bytecode.OpEq:
			sp--
			stack[sp-1] = value.MakeBool(value.Equal(stack[sp-1], stack[sp]))
		case bytecode.OpNe:
			sp--
			stack[sp-1] = value.MakeBool(!value.Equal(stack[sp-1], stack[sp]))
		case bytecode.OpLt, bytecode.OpLe, bytecode.OpGt, bytecode.OpGe:
			a, b := stack[sp-2], stack[sp-1]
			var r bool
			if a.Kind() == value.Int && b.Kind() == value.Int {
				x, y := a.AsInt(), b.AsInt()
				switch op {
				case bytecode.OpLt:
					r = x < y
				case bytecode.OpLe:
					r = x <= y
				case bytecode.OpGt:
					r = x > y
				default:
					r = x >= y
				}
			} else {
				var e error
				if r, e = value.Compare(value.CmpOp(op-bytecode.OpLt), a, b); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			sp--
			stack[sp-1] = value.MakeBool(r)
		case bytecode.OpInc, bytecode.OpDec:
			delta := int64(1)
			if op == bytecode.OpDec {
				delta = -1
			}
			r, e := value.IncDec(stack[sp-1], delta)
			if e != nil {
				return vm.fail(f, start, sp, e)
			}
			stack[sp-1] = r

		case bytecode.OpJump:
			if t := bytecode.ReadU32(code, ip); t < ip {
				if e := vm.step(start); e != nil {
					return vm.fail(f, start, sp, e)
				}
				ip = t
			} else {
				ip = t
			}
		case bytecode.OpJumpIfFalse:
			sp--
			if !stack[sp].Truthy() {
				t := bytecode.ReadU32(code, ip)
				if t < ip {
					if e := vm.step(start); e != nil {
						return vm.fail(f, start, sp, e)
					}
				}
				ip = t
			} else {
				ip += 4
			}
		case bytecode.OpJumpIfTrue:
			sp--
			if stack[sp].Truthy() {
				t := bytecode.ReadU32(code, ip)
				if t < ip {
					if e := vm.step(start); e != nil {
						return vm.fail(f, start, sp, e)
					}
				}
				ip = t
			} else {
				ip += 4
			}
		case bytecode.OpJumpIfArg:
			if bytecode.ReadU16(code, ip) < f.argc {
				t := bytecode.ReadU32(code, ip+2)
				if t < ip {
					if e := vm.step(start); e != nil {
						return vm.fail(f, start, sp, e)
					}
				}
				ip = t
			} else {
				ip += 6
			}

		case bytecode.OpMakeVector:
			n := bytecode.ReadU16(code, ip)
			ip += 2
			elems := make([]value.Value, n)
			copy(elems, stack[sp-n:sp])
			sp -= n
			stack[sp] = value.MakeVector(elems)
			sp++
		case bytecode.OpAppend:
			if stack[sp-2].Kind() != value.Vector {
				return vm.fail(f, start, sp, bad(f, "APPEND without a vector"))
			}
			vec := stack[sp-2].AsVector() // always a fresh vector made by the compiler's code
			vec.Elems = append(vec.Elems, stack[sp-1])
			sp--
		case bytecode.OpExtend:
			x := stack[sp-1]
			if x.Kind() != value.Vector {
				return vm.fail(f, start, sp, value.Errorf(value.TypeError, "cannot spread %s (a vector is needed)", x.Kind()))
			}
			if stack[sp-2].Kind() != value.Vector {
				return vm.fail(f, start, sp, bad(f, "EXTEND without a vector"))
			}
			vec := stack[sp-2].AsVector()
			vec.Elems = append(vec.Elems, x.AsVector().Elems...)
			sp--

		case bytecode.OpMakeMap:
			n := bytecode.ReadU16(code, ip)
			ip += 2
			m := value.NewMap()
			for i := sp - 2*n; i < sp; i += 2 {
				if e := m.Set(stack[i], stack[i+1]); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			sp -= 2 * n
			stack[sp] = value.MakeMap(m)
			sp++
		case bytecode.OpIndexGet:
			r, e := value.Index(stack[sp-2], stack[sp-1])
			if e != nil {
				return vm.fail(f, start, sp, e)
			}
			sp--
			stack[sp-1] = r
		case bytecode.OpIndexLL:
			r, e := value.Index(stack[base+bytecode.ReadU16(code, ip)], stack[base+bytecode.ReadU16(code, ip+2)])
			if e != nil {
				return vm.fail(f, start, sp, e)
			}
			ip += 4
			stack[sp] = r
			sp++
		case bytecode.OpIndexSet:
			if e := value.SetIndex(stack[sp-3], stack[sp-2], stack[sp-1]); e != nil {
				return vm.fail(f, start, sp, e)
			}
			stack[sp-3] = stack[sp-1]
			sp -= 2
		case bytecode.OpIterInit:
			it, e := value.NewIterator(stack[sp-1])
			if e != nil {
				return vm.fail(f, start, sp, e)
			}
			stack[sp-1] = it
		case bytecode.OpIterNext:
			it := stack[base+bytecode.ReadU16(code, ip)]
			if it.Kind() != value.Iterator {
				return vm.fail(f, start, sp, bad(f, "ITER_NEXT without an iterator"))
			}
			n := bytecode.ReadU16(code, ip+2)
			first, second, ok := it.Next()
			if !ok {
				t := bytecode.ReadU32(code, ip+4)
				if t < ip {
					if e := vm.step(start); e != nil {
						return vm.fail(f, start, sp, e)
					}
				}
				ip = t
				break
			}
			ip += 8
			switch {
			case n == 2:
				stack[sp], stack[sp+1] = first, second
				sp += 2
			case it.OneNameGetsKey():
				stack[sp] = first
				sp++
			default:
				stack[sp] = second
				sp++
			}

		case bytecode.OpLoadThis:
			if f.this != nil {
				stack[sp] = value.ObjectValue(f.this)
			} else {
				stack[sp] = value.NilValue
			}
			sp++
		case bytecode.OpLoadMember, bytecode.OpStoreMember:
			if f.this == nil {
				return vm.fail(f, start, sp, value.Errorf(value.InternalError, "%s has no object", f.fn.FuncName))
			}
			fields := f.this.Fields
			i := bytecode.ReadU16(code, ip)
			ip += 2
			if i >= len(fields) {
				return vm.fail(f, start, sp, bad(f, "data member slot out of range"))
			}
			if op == bytecode.OpLoadMember {
				stack[sp] = fields[i]
				sp++
			} else {
				sp--
				fields[i] = stack[sp]
			}
		case bytecode.OpGetMember:
			obj := stack[sp-1]
			c := &vm.cacheOf(f.fn)[bytecode.ReadU16(code, ip+2)]
			if obj.Kind() == value.Object {
				if o := obj.AsObject(); o.Class == c.class && c.field >= 0 {
					stack[sp-1] = o.Fields[c.field] // the cached data member slot
					ip += 4
					break
				}
			}
			name := consts[bytecode.ReadU16(code, ip)].AsString()
			r, e := value.Member(obj, name, makeFunction)
			if e != nil {
				return vm.fail(f, start, sp, e)
			}
			cacheField(c, obj, name)
			ip += 4
			stack[sp-1] = r
		case bytecode.OpGetMemberL:
			obj := stack[base+bytecode.ReadU16(code, ip)]
			c := &vm.cacheOf(f.fn)[bytecode.ReadU16(code, ip+4)]
			if obj.Kind() == value.Object {
				if o := obj.AsObject(); o.Class == c.class && c.field >= 0 {
					stack[sp] = o.Fields[c.field] // the cached data member slot
					sp++
					ip += 6
					break
				}
			}
			name := consts[bytecode.ReadU16(code, ip+2)].AsString()
			r, e := value.Member(obj, name, makeFunction)
			if e != nil {
				return vm.fail(f, start, sp, e)
			}
			cacheField(c, obj, name)
			ip += 6
			stack[sp] = r
			sp++
		case bytecode.OpSetMember:
			obj := stack[sp-2]
			c := &vm.cacheOf(f.fn)[bytecode.ReadU16(code, ip+2)]
			if obj.Kind() == value.Object && obj.AsObject().Class == c.class && c.field >= 0 {
				obj.AsObject().Fields[c.field] = stack[sp-1]
			} else {
				name := consts[bytecode.ReadU16(code, ip)].AsString()
				if e := value.SetMember(obj, name, stack[sp-1]); e != nil {
					return vm.fail(f, start, sp, e)
				}
				cacheField(c, obj, name)
			}
			ip += 4
			stack[sp-2] = stack[sp-1]
			sp--
		case bytecode.OpLoadFunc:
			stack[sp] = value.MakeFunction(vm.prog.Funcs[bytecode.ReadU16(code, ip)])
			ip += 2
			sp++
		case bytecode.OpLoadStatic:
			stack[sp] = vm.classes[bytecode.ReadU16(code, ip)].Statics[bytecode.ReadU16(code, ip+2)]
			ip += 4
			sp++
		case bytecode.OpStoreStatic:
			sp--
			vm.classes[bytecode.ReadU16(code, ip)].Statics[bytecode.ReadU16(code, ip+2)] = stack[sp]
			ip += 4

		case bytecode.OpCall, bytecode.OpCallV, bytecode.OpCallMethod, bytecode.OpCallMethodV,
			bytecode.OpCallDirect, bytecode.OpCallNative, bytecode.OpNew, bytecode.OpNewV:
			// The common calls first, the others (and what the fast ones
			// do not handle) through callOp.
			if !(op == bytecode.OpCall && vm.callFast(f, ip+2, sp, bytecode.ReadU16(code, ip)) ||
				op == bytecode.OpCallMethod && vm.callMethodFast(f, ip+6, sp, bytecode.ReadU16(code, ip+2), bytecode.ReadU16(code, ip+4))) {
				if e := vm.callOp(op, f, ip, sp); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			// A built-in has finished; a function has a new frame.
			stack, sp = vm.stack, vm.sp
			f = &vm.frames[len(vm.frames)-1]
			code, consts, base, ip = f.fn.Code, f.fn.Consts, f.base, f.ip

		case bytecode.OpReturn, bytecode.OpReturnNil:
			r := value.NilValue
			if f.ctor {
				r = value.ObjectValue(f.this) // new C(...) gives the object
				if e := vm.checkErrorObject(f.this); e != nil {
					return vm.fail(f, start, sp, e)
				}
			} else if op == bytecode.OpReturn {
				r = stack[sp-1]
			}
			sp = f.base - 1 // drop the slots, the operands and the callee
			stack[sp] = r
			sp++
			vm.frames = vm.frames[:len(vm.frames)-1]
			for n := len(vm.handlers); n > 0 && vm.handlers[n-1].frame >= len(vm.frames); n-- {
				vm.handlers = vm.handlers[:n-1] // (the compiler pops them; this is a safety net)
			}
			if len(vm.frames) == stopDepth {
				vm.sp = sp - 1
				return r, nil
			}
			f = &vm.frames[len(vm.frames)-1]
			code, consts, base, ip = f.fn.Code, f.fn.Consts, f.base, f.ip

		case bytecode.OpNewCell:
			sp--
			stack[base+bytecode.ReadU16(code, ip)] = value.NewCell(stack[sp])
			ip += 2
		case bytecode.OpLoadCell:
			if stack[base+bytecode.ReadU16(code, ip)].Kind() != value.Cell {
				return vm.fail(f, start, sp, bad(f, "LOAD_CELL without a cell"))
			}
			stack[sp] = stack[base+bytecode.ReadU16(code, ip)].AsCell().V
			ip += 2
			sp++
		case bytecode.OpStoreCell:
			if stack[base+bytecode.ReadU16(code, ip)].Kind() != value.Cell {
				return vm.fail(f, start, sp, bad(f, "STORE_CELL without a cell"))
			}
			sp--
			stack[base+bytecode.ReadU16(code, ip)].AsCell().V = stack[sp]
			ip += 2
		case bytecode.OpLoadUpvalue:
			if f.cl == nil || bytecode.ReadU16(code, ip) >= len(f.cl.Cells) {
				return vm.fail(f, start, sp, bad(f, "upvalue out of range"))
			}
			stack[sp] = f.cl.Cells[bytecode.ReadU16(code, ip)].V
			ip += 2
			sp++
		case bytecode.OpStoreUpvalue:
			if f.cl == nil || bytecode.ReadU16(code, ip) >= len(f.cl.Cells) {
				return vm.fail(f, start, sp, bad(f, "upvalue out of range"))
			}
			sp--
			f.cl.Cells[bytecode.ReadU16(code, ip)].V = stack[sp]
			ip += 2
		case bytecode.OpClosure:
			fn := vm.prog.Funcs[bytecode.ReadU16(code, ip)]
			ip += 2
			cells := make([]*value.CellObj, len(fn.Upvalues))
			for i, u := range fn.Upvalues {
				switch {
				case u.FromLocal && u.Index < f.fn.NumSlots && stack[base+u.Index].Kind() == value.Cell:
					cells[i] = stack[base+u.Index].AsCell()
				case !u.FromLocal && f.cl != nil && u.Index < len(f.cl.Cells):
					cells[i] = f.cl.Cells[u.Index]
				default:
					return vm.fail(f, start, sp, bad(f, "closure captures a variable that is not there"))
				}
			}
			stack[sp] = value.MakeClosure(&value.ClosureObj{Fn: fn, Cells: cells, This: f.this})
			sp++

		case bytecode.OpCmpJump:
			a, b := stack[sp-2], stack[sp-1]
			sp -= 2
			k := bytecode.ReadU16(code, ip)
			var r bool
			if a.Kind() == value.Int && b.Kind() == value.Int {
				x, y := a.AsInt(), b.AsInt()
				switch k &^ bytecode.CmpJumpIfTrue {
				case bytecode.CmpLt:
					r = x < y
				case bytecode.CmpLe:
					r = x <= y
				case bytecode.CmpGt:
					r = x > y
				case bytecode.CmpGe:
					r = x >= y
				case bytecode.CmpEq:
					r = x == y
				default:
					r = x != y
				}
			} else if c := k &^ bytecode.CmpJumpIfTrue; c >= bytecode.CmpEq {
				r = value.Equal(a, b) == (c == bytecode.CmpEq)
			} else {
				var e error
				if r, e = value.Compare(value.CmpOp(c), a, b); e != nil {
					return vm.fail(f, start, sp+2, e)
				}
			}
			if r == (k&bytecode.CmpJumpIfTrue != 0) {
				t := bytecode.ReadU32(code, ip+2)
				if t < ip {
					if e := vm.step(start); e != nil {
						return vm.fail(f, start, sp, e)
					}
				}
				ip = t
			} else {
				ip += 6
			}
		case bytecode.OpArithLK:
			aop := bytecode.Op(bytecode.ReadU16(code, ip))
			a, b := stack[base+bytecode.ReadU16(code, ip+2)], consts[bytecode.ReadU16(code, ip+4)]
			var r value.Value
			if a.Kind() == value.Int && b.Kind() == value.Int && aop <= bytecode.OpMul {
				x, y := a.AsInt(), b.AsInt()
				switch aop {
				case bytecode.OpAdd:
					r = value.MakeInt(x + y)
				case bytecode.OpSub:
					r = value.MakeInt(x - y)
				default:
					r = value.MakeInt(x * y)
				}
			} else {
				var e error
				if r, e = value.Arith(value.ArithOp(aop-bytecode.OpAdd), a, b); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			stack[sp] = r
			sp++
			ip += 6
		case bytecode.OpArithK: // as ARITH_LK, the left operand on the stack
			aop := bytecode.Op(bytecode.ReadU16(code, ip))
			a, b := stack[sp-1], consts[bytecode.ReadU16(code, ip+2)]
			var r value.Value
			if a.Kind() == value.Int && b.Kind() == value.Int && aop <= bytecode.OpMul {
				x, y := a.AsInt(), b.AsInt()
				switch aop {
				case bytecode.OpAdd:
					r = value.MakeInt(x + y)
				case bytecode.OpSub:
					r = value.MakeInt(x - y)
				default:
					r = value.MakeInt(x * y)
				}
			} else {
				var e error
				if r, e = value.Arith(value.ArithOp(aop-bytecode.OpAdd), a, b); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			stack[sp-1] = r
			ip += 4
		case bytecode.OpArithLL: // as ARITH_LK, the right operand a local
			aop := bytecode.Op(bytecode.ReadU16(code, ip))
			a, b := stack[base+bytecode.ReadU16(code, ip+2)], stack[base+bytecode.ReadU16(code, ip+4)]
			var r value.Value
			if a.Kind() == value.Int && b.Kind() == value.Int && aop <= bytecode.OpMul {
				x, y := a.AsInt(), b.AsInt()
				switch aop {
				case bytecode.OpAdd:
					r = value.MakeInt(x + y)
				case bytecode.OpSub:
					r = value.MakeInt(x - y)
				default:
					r = value.MakeInt(x * y)
				}
			} else {
				var e error
				if r, e = value.Arith(value.ArithOp(aop-bytecode.OpAdd), a, b); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			stack[sp] = r
			sp++
			ip += 6
		case bytecode.OpCmpJumpLK:
			k := bytecode.ReadU16(code, ip)
			a, b := stack[base+bytecode.ReadU16(code, ip+2)], consts[bytecode.ReadU16(code, ip+4)]
			var r bool
			if a.Kind() == value.Int && b.Kind() == value.Int {
				x, y := a.AsInt(), b.AsInt()
				switch k &^ bytecode.CmpJumpIfTrue {
				case bytecode.CmpLt:
					r = x < y
				case bytecode.CmpLe:
					r = x <= y
				case bytecode.CmpGt:
					r = x > y
				case bytecode.CmpGe:
					r = x >= y
				case bytecode.CmpEq:
					r = x == y
				default:
					r = x != y
				}
			} else if c := k &^ bytecode.CmpJumpIfTrue; c >= bytecode.CmpEq {
				r = value.Equal(a, b) == (c == bytecode.CmpEq)
			} else {
				var e error
				if r, e = value.Compare(value.CmpOp(c), a, b); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			if r == (k&bytecode.CmpJumpIfTrue != 0) {
				t := bytecode.ReadU32(code, ip+6)
				if t < ip {
					if e := vm.step(start); e != nil {
						return vm.fail(f, start, sp, e)
					}
				}
				ip = t
			} else {
				ip += 10
			}
		case bytecode.OpCmpJumpK: // as CMP_JUMP_LK, the left operand popped
			k := bytecode.ReadU16(code, ip)
			a, b := stack[sp-1], consts[bytecode.ReadU16(code, ip+2)]
			sp--
			var r bool
			if a.Kind() == value.Int && b.Kind() == value.Int {
				x, y := a.AsInt(), b.AsInt()
				switch k &^ bytecode.CmpJumpIfTrue {
				case bytecode.CmpLt:
					r = x < y
				case bytecode.CmpLe:
					r = x <= y
				case bytecode.CmpGt:
					r = x > y
				case bytecode.CmpGe:
					r = x >= y
				case bytecode.CmpEq:
					r = x == y
				default:
					r = x != y
				}
			} else if c := k &^ bytecode.CmpJumpIfTrue; c >= bytecode.CmpEq {
				r = value.Equal(a, b) == (c == bytecode.CmpEq)
			} else {
				var e error
				if r, e = value.Compare(value.CmpOp(c), a, b); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			if r == (k&bytecode.CmpJumpIfTrue != 0) {
				t := bytecode.ReadU32(code, ip+4)
				if t < ip {
					if e := vm.step(start); e != nil {
						return vm.fail(f, start, sp, e)
					}
				}
				ip = t
			} else {
				ip += 8
			}
		case bytecode.OpCmpJumpLL: // as CMP_JUMP_LK, the right operand a local
			k := bytecode.ReadU16(code, ip)
			a, b := stack[base+bytecode.ReadU16(code, ip+2)], stack[base+bytecode.ReadU16(code, ip+4)]
			var r bool
			if a.Kind() == value.Int && b.Kind() == value.Int {
				x, y := a.AsInt(), b.AsInt()
				switch k &^ bytecode.CmpJumpIfTrue {
				case bytecode.CmpLt:
					r = x < y
				case bytecode.CmpLe:
					r = x <= y
				case bytecode.CmpGt:
					r = x > y
				case bytecode.CmpGe:
					r = x >= y
				case bytecode.CmpEq:
					r = x == y
				default:
					r = x != y
				}
			} else if c := k &^ bytecode.CmpJumpIfTrue; c >= bytecode.CmpEq {
				r = value.Equal(a, b) == (c == bytecode.CmpEq)
			} else {
				var e error
				if r, e = value.Compare(value.CmpOp(c), a, b); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			if r == (k&bytecode.CmpJumpIfTrue != 0) {
				t := bytecode.ReadU32(code, ip+6)
				if t < ip {
					if e := vm.step(start); e != nil {
						return vm.fail(f, start, sp, e)
					}
				}
				ip = t
			} else {
				ip += 10
			}
		case bytecode.OpIncLocal, bytecode.OpDecLocal:
			v := &stack[base+bytecode.ReadU16(code, ip)]
			delta := int64(1)
			if op == bytecode.OpDecLocal {
				delta = -1
			}
			if v.Kind() == value.Int {
				*v = value.MakeInt(v.AsInt() + delta)
			} else {
				r, e := value.IncDec(*v, delta)
				if e != nil {
					return vm.fail(f, start, sp, e)
				}
				*v = r
			}
			ip += 2

		case bytecode.OpAddLocal:
			sp--
			v := &stack[base+bytecode.ReadU16(code, ip)]
			if v.Kind() == value.Int && stack[sp].Kind() == value.Int {
				*v = value.MakeInt(v.AsInt() + stack[sp].AsInt())
			} else if e := addTo(v, stack[sp]); e != nil {
				return vm.fail(f, start, sp+1, e)
			}
			ip += 2

		case bytecode.OpSwitchTable:
			sp--
			x, t := stack[sp], &f.fn.Tables[bytecode.ReadU16(code, ip)]
			target := -1
			if n, ok := x.IntValue(); ok {
				if i := uint64(n) - uint64(t.Min); i < uint64(len(t.Targets)) {
					target = t.Targets[i]
				}
			}
			if target < 0 {
				target = bytecode.ReadU32(code, ip+2)
			}
			if target < ip {
				if e := vm.step(start); e != nil {
					return vm.fail(f, start, sp, e)
				}
			}
			ip = target

		case bytecode.OpTryPush:
			vm.handlers = append(vm.handlers, handler{
				frame:   len(vm.frames) - 1,
				pc:      bytecode.ReadU32(code, ip+2),
				sp:      sp,
				finally: bytecode.ReadU16(code, ip) == 1,
			})
			ip += 6
		case bytecode.OpTryPop:
			if n := len(vm.handlers); n == 0 || vm.handlers[n-1].frame != len(vm.frames)-1 {
				return vm.fail(f, start, sp, bad(f, "TRY_POP without a handler"))
			}
			vm.handlers = vm.handlers[:len(vm.handlers)-1]
		case bytecode.OpThrow:
			sp--
			if v := stack[sp]; v.Kind() != value.Object || !v.AsObject().Class.IsSubclassOf(vm.errorClass) {
				return vm.fail(f, start, sp, value.Errorf(value.TypeError, "only Error objects can be thrown, not %s (throw new Error(message, kind))", v.Kind()))
			}
			f.ip = start
			vm.sp = sp
			return value.NilValue, vm.throwValue(stack[sp])
		case bytecode.OpRethrow:
			sp--
			re, ok := stack[sp].Obj().(*RuntimeError)
			if stack[sp].Kind() != value.Internal || !ok {
				return vm.fail(f, start, sp, bad(f, "RETHROW without a pending exception"))
			}
			vm.sp = sp
			return value.NilValue, re

		default:
			return vm.fail(f, start, sp, value.Errorf(value.InternalError, "bad opcode %d at %d in %s", op, start, f.fn.FuncName))
		}
	}
}

// ----------------------------------------------------------------------
// For the debugger

// DebugFrame is a call the program is in, for the debugger.
type DebugFrame struct {
	Func *bytecode.Function
	Line int              // the line it is at (where it called the next frame)
	This *value.ObjectObj // the object of a member function call, or nil
}

// Frames gives the calls the program is in, outermost first; the last is
// the one at the BREAK the debug hook was called for.
func (vm *VM) Frames() []DebugFrame {
	out := make([]DebugFrame, len(vm.frames))
	for i, f := range vm.frames {
		out[i] = DebugFrame{Func: f.fn, Line: f.fn.LineAt(vm.framePC(i)), This: f.this}
	}
	return out
}

// framePC is the pc frame i is at.
func (vm *VM) framePC(i int) int {
	pc := vm.frames[i].ip
	if i < len(vm.frames)-1 && pc > 0 {
		pc-- // a caller's ip is past its call instruction
	}
	return pc
}

// DebugVar is a variable of a frame: a parameter, local or captured one.
type DebugVar struct {
	Name  string
	Value value.Value
}

// Vars gives the variables of frame i (an index into Frames): the
// parameters and locals in scope where it is, by their slots, then captured
// variables. A function without Locals (from a .kkc file) gives all its
// slots by SlotNames (a slot that two blocks share has both names).
func (vm *VM) Vars(i int) []DebugVar {
	f := vm.frames[i]
	var vars []DebugVar
	for _, l := range vm.slots(i) {
		v := vm.stack[f.base+l.Slot]
		if v.Kind() == value.Cell {
			v = v.AsCell().V
		}
		vars = append(vars, DebugVar{l.Name, v})
	}
	if f.cl != nil {
		for j, u := range f.fn.Upvalues {
			if j < len(f.cl.Cells) {
				vars = append(vars, DebugVar{u.Name, f.cl.Cells[j].V})
			}
		}
	}
	return vars
}

// SetVar gives the variable name of frame i (as Vars names it; of two
// with that name, the inner one) the value v, and reports whether there
// is one.
func (vm *VM) SetVar(i int, name string, v value.Value) bool {
	f := vm.frames[i]
	if f.cl != nil {
		for j := len(f.fn.Upvalues) - 1; j >= 0; j-- {
			if f.fn.Upvalues[j].Name == name && j < len(f.cl.Cells) {
				f.cl.Cells[j].V = v
				return true
			}
		}
	}
	live := vm.slots(i)
	for j := len(live) - 1; j >= 0; j-- {
		if live[j].Name == name {
			at := &vm.stack[f.base+live[j].Slot]
			if at.Kind() == value.Cell {
				at.AsCell().V = v
			} else {
				*at = v
			}
			return true
		}
	}
	return false
}

// slots gives the named slots of frame i in scope where it is, by slot.
func (vm *VM) slots(i int) []bytecode.LocalInfo {
	f := vm.frames[i]
	var live []bytecode.LocalInfo
	if f.fn.Locals != nil {
		pc := vm.framePC(i)
		for _, l := range f.fn.Locals {
			if l.Start <= pc && pc < l.End {
				live = append(live, l)
			}
		}
		slices.SortFunc(live, func(a, b bytecode.LocalInfo) int { return a.Slot - b.Slot })
		return live
	}
	for s := 0; s < f.fn.NumSlots && s < len(f.fn.SlotNames); s++ {
		if name := f.fn.SlotNames[s]; name != "" && name[0] != '(' { // not (switch), (iterator), (temp), ...
			live = append(live, bytecode.LocalInfo{Name: name, Slot: s})
		}
	}
	return live
}
