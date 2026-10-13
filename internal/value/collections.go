package value

import (
	"math"
)

// ----------------------------------------------------------------------
// Maps

// mapKey is the normalized form of a map key: strings by content, numbers
// by value, so that 1 and 1.0 are the same key.
type mapKey struct {
	kind Kind // Int, Float (non-integral only) or String
	num  uint64
	str  string
}

// keyOf normalizes a key. Keys must be strings or numbers (not NaN).
func keyOf(k Value) (mapKey, error) {
	switch k.kind {
	case Int:
		return mapKey{kind: Int, num: k.num}, nil
	case Float:
		f := k.AsFloat()
		if f != f {
			return mapKey{}, Errorf(TypeError, "nan cannot be a map key")
		}
		if f == math.Trunc(f) && f >= -9223372036854775808.0 && f < 9223372036854775808.0 {
			return mapKey{kind: Int, num: uint64(int64(f))}, nil // also turns -0.0 into 0
		}
		return mapKey{kind: Float, num: math.Float64bits(f)}, nil
	case String:
		return mapKey{kind: String, str: k.AsString()}, nil
	}
	return mapKey{}, Errorf(TypeError, "map keys must be strings or numbers, not %s", k.kind)
}

type mapEntry struct {
	key, value Value
	deleted    bool
}

// MapObj is the shared, mutable storage of a map. It keeps the insertion
// order of its keys.
type MapObj struct {
	entries []mapEntry     // in insertion order; deleted entries are kept until compaction
	index   map[mapKey]int // key -> position in entries
}

func NewMap() *MapObj { return &MapObj{index: map[mapKey]int{}} }

func MakeMap(m *MapObj) Value { return Value{kind: Map, obj: m} }

// Len returns the number of keys.
func (m *MapObj) Len() int { return len(m.index) }

// Get returns the value for k; ok is false when k is not a key.
func (m *MapObj) Get(k Value) (v Value, ok bool, err error) {
	mk, err := keyOf(k)
	if err != nil {
		return NilValue, false, err
	}
	if i, ok := m.index[mk]; ok {
		return m.entries[i].value, true, nil
	}
	return NilValue, false, nil
}

// Set sets the value for k. An existing equal key keeps its place (and its
// original form: m[1.0] = x after m[1] = y keeps the key 1).
func (m *MapObj) Set(k, v Value) error {
	mk, err := keyOf(k)
	if err != nil {
		return err
	}
	if i, ok := m.index[mk]; ok {
		m.entries[i].value = v
		return nil
	}
	m.index[mk] = len(m.entries)
	m.entries = append(m.entries, mapEntry{key: k, value: v})
	return nil
}

// Delete removes k and reports whether it was a key.
func (m *MapObj) Delete(k Value) (bool, error) {
	mk, err := keyOf(k)
	if err != nil {
		return false, err
	}
	i, ok := m.index[mk]
	if !ok {
		return false, nil
	}
	delete(m.index, mk)
	m.entries[i] = mapEntry{deleted: true}
	if len(m.entries) > 32 && len(m.index) < len(m.entries)/2 {
		m.compact()
	}
	return true, nil
}

func (m *MapObj) compact() {
	live := m.entries[:0:0]
	for _, e := range m.entries {
		if !e.deleted {
			live = append(live, e)
		}
	}
	m.entries = live
	for i, e := range live {
		mk, _ := keyOf(e.key)
		m.index[mk] = i
	}
}

// Each calls f for each key and value in insertion order.
func (m *MapObj) Each(f func(k, v Value)) {
	for _, e := range m.entries {
		if !e.deleted {
			f(e.key, e.value)
		}
	}
}

// Keys returns the keys in insertion order.
func (m *MapObj) Keys() []Value {
	keys := make([]Value, 0, len(m.index))
	m.Each(func(k, _ Value) { keys = append(keys, k) })
	return keys
}

// ----------------------------------------------------------------------
// Indexing

// Len returns the length of a string (in bytes), vector or map.
func Len(v Value) (int, error) {
	switch v.kind {
	case String:
		return len(v.AsString()), nil
	case Vector:
		return len(v.AsVector().Elems), nil
	case Map:
		return v.AsMap().Len(), nil
	}
	return 0, Errorf(TypeError, "%s has no length", v.kind)
}

// intIndex checks a vector or string index.
func intIndex(what string, i Value, n int) (int, error) {
	if i.kind != Int {
		return 0, Errorf(TypeError, "%s index must be an int, not %s", what, i.kind)
	}
	x := i.AsInt()
	if x < 0 || x >= int64(n) {
		return 0, Errorf(RangeError, "index %d out of range for %s of length %d", x, what, n)
	}
	return int(x), nil
}

// Index returns x[i]: an element of a vector, the byte (an int) of a
// string, or the value of a map key (nil for a missing key).
func Index(x, i Value) (Value, error) {
	switch x.kind {
	case Vector:
		elems := x.AsVector().Elems
		n, err := intIndex("vector", i, len(elems))
		if err != nil {
			return NilValue, err
		}
		return elems[n], nil
	case String:
		s := x.AsString()
		n, err := intIndex("string", i, len(s))
		if err != nil {
			return NilValue, err
		}
		return MakeInt(int64(s[n])), nil
	case Map:
		v, _, err := x.AsMap().Get(i)
		return v, err
	}
	return NilValue, Errorf(TypeError, "cannot index %s", x.kind)
}

// SetIndex does x[i] = v for a vector (an existing index) or a map.
func SetIndex(x, i, v Value) error {
	switch x.kind {
	case Vector:
		elems := x.AsVector().Elems
		n, err := intIndex("vector", i, len(elems))
		if err != nil {
			return err
		}
		elems[n] = v
		return nil
	case Map:
		return x.AsMap().Set(i, v)
	case String:
		return Errorf(TypeError, "strings cannot be changed (s[i] = x)")
	}
	return Errorf(TypeError, "cannot index %s", x.kind)
}

// ----------------------------------------------------------------------
// Iteration (for-each)

// IteratorObj is the state of a for-each loop.
type IteratorObj struct {
	x    Value   // the vector, map or string
	keys []Value // map: snapshot of the keys at the start
	i    int
}

// NewIterator starts iterating over a vector, map or string.
func NewIterator(x Value) (Value, error) {
	it := &IteratorObj{x: x}
	switch x.kind {
	case Vector, String:
	case Map:
		it.keys = x.AsMap().Keys()
	default:
		return NilValue, Errorf(TypeError, "cannot iterate over %s", x.kind)
	}
	return Value{kind: Iterator, obj: it}, nil
}

// Next returns the next pair: (element index, element) for a vector,
// (key, value) for a map, (byte index, byte) for a string. ok is false at
// the end. A vector is walked while the index is below its current length;
// map keys deleted during the loop are skipped.
func (v Value) Next() (first, second Value, ok bool) {
	it := v.obj.(*IteratorObj)
	switch it.x.kind {
	case Vector:
		elems := it.x.AsVector().Elems
		if it.i >= len(elems) {
			return NilValue, NilValue, false
		}
		it.i++
		return MakeInt(int64(it.i - 1)), elems[it.i-1], true
	case String:
		s := it.x.AsString()
		if it.i >= len(s) {
			return NilValue, NilValue, false
		}
		it.i++
		return MakeInt(int64(it.i - 1)), MakeInt(int64(s[it.i-1])), true
	}
	m := it.x.AsMap()
	for it.i < len(it.keys) {
		k := it.keys[it.i]
		it.i++
		if val, present, _ := m.Get(k); present {
			return k, val, true
		}
	}
	return NilValue, NilValue, false
}

// OneNameGetsKey reports what a for-each loop with one name gets from
// Next: the first value (the key) for a map, the second (the element or
// byte) otherwise.
func (v Value) OneNameGetsKey() bool { return v.obj.(*IteratorObj).x.kind == Map }
