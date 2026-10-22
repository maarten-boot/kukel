package debugger

import (
	"strings"
	"unicode/utf8"

	"github.com/maarten-boot/kukel/internal/value"
)

// The bounds of Show: the elements of a vector or map, and the text in all.
const (
	showElems = 100
	showChars = 1000
)

// more stands for what Show leaves out.
const more = "…"

// Show gives v as print shows it (Repr), shortened for the debugger: at
// most 100 elements of each vector or map, and about 1000 characters in
// all; "…" stands for what is left out. A value inside itself shows as
// [...], {...} or C{...}, as with Repr.
func Show(v value.Value) string {
	var b strings.Builder
	show(&b, v, map[any]bool{})
	return b.String()
}

// show writes v to b; when b is full, only "…".
func show(b *strings.Builder, v value.Value, open map[any]bool) {
	if b.Len() >= showChars {
		b.WriteString(more)
		return
	}
	var key any
	switch v.Kind() {
	case value.String:
		s := v.AsString()
		if left := showChars - b.Len(); len(s) > left {
			q := value.Quote(s[:runeStart(s, left)])
			b.WriteString(q[:len(q)-1] + more + `"`)
			return
		}
		b.WriteString(value.Quote(s))
		return
	case value.Vector:
		key = v.AsVector()
	case value.Map:
		key = v.AsMap()
	case value.Object:
		o := v.AsObject()
		if o.Class.Show != nil {
			b.WriteString(cut(v.Repr(), showChars-b.Len()))
			return
		}
		key = o
	default:
		b.WriteString(v.Repr())
		return
	}
	if open[key] {
		switch v.Kind() {
		case value.Vector:
			b.WriteString("[...]")
		case value.Map:
			b.WriteString("{...}")
		default:
			b.WriteString(v.AsObject().Class.Name + "{...}")
		}
		return
	}
	open[key] = true
	defer delete(open, key)
	// elem writes the separator and an element, and reports whether there
	// is room for more.
	n := 0
	elem := func(write func()) bool {
		if n > 0 {
			b.WriteString(", ")
		}
		if n == showElems || b.Len() >= showChars {
			b.WriteString(more)
			return false
		}
		n++
		write()
		return true
	}
	switch v.Kind() {
	case value.Vector:
		b.WriteByte('[')
		for _, e := range v.AsVector().Elems {
			if !elem(func() { show(b, e, open) }) {
				break
			}
		}
		b.WriteByte(']')
	case value.Map:
		b.WriteByte('{')
		full := false
		v.AsMap().Each(func(k, e value.Value) {
			if full {
				return
			}
			full = !elem(func() {
				show(b, k, open)
				b.WriteString(": ")
				show(b, e, open)
			})
		})
		b.WriteByte('}')
	default:
		o := v.AsObject()
		b.WriteString(o.Class.Name)
		b.WriteByte('{')
		for i, name := range o.Class.FieldNames {
			if !elem(func() {
				b.WriteString(name + ": ")
				show(b, o.Fields[i], open)
			}) {
				break
			}
		}
		b.WriteByte('}')
	}
}

// cut shortens s to at most n bytes (whole characters), with "…" if it did.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:runeStart(s, max(n, 0))] + more
}

// runeStart moves i (< len(s)) back to the start of the character it is in.
func runeStart(s string, i int) int {
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}
