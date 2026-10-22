// Package jsonmod is the native module "json": encode Kukel values as JSON
// text and decode JSON text into Kukel values.
//
//	use json;
//	local s = json::encode({"a": [1, 2.5, nil]});   // {"a":[1,2.5,null]}
//	println(json::decode(s)["a"][1]);                // 2.5
package jsonmod

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name: "json",
		Doc:  "encode and decode JSON",
		Funcs: map[string]native.Func{
			"encode": encode,
			"decode": decode,
		},
	})
}

// encode(v [, indent]): v as JSON text. With indent (a string such as "  ")
// the text spreads over lines, nested levels indented by it.
func encode(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("json::encode", args, 1, 2)
	if err != nil {
		return value.NilValue, err
	}
	e := &encoder{a: a, open: map[any]bool{}}
	if a.Has(1) {
		if e.indent, err = a.String(1); err != nil {
			return value.NilValue, err
		}
		if strings.Trim(e.indent, " \t") != "" { // anything else would not be JSON
			return value.NilValue, a.Errorf(value.ValueError, "the indent must be spaces and tabs, not %s", strconv.Quote(e.indent))
		}
	}
	if err := e.value(args[0], 0); err != nil {
		return value.NilValue, err
	}
	return value.MakeString(e.b.String()), nil
}

type encoder struct {
	a      native.Args
	b      strings.Builder
	indent string
	open   map[any]bool // the vectors and maps being encoded (a cycle is an error)
}

func (e *encoder) value(v value.Value, depth int) error {
	switch v.Kind() {
	case value.Nil:
		e.b.WriteString("null")
	case value.Bool:
		e.b.WriteString(strconv.FormatBool(v.AsBool()))
	case value.Int:
		e.b.WriteString(strconv.FormatInt(v.AsInt(), 10))
	case value.Float:
		f := v.AsFloat()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return e.a.Errorf(value.ValueError, "%s has no JSON form", value.FormatFloat(f))
		}
		e.b.WriteString(value.FormatFloat(f)) // keeps ".0", so a float stays a float
	case value.String:
		return e.str(v.AsString())
	case value.Vector:
		elems := v.AsVector().Elems
		return e.container(v, '[', ']', len(elems), depth, func(i int) error {
			return e.value(elems[i], depth+1)
		})
	case value.Map:
		keys := v.AsMap().Keys()
		return e.container(v, '{', '}', len(keys), depth, func(i int) error {
			k := keys[i]
			if k.Kind() == value.Float && (math.IsInf(k.AsFloat(), 0) || math.IsNaN(k.AsFloat())) {
				return e.a.Errorf(value.ValueError, "the map key %s has no JSON form", value.FormatFloat(k.AsFloat()))
			}
			if err := e.str(k.String()); err != nil { // number keys become their text
				return err
			}
			e.b.WriteByte(':')
			if e.indent != "" {
				e.b.WriteByte(' ')
			}
			val, _, _ := v.AsMap().Get(k)
			return e.value(val, depth+1)
		})
	default:
		return e.a.Errorf(value.TypeError, "cannot encode %s as JSON", native.TypeName(v))
	}
	return nil
}

// maxDepth is how deeply vectors and maps (arrays and objects) may nest,
// for encode and decode alike: deeper input could exhaust the stack.
const maxDepth = 10000

func (e *encoder) container(v value.Value, open, close byte, n, depth int, item func(int) error) error {
	if e.open[v.Obj()] {
		return e.a.Errorf(value.ValueError, "the value contains itself")
	}
	if depth >= maxDepth {
		return e.a.Errorf(value.ValueError, "vectors and maps nested more than %d deep", maxDepth)
	}
	e.open[v.Obj()] = true
	defer delete(e.open, v.Obj())
	e.b.WriteByte(open)
	for i := 0; i < n; i++ {
		if i > 0 {
			e.b.WriteByte(',')
		}
		e.newline(depth + 1)
		if err := item(i); err != nil {
			return err
		}
	}
	if n > 0 {
		e.newline(depth)
	}
	e.b.WriteByte(close)
	return nil
}

func (e *encoder) newline(depth int) {
	if e.indent != "" {
		e.b.WriteByte('\n')
		e.b.WriteString(strings.Repeat(e.indent, depth))
	}
}

func (e *encoder) str(s string) error {
	if !utf8.ValidString(s) {
		shown := s
		if len(shown) > 40 { // the string may be long: its start
			shown = shown[:40]
			return e.a.Errorf(value.ValueError, "string %s... (%d bytes) is not valid UTF-8", strconv.Quote(shown), len(s))
		}
		return e.a.Errorf(value.ValueError, "string %s is not valid UTF-8", strconv.Quote(shown))
	}
	e.b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			e.b.WriteByte('\\')
			e.b.WriteByte(c)
		case c == '\n':
			e.b.WriteString(`\n`)
		case c == '\r':
			e.b.WriteString(`\r`)
		case c == '\t':
			e.b.WriteString(`\t`)
		case c < 0x20:
			e.b.WriteString(`\u00`)
			e.b.WriteByte("0123456789abcdef"[c>>4])
			e.b.WriteByte("0123456789abcdef"[c&15])
		default:
			e.b.WriteByte(c)
		}
	}
	e.b.WriteByte('"')
	return nil
}

// decode(s): the value JSON text s stands for. Objects become maps (keys in
// the order of the text), arrays vectors; a number without a fraction or
// exponent that fits in 64 bits becomes an int, any other number a float.
// The decoder is our own, not encoding/json, so that its messages (and the
// byte positions in them) are the same with every version of Go.
func decode(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("json::decode", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	p := &parser{s: s}
	p.space()
	v, err := p.value(0)
	if err == nil {
		p.space()
		if p.i < len(s) {
			err = p.errorf("text after the value")
		}
	}
	switch e := err.(type) {
	case nil:
		return v, nil
	case *syntaxError:
		if e.pos < 0 {
			return value.NilValue, a.Errorf(value.ValueError, "invalid JSON: unexpected end of text")
		}
		return value.NilValue, a.Errorf(value.ValueError, "invalid JSON at byte %d: %s", e.pos+1, e.msg)
	}
	return value.NilValue, a.Errorf(value.ValueError, "%v", err)
}

// errTooDeep is the error for nesting deeper than maxDepth.
var errTooDeep = fmt.Errorf("arrays and objects nested more than %d deep", maxDepth)

// syntaxError is invalid JSON at byte pos of the text (counted from 0;
// -1: the text ended too soon).
type syntaxError struct {
	pos int
	msg string
}

func (e *syntaxError) Error() string { return e.msg }

var errEnd = &syntaxError{pos: -1, msg: "unexpected end of text"}

// parser reads JSON text (RFC 8259) from s at i.
type parser struct {
	s string
	i int
}

func (p *parser) errorf(format string, args ...any) error {
	return &syntaxError{p.i, fmt.Sprintf(format, args...)}
}

// char shows the byte at i in a message: "character 'x'", or "byte 0x01"
// for one that is not printable.
func (p *parser) char() string {
	c := p.s[p.i]
	switch {
	case c == '\'':
		return `character '\''`
	case c >= 0x20 && c < 0x7f:
		return "character '" + string(c) + "'"
	}
	return fmt.Sprintf("byte 0x%02x", c)
}

func (p *parser) space() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t' || p.s[p.i] == '\n' || p.s[p.i] == '\r') {
		p.i++
	}
}

// value reads a value; depth is the number of arrays and objects around
// it.
func (p *parser) value(depth int) (value.Value, error) {
	if p.i >= len(p.s) {
		return value.NilValue, errEnd
	}
	switch c := p.s[p.i]; {
	case c == '{' || c == '[':
		if depth >= maxDepth {
			return value.NilValue, errTooDeep
		}
		if c == '{' {
			return p.object(depth)
		}
		return p.array(depth)
	case c == '"':
		s, err := p.str()
		return value.MakeString(s), err
	case c == '-' || c >= '0' && c <= '9':
		return p.number()
	case c == 't':
		return value.MakeBool(true), p.word("true")
	case c == 'f':
		return value.MakeBool(false), p.word("false")
	case c == 'n':
		return value.NilValue, p.word("null")
	}
	return value.NilValue, p.errorf("invalid %s where a value should be", p.char())
}

func (p *parser) word(w string) error {
	for k := 0; k < len(w); k++ {
		if p.i >= len(p.s) {
			return errEnd
		}
		if p.s[p.i] != w[k] {
			return p.errorf("invalid %s in the literal %s", p.char(), w)
		}
		p.i++
	}
	return nil
}

func (p *parser) array(depth int) (value.Value, error) {
	p.i++ // [
	elems := []value.Value{}
	p.space()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return value.MakeVector(elems), nil
	}
	for {
		p.space()
		v, err := p.value(depth + 1)
		if err != nil {
			return value.NilValue, err
		}
		elems = append(elems, v)
		p.space()
		if p.i >= len(p.s) {
			return value.NilValue, errEnd
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return value.MakeVector(elems), nil
		default:
			return value.NilValue, p.errorf("invalid %s after an array element (',' or ']' expected)", p.char())
		}
	}
}

func (p *parser) object(depth int) (value.Value, error) {
	p.i++ // {
	m := value.NewMap()
	p.space()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return value.MakeMap(m), nil
	}
	for {
		p.space()
		if p.i >= len(p.s) {
			return value.NilValue, errEnd
		}
		if p.s[p.i] != '"' {
			return value.NilValue, p.errorf("invalid %s where an object key (a string) should be", p.char())
		}
		k, err := p.str()
		if err != nil {
			return value.NilValue, err
		}
		p.space()
		if p.i >= len(p.s) {
			return value.NilValue, errEnd
		}
		if p.s[p.i] != ':' {
			return value.NilValue, p.errorf("invalid %s after an object key (':' expected)", p.char())
		}
		p.i++
		p.space()
		v, err := p.value(depth + 1)
		if err != nil {
			return value.NilValue, err
		}
		if err := m.Set(value.MakeString(k), v); err != nil { // a repeated key keeps its first place
			return value.NilValue, err
		}
		p.space()
		if p.i >= len(p.s) {
			return value.NilValue, errEnd
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return value.MakeMap(m), nil
		default:
			return value.NilValue, p.errorf("invalid %s after an object member (',' or '}' expected)", p.char())
		}
	}
}

// str reads a string. Invalid UTF-8 and lone surrogates become U+FFFD, as
// encoding/json does.
func (p *parser) str() (string, error) {
	p.i++ // "
	var b strings.Builder
	for {
		start := p.i
		for p.i < len(p.s) && p.s[p.i] != '"' && p.s[p.i] != '\\' && p.s[p.i] >= 0x20 && p.s[p.i] < 0x80 {
			p.i++
		}
		b.WriteString(p.s[start:p.i])
		if p.i >= len(p.s) {
			return "", errEnd
		}
		switch c := p.s[p.i]; {
		case c == '"':
			p.i++
			return b.String(), nil
		case c < 0x20:
			return "", p.errorf("invalid %s in a string (control characters must be escaped)", p.char())
		case c >= 0x80:
			r, size := utf8.DecodeRuneInString(p.s[p.i:])
			if r == utf8.RuneError && size == 1 {
				r = utf8.RuneError
			}
			b.WriteRune(r)
			p.i += size
		default: // a backslash
			p.i++
			if p.i >= len(p.s) {
				return "", errEnd
			}
			e := p.s[p.i]
			if r, ok := escapes[e]; ok {
				b.WriteByte(r)
				p.i++
				continue
			}
			if e != 'u' {
				return "", p.errorf("invalid %s in an escape (one of \\\" \\\\ \\/ \\b \\f \\n \\r \\t \\u)", p.char())
			}
			p.i++
			r, err := p.hex4()
			if err != nil {
				return "", err
			}
			if utf16.IsSurrogate(r) {
				r2 := utf8.RuneError
				if strings.HasPrefix(p.s[p.i:], "\\u") {
					save := p.i
					p.i += 2
					if r2, err = p.hex4(); err != nil {
						return "", err
					}
					if d := utf16.DecodeRune(r, r2); d != utf8.RuneError {
						r = d
					} else {
						p.i = save // not the second half: it is read on its own
						r = utf8.RuneError
					}
				} else {
					r = utf8.RuneError
				}
			}
			b.WriteRune(r)
		}
	}
}

var escapes = map[byte]byte{'"': '"', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'}

// hex4 reads the four hexadecimal digits of a \u escape.
func (p *parser) hex4() (rune, error) {
	var r rune
	for k := 0; k < 4; k++ {
		if p.i >= len(p.s) {
			return 0, errEnd
		}
		c := p.s[p.i]
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, p.errorf("invalid %s in a \\u escape (four hexadecimal digits expected)", p.char())
		}
		r = r*16 + rune(d)
		p.i++
	}
	return r, nil
}

// number reads -? (0 | [1-9][0-9]*) (. [0-9]+)? ([eE] [+-]? [0-9]+)?.
func (p *parser) number() (value.Value, error) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	digits := func() error {
		if p.i >= len(p.s) {
			return errEnd
		}
		if p.s[p.i] < '0' || p.s[p.i] > '9' {
			return p.errorf("invalid %s in a number (a digit expected)", p.char())
		}
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
		return nil
	}
	if p.i < len(p.s) && p.s[p.i] == '0' {
		p.i++
		if p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			return value.NilValue, p.errorf("a number cannot start with 0 and another digit")
		}
	} else if err := digits(); err != nil {
		return value.NilValue, err
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		if err := digits(); err != nil {
			return value.NilValue, err
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		if err := digits(); err != nil {
			return value.NilValue, err
		}
	}
	return number(p.s[start:p.i])
}

func number(s string) (value.Value, error) {
	if !strings.ContainsAny(s, ".eE") {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return value.MakeInt(i), nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return value.NilValue, err
	}
	return value.MakeFloat(f), nil // a number too large for a float is ±inf
}
