package compiler

import (
	"strings"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/token"
	"github.com/maarten-boot/kukel/internal/value"
)

// Named arguments (syntax version 11, TODO.md Q107, docs/future.md §9):
// f(1, c: 3) gives an argument by the name of its parameter. They work in
// the calls whose function the compiler knows (a function, a static member
// function, Base::m(...), a constructor): the compiler turns them into
// positional arguments, with ABSENT for an optional parameter that is
// skipped, so that its default runs.

// hasNamed reports whether args holds a named argument.
func hasNamed(args []ast.Expr) bool {
	for _, a := range args {
		if _, ok := a.(*ast.NamedArg); ok {
			return true
		}
	}
	return false
}

// noNamed refuses the named arguments of a call whose parameters the
// compiler does not know; why says what the callee is.
func (c *compiler) noNamed(args []ast.Expr, why string) {
	for _, a := range args {
		if na, ok := a.(*ast.NamedArg); ok {
			c.errorf(na.Name.NamePos, "named argument %s: %s", na.Name.Name, why)
		}
	}
}

// Why named arguments are refused, by the kind of call.
const (
	knownCallees  = "named arguments need a function the compiler knows: a function, a static member function, Base::m(...) or a constructor"
	whyMethodCall = "p->m(...) calls a member function, which is chosen only when the call runs; " + knownCallees
)

// whyNotNamed says why the callee of an ordinary call f(...) takes no named
// arguments (staticCallee found no parameters for it).
func (c *compiler) whyNotNamed(fun ast.Expr) string {
	switch fun := fun.(type) {
	case *ast.Ident:
		if _, ok := c.lookupLocal(fun.Name); ok {
			return fun.Name + " is a variable; " + knownCallees
		}
		if _, ok := c.upvalue(c.fs, fun.Name); ok {
			return fun.Name + " is a variable; " + knownCallees
		}
		if g, ok := c.lookup(fun.Name); ok {
			return c.whyNotGlobal(fun.Name, g)
		}
	case *ast.StaticRef:
		if c.isModuleRef(fun) {
			return c.whyNotGlobal(fun.Class.Name+"::"+fun.Name.Name, c.moduleMember(fun.Class, fun.Name))
		}
		if k := c.findClass(fun.Module, fun.Class); k != nil {
			if m := k.lookup(fun.Name.Name); m != nil && m.kind == memberMethod && m.static {
				if m.native != nil || m.class.native {
					return m.fullName() + " is written in Go, and its parameters have no names in Kukel"
				}
				return m.fullName() + " is declared but not defined"
			}
		}
	}
	return knownCallees
}

// whyNotGlobal says why the top-level name g, called name, takes no named
// arguments.
func (c *compiler) whyNotGlobal(name string, g *global) string {
	switch {
	case g.kind == symBuiltin && g.lkind == bytecode.GlobalNative:
		return name + " is written in Go, and its parameters have no names in Kukel"
	case g.kind == symBuiltin:
		return name + " is a built-in function, whose parameters have no names in Kukel"
	case g.kind == symVar:
		return name + " is a variable; " + knownCallees
	}
	return knownCallees
}

// namedArgs compiles the arguments of a call with named arguments to a
// callee with the parameters ps, and returns the number of arguments it
// pushed: the positional ones, then for each parameter up to the last one
// given its argument, or ABSENT for an optional one that is skipped. The
// arguments are evaluated as they are written; when the named ones are not
// in the order of the parameters, into hidden slots first. call emits the
// call instruction, after NAMED_CALL when an argument is ABSENT.
func (c *compiler) namedArgs(ps *paramShape, args []ast.Expr, pos token.Pos, call func(argc int)) {
	if hasSpread(args) {
		c.errorf(pos, "a spread (x..) cannot be combined with named arguments: which parameters it fills is known only when the call runs")
	}
	npos := 0
	for npos < len(args) {
		if _, ok := args[npos].(*ast.NamedArg); ok {
			break
		}
		npos++
	}
	n := len(ps.names)
	if npos > n {
		if ps.restName != "" {
			c.errorf(pos, "%s: in a call with named arguments the rest parameter %s gets nothing, so at most %s can be given by place (got %d)", ps.name, ps.restName, value.Arguments(n), npos)
		}
		c.errorf(pos, "%s expects at most %s, got %d by place", ps.name, value.Arguments(n), npos)
	}
	given := make([]int, n) // the index in args of each parameter's argument, or -1
	for k := range given {
		given[k] = -1
		if k < npos {
			given[k] = k
		}
	}
	for j := npos; j < len(args); j++ {
		na := args[j].(*ast.NamedArg)
		k := ps.index(na.Name.Name)
		switch {
		case k < 0 && na.Name.Name == ps.restName:
			c.errorf(na.Name.NamePos, "%s is the rest parameter of %s; it cannot be named", na.Name.Name, ps.name)
		case k < 0 && n == 0:
			c.errorf(na.Name.NamePos, "%s has no parameter %s; it has no parameters", ps.name, na.Name.Name)
		case k < 0:
			c.errorf(na.Name.NamePos, "%s has no parameter %s; its parameters are %s", ps.name, na.Name.Name, andList(ps.names))
		case given[k] >= 0 && given[k] < npos:
			c.errorf(na.Name.NamePos, "%s is given twice: by place and by name", na.Name.Name)
		case given[k] >= 0:
			c.errorf(na.Name.NamePos, "%s is given twice", na.Name.Name)
		}
		given[k] = j
	}
	var missing []string
	for k := 0; k < ps.required; k++ {
		if given[k] < 0 {
			missing = append(missing, ps.names[k])
		}
	}
	switch len(missing) {
	case 0:
	case 1:
		c.errorf(pos, "%s: argument %s is missing", ps.name, missing[0])
	default:
		c.errorf(pos, "%s: arguments %s are missing", ps.name, andList(missing))
	}
	argc := n
	for argc > 0 && given[argc-1] < 0 {
		argc--
	}
	skips := false
	for k := npos; k < argc; k++ {
		skips = skips || given[k] < 0
	}
	finish := func() {
		c.mark(pos)
		if skips {
			c.emit(bytecode.OpNamedCall)
		}
		call(argc)
	}

	// The positional arguments come first in both the source and the
	// parameters. Then the named ones: in the order of the parameters,
	// they are pushed as they are evaluated.
	inOrder := true
	for k, last := npos, -1; k < argc; k++ {
		if j := given[k]; j >= 0 {
			if j < last {
				inOrder = false
			}
			last = j
		}
	}
	for _, a := range args[:npos] {
		c.expr(a)
	}
	if inOrder {
		for k := npos; k < argc; k++ {
			if j := given[k]; j >= 0 {
				c.expr(args[j].(*ast.NamedArg).X)
			} else {
				c.emit(bytecode.OpAbsent)
			}
		}
		finish()
		return
	}
	slots := make(map[int]int, len(args)-npos) // the hidden slot of args[j]
	for j := npos; j < len(args); j++ {
		slots[j] = c.tempSlot()
		c.expr(args[j].(*ast.NamedArg).X)
		c.emit(bytecode.OpStoreLocal, slots[j])
	}
	for k := npos; k < argc; k++ {
		if j := given[k]; j >= 0 {
			c.emit(bytecode.OpLoadLocal, slots[j])
		} else {
			c.emit(bytecode.OpAbsent)
		}
	}
	for range slots {
		c.freeTempSlot()
	}
	finish()
}

// index returns the position of the parameter called name, or -1 (also for
// the rest parameter).
func (ps *paramShape) index(name string) int {
	for k, p := range ps.names {
		if p == name {
			return k
		}
	}
	return -1
}

// andList writes names as "a", "a and b", "a, b and c".
func andList(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// namedEntry compiles the second entry of a function with optional
// parameters (Function.NamedEntry), after its body: the parameters' code
// again, with JUMP_IF_GIVEN for JUMP_IF_ARG, then a jump to bodyStart, the
// code after the first copy. A call starts there only when it passes
// ABSENT, so that calls without named arguments run as before, and the
// VM's dispatch loop has no code for ABSENT. Each default sees the
// parameters before it, as in the first copy; lvs are their variables.
func (c *compiler) namedEntry(params *ast.ParamList, lvs []localVar, bodyStart int) {
	fs := c.fs
	saved := fs.scopes
	fs.scopes = []map[string]localVar{{}}
	defer func() { fs.scopes = saved }()
	fs.fn.NamedEntry = c.here()
	for i, p := range params.Params {
		if p.Default != nil && !p.Rest {
			skip := c.emit(bytecode.OpJumpIfGiven, i, 0)
			c.mark(p.Pos())
			c.expr(p.Default)
			c.emit(bytecode.OpStoreLocal, i)
			c.patch([]int{skip + 2}, c.here())
		}
		if lvs[i].cell {
			c.emit(bytecode.OpLoadLocal, lvs[i].slot)
			c.emit(bytecode.OpNewCell, lvs[i].slot)
		}
		fs.scopes[0][p.Name.Name] = lvs[i]
	}
	c.emit(bytecode.OpJump, bodyStart)
}
