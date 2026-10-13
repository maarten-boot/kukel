// Package value defines Kukel's runtime values and the semantics of the
// operators on them. The compiler uses the same operations for constant
// folding as the VM uses at run time.
package value

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// Kind is the type of a value.
type Kind uint8

const (
	Nil Kind = iota
	Bool
	Int
	Float
	String
	Vector
	Map
	Function
	Builtin
	Class
	Object
	BoundMethod
	Closure  // a function literal with its captured variables
	Iterator // internal: the state of a for-each loop
	Internal // internal: a Go value the VM keeps in a slot (a pending exception)
	Cell     // internal: a captured variable
)

var kindNames = [...]string{
	Nil:         "nil",
	Bool:        "bool",
	Int:         "int",
	Float:       "float",
	String:      "string",
	Vector:      "vector",
	Map:         "map",
	Function:    "function",
	Builtin:     "function",
	Class:       "class",
	Object:      "object",
	BoundMethod: "function",
	Closure:     "function",
	Iterator:    "iterator",
	Internal:    "internal",
	Cell:        "cell",
}

// String returns the name typeof() reports for k.
func (k Kind) String() string { return kindNames[k] }

// Value is a Kukel value. It is a small tagged struct, so that numbers do
// not allocate: num holds the bits of a Bool, Int or Float, obj the payload
// of the other kinds (string, *VectorObj, a function object, *BuiltinObj).
// The zero Value is nil.
type Value struct {
	kind Kind
	num  uint64
	obj  any
}

var (
	NilValue = Value{}
	True     = Value{kind: Bool, num: 1}
	False    = Value{kind: Bool}
)

func MakeBool(b bool) Value {
	if b {
		return True
	}
	return False
}
func MakeInt(i int64) Value     { return Value{kind: Int, num: uint64(i)} }
func MakeFloat(f float64) Value { return Value{kind: Float, num: math.Float64bits(f)} }
func MakeString(s string) Value { return Value{kind: String, obj: s} }

// VectorObj is the shared, mutable storage of a vector.
type VectorObj struct {
	Elems []Value
}

func MakeVector(elems []Value) Value { return Value{kind: Vector, obj: &VectorObj{Elems: elems}} }

// MakeFunction wraps a function object (a *bytecode.Function); Named gives
// its name for printing.
func MakeFunction(f Named) Value { return Value{kind: Function, obj: f} }

// Named is implemented by function objects.
type Named interface {
	Name() string
}

// Env is what built-in functions get from the VM.
type Env interface {
	Stdout() io.Writer
	Stderr() io.Writer
	Stdin() *bufio.Reader
	// Call calls a Kukel function (or built-in) and returns its result.
	Call(fn Value, args []Value) (Value, error)
	// Rand is the program's random number source (seed sets it).
	Rand() *rand.Rand
	// Started is when the program started (for clock).
	Started() time.Time
	// Local returns the value stored under key for this interpreter,
	// storing init() first if there is none: per-program state of a
	// native module (such as its event loop).
	Local(key any, init func() any) any
	// Thrown returns the value a Kukel exception carries, when err (from
	// Call) is one that a try could catch; not for exit() or fatal errors.
	Thrown(err error) (Value, bool)
}

// ExitError is returned by the exit built-in: the program stops at once
// with this exit code (try cannot catch it; finally blocks do not run).
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// BuiltinObj is a function implemented in Go. It returns an error message
// (a runtime error) or a result. args is a view of the VM's stack, valid
// only during the call: copy values out of it, never keep the slice.
type BuiltinObj struct {
	Name string
	Fn   func(env Env, args []Value) (Value, error)
}

func MakeBuiltin(b *BuiltinObj) Value { return Value{kind: Builtin, obj: b} }

func (v Value) Kind() Kind           { return v.kind }
func (v Value) IsNil() bool          { return v.kind == Nil }
func (v Value) AsBool() bool         { return v.num != 0 }
func (v Value) AsInt() int64         { return int64(v.num) }
func (v Value) AsFloat() float64     { return math.Float64frombits(v.num) }
func (v Value) AsString() string     { return v.obj.(string) }
func (v Value) AsVector() *VectorObj { return v.obj.(*VectorObj) }
func (v Value) AsMap() *MapObj       { return v.obj.(*MapObj) }
func (v Value) AsBuiltin() *BuiltinObj {
	return v.obj.(*BuiltinObj)
}

// Obj returns the payload of a Function value (or any other object).
func (v Value) Obj() any { return v.obj }

// Truthy reports whether v counts as true: nil, false, 0 and 0.0 are false,
// everything else is true.
func (v Value) Truthy() bool {
	switch v.kind {
	case Nil:
		return false
	case Bool, Int:
		return v.num != 0
	case Float:
		return v.AsFloat() != 0
	}
	return true
}

// String returns the text print() and str() produce for v.
func (v Value) String() string {
	switch v.kind { // simple values without a builder
	case Nil:
		return "nil"
	case Bool:
		return strconv.FormatBool(v.AsBool())
	case Int:
		return strconv.FormatInt(v.AsInt(), 10)
	case Float:
		return FormatFloat(v.AsFloat())
	case String:
		return v.AsString()
	}
	var b strings.Builder
	v.write(&b, false, nil)
	return b.String()
}

// Repr returns v as it appears inside a container: strings are quoted.
func (v Value) Repr() string {
	var b strings.Builder
	v.write(&b, true, nil)
	return b.String()
}

// write appends the text of v. open holds the containers being written, so
// that a container inside itself prints as [...] or {...}.
func (v Value) write(b *strings.Builder, quote bool, open map[any]bool) {
	if v.kind == Vector || v.kind == Map || v.kind == Object {
		if open[v.obj] {
			switch v.kind {
			case Vector:
				b.WriteString("[...]")
			case Map:
				b.WriteString("{...}")
			default:
				b.WriteString(v.AsObject().Class.Name + "{...}")
			}
			return
		}
		if open == nil {
			open = map[any]bool{}
		}
		open[v.obj] = true
		defer delete(open, v.obj)
	}
	switch v.kind {
	case Nil:
		b.WriteString("nil")
	case Bool:
		b.WriteString(strconv.FormatBool(v.AsBool()))
	case Int:
		b.WriteString(strconv.FormatInt(v.AsInt(), 10))
	case Float:
		b.WriteString(FormatFloat(v.AsFloat()))
	case String:
		if quote {
			b.WriteString(Quote(v.AsString()))
		} else {
			b.WriteString(v.AsString())
		}
	case Vector:
		b.WriteByte('[')
		for i, e := range v.AsVector().Elems {
			if i > 0 {
				b.WriteString(", ")
			}
			e.write(b, true, open)
		}
		b.WriteByte(']')
	case Map:
		b.WriteByte('{')
		first := true
		v.AsMap().Each(func(k, e Value) {
			if !first {
				b.WriteString(", ")
			}
			first = false
			k.write(b, true, open)
			b.WriteString(": ")
			e.write(b, true, open)
		})
		b.WriteByte('}')
	case Class:
		b.WriteString("<class " + v.AsClass().Name + ">")
	case Object:
		o := v.AsObject()
		if o.Class.Show != nil {
			b.WriteString(o.Class.Show(o))
			return
		}
		b.WriteString(o.Class.Name)
		b.WriteByte('{')
		for i, name := range o.Class.FieldNames {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(name)
			b.WriteString(": ")
			o.Fields[i].write(b, true, open)
		}
		b.WriteByte('}')
	case BoundMethod:
		b.WriteString("<method " + v.AsBoundMethod().Method.FullName() + ">")
	case Closure:
		b.WriteString("<function " + v.AsClosure().Fn.Name() + ">")
	case Cell:
		b.WriteString("<cell>")
	case Iterator:
		b.WriteString("<iterator>")
	case Internal:
		b.WriteString("<internal>")
	case Function:
		b.WriteString("<function " + v.obj.(Named).Name() + ">")
	case Builtin:
		b.WriteString("<builtin " + v.AsBuiltin().Name + ">")
	}
}

// FormatFloat formats f as the shortest text that reads back as f, always
// with a "." or an exponent so that it does not look like an int.
func FormatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

// Quote writes s as a Kukel string literal.
func Quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\0`)
		default:
			if c < 0x20 || c == 0x7f {
				const hex = "0123456789abcdef"
				b.WriteString(`\x`)
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&15])
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// MakeInternal wraps a Go value that only the VM uses.
func MakeInternal(x any) Value { return Value{kind: Internal, obj: x} }

// CellObj holds a captured variable, shared by the frame that declared it
// and the closures that captured it.
type CellObj struct {
	V Value
}

func NewCell(v Value) Value      { return Value{kind: Cell, obj: &CellObj{V: v}} }
func (v Value) AsCell() *CellObj { return v.obj.(*CellObj) }

// ClosureObj is a function literal's value: the function, the cells of the
// variables it captured, and the this of the member function it was made in.
type ClosureObj struct {
	Fn    Named
	Cells []*CellObj
	This  *ObjectObj // nil outside member functions
}

func MakeClosure(c *ClosureObj) Value  { return Value{kind: Closure, obj: c} }
func (v Value) AsClosure() *ClosureObj { return v.obj.(*ClosureObj) }

// IsCallable reports whether v can be called: a function, function literal,
// bound method or built-in.
func (v Value) IsCallable() bool {
	switch v.kind {
	case Function, Closure, BoundMethod, Builtin:
		return true
	}
	return false
}
