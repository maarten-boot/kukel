// Package utf8mod is the native module "utf8": text as characters (code
// points) instead of bytes (TODO.md Q93).
//
//	use utf8;
//	local s = "héllo wörld";
//	println(len(s), utf8::len(s));          // 13 11
//	println(utf8::upper(utf8::slice(s, 0, 5)));   // HÉLLO
//
// Strings stay bytes. Bytes that are not UTF-8 are kept: each counts as
// one character, whose code is 0xFFFD (the replacement character) and
// whose string is the byte itself, so join(utf8::chars(s), "") is s for
// any s. Positions are character positions; utf8::offset and utf8::index
// convert them to and from the byte positions of find, slice and re.
package utf8mod

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name: "utf8",
		Doc:  "text as characters (Unicode code points) instead of bytes",
		Funcs: map[string]native.Func{
			"len": str1("utf8::len", func(a native.Args, s string) (value.Value, error) {
				return value.MakeInt(int64(Len(s))), nil
			}),
			"valid": str1("utf8::valid", func(a native.Args, s string) (value.Value, error) {
				return value.MakeBool(utf8.ValidString(s)), nil
			}),
			"chars": str1("utf8::chars", func(a native.Args, s string) (value.Value, error) {
				elems := make([]value.Value, 0, Len(s))
				for i := 0; i < len(s); {
					_, n := decode(s[i:])
					elems = append(elems, value.MakeString(s[i:i+n]))
					i += n
				}
				return value.MakeVector(elems), nil
			}),
			"codes": str1("utf8::codes", func(a native.Args, s string) (value.Value, error) {
				elems := make([]value.Value, 0, Len(s))
				for i := 0; i < len(s); {
					r, n := decode(s[i:])
					elems = append(elems, value.MakeInt(int64(r)))
					i += n
				}
				return value.MakeVector(elems), nil
			}),
			"upper": str1("utf8::upper", func(a native.Args, s string) (value.Value, error) {
				return value.MakeString(mapChars(s, unicode.ToUpper)), nil
			}),
			"lower": str1("utf8::lower", func(a native.Args, s string) (value.Value, error) {
				return value.MakeString(mapChars(s, unicode.ToLower)), nil
			}),
			"trim": str1("utf8::trim", func(a native.Args, s string) (value.Value, error) {
				// An invalid byte decodes as utf8.RuneError, which is not
				// white space: it stays.
				return value.MakeString(strings.TrimFunc(s, unicode.IsSpace)), nil
			}),
			"fix":    fix,
			"char":   char,
			"string": fromCodes,
			"code":   code,
			"slice":  slice,
			"offset": offset,
			"index":  index,
		},
		Consts: map[string]value.Value{
			"UNICODE": value.MakeString(unicode.Version),
		},
	})
}

// str1 is a function of one string.
func str1(name string, f func(a native.Args, s string) (value.Value, error)) native.Func {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		a, err := native.Check(name, args, 1, 1)
		if err != nil {
			return value.NilValue, err
		}
		s, err := a.String(0)
		if err != nil {
			return value.NilValue, err
		}
		return f(a, s)
	}
}

// decode gives the character at the start of s (not empty) and its size
// in bytes: a byte that does not start valid UTF-8 is one character of
// its own, with code utf8.RuneError (0xFFFD).
func decode(s string) (rune, int) {
	if c := s[0]; c < utf8.RuneSelf {
		return rune(c), 1
	}
	return utf8.DecodeRuneInString(s)
}

// invalid reports whether the character decode found at the start of s,
// of size n, is a byte that is not UTF-8 (and not a U+FFFD in the text).
func invalid(r rune, n int) bool { return r == utf8.RuneError && n == 1 }

// ascii reports whether s has only ASCII bytes: then characters are
// bytes.
func ascii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// Len is the number of characters of s (each byte that is not UTF-8 one).
func Len(s string) int {
	if ascii(s) {
		return len(s)
	}
	return utf8.RuneCountInString(s)
}

// byteOffset gives the byte position where character i of s starts (i
// from 0 to the number of characters; that number gives len(s)), and false
// when s has fewer characters.
func byteOffset(s string, i int) (int, bool) {
	if ascii(s) {
		return i, i <= len(s)
	}
	b := 0
	for ; i > 0; i-- {
		if b == len(s) {
			return 0, false
		}
		_, n := decode(s[b:])
		b += n
	}
	return b, true
}

// mapChars is s with each character mapped by f; bytes that are not UTF-8
// stay as they are (Go's strings.Map would make them U+FFFD).
func mapChars(s string, f func(rune) rune) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, n := decode(s[i:])
		if invalid(r, n) {
			b.WriteByte(s[i])
		} else {
			b.WriteRune(f(r))
		}
		i += n
	}
	return b.String()
}

// position returns argument i, a character position from 0 to n.
func position(a native.Args, i int, n int, what string) (int, error) {
	x, err := a.Int(i)
	if err != nil {
		return 0, err
	}
	if x < 0 || x > int64(n) {
		return 0, a.Errorf(value.RangeError, "%s %d out of range 0..%d", what, x, n)
	}
	return int(x), nil
}

// checkCode refuses what is not a Unicode character: a number outside
// 0..0x10FFFF, or a surrogate (which UTF-8 cannot hold).
func checkCode(a native.Args, where string, c int64) error {
	switch {
	case c < 0 || c > unicode.MaxRune:
		return a.Errorf(value.RangeError, "%scode %d out of range 0..0x10FFFF", where, c)
	case c >= 0xD800 && c <= 0xDFFF:
		return a.Errorf(value.RangeError, "%s0x%X is a surrogate, not a character", where, c)
	}
	return nil
}

// utf8::fix(s [, repl]): s with each byte that is not UTF-8 replaced by
// repl (the replacement character "�" by default).
func fix(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("utf8::fix", args, 1, 2)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	repl := string(utf8.RuneError)
	if a.Has(1) {
		if repl, err = a.String(1); err != nil {
			return value.NilValue, err
		}
	}
	if utf8.ValidString(s) {
		return args[0], nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := decode(s[i:])
		if invalid(r, n) {
			b.WriteString(repl)
		} else {
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return value.MakeString(b.String()), nil
}

// utf8::char(code): the string of one character.
func char(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("utf8::char", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	c, err := a.Int(0)
	if err != nil {
		return value.NilValue, err
	}
	if err := checkCode(a, "", c); err != nil {
		return value.NilValue, err
	}
	return value.MakeString(string(rune(c))), nil
}

// utf8::string(codes): the string of a vector of codes.
func fromCodes(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("utf8::string", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	if args[0].Kind() != value.Vector {
		return value.NilValue, a.TypeError(0, "a vector of codes")
	}
	elems := args[0].AsVector().Elems
	var b strings.Builder
	for i, e := range elems {
		if e.Kind() != value.Int {
			return value.NilValue, a.Errorf(value.TypeError, "element %d must be an int, not %s", i, native.TypeName(e))
		}
		if err := checkCode(a, "element "+strconv.Itoa(i)+": ", e.AsInt()); err != nil {
			return value.NilValue, err
		}
		b.WriteRune(rune(e.AsInt()))
	}
	return value.MakeString(b.String()), nil
}

// utf8::code(s [, i]): the code of character i (0 by default).
func code(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("utf8::code", args, 1, 2)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	var i int64
	if a.Has(1) {
		if i, err = a.Int(1); err != nil {
			return value.NilValue, err
		}
	}
	if n := Len(s); i < 0 || i >= int64(n) {
		return value.NilValue, a.Errorf(value.RangeError, "position %d out of range for a string of %d characters", i, n)
	}
	b, _ := byteOffset(s, int(i))
	r, _ := decode(s[b:])
	return value.MakeInt(int64(r)), nil
}

// utf8::slice(s, start [, end]): the characters from start up to, not
// including, end; as slice does for bytes.
func slice(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("utf8::slice", args, 2, 3)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	n := Len(s)
	from, err := position(a, 1, n, "start")
	if err != nil {
		return value.NilValue, err
	}
	to := n
	if a.Has(2) {
		if to, err = position(a, 2, n, "end"); err != nil {
			return value.NilValue, err
		}
		if to < from {
			return value.NilValue, a.Errorf(value.RangeError, "end %d is before start %d", to, from)
		}
	}
	bf, _ := byteOffset(s, from)
	bt, _ := byteOffset(s[bf:], to-from)
	return value.MakeString(s[bf : bf+bt]), nil
}

// utf8::offset(s, i): the byte position where character i starts.
func offset(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("utf8::offset", args, 2, 2)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	i, err := position(a, 1, Len(s), "position")
	if err != nil {
		return value.NilValue, err
	}
	b, _ := byteOffset(s, i)
	return value.MakeInt(int64(b)), nil
}

// utf8::index(s, b): the character position of byte position b, which
// must be where a character starts (or len(s)).
func index(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("utf8::index", args, 2, 2)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	b, err := a.Int(1)
	if err != nil {
		return value.NilValue, err
	}
	if b < 0 || b > int64(len(s)) {
		return value.NilValue, a.Errorf(value.RangeError, "byte %d out of range 0..%d", b, len(s))
	}
	if ascii(s) {
		return value.MakeInt(b), nil
	}
	i, at := 0, 0
	for at < int(b) {
		_, n := decode(s[at:])
		if at+n > int(b) {
			return value.NilValue, a.Errorf(value.ValueError, "byte %d is inside a character (which starts at byte %d)", b, at)
		}
		at += n
		i++
	}
	return value.MakeInt(int64(i)), nil
}
