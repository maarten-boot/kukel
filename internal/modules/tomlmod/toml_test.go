package tomlmod

import (
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/modules/timemod"
	"github.com/maarten-boot/kukel/internal/value"
)

func call(fn func(value.Env, []value.Value) (value.Value, error), args ...value.Value) (value.Value, error) {
	return fn(nil, args)
}

func dec(t *testing.T, text string) value.Value {
	t.Helper()
	v, err := call(decode, value.MakeString(text))
	if err != nil {
		t.Fatalf("decode(%q): %v", text, err)
	}
	return v
}

func TestDecodeValues(t *testing.T) {
	v := dec(t, `
s = "a\tb"
lit = 'C:\x'
i = 0x1F
f = 1e3
big = inf
b = true
odt = 1979-05-27T07:32:00.5-07:00
ldt = 1979-05-27T07:32:00
ld = 1979-05-27
lt = 07:32:00.25
arr = [1, "two", [3.0]]
`)
	want := `{"s": "a\tb", "lit": "C:\\x", "i": 31, "f": 1000.0, "big": inf, "b": true, ` +
		`"odt": time::Time(1979-05-27T07:32:00.5-07:00), "ldt": "1979-05-27T07:32:00", ` +
		`"ld": "1979-05-27", "lt": "07:32:00.25", "arr": [1, "two", [3.0]]}`
	if got := v.String(); got != want {
		t.Errorf("decode =\n%s\nwant\n%s", got, want)
	}
	m := v.AsMap()
	odt, _, _ := m.Get(value.MakeString("odt"))
	if !timemod.Time.Is(odt) {
		t.Errorf("odt is %v, not a time::Time", odt)
	}
}

// TestDecodeOrder: every table keeps the order of the text, also tables
// made by dotted keys, [[headers]] and inline arrays of tables, whose
// elements have the same keys in different orders.
func TestDecodeOrder(t *testing.T) {
	v := dec(t, `
z = 1
a = {y = 2, b = {q = 1, p = 2}}
arr = [{k = 1, j = 2}, {j = 3, k = 4}, {}, {m = 5}]
t.x.c = 3
t.w = 4
[srv]
port = 8
host = "h"
[[fruit]]
name = "apple"
[fruit.physical]
color = "red"
shape = "round"
[[fruit.variety]]
name = "red delicious"
[[fruit]]
when = 1979-05-27
name = "banana"
`)
	want := `{"z": 1, "a": {"y": 2, "b": {"q": 1, "p": 2}}, ` +
		`"arr": [{"k": 1, "j": 2}, {"j": 3, "k": 4}, {}, {"m": 5}], ` +
		`"t": {"x": {"c": 3}, "w": 4}, "srv": {"port": 8, "host": "h"}, ` +
		`"fruit": [{"name": "apple", "physical": {"color": "red", "shape": "round"}, "variety": [{"name": "red delicious"}]}, ` +
		`{"when": "1979-05-27", "name": "banana"}]}`
	if got := v.String(); got != want {
		t.Errorf("decode =\n%s\nwant\n%s", got, want)
	}
}

func TestEncode(t *testing.T) {
	v := dec(t, `
title = "x"
srv = {port = 8}
list = [{a = 1}, {b = 2}]
mixed = [1, {c = 3}]
empty = []
"a b" = "q\"\u0001"
when = 2026-11-09T10:00:00Z
f = 1.0
n = -inf
`)
	want := `title = "x"
mixed = [1, { c = 3 }]
empty = []
"a b" = "q\"\u0001"
when = 2026-11-09T10:00:00Z
f = 1.0
n = -inf

[srv]
port = 8

[[list]]
a = 1

[[list]]
b = 2
`
	out, err := call(encode, v)
	if err != nil {
		t.Fatal(err)
	}
	if out.AsString() != want {
		t.Errorf("encode =\n%s\nwant\n%s", out.AsString(), want)
	}
	// The text decodes to the same values (tables after the other keys).
	again := dec(t, out.AsString())
	out2, _ := call(encode, again)
	if out2.AsString() != want {
		t.Errorf("encode(decode(encode)) =\n%s", out2.AsString())
	}
}

// TestEncodeNested: a map is always written as a [table], also below an
// array of tables; inline tables stand only in arrays that hold other
// values too.
func TestEncodeNested(t *testing.T) {
	v := dec(t, `
[a.b]
c = 1
[[a.d]]
e = {f = [{g = 1}]}
`)
	want := `
[a]

[a.b]
c = 1

[[a.d]]

[a.d.e]

[[a.d.e.f]]
g = 1
`
	out, err := call(encode, v)
	if err != nil || out.AsString() != want[1:] {
		t.Errorf("encode = %q, %v; want %q", out.AsString(), err, want[1:])
	}
}

func TestErrors(t *testing.T) {
	self := value.NewMap()
	self.Set(value.MakeString("self"), value.MakeMap(self))
	dup := value.NewMap()
	dup.Set(value.MakeInt(1), value.MakeInt(1))
	dup.Set(value.MakeString("1"), value.MakeInt(2))
	withNil := value.NewMap()
	withNil.Set(value.MakeString("x"), value.NilValue)
	bad := value.NewMap()
	bad.Set(value.MakeString("x"), value.MakeString("\xff"))
	tests := []struct {
		fn   func(value.Env, []value.Value) (value.Value, error)
		arg  value.Value
		want string
	}{
		{decode, value.MakeString("a = 1\na = 2\n"), "toml::decode: invalid TOML at line 2, column 1: Key 'a' has already been defined"},
		{decode, value.MakeString("a = "), "invalid TOML at line 1, column 4: unexpected EOF; expected value"},
		{decode, value.MakeString("a = " + strings.Repeat("[", maxDepth+1)), "toml::decode: arrays and tables nested more than 32 deep"},
		{decode, value.MakeString(strings.Repeat("a.", maxDepth+1) + "b = 1"), "toml::decode: arrays and tables nested more than 32 deep"},
		{decode, value.MakeInt(1), "argument 1 must be a string, not int"},
		{encode, value.MakeVector(nil), "toml::encode: a TOML document is a map, not vector"},
		{encode, value.MakeMap(self), "toml::encode: the value contains itself"},
		{encode, value.MakeMap(dup), `toml::encode: the map has the key "1" twice (as a number and as a string)`},
		{encode, value.MakeMap(withNil), "toml::encode: cannot encode nil as TOML"},
		{encode, value.MakeMap(bad), `toml::encode: string "\xff" is not valid UTF-8`},
	}
	for _, tc := range tests {
		_, err := call(tc.fn, tc.arg)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("got %v, want %q", err, tc.want)
		}
	}
}

func FuzzTOML(f *testing.F) {
	for _, s := range []string{"a = 1", "[x]\ny = [1, {z = 2}]", "[[t]]\n[[t]]\nk = 'v'", "d = 1979-05-27T07:32:00Z"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := call(decode, value.MakeString(s))
		if err != nil {
			return
		}
		out, err := call(encode, v)
		if err != nil {
			t.Fatalf("encode(decode(%q)): %v", s, err)
		}
		v2, err := call(decode, out)
		if err != nil {
			t.Fatalf("decode(encode(decode(%q))) of %q: %v", s, out.AsString(), err)
		}
		if out2, _ := call(encode, v2); out2.AsString() != out.AsString() {
			t.Fatalf("not stable for %q:\n%s\n%s", s, out.AsString(), out2.AsString())
		}
	})
}
