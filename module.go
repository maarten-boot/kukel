package kukel

import (
	"errors"
	"fmt"
	"sync"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/token"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Func is a Go function that Kukel code calls. in is the interpreter that
// calls it (to call back into Kukel with in.CallValue). The arguments are
// valid during the call only; keep copies (ToGo) of what is needed later.
// An error becomes a Kukel exception: an *Error keeps its kind (see
// Errorf), any other error has the kind "HostError".
type Func func(in *Interp, args []Value) (Value, error)

// Module is a module written in Go for one program: Kukel code that says
// "use name;" reaches its functions and constants as name::f and name::K.
// Add everything before the module is first used in CompileOptions.
type Module struct {
	m    native.Module
	once sync.Once
	used bool
}

// NewModule makes an empty module. name must be a Kukel identifier that is
// not a keyword. NewModule, Func and Const panic on such programming
// mistakes: a bad name, a nil function, a name used twice, a constant that
// is not nil, a boolean, a number or a string, or an addition after the
// module is in use.
func NewModule(name string) *Module {
	if !isIdent(name) {
		panic("kukel: module name " + name + " is not an identifier")
	}
	return &Module{m: native.Module{Name: name, Funcs: map[string]native.Func{}, Consts: map[string]value.Value{}}}
}

// Func adds a function; it gives m, so that calls can be chained.
func (m *Module) Func(name string, fn Func) *Module {
	if fn == nil {
		panic("kukel: function " + m.m.Name + "::" + name + " is nil")
	}
	m.add(name)
	full := m.m.Name + "::" + name
	m.m.Funcs[name] = goFunc(func() string { return full }, fn)
	return m
}

// goFunc makes the function the VM calls for fn (full gives its name for
// messages): a panic is fn's failure, not the interpreter's, and errors
// become exceptions.
func goFunc(full func() string, fn Func) native.Func {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		return callGo(full, env, args, fn)
	}
}

// callGo calls fn for the VM.
func callGo(full func() string, env value.Env, args []value.Value, fn Func) (r value.Value, err error) {
	in := interpOf(env)
	vs := make([]Value, len(args))
	for i, a := range args {
		vs[i] = Value{a}
	}
	defer func() {
		if p := recover(); p != nil {
			r, err = value.NilValue, value.Errorf("HostError", "%s panicked: %v", full(), p)
		}
	}()
	v, err := fn(in, vs)
	if err != nil {
		return value.NilValue, toKukelError(err)
	}
	return v.v, nil
}

// Class adds a class written in Go (see NewClass); Kukel code reaches it
// as name::C: new name::C(...), class D : name::C, name::C::f(...).
func (m *Module) Class(c *Class) *Module {
	if c.mod != nil {
		panic("kukel: class " + c.c.Name + " is already in module " + c.mod.m.Name)
	}
	m.add(c.c.Name)
	c.mod = m
	m.m.Classes = append(m.m.Classes, &c.c)
	return m
}

// Const adds a constant: nil, a boolean, a number or a string. Kukel code
// can use it in constant expressions.
func (m *Module) Const(name string, v Value) *Module {
	switch v.v.Kind() {
	case value.Nil, value.Bool, value.Int, value.Float, value.String:
	default:
		panic("kukel: constant " + name + " must be nil, a boolean, a number or a string")
	}
	m.add(name)
	m.m.Consts[name] = v.v
	return m
}

func (m *Module) add(name string) {
	if m.used {
		panic("kukel: module " + m.m.Name + " is already in use; add " + name + " before compiling")
	}
	if !isIdent(name) {
		panic("kukel: " + name + " is not an identifier")
	}
	if _, ok := m.m.Funcs[name]; ok {
		panic("kukel: " + m.m.Name + "::" + name + " is defined twice")
	}
	if _, ok := m.m.Consts[name]; ok {
		panic("kukel: " + m.m.Name + "::" + name + " is defined twice")
	}
	for _, c := range m.m.Classes {
		if c.Name == name {
			panic("kukel: " + m.m.Name + "::" + name + " is defined twice")
		}
	}
}

// Method is a member function written in Go: this is the object it is
// called on (also an object of a Kukel class derived from the Go class).
// For the constructor (Class.New), it sets the object's Go value with
// this.SetState; its result is not used.
type Method func(in *Interp, this *Object, args []Value) (Value, error)

// Class is a class written in Go for a host module. Its objects carry a
// Go value (their state) and have no data members: everything goes through
// member functions. Kukel code makes objects with new name::C(...) when the
// class has a constructor, and may then derive classes from it; the derived
// constructor calls name::C::C(...) to set up the Go value. Without a
// constructor, objects come from the module's functions (Class.Object).
//
// NewClass, New, Method, Static and Show panic on programming mistakes: a
// bad name, a nil function, a name used twice, or an addition after the
// module is in use.
type Class struct {
	c   native.Class
	mod *Module
}

// NewClass makes an empty class. name must be an identifier.
func NewClass(name string) *Class {
	if !isIdent(name) {
		panic("kukel: class name " + name + " is not an identifier")
	}
	return &Class{c: native.Class{Name: name, Methods: map[string]value.NativeMethod{}, Statics: map[string]native.Func{}}}
}

func (c *Class) full(name string) string {
	mod := "?"
	if c.mod != nil {
		mod = c.mod.m.Name
	}
	return mod + "::" + c.c.Name + "::" + name
}

func (c *Class) addMember(name string) {
	if c.mod != nil && c.mod.used {
		panic("kukel: module " + c.mod.m.Name + " is already in use; add " + name + " before compiling")
	}
	if !isIdent(name) {
		panic("kukel: " + name + " is not an identifier")
	}
	_, m := c.c.Methods[name]
	_, s := c.c.Statics[name]
	if m || s || name == c.c.Name && c.c.New != nil {
		panic("kukel: " + c.c.Name + "::" + name + " is defined twice")
	}
}

// New sets the constructor: new name::C(args) calls fn with the new
// object, whose Go value fn sets (this.SetState).
func (c *Class) New(fn Method) *Class {
	if fn == nil {
		panic("kukel: the constructor of class " + c.c.Name + " is nil")
	}
	c.addMember(c.c.Name)
	c.c.New = c.method(c.c.Name, fn)
	return c
}

// Method adds an instance member function.
func (c *Class) Method(name string, fn Method) *Class {
	if fn == nil {
		panic("kukel: member function " + name + " of class " + c.c.Name + " is nil")
	}
	c.addMember(name)
	if name == c.c.Name {
		panic("kukel: " + name + " is the constructor's name; use New")
	}
	c.c.Methods[name] = c.method(name, fn)
	return c
}

// Static adds a static member function (name::C::f(...)).
func (c *Class) Static(name string, fn Func) *Class {
	if fn == nil {
		panic("kukel: member function " + name + " of class " + c.c.Name + " is nil")
	}
	c.addMember(name)
	if name == c.c.Name {
		panic("kukel: " + name + " is the constructor's name; use New")
	}
	c.c.Statics[name] = goFunc(func() string { return c.full(name) }, fn)
	return c
}

// Show sets how str() and println show an object of the class (without
// it: "name::C{}").
func (c *Class) Show(fn func(this *Object) string) *Class {
	if c.mod != nil && c.mod.used {
		panic("kukel: module " + c.mod.m.Name + " is already in use")
	}
	c.c.Show = func(o *value.ObjectObj) string { return fn(&Object{o}) }
	return c
}

// method makes the native member function for fn.
func (c *Class) method(name string, fn Method) value.NativeMethod {
	full := func() string { return c.full(name) }
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		return callGo(full, env, args, func(in *Interp, args []Value) (Value, error) {
			return fn(in, &Object{this}, args)
		})
	}
}

// Object makes an object of the class holding state, for a Func to return.
// The class must be in a module that a compiled program uses.
func (c *Class) Object(state any) Value {
	if c.c.ClassObj() == nil {
		panic("kukel: class " + c.c.Name + " is not in use yet (add it to a module and compile a program with it)")
	}
	return Value{c.c.Object(state)}
}

// StateOf gives the Go value of v, an object of the class or of a Kukel
// class derived from it whose constructor has set it up.
func (c *Class) StateOf(v Value) (any, bool) {
	if c.c.ClassObj() == nil || !c.c.Is(v.v) {
		return nil, false
	}
	return v.v.AsObject().State, true
}

// Object is an object, as a Method gets it (this) or Value.Object gives it.
type Object struct{ o *value.ObjectObj }

// State gives the object's Go value (nil before the constructor sets it).
func (o *Object) State() any { return o.o.State }

// SetState sets the object's Go value.
func (o *Object) SetState(state any) { o.o.State = state }

// ClassName gives the name of the object's class ("app::Counter", or the
// Kukel class derived from it).
func (o *Object) ClassName() string { return o.o.Class.Name }

// Value gives the object as a Value.
func (o *Object) Value() Value { return Value{value.ObjectValue(o.o)} }

// Call calls the member function name on the object, as p->name(args)
// does: a Kukel class derived from a Go class may override it.
func (o *Object) Call(in *Interp, name string, args ...Value) (Value, error) {
	m, err := value.Member(value.ObjectValue(o.o), name, value.MakeFunction)
	if err != nil {
		var e *value.Error
		if errors.As(err, &e) {
			return Nil, &Error{Kind: e.Kind, Message: e.Msg}
		}
		return Nil, err
	}
	return in.CallValue(Value{m}, args...)
}

// prepare readies the module once; it cannot change afterwards.
func (m *Module) prepare() *native.Module {
	m.once.Do(func() {
		m.used = true
		native.Prepare(&m.m)
	})
	return &m.m
}

func isIdent(s string) bool {
	if s == "" || token.Lookup(s) != token.IDENT {
		return false
	}
	for i, c := range s {
		letter := c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// Error is an exception of a Kukel program that nothing caught, or the
// end of a call by its step limit (Kind "StepLimitError") or its context
// (Kind "CancelledError").
type Error struct {
	Kind    string  // "TypeError", "ValueError", ...
	Message string  // the Error object's message
	Value   Value   // the thrown Error object (Nil for the step limit and cancellation)
	Trace   []Frame // the calls, innermost first
	err     error   // the VM's error, to throw again unchanged
}

// Frame is one call in a trace.
type Frame struct {
	Func string
	File string
	Line int
}

// Error gives the kind and message, then the trace if there is one (one
// "  at f (file:line)" line per call).
func (e *Error) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	if e.Kind == "" {
		return e.Message
	}
	return e.Kind + ": " + e.Message
}

// Errorf makes an error for a Func to return: Kukel code sees an exception
// (an Error object) with that kind (or "HostError" if kind is "") and
// message.
func Errorf(kind, format string, args ...any) *Error {
	if kind == "" {
		kind = "HostError" // so Error() and Kukel code see the same kind
	}
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// ExitError is what Call, CallContext, CallValue and Global give when the
// program calls exit(code) (Run gives the code instead). A Func may return
// one to end the program as exit(code) does.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("the program called exit(%d)", e.Code) }

// fromVM turns an error of the VM into the package's errors.
func fromVM(err error) error {
	var re *vm.RuntimeError
	if !errors.As(err, &re) {
		return err
	}
	if re.Exit != nil {
		return &ExitError{Code: *re.Exit}
	}
	e := &Error{Kind: re.Kind, Message: re.Msg, Value: Value{re.Value}, err: re}
	for _, t := range re.Trace {
		e.Trace = append(e.Trace, Frame{Func: t.Func, File: t.File, Line: t.Line})
	}
	return e
}

// toKukelError turns an error of a Func into one the VM throws.
func toKukelError(err error) error {
	var e *Error
	if errors.As(err, &e) {
		if e.err != nil {
			return e.err // an exception that came through: throw it on unchanged
		}
		kind := e.Kind
		if kind == "" {
			kind = "HostError"
		}
		return value.Errorf(kind, "%s", e.Message)
	}
	var x *ExitError
	if errors.As(err, &x) {
		return &value.ExitError{Code: x.Code}
	}
	return value.Errorf("HostError", "%s", err.Error())
}
