package compiler

import (
	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/token"
)

// paramShape is what a call needs to know about a function's parameters.
type paramShape struct {
	name     string // as in run-time messages: "f", "C::m"
	required int
	optional int
	rest     bool
}

func shapeOf(name string, pl *ast.ParamList) *paramShape {
	ps := &paramShape{name: name}
	for _, p := range pl.Params {
		switch {
		case p.Rest:
			ps.rest = true
		case p.Default != nil:
			ps.optional++
		default:
			ps.required++
		}
	}
	return ps
}

// checkArgs reports a wrong number of arguments for a callee known at
// compile time (Q32): a function, a static member function, Base::m(...)
// or a constructor. These cannot be reassigned, so the check is exact. A
// call with spread arguments is checked at run time.
func (c *compiler) checkArgs(ps *paramShape, args []ast.Expr, pos token.Pos) {
	if ps == nil || hasSpread(args) {
		return
	}
	n := len(args)
	switch {
	case ps.rest && n < ps.required:
		c.errorf(pos, "%s expects at least %d argument(s), got %d", ps.name, ps.required, n)
	case ps.rest:
	case ps.optional > 0 && (n < ps.required || n > ps.required+ps.optional):
		c.errorf(pos, "%s expects %d to %d arguments, got %d", ps.name, ps.required, ps.required+ps.optional, n)
	case ps.optional == 0 && n != ps.required:
		c.errorf(pos, "%s expects %d argument(s), got %d", ps.name, ps.required, n)
	}
}

// staticCallee returns the parameters of the callee of an ordinary call
// when it is known at compile time: a top-level function, or a static
// member function named C::f. Otherwise nil.
func (c *compiler) staticCallee(fun ast.Expr) *paramShape {
	switch fun := fun.(type) {
	case *ast.Ident:
		if _, ok := c.lookupLocal(fun.Name); ok {
			return nil
		}
		if _, ok := c.upvalue(c.fs, fun.Name); ok {
			return nil
		}
		if c.memberOf(fun.Name) != nil {
			return nil
		}
		if g, ok := c.globals[fun.Name]; ok && g.kind == symFunc {
			return g.params
		}
	case *ast.StaticRef:
		if g, ok := c.globals[fun.Class.Name]; ok && g.kind == symClass {
			if m := g.class.lookup(fun.Name.Name); m != nil && m.kind == memberMethod && m.static {
				return m.def
			}
		}
	}
	return nil
}

// checkNew checks new C(...) when C names a class directly (not a variable
// holding one): the parameters of the constructor that runs (C's own, else
// the nearest base class's, Q33), or no arguments at all when there is none.
func (c *compiler) checkNew(e *ast.NewExpr) {
	if _, ok := c.lookupLocal(e.Class.Name); ok {
		return
	}
	if _, ok := c.upvalue(c.fs, e.Class.Name); ok {
		return
	}
	g, ok := c.globals[e.Class.Name]
	if !ok || g.kind != symClass {
		return
	}
	k := g.class
	ctor := k.constructor()
	switch {
	case ctor == nil:
		if len(e.Args) > 0 && !hasSpread(e.Args) {
			c.errorf(e.New, "class %s has no constructor, so new %s() takes no arguments (got %d)", k.name, k.name, len(e.Args))
		}
	case ctor.fn < 0:
		c.errorf(e.New, "%s is declared but not defined", ctor.fullName())
	default:
		c.checkArgs(ctor.def, e.Args, e.New)
	}
}
