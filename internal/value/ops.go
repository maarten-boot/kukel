package value

import (
	"fmt"
	"math"
)

// Error is a runtime error raised by an operation. Kind names the class of
// error (it becomes the "kind" field of the Error object in milestone 7).
type Error struct {
	Kind string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Runtime error kinds.
const (
	TypeError     = "TypeError"
	ZeroDivision  = "ZeroDivisionError"
	RangeError    = "RangeError"
	ArgumentError = "ArgumentError"
	StackOverflow = "StackOverflowError"
	IOError       = "IOError"
	InternalError = "InternalError"
)

// Errorf returns a runtime error of the given kind.
func Errorf(kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// ArithOp is a binary arithmetic or bitwise operator.
type ArithOp uint8

const (
	Add ArithOp = iota
	Sub
	Mul
	Div
	Mod
	BitAnd
	BitOr
	BitXor
	Shl
	Shr
)

var arithNames = [...]string{Add: "+", Sub: "-", Mul: "*", Div: "/", Mod: "%",
	BitAnd: "&", BitOr: "|", BitXor: "^", Shl: "<<", Shr: ">>"}

func (op ArithOp) String() string { return arithNames[op] }

func (v Value) isNumber() bool { return v.kind == Int || v.kind == Float }

// toFloat converts a number to float64.
func (v Value) toFloat() float64 {
	if v.kind == Int {
		return float64(v.AsInt())
	}
	return v.AsFloat()
}

func typeErr(op string, a, b Value) error {
	return Errorf(TypeError, "cannot apply %s to %s and %s", op, a.kind, b.kind)
}

// Arith applies a binary arithmetic or bitwise operator.
//
// Integers wrap around (two's complement); an int combined with a float
// gives a float; int / int truncates toward zero; / and % by integer 0 are
// errors; + also concatenates two strings. Bitwise operators and shifts
// take ints only; a shift count must be in 0..63; >> is an arithmetic shift.
func Arith(op ArithOp, a, b Value) (Value, error) {
	if a.kind == Int && b.kind == Int {
		x, y := a.AsInt(), b.AsInt()
		switch op {
		case Add:
			return MakeInt(x + y), nil
		case Sub:
			return MakeInt(x - y), nil
		case Mul:
			return MakeInt(x * y), nil
		case Div:
			if y == 0 {
				return NilValue, Errorf(ZeroDivision, "integer division by zero")
			}
			return MakeInt(x / y), nil // MinInt64 / -1 wraps to MinInt64
		case Mod:
			if y == 0 {
				return NilValue, Errorf(ZeroDivision, "integer modulo by zero")
			}
			return MakeInt(x % y), nil
		case BitAnd:
			return MakeInt(x & y), nil
		case BitOr:
			return MakeInt(x | y), nil
		case BitXor:
			return MakeInt(x ^ y), nil
		case Shl, Shr:
			if y < 0 || y > 63 {
				return NilValue, Errorf(RangeError, "shift count %d out of range 0..63", y)
			}
			if op == Shl {
				return MakeInt(x << uint(y)), nil
			}
			return MakeInt(x >> uint(y)), nil
		}
	}
	if a.isNumber() && b.isNumber() {
		x, y := a.toFloat(), b.toFloat()
		switch op {
		case Add:
			return MakeFloat(x + y), nil
		case Sub:
			return MakeFloat(x - y), nil
		case Mul:
			return MakeFloat(x * y), nil
		case Div:
			return MakeFloat(x / y), nil
		case Mod:
			return MakeFloat(math.Mod(x, y)), nil
		}
		return NilValue, typeErr(op.String(), a, b)
	}
	if op == Add && a.kind == String && b.kind == String {
		return MakeString(a.AsString() + b.AsString()), nil
	}
	return NilValue, typeErr(op.String(), a, b)
}

// Neg returns -v.
func Neg(v Value) (Value, error) {
	switch v.kind {
	case Int:
		return MakeInt(-v.AsInt()), nil
	case Float:
		return MakeFloat(-v.AsFloat()), nil
	}
	return NilValue, Errorf(TypeError, "cannot apply - to %s", v.kind)
}

// BitNot returns ~v.
func BitNot(v Value) (Value, error) {
	if v.kind == Int {
		return MakeInt(^v.AsInt()), nil
	}
	return NilValue, Errorf(TypeError, "cannot apply ~ to %s", v.kind)
}

// IncDec adds delta (1 or -1) to a number, for ++ and --.
func IncDec(v Value, delta int64) (Value, error) {
	switch v.kind {
	case Int:
		return MakeInt(v.AsInt() + delta), nil
	case Float:
		return MakeFloat(v.AsFloat() + float64(delta)), nil
	}
	op := "++"
	if delta < 0 {
		op = "--"
	}
	return NilValue, Errorf(TypeError, "cannot apply %s to %s", op, v.kind)
}

// cmpNumbers compares two numbers exactly (also an int with a float). ok is
// false when a NaN is involved.
func cmpNumbers(a, b Value) (c int, ok bool) {
	switch {
	case a.kind == Int && b.kind == Int:
		return cmp3(a.AsInt(), b.AsInt()), true
	case a.kind == Float && b.kind == Float:
		x, y := a.AsFloat(), b.AsFloat()
		if x != x || y != y {
			return 0, false
		}
		return cmp3(x, y), true
	case a.kind == Int:
		c, ok := cmpIntFloat(a.AsInt(), b.AsFloat())
		return c, ok
	default:
		c, ok := cmpIntFloat(b.AsInt(), a.AsFloat())
		return -c, ok
	}
}

func cmp3[T int64 | float64](x, y T) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// cmpIntFloat compares i with f without losing precision.
func cmpIntFloat(i int64, f float64) (int, bool) {
	switch {
	case f != f:
		return 0, false
	case f >= 9223372036854775808.0: // 2^63
		return -1, true
	case f < -9223372036854775808.0:
		return 1, true
	}
	t := math.Trunc(f)
	if c := cmp3(i, int64(t)); c != 0 {
		return c, true
	}
	return cmp3(t, f), true // i == trunc(f): the fraction decides
}

// Equal reports whether a == b: numbers by value (1 == 1.0), strings by
// bytes, nil and booleans by value, everything else by identity. Values of
// different kinds are never equal (except int and float).
func Equal(a, b Value) bool {
	if a.isNumber() && b.isNumber() {
		c, ok := cmpNumbers(a, b)
		return ok && c == 0
	}
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case Nil:
		return true
	case Bool:
		return a.num == b.num
	case String:
		return a.AsString() == b.AsString()
	}
	return a.obj == b.obj
}

// CmpOp is a relational operator.
type CmpOp uint8

const (
	Less CmpOp = iota
	LessEq
	Greater
	GreaterEq
)

var cmpNames = [...]string{Less: "<", LessEq: "<=", Greater: ">", GreaterEq: ">="}

func (op CmpOp) String() string { return cmpNames[op] }

// Compare applies a relational operator: numbers with numbers, strings
// with strings (byte order); other combinations are type errors. Any
// comparison with NaN is false.
func Compare(op CmpOp, a, b Value) (bool, error) {
	var c int
	switch {
	case a.isNumber() && b.isNumber():
		var ok bool
		if c, ok = cmpNumbers(a, b); !ok {
			return false, nil
		}
	case a.kind == String && b.kind == String:
		x, y := a.AsString(), b.AsString()
		switch {
		case x < y:
			c = -1
		case x > y:
			c = 1
		}
	default:
		return false, Errorf(TypeError, "cannot compare %s and %s with %s", a.kind, b.kind, op)
	}
	switch op {
	case Less:
		return c < 0, nil
	case LessEq:
		return c <= 0, nil
	case Greater:
		return c > 0, nil
	}
	return c >= 0, nil
}
