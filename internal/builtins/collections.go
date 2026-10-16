package builtins

import (
	"sort"
	"strings"

	"github.com/maarten-boot/kukel/internal/value"
)

// String and collection built-ins (milestone 5).

func init() {
	define("len", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("len", args, 1); err != nil {
			return value.NilValue, err
		}
		n, err := value.Len(args[0])
		return value.MakeInt(int64(n)), err
	})

	// ---- strings ----

	define("find", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := argRange("find", args, 2, 3); err != nil {
			return value.NilValue, err
		}
		s, err := argString("find", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		sub, err := argString("find", args, 1)
		if err != nil {
			return value.NilValue, err
		}
		from := 0
		if len(args) == 3 {
			if from, err = argBound("find", args, 2, len(s), "from"); err != nil {
				return value.NilValue, err
			}
		}
		i := strings.Index(s[from:], sub)
		if i >= 0 {
			i += from
		}
		return value.MakeInt(int64(i)), nil
	})
	define("split", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("split", args, 2); err != nil {
			return value.NilValue, err
		}
		s, err := argString("split", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		sep, err := argString("split", args, 1)
		if err != nil {
			return value.NilValue, err
		}
		var parts []string
		if sep == "" { // single bytes
			for i := 0; i < len(s); i++ {
				parts = append(parts, s[i:i+1])
			}
		} else {
			parts = strings.Split(s, sep)
		}
		elems := make([]value.Value, len(parts))
		for i, p := range parts {
			elems[i] = value.MakeString(p)
		}
		return value.MakeVector(elems), nil
	})
	define("join", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := argRange("join", args, 1, 2); err != nil {
			return value.NilValue, err
		}
		v, err := argVector("join", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		sep := ""
		if len(args) == 2 {
			if sep, err = argString("join", args, 1); err != nil {
				return value.NilValue, err
			}
		}
		var b strings.Builder
		for i, e := range v.Elems {
			if i > 0 {
				b.WriteString(sep)
			}
			b.WriteString(e.String()) // strings as they are, other values as str() gives them
		}
		return value.MakeString(b.String()), nil
	})
	define("upper", stringMap("upper", func(c byte) byte {
		if 'a' <= c && c <= 'z' {
			return c - 'a' + 'A'
		}
		return c
	}))
	define("lower", stringMap("lower", func(c byte) byte {
		if 'A' <= c && c <= 'Z' {
			return c - 'A' + 'a'
		}
		return c
	}))
	define("trim", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("trim", args, 1); err != nil {
			return value.NilValue, err
		}
		s, err := argString("trim", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		return value.MakeString(strings.Trim(s, " \t\n\r\f\v")), nil
	})
	define("replace", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("replace", args, 3); err != nil {
			return value.NilValue, err
		}
		var str [3]string
		for i := range str {
			s, err := argString("replace", args, i)
			if err != nil {
				return value.NilValue, err
			}
			str[i] = s
		}
		if str[1] == "" {
			return value.NilValue, value.Errorf(value.ArgumentError, "replace: the string to replace is empty")
		}
		return value.MakeString(strings.ReplaceAll(str[0], str[1], str[2])), nil
	})
	define("char", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("char", args, 1); err != nil {
			return value.NilValue, err
		}
		c, err := argInt("char", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		if c < 0 || c > 255 {
			return value.NilValue, value.Errorf(value.RangeError, "char: code %d out of range 0..255", c)
		}
		return value.MakeString(string([]byte{byte(c)})), nil
	})
	define("ord", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := argRange("ord", args, 1, 2); err != nil {
			return value.NilValue, err
		}
		if _, err := argString("ord", args, 0); err != nil {
			return value.NilValue, err
		}
		i := value.MakeInt(0)
		if len(args) == 2 {
			i = args[1]
		}
		return value.Index(args[0], i)
	})

	// ---- vectors ----

	define("push", func(env value.Env, args []value.Value) (value.Value, error) {
		if len(args) < 2 {
			return value.NilValue, value.Errorf(value.ArgumentError, "push expects at least 2 arguments, got %d", len(args))
		}
		v, err := argVector("push", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		v.Elems = append(v.Elems, args[1:]...)
		return value.MakeInt(int64(len(v.Elems))), nil
	})
	define("pop", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("pop", args, 1); err != nil {
			return value.NilValue, err
		}
		v, err := argVector("pop", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		if len(v.Elems) == 0 {
			return value.NilValue, value.Errorf(value.RangeError, "pop: the vector is empty")
		}
		last := v.Elems[len(v.Elems)-1]
		v.Elems[len(v.Elems)-1] = value.NilValue
		v.Elems = v.Elems[:len(v.Elems)-1]
		return last, nil
	})
	define("insert", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("insert", args, 3); err != nil {
			return value.NilValue, err
		}
		v, err := argVector("insert", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		i, err := argBound("insert", args, 1, len(v.Elems), "index")
		if err != nil {
			return value.NilValue, err
		}
		v.Elems = append(v.Elems, value.NilValue)
		copy(v.Elems[i+1:], v.Elems[i:])
		v.Elems[i] = args[2]
		return value.NilValue, nil
	})
	define("remove", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("remove", args, 2); err != nil {
			return value.NilValue, err
		}
		v, err := argVector("remove", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		x, err := value.Index(args[0], args[1]) // checks the index
		if err != nil {
			return value.NilValue, err
		}
		i := int(args[1].AsInt())
		copy(v.Elems[i:], v.Elems[i+1:])
		v.Elems[len(v.Elems)-1] = value.NilValue
		v.Elems = v.Elems[:len(v.Elems)-1]
		return x, nil
	})
	define("slice", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := argRange("slice", args, 2, 3); err != nil {
			return value.NilValue, err
		}
		n, err := value.Len(args[0])
		if err != nil || args[0].Kind() == value.Map {
			return value.NilValue, value.Errorf(value.TypeError, "slice: argument 1 must be a vector or string, not %s", args[0].Kind())
		}
		from, err := argBound("slice", args, 1, n, "start")
		if err != nil {
			return value.NilValue, err
		}
		to := n
		if len(args) == 3 {
			if to, err = argBound("slice", args, 2, n, "end"); err != nil {
				return value.NilValue, err
			}
			if to < from {
				return value.NilValue, value.Errorf(value.RangeError, "slice: end %d is before start %d", to, from)
			}
		}
		if args[0].Kind() == value.String {
			return value.MakeString(args[0].AsString()[from:to]), nil
		}
		return value.MakeVector(append([]value.Value{}, args[0].AsVector().Elems[from:to]...)), nil
	})
	define("sort", builtinSort)

	// ---- maps ----

	define("keys", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("keys", args, 1); err != nil {
			return value.NilValue, err
		}
		m, err := argMap("keys", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		return value.MakeVector(m.Keys()), nil
	})
	define("values", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("values", args, 1); err != nil {
			return value.NilValue, err
		}
		m, err := argMap("values", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		vals := make([]value.Value, 0, m.Len())
		m.Each(func(_, v value.Value) { vals = append(vals, v) })
		return value.MakeVector(vals), nil
	})
	define("haskey", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("haskey", args, 2); err != nil {
			return value.NilValue, err
		}
		m, err := argMap("haskey", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		_, ok, err := m.Get(args[1])
		return value.MakeBool(ok), err
	})
	define("delete", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("delete", args, 2); err != nil {
			return value.NilValue, err
		}
		m, err := argMap("delete", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		ok, err := m.Delete(args[1])
		return value.MakeBool(ok), err
	})
}

// builtinSort sorts a vector in place (stable). Without a function it
// compares with <; with one, less(a, b) says whether a goes before b.
func builtinSort(env value.Env, args []value.Value) (value.Value, error) {
	if err := argRange("sort", args, 1, 2); err != nil {
		return value.NilValue, err
	}
	v, err := argVector("sort", args, 0)
	if err != nil {
		return value.NilValue, err
	}
	elems := append([]value.Value{}, v.Elems...) // safe if less changes the vector
	var sortErr error
	less := func(a, b value.Value) bool {
		r, err := value.Compare(value.Less, a, b)
		if err != nil && sortErr == nil {
			sortErr = err
		}
		return r
	}
	if len(args) == 2 {
		fn := args[1]
		if !fn.IsCallable() {
			return value.NilValue, value.Errorf(value.TypeError, "sort: argument 2 must be a function, not %s", fn.Kind())
		}
		less = func(a, b value.Value) bool {
			if sortErr != nil {
				return false
			}
			r, err := env.Call(fn, []value.Value{a, b})
			if err != nil {
				sortErr = err
				return false
			}
			return r.Truthy()
		}
	}
	sort.SliceStable(elems, func(i, j int) bool { return less(elems[i], elems[j]) })
	if sortErr != nil {
		return value.NilValue, sortErr
	}
	copy(v.Elems, elems)
	return value.NilValue, nil
}

// stringMap builds a built-in that maps each byte of a string.
func stringMap(name string, f func(byte) byte) func(value.Env, []value.Value) (value.Value, error) {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs(name, args, 1); err != nil {
			return value.NilValue, err
		}
		s, err := argString(name, args, 0)
		if err != nil {
			return value.NilValue, err
		}
		b := []byte(s)
		for i := range b {
			b[i] = f(b[i])
		}
		return value.MakeString(string(b)), nil
	}
}

// ----------------------------------------------------------------------
// Argument helpers. Argument positions in messages count from 1.

func argRange(name string, args []value.Value, min, max int) error {
	if len(args) < min || len(args) > max {
		return value.Errorf(value.ArgumentError, "%s expects %d to %d arguments, got %d", name, min, max, len(args))
	}
	return nil
}

func argTypeError(name string, i int, want string, got value.Value) error {
	return value.Errorf(value.TypeError, "%s: argument %d must be %s, not %s", name, i+1, want, got.Kind())
}

func argString(name string, args []value.Value, i int) (string, error) {
	if args[i].Kind() != value.String {
		return "", argTypeError(name, i, "a string", args[i])
	}
	return args[i].AsString(), nil
}

func argInt(name string, args []value.Value, i int) (int64, error) {
	if args[i].Kind() != value.Int {
		return 0, argTypeError(name, i, "an int", args[i])
	}
	return args[i].AsInt(), nil
}

// argBound returns an int argument that must lie in 0..n.
func argBound(name string, args []value.Value, i, n int, what string) (int, error) {
	x, err := argInt(name, args, i)
	if err != nil {
		return 0, err
	}
	if x < 0 || x > int64(n) {
		return 0, value.Errorf(value.RangeError, "%s: %s %d out of range 0..%d", name, what, x, n)
	}
	return int(x), nil
}

func argVector(name string, args []value.Value, i int) (*value.VectorObj, error) {
	if args[i].Kind() != value.Vector {
		return nil, argTypeError(name, i, "a vector", args[i])
	}
	return args[i].AsVector(), nil
}

func argMap(name string, args []value.Value, i int) (*value.MapObj, error) {
	if args[i].Kind() != value.Map {
		return nil, argTypeError(name, i, "a map", args[i])
	}
	return args[i].AsMap(), nil
}
