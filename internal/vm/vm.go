// Package vm executes compiled Kukel programs on a stack machine.
//
// Each call has a frame. A frame's slots (parameters, temporaries, locals)
// sit on the value stack at frame.base, followed by its operand stack. The
// callee value sits just below the slots.
package vm

import (
	"fmt"
	"io"
	"math"
	"strings"

	"kukel/internal/bytecode"
	"kukel/internal/value"
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
	Value value.Value  // the thrown value (an Error object for runtime errors)
	Kind  string       // the kind of an Error object; "" for other values
	Msg   string       // the message of an Error object, or the value as text
	Trace []TraceEntry // innermost call first
	Fatal bool         // try cannot catch it (the step limit)
}

// Error returns the message and the stack trace. Long traces (deep
// recursion) show only the first and last traceEnds calls.
func (e *RuntimeError) Error() string {
	const traceEnds = 10
	var b strings.Builder
	switch {
	case e.Kind == "":
		fmt.Fprintf(&b, "uncaught exception: %s", e.Msg)
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
	ip   int              // next instruction (saved while the frame calls another)
	base int              // first slot
	argc int              // arguments passed (for JUMP_IF_ARG)
	this value.Value      // the object of a member function call
	ctor bool             // a constructor called by new: it returns this
	up   []*value.CellObj // a closure's captured variables
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
	handlers   []handler
	errorClass *value.ClassObj // the prelude's Error class

	// StepLimit, when positive, stops the program with a fatal error after
	// that many backward jumps and calls (for tests that run untrusted code).
	StepLimit int
	steps     int
	stack     []value.Value
	sp        int // first free stack entry
	frames    []frame
	stdout    io.Writer
	stdin     io.Reader
}

// Builtins resolves built-in functions by name.
type Builtins func(name string) (*value.BuiltinObj, bool)

// New prepares prog for running: it binds the functions and built-ins to
// their global slots.
func New(prog *bytecode.Program, builtins Builtins, stdout io.Writer, stdin io.Reader) (*VM, error) {
	vm := &VM{
		prog:    prog,
		globals: make([]value.Value, len(prog.Globals)),
		stack:   make([]value.Value, 1024),
		stdout:  stdout,
		stdin:   stdin,
	}
	for i, g := range prog.Globals {
		switch g.Kind {
		case bytecode.GlobalFunc:
			if g.Func < 0 || g.Func >= len(prog.Funcs) {
				return nil, fmt.Errorf("global %s: bad function index %d", g.Name, g.Func)
			}
			vm.globals[i] = value.MakeFunction(prog.Funcs[g.Func])
		case bytecode.GlobalBuiltin:
			b, ok := builtins(g.Name)
			if !ok {
				return nil, fmt.Errorf("unknown built-in function %s", g.Name)
			}
			vm.globals[i] = value.MakeBuiltin(b)
		}
	}
	if err := vm.makeClasses(); err != nil {
		return nil, err
	}
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
	for i, g := range prog.Globals {
		if g.Kind == bytecode.GlobalClass {
			if g.Class < 0 || g.Class >= len(vm.classes) {
				return nil, fmt.Errorf("global %s: bad class index %d", g.Name, g.Class)
			}
			vm.globals[i] = value.MakeClass(vm.classes[g.Class])
		}
	}
	return vm, nil
}

// makeClasses builds the run-time classes: an object's data members are the
// base class's followed by the class's own; the method table holds the
// inherited member functions with the class's own ones overriding them.
func (vm *VM) makeClasses() error {
	for i, c := range vm.prog.Classes {
		k := &value.ClassObj{
			Name:       c.Name,
			FieldIndex: map[string]int{},
			Methods:    map[string]*value.MethodObj{},
			Statics:    make([]value.Value, len(c.Statics)),
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
			if m.Name == c.Name && !m.Static {
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

// Stdout, Stdin and Call implement value.Env for the built-ins.
func (vm *VM) Stdout() io.Writer { return vm.stdout }
func (vm *VM) Stdin() io.Reader  { return vm.stdin }

// Call calls fn from a built-in (sort's comparator, for example). The
// built-in's own arguments stay on the stack below the new call.
func (vm *VM) Call(fn value.Value, args []value.Value) (value.Value, error) {
	return vm.call(fn, args)
}

// Run runs the global initializers, then main(args). It returns main's
// result as the exit code (an int result; anything else is 0).
func (vm *VM) Run(args []string) (int, error) {
	if _, err := vm.call(value.MakeFunction(vm.prog.Init), nil); err != nil {
		return 1, err
	}
	main := vm.globals[vm.prog.Main]
	var mainArgs []value.Value
	if f, ok := main.Obj().(*bytecode.Function); ok && f.ParamSlots() > 0 {
		elems := make([]value.Value, len(args))
		for i, a := range args {
			elems[i] = value.MakeString(a)
		}
		mainArgs = []value.Value{value.MakeVector(elems)}
	}
	result, err := vm.call(main, mainArgs)
	if err != nil {
		return 1, err
	}
	if result.Kind() == value.Int {
		return int(result.AsInt()), nil
	}
	return 0, nil
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
		return vm.callFunction(callee.Obj().(*bytecode.Function), value.NilValue, nil, argc, false)
	case value.BoundMethod:
		bm := callee.AsBoundMethod()
		return vm.invoke(bm.Method, bm.Recv, argc)
	case value.Closure:
		cl := callee.AsClosure()
		return vm.callFunction(cl.Fn.(*bytecode.Function), cl.This, cl.Cells, argc, false)
	case value.Builtin:
		b := callee.AsBuiltin()
		args := make([]value.Value, argc)
		copy(args, vm.stack[vm.sp-argc:vm.sp])
		result, err := b.Fn(vm, args)
		if err != nil {
			return err
		}
		vm.sp -= argc + 1
		vm.stack[vm.sp] = result
		vm.sp++
		return nil
	case value.Class:
		name := callee.AsClass().Name
		return value.Errorf(value.TypeError, "cannot call class %s (use new %s(...))", name, name)
	}
	return value.Errorf(value.TypeError, "cannot call %s", callee.Kind())
}

// invoke calls member function m on recv (a static one without this).
func (vm *VM) invoke(m *value.MethodObj, recv value.Value, argc int) error {
	if m.Fn == nil {
		return value.Errorf(value.MemberError, "%s is declared but not defined", m.FullName())
	}
	if m.Static {
		recv = value.NilValue
	}
	return vm.callFunction(m.Fn.(*bytecode.Function), recv, nil, argc, false)
}

// newObject runs new: the class is below the argc arguments; it is replaced
// by the new object, and the constructor (if any) is called on it.
func (vm *VM) newObject(argc int) error {
	cv := vm.stack[vm.sp-argc-1]
	if cv.Kind() != value.Class {
		return value.Errorf(value.TypeError, "cannot create an object of %s with new", cv.Kind())
	}
	c := cv.AsClass()
	obj := value.NewObject(c)
	vm.stack[vm.sp-argc-1] = obj
	if c.Ctor == nil {
		if argc > 0 {
			return value.Errorf(value.ArgumentError, "class %s has no constructor, so new %s() takes no arguments (got %d)", c.Name, c.Name, argc)
		}
		return nil
	}
	if c.Ctor.Fn == nil {
		return value.Errorf(value.MemberError, "%s is declared but not defined", c.Ctor.FullName())
	}
	return vm.callFunction(c.Ctor.Fn.(*bytecode.Function), obj, nil, argc, true)
}

// callOp performs a call instruction; ip points just past its opcode. The
// caller reloads its state from the (possibly new) top frame.
func (vm *VM) callOp(op bytecode.Op, f *frame, ip, sp int) error {
	code := f.fn.Code
	var argc, operand int
	switch op {
	case bytecode.OpCall, bytecode.OpNew:
		argc = bytecode.ReadU16(code, ip)
		ip += 2
	case bytecode.OpCallMethod, bytecode.OpCallDirect:
		operand, argc = bytecode.ReadU16(code, ip), bytecode.ReadU16(code, ip+2)
		ip += 4
	case bytecode.OpCallMethodV:
		operand = bytecode.ReadU16(code, ip)
		ip += 2
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
		m, err := value.Method(recv, f.fn.Consts[operand].AsString())
		if err != nil {
			return err
		}
		return vm.invoke(m, recv, argc)
	case bytecode.OpCallDirect:
		return vm.callFunction(vm.prog.Funcs[operand], vm.stack[vm.sp-argc-1], nil, argc, false)
	case bytecode.OpNew, bytecode.OpNewV:
		return vm.newObject(argc)
	}
	return vm.callValue(argc)
}

// callFunction pushes a frame for fn; its arguments are the top argc stack
// entries, below them is the callee's place.
func (vm *VM) callFunction(fn *bytecode.Function, this value.Value, up []*value.CellObj, argc int, ctor bool) error {
	if err := vm.step(); err != nil {
		return err
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
		vm.frames = append(vm.frames, frame{fn: fn, base: base, argc: argc, this: this, ctor: ctor, up: up})
		return nil
	}
}

func makeFunction(fn value.Named) value.Value { return value.MakeFunction(fn) }

// step counts a backward jump or a call against StepLimit.
func (vm *VM) step() error {
	if vm.StepLimit <= 0 {
		return nil
	}
	if vm.steps++; vm.steps > vm.StepLimit {
		return &RuntimeError{Kind: "StepLimitError", Msg: fmt.Sprintf("more than %d steps", vm.StepLimit),
			Trace: vm.trace(), Fatal: true}
	}
	return nil
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
		return value.Errorf(value.ArgumentError, "%s expects at least %d argument(s), got %d", fn.FuncName, fn.NumParams, argc)
	case fn.NumOptional > 0:
		return value.Errorf(value.ArgumentError, "%s expects %d to %d arguments, got %d",
			fn.FuncName, fn.NumParams, fn.NumParams+fn.NumOptional, argc)
	}
	return value.Errorf(value.ArgumentError, "%s expects %d argument(s), got %d", fn.FuncName, fn.NumParams, argc)
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
	kind, msg := value.InternalError, err.Error()
	if ve, ok := err.(*value.Error); ok {
		kind, msg = ve.Kind, ve.Msg
	}
	obj := value.NewObject(vm.errorClass)
	vm.setField(obj, "message", value.MakeString(msg))
	vm.setField(obj, "kind", value.MakeString(kind))
	return vm.throwValue(obj)
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
func (vm *VM) throwValue(v value.Value) *RuntimeError {
	re := &RuntimeError{Value: v}
	if v.Kind() != value.Object || !v.AsObject().Class.IsSubclassOf(vm.errorClass) {
		re.Msg = v.Repr()
		re.Trace = vm.trace()
		return re
	}
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
	if vm.field(v, "kind").IsNil() {
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
	for {
		r, err := vm.run(stopDepth)
		if err == nil {
			return r, nil
		}
		re := err.(*RuntimeError)
		if !re.Fatal {
			if e := vm.step(); e != nil { // a throw/catch cycle is a loop too
				re = e.(*RuntimeError)
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
	vm.frames = vm.frames[:stopDepth]
	return false
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

	// fail records the position of the failing instruction and returns the
	// exception.
	fail := func(start int, e error) (value.Value, error) {
		f.ip = start
		vm.sp = sp
		return value.NilValue, vm.wrap(e)
	}

	for {
		start := ip
		op := bytecode.Op(code[ip])
		ip++
		switch op {
		case bytecode.OpNop:

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
					return fail(start, e)
				}
			}
			sp--
			stack[sp-1] = r
		case bytecode.OpNeg:
			r, e := value.Neg(stack[sp-1])
			if e != nil {
				return fail(start, e)
			}
			stack[sp-1] = r
		case bytecode.OpNot:
			stack[sp-1] = value.MakeBool(!stack[sp-1].Truthy())
		case bytecode.OpBitNot:
			r, e := value.BitNot(stack[sp-1])
			if e != nil {
				return fail(start, e)
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
					return fail(start, e)
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
				return fail(start, e)
			}
			stack[sp-1] = r

		case bytecode.OpJump:
			if t := bytecode.ReadU32(code, ip); t < ip {
				if e := vm.step(); e != nil {
					return fail(start, e)
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
					if e := vm.step(); e != nil {
						return fail(start, e)
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
					if e := vm.step(); e != nil {
						return fail(start, e)
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
					if e := vm.step(); e != nil {
						return fail(start, e)
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
				return fail(start, bad(f, "APPEND without a vector"))
			}
			vec := stack[sp-2].AsVector() // always a fresh vector made by the compiler's code
			vec.Elems = append(vec.Elems, stack[sp-1])
			sp--
		case bytecode.OpExtend:
			x := stack[sp-1]
			if x.Kind() != value.Vector {
				return fail(start, value.Errorf(value.TypeError, "cannot spread %s (a vector is needed)", x.Kind()))
			}
			if stack[sp-2].Kind() != value.Vector {
				return fail(start, bad(f, "EXTEND without a vector"))
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
					return fail(start, e)
				}
			}
			sp -= 2 * n
			stack[sp] = value.MakeMap(m)
			sp++
		case bytecode.OpIndexGet:
			r, e := value.Index(stack[sp-2], stack[sp-1])
			if e != nil {
				return fail(start, e)
			}
			sp--
			stack[sp-1] = r
		case bytecode.OpIndexSet:
			if e := value.SetIndex(stack[sp-3], stack[sp-2], stack[sp-1]); e != nil {
				return fail(start, e)
			}
			stack[sp-3] = stack[sp-1]
			sp -= 2
		case bytecode.OpIterInit:
			it, e := value.NewIterator(stack[sp-1])
			if e != nil {
				return fail(start, e)
			}
			stack[sp-1] = it
		case bytecode.OpIterNext:
			it := stack[base+bytecode.ReadU16(code, ip)]
			if it.Kind() != value.Iterator {
				return fail(start, bad(f, "ITER_NEXT without an iterator"))
			}
			n := bytecode.ReadU16(code, ip+2)
			first, second, ok := it.Next()
			if !ok {
				t := bytecode.ReadU32(code, ip+4)
				if t < ip {
					if e := vm.step(); e != nil {
						return fail(start, e)
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
			stack[sp] = f.this
			sp++
		case bytecode.OpLoadMember, bytecode.OpStoreMember:
			if f.this.Kind() != value.Object {
				return fail(start, value.Errorf(value.InternalError, "%s has no object", f.fn.FuncName))
			}
			fields := f.this.AsObject().Fields
			i := bytecode.ReadU16(code, ip)
			ip += 2
			if i >= len(fields) {
				return fail(start, bad(f, "data member slot out of range"))
			}
			if op == bytecode.OpLoadMember {
				stack[sp] = fields[i]
				sp++
			} else {
				sp--
				fields[i] = stack[sp]
			}
		case bytecode.OpGetMember:
			r, e := value.Member(stack[sp-1], consts[bytecode.ReadU16(code, ip)].AsString(), makeFunction)
			if e != nil {
				return fail(start, e)
			}
			ip += 2
			stack[sp-1] = r
		case bytecode.OpSetMember:
			if e := value.SetMember(stack[sp-2], consts[bytecode.ReadU16(code, ip)].AsString(), stack[sp-1]); e != nil {
				return fail(start, e)
			}
			ip += 2
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
			bytecode.OpCallDirect, bytecode.OpNew, bytecode.OpNewV:
			if e := vm.callOp(op, f, ip, sp); e != nil {
				return fail(start, e)
			}
			// A built-in has finished; a function has a new frame.
			stack, sp = vm.stack, vm.sp
			f = &vm.frames[len(vm.frames)-1]
			code, consts, base, ip = f.fn.Code, f.fn.Consts, f.base, f.ip

		case bytecode.OpReturn, bytecode.OpReturnNil:
			r := value.NilValue
			if f.ctor {
				r = f.this // new C(...) gives the object
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
				return fail(start, bad(f, "LOAD_CELL without a cell"))
			}
			stack[sp] = stack[base+bytecode.ReadU16(code, ip)].AsCell().V
			ip += 2
			sp++
		case bytecode.OpStoreCell:
			if stack[base+bytecode.ReadU16(code, ip)].Kind() != value.Cell {
				return fail(start, bad(f, "STORE_CELL without a cell"))
			}
			sp--
			stack[base+bytecode.ReadU16(code, ip)].AsCell().V = stack[sp]
			ip += 2
		case bytecode.OpLoadUpvalue:
			if bytecode.ReadU16(code, ip) >= len(f.up) {
				return fail(start, bad(f, "upvalue out of range"))
			}
			stack[sp] = f.up[bytecode.ReadU16(code, ip)].V
			ip += 2
			sp++
		case bytecode.OpStoreUpvalue:
			if bytecode.ReadU16(code, ip) >= len(f.up) {
				return fail(start, bad(f, "upvalue out of range"))
			}
			sp--
			f.up[bytecode.ReadU16(code, ip)].V = stack[sp]
			ip += 2
		case bytecode.OpClosure:
			fn := vm.prog.Funcs[bytecode.ReadU16(code, ip)]
			ip += 2
			cells := make([]*value.CellObj, len(fn.Upvalues))
			for i, u := range fn.Upvalues {
				switch {
				case u.FromLocal && u.Index < f.fn.NumSlots && stack[base+u.Index].Kind() == value.Cell:
					cells[i] = stack[base+u.Index].AsCell()
				case !u.FromLocal && u.Index < len(f.up):
					cells[i] = f.up[u.Index]
				default:
					return fail(start, bad(f, "closure captures a variable that is not there"))
				}
			}
			stack[sp] = value.MakeClosure(&value.ClosureObj{Fn: fn, Cells: cells, This: f.this})
			sp++

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
				return fail(start, bad(f, "TRY_POP without a handler"))
			}
			vm.handlers = vm.handlers[:len(vm.handlers)-1]
		case bytecode.OpThrow:
			sp--
			f.ip = start
			vm.sp = sp
			return value.NilValue, vm.throwValue(stack[sp])
		case bytecode.OpRethrow:
			sp--
			re, ok := stack[sp].Obj().(*RuntimeError)
			if stack[sp].Kind() != value.Internal || !ok {
				return fail(start, bad(f, "RETHROW without a pending exception"))
			}
			vm.sp = sp
			return value.NilValue, re

		default:
			return fail(start, value.Errorf(value.InternalError, "bad opcode %d at %d in %s", op, start, f.fn.FuncName))
		}
	}
}
