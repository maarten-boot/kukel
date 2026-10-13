package bytecode

// StackUse returns how many values an instruction pops and pushes. For
// ITER_NEXT it is the path that continues the loop (the end of the loop
// pushes nothing); TRY_PUSH's handler starts with one more value than the
// instruction had.
func StackUse(op Op, operands []int) (pops, pushes int) {
	arg := func(i int) int {
		if i < len(operands) {
			return operands[i]
		}
		return 0
	}
	switch op {
	case OpConst, OpNil, OpTrue, OpFalse, OpLoadLocal, OpLoadGlobal, OpLoadThis,
		OpLoadMember, OpLoadFunc, OpLoadStatic, OpLoadCell, OpLoadUpvalue, OpClosure:
		return 0, 1
	case OpPop, OpStoreLocal, OpStoreGlobal, OpStoreMember, OpStoreStatic, OpStoreCell,
		OpStoreUpvalue, OpNewCell, OpJumpIfFalse, OpJumpIfTrue, OpReturn, OpThrow, OpRethrow:
		return 1, 0
	case OpCmpJump:
		return 2, 0
	case OpDup:
		return 1, 2
	case OpDup2:
		return 2, 4
	case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr,
		OpEq, OpNe, OpLt, OpLe, OpGt, OpGe, OpAppend, OpExtend, OpIndexGet, OpSetMember,
		OpCallV, OpCallMethodV, OpNewV:
		return 2, 1
	case OpNeg, OpNot, OpBitNot, OpInc, OpDec, OpIterInit, OpGetMember:
		return 1, 1
	case OpIndexSet:
		return 3, 1
	case OpCall, OpNew:
		return arg(0) + 1, 1
	case OpCallMethod, OpCallDirect:
		return arg(1) + 1, 1
	case OpMakeVector:
		return arg(0), 1
	case OpMakeMap:
		return 2 * arg(0), 1
	case OpIterNext:
		return 0, arg(1)
	}
	return 0, 0 // NOP, JUMP, JUMP_IF_ARG, RETURN_NIL, TRY_PUSH, TRY_POP
}
