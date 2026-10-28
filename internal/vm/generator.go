package vm

import (
	"errors"

	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/value"
)

// Generators (Q86). Calling a generator function runs it up to GENERATOR,
// which keeps the frame (its slots, operands and try handlers) in a
// generator and returns it, an object of the prelude's class Generator.
// Each step (resume) puts the frame back on top of the stack and runs it,
// in a run of its own, until YIELD keeps it again or the body ends.
//
// Closing a suspended generator resumes it with an exception that catch
// blocks do not see (closing): its finally blocks and with statements run,
// and it stops at the generator's frame.

type genState uint8

const (
	genReady   genState = iota // before its first step, or at a yield
	genRunning                 // a step is running
	genDone                    // it ended, threw, or was closed
)

// generator is the state of a generator: the Go value of its object.
type generator struct {
	obj   *value.ObjectObj
	state genState

	// A generator written in Kukel: the frame while it is suspended.
	fn       *bytecode.Function
	this     *value.ObjectObj
	cl       *value.ClosureObj
	argc     int
	ip       int
	slots    []value.Value // its slots and operands
	handlers []handler     // its try handlers, sp relative to the frame's base
	loops    []*generator  // the generators of its handlers of for-each loops, in order
	started  bool          // it has run a step (a close before one runs nothing)
	closing  bool          // it is being closed: it may not yield

	// A step that a for-each loop runs in its own dispatch loop (genNext):
	// what YIELD and GEN_RETURN do for the loop.
	inline bool
	two    bool     // the loop has two names: push the count too
	end    int      // where the loop goes on at the generator's end
	gi     *genIter // the loop's iterator (the count)

	// A generator written in Go (NewGenerator): its next value.
	name string
	next func() (value.Value, bool, error)
}

// genIter is what a for-each loop over a generator keeps in its iterator
// slot: the generator and the count for a loop with two names.
type genIter struct {
	g      *generator
	i      int64
	closed bool // ITER_CLOSE ran
}

func (g *generator) funcName() string {
	if g.fn != nil {
		return g.fn.FuncName
	}
	return g.name
}

// setupGenerators finds the prelude's class Generator and gives it its
// member functions, written in Go.
func (vm *VM) setupGenerators() {
	var k *value.ClassObj
	for _, c := range vm.classes {
		if c.Name == "Generator" && c.Base == nil {
			k = c
			break
		}
	}
	if k == nil {
		return // a program made without the prelude: no generators
	}
	vm.genClass = k
	k.Native = true // its objects come from generator functions only
	k.Show = func(o *value.ObjectObj) string {
		if g, ok := o.State.(*generator); ok {
			return "Generator(" + g.funcName() + ")"
		}
		return "Generator()"
	}
	method := func(name string, maxArgs int, fn func(g *generator, args []value.Value) (value.Value, error)) {
		m := k.Methods[name]
		if m == nil {
			return
		}
		m.Fn = nil // (the compiler refuses a definition; a file could still hold one)
		m.Native = func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			g, ok := this.State.(*generator)
			if !ok {
				return value.NilValue, value.Errorf(value.ValueError, "Generator::%s: this object is not a generator (generators come from calling a generator function)", name)
			}
			if len(args) > maxArgs {
				want := value.Arguments(0)
				if maxArgs > 0 {
					want = value.ArgumentRange(0, maxArgs)
				}
				return value.NilValue, value.Errorf(value.ArgumentError, "Generator::%s expects %s, got %d", name, want, len(args))
			}
			return fn(g, args)
		}
	}
	method("next", 1, func(g *generator, args []value.Value) (value.Value, error) {
		v, ok, err := vm.resume(g, false)
		if !ok && err == nil && len(args) > 0 {
			v = args[0]
		}
		return v, err
	})
	method("done", 0, func(g *generator, _ []value.Value) (value.Value, error) {
		return value.MakeBool(g.state == genDone), nil
	})
	method("close", 0, func(g *generator, _ []value.Value) (value.Value, error) {
		_, _, err := vm.resume(g, true)
		return value.NilValue, err
	})
	method("iter", 0, func(g *generator, _ []value.Value) (value.Value, error) {
		return value.ObjectValue(g.obj), nil
	})
}

// NewGenerator makes a generator whose values come from next, a function
// written in Go: it gives the next value, or false at the end. name is how
// the generator shows ("fs::File::iter"). See native.Generator.
func (vm *VM) NewGenerator(name string, next func() (value.Value, bool, error)) (value.Value, error) {
	if vm.genClass == nil {
		return value.NilValue, value.Errorf(value.InternalError, "the program has no Generator class")
	}
	g := &generator{name: name, next: next}
	g.obj = &value.ObjectObj{Class: vm.genClass, State: g}
	return value.ObjectValue(g.obj), nil
}

// generatorOf returns the generator v holds, or nil.
func (vm *VM) generatorOf(v value.Value) *generator {
	if v.Kind() != value.Object {
		return nil
	}
	g, _ := v.AsObject().State.(*generator)
	if g != nil && v.AsObject().Class != vm.genClass {
		return nil
	}
	return g
}

// errGenStep is how run leaves an instruction to genStep: the generators'
// instructions, ITER_INIT of an object and ITER_NEXT of a generator. (Run
// has no code of its own for them: the dispatch loop is faster with less
// code in it, and these have the error paths of run for ITER_INIT and
// ITER_NEXT, and its default case.) fail leaves the frame at the
// instruction.
var errGenStep = errors.New("an instruction for genStep")

// errGenReturn is how genStep makes execute return vm.genResult at stack
// depth vm.genSP: YIELD, or GENERATOR in the frame run started with.
var errGenReturn = errors.New("generator step done")

// genStep does the instruction run left to it (errGenStep) in the top
// frame, then run goes on (nil), or returns (errGenReturn), or the
// instruction throws (a *RuntimeError).
func (vm *VM) genStep(stopDepth int) error {
	f := &vm.frames[len(vm.frames)-1]
	start, sp := f.ip, vm.sp
	err := vm.genOp(start, sp, stopDepth)
	if err == nil || err == errGenReturn {
		return err
	}
	f = &vm.frames[len(vm.frames)-1]
	f.ip, vm.sp = start, sp
	return vm.wrap(err)
}

// genOp does the instruction at start in the top frame, with the stack
// depth sp. It leaves the state in vm.sp and the frame's ip, or returns
// errGenReturn, or an error (the frame is then the instruction's).
func (vm *VM) genOp(start, sp, stopDepth int) error {
	f := &vm.frames[len(vm.frames)-1]
	code := f.fn.Code
	op := bytecode.Op(code[start])
	if op == bytecode.OpBreak {
		op = f.fn.Saved[start] // in the debugger's copy of the code
	}
	ip := start + 1
	f.ip, vm.sp = ip, sp // (a caller's ip is within its instruction for the trace)
	switch op {
	case bytecode.OpIterInit:
		x := vm.stack[sp-1]
		_, err := value.NewIterator(x)
		it, err := vm.iterate(x, err, sp-1)
		if err != nil {
			return err
		}
		vm.stack[sp-1] = it
	case bytecode.OpIterNext:
		return vm.genNext(f, ip, sp, vm.stack[f.base+bytecode.ReadU16(code, ip)])
	case bytecode.OpIterClose:
		f.ip = ip + 2
		if err := vm.iterClose(f, vm.stack[f.base+bytecode.ReadU16(code, ip)]); err != nil {
			return err
		}
	case bytecode.OpGenerator: // it returns the generator, as RETURN does
		r, err := vm.makeGenerator(f, ip, sp)
		if err != nil {
			return err
		}
		vm.sp = f.base - 1
		vm.stack[vm.sp] = r
		vm.frames = vm.frames[:len(vm.frames)-1]
		if len(vm.frames) == stopDepth {
			vm.genResult, vm.genSP = r, vm.sp
			return errGenReturn
		}
		vm.sp++
	case bytecode.OpYield:
		g := vm.running(f)
		if g == nil {
			return bad(f, "YIELD outside a step of its generator")
		}
		return vm.yield(f, g, vm.stack[sp-1], ip, sp-1, stopDepth)
	case bytecode.OpGenReturn:
		g := vm.running(f)
		if g == nil {
			return bad(f, "GEN_RETURN outside a step of its generator")
		}
		return vm.genReturn(f, g, stopDepth)
	default:
		return value.Errorf(value.InternalError, "bad opcode %d at %d in %s", op, start, f.fn.FuncName)
	}
	return nil
}

// makeGenerator is GENERATOR: it keeps the frame f, with the slots up to
// sp, in a new generator that goes on at ip.
func (vm *VM) makeGenerator(f *frame, ip, sp int) (value.Value, error) {
	if vm.genClass == nil {
		return value.NilValue, value.Errorf(value.InternalError, "the program has no Generator class")
	}
	if g := vm.generatorOf(vm.stack[f.base-1]); g != nil && g.state == genRunning || f.ctor {
		return value.NilValue, bad(f, "GENERATOR in a running generator or a constructor")
	}
	if n := len(vm.handlers); n > 0 && vm.handlers[n-1].frame == len(vm.frames)-1 {
		return value.NilValue, bad(f, "GENERATOR with a handler registered")
	}
	g := &generator{fn: f.fn, this: f.this, cl: f.cl, argc: f.argc, ip: ip}
	g.slots = append([]value.Value(nil), vm.stack[f.base:sp]...)
	g.obj = &value.ObjectObj{Class: vm.genClass, State: g}
	return value.ObjectValue(g.obj), nil
}

// running gives the generator whose step frame f (the top one) runs.
func (vm *VM) running(f *frame) *generator {
	if g := vm.generatorOf(vm.stack[f.base-1]); g != nil && g.state == genRunning {
		return g
	}
	return nil
}

// yield is YIELD in frame f (the top one) of generator g: it keeps the
// frame in g, to go on at ip, and drops it. A step of a nested run ends
// it (errGenReturn, its frame at depth stopDepth); a step of a for-each
// loop (inline) gives the loop its value(s), and the loop goes on.
func (vm *VM) yield(f *frame, g *generator, v value.Value, ip, sp, stopDepth int) error {
	if !g.inline && len(vm.frames)-1 != stopDepth {
		return bad(f, "YIELD outside a step of its generator")
	}
	if g.closing {
		return value.Errorf(value.ValueError, "the generator %s yields while it is being closed (a yield in a finally block?)", g.funcName())
	}
	vm.suspend(f, g, ip, sp)
	if !g.inline {
		vm.genResult, vm.genSP = v, vm.sp
		return errGenReturn
	}
	g.inline = false
	vm.ensure(2)
	if g.two {
		vm.stack[vm.sp] = value.MakeInt(g.gi.i)
		vm.sp++
	}
	g.gi.i++
	vm.stack[vm.sp] = v
	vm.sp++
	return nil
}

// genYield is YIELD in frame f (ip just past it, sp the stack depth with
// the value on top). done: run returns vm.genResult (a step of a nested
// run ended); else run reloads its state from the top frame (the loop's).
//
//go:noinline
func (vm *VM) genYield(f *frame, ip, sp, stopDepth int) (done bool, err error) {
	g := vm.running(f)
	if g == nil {
		return false, bad(f, "YIELD outside a step of its generator")
	}
	err = vm.yield(f, g, vm.stack[sp-1], ip, sp-1, stopDepth)
	if err == errGenReturn {
		return true, nil
	}
	return false, err
}

// suspend keeps frame f (the top one, with the stack up to sp) in g, to go
// on at ip, with its try handlers, and drops it.
func (vm *VM) suspend(f *frame, g *generator, ip, sp int) {
	top := len(vm.frames) - 1
	g.ip = ip
	g.slots = append(g.slots[:0], vm.stack[f.base:sp]...)
	n := len(vm.handlers)
	for n > 0 && vm.handlers[n-1].frame == top {
		n--
	}
	g.handlers = append(g.handlers[:0], vm.handlers[n:]...)
	g.loops = g.loops[:0]
	for i := n; i < len(vm.handlers); i++ {
		if h := vm.handlers[i]; h.loop != 0 {
			var lg *generator
			if int(h.loop) <= len(vm.loops) {
				lg = vm.loops[h.loop-1]
			}
			g.loops = append(g.loops, lg)
		}
	}
	for i := len(vm.handlers) - 1; i >= n; i-- {
		vm.popLoop(vm.handlers[i])
	}
	for i := range g.handlers {
		g.handlers[i].sp -= f.base
	}
	vm.handlers = vm.handlers[:n]
	g.state = genReady
	vm.frames = vm.frames[:top]
	vm.sp = f.base - 1
}

// genReturn is GEN_RETURN in frame f (the top one) of generator g: g is
// done, and its frame dropped. A nested run ends (errGenReturn); a
// for-each loop goes on at its end.
func (vm *VM) genReturn(f *frame, g *generator, stopDepth int) error {
	top := len(vm.frames) - 1
	if !g.inline && top != stopDepth {
		return bad(f, "GEN_RETURN outside a step of its generator")
	}
	if g.inline && g.end < vm.frames[top-1].ip { // the loop jumps back to go on: a step (see genNext)
		if err := vm.step(pcTop); err != nil {
			return err
		}
	}
	for n := len(vm.handlers); n > 0 && vm.handlers[n-1].frame == top; n-- { // (the verifier lets none through)
		vm.popLoop(vm.handlers[n-1])
		vm.handlers = vm.handlers[:n-1]
	}
	vm.frames = vm.frames[:top]
	vm.sp = f.base - 1
	inline := g.inline
	g.finish()
	if !inline {
		vm.genResult, vm.genSP = value.NilValue, vm.sp
		return errGenReturn
	}
	vm.frames[top-1].ip = g.end
	return nil
}

// finish marks g done and lets go of what it kept.
func (g *generator) finish() {
	g.state, g.slots, g.handlers, g.loops, g.inline, g.gi = genDone, nil, nil, nil, false, nil
}

// resume runs the next step of g: it gives the value of its next yield, or
// false when it has ended (also when it ends now). With closing, the step
// ends the generator at its yield as a return would, running its finally
// blocks; closing one that has not started or has ended does nothing.
func (vm *VM) resume(g *generator, closing bool) (value.Value, bool, error) {
	switch g.state {
	case genRunning:
		return value.NilValue, false, value.Errorf(value.ValueError, "the generator %s is already running (its own body asks it for a value)", g.funcName())
	case genDone:
		return value.NilValue, false, nil
	}
	if g.next != nil { // written in Go
		if closing {
			g.state = genDone
			return value.NilValue, false, nil
		}
		g.state = genRunning
		v, ok, err := g.next()
		if g.state = genReady; err != nil || !ok {
			g.state = genDone
		}
		return v, ok && err == nil, err
	}
	if closing && !g.started {
		g.state, g.slots = genDone, nil
		return value.NilValue, false, nil
	}
	sp0, depth := vm.sp, len(vm.frames)
	if err := vm.enterGenerator(g); err != nil {
		return value.NilValue, false, err
	}
	g.closing = closing

	var r value.Value
	var err error
	if closing {
		re := &RuntimeError{Kind: "GeneratorClose", closing: true}
		if re, caught := vm.catch(re, depth); !caught {
			err = re // no finally block to run
		}
	}
	if err == nil {
		r, err = vm.execute(depth)
	}
	vm.sp = sp0
	if err == nil && g.state == genReady {
		return r, true, nil // it yielded r
	}
	g.finish()
	if re, ok := err.(*RuntimeError); ok && re.closing {
		err = nil // closed
	}
	return value.NilValue, false, err
}

// enterGenerator puts g's frame back on top of the stack, as it was: the
// generator in the callee's place, then its slots and operands, and its
// try handlers; g is running.
func (vm *VM) enterGenerator(g *generator) error {
	if err := vm.step(pcCaller); err != nil {
		return err
	}
	depth := len(vm.frames)
	if depth >= MaxFrames {
		return value.Errorf(value.StackOverflow, "stack overflow (more than %d nested calls)", MaxFrames)
	}
	if s := vm.prof; s != nil {
		s.count(g.fn)
	}
	sp0 := vm.sp
	vm.ensure(1 + len(g.slots) + g.fn.MaxStack)
	vm.stack[sp0] = value.ObjectValue(g.obj)
	base := sp0 + 1
	vm.sp = base + copy(vm.stack[base:], g.slots)
	vm.frames = append(vm.frames, frame{fn: g.fn, ip: g.ip, base: base, argc: g.argc, this: g.this, cl: g.cl})
	loops := g.loops
	for _, h := range g.handlers {
		h.frame, h.sp = depth, h.sp+base
		if h.loop != 0 && len(loops) > 0 {
			vm.loops = append(vm.loops, loops[0])
			loops, h.loop = loops[1:], int32(len(vm.loops))
		}
		vm.handlers = append(vm.handlers, h)
	}
	g.handlers, g.loops = g.handlers[:0], g.loops[:0]
	g.state, g.started, g.closing = genRunning, true, false
	return nil
}

// genNext is ITER_NEXT of a for-each loop over a generator, in frame f
// (ip just past the opcode, sp the stack depth). A suspended generator
// written in Kukel runs its step in the loop's own dispatch loop: its
// frame goes on top, and YIELD (or GEN_RETURN) hands back to the loop.
// Others (written in Go, or done) are asked here. The dispatch loop then
// reloads its state from the top frame. (Not inlined: run is faster with
// less code in it.)
//
//go:noinline
func (vm *VM) genNext(f *frame, ip, sp int, it value.Value) error {
	gi, ok := it.Obj().(*genIter)
	if it.Kind() != value.Internal || !ok {
		return bad(f, "ITER_NEXT without an iterator")
	}
	code := f.fn.Code
	g, two, end := gi.g, bytecode.ReadU16(code, ip+2) == 2, bytecode.ReadU32(code, ip+4)
	f.ip, vm.sp = ip+8, sp
	if g.next == nil && g.state == genReady {
		if err := vm.enterGenerator(g); err != nil {
			return err
		}
		g.inline, g.two, g.end, g.gi = true, two, end, gi
		return nil
	}
	count, v, more, err := vm.iterNext(f, it)
	f = &vm.frames[len(vm.frames)-1] // a step may have moved them
	if err != nil {
		return err
	}
	if !more {
		if end < ip { // a backward jump is a step, as for the other loops (only in bytecode the compiler does not make)
			if err := vm.step(pcTop); err != nil {
				return err
			}
		}
		f.ip = end
		return nil
	}
	if two {
		vm.stack[sp] = count
		sp++
	}
	vm.stack[sp] = v
	vm.sp = sp + 1
	return nil
}

// iterate is ITER_INIT of a value that is not a vector, map or string (e,
// the error for it, if not an object either): a generator, or an object
// whose member function iter() gives one. The loop's frame registers a
// handler that closes the generator when an exception leaves the loop;
// sp is the stack depth outside the loop.
func (vm *VM) iterate(x value.Value, e error, sp int) (value.Value, error) {
	if x.Kind() != value.Object {
		return value.NilValue, e
	}
	g := vm.generatorOf(x)
	if _, other := x.AsObject().State.(*generator); g == nil && other {
		return value.NilValue, value.Errorf(value.TypeError, "this generator belongs to another interpreter: a generator runs where it was made")
	}
	if g == nil {
		k := x.AsObject().Class
		m, ok := k.Methods["iter"]
		if !ok || m.Static {
			return value.NilValue, value.Errorf(value.TypeError, "cannot iterate over an object of class %s: it has no member function iter() (which gives a generator)", k.Name)
		}
		v, err := vm.call(value.MakeBoundMethod(x, m), nil)
		if err != nil {
			return value.NilValue, err
		}
		if g = vm.generatorOf(v); g == nil {
			return value.NilValue, value.Errorf(value.TypeError, "%s::iter() must give a generator, not %s", k.Name, typeName(v))
		}
	}
	vm.loops = append(vm.loops, g)
	vm.handlers = append(vm.handlers, handler{frame: len(vm.frames) - 1, sp: sp, loop: int32(len(vm.loops))})
	return value.MakeInternal(&genIter{g: g}), nil
}

// typeName names the kind of v, or its class for an object.
func typeName(v value.Value) string {
	if v.Kind() == value.Object {
		return "an object of class " + v.AsObject().Class.Name
	}
	return v.Kind().String()
}

// iterNext is ITER_NEXT of a loop over a generator: the next value (with
// its count), or false at the end.
func (vm *VM) iterNext(f *frame, it value.Value) (count, v value.Value, ok bool, err error) {
	gi, isGen := it.Obj().(*genIter)
	if it.Kind() != value.Internal || !isGen {
		return value.NilValue, value.NilValue, false, bad(f, "ITER_NEXT without an iterator")
	}
	v, ok, err = vm.resume(gi.g, false)
	if ok {
		count = value.MakeInt(gi.i)
		gi.i++
	}
	return count, v, ok, err
}

// iterClose is ITER_CLOSE: a loop over a generator is left, so its handler
// goes and the generator is closed (unless done already).
func (vm *VM) iterClose(f *frame, it value.Value) error {
	gi, ok := it.Obj().(*genIter)
	if it.Kind() != value.Internal || !ok || gi.closed {
		return nil // a vector, map or string
	}
	gi.closed = true
	n := len(vm.handlers)
	if n == 0 || vm.handlers[n-1].loop == 0 || vm.popLoop(vm.handlers[n-1]) != gi.g || vm.handlers[n-1].frame != len(vm.frames)-1 {
		return bad(f, "ITER_CLOSE without its loop's handler")
	}
	vm.handlers = vm.handlers[:n-1]
	_, _, err := vm.resume(gi.g, true)
	return err
}

// popLoop gives the generator of h, the handler of a for-each loop, and
// drops it (with any left above it) from vm.loops; nil if h has none.
func (vm *VM) popLoop(h handler) *generator {
	i := int(h.loop) - 1
	if i < 0 || i >= len(vm.loops) {
		return nil
	}
	g := vm.loops[i]
	clear(vm.loops[i:])
	vm.loops = vm.loops[:i]
	return g
}

// dropFrames drops the frames from index n up, as an exception leaves
// them. A generator whose step one of them ran for a for-each loop
// (inline) has ended (a step of a nested run ends in resume).
func (vm *VM) dropFrames(n int) {
	for i := len(vm.frames) - 1; i >= n; i-- {
		if g := vm.generatorOf(vm.stack[vm.frames[i].base-1]); g != nil && g.state == genRunning && g.inline {
			g.finish()
		}
	}
	vm.frames = vm.frames[:n]
}
