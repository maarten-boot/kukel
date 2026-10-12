// Package vm executes compiled Kukel programs on a stack machine.
//
// Each call has a frame. A frame's slots (parameters, temporaries, locals)
// sit on the value stack at frame.base, followed by its operand stack. The
// callee value sits just below the slots.
package vm

import (
	"fmt"
	"io"
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

// RuntimeError is an error raised while the program runs. (Milestone 7
// turns these into catchable exceptions.)
type RuntimeError struct {
	Kind  string
	Msg   string
	Trace []TraceEntry // innermost call first
}

// Error returns the message and the stack trace. Long traces (deep
// recursion) show only the first and last traceEnds calls.
func (e *RuntimeError) Error() string {
	const traceEnds = 10
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", e.Kind, e.Msg)
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
	ip   int // next instruction (saved while the frame calls another)
	base int // first slot
}

// VM runs one program.
type VM struct {
	prog    *bytecode.Program
	globals []value.Value
	stack   []value.Value
	sp      int // first free stack entry
	frames  []frame
	stdout  io.Writer
	stdin   io.Reader
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
	return vm, nil
}

// Stdout and Stdin implement value.Env for the built-ins.
func (vm *VM) Stdout() io.Writer { return vm.stdout }
func (vm *VM) Stdin() io.Reader  { return vm.stdin }

// Run runs the global initializers, then main(args). It returns main's
// result as the exit code (an int result; anything else is 0).
func (vm *VM) Run(args []string) (int, error) {
	if _, err := vm.call(value.MakeFunction(vm.prog.Init), nil); err != nil {
		return 1, err
	}
	main := vm.globals[vm.prog.Main]
	var mainArgs []value.Value
	if f, ok := main.Obj().(*bytecode.Function); ok && f.NumParams == 1 {
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

// call calls fn with args from Go and runs it to completion.
func (vm *VM) call(fn value.Value, args []value.Value) (value.Value, error) {
	vm.ensure(len(args) + 1)
	vm.stack[vm.sp] = fn
	vm.sp++
	for _, a := range args {
		vm.stack[vm.sp] = a
		vm.sp++
	}
	depth := len(vm.frames)
	if err := vm.callValue(len(args)); err != nil {
		return value.NilValue, vm.wrap(err)
	}
	if len(vm.frames) == depth { // a built-in: already done
		vm.sp--
		return vm.stack[vm.sp], nil
	}
	return vm.execute(depth)
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
		fn := callee.Obj().(*bytecode.Function)
		if argc != fn.NumParams {
			return value.Errorf(value.ArgumentError, "%s expects %d argument(s), got %d", fn.FuncName, fn.NumParams, argc)
		}
		if len(vm.frames) >= MaxFrames {
			return value.Errorf(value.StackOverflow, "stack overflow (more than %d nested calls)", MaxFrames)
		}
		base := vm.sp - argc
		vm.ensure(fn.NumSlots - argc + fn.MaxStack)
		for i := vm.sp; i < base+fn.NumSlots; i++ {
			vm.stack[i] = value.NilValue
		}
		vm.sp = base + fn.NumSlots
		vm.frames = append(vm.frames, frame{fn: fn, base: base})
		return nil
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
	}
	return value.Errorf(value.TypeError, "cannot call %s", callee.Kind())
}

// wrap turns an error into a RuntimeError with a stack trace of the current
// frames. The innermost frame's ip must point at the failing instruction.
func (vm *VM) wrap(err error) error {
	re := &RuntimeError{Kind: value.InternalError, Msg: err.Error()}
	if ve, ok := err.(*value.Error); ok {
		re.Kind, re.Msg = ve.Kind, ve.Msg
	}
	for i := len(vm.frames) - 1; i >= 0; i-- {
		f := vm.frames[i]
		pc := f.ip
		if i < len(vm.frames)-1 {
			pc-- // a caller's ip is just past its CALL instruction
		}
		re.Trace = append(re.Trace, TraceEntry{Func: f.fn.FuncName, File: f.fn.File, Line: f.fn.LineAt(pc)})
	}
	return re
}

// execute runs frames until the frame count drops back to stopDepth, and
// returns the value the last frame returned.
func (vm *VM) execute(stopDepth int) (result value.Value, err error) {
	f := &vm.frames[len(vm.frames)-1]
	code, consts, base := f.fn.Code, f.fn.Consts, f.base
	ip := 0
	stack := vm.stack
	sp := vm.sp

	// fail records the position of the failing instruction and unwinds.
	fail := func(start int, e error) (value.Value, error) {
		f.ip = start
		vm.sp = sp
		err := vm.wrap(e)
		vm.frames = vm.frames[:stopDepth]
		return value.NilValue, err
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
			ip = bytecode.ReadU32(code, ip)
		case bytecode.OpJumpIfFalse:
			sp--
			if !stack[sp].Truthy() {
				ip = bytecode.ReadU32(code, ip)
			} else {
				ip += 4
			}
		case bytecode.OpJumpIfTrue:
			sp--
			if stack[sp].Truthy() {
				ip = bytecode.ReadU32(code, ip)
			} else {
				ip += 4
			}

		case bytecode.OpCall:
			argc := bytecode.ReadU16(code, ip)
			ip += 2
			f.ip = ip
			vm.sp = sp
			if e := vm.callValue(argc); e != nil {
				return fail(start, e)
			}
			// A built-in has finished; a function has a new frame.
			stack, sp = vm.stack, vm.sp
			f = &vm.frames[len(vm.frames)-1]
			code, consts, base, ip = f.fn.Code, f.fn.Consts, f.base, f.ip

		case bytecode.OpReturn, bytecode.OpReturnNil:
			r := value.NilValue
			if op == bytecode.OpReturn {
				r = stack[sp-1]
			}
			sp = f.base - 1 // drop the slots, the operands and the callee
			stack[sp] = r
			sp++
			vm.frames = vm.frames[:len(vm.frames)-1]
			if len(vm.frames) == stopDepth {
				vm.sp = sp - 1
				return r, nil
			}
			f = &vm.frames[len(vm.frames)-1]
			code, consts, base, ip = f.fn.Code, f.fn.Consts, f.base, f.ip

		default:
			return fail(start, value.Errorf(value.InternalError, "bad opcode %d at %d in %s", op, start, f.fn.FuncName))
		}
	}
}
