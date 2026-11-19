package value

import (
	"math"
	"math/big"
	"strings"
	"testing"
)

// d parses a decimal for the tests.
func d(t testing.TB, text string) Value {
	t.Helper()
	v, err := ParseDecimal(text)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", text, err)
	}
	return v
}

func TestParseDecimal(t *testing.T) {
	tests := []struct{ in, want string }{
		{"12.50", "12.50"},
		{"3", "3"},
		{"0", "0"},
		{"0.00", "0.00"},
		{"-0.00", "0.00"},
		{".5", "0.5"},
		{"1.", "1"},
		{"+7.25", "7.25"},
		{"-12.50", "-12.50"},
		{"007.10", "7.10"},
		{"1.5e3", "1500"},
		{"1.50E+3", "1500"},
		{"2.5e-3", "0.0025"},
		{"1e3", "1000"},
		{"1.25e1", "12.5"},
		{"0e5", "0"},
		{"0.000e2", "0.0"},
		{"9223372036854775807", "9223372036854775807"},
		{"-9223372036854775808", "-9223372036854775808"}, // the big form
		{"123456789012345678901234567890.123", "123456789012345678901234567890.123"},
		{"0." + strings.Repeat("0", 999) + "1", "0." + strings.Repeat("0", 999) + "1"},
		{strings.Repeat("9", 1000), strings.Repeat("9", 1000)},
	}
	for _, tt := range tests {
		v, err := ParseDecimal(tt.in)
		if err != nil {
			t.Errorf("ParseDecimal(%q): %v", tt.in, err)
			continue
		}
		if v.Kind() != Decimal || v.String() != tt.want {
			t.Errorf("ParseDecimal(%q) = %s %q, want %q", tt.in, v.Kind(), v.String(), tt.want)
		}
	}
	bad := []struct{ in, kind string }{
		{"", ValueError}, {".", ValueError}, {"-", ValueError}, {"1e", ValueError},
		{"1e+", ValueError}, {"1.2.3", ValueError}, {" 1", ValueError}, {"1_000", ValueError},
		{"inf", ValueError}, {"nan", ValueError}, {"0x10", ValueError}, {"1d", ValueError},
		{strings.Repeat("9", 1001), RangeError},
		{"1e1000", RangeError}, // 1001 digits
		{"1e999999999999", RangeError},
		{"1e-1001", RangeError}, // scale 1001
		{"0e-1001", RangeError},
	}
	for _, tt := range bad {
		_, err := ParseDecimal(tt.in)
		if e, ok := err.(*Error); !ok || e.Kind != tt.kind {
			t.Errorf("ParseDecimal(%q) error %v, want a %s", tt.in, err, tt.kind)
		}
	}
	if v := d(t, "1e999"); len(v.String()) != 1000 {
		t.Errorf("1e999 has %d digits", len(v.String()))
	}
}

func TestDecimalForms(t *testing.T) {
	// Every value that fits the small form has it, whatever made it.
	big := d(t, "9223372036854775808") // 2^63: big
	if _, _, ok := big.decSmall(); ok {
		t.Error("2^63 is small")
	}
	v, err := Arith(Sub, big, i(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := v.decSmall(); !ok || v.String() != "9223372036854775807" {
		t.Errorf("2^63 - 1 = %s, small %v", v, ok)
	}
	if n := testing.AllocsPerRun(100, func() {
		x, _ := Arith(Add, makeDecSmall(1250, 2), makeDecSmall(199, 2))
		x, _ = Arith(Mul, x, i(3))
		_, _ = Arith(Sub, x, makeDecSmall(5, 1))
	}); n != 0 {
		t.Errorf("small decimals allocate: %v a run", n)
	}
}

func TestDecimalArith(t *testing.T) {
	tests := []struct {
		op         ArithOp
		a, b, want string // "i3" is the int 3, the others decimals
	}{
		{Add, "12.50", "1.5", "14.00"},
		{Add, "1.25", "1.25", "2.50"},
		{Sub, "1.00", "0.005", "0.995"},
		{Sub, "0.1", "0.3", "-0.2"},
		{Add, "0.1", "0.2", "0.3"},
		{Mul, "2.50", "i3", "7.50"},
		{Mul, "2.50", "1.10", "2.7500"},
		{Mul, "-0.5", "0.5", "-0.25"},
		{Mul, "0.00", "5", "0.00"},
		{Add, "i2", "0.50", "2.50"},
		{Sub, "i2", "0.50", "1.50"},
		{Mod, "7.5", "2", "1.5"},
		{Mod, "-7.5", "i2", "-1.5"}, // the sign of the left side
		{Mod, "7.50", "-0.2", "0.10"},
		{Div, "7.50", "i2", "3.75"},
		{Div, "10.00", "i4", "2.50"}, // the preferred scale
		{Div, "10", "i4", "2.5"},
		{Div, "1", "i3", "0.3333333333333333333333333333333333"},
		{Div, "2", "i3", "0.6666666666666666666666666666666667"},
		{Div, "-2", "i3", "-0.6666666666666666666666666666666667"},
		{Div, "i1", "8.0", "0.125"},
		{Div, "1.00", "i1", "1.00"},
		{Div, "6", "0.5", "12"},
		{Div, "6.0", "1.00", "6"}, // scale 1 - 2 is below 0
		{Div, "0.00", "i7", "0.00"},
		{Div, "i100", "0.1", "1000"},
		{Div, "12.34", "i100", "0.1234"},
		{Div, "1e40", "i3", "3333333333333333333333333333333333333333"}, // integer digits kept
		{Div, "9.99999999999999999999999999999999999", "i1", "10.00000000000000000000000000000000"},
		{Div, "1" + strings.Repeat("0", 40) + ".5", "i1", "10000000000000000000000000000000000000000"}, // 41 digits: integer rounded half to even
		{Div, "2.5", "i1", "2.5"},
		{Div, "1e-990", "i3", "0." + strings.Repeat("0", 990) + "3333333333"}, // scale 1000 at most
	}
	val := func(x string) Value {
		if strings.HasPrefix(x, "i") {
			v := d(t, x[1:])
			n, _ := v.IntValue()
			return MakeInt(n)
		}
		return d(t, x)
	}
	for _, tt := range tests {
		got, err := Arith(tt.op, val(tt.a), val(tt.b))
		if err != nil {
			t.Errorf("%s %s %s: %v", tt.a, tt.op, tt.b, err)
			continue
		}
		if got.Kind() != Decimal || got.String() != tt.want {
			t.Errorf("%s %s %s = %s %s, want %s", tt.a, tt.op, tt.b, got.Kind(), got, tt.want)
		}
	}
	errs := []struct {
		op   ArithOp
		a, b Value
		kind string
	}{
		{Div, d(t, "1"), d(t, "0.00"), ZeroDivision},
		{Mod, d(t, "1"), i(0), ZeroDivision},
		{Div, i(1), d(t, "0"), ZeroDivision},
		{Mul, d(t, "12.50"), f(1.1), TypeError},
		{Add, f(1.1), d(t, "1"), TypeError},
		{Add, d(t, "1"), s("a"), TypeError},
		{BitAnd, d(t, "1"), i(1), TypeError},
		{Shl, i(1), d(t, "1"), TypeError},
		{Mul, d(t, "0."+strings.Repeat("0", 599)+"1"), d(t, "0."+strings.Repeat("0", 599)+"1"), RangeError}, // scale 1200
		{Mul, d(t, strings.Repeat("9", 600)), d(t, strings.Repeat("9", 600)), RangeError},
	}
	for _, tt := range errs {
		_, err := Arith(tt.op, tt.a, tt.b)
		if e, ok := err.(*Error); !ok || e.Kind != tt.kind {
			t.Errorf("%s %s %s: error %v, want a %s", tt.a.Repr(), tt.op, tt.b.Repr(), err, tt.kind)
		}
	}
	_, err := Arith(Mul, d(t, "12.50"), f(1.1))
	if err == nil || !strings.Contains(err.Error(), "decimal(x)") {
		t.Errorf("decimal * float: %v", err)
	}
}

func TestDecimalUnary(t *testing.T) {
	v, _ := Neg(d(t, "12.50"))
	if v.String() != "-12.50" {
		t.Errorf("-12.50d = %s", v)
	}
	v, _ = Neg(d(t, "-9223372036854775808")) // big to small
	if v.String() != "9223372036854775808" {
		t.Errorf("-(-2^63) = %s", v)
	}
	for _, tt := range []struct {
		in    string
		delta int64
		want  string
	}{{"1.50", 1, "2.50"}, {"0.25", -1, "-0.75"}, {"9223372036854775807", 1, "9223372036854775808"}, {"0." + strings.Repeat("0", 29) + "1", 1, "1." + strings.Repeat("0", 29) + "1"}} {
		v, err := IncDec(d(t, tt.in), tt.delta)
		if err != nil || v.String() != tt.want {
			t.Errorf("%s + %d = %s %v, want %s", tt.in, tt.delta, v, err, tt.want)
		}
	}
	if _, err := BitNot(d(t, "1")); err == nil {
		t.Error("~1d works")
	}
}

func TestDecimalCompare(t *testing.T) {
	eq := []struct{ a, b Value }{
		{d(t, "12.50"), d(t, "12.5")},
		{d(t, "2"), i(2)},
		{d(t, "2.00"), f(2)},
		{d(t, "2.5"), f(2.5)},
		{d(t, "0.00"), i(0)},
		{d(t, "0"), f(math.Copysign(0, -1))},
		{d(t, "9223372036854775808.0"), f(9223372036854775808.0)},
		{d(t, "1"+strings.Repeat("0", 30)), d(t, "1e30")},
	}
	for _, tt := range eq {
		if !Equal(tt.a, tt.b) || !Equal(tt.b, tt.a) {
			t.Errorf("%s != %s", tt.a.Repr(), tt.b.Repr())
		}
		ka, _ := keyOf(tt.a)
		kb, _ := keyOf(tt.b)
		if ka != kb {
			t.Errorf("keys of %s and %s differ: %v %v", tt.a.Repr(), tt.b.Repr(), ka, kb)
		}
	}
	ne := []struct{ a, b Value }{
		{d(t, "0.1"), f(0.1)}, // the float is not exactly 0.1
		{d(t, "1"), s("1")},
		{d(t, "0"), NilValue},
		{d(t, "1.5"), f(math.NaN())},
		{d(t, "12.501"), d(t, "12.5")},
	}
	for _, tt := range ne {
		if Equal(tt.a, tt.b) || Equal(tt.b, tt.a) {
			t.Errorf("%s == %s", tt.a.Repr(), tt.b.Repr())
		}
	}
	ka, _ := keyOf(d(t, "0.1"))
	kb, _ := keyOf(f(0.1))
	kc, _ := keyOf(d(t, "0.100"))
	if ka == kb || ka != kc {
		t.Errorf("keys of 0.1d, 0.1 and 0.100d: %v %v %v", ka, kb, kc)
	}
	less := []struct{ a, b Value }{
		{d(t, "12.49"), d(t, "12.5")},
		{d(t, "-1"), i(0)},
		{i(2), d(t, "2.01")},
		{d(t, "0.1"), f(0.1)}, // 0.1 is a little more than 0.1 as a float
		{f(math.Inf(-1)), d(t, "-1e999")},
		{d(t, "1e999"), f(math.Inf(1))},
		{d(t, strings.Repeat("9", 30)), d(t, "1e30")},
	}
	for _, tt := range less {
		if ok, err := Compare(Less, tt.a, tt.b); !ok || err != nil {
			t.Errorf("%s < %s: %v %v", tt.a.Repr(), tt.b.Repr(), ok, err)
		}
		if ok, err := Compare(GreaterEq, tt.a, tt.b); ok || err != nil {
			t.Errorf("%s >= %s: %v %v", tt.a.Repr(), tt.b.Repr(), ok, err)
		}
	}
	if ok, err := Compare(Less, d(t, "1"), f(math.NaN())); ok || err != nil {
		t.Errorf("1d < nan: %v %v", ok, err)
	}
	if _, err := Compare(Less, d(t, "1"), s("2")); err == nil {
		t.Error("1d < \"2\" works")
	}
}

func TestDecimalValue(t *testing.T) {
	v := d(t, "12.50")
	if v.Kind().String() != "decimal" || v.String() != "12.50" || v.Repr() != "12.50d" {
		t.Errorf("12.50d: %s %s %s", v.Kind(), v.String(), v.Repr())
	}
	if vec := MakeVector([]Value{v, f(12.5)}); vec.String() != "[12.50d, 12.5]" {
		t.Errorf("vector: %s", vec)
	}
	if d(t, "0.00").Truthy() || !d(t, "0.01").Truthy() || d(t, "-0").Truthy() {
		t.Error("truth of decimals")
	}
	for _, tt := range []struct {
		in   string
		n    int64
		isOK bool
	}{{"2.00", 2, true}, {"2.5", 0, false}, {"-9223372036854775808", math.MinInt64, true}, {"9223372036854775808", 0, false}, {"0e-30", 0, true}, {"1" + strings.Repeat("0", 25) + "e-25", 1, true}} {
		n, ok := d(t, tt.in).IntValue()
		if n != tt.n || ok != tt.isOK {
			t.Errorf("IntValue(%s) = %d %v", tt.in, n, ok)
		}
	}
	m := NewMap()
	_ = m.Set(d(t, "2.00"), s("a"))
	_ = m.Set(i(2), s("b"))
	_ = m.Set(d(t, "0.1"), s("c"))
	_ = m.Set(f(0.1), s("d"))
	if m.Len() != 3 {
		t.Errorf("map of 2.00d, 2, 0.1d, 0.1: %d keys", m.Len())
	}
}

func TestDecimalConversions(t *testing.T) {
	for _, tt := range []struct {
		f    float64
		want string
	}{{0.1, "0.1"}, {2.5, "2.5"}, {100, "100"}, {1e21, "1000000000000000000000"}, {1.5e-7, "0.00000015"}, {-0.0, "0"}} {
		v, err := DecimalFromFloat(tt.f)
		if err != nil || v.String() != tt.want {
			t.Errorf("DecimalFromFloat(%v) = %s %v, want %s", tt.f, v, err, tt.want)
		}
	}
	if _, err := DecimalFromFloat(math.Inf(1)); err == nil {
		t.Error("decimal(inf) works")
	}
	if f := d(t, "0.1").DecimalToFloat(); f != 0.1 {
		t.Errorf("float(0.1d) = %v", f)
	}
	if f := d(t, "1e999").DecimalToFloat(); !math.IsInf(f, 1) {
		t.Errorf("float(1e999d) = %v", f)
	}
	rounds := []struct {
		in    string
		scale int
		mode  RoundMode
		want  string
	}{
		{"7.8750", 2, HalfAway, "7.88"},
		{"7.8650", 2, HalfAway, "7.87"},
		{"7.8650", 2, HalfEven, "7.86"},
		{"7.8750", 2, HalfEven, "7.88"},
		{"-2.5", 0, HalfAway, "-3"},
		{"-2.5", 0, HalfEven, "-2"},
		{"-2.5", 0, Down, "-2"},
		{"-2.1", 0, Floor, "-3"},
		{"-2.1", 0, Ceil, "-2"},
		{"2.1", 0, Ceil, "3"},
		{"1.5", 3, HalfAway, "1.500"},
		{"0.004", 2, HalfAway, "0.00"},
		{"0.005", 2, HalfAway, "0.01"},
	}
	for _, tt := range rounds {
		v, err := DecimalRound(d(t, tt.in), tt.scale, tt.mode)
		if err != nil || v.String() != tt.want {
			t.Errorf("round(%s, %d, %d) = %s %v, want %s", tt.in, tt.scale, tt.mode, v, err, tt.want)
		}
	}
	if v, _ := DecimalRound(i(3), 2, HalfAway); v.Repr() != "3.00d" {
		t.Errorf("round(3, 2) = %s", v.Repr())
	}
	if _, err := DecimalRound(d(t, "1"), 1001, HalfAway); err == nil {
		t.Error("round to scale 1001 works")
	}
	for _, tt := range []struct {
		in   string
		mode RoundMode
		want int64
	}{{"2.5", HalfAway, 3}, {"-2.5", HalfAway, -3}, {"2.9", Down, 2}, {"-2.9", Down, -2}, {"-2.1", Floor, -3}, {"2.1", Ceil, 3}, {"7", Down, 7}} {
		n, err := DecimalToInt(d(t, tt.in), tt.mode)
		if err != nil || n != tt.want {
			t.Errorf("int(%s, %d) = %d %v, want %d", tt.in, tt.mode, n, err, tt.want)
		}
	}
	if _, err := DecimalToInt(d(t, "9223372036854775808"), Down); err == nil {
		t.Error("int(2^63 d) works")
	}
	pows := []struct {
		in   string
		n    int64
		want string
	}{{"1.5", 2, "2.25"}, {"1.10", 3, "1.331000"}, {"7", 0, "1"}, {"0.0", 3, "0.000"}, {"-2", 3, "-8"}, {"10", 999, "1" + strings.Repeat("0", 999)}}
	for _, tt := range pows {
		v, err := DecimalPow(d(t, tt.in), tt.n)
		if err != nil || v.String() != tt.want {
			t.Errorf("pow(%s, %d) = %s %v, want %s", tt.in, tt.n, v, err, tt.want)
		}
	}
	for _, tt := range []struct {
		in string
		n  int64
	}{{"10", 1000}, {"2", 1 << 40}, {"0.1", 1001}, {"1.5", -1}} {
		if _, err := DecimalPow(d(t, tt.in), tt.n); err == nil {
			t.Errorf("pow(%s, %d) works", tt.in, tt.n)
		}
	}
}

// FuzzDecimal checks +, -, * and % against big.Rat, and that / is within
// half a unit of its last digit (exact when it says so).
func FuzzDecimal(f *testing.F) {
	for _, seed := range [][2]string{{"12.50", "1.5"}, {"1", "3"}, {"-7.5", "2"}, {"9223372036854775807", "0.01"}, {"1e40", "7"}, {"0.000001", "-3e5"}} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, x, y string) {
		a, err1 := ParseDecimal(x)
		b, err2 := ParseDecimal(y)
		if err1 != nil || err2 != nil || len(x) > 60 || len(y) > 60 {
			return
		}
		ra, rb := a.decRat(), b.decRat()
		check := func(op ArithOp, want *big.Rat) {
			got, err := Arith(op, a, b)
			if err != nil {
				if e, ok := err.(*Error); ok && e.Kind == RangeError {
					return
				}
				t.Fatalf("%s %s %s: %v", x, op, y, err)
			}
			if got.decRat().Cmp(want) != 0 {
				t.Fatalf("%s %s %s = %s, want %s", x, op, y, got, want.FloatString(40))
			}
			if again, err := ParseDecimal(got.String()); err != nil || !Equal(again, got) || again.String() != got.String() {
				t.Fatalf("%s does not read back: %s %v", got, again, err)
			}
		}
		check(Add, new(big.Rat).Add(ra, rb))
		check(Sub, new(big.Rat).Sub(ra, rb))
		check(Mul, new(big.Rat).Mul(ra, rb))
		if b.decSign() == 0 {
			return
		}
		q := new(big.Rat).Quo(ra, rb)
		n := new(big.Int).Quo(q.Num(), q.Denom()) // truncated
		r := new(big.Rat).Sub(q, new(big.Rat).SetInt(n))
		_ = r
		m := new(big.Rat).Sub(ra, new(big.Rat).Mul(rb, new(big.Rat).SetInt(n)))
		check(Mod, m)
		got, err := Arith(Div, a, b)
		if err != nil {
			if e, ok := err.(*Error); ok && e.Kind == RangeError {
				return
			}
			t.Fatalf("%s / %s: %v", x, y, err)
		}
		half := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Mul(big.NewInt(2), pow10(got.decScaleOf())))
		diff := new(big.Rat).Sub(got.decRat(), q)
		if diff.Abs(diff).Cmp(half) > 0 {
			t.Fatalf("%s / %s = %s, off by %s", x, y, got, diff.FloatString(50))
		}
		c, _ := got.DecimalParts()
		if digits := len(new(big.Int).Abs(c).String()); digits > DivisionDigits && got.decScaleOf() > 0 {
			t.Fatalf("%s / %s = %s: %d digits", x, y, got, digits)
		}
	})
}
