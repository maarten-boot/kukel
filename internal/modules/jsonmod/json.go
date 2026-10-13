// Package jsonmod is the native module "json": encode Kukel values as JSON
// text and decode JSON text into Kukel values.
//
//	use json;
//	local s = json::encode({"a": [1, 2.5, nil]});   // {"a":[1,2.5,null]}
//	println(json::decode(s)["a"][1]);                // 2.5
package jsonmod

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
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
		return e.a.Errorf(value.TypeError, "cannot encode %s as JSON", v.Kind())
	}
	return nil
}

// maxDepth is how deeply vectors and maps may nest, as for decode (Go's
// JSON decoder stops at the same depth).
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
		return e.a.Errorf(value.ValueError, "string %s is not valid UTF-8", strconv.Quote(s))
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
func decode(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("json::decode", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	v, err := decodeValue(d)
	if err == nil {
		if _, extra := d.Token(); extra != io.EOF {
			err = errors.New("text after the value")
		}
	}
	if err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return value.NilValue, a.Errorf(value.ValueError, "invalid JSON at byte %d: %v", syn.Offset, err)
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return value.NilValue, a.Errorf(value.ValueError, "invalid JSON: unexpected end of text")
		}
		return value.NilValue, a.Errorf(value.ValueError, "invalid JSON at byte %d: %v", d.InputOffset(), err)
	}
	return v, nil
}

func decodeValue(d *json.Decoder) (value.Value, error) {
	t, err := d.Token()
	if err != nil {
		return value.NilValue, err
	}
	switch t := t.(type) {
	case nil:
		return value.NilValue, nil
	case bool:
		return value.MakeBool(t), nil
	case string:
		return value.MakeString(t), nil
	case json.Number:
		return number(string(t))
	case json.Delim:
		switch t {
		case '[':
			elems := []value.Value{}
			for d.More() {
				v, err := decodeValue(d)
				if err != nil {
					return value.NilValue, err
				}
				elems = append(elems, v)
			}
			_, err := d.Token() // ]
			return value.MakeVector(elems), err
		case '{':
			m := value.NewMap()
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return value.NilValue, err
				}
				v, err := decodeValue(d)
				if err != nil {
					return value.NilValue, err
				}
				if err := m.Set(value.MakeString(k.(string)), v); err != nil {
					return value.NilValue, err
				}
			}
			_, err := d.Token() // }
			return value.MakeMap(m), err
		}
	}
	return value.NilValue, fmt.Errorf("unexpected %v", t) // Token reports misplaced delimiters itself
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
