package debugger

import (
	"errors"
	"fmt"
	"strings"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/diag"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/token"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Eval evaluates an expression as the function of frame i sees its names:
// literals, names (Lookup), the operators, ?:, indexing and members, vector
// and map literals. Nothing it does changes the program: calls,
// assignments, ++, new and function literals are refused.
func Eval(m *vm.VM, i int, src string) (value.Value, error) {
	x, err := parse(src)
	if err != nil {
		return value.NilValue, err
	}
	return (&evaluator{m, i}).eval(x)
}

// Check reports what keeps Eval from evaluating src anywhere (it does not
// parse, or it has a call, an assignment, ...), or nil: for the condition
// of a breakpoint, when it is set.
func Check(src string) error {
	x, err := parse(src)
	if err != nil {
		return err
	}
	return check(x)
}

func parse(src string) (ast.Expr, error) {
	if strings.TrimSpace(src) == "" {
		return nil, errors.New("print what? (an expression, such as x, this->name or v[i] + 1)")
	}
	x, err := parser.ParseExpr(src)
	if err != nil {
		var de *diag.Error
		if errors.As(err, &de) {
			return nil, errors.New(de.Msg) // a position in one line says nothing
		}
		return nil, err
	}
	return x, nil
}

// check walks x as eval does, without values: it gives eval's error for
// what eval refuses whatever the values are.
func check(x ast.Expr) error {
	switch x := x.(type) {
	case *ast.IntLit, *ast.CharLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.NilLit,
		*ast.Ident, *ast.ThisExpr, *ast.StaticRef:
		return nil
	case *ast.UnaryExpr:
		switch x.Op {
		case token.MINUS, token.TILDE, token.BANG:
			return check(x.X)
		}
	case *ast.BinaryExpr:
		return checkAll(x.X, x.Y)
	case *ast.CondExpr:
		return checkAll(x.Cond, x.Then, x.Else)
	case *ast.IndexExpr:
		return checkAll(x.X, x.Index)
	case *ast.MemberExpr:
		return check(x.X)
	case *ast.VectorLit:
		for _, el := range x.Elems {
			if _, spread := el.(*ast.Spread); spread {
				return errSpread
			}
		}
		return checkAll(x.Elems...)
	case *ast.MapLit:
		for _, en := range x.Entries {
			if err := checkAll(en.Key, en.Value); err != nil {
				return err
			}
		}
		return nil
	case *ast.CallExpr, *ast.MethodCall:
		return errCall
	}
	return errSideEffects
}

func checkAll(xs ...ast.Expr) error {
	for _, x := range xs {
		if err := check(x); err != nil {
			return err
		}
	}
	return nil
}

var (
	errCall        = errors.New("calls are not evaluated: they could change the program")
	errSpread      = errors.New("a vector literal with .. is not evaluated")
	errSideEffects = errors.New("only expressions without side effects are evaluated (names, operators, ?:, [i], ->member)")
)

type evaluator struct {
	m     *vm.VM
	frame int
}

func (e *evaluator) eval(x ast.Expr) (value.Value, error) {
	switch x := x.(type) {
	case *ast.IntLit:
		return value.MakeInt(x.Value), nil
	case *ast.CharLit:
		return value.MakeInt(x.Value), nil
	case *ast.FloatLit:
		return value.MakeFloat(x.Value), nil
	case *ast.StringLit:
		return value.MakeString(x.Value), nil
	case *ast.BoolLit:
		return value.MakeBool(x.Value), nil
	case *ast.NilLit:
		return value.NilValue, nil
	case *ast.Ident:
		return e.name(x.Name)
	case *ast.ThisExpr:
		return e.name("this")
	case *ast.StaticRef:
		name := x.Class.Name + "::" + x.Name.Name
		if x.Module != nil {
			name = x.Module.Name + "::" + name
		}
		return e.name(name)
	case *ast.UnaryExpr:
		v, err := e.eval(x.X)
		if err != nil {
			return value.NilValue, err
		}
		switch x.Op {
		case token.MINUS:
			return value.Neg(v)
		case token.TILDE:
			return value.BitNot(v)
		case token.BANG:
			return value.MakeBool(!v.Truthy()), nil
		}
	case *ast.BinaryExpr:
		a, err := e.eval(x.X)
		if err != nil {
			return value.NilValue, err
		}
		switch x.Op {
		case token.ANDAND:
			if !a.Truthy() {
				return value.False, nil
			}
			b, err := e.eval(x.Y)
			return value.MakeBool(b.Truthy()), err
		case token.OROR:
			if a.Truthy() {
				return value.True, nil
			}
			b, err := e.eval(x.Y)
			return value.MakeBool(b.Truthy()), err
		}
		b, err := e.eval(x.Y)
		if err != nil {
			return value.NilValue, err
		}
		return compiler.EvalBinary(x.Op, a, b)
	case *ast.CondExpr:
		c, err := e.eval(x.Cond)
		if err != nil {
			return value.NilValue, err
		}
		if c.Truthy() {
			return e.eval(x.Then)
		}
		return e.eval(x.Else)
	case *ast.IndexExpr:
		v, err := e.eval(x.X)
		if err != nil {
			return value.NilValue, err
		}
		k, err := e.eval(x.Index)
		if err != nil {
			return value.NilValue, err
		}
		return value.Index(v, k)
	case *ast.MemberExpr:
		v, err := e.eval(x.X)
		if err != nil {
			return value.NilValue, err
		}
		return value.Member(v, x.Name.Name, value.MakeFunction)
	case *ast.VectorLit:
		elems := make([]value.Value, len(x.Elems))
		for j, el := range x.Elems {
			if _, spread := el.(*ast.Spread); spread {
				return value.NilValue, errSpread
			}
			v, err := e.eval(el)
			if err != nil {
				return value.NilValue, err
			}
			elems[j] = v
		}
		return value.MakeVector(elems), nil
	case *ast.MapLit:
		m := value.NewMap()
		for _, en := range x.Entries {
			k, err := e.eval(en.Key)
			if err != nil {
				return value.NilValue, err
			}
			v, err := e.eval(en.Value)
			if err != nil {
				return value.NilValue, err
			}
			if err := m.Set(k, v); err != nil {
				return value.NilValue, err
			}
		}
		return value.MakeMap(m), nil
	case *ast.CallExpr, *ast.MethodCall:
		return value.NilValue, errCall
	}
	return value.NilValue, errSideEffects
}

func (e *evaluator) name(name string) (value.Value, error) {
	if v, ok := Lookup(e.m, e.frame, name); ok {
		return v, nil
	}
	return value.NilValue, fmt.Errorf("no variable %s here", name)
}
