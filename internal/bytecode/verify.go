package bytecode

import (
	"fmt"

	"github.com/maarten-boot/kukel/internal/value"
)

// Verify checks that a program cannot make the VM crash: every index in an
// instruction refers to something that exists, jumps land on instructions,
// no function can run past its last instruction, and on every path the
// operand stack depth is the same at each instruction, never negative and
// never above the function's MaxStack. Decode calls it for every file; the
// compiler's output always passes. Try handlers are counted the same way:
// the same number at each instruction on every path, TRY_POP only with one
// registered, and none left when the function returns.
func Verify(p *Program) error {
	v := &verifier{p: p}
	if p.Init == nil {
		return fmt.Errorf("invalid bytecode: no init function")
	}
	if len(p.Globals) > maxCount || len(p.Classes) > maxCount || len(p.Funcs) > maxCount {
		return fmt.Errorf("invalid bytecode: too many globals, classes or functions")
	}
	for i, g := range p.Globals {
		switch g.Kind {
		case GlobalVar, GlobalBuiltin, GlobalNative:
		case GlobalFunc:
			if g.Func < 0 || g.Func >= len(p.Funcs) {
				return fmt.Errorf("invalid bytecode: global %s: bad function %d", g.Name, g.Func)
			}
		case GlobalClass:
			if g.Class < 0 || g.Class >= len(p.Classes) {
				return fmt.Errorf("invalid bytecode: global %s: bad class %d", g.Name, g.Class)
			}
		default:
			return fmt.Errorf("invalid bytecode: global %d has unknown kind %d", i, g.Kind)
		}
	}
	if p.Main < 0 || p.Main >= len(p.Globals) || p.Globals[p.Main].Kind != GlobalFunc {
		return fmt.Errorf("invalid bytecode: main is not a function")
	}
	for i, c := range p.Classes {
		if c.Base < -1 || c.Base >= i || c.Native != "" && c.Base != -1 {
			return fmt.Errorf("invalid bytecode: class %s: bad base class %d", c.Name, c.Base)
		}
		for _, m := range c.Methods {
			if m.Func < -1 || m.Func >= len(p.Funcs) {
				return fmt.Errorf("invalid bytecode: %s::%s: bad function %d", c.Name, m.Name, m.Func)
			}
		}
	}
	if p.Init.Index != -1 {
		return fmt.Errorf("invalid bytecode: the init function has index %d", p.Init.Index)
	}
	if err := v.function(p.Init); err != nil {
		return err
	}
	for i, f := range p.Funcs {
		if f == nil {
			return fmt.Errorf("invalid bytecode: missing function")
		}
		if f.Index != i {
			return fmt.Errorf("invalid bytecode: function %d has index %d", i, f.Index)
		}
		if err := v.function(f); err != nil {
			return err
		}
	}
	return nil
}

type verifier struct {
	p *Program
}

func (v *verifier) function(f *Function) error {
	fail := func(pc int, format string, args ...any) error {
		return fmt.Errorf("invalid bytecode in %s at %d: %s", f.FuncName, pc, fmt.Sprintf(format, args...))
	}
	if f.NumParams < 0 || f.NumOptional < 0 || f.NumOptional > 255 || f.NumSlots < f.ParamSlots() ||
		f.NumSlots > maxCount || f.MaxStack < 0 || f.MaxStack > maxStack || f.NumCaches < 0 || f.NumCaches > maxCount {
		return fail(0, "bad parameter, slot or stack counts")
	}
	for i, u := range f.Upvalues {
		if u.Index < 0 || u.Index > maxCount {
			return fail(0, "bad upvalue %d", i)
		}
	}
	if len(f.Code) == 0 {
		return fail(0, "no code")
	}

	// Decode every instruction and check its operands.
	start := make([]bool, len(f.Code)) // instruction boundaries
	type instr struct {
		op       Op
		operands []int
	}
	instrs := map[int]instr{}
	cacheUser := map[int]int{}               // inline cache slot -> the pc of the one instruction using it
	generator, yields, returns := -1, -1, -1 // the pc of GENERATOR, of a YIELD, of a RETURN
	for pc := 0; pc < len(f.Code); {
		op := Op(f.Code[pc])
		info, ok := Info(op)
		if !ok {
			return fail(pc, "unknown opcode %d", op)
		}
		if pc+op.Size() > len(f.Code) {
			return fail(pc, "%s is cut off", info.Name)
		}
		var operands []int
		at := pc + 1
		for _, w := range info.Operands {
			if w == 2 {
				operands = append(operands, ReadU16(f.Code, at))
			} else {
				operands = append(operands, ReadU32(f.Code, at))
			}
			at += w
		}
		if err := v.operands(f, pc, op, operands); err != nil {
			return err
		}
		switch op {
		case OpGetMember, OpSetMember, OpCallMethod, OpCallMethodV, OpGetMemberL:
			// A cache entry belongs to one instruction: shared, it could
			// give one member name the method found for another.
			c := operands[len(operands)-1]
			if other, ok := cacheUser[c]; ok {
				return fail(pc, "%s uses cache %d, as the instruction at %d does", info.Name, c, other)
			}
			cacheUser[c] = pc
		case OpGenerator:
			if generator >= 0 {
				return fail(pc, "a second GENERATOR (the first is at %d)", generator)
			}
			generator = pc
		case OpYield, OpYieldValue, OpGenReturn:
			yields = pc
		case OpReturn, OpReturnNil:
			returns = pc
		}
		start[pc] = true
		instrs[pc] = instr{op, operands}
		pc += op.Size()
	}

	if returns >= 0 && generator >= 0 { // (a for loop runs a generator's steps in its own dispatch loop: a RETURN there would go on in the loop's body)
		return fail(returns, "%s in a generator function (it ends with GEN_RETURN)", Op(f.Code[returns]))
	}
	if yields >= 0 && generator < 0 {
		return fail(yields, "%s in a function that is not a generator (it has no GENERATOR)", Op(f.Code[yields]))
	}

	// Follow every path from the entry and from handlers, checking the
	// stack depth and the number of registered handlers at each instruction.
	depth := make([]int, len(f.Code))
	handlers := make([]int, len(f.Code))
	for i := range depth {
		depth[i] = -1
	}
	type item struct{ pc, depth, handlers int }
	work := []item{{0, 0, 0}}
	h := 0 // handlers at the instruction being looked at
	visit := func(pc, d, from int) error {
		if pc < 0 || pc >= len(f.Code) || !start[pc] {
			return fail(from, "jump to %d, which is not an instruction", pc)
		}
		if d > f.MaxStack {
			return fail(from, "stack depth %d exceeds the maximum %d", d, f.MaxStack)
		}
		work = append(work, item{pc, d, h})
		return nil
	}
	for len(work) > 0 {
		it := work[len(work)-1]
		work = work[:len(work)-1]
		if depth[it.pc] >= 0 {
			if depth[it.pc] != it.depth {
				return fail(it.pc, "stack depth %d here, %d on another path", it.depth, depth[it.pc])
			}
			if handlers[it.pc] != it.handlers {
				return fail(it.pc, "%d try handlers here, %d on another path", it.handlers, handlers[it.pc])
			}
			continue
		}
		depth[it.pc], handlers[it.pc] = it.depth, it.handlers
		h = it.handlers
		in := instrs[it.pc]
		pops, pushes := StackUse(in.op, in.operands)
		if it.depth < pops {
			return fail(it.pc, "%s needs %d values on the stack, there are %d", in.op, pops, it.depth)
		}
		after := it.depth - pops + pushes
		next := it.pc + in.op.Size()
		var err error
		switch in.op {
		case OpReturn, OpReturnNil, OpGenReturn:
			if h != 0 {
				return fail(it.pc, "returns with %d try handlers registered", h)
			}
			continue
		case OpThrow, OpRethrow:
			continue
		case OpGenerator: // the steps go on from the next instruction, with the frame as it was
			if h != 0 || it.depth != 0 {
				return fail(it.pc, "GENERATOR with %d try handlers and %d values on the stack (none are allowed)", h, it.depth)
			}
			err = visit(next, after, it.pc)
		case OpTryPop:
			if h == 0 {
				return fail(it.pc, "TRY_POP without a handler")
			}
			h--
			err = visit(next, after, it.pc)
		case OpJump:
			err = visit(in.operands[0], after, it.pc)
		case OpJumpIfFalse, OpJumpIfTrue:
			if err = visit(in.operands[0], after, it.pc); err == nil {
				err = visit(next, after, it.pc)
			}
		case OpCmpJump:
			if err = visit(in.operands[1], after, it.pc); err == nil {
				err = visit(next, after, it.pc)
			}
		case OpCmpJumpLK, OpCmpJumpLL:
			if err = visit(in.operands[3], after, it.pc); err == nil {
				err = visit(next, after, it.pc)
			}
		case OpCmpJumpK:
			if err = visit(in.operands[2], after, it.pc); err == nil {
				err = visit(next, after, it.pc)
			}
		case OpJumpIfArg:
			if err = visit(in.operands[1], after, it.pc); err == nil {
				err = visit(next, after, it.pc)
			}
		case OpSwitchTable: // always jumps
			err = visit(in.operands[1], after, it.pc)
			for _, t := range f.Tables[in.operands[0]].Targets {
				if err == nil && t >= 0 {
					err = visit(t, after, it.pc)
				}
			}
		case OpIterNext: // continues with n values pushed, or ends with none
			if err = visit(in.operands[2], it.depth, it.pc); err == nil {
				err = visit(next, after, it.pc)
			}
		case OpTryPush: // the handler starts with the thrown value pushed
			if err = visit(in.operands[1], it.depth+1, it.pc); err == nil { // the handler is gone there
				h++
				err = visit(next, after, it.pc)
			}
		default:
			if next >= len(f.Code) {
				return fail(it.pc, "the code runs past its end")
			}
			err = visit(next, after, it.pc)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// operands checks the operands of one instruction.
func (v *verifier) operands(f *Function, pc int, op Op, x []int) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("invalid bytecode in %s at %d: %s %s", f.FuncName, pc, op, fmt.Sprintf(format, args...))
	}
	p := v.p
	slot := func(i int) error {
		if i >= f.NumSlots {
			return fail("uses slot %d of %d", i, f.NumSlots)
		}
		return nil
	}
	name := func(k int) error {
		if k >= len(f.Consts) || f.Consts[k].Kind() != value.String {
			return fail("needs a string constant, %d is not one", k)
		}
		return nil
	}
	funcIndex := func(i int) error {
		if i >= len(p.Funcs) {
			return fail("refers to function %d of %d", i, len(p.Funcs))
		}
		return nil
	}
	switch op {
	case OpConst:
		if x[0] >= len(f.Consts) {
			return fail("refers to constant %d of %d", x[0], len(f.Consts))
		}
	case OpLoadLocal, OpStoreLocal, OpNewCell, OpLoadCell, OpStoreCell, OpIncLocal, OpDecLocal, OpAddLocal:
		return slot(x[0])
	case OpCmpJump:
		if x[0]&^CmpJumpIfTrue > CmpNe {
			return fail("has unknown comparison %d", x[0])
		}
	case OpArithLK, OpCmpJumpLK:
		if op == OpArithLK && (x[0] < int(OpAdd) || x[0] > int(OpShr)) {
			return fail("has unknown operation %d", x[0])
		}
		if op == OpCmpJumpLK && x[0]&^CmpJumpIfTrue > CmpNe {
			return fail("has unknown comparison %d", x[0])
		}
		if x[2] >= len(f.Consts) {
			return fail("refers to constant %d of %d", x[2], len(f.Consts))
		}
		return slot(x[1])
	case OpIndexLL:
		if err := slot(x[0]); err != nil {
			return err
		}
		return slot(x[1])
	case OpArithK, OpCmpJumpK:
		if op == OpArithK && (x[0] < int(OpAdd) || x[0] > int(OpShr)) {
			return fail("has unknown operation %d", x[0])
		}
		if op == OpCmpJumpK && x[0]&^CmpJumpIfTrue > CmpNe {
			return fail("has unknown comparison %d", x[0])
		}
		if x[1] >= len(f.Consts) {
			return fail("refers to constant %d of %d", x[1], len(f.Consts))
		}
	case OpArithLL, OpCmpJumpLL:
		if op == OpArithLL && (x[0] < int(OpAdd) || x[0] > int(OpShr)) {
			return fail("has unknown operation %d", x[0])
		}
		if op == OpCmpJumpLL && x[0]&^CmpJumpIfTrue > CmpNe {
			return fail("has unknown comparison %d", x[0])
		}
		if err := slot(x[1]); err != nil {
			return err
		}
		return slot(x[2])
	case OpGetMemberL:
		if x[2] >= f.NumCaches {
			return fail("uses cache %d of %d", x[2], f.NumCaches)
		}
		if err := slot(x[0]); err != nil {
			return err
		}
		return name(x[1])
	case OpIterClose, OpIterSend:
		return slot(x[0])
	case OpIterNext:
		if x[1] < 1 || x[1] > 2 {
			return fail("pushes %d values", x[1])
		}
		return slot(x[0])
	case OpLoadGlobal, OpStoreGlobal:
		if x[0] >= len(p.Globals) {
			return fail("refers to global %d of %d", x[0], len(p.Globals))
		}
	case OpGetMember, OpSetMember, OpCallMethod, OpCallMethodV:
		if c := x[len(x)-1]; c >= f.NumCaches {
			return fail("uses cache %d of %d", c, f.NumCaches)
		}
		return name(x[0])
	case OpCallDirect, OpLoadFunc, OpClosure:
		return funcIndex(x[0])
	case OpCallNative:
		if x[0] >= len(p.Globals) {
			return fail("refers to global %d of %d", x[0], len(p.Globals))
		}
		return name(x[1])
	case OpLoadStatic, OpStoreStatic:
		if x[0] >= len(p.Classes) || x[1] >= len(p.Classes[x[0]].Statics) {
			return fail("refers to a static data member that does not exist")
		}
	case OpLoadUpvalue, OpStoreUpvalue:
		if x[0] >= len(f.Upvalues) {
			return fail("refers to upvalue %d of %d", x[0], len(f.Upvalues))
		}
	case OpTryPush:
		if x[0] > 1 {
			return fail("has unknown handler kind %d", x[0])
		}
	case OpBreak:
		return fail("is only for the debugger, not in programs")
	case OpSwitchTable:
		if x[0] >= len(f.Tables) {
			return fail("refers to jump table %d of %d", x[0], len(f.Tables))
		}
	}
	return nil
}
