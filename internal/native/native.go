// Package native lets Go code define Kukel modules: functions, constants and
// native classes, whose objects carry a Go value and whose member functions
// are written in Go. A program uses one with "use name;", like a module
// written in Kukel, and reaches its names as name::x.
//
// A module registers itself (Register, usually from an init function); the
// loader, the compiler and the VM look modules up by name. Native classes
// cannot be derived from (yet), and have no data members: everything goes
// through member functions.
package native

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maarten-boot/kukel/internal/value"
)

// Func is a function written in Go, as for the built-ins.
type Func func(env value.Env, args []value.Value) (value.Value, error)

// Module is a module written in Go.
type Module struct {
	Name    string
	Doc     string // one line: what the module is for
	Funcs   map[string]Func
	Consts  map[string]value.Value // nil, booleans, numbers and strings
	Classes []*Class

	globals map[string]value.Value // by qualified name, built by Register
}

// Class is a native class. Methods and Statics are its instance and static
// member functions. New is its constructor (for "new name::C(...)"): it
// sets this.State; without one, objects only come from the module's
// functions (Class.Object). Show formats an object for str() and println;
// without it an object shows as "name::C{}".
type Class struct {
	Name    string
	New     value.NativeMethod
	Methods map[string]value.NativeMethod
	Statics map[string]Func
	Show    func(o *value.ObjectObj) string

	module string
	obj    *value.ClassObj // built by Register; shared by all programs
}

var modules = map[string]*Module{}

// Register adds module m. It panics on a second module with the same name
// (a programming error).
func Register(m *Module) {
	if _, ok := modules[m.Name]; ok {
		panic("native: module " + m.Name + " registered twice")
	}
	m.globals = map[string]value.Value{}
	for name, fn := range m.Funcs {
		m.globals[m.Name+"::"+name] = value.MakeBuiltin(&value.BuiltinObj{Name: m.Name + "::" + name, Fn: fn})
	}
	for _, c := range m.Classes {
		c.build(m.Name)
		m.globals[c.obj.Name] = value.MakeClass(c.obj)
		for _, mo := range c.obj.Methods {
			if mo.Builtin != nil {
				m.globals[mo.Builtin.Name] = value.MakeBuiltin(mo.Builtin)
			}
		}
	}
	modules[m.Name] = m
}

// Lookup returns the module called name.
func Lookup(name string) (*Module, bool) {
	m, ok := modules[name]
	return m, ok
}

// Names returns the names of the registered modules, sorted.
func Names() []string {
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// QualifiedName is "module::Class".
func (c *Class) QualifiedName() string { return c.module + "::" + c.Name }

func (c *Class) build(module string) {
	c.module = module
	k := &value.ClassObj{
		Name:       c.QualifiedName(),
		FieldIndex: map[string]int{},
		Methods:    map[string]*value.MethodObj{},
		Show:       c.Show,
	}
	for name, fn := range c.Methods {
		k.Methods[name] = &value.MethodObj{Name: name, Class: k, Native: fn}
	}
	for name, fn := range c.Statics {
		if _, ok := c.Methods[name]; ok || name == c.Name {
			panic("native: " + k.Name + "::" + name + " is both a static and an instance member function")
		}
		full := k.Name + "::" + name
		k.Methods[name] = &value.MethodObj{Name: name, Class: k, Static: true, Builtin: &value.BuiltinObj{Name: full, Fn: fn}}
	}
	if c.New != nil {
		k.Ctor = &value.MethodObj{Name: c.Name, Class: k, Native: c.New}
		k.Methods[c.Name] = k.Ctor
	}
	c.obj = k
}

// ClassObj is the run-time class.
func (c *Class) ClassObj() *value.ClassObj { return c.obj }

// Object makes a new object of class c holding state.
func (c *Class) Object(state any) value.Value {
	return value.ObjectValue(&value.ObjectObj{Class: c.obj, State: state})
}

// Global returns what a qualified name of a native module stands for at
// run time: a function ("json::encode"), a static member function
// ("time::Time::parse") or a class ("time::Time"). The VM links the global
// slots of a program with it.
func Global(qualified string) (value.Value, bool) {
	name, _, _ := strings.Cut(qualified, "::")
	if m, ok := modules[name]; ok {
		v, ok := m.globals[qualified]
		return v, ok
	}
	return value.NilValue, false
}

// ----------------------------------------------------------------------
// Arguments

// Args are the arguments of a call of a native function, for checking
// them with messages like those of the built-ins.
type Args struct {
	Name string // as in messages: "json::encode", "time::Time::format"
	V    []value.Value
}

// Check checks the number of arguments: min to max (max < 0: no limit).
func Check(name string, args []value.Value, min, max int) (Args, error) {
	a := Args{Name: name, V: args}
	n := len(args)
	switch {
	case max < 0 && n < min:
		return a, value.Errorf(value.ArgumentError, "%s expects at least %d argument(s), got %d", name, min, n)
	case max >= 0 && min == max && n != min:
		return a, value.Errorf(value.ArgumentError, "%s expects %d argument(s), got %d", name, min, n)
	case max >= 0 && (n < min || n > max):
		return a, value.Errorf(value.ArgumentError, "%s expects %d to %d arguments, got %d", name, min, max, n)
	}
	return a, nil
}

// Has reports whether optional argument i was passed.
func (a Args) Has(i int) bool { return i < len(a.V) }

// TypeError is the error for argument i of the wrong type.
func (a Args) TypeError(i int, want string) error {
	return value.Errorf(value.TypeError, "%s: argument %d must be %s, not %s", a.Name, i+1, want, a.V[i].Kind())
}

// String returns argument i, which must be a string.
func (a Args) String(i int) (string, error) {
	if a.V[i].Kind() != value.String {
		return "", a.TypeError(i, "a string")
	}
	return a.V[i].AsString(), nil
}

// Int returns argument i, which must be an int.
func (a Args) Int(i int) (int64, error) {
	if a.V[i].Kind() != value.Int {
		return 0, a.TypeError(i, "an int")
	}
	return a.V[i].AsInt(), nil
}

// Number returns argument i, an int or a float, as a float.
func (a Args) Number(i int) (float64, error) {
	switch v := a.V[i]; v.Kind() {
	case value.Int:
		return float64(v.AsInt()), nil
	case value.Float:
		return v.AsFloat(), nil
	}
	return 0, a.TypeError(i, "a number")
}

// Object returns the Go value of argument i, which must be an object of
// class c.
func (a Args) Object(i int, c *Class) (any, error) {
	if v := a.V[i]; v.Kind() == value.Object && v.AsObject().Class == c.obj {
		return v.AsObject().State, nil
	}
	return nil, a.TypeError(i, "a "+c.QualifiedName())
}

// Errorf returns a Kukel exception of the given kind (value.ValueError,
// value.TypeError, ...), prefixed with the function's name.
func (a Args) Errorf(kind string, format string, args ...any) error {
	return value.Errorf(kind, "%s: %s", a.Name, fmt.Sprintf(format, args...))
}
