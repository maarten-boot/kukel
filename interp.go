package kukel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Options configure an interpreter.
type Options struct {
	Stdout io.Writer // what print writes to; nil: discarded
	Stderr io.Writer // what eprint writes to; nil: discarded
	Stdin  io.Reader // what readline reads; nil: empty

	// StepLimit bounds each call from Go (Run, Call, CallValue): after that
	// many steps (backward jumps, calls and throws, so every loop counts)
	// the call ends with an *Error of kind "StepLimitError" that the
	// program cannot catch. Calls a Func makes back into Kukel share the
	// budget of the call that runs the Func. 0: no limit. Workers the
	// program starts get the same limit for their whole run.
	StepLimit int
}

// Interp is an interpreter running a Program: it has its own globals,
// output and state. Its methods must not be called from several
// goroutines at once; use one Interp per goroutine (they can share a
// Program). A Func may call back into the Interp that called it.
type Interp struct {
	vm     *vm.VM
	life   *vm.Canceller // cancelled by Close; the calls' cancellers are its children
	depth  int           // calls from Go in progress (a Func may call back)
	closed bool
}

type interpKey struct{}

// New makes an interpreter for p. It fails if p uses a built-in or module
// that its CompileOptions do not allow (a program from LoadProgram).
func New(p *Program, opts Options) (*Interp, error) {
	stdout, stderr, stdin := opts.Stdout, opts.Stderr, opts.Stdin
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	m, err := vm.NewWith(p.prog, builtins.Lookup, p.config, stdout, stdin)
	if err != nil {
		return nil, fmt.Errorf("kukel: %w", err)
	}
	m.SetStderr(stderr)
	m.StepLimit = opts.StepLimit
	in := &Interp{vm: m, life: m.Cancellable()}
	m.Local(interpKey{}, func() any { return in })
	return in, nil
}

// Close stops the interpreter for good: workers it started that are still
// running are cancelled, and what they write later is dropped. Calls after
// Close give an error. Close may be called more than once.
func (in *Interp) Close() {
	in.life.Cancel(errClosed)
	in.vm.CloseOutput()
	in.closed = true
}

var errClosed = errors.New("the interpreter was closed")

// interpOf is the Interp of a VM that calls a Func; a worker's VM gets one
// when it first needs it.
func interpOf(env value.Env) *Interp {
	m := env.(*vm.VM)
	return m.Local(interpKey{}, func() any {
		life := m.CurrentCanceller()
		if life == nil {
			life = m.Cancellable()
		}
		return &Interp{vm: m, life: life, depth: 1}
	}).(*Interp)
}

// Run runs the program's main, with args as its parameter (a vector of
// strings) if it has one, and gives the exit code: main's result if that
// is an int, else 0; or the code of exit(code). If the program fails, Run
// gives 1 and the error. The global initializers run first, the first time
// anything is called; if they throw an exception, every call gives it (a
// step limit or cancellation lets the next call run them again). Run does
// not wait for workers the program started (see Close).
func (in *Interp) Run(args ...string) (int, error) {
	return in.RunContext(context.Background(), args...)
}

// RunContext is Run, stopped when ctx ends (see CallContext).
func (in *Interp) RunContext(ctx context.Context, args ...string) (int, error) {
	elems := make([]value.Value, len(args))
	for i, a := range args {
		elems[i] = value.MakeString(a)
	}
	r, err := in.enter(ctx, func() (value.Value, error) {
		return in.vm.Start(value.NilValue, []value.Value{value.MakeVector(elems)})
	})
	if x, ok := err.(*ExitError); ok {
		return x.Code, nil
	}
	if err != nil {
		return 1, err
	}
	if n, ok := r.AsInt(); ok {
		return int(n), nil
	}
	return 0, nil
}

// Call calls the program's function name ("discount", or a module's:
// "util::f", also a private "util::_g", or "json::encode" of a Go module the
// program may use) with args and gives its result. A Kukel exception that
// nothing catches is an *Error; exit(code) gives an *ExitError.
func (in *Interp) Call(name string, args ...Value) (Value, error) {
	return in.CallContext(context.Background(), name, args...)
}

// CallContext is Call, stopped when ctx ends: the program stops at its
// next step (a function written in Go that waits, such as time::sleep,
// returns at once) with an *Error of kind "CancelledError" that it cannot
// catch. This also holds for a call a Func makes back into Kukel. Workers
// started during the call are cancelled if ctx ends while it runs; Close
// cancels those that outlive it.
func (in *Interp) CallContext(ctx context.Context, name string, args ...Value) (Value, error) {
	return in.enter(ctx, func() (value.Value, error) {
		if err := in.vm.Init(); err != nil {
			return value.NilValue, err
		}
		fn, ok := in.global(name)
		if !ok {
			return value.NilValue, fmt.Errorf("kukel: the program has no function %s", name)
		}
		switch fn.Kind() {
		case value.Function, value.Closure, value.Builtin, value.BoundMethod:
		default:
			return value.NilValue, fmt.Errorf("kukel: %s is not a function", name)
		}
		return in.vm.Call(fn, unwrap(args))
	})
}

// CallValue calls a Kukel function value (such as a callback a Func
// received) with args.
func (in *Interp) CallValue(fn Value, args ...Value) (Value, error) {
	return in.enter(context.Background(), func() (value.Value, error) {
		return in.vm.Call(fn.v, unwrap(args))
	})
}

// Global gives the value of a global of the program (a variable, function
// or class), after the global initializers have run.
func (in *Interp) Global(name string) (Value, error) {
	r, err := in.enter(context.Background(), func() (value.Value, error) {
		if err := in.vm.Init(); err != nil {
			return value.NilValue, err
		}
		v, ok := in.global(name)
		if !ok {
			return value.NilValue, fmt.Errorf("kukel: the program has no global %s", name)
		}
		return v, nil
	})
	return r, err
}

// global finds name among the program's globals, else among the names of
// the modules written in Go that it may use (a function the program itself
// does not call has no global slot).
func (in *Interp) global(name string) (value.Value, bool) {
	if v, ok := in.vm.Global(name); ok {
		return v, true
	}
	if strings.Contains(name, "::") {
		return in.vm.Config().Global(name)
	}
	return value.NilValue, false
}

// enter runs f as a call from Go. The outermost call resets the step
// count (calls from a Func share the budget of the call that runs it). A
// context that can end gets a canceller of its own for the call, a child
// of the current one, so a nested call can be cancelled too; workers
// started during the call keep it.
func (in *Interp) enter(ctx context.Context, f func() (value.Value, error)) (Value, error) {
	if in.closed {
		return Nil, fmt.Errorf("kukel: %w", errClosed)
	}
	if err := ctx.Err(); err != nil {
		return Nil, &Error{Kind: "CancelledError", Message: "the program was cancelled: " + context.Cause(ctx).Error()}
	}
	if in.depth == 0 {
		in.vm.ResetSteps()
	}
	if ctx.Done() != nil {
		outer := in.vm.CurrentCanceller()
		c := outer.Child()
		in.vm.ShareCanceller(c)
		stop := context.AfterFunc(ctx, func() { c.Cancel(context.Cause(ctx)) })
		defer func() {
			stop()
			in.vm.ShareCanceller(outer)
			outer.Release(c)
		}()
	}
	in.depth++
	defer func() { in.depth-- }()
	r, err := f()
	if err != nil {
		return Nil, fromVM(err)
	}
	return Value{r}, nil
}

func unwrap(args []Value) []value.Value {
	vs := make([]value.Value, len(args))
	for i, a := range args {
		vs[i] = a.v
	}
	return vs
}
