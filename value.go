package kukel

import (
	"fmt"
	"math"
	"reflect"
	"sort"

	"github.com/maarten-boot/kukel/internal/value"
)

// Value is a Kukel value: nil, a boolean, an int (64 bits), a float, a
// string (bytes), a vector, a map, a function, a class or an object. The
// zero Value is nil.
//
// Vectors, maps and objects are shared, not copied: a Value taken from an
// interpreter refers to the interpreter's data. Use it only while that
// interpreter does not run on another goroutine.
type Value struct{ v value.Value }

// Nil is the Kukel value nil.
var Nil = Value{}

// Bool makes a boolean.
func Bool(b bool) Value { return Value{value.MakeBool(b)} }

// Int makes an int.
func Int(i int64) Value { return Value{value.MakeInt(i)} }

// Float makes a float.
func Float(f float64) Value { return Value{value.MakeFloat(f)} }

// String makes a string.
func String(s string) Value { return Value{value.MakeString(s)} }

// Vector makes a new vector of the elements.
func Vector(elems ...Value) Value {
	vs := make([]value.Value, len(elems))
	for i, e := range elems {
		vs[i] = e.v
	}
	return Value{value.MakeVector(vs)}
}

// NewMap makes a new, empty map; add entries with SetKey.
func NewMap() Value { return Value{value.MakeMap(value.NewMap())} }

// MapOf makes a new map of m, with the keys in sorted order (Kukel maps
// keep the order in which keys were added).
func MapOf(m map[string]Value) Value {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	mo := value.NewMap()
	for _, k := range keys {
		mo.Set(value.MakeString(k), m[k].v) // a string key cannot fail
	}
	return Value{value.MakeMap(mo)}
}

// Kind is what typeof gives in Kukel: "nil", "bool", "int", "float",
// "string", "vector", "map", "function", "class" or "object".
func (v Value) Kind() string { return v.v.Kind().String() }

// IsNil reports whether v is nil.
func (v Value) IsNil() bool { return v.v.IsNil() }

// AsBool gives a boolean's value.
func (v Value) AsBool() (b, ok bool) {
	if v.v.Kind() != value.Bool {
		return false, false
	}
	return v.v.AsBool(), true
}

// AsInt gives an int's value.
func (v Value) AsInt() (int64, bool) {
	if v.v.Kind() != value.Int {
		return 0, false
	}
	return v.v.AsInt(), true
}

// AsFloat gives a float's value, or an int's as a float.
func (v Value) AsFloat() (float64, bool) {
	switch v.v.Kind() {
	case value.Float:
		return v.v.AsFloat(), true
	case value.Int:
		return float64(v.v.AsInt()), true
	}
	return 0, false
}

// AsString gives a string's value.
func (v Value) AsString() (string, bool) {
	if v.v.Kind() != value.String {
		return "", false
	}
	return v.v.AsString(), true
}

// Truthy reports whether v counts as true in a condition (everything but
// nil, false, 0 and 0.0).
func (v Value) Truthy() bool { return v.v.Truthy() }

// Len is the length of a string (in bytes), vector or map, else -1.
func (v Value) Len() int {
	n, err := value.Len(v.v)
	if err != nil {
		return -1
	}
	return n
}

// Index gives element i of a vector (Nil if v is not a vector or i is out
// of range).
func (v Value) Index(i int) Value {
	if v.v.Kind() != value.Vector {
		return Nil
	}
	elems := v.v.AsVector().Elems
	if i < 0 || i >= len(elems) {
		return Nil
	}
	return Value{elems[i]}
}

// Get gives the value of key in a map (Nil if v is not a map or has no
// such key).
func (v Value) Get(key Value) Value {
	if v.v.Kind() != value.Map {
		return Nil
	}
	e, _, _ := v.v.AsMap().Get(key.v)
	return Value{e}
}

// Keys gives a map's keys, in order.
func (v Value) Keys() []Value {
	if v.v.Kind() != value.Map {
		return nil
	}
	keys := v.v.AsMap().Keys()
	out := make([]Value, len(keys))
	for i, k := range keys {
		out[i] = Value{k}
	}
	return out
}

// Push appends e to a vector.
func (v Value) Push(e Value) error {
	if v.v.Kind() != value.Vector {
		return fmt.Errorf("kukel: Push on %s", v.Kind())
	}
	vec := v.v.AsVector()
	vec.Elems = append(vec.Elems, e.v)
	return nil
}

// SetKey sets key to val in a map. Keys are strings and numbers.
func (v Value) SetKey(key, val Value) error {
	if v.v.Kind() != value.Map {
		return fmt.Errorf("kukel: SetKey on %s", v.Kind())
	}
	return v.v.AsMap().Set(key.v, val.v)
}

// String gives v as Kukel's print and str() show it.
func (v Value) String() string { return v.v.String() }

// FromGo makes a Value of a Go value: nil, a bool, any integer or float
// type, a string, a Value, a slice (a vector) or a map with string keys (a
// map, keys in sorted order), also nested.
func FromGo(x any) (Value, error) {
	switch x := x.(type) {
	case nil:
		return Nil, nil
	case Value:
		return x, nil
	case bool:
		return Bool(x), nil
	case string:
		return String(x), nil
	case int:
		return Int(int64(x)), nil
	case int64:
		return Int(x), nil
	case float64:
		return Float(x), nil
	case []Value:
		return Vector(x...), nil
	}
	r := reflect.ValueOf(x)
	switch r.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Int(r.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := r.Uint()
		if u > math.MaxInt64 {
			return Nil, fmt.Errorf("kukel: %d does not fit in an int", u)
		}
		return Int(int64(u)), nil
	case reflect.Float32, reflect.Float64:
		return Float(r.Float()), nil
	case reflect.Slice, reflect.Array:
		elems := make([]Value, r.Len())
		for i := range elems {
			var err error
			if elems[i], err = FromGo(r.Index(i).Interface()); err != nil {
				return Nil, err
			}
		}
		return Vector(elems...), nil
	case reflect.Map:
		if r.Type().Key().Kind() != reflect.String {
			return Nil, fmt.Errorf("kukel: map keys must be strings, not %s", r.Type().Key())
		}
		m := map[string]Value{}
		for it := r.MapRange(); it.Next(); {
			e, err := FromGo(it.Value().Interface())
			if err != nil {
				return Nil, err
			}
			m[it.Key().String()] = e
		}
		return MapOf(m), nil
	}
	return Nil, fmt.Errorf("kukel: cannot make a value of %T", x)
}

// ToGo gives v as a Go value: nil, bool, int64, float64, string, []any for
// a vector and map[string]any for a map (keys as str() shows them), nested.
// A function, class or object stays a Value. A vector or map that contains
// itself is an error.
func (v Value) ToGo() (any, error) {
	return toGo(v.v, map[any]bool{})
}

func toGo(v value.Value, open map[any]bool) (any, error) {
	switch v.Kind() {
	case value.Nil:
		return nil, nil
	case value.Bool:
		return v.AsBool(), nil
	case value.Int:
		return v.AsInt(), nil
	case value.Float:
		return v.AsFloat(), nil
	case value.String:
		return v.AsString(), nil
	case value.Vector, value.Map:
		if open[v.Obj()] {
			return nil, fmt.Errorf("kukel: ToGo of a %s that contains itself", v.Kind())
		}
		open[v.Obj()] = true
		defer delete(open, v.Obj())
		if v.Kind() == value.Vector {
			out := make([]any, len(v.AsVector().Elems))
			for i, e := range v.AsVector().Elems {
				var err error
				if out[i], err = toGo(e, open); err != nil {
					return nil, err
				}
			}
			return out, nil
		}
		out := map[string]any{}
		var err error
		v.AsMap().Each(func(k, e value.Value) {
			if err == nil {
				out[k.String()], err = toGo(e, open)
			}
		})
		return out, err
	}
	return Value{v}, nil
}
