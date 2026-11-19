package jsonmod

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/value"
)

func call(fn func(value.Env, []value.Value) (value.Value, error), args ...value.Value) (value.Value, error) {
	return fn(nil, args)
}

func TestRoundTrip(t *testing.T) {
	texts := []string{
		`null`, `true`, `0`, `-9223372036854775808`, `1.5`, `1e+300`, `""`, `"a\"b\\c\n\u0001é"`,
		`[]`, `{}`, `[1,[2,[3]],{"x":null}]`, `{"b":1,"a":{"c":[true,false]}}`,
	}
	for _, text := range texts {
		v, err := call(decode, value.MakeString(text))
		if err != nil {
			t.Errorf("decode(%s): %v", text, err)
			continue
		}
		out, err := call(encode, v)
		if err != nil || out.AsString() != text {
			t.Errorf("encode(decode(%s)) = %v, %v", text, out, err)
		}
	}
}

func TestNumbers(t *testing.T) {
	tests := map[string]string{
		"12":                   "12",
		"-0":                   "0",
		"1.0":                  "1.0",
		"1E2":                  "100.0",
		"9223372036854775807":  "9223372036854775807",
		"9223372036854775808":  "9.223372036854776e+18",
		"-9223372036854775809": "-9.223372036854776e+18",
		"1e400":                "inf",
	}
	for text, want := range tests {
		v, err := call(decode, value.MakeString(text))
		if err != nil || v.String() != want {
			t.Errorf("decode(%s) = %v, %v; want %s", text, v, err, want)
		}
	}
}

func TestErrors(t *testing.T) {
	deep := value.MakeVector(nil)
	for i := 0; i < maxDepth+5; i++ {
		deep = value.MakeVector([]value.Value{deep})
	}
	m := value.NewMap()
	m.Set(value.MakeString("self"), value.MakeMap(m))
	tests := []struct {
		fn   func(value.Env, []value.Value) (value.Value, error)
		args []value.Value
		msg  string
	}{
		{encode, nil, "json::encode expects 1 to 2 arguments, got 0"},
		{encode, []value.Value{value.NilValue, value.MakeInt(2)}, "argument 2 must be a string"},
		{encode, []value.Value{value.NilValue, value.MakeString("x")}, `the indent must be spaces and tabs, not "x"`},
		{encode, []value.Value{deep}, "nested more than 10000 deep"},
		{encode, []value.Value{value.MakeMap(m)}, "the value contains itself"},
		{encode, []value.Value{value.MakeFloat(-1 / zero())}, "-inf has no JSON form"},
		{encode, []value.Value{infKey()}, "the map key inf has no JSON form"},
		{decode, []value.Value{value.MakeInt(1)}, "argument 1 must be a string, not int"},
		{decode, []value.Value{value.MakeString(strings.Repeat("[", 20000))}, "nested more than 10000 deep"},
		{decode, []value.Value{value.MakeString(`{"a":1,}`)}, "invalid JSON at byte 8: invalid character '}' where an object key (a string) should be"},
		{decode, []value.Value{value.MakeString(`[1 2]`)}, "invalid JSON at byte 4: invalid character '2' after an array element (',' or ']' expected)"},
		{decode, []value.Value{value.MakeString(`{"a" 1}`)}, "invalid JSON at byte 6: invalid character '1' after an object key (':' expected)"},
		{decode, []value.Value{value.MakeString(`{"a":1 "b"}`)}, "invalid JSON at byte 8: invalid character '\"' after an object member (',' or '}' expected)"},
		{decode, []value.Value{value.MakeString(`tru`)}, "invalid JSON: unexpected end of text"},
		{decode, []value.Value{value.MakeString(`nul!`)}, "invalid JSON at byte 4: invalid character '!' in the literal null"},
		{decode, []value.Value{value.MakeString(`01`)}, "invalid JSON at byte 2: a number cannot start with 0 and another digit"},
		{decode, []value.Value{value.MakeString(`[-007]`)}, "invalid JSON at byte 4: a number cannot start with 0 and another digit"},
		{encode, []value.Value{value.MakeString("\xff" + strings.Repeat("a", 1000))}, `string "\xffaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"... (1001 bytes) is not valid UTF-8`},
		{encode, []value.Value{value.MakeString("a\xff")}, `string "a\xff" is not valid UTF-8`},
		{encode, []value.Value{value.NewObject(&value.ClassObj{Name: "geo::Point"})}, "cannot encode geo::Point as JSON"},
		{decode, []value.Value{value.MakeString(`-x`)}, "invalid JSON at byte 2: invalid character 'x' in a number (a digit expected)"},
		{decode, []value.Value{value.MakeString(`1.`)}, "invalid JSON: unexpected end of text"},
		{decode, []value.Value{value.MakeString("\"a\tb\"")}, "invalid JSON at byte 3: invalid byte 0x09 in a string (control characters must be escaped)"},
		{decode, []value.Value{value.MakeString(`"\x"`)}, "invalid JSON at byte 3: invalid character 'x' in an escape"},
		{decode, []value.Value{value.MakeString(`"\u12g4"`)}, "invalid JSON at byte 6: invalid character 'g' in a \\u escape"},
		{decode, []value.Value{value.MakeString(`'a'`)}, "invalid JSON at byte 1: invalid character '\\'' where a value should be"},
	}
	for _, tc := range tests {
		_, err := call(tc.fn, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("got %v, want %q", err, tc.msg)
		}
	}
}

func zero() float64 { return 0 }

// FuzzJSON: decode never panics, accepts what encoding/json accepts, and
// what it decodes encodes and decodes again to the same text.
func FuzzJSON(f *testing.F) {
	for _, s := range []string{`{"a":[1,2.5,"x",null,true]}`, `"é"`, `[[[]]]`, `1e400`, `{"a":1,"a":2}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := call(decode, value.MakeString(s))
		if valid := json.Valid([]byte(s)); valid != (err == nil) {
			t.Fatalf("%q: encoding/json valid %v, decode: %v", s, valid, err)
		}
		if err != nil {
			return
		}
		once, err := call(encode, v)
		if err != nil {
			return // inf from a huge number has no JSON form
		}
		v2, err := call(decode, once)
		if err != nil {
			t.Fatalf("decode(encode(decode(%q))) = %v", s, err)
		}
		twice, err := call(encode, v2)
		if err != nil || twice.AsString() != once.AsString() {
			t.Fatalf("%q: %s then %v (%v)", s, once, twice, err)
		}
		// With decimals, numbers are exact: decoding and encoding again
		// gives the same text, and the values are equal (== for numbers).
		d, err := call(decode, value.MakeString(s), decimalsOn())
		if err != nil {
			return // a number beyond a decimal's limits
		}
		once, err = call(encode, d)
		if err != nil {
			t.Fatalf("encode(decode(%q, decimals)) = %v", s, err)
		}
		d2, err := call(decode, once, decimalsOn())
		if err != nil {
			t.Fatalf("decode(encode(decode(%q, decimals))) = %v", s, err)
		}
		if twice, _ := call(encode, d2); twice.AsString() != once.AsString() {
			t.Fatalf("%q with decimals: %s then %s", s, once, twice)
		}
	})
}

// Deep nesting is a ValueError, not a crash of the process.
func TestDeepDecode(t *testing.T) {
	for _, n := range []int{maxDepth + 1, 3_000_000} {
		s := strings.Repeat("[", n) + strings.Repeat("]", n)
		_, err := call(decode, value.MakeString(s))
		if err == nil || !strings.Contains(err.Error(), "nested more than 10000 deep") {
			t.Errorf("depth %d: %v", n, err)
		}
	}
	s := strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth)
	if _, err := call(decode, value.MakeString(s)); err != nil {
		t.Errorf("depth %d: %v", maxDepth, err)
	}
}

// The decoder accepts what encoding/json accepts, and gives the same
// values (compared as encode gives them).
func TestLikeEncodingJSON(t *testing.T) {
	texts := []string{
		`null`, ` true `, `false`, `0`, `-0.5e-3`, `1E+2`, `123456789012345678901234567890`, `""`,
		`"\u00e9\ud83d\ude00"`, `"\ud800"`, `"\udc00x"`, `"\ud800\u0041"`, "\"\xff\xfe\"", "\"caf\xc3\"",
		`"\/\b\f\n\r\t\"\\"`, `[]`, `[ ]`, `{}`, `{ }`, `[1,2,[3,{"a":[]}]]`, `{"a":1,"a":2,"b":3}`,
		`[1,]`, `[,1]`, `{"a":1,}`, `{,}`, `{"a"}`, `{"a":}`, `{1:2}`, `[1 2]`, `01`, `1.`, `.5`, `+1`, `1e`, `1e+`,
		`-`, `--1`, `tru`, `truex`, `nul`, `NaN`, `"abc`, `"\`, `"\u12"`, `"\q"`, "\"a\x01\"", `[`, `{`, `{"a"`,
		"\t[\r\n1\n]\n", `[] []`, ``, ` `, `"a" x`,
	}
	for _, text := range texts {
		v, err := call(decode, value.MakeString(text))
		if valid := json.Valid([]byte(text)); valid != (err == nil) {
			t.Errorf("%q: encoding/json valid %v, decode: %v", text, valid, err)
			continue
		}
		if err != nil {
			continue
		}
		var x any
		if err := json.Unmarshal([]byte(text), &x); err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(x)
		got, err := call(encode, v)
		if err != nil {
			t.Errorf("%q: %v", text, err)
			continue
		}
		var y any
		json.Unmarshal([]byte(got.AsString()), &y)
		again, _ := json.Marshal(y)
		if string(again) != string(want) {
			t.Errorf("%q: decode gives %s, encoding/json %s", text, again, want)
		}
	}
}

func infKey() value.Value {
	m := value.NewMap()
	m.Set(value.MakeFloat(1/zero()), value.MakeInt(1))
	return value.MakeMap(m)
}

func decimalsOn() value.Value {
	m := value.NewMap()
	m.Set(value.MakeString("decimals"), value.True)
	return value.MakeMap(m)
}

// TestDecimals: encode writes a decimal's digits; decode with
// {"decimals": true} reads numbers with a fraction or exponent, and ints
// beyond 64 bits, as exact decimals with the scale of their text.
func TestDecimals(t *testing.T) {
	d, _ := value.ParseDecimal("12.50")
	big, _ := value.ParseDecimal("123456789012345678901234567890.25")
	v := value.MakeVector([]value.Value{d, big})
	out, err := call(encode, v)
	if err != nil || out.AsString() != `[12.50,123456789012345678901234567890.25]` {
		t.Errorf("encode = %v, %v", out, err)
	}
	tests := map[string]string{ // text: the value inside a vector (Repr)
		`12.50`:                   "12.50d",
		`1.5e3`:                   "1500d",
		`2.5E-3`:                  "0.0025d",
		`-0.10`:                   "-0.10d",
		`7`:                       "7",
		`-9223372036854775808`:    "-9223372036854775808",
		`9223372036854775808`:     "9223372036854775808d",
		`[0.1, 2, {"p": 9.99}]`:   `[0.1d, 2, {"p": 9.99d}]`,
		`123456789012345678.1234`: "123456789012345678.1234d",
	}
	for text, want := range tests {
		v, err := call(decode, value.MakeString(text), decimalsOn())
		if err != nil || v.Repr() != want {
			t.Errorf("decode(%s, decimals) = %s, %v; want %s", text, v.Repr(), err, want)
		}
	}
	// Without the option, as before.
	if v, _ := call(decode, value.MakeString(`12.50`)); v.Kind() != value.Float {
		t.Errorf("decode(12.50) = %s", v.Repr())
	}
	off := value.NewMap()
	off.Set(value.MakeString("decimals"), value.False)
	if v, _ := call(decode, value.MakeString(`12.50`), value.MakeMap(off)); v.Kind() != value.Float {
		t.Errorf("decode(12.50, decimals false) = %s", v.Repr())
	}
	// A round trip keeps every digit and the scale.
	text := `{"net":51.65,"vat":10.85,"rate":0.210,"n":3}`
	v, err = call(decode, value.MakeString(text), decimalsOn())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := call(encode, v); err != nil || out.AsString() != text {
		t.Errorf("round trip = %v, %v", out, err)
	}
	bad := map[string]value.Value{}
	m := value.NewMap()
	m.Set(value.MakeString("decimal"), value.True)
	bad[`unknown option "decimal" (the option is decimals)`] = value.MakeMap(m)
	m = value.NewMap()
	m.Set(value.MakeString("decimals"), value.MakeInt(1))
	bad[`option decimals must be a bool, not int`] = value.MakeMap(m)
	bad[`must be a map of options`] = value.True
	for msg, opts := range bad {
		if _, err := call(decode, value.MakeString(`1`), opts); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("decode with %s: %v, want %q", opts.Repr(), err, msg)
		}
	}
	if _, err := call(decode, value.MakeString(`[1, 1e5000]`), decimalsOn()); err == nil ||
		!strings.Contains(err.Error(), "invalid JSON at byte 5: the number 1e5000 has more than 1000 digits or a scale above 1000 for a decimal") {
		t.Errorf("decode(1e5000, decimals): %v", err)
	}
}
