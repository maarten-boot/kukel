package utf8mod

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// call calls utf8::f with Go values (string, int, []int64) as arguments.
func call(t testing.TB, f string, args ...any) (value.Value, error) {
	t.Helper()
	m, _ := native.Lookup("utf8")
	var vs []value.Value
	for _, a := range args {
		switch a := a.(type) {
		case string:
			vs = append(vs, value.MakeString(a))
		case int:
			vs = append(vs, value.MakeInt(int64(a)))
		case []int64:
			var e []value.Value
			for _, c := range a {
				e = append(e, value.MakeInt(c))
			}
			vs = append(vs, value.MakeVector(e))
		case value.Value:
			vs = append(vs, a)
		default:
			t.Fatalf("argument %T", a)
		}
	}
	return m.Funcs[f](nil, vs)
}

// show writes a result as Kukel would print it: strings quoted (with Go's
// escapes, so invalid bytes show), vectors of them in brackets.
func show(v value.Value) string {
	switch v.Kind() {
	case value.String:
		return fmt.Sprintf("%q", v.AsString())
	case value.Vector:
		var parts []string
		for _, e := range v.AsVector().Elems {
			parts = append(parts, show(e))
		}
		return "[" + strings.Join(parts, " ") + "]"
	case value.Int:
		return fmt.Sprint(v.AsInt())
	case value.Bool:
		return fmt.Sprint(v.AsBool())
	}
	return v.Kind().String()
}

func TestFunctions(t *testing.T) {
	const s = "héllo wörld €😀" // 1-, 2-, 3- and 4-byte characters
	for _, c := range []struct {
		f    string
		args []any
		want string
	}{
		{"len", []any{s}, "14"},
		{"len", []any{""}, "0"},
		{"len", []any{"abc"}, "3"},
		{"len", []any{"a\xffb\xe2\x82"}, "5"}, // a byte that is not UTF-8, and a cut-off €: one character per byte
		{"len", []any{"�"}, "1"},
		{"valid", []any{s}, "true"},
		{"valid", []any{"a\xff"}, "false"},
		{"valid", []any{"\xed\xa0\x80"}, "false"}, // a surrogate in UTF-8 form
		{"chars", []any{"hé€😀"}, `["h" "é" "€" "😀"]`},
		{"chars", []any{"a\xffé"}, `["a" "\xff" "é"]`},
		{"chars", []any{""}, "[]"},
		{"codes", []any{"hé€😀"}, "[104 233 8364 128512]"},
		{"codes", []any{"a\xff�"}, "[97 65533 65533]"},
		{"char", []any{233}, `"é"`},
		{"char", []any{0}, `"\x00"`},
		{"char", []any{0x10FFFF}, `"\U0010ffff"`},
		{"string", []any{[]int64{104, 233, 8364, 128512}}, `"hé€😀"`},
		{"string", []any{[]int64{}}, `""`},
		{"code", []any{"é"}, "233"},
		{"code", []any{s, 13}, "128512"},
		{"code", []any{"a\xff", 1}, "65533"},
		{"slice", []any{s, 0, 5}, `"héllo"`},
		{"slice", []any{s, 6}, `"wörld €😀"`},
		{"slice", []any{s, 12, 14}, `"€😀"`},
		{"slice", []any{s, 14}, `""`},
		{"slice", []any{"a\xffb", 1, 2}, `"\xff"`},
		{"slice", []any{"abc", 1, 1}, `""`},
		{"upper", []any{"héllo ß ı"}, `"HÉLLO ß I"`},
		{"lower", []any{"HÉLLO Σ"}, `"héllo σ"`},
		{"upper", []any{"a\xffé"}, `"A\xffÉ"`}, // the invalid byte stays (Go's ToUpper would make it U+FFFD)
		{"trim", []any{" 　 hé \t "}, `"hé"`},
		{"trim", []any{"\xff x \xff"}, `"\xff x \xff"`},
		{"fix", []any{"a\xffb\xe2\x82"}, `"a�b��"`},
		{"fix", []any{"a\xffb", "?"}, `"a?b"`},
		{"fix", []any{"a\xffb", ""}, `"ab"`},
		{"fix", []any{s}, fmt.Sprintf("%q", s)},
		{"offset", []any{s, 0}, "0"},
		{"offset", []any{s, 2}, "3"},
		{"offset", []any{s, 13}, "17"},
		{"offset", []any{s, 14}, "21"},
		{"offset", []any{"abc", 3}, "3"},
		{"index", []any{s, 3}, "2"},
		{"index", []any{s, 21}, "14"},
		{"index", []any{s, 0}, "0"},
		{"index", []any{"a\xff\xe2\x82x", 3}, "3"}, // the bytes of a cut-off character are characters each
	} {
		v, err := call(t, c.f, c.args...)
		if got := show(v); err != nil || got != c.want {
			t.Errorf("utf8::%s%q: %s, %v; want %s", c.f, c.args, got, err, c.want)
		}
	}
	if v := m().Consts["UNICODE"]; v.AsString() == "" {
		t.Error("utf8::UNICODE is empty")
	}
}

func m() *native.Module { mod, _ := native.Lookup("utf8"); return mod }

func TestErrors(t *testing.T) {
	const s = "héllo"
	for _, c := range []struct {
		f    string
		args []any
		want string
	}{
		{"len", []any{1}, "TypeError: utf8::len: argument 1 must be a string, not int"},
		{"len", []any{}, "ArgumentError: utf8::len expects 1 argument, got 0"},
		{"fix", []any{"x", 1}, "TypeError: utf8::fix: argument 2 must be a string, not int"},
		{"char", []any{-1}, "RangeError: utf8::char: code -1 out of range 0..0x10FFFF"},
		{"char", []any{0x110000}, "RangeError: utf8::char: code 1114112 out of range 0..0x10FFFF"},
		{"char", []any{0xD800}, "RangeError: utf8::char: 0xD800 is a surrogate, not a character"},
		{"char", []any{"a"}, "TypeError: utf8::char: argument 1 must be an int, not string"},
		{"string", []any{"a"}, "TypeError: utf8::string: argument 1 must be a vector of codes, not string"},
		{"string", []any{[]int64{65, 0xDFFF}}, "RangeError: utf8::string: element 1: 0xDFFF is a surrogate, not a character"},
		{"string", []any{value.MakeVector([]value.Value{value.MakeString("a")})}, "TypeError: utf8::string: element 0 must be an int, not string"},
		{"code", []any{s, 5}, "RangeError: utf8::code: position 5 out of range for a string of 5 characters"},
		{"code", []any{s, -1}, "RangeError: utf8::code: position -1 out of range for a string of 5 characters"},
		{"code", []any{""}, "RangeError: utf8::code: position 0 out of range for a string of 0 characters"},
		{"slice", []any{s, 6}, "RangeError: utf8::slice: start 6 out of range 0..5"},
		{"slice", []any{s, 0, -1}, "RangeError: utf8::slice: end -1 out of range 0..5"},
		{"slice", []any{s, 3, 2}, "RangeError: utf8::slice: end 2 is before start 3"},
		{"offset", []any{s, 6}, "RangeError: utf8::offset: position 6 out of range 0..5"},
		{"index", []any{s, 7}, "RangeError: utf8::index: byte 7 out of range 0..6"},
		{"index", []any{s, 2}, "ValueError: utf8::index: byte 2 is inside a character (which starts at byte 1)"},
	} {
		_, err := call(t, c.f, c.args...)
		var e *value.Error
		if !errors.As(err, &e) || e.Kind+": "+e.Msg != c.want {
			t.Errorf("utf8::%s%q: %v; want %s", c.f, c.args, err, c.want)
		}
	}
}

// The functions agree with each other for any bytes: the characters put
// together give the string back, codes and string are inverses on valid
// text, every character position converts to a byte position and back,
// and slices of positions put together give the string.
func FuzzUTF8(f *testing.F) {
	for _, s := range []string{"", "abc", "héllo wörld €😀", "a\xffb\xe2\x82", "\xed\xa0\x80", "�\xf0\x9f"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, _ := call(t, "len", s)
		if int(n.AsInt()) != len([]rune(s)) {
			t.Fatalf("len %d, Go counts %d", n.AsInt(), len([]rune(s)))
		}
		chars, _ := call(t, "chars", s)
		var b strings.Builder
		for _, c := range chars.AsVector().Elems {
			b.WriteString(c.AsString())
		}
		if b.String() != s || len(chars.AsVector().Elems) != int(n.AsInt()) {
			t.Fatalf("chars %s", show(chars))
		}
		fixed, _ := call(t, "fix", s)
		if !utf8.ValidString(fixed.AsString()) {
			t.Fatalf("fix gives %q", fixed.AsString())
		}
		codes, _ := call(t, "codes", fixed)
		back, err := call(t, "string", codes)
		if err != nil || back.AsString() != fixed.AsString() {
			t.Fatalf("string(codes(fix(s))) is %q, %v", back.AsString(), err)
		}
		up, _ := call(t, "upper", s)
		if l, _ := call(t, "len", up); l.AsInt() != n.AsInt() {
			t.Fatalf("upper changes the number of characters: %q", up.AsString())
		}
		var joined strings.Builder
		for i := 0; i <= int(n.AsInt()); i++ {
			off, err := call(t, "offset", s, i)
			if err != nil {
				t.Fatal(err)
			}
			if j, err := call(t, "index", s, int(off.AsInt())); err != nil || j.AsInt() != int64(i) {
				t.Fatalf("index(offset(%d)) is %d, %v", i, j.AsInt(), err)
			}
			if i < int(n.AsInt()) {
				part, err := call(t, "slice", s, i, i+1)
				if err != nil {
					t.Fatal(err)
				}
				joined.WriteString(part.AsString())
				if c, _ := call(t, "code", s, i); c.AsInt() != codes.AsVector().Elems[i].AsInt() {
					t.Fatalf("code(%d) is %d", i, c.AsInt())
				}
			}
		}
		if joined.String() != s {
			t.Fatalf("slices give %q", joined.String())
		}
	})
}
