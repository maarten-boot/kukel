// Package builtins implements Kukel's built-in functions.
package builtins

import (
	"io"
	"sort"

	"github.com/maarten-boot/kukel/internal/value"
)

var table = map[string]*value.BuiltinObj{}

func define(name string, fn func(env value.Env, args []value.Value) (value.Value, error)) {
	table[name] = &value.BuiltinObj{Name: name, Fn: fn}
}

// Lookup returns the built-in called name.
func Lookup(name string) (*value.BuiltinObj, bool) {
	b, ok := table[name]
	return b, ok
}

// Names returns the names of all built-ins, sorted.
func Names() []string {
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func init() {
	define("print", func(env value.Env, args []value.Value) (value.Value, error) {
		return value.NilValue, write(env.Stdout(), args, "")
	})
	define("println", func(env value.Env, args []value.Value) (value.Value, error) {
		return value.NilValue, write(env.Stdout(), args, "\n")
	})
	define("str", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("str", args, 1); err != nil {
			return value.NilValue, err
		}
		if args[0].Kind() == value.String {
			return args[0], nil
		}
		return value.MakeString(args[0].String()), nil
	})
	define("classof", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("classof", args, 1); err != nil {
			return value.NilValue, err
		}
		if args[0].Kind() != value.Object {
			return value.NilValue, value.Errorf(value.TypeError, "classof: argument 1 must be an object, not %s", args[0].Kind())
		}
		return value.MakeClass(args[0].AsObject().Class), nil
	})
	define("instanceof", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("instanceof", args, 2); err != nil {
			return value.NilValue, err
		}
		if args[1].Kind() != value.Class {
			return value.NilValue, value.Errorf(value.TypeError, "instanceof: argument 2 must be a class, not %s", args[1].Kind())
		}
		ok := args[0].Kind() == value.Object && args[0].AsObject().Class.IsSubclassOf(args[1].AsClass())
		return value.MakeBool(ok), nil
	})
	define("typeof", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("typeof", args, 1); err != nil {
			return value.NilValue, err
		}
		return value.MakeString(args[0].Kind().String()), nil
	})
}

// write prints the arguments separated by single spaces, then end.
func write(w io.Writer, args []value.Value, end string) error {
	buf := make([]byte, 0, 64)
	for i, a := range args {
		if i > 0 {
			buf = append(buf, ' ')
		}
		if a.Kind() == value.String {
			buf = append(buf, a.AsString()...)
		} else {
			buf = append(buf, a.String()...)
		}
	}
	buf = append(buf, end...)
	if _, err := w.Write(buf); err != nil {
		return value.Errorf(value.IOError, "write: %v", err)
	}
	return nil
}

func wantArgs(name string, args []value.Value, n int) error {
	if len(args) != n {
		return value.Errorf(value.ArgumentError, "%s expects %d argument(s), got %d", name, n, len(args))
	}
	return nil
}
