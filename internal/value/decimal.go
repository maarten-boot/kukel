package value

import (
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"strings"
)

// A decimal is a number in base 10: a coefficient c and a scale s (the
// digits after the point), its value c / 10^s. The scale is kept, as in
// SQL's NUMERIC: 12.50 has c = 1250 and s = 2. See docs/future.md §14.
//
// A decimal has two forms, chosen by its size and never seen outside
// this file:
//
//   - small: c fits in an int64 other than MinInt64; num holds c and obj a
//     *decScale, a pointer into decScales, so that it does not allocate;
//   - big: obj holds a *decBig, which is never changed once made.
//
// Every decimal that fits the small form has it, so two equal decimals of
// one scale have the same form.

// MaxDecimalDigits and MaxDecimalScale bound a decimal: at most that many
// significant digits, and a scale from 0 to MaxDecimalScale (F-69). A
// result beyond them is a RangeError.
const (
	MaxDecimalDigits = 1000
	MaxDecimalScale  = 1000
)

// DivisionDigits is the number of significant digits / gives when the
// quotient is not exact (F-67).
const DivisionDigits = 34

type decScale int16

var decScales [MaxDecimalScale + 1]decScale

func init() {
	for i := range decScales {
		decScales[i] = decScale(i)
	}
}

type decBig struct {
	coef  *big.Int
	scale int
}

var (
	bigOne     = big.NewInt(1)
	bigTen     = big.NewInt(10)
	maxCoef    = new(big.Int).Sub(new(big.Int).Exp(bigTen, big.NewInt(MaxDecimalDigits), nil), bigOne) // 10^1000 - 1
	pow10Small [19]int64
)

func init() {
	p := int64(1)
	for i := range pow10Small {
		pow10Small[i] = p
		p *= 10
	}
}

// pow10 returns 10^n as a new big.Int.
func pow10(n int) *big.Int {
	if n < len(pow10Small) {
		return big.NewInt(pow10Small[n])
	}
	return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil)
}

func decRangeErr(what string) error {
	return Errorf(RangeError, "decimal %s: more than %d digits or a scale above %d", what, MaxDecimalDigits, MaxDecimalScale)
}

// makeDecSmall makes the small form; c must not be MinInt64 and scale must
// be in 0..MaxDecimalScale.
func makeDecSmall(c int64, scale int) Value {
	return Value{kind: Decimal, num: uint64(c), obj: &decScales[scale]}
}

// makeDec makes a decimal of c / 10^scale (c is not changed or kept by
// the caller afterwards), or a RangeError naming what made it.
func makeDec(c *big.Int, scale int, what string) (Value, error) {
	if scale < 0 || scale > MaxDecimalScale {
		return NilValue, decRangeErr(what)
	}
	if c.IsInt64() && c.Int64() != math.MinInt64 {
		return makeDecSmall(c.Int64(), scale), nil
	}
	if c.CmpAbs(maxCoef) > 0 {
		return NilValue, decRangeErr(what)
	}
	return Value{kind: Decimal, obj: &decBig{coef: c, scale: scale}}, nil
}

// MakeDecimal makes the decimal c / 10^scale; c is copied.
func MakeDecimal(c *big.Int, scale int) (Value, error) {
	return makeDec(new(big.Int).Set(c), scale, "value")
}

// small gives the parts of a decimal in the small form.
func (v Value) decSmall() (c int64, scale int, ok bool) {
	if s, ok := v.obj.(*decScale); ok {
		return int64(v.num), int(*s), true
	}
	return 0, 0, false
}

// DecimalParts gives the coefficient (a new big.Int) and the scale of a
// decimal, or of an int (scale 0).
func (v Value) DecimalParts() (*big.Int, int) {
	if v.kind == Int {
		return big.NewInt(v.AsInt()), 0
	}
	if c, s, ok := v.decSmall(); ok {
		return big.NewInt(c), s
	}
	d := v.obj.(*decBig)
	return new(big.Int).Set(d.coef), d.scale
}

// decScaleOf gives the scale of a decimal (0 for an int).
func (v Value) decScaleOf() int {
	if v.kind == Int {
		return 0
	}
	if s, ok := v.obj.(*decScale); ok {
		return int(*s)
	}
	return v.obj.(*decBig).scale
}

// decSign gives -1, 0 or 1.
func (v Value) decSign() int {
	if c, _, ok := v.decSmall(); ok {
		return cmp3(c, 0)
	}
	return v.obj.(*decBig).coef.Sign()
}

// ParseDecimal reads a decimal: an optional sign, digits with an optional
// point (at least one digit), and an optional exponent (e or E, an
// optional sign, digits). The scale is the number of digits after the
// point less the exponent, and at least 0: "1.50" has scale 2, "1.5e3"
// is 1500, "2.5e-3" is 0.0025. Nothing else is allowed (no spaces, no _,
// no inf or nan): callers remove what they allow first. A text that is
// not such a number is a ValueError, one beyond the limits a RangeError.
func ParseDecimal(s string) (Value, error) {
	bad := func() (Value, error) {
		return NilValue, Errorf(ValueError, "not a decimal number: %s", Quote(s))
	}
	i := 0
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	var digits strings.Builder
	frac, nd, point := 0, 0, false
	for ; i < len(s); i++ {
		ch := s[i]
		if ch == '.' && !point {
			point = true
			continue
		}
		if ch < '0' || ch > '9' {
			break
		}
		nd++
		if point {
			frac++
		}
		if digits.Len() > 0 || ch != '0' { // leading zeros do not count
			digits.WriteByte(ch)
		}
	}
	if nd == 0 {
		return bad()
	}
	exp := 0
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		eneg := false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			eneg = s[i] == '-'
			i++
		}
		start := i
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			if exp < 1e8 { // beyond, any non-zero coefficient is out of range
				exp = exp*10 + int(s[i]-'0')
			}
		}
		if i == start {
			return bad()
		}
		if eneg {
			exp = -exp
		}
	}
	if i != len(s) {
		return bad()
	}
	text := digits.String()
	if text == "" { // zero: only its scale
		scale := max(frac-exp, 0)
		if scale > MaxDecimalScale {
			return NilValue, decRangeErr("literal")
		}
		return makeDecSmall(0, scale), nil
	}
	scale := frac - exp
	if scale < 0 { // 1.5e3: the digits shift left, the scale is 0
		if len(text)-scale > MaxDecimalDigits {
			return NilValue, decRangeErr("literal")
		}
		text += strings.Repeat("0", -scale)
		scale = 0
	}
	if len(text) > MaxDecimalDigits || scale > MaxDecimalScale {
		return NilValue, decRangeErr("literal")
	}
	c, _ := new(big.Int).SetString(text, 10)
	if neg {
		c.Neg(c)
	}
	return makeDec(c, scale, "literal")
}

// DecimalFromFloat gives the decimal of a float's shortest text (0.1 is
// 0.1, not 0.1000000000000000055511151231257827); inf and nan are a
// ValueError.
func DecimalFromFloat(f float64) (Value, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return NilValue, Errorf(ValueError, "%s cannot be a decimal", FormatFloat(f))
	}
	return ParseDecimal(strconv.FormatFloat(f, 'g', -1, 64))
}

// decText writes a decimal as its digits, never with an exponent: 12.50,
// -0.05, 3.
func (v Value) decText() string {
	var digits string
	neg := false
	scale := 0
	if c, s, ok := v.decSmall(); ok {
		scale = s
		if c < 0 {
			neg = true
			c = -c
		}
		digits = strconv.FormatInt(c, 10)
	} else {
		d := v.obj.(*decBig)
		scale = d.scale
		neg = d.coef.Sign() < 0
		digits = new(big.Int).Abs(d.coef).String()
	}
	if scale > 0 {
		if len(digits) <= scale {
			digits = strings.Repeat("0", scale-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// decRat gives the exact value of a decimal or an int as a big.Rat.
func (v Value) decRat() *big.Rat {
	c, s := v.DecimalParts()
	return new(big.Rat).SetFrac(c, pow10(s))
}

// DecimalToFloat gives the float nearest to a decimal.
func (v Value) DecimalToFloat() float64 {
	f, _ := strconv.ParseFloat(v.decText(), 64) // correctly rounded; ±inf beyond the range
	return f
}

// RoundMode is how a decimal is rounded to fewer digits.
type RoundMode uint8

const (
	HalfAway RoundMode = iota // halves away from zero (round, F-64)
	HalfEven                  // halves to the even digit (/)
	Down                      // toward zero (int)
	Floor                     // toward -inf
	Ceil                      // toward +inf
)

// roundQuo gives n / d rounded by mode (d > 0).
func roundQuo(n, d *big.Int, mode RoundMode) (q *big.Int, exact bool) {
	q, r := new(big.Int).QuoRem(n, d, new(big.Int))
	if r.Sign() == 0 {
		return q, true
	}
	neg := n.Sign() < 0
	up := false // away from zero by one
	switch mode {
	case HalfAway, HalfEven:
		c := new(big.Int).Abs(r)
		c.Lsh(c, 1)
		switch c.Cmp(d) {
		case 1:
			up = true
		case 0:
			up = mode == HalfAway || q.Bit(0) == 1
		}
	case Floor:
		up = neg
	case Ceil:
		up = !neg
	}
	if up {
		if neg {
			q.Sub(q, bigOne)
		} else {
			q.Add(q, bigOne)
		}
	}
	return q, false
}

// DecimalRound gives a decimal (or an int) rounded to scale digits after
// the point (0..MaxDecimalScale), as a decimal of that scale.
func DecimalRound(v Value, scale int, mode RoundMode) (Value, error) {
	if scale < 0 || scale > MaxDecimalScale {
		return NilValue, Errorf(RangeError, "a decimal's scale is 0..%d, not %d", MaxDecimalScale, scale)
	}
	c, s := v.DecimalParts()
	if scale >= s {
		return makeDec(c.Mul(c, pow10(scale-s)), scale, "round")
	}
	q, _ := roundQuo(c, pow10(s-scale), mode)
	return makeDec(q, scale, "round")
}

// DecimalToInt gives a decimal rounded to an int by mode, or a RangeError
// when it does not fit.
func DecimalToInt(v Value, mode RoundMode) (int64, error) {
	if c, s, ok := v.decSmall(); ok && s == 0 {
		return c, nil
	}
	c, s := v.DecimalParts()
	q, _ := roundQuo(c, pow10(s), mode)
	if !q.IsInt64() {
		return 0, Errorf(RangeError, "decimal %s does not fit in an int", v.decText())
	}
	return q.Int64(), nil
}

// isDecOperand reports whether v takes part in decimal arithmetic.
func (v Value) isDecOperand() bool { return v.kind == Decimal || v.kind == Int }

// decArith applies an arithmetic operator when a or b is a decimal.
func decArith(op ArithOp, a, b Value) (Value, error) {
	if !a.isDecOperand() || !b.isDecOperand() || op > Mod {
		if (a.kind == Float && b.isDecOperand() || b.kind == Float && a.isDecOperand()) && op <= Mod {
			return NilValue, Errorf(TypeError, "cannot apply %s to %s and %s: write the float as a decimal (1.1d) or convert it with decimal(x)", op, a.kind, b.kind)
		}
		return NilValue, typeErr(op.String(), a, b)
	}
	// The small form: +, - and * without big.Int while nothing overflows.
	if x, sx, ok := a.smallOrInt(); ok {
		if y, sy, ok := b.smallOrInt(); ok {
			if v, ok := smallArith(op, x, sx, y, sy); ok {
				return v, nil
			}
		}
	}
	x, sx := a.DecimalParts()
	y, sy := b.DecimalParts()
	switch op {
	case Add, Sub, Mod:
		s := max(sx, sy)
		x.Mul(x, pow10(s-sx))
		y.Mul(y, pow10(s-sy))
		switch op {
		case Add:
			return makeDec(x.Add(x, y), s, "+")
		case Sub:
			return makeDec(x.Sub(x, y), s, "-")
		}
		if y.Sign() == 0 {
			return NilValue, Errorf(ZeroDivision, "decimal modulo by zero")
		}
		return makeDec(x.Rem(x, y), s, "%") // the sign of the left side
	case Mul:
		return makeDec(x.Mul(x, y), sx+sy, "*")
	}
	if y.Sign() == 0 {
		return NilValue, Errorf(ZeroDivision, "decimal division by zero")
	}
	return decDiv(x, sx, y, sy)
}

// smallArith does +, - and * on two small coefficients, when the result
// is small too; ok is false when it is not (or for another operator).
func smallArith(op ArithOp, x int64, sx int, y int64, sy int) (Value, bool) {
	switch op {
	case Mul:
		if sx+sy > MaxDecimalScale {
			return NilValue, false
		}
		hi, lo := bits.Mul64(uint64(absInt(x)), uint64(absInt(y)))
		if hi != 0 || lo >= 1<<63 {
			return NilValue, false
		}
		r := int64(lo)
		if (x < 0) != (y < 0) {
			r = -r
		}
		return makeDecSmall(r, sx+sy), true
	case Add, Sub:
		s := max(sx, sy)
		var ok bool
		if x, ok = rescale(x, s-sx); !ok {
			return NilValue, false
		}
		if y, ok = rescale(y, s-sy); !ok {
			return NilValue, false
		}
		if op == Sub {
			y = -y // never MinInt64
		}
		r := x + y
		if (x > 0 && y > 0 && r < 0) || (x < 0 && y < 0 && r >= 0) || r == math.MinInt64 {
			return NilValue, false
		}
		return makeDecSmall(r, s), true
	}
	return NilValue, false
}

// rescale gives c * 10^n, if it stays small.
func rescale(c int64, n int) (int64, bool) {
	if n == 0 {
		return c, true
	}
	if n >= len(pow10Small) {
		return 0, c == 0
	}
	hi, lo := bits.Mul64(uint64(absInt(c)), uint64(pow10Small[n]))
	if hi != 0 || lo >= 1<<63 {
		return 0, false
	}
	if c < 0 {
		return -int64(lo), true
	}
	return int64(lo), true
}

// absInt gives |c| (c is never MinInt64 here).
func absInt(c int64) int64 {
	if c < 0 {
		return -c
	}
	return c
}

// smallOrInt gives the coefficient and scale of an int or a small decimal.
func (v Value) smallOrInt() (int64, int, bool) {
	if v.kind == Int {
		c := v.AsInt()
		return c, 0, c != math.MinInt64
	}
	return v.decSmall()
}

// decDiv gives x/10^sx divided by y/10^sy (y not 0): the exact quotient
// when it has at most DivisionDigits significant digits, at the scale
// sx - sy when that is enough (7.50 / 2 is 3.75, 10.00 / 4 is 2.50);
// else rounded to DivisionDigits significant digits, halves to even. The
// scale stays within 0..MaxDecimalScale: a quotient of 10^34 and more
// keeps all its integer digits, a tiny one is rounded at scale 1000.
func decDiv(x *big.Int, sx int, y *big.Int, sy int) (Value, error) {
	neg := x.Sign()*y.Sign() < 0
	n := new(big.Int).Abs(x) // the quotient is n / d
	n.Mul(n, pow10(sy))
	d := new(big.Int).Abs(y)
	d.Mul(d, pow10(sx))
	want := max(sx-sy, 0) // the preferred scale
	if n.Sign() == 0 {
		return makeDecSmall(0, want), nil
	}
	// n/d lies in [10^(e-1), 10^(e+1)): at scale DivisionDigits-e the
	// quotient has DivisionDigits or one more digits.
	e := len(n.String()) - len(d.String())
	t := min(max(DivisionDigits-e, 0), MaxDecimalScale)
	quo := func(t int) (*big.Int, bool) {
		return roundQuo(new(big.Int).Mul(n, pow10(t)), d, HalfEven)
	}
	q, exact := quo(t)
	if len(q.String()) > DivisionDigits && t > 0 {
		t--
		q, exact = quo(t)
		if len(q.String()) > DivisionDigits && t > 0 { // rounded up to 10^34: one zero less
			q.Quo(q, bigTen)
			t--
		}
	}
	if exact { // trailing zeros down to the preferred scale
		r := new(big.Int)
		for t > want {
			qq, rr := new(big.Int).QuoRem(q, bigTen, r)
			if rr.Sign() != 0 {
				break
			}
			q = qq
			t--
		}
		if t < want && len(q.String())+want-t <= DivisionDigits {
			q.Mul(q, pow10(want-t))
			t = want
		}
	}
	if neg {
		q.Neg(q)
	}
	return makeDec(q, t, "/")
}

// decNeg gives -v of a decimal.
func decNeg(v Value) (Value, error) {
	if c, s, ok := v.decSmall(); ok {
		return makeDecSmall(-c, s), nil // c is never MinInt64
	}
	c, s := v.DecimalParts()
	return makeDec(c.Neg(c), s, "-")
}

// decIncDec adds delta (1 or -1) to a decimal, keeping its scale.
func decIncDec(v Value, delta int64) (Value, error) {
	s := v.decScaleOf()
	var one Value // delta at scale s
	if s < len(pow10Small) {
		one = makeDecSmall(delta*pow10Small[s], s)
	} else {
		c := pow10(s)
		if delta < 0 {
			c.Neg(c)
		}
		one, _ = makeDec(c, s, "++")
	}
	return decArith(Add, v, one)
}

// cmpDec compares two numbers of which one is a decimal, exactly. ok is
// false when the other is NaN.
func cmpDec(a, b Value) (int, bool) {
	if x, sx, ok := a.smallOrInt(); ok {
		if y, sy, ok := b.smallOrInt(); ok && sx == sy {
			return cmp3(x, y), true
		}
	}
	if a.kind == Float || b.kind == Float {
		f, other, sign := a.AsFloat(), b, -1
		if b.kind == Float {
			f, other, sign = b.AsFloat(), a, 1
		}
		switch {
		case math.IsNaN(f):
			return 0, false
		case math.IsInf(f, 1):
			return -sign, true
		case math.IsInf(f, -1):
			return sign, true
		}
		r := new(big.Rat).SetFloat64(f)
		return sign * other.decRat().Cmp(r), true
	}
	x, sx := a.DecimalParts()
	y, sy := b.DecimalParts()
	s := max(sx, sy)
	x.Mul(x, pow10(s-sx))
	y.Mul(y, pow10(s-sy))
	return x.Cmp(y), true
}

// decKey gives the map key of a decimal: that of the int or float it
// equals, if one does, so that 2d, 2 and 2.0 are one key, and 1.5d and
// 1.5 another; else its digits without trailing zeros.
func decKey(v Value) mapKey {
	if n, ok := v.IntValue(); ok {
		return mapKey{kind: Int, num: uint64(n)}
	}
	if f, exact := v.decRat().Float64(); exact {
		return mapKey{kind: Float, num: math.Float64bits(f)}
	}
	c, s := v.DecimalParts()
	r := new(big.Int)
	for s > 0 {
		q, m := new(big.Int).QuoRem(c, bigTen, r)
		if m.Sign() != 0 {
			break
		}
		c = q
		s--
	}
	return mapKey{kind: Decimal, str: c.String() + "e-" + strconv.Itoa(s)}
}

// decIntValue gives the int a decimal equals, if it is integral and fits.
func (v Value) decIntValue() (int64, bool) {
	if c, s, ok := v.decSmall(); ok {
		if s == 0 {
			return c, true
		}
		if s < len(pow10Small) && c%pow10Small[s] == 0 {
			return c / pow10Small[s], true
		}
		if s >= len(pow10Small) && c == 0 {
			return 0, true
		}
		return 0, false
	}
	c, s := v.DecimalParts()
	q, r := new(big.Int).QuoRem(c, pow10(s), new(big.Int))
	if r.Sign() != 0 || !q.IsInt64() {
		return 0, false
	}
	return q.Int64(), true
}

// DecimalPow gives v to the power n (an int, n >= 0), exactly.
func DecimalPow(v Value, n int64) (Value, error) {
	if n < 0 {
		return NilValue, Errorf(ValueError, "a decimal's power is an int of 0 or more, not %d", n)
	}
	c, s := v.DecimalParts()
	if n == 0 {
		return makeDecSmall(1, 0), nil
	}
	if c.Sign() == 0 {
		if int64(s)*n > MaxDecimalScale {
			return NilValue, decRangeErr("pow")
		}
		return makeDecSmall(0, s*int(n)), nil
	}
	// |c|^n has at least n * (bits(c) - 1) * log10(2) digits: a bound
	// before computing, so that 2d ** 2^40 is refused, not attempted.
	if int64(s)*n > MaxDecimalScale || float64(n)*float64(c.BitLen()-1)*math.Log10(2) > MaxDecimalDigits {
		return NilValue, decRangeErr("pow")
	}
	return makeDec(c.Exp(c, big.NewInt(n), nil), s*int(n), "pow")
}

// RoundFloat gives f rounded to n digits after the point (0..1000),
// halves away from zero, from the float's exact value: round(2.675, 2) is
// 2.67, since the float 2.675 is a little less than 2.675. inf and nan
// stay as they are.
func RoundFloat(f float64, n int) float64 {
	if math.IsInf(f, 0) || math.IsNaN(f) || f == 0 {
		return f
	}
	r := new(big.Rat).SetFloat64(f)
	p := pow10(n)
	num := new(big.Int).Mul(r.Num(), p)
	q, _ := roundQuo(num, r.Denom(), HalfAway)
	g, _ := new(big.Rat).SetFrac(q, p).Float64()
	if g == 0 {
		return math.Copysign(0, f)
	}
	return g
}
