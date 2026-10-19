// Package native lets Go code define Kukel modules: functions, constants and
// native classes, whose objects carry a Go value and whose member functions
// are written in Go. A program uses one with "use name;", like a module
// written in Kukel, and reaches its names as name::x.
//
// A module registers itself (Register, usually from an init function); the
// loader, the compiler and the VM look modules up by name. Native classes
// have no data members: everything goes through member functions. A Kukel
// class can derive from a native class that has a constructor; its objects
// then hold the Go value too, once the derived constructor has called the
// native one.
package native

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/maarten-boot/kukel/internal/token"
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

// omitted are the standard modules this kukel was built without, with why
// (for the message about a "use" of one).
var omitted = map[string]string{}

// Omit records that module name is not in this kukel, and why: "it was
// built with the build tag nozmq", "it was built without the build tag tk".
func Omit(name, why string) { omitted[name] = why }

// Omitted gives why module name is not in this kukel, if a build tag left
// it out.
func Omitted(name string) (string, bool) {
	why, ok := omitted[name]
	return why, ok
}

// Register adds module m. It panics on a second module with the same name
// (a programming error).
func Register(m *Module) {
	if _, ok := modules[m.Name]; ok {
		panic("native: module " + m.Name + " registered twice")
	}
	Prepare(m)
	modules[m.Name] = m
}

// Prepare readies m for use without registering it: for a module that
// belongs to one program (Config.Modules). Register prepares a module
// itself.
func Prepare(m *Module) {
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
	for _, names := range []map[string]bool{keysOf(c.Methods), keysOf(c.Statics)} {
		for name := range names {
			if token.Lookup(name) != token.IDENT { // p->while(...) cannot be written
				panic("native: " + module + "::" + c.Name + "::" + name + " is a keyword")
			}
		}
	}
	k := &value.ClassObj{
		Name:       c.QualifiedName(),
		FieldIndex: map[string]int{},
		Methods:    map[string]*value.MethodObj{},
		Native:     true,
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

// Global returns what a qualified name of a registered module stands for
// at run time: a function ("json::encode"), a static member function
// ("time::Time::parse") or a class ("time::Time"). The VM links the global
// slots of a program with it.
func Global(qualified string) (value.Value, bool) {
	return (*Config)(nil).Global(qualified)
}

// Config is what one program may use beyond its own code: modules written
// for it (by a Go program that embeds Kukel), which of the registered
// modules and built-ins are allowed, and where "use" finds modules written
// in Kukel. The compiler, the loader and the VM (also of the program's
// workers) follow it. A nil *Config allows everything and finds Kukel
// modules only next to the files that use them.
type Config struct {
	Modules        []*Module       // this program's own modules (prepared), found before the registered ones
	AllowedModules map[string]bool // registered modules the program may use; nil: all
	DeniedBuiltins map[string]bool // built-ins the program may not use
	Path           []string        // directories where "use" looks for name.kkl after the using file's directory
	NoFileModules  bool            // "use" never reads .kkl files (modules written in Go only)
}

// Lookup finds module name: one of the program's own, else a registered
// module if it is allowed.
func (c *Config) Lookup(name string) (*Module, bool) {
	if c != nil {
		for _, m := range c.Modules {
			if m.Name == name {
				return m, true
			}
		}
		if c.AllowedModules != nil && !c.AllowedModules[name] {
			return nil, false
		}
	}
	return Lookup(name)
}

// Global is the run-time value of a qualified name of a module the program
// may use (see the function Global).
func (c *Config) Global(qualified string) (value.Value, bool) {
	name, _, _ := strings.Cut(qualified, "::")
	if m, ok := c.Lookup(name); ok {
		v, ok := m.globals[qualified]
		return v, ok
	}
	return value.NilValue, false
}

// BuiltinDenied reports whether the program may not use built-in name.
func (c *Config) BuiltinDenied(name string) bool {
	return c != nil && c.DeniedBuiltins[name]
}

// Disallowed reports whether name is a registered module the program may
// not use (for a clearer message than "not found").
func (c *Config) Disallowed(name string) bool {
	if c == nil || c.AllowedModules == nil || c.AllowedModules[name] {
		return false
	}
	_, ok := Lookup(name)
	return ok
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

// TypeError is the error for argument i of the wrong type: it names the
// kind of the argument, or for an object its class.
func (a Args) TypeError(i int, want string) error {
	got := a.V[i].Kind().String()
	if a.V[i].Kind() == value.Object {
		got = a.V[i].AsObject().Class.Name
	}
	return value.Errorf(value.TypeError, "%s: argument %d must be %s, not %s", a.Name, i+1, want, got)
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

// Seconds returns argument i, a number of seconds that is not negative,
// as a duration; what names it in the message for another number ("a
// usable timeout"). See Duration for very long times.
func (a Args) Seconds(i int, what string) (time.Duration, error) {
	s, err := a.Number(i)
	if err != nil {
		return 0, err
	}
	if s != s || s < 0 {
		return 0, a.Errorf(value.ValueError, "%s seconds is not %s", a.V[i].String(), what)
	}
	return Duration(s), nil
}

// Duration converts seconds (not negative) to a duration. Beyond the
// longest duration, about 292 years, it gives the longest: as good as no
// limit.
func Duration(seconds float64) time.Duration {
	if ns := seconds * 1e9; ns < float64(math.MaxInt64) { // 2**63: one too many
		return time.Duration(ns)
	}
	return time.Duration(math.MaxInt64)
}

// Flush writes out what the program's standard output and standard error
// hold in a buffer (kukel buffers standard output; the writers of a Go
// program that embeds Kukel may not buffer). A function that waits, for
// time, a message or an event, calls it first: what the program wrote
// before the wait is seen during it.
func Flush(env value.Env) {
	for _, w := range []io.Writer{env.Stdout(), env.Stderr()} {
		if f, ok := w.(interface{ Flush() error }); ok {
			f.Flush()
		}
	}
}

// Object returns the Go value of argument i, which must be an object of
// class c or of a class derived from it.
func (a Args) Object(i int, c *Class) (any, error) {
	if v := a.V[i]; v.Kind() == value.Object && v.AsObject().Class.IsSubclassOf(c.obj) {
		if s := v.AsObject().State; s != nil {
			return s, nil
		}
		return nil, a.Errorf(value.ValueError, "argument %d: the %s part of this %s object is not set up: its constructor must call %s::%s(...)",
			i+1, c.QualifiedName(), v.AsObject().Class.Name, c.QualifiedName(), c.Name)
	}
	return nil, a.TypeError(i, "a "+c.QualifiedName())
}

// Is reports whether v is an object of class c, or of a class derived from
// it, with its Go value set up.
func (c *Class) Is(v value.Value) bool {
	return v.Kind() == value.Object && v.AsObject().Class.IsSubclassOf(c.obj) && v.AsObject().State != nil
}

// Errorf returns a Kukel exception of the given kind (value.ValueError,
// value.TypeError, ...), prefixed with the function's name.
func (a Args) Errorf(kind string, format string, args ...any) error {
	return value.Errorf(kind, "%s: %s", a.Name, fmt.Sprintf(format, args...))
}

func keysOf[V any](m map[string]V) map[string]bool {
	keys := map[string]bool{}
	for k := range m {
		keys[k] = true
	}
	return keys
}
