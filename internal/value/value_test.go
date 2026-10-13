package value

import (
	"math"
	"testing"
)

func i(x int64) Value   { return MakeInt(x) }
func f(x float64) Value { return MakeFloat(x) }
func s(x string) Value  { return MakeString(x) }

func TestArith(t *testing.T) {
	tests := []struct {
		op   ArithOp
		a, b Value
		want Value
	}{
		{Add, i(2), i(3), i(5)},
		{Add, i(math.MaxInt64), i(1), i(math.MinInt64)}, // wraps
		{Sub, i(math.MinInt64), i(1), i(math.MaxInt64)},
		{Mul, i(math.MaxInt64), i(2), i(-2)},
		{Div, i(7), i(2), i(3)},
		{Div, i(-7), i(2), i(-3)}, // truncates toward zero
		{Div, i(math.MinInt64), i(-1), i(math.MinInt64)},
		{Mod, i(-7), i(2), i(-1)}, // sign of the dividend
		{Mod, i(7), i(-2), i(1)},
		{Mod, i(math.MinInt64), i(-1), i(0)},
		{Add, i(1), f(0.5), f(1.5)},
		{Mul, f(2), i(3), f(6)},
		{Div, i(1), i(2), i(0)},
		{Div, i(1), f(2), f(0.5)},
		{Div, f(1), f(0), f(math.Inf(1))},
		{Div, i(1), f(0), f(math.Inf(1))},
		{Mod, f(7.5), i(2), f(1.5)},
		{Add, s("ab"), s("cd"), s("abcd")},
		{BitAnd, i(12), i(10), i(8)},
		{BitOr, i(12), i(10), i(14)},
		{BitXor, i(12), i(10), i(6)},
		{Shl, i(1), i(63), i(math.MinInt64)},
		{Shr, i(-8), i(1), i(-4)}, // arithmetic shift
		{Shr, i(math.MinInt64), i(63), i(-1)},
		{Shl, i(5), i(0), i(5)},
	}
	for _, tt := range tests {
		got, err := Arith(tt.op, tt.a, tt.b)
		if err != nil {
			t.Errorf("%s %s %s: %v", tt.a.Repr(), tt.op, tt.b.Repr(), err)
			continue
		}
		if got.Kind() != tt.want.Kind() || !Equal(got, tt.want) {
			t.Errorf("%s %s %s = %s (%s), want %s (%s)", tt.a.Repr(), tt.op, tt.b.Repr(),
				got.Repr(), got.Kind(), tt.want.Repr(), tt.want.Kind())
		}
	}
}

func TestArithErrors(t *testing.T) {
	tests := []struct {
		op        ArithOp
		a, b      Value
		kind, msg string
	}{
		{Div, i(1), i(0), ZeroDivision, "integer division by zero"},
		{Mod, i(1), i(0), ZeroDivision, "integer modulo by zero"},
		{Shl, i(1), i(64), RangeError, "shift count 64 out of range 0..63"},
		{Shr, i(1), i(-1), RangeError, "shift count -1 out of range 0..63"},
		{Add, s("a"), i(1), TypeError, "cannot apply + to string and int"},
		{Sub, s("a"), s("b"), TypeError, "cannot apply - to string and string"},
		{BitAnd, f(1), i(1), TypeError, "cannot apply & to float and int"},
		{Shl, i(1), f(1), TypeError, "cannot apply << to int and float"},
		{Add, NilValue, i(1), TypeError, "cannot apply + to nil and int"},
		{Mul, True, i(1), TypeError, "cannot apply * to bool and int"},
	}
	for _, tt := range tests {
		_, err := Arith(tt.op, tt.a, tt.b)
		e, ok := err.(*Error)
		if !ok || e.Kind != tt.kind || e.Msg != tt.msg {
			t.Errorf("%s %s %s: got %v, want %s: %s", tt.a.Repr(), tt.op, tt.b.Repr(), err, tt.kind, tt.msg)
		}
	}
}

func TestEqualAndCompare(t *testing.T) {
	eq := []struct {
		a, b Value
		want bool
	}{
		{i(1), f(1), true},
		{i(1), f(1.5), false},
		{f(math.NaN()), f(math.NaN()), false},
		{i(math.MaxInt64), f(9223372036854775807), false}, // the float is 2^63
		{i(1 << 53), f(1 << 53), true},
		{i(1<<53 + 1), f(1 << 53), false}, // would be equal after int→float
		{s("a"), s("a"), true},
		{s("a"), s("b"), false},
		{NilValue, NilValue, true},
		{NilValue, False, false},
		{i(0), False, false},
		{s("1"), i(1), false},
		{True, True, true},
	}
	for _, tt := range eq {
		if got := Equal(tt.a, tt.b); got != tt.want {
			t.Errorf("%s == %s: got %v", tt.a.Repr(), tt.b.Repr(), got)
		}
	}
	v := MakeVector(nil)
	if !Equal(v, v) || Equal(v, MakeVector(nil)) {
		t.Errorf("vectors compare by identity")
	}

	lt := []struct {
		a, b Value
		want bool
	}{
		{i(1), i(2), true},
		{i(2), f(1.5), false},
		{f(1.5), i(2), true},
		{i(math.MaxInt64), f(9223372036854775807), true}, // 2^63-1 < 2^63
		{i(-3), f(-2.5), true},
		{i(-2), f(-2.5), false},
		{f(math.NaN()), i(1), false},
		{i(1), f(math.NaN()), false},
		{s("abc"), s("abd"), true},
		{s("ab"), s("abc"), true},
		{s("b"), s("abc"), false},
		{i(math.MinInt64), f(math.Inf(-1)), false},
		{i(math.MaxInt64), f(math.Inf(1)), true},
	}
	for _, tt := range lt {
		got, err := Compare(Less, tt.a, tt.b)
		if err != nil || got != tt.want {
			t.Errorf("%s < %s: got %v, %v", tt.a.Repr(), tt.b.Repr(), got, err)
		}
	}
	if got, _ := Compare(LessEq, i(2), f(2)); !got {
		t.Errorf("2 <= 2.0 should be true")
	}
	if got, _ := Compare(GreaterEq, f(math.NaN()), f(math.NaN())); got {
		t.Errorf("NaN >= NaN should be false")
	}
	if _, err := Compare(Less, s("a"), i(1)); err == nil || err.Error() != "cannot compare string and int with <" {
		t.Errorf("string < int: got %v", err)
	}
	if _, err := Compare(Greater, NilValue, NilValue); err == nil {
		t.Errorf("nil > nil should be an error")
	}
}

func TestTruthy(t *testing.T) {
	for _, v := range []Value{NilValue, False, i(0), f(0), f(math.Copysign(0, -1))} {
		if v.Truthy() {
			t.Errorf("%s should be false", v.Repr())
		}
	}
	for _, v := range []Value{True, i(1), i(-1), f(0.5), f(math.NaN()), s(""), s("0"), MakeVector(nil)} {
		if !v.Truthy() {
			t.Errorf("%s should be true", v.Repr())
		}
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		v         Value
		str, repr string
	}{
		{NilValue, "nil", "nil"},
		{True, "true", "true"},
		{i(-42), "-42", "-42"},
		{f(1), "1.0", "1.0"},
		{f(1.5), "1.5", "1.5"},
		{f(0.1), "0.1", "0.1"},
		{f(1e21), "1e+21", "1e+21"},
		{f(1e-7), "1e-07", "1e-07"},
		{f(123456789), "1.23456789e+08", "1.23456789e+08"},
		{f(math.Inf(1)), "inf", "inf"},
		{f(math.Inf(-1)), "-inf", "-inf"},
		{f(math.NaN()), "nan", "nan"},
		{f(math.Copysign(0, -1)), "-0.0", "-0.0"},
		{s("a\"b\n"), "a\"b\n", `"a\"b\n"`},
		{s("\x00\x7f\xc3\xa9"), "\x00\x7f\xc3\xa9", `"\0\x7f` + "\xc3\xa9" + `"`},
		{MakeVector([]Value{i(1), s("x"), MakeVector(nil)}), `[1, "x", []]`, `[1, "x", []]`},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.str {
			t.Errorf("String() = %q, want %q", got, tt.str)
		}
		if got := tt.v.Repr(); got != tt.repr {
			t.Errorf("Repr() = %q, want %q", got, tt.repr)
		}
	}
}

func TestUnary(t *testing.T) {
	if v, _ := Neg(i(math.MinInt64)); v.AsInt() != math.MinInt64 {
		t.Errorf("-MinInt64 should wrap to itself")
	}
	if v, _ := Neg(f(1.5)); v.AsFloat() != -1.5 {
		t.Errorf("-1.5")
	}
	if v, _ := BitNot(i(0)); v.AsInt() != -1 {
		t.Errorf("~0")
	}
	if _, err := Neg(s("x")); err == nil || err.Error() != "cannot apply - to string" {
		t.Errorf("-string: %v", err)
	}
	if _, err := BitNot(f(1)); err == nil {
		t.Errorf("~float should fail")
	}
	if v, _ := IncDec(i(math.MaxInt64), 1); v.AsInt() != math.MinInt64 {
		t.Errorf("++ should wrap")
	}
	if v, _ := IncDec(f(0.5), -1); v.AsFloat() != -0.5 {
		t.Errorf("-- on float")
	}
	if _, err := IncDec(NilValue, -1); err == nil || err.Error() != "cannot apply -- to nil" {
		t.Errorf("-- nil: %v", err)
	}
}

func TestMapKeys(t *testing.T) {
	m := NewMap()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(m.Set(i(1), s("int")))
	must(m.Set(f(1), s("float"))) // same key: keeps the key 1, updates the value
	must(m.Set(f(math.Copysign(0, -1)), s("zero")))
	must(m.Set(i(0), s("zero again")))
	must(m.Set(f(1.5), s("x")))
	must(m.Set(s("1"), s("string")))
	if m.Len() != 4 {
		t.Fatalf("Len = %d, want 4", m.Len())
	}
	if got := MakeMap(m).Repr(); got != `{1: "float", -0.0: "zero again", 1.5: "x", "1": "string"}` {
		t.Errorf("map = %s", got)
	}
	for _, k := range []Value{i(1), f(1), f(1.5), s("1"), i(0)} {
		if _, ok, err := m.Get(k); !ok || err != nil {
			t.Errorf("Get(%s): ok=%v err=%v", k.Repr(), ok, err)
		}
	}
	if _, ok, _ := m.Get(f(1.25)); ok {
		t.Errorf("1.25 should not be a key")
	}
	for _, k := range []Value{NilValue, True, MakeVector(nil), f(math.NaN())} {
		if err := m.Set(k, i(1)); err == nil {
			t.Errorf("Set(%s) should fail", k.Repr())
		}
	}
	// 2^63 as a float is not an int64; it stays a float key.
	must(m.Set(f(9223372036854775808.0), s("big")))
	if _, ok, _ := m.Get(i(math.MinInt64)); ok {
		t.Errorf("2^63 and -2^63 must be different keys")
	}
}

func TestMapDeleteAndCompact(t *testing.T) {
	m := NewMap()
	for n := 0; n < 100; n++ {
		m.Set(i(int64(n)), i(int64(n*n)))
	}
	for n := 0; n < 90; n++ {
		if ok, _ := m.Delete(i(int64(n))); !ok {
			t.Fatalf("Delete(%d) = false", n)
		}
	}
	if ok, _ := m.Delete(i(5)); ok {
		t.Errorf("deleting twice should report false")
	}
	if len(m.entries) >= 100 {
		t.Errorf("entries not compacted: %d", len(m.entries))
	}
	keys := m.Keys()
	if len(keys) != 10 || keys[0].AsInt() != 90 || keys[9].AsInt() != 99 {
		t.Errorf("keys after delete: %v", MakeVector(keys).Repr())
	}
	if v, ok, _ := m.Get(i(95)); !ok || v.AsInt() != 9025 {
		t.Errorf("Get(95) after compaction = %v, %v", v.Repr(), ok)
	}
}

func TestIterator(t *testing.T) {
	m := NewMap()
	m.Set(s("a"), i(1))
	m.Set(s("b"), i(2))
	it, _ := NewIterator(MakeMap(m))
	k, v, ok := it.Next()
	if !ok || k.AsString() != "a" || v.AsInt() != 1 {
		t.Fatalf("first: %v %v %v", k.Repr(), v.Repr(), ok)
	}
	m.Delete(s("b"))
	m.Set(s("c"), i(3)) // added after the snapshot: not visited
	if _, _, ok := it.Next(); ok {
		t.Errorf("deleted and new keys should not be visited")
	}
	if _, err := NewIterator(i(1)); err == nil {
		t.Errorf("iterating an int should fail")
	}
}
