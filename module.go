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
	m.m.Funcs[name] = func(env value.Env, args []value.Value) (r value.Value, err error) {
		in := interpOf(env)
		vs := make([]Value, len(args))
		for i, a := range args {
			vs[i] = Value{a}
		}
		defer func() {
			if p := recover(); p != nil { // a panic is the Func's failure, not the interpreter's
				r, err = value.NilValue, value.Errorf("HostError", "%s panicked: %v", full, p)
			}
		}()
		v, err := fn(in, vs)
		if err != nil {
			return value.NilValue, toKukelError(err)
		}
		return v.v, nil
	}
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
