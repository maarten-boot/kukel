package compiler

import (
	"fmt"
	"strings"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/token"
	"github.com/maarten-boot/kukel/internal/value"
)

// classInfo is what the compiler knows about a class.
type classInfo struct {
	native  bool    // a class of a native module: no bytecode class, index -1
	global  *global // a native class: its global, linked by name
	name    string  // module::Class for a class of a module
	local   string  // the name in the class's module (and its constructor's name)
	pos     token.Pos
	index   int // in Program.Classes
	base    *classInfo
	nfields int // instance data members, including inherited ones
	members map[string]*memberInfo
	bc      *bytecode.Class
}

type memberKind uint8

const (
	memberField  memberKind = iota // an instance data member
	memberStatic                   // a static data member
	memberMethod                   // a (static) member function
)

// memberInfo is a member declared in a class.
type memberInfo struct {
	name   string
	pos    token.Pos
	kind   memberKind
	class  *classInfo  // the declaring class
	field  int         // memberField: slot in the object
	slot   int         // memberStatic: index in the class's statics
	static bool        // memberMethod
	params int         // memberMethod: declared parameters, without the rest parameter
	rest   bool        // memberMethod: has a rest parameter
	method int         // memberMethod: index in bytecode.Class.Methods
	fn     int         // memberMethod: index in Program.Funcs, -1 until defined
	defPos token.Pos   // memberMethod: position of the definition
	def    *paramShape // memberMethod: the definition's parameters
	native *global     // a static member function of a native class
}

// lookup finds a member in k or its base classes.
func (k *classInfo) lookup(name string) *memberInfo {
	for ; k != nil; k = k.base {
		if m, ok := k.members[name]; ok {
			return m
		}
	}
	return nil
}

// isSubclassOf reports whether k is b or derives from b.
func (k *classInfo) isSubclassOf(b *classInfo) bool {
	for ; k != nil; k = k.base {
		if k == b {
			return true
		}
	}
	return false
}

// constructor returns the constructor new runs for k: k's own, else the
// nearest base class's (Q33), or nil.
func (k *classInfo) constructor() *memberInfo {
	for ; k != nil; k = k.base {
		if m, ok := k.members[k.local]; ok && m.kind == memberMethod {
			return m
		}
	}
	return nil
}

// isCtor reports whether m is the constructor of its class.
func (m *memberInfo) isCtor() bool { return m.kind == memberMethod && m.name == m.class.local }

func (m *memberInfo) fullName() string { return m.class.name + "::" + m.name }

// ----------------------------------------------------------------------
// Declarations and definitions

func (c *compiler) declareClass(d *ast.ClassDecl) {
	if len(c.prog.Classes) > maxIndex {
		c.errorf(d.Name.NamePos, "too many classes in the program (more than %d)", maxIndex+1)
	}
	k := &classInfo{
		name:    c.qualify(d.Name.Name),
		local:   d.Name.Name,
		pos:     d.Name.NamePos,
		index:   len(c.prog.Classes),
		members: map[string]*memberInfo{},
		bc:      &bytecode.Class{Name: c.qualify(d.Name.Name), Base: -1},
	}
	if d.Base != nil {
		var g *global
		ok := true
		if d.BaseModule != nil {
			g = c.moduleMember(d.BaseModule, d.Base)
		} else {
			g, ok = c.lookup(d.Base.Name)
		}
		switch {
		case !ok:
			c.errorf(d.Base.NamePos, "base class %s is not defined (a base class must be defined before the classes derived from it)", d.Base.Name)
		case g.kind != symClass:
			c.errorf(d.Base.NamePos, "%s is not a class", d.Base.Name)
		}
		if g.class.isGenerator() {
			c.errorf(d.Base.NamePos, "class %s cannot derive from Generator: generators come from calling generator functions", d.Name.Name)
		}
		if g.class.native && g.class.constructor() == nil {
			c.errorf(d.Base.NamePos, "class %s cannot derive from native class %s: it has no constructor (its objects come from the functions of its module)", d.Name.Name, g.class.name)
		}
		k.base = g.class
		k.bc.Base = g.class.index
		k.nfields = g.class.nfields
		if g.class.native { // linked by name; its Go part has no data members
			k.bc.Native = g.class.name
		}
	}
	g := c.define(d.Name, symClass)
	g.class = k
	c.prog.Globals[g.index].Class = k.index
	c.prog.Classes = append(c.prog.Classes, k.bc)

	for _, mem := range d.Members {
		switch mem := mem.(type) {
		case *ast.FieldDecl:
			for _, id := range mem.Names {
				m := &memberInfo{name: id.Name, pos: id.NamePos, class: k}
				if mem.Static {
					m.kind, m.slot = memberStatic, len(k.bc.Statics)
					k.bc.Statics = append(k.bc.Statics, id.Name)
				} else {
					m.kind, m.field = memberField, k.nfields
					k.nfields++
					k.bc.Fields = append(k.bc.Fields, id.Name)
				}
				if k.nfields > maxIndex || len(k.bc.Statics) > maxIndex {
					c.errorf(id.NamePos, "too many data members in class %s", k.name)
				}
				c.addMember(k, m)
			}
		case *ast.MethodDecl:
			m := &memberInfo{name: mem.Name.Name, pos: mem.Name.NamePos, kind: memberMethod, class: k,
				static: mem.Static, params: len(mem.Params), rest: mem.Rest, method: len(k.bc.Methods), fn: -1}
			if m.rest {
				m.params--
			}
			if m.isCtor() && m.static {
				c.errorf(mem.Pos(), "the constructor %s::%s cannot be static", k.name, k.local)
			}
			if len(k.bc.Methods) > maxIndex {
				c.errorf(mem.Pos(), "too many member functions in class %s (more than %d)", k.name, maxIndex+1)
			}
			k.bc.Methods = append(k.bc.Methods, bytecode.Method{Name: m.name, Static: m.static, Func: -1})
			c.addMember(k, m)
		}
	}
}

// addMember adds a member to k. A class may redeclare a member function of
// a base class (overriding it) with the same static-ness; any other reuse of
// a name is an error.
func (c *compiler) addMember(k *classInfo, m *memberInfo) {
	if prev, ok := k.members[m.name]; ok {
		c.errorf(m.pos, "member %s is already declared in class %s at %s", m.name, k.name, prev.pos)
	}
	if k.base != nil {
		if inh := k.base.lookup(m.name); inh != nil &&
			!(inh.kind == memberMethod && m.kind == memberMethod && inh.static == m.static) {
			if inh.class.native {
				c.errorf(m.pos, "member %s conflicts with member function %s of native base class %s", m.name, m.name, inh.class.name)
			}
			c.errorf(m.pos, "member %s conflicts with %s declared in base class %s at %s", m.name, m.name, inh.class.name, inh.pos)
		}
	}
	k.members[m.name] = m
}

// methodDefinition checks "C::m(params) { ... }" against the declaration.
func (c *compiler) methodDefinition(d *ast.FuncDecl) (*classInfo, *memberInfo) {
	k := c.classNamed(nil, d.Class)
	if k.isGenerator() {
		c.errorf(d.Name.NamePos, "Generator::%s cannot be defined: the member functions of Generator are built in", d.Name.Name)
	}
	m, ok := k.members[d.Name.Name]
	if !ok {
		hint := ""
		if inh := k.lookup(d.Name.Name); inh != nil {
			hint = fmt.Sprintf(" (it is declared in base class %s; declare it in %s to override it)", inh.class.name, k.name)
		}
		c.errorf(d.Name.NamePos, "class %s does not declare a member function %s%s", k.name, d.Name.Name, hint)
	}
	if m.kind != memberMethod {
		c.errorf(d.Name.NamePos, "%s is a data member, not a member function", m.fullName())
	}
	if m.fn >= 0 {
		c.errorf(d.Name.NamePos, "%s is already defined at %s", m.fullName(), m.defPos)
	}
	n, rest := 0, false
	for _, p := range d.Params.Params {
		if p.Rest {
			rest = true
		} else {
			n++
		}
	}
	if n != m.params || rest != m.rest {
		c.errorf(d.Name.NamePos, "the definition of %s has %s, but its declaration at %s has %s",
			m.fullName(), shape(n, rest), m.pos, shape(m.params, m.rest))
	}
	m.defPos = d.Name.NamePos
	m.def = shapeOf(m.fullName(), d.Params)
	return k, m
}

func stringValue(s string) value.Value { return value.MakeString(s) }

func shape(n int, rest bool) string {
	s := fmt.Sprintf("%d parameter", n)
	if n != 1 {
		s += "s"
	}
	if rest {
		s += " and a rest parameter"
	}
	return s
}

// ----------------------------------------------------------------------
// Names inside member functions

// memberOf finds an unqualified name among the members of the current
// function's class (constructors excluded: they are called with new, or as
// Base::Base(...)).
func (c *compiler) memberOf(name string) *memberInfo {
	if c.fs.class == nil {
		return nil
	}
	if m := c.fs.class.lookup(name); m != nil && !m.isCtor() {
		return m
	}
	return nil
}

// checkThis reports an error if the current function has no this.
func (c *compiler) checkThis(pos token.Pos, what string) {
	switch {
	case c.fs.class == nil:
		c.errorf(pos, "%s is only available in member functions", what)
	case c.fs.static:
		c.errorf(pos, "%s is not available in static member function %s (it has no 'this')", what, c.fs.fn.FuncName)
	}
}

func (c *compiler) loadThis(pos token.Pos) {
	c.checkThis(pos, "'this'")
	c.emit(bytecode.OpLoadThis)
}

// funcIndex returns the function of a member function that must be defined.
func (c *compiler) funcIndex(m *memberInfo, pos token.Pos) int {
	if m.fn < 0 {
		c.errorf(pos, "%s is declared but not defined", m.fullName())
	}
	return m.fn
}

// loadMember pushes the value of an unqualified member name.
func (c *compiler) loadMember(m *memberInfo, pos token.Pos) {
	switch {
	case m.kind == memberField:
		c.checkThis(pos, "data member "+m.name)
		c.emit(bytecode.OpLoadMember, m.field)
	case m.kind == memberStatic:
		c.emit(bytecode.OpLoadStatic, m.class.index, m.slot)
	case m.native != nil:
		c.emit(bytecode.OpLoadGlobal, c.globalIndex(m.native, pos))
	case m.static:
		c.emit(bytecode.OpLoadFunc, c.funcIndex(m, pos))
	default: // a bound method this->m
		c.checkThis(pos, "member function "+m.name)
		c.emit(bytecode.OpLoadThis)
		c.emit(bytecode.OpGetMember, c.constIndex(stringValue(m.name), pos), c.cache(pos))
	}
}

// memberVariable is an unqualified member name as an assignment target.
func (c *compiler) memberVariable(m *memberInfo, pos token.Pos) variable {
	switch m.kind {
	case memberField:
		c.checkThis(pos, "data member "+m.name)
		return variable{kind: lvThisField, index: m.field}
	case memberStatic:
		return variable{kind: lvStatic, class: m.class.index, index: m.slot}
	}
	c.errorf(pos, "cannot assign to member function %s", m.name)
	return variable{}
}

// thisField returns the data member for "this->x" when x is a data member
// of the current class (then a direct slot access is possible).
func (c *compiler) thisField(e *ast.MemberExpr) *memberInfo {
	if _, ok := e.X.(*ast.ThisExpr); !ok {
		return nil
	}
	c.checkThis(e.X.Pos(), "'this'")
	if m := c.fs.class.lookup(e.Name.Name); m != nil && m.kind == memberField {
		return m
	}
	return nil
}

// classNamed resolves a class name, [module::]Class (in C::x and Class::m(...)
// definitions).
func (c *compiler) classNamed(mod, id *ast.Ident) *classInfo {
	var g *global
	if mod != nil {
		g = c.moduleMember(mod, id)
	} else {
		var ok bool
		if g, ok = c.lookup(id.Name); !ok {
			if _, isMod := c.modules[id.Name]; isMod && id.Name != "" {
				c.errorf(id.NamePos, "%s is not a module used by this file (add 'use %s;' at the top)", id.Name, id.Name)
			}
			c.errorf(id.NamePos, "undefined class %s", id.Name)
		}
	}
	if g.kind != symClass {
		c.errorf(id.NamePos, "%s is not a class", id.Name)
	}
	return g.class
}

// findClass is classNamed without errors: the class, or nil.
func (c *compiler) findClass(mod, id *ast.Ident) *classInfo {
	var g *global
	if mod != nil {
		m := c.usedModule(mod)
		if m == nil || strings.HasPrefix(id.Name, "_") {
			return nil
		}
		g = m.names[id.Name]
	} else {
		g, _ = c.lookup(id.Name)
	}
	if g == nil || g.kind != symClass {
		return nil
	}
	return g.class
}

func (c *compiler) classMember(k *classInfo, id *ast.Ident) *memberInfo {
	m := k.lookup(id.Name)
	if m == nil {
		c.errorf(id.NamePos, "class %s has no member %s", k.name, id.Name)
	}
	return m
}

// loadStaticRef pushes the value of C::x: a static data member or a static
// member function.
func (c *compiler) loadStaticRef(e *ast.StaticRef) {
	if c.isModuleRef(e) {
		c.loadGlobal(c.moduleMember(e.Class, e.Name), e.Name)
		return
	}
	k := c.classNamed(e.Module, e.Class)
	m := c.classMember(k, e.Name)
	pos := e.Name.NamePos
	switch {
	case m.kind == memberStatic:
		c.emit(bytecode.OpLoadStatic, m.class.index, m.slot)
	case m.kind == memberField:
		c.errorf(pos, "%s::%s is an instance data member: use an object (p->%s)", k.name, m.name, m.name)
	case m.native != nil:
		c.emit(bytecode.OpLoadGlobal, c.globalIndex(m.native, pos))
	case m.static:
		c.emit(bytecode.OpLoadFunc, c.funcIndex(m, pos))
	default:
		c.notStatic(pos, k, m)
	}
}

// notStatic reports the use of instance member function m of class k as a
// plain function.
func (c *compiler) notStatic(pos token.Pos, k *classInfo, m *memberInfo) {
	if k.isGenerator() {
		c.errorf(pos, "Generator::%s is not static: call it on a generator (g->%s(...))", m.name, m.name)
	}
	c.errorf(pos, "%s::%s is not static: call it on an object (p->%s(...)), or call %s::%s(...) from a member function of %s or a class derived from it",
		k.name, m.name, m.name, k.name, m.name, k.name)
}

// isGenerator reports whether k is the prelude's class Generator.
func (k *classInfo) isGenerator() bool { return k.pos.File == PreludeName && k.name == "Generator" }

// newClassValue pushes the class named in "new X(...)": a local or global
// (members are not searched, so that the constructor's name in a member
// function still means the class).
func (c *compiler) newClassValue(mod, id *ast.Ident) {
	if mod != nil {
		g := c.moduleMember(mod, id)
		if g.kind != symClass && g.kind != symVar {
			c.errorf(id.NamePos, "%s::%s is not a class", mod.Name, id.Name)
		}
		c.emit(bytecode.OpLoadGlobal, c.globalIndex(g, id.NamePos))
		return
	}
	if _, ok := c.lookupLocal(id.Name); ok {
		c.load(id)
		return
	}
	if up, ok := c.upvalue(c.fs, id.Name); ok {
		c.emit(bytecode.OpLoadUpvalue, up)
		return
	}
	g, ok := c.lookup(id.Name)
	if !ok {
		c.errorf(id.NamePos, "undefined class %s", id.Name)
	}
	if g.kind != symClass && g.kind != symVar {
		c.errorf(id.NamePos, "%s is not a class", id.Name)
	}
	c.emit(bytecode.OpLoadGlobal, c.globalIndex(g, id.NamePos))
}

// callArgs compiles the arguments of a call and the call instruction:
// fixed(argc) without spread arguments, spread() with an argument vector.
func (c *compiler) callArgs(args []ast.Expr, pos token.Pos, fixed func(argc int), spread func()) {
	if hasSpread(args) {
		c.argVector(args)
		c.mark(pos)
		spread()
		return
	}
	for _, a := range args {
		c.expr(a)
	}
	c.mark(pos)
	fixed(len(args))
}

// memberCall compiles calls that involve members and reports whether it
// did: f(...) where f is a member function of the current class (Q8: this->f
// for an instance member function), and C::m(...).
func (c *compiler) memberCall(e *ast.CallExpr) bool {
	call := func(argc int) { c.emit(bytecode.OpCall, argc) }
	callV := func() { c.emit(bytecode.OpCallV) }
	switch fun := e.Fun.(type) {
	case *ast.Ident:
		if _, ok := c.lookupLocal(fun.Name); ok {
			return false
		}
		if _, ok := c.upvalue(c.fs, fun.Name); ok {
			return false
		}
		m := c.memberOf(fun.Name)
		if m == nil || m.kind != memberMethod {
			return false
		}
		if m.static {
			if m.native != nil { // a static of a native base class
				c.emit(bytecode.OpLoadGlobal, c.globalIndex(m.native, fun.NamePos))
			} else {
				c.emit(bytecode.OpLoadFunc, c.funcIndex(m, fun.NamePos))
			}
			c.checkArgs(m.def, e.Args, e.Lparen)
			c.callArgs(e.Args, e.Lparen, call, callV)
			return true
		}
		c.checkThis(fun.NamePos, "member function "+m.name)
		c.emit(bytecode.OpLoadThis)
		name := c.constIndex(stringValue(m.name), fun.NamePos)
		c.callArgs(e.Args, e.Lparen,
			func(argc int) { c.emit(bytecode.OpCallMethod, name, argc, c.cache(fun.NamePos)) },
			func() { c.emit(bytecode.OpCallMethodV, name, c.cache(fun.NamePos)) })
		return true
	case *ast.StaticRef:
		if c.isModuleRef(fun) {
			return false // a module's function: an ordinary call
		}
		k := c.classNamed(fun.Module, fun.Class)
		m := c.classMember(k, fun.Name)
		if m.kind != memberMethod || m.static {
			return false // a static function or data member: an ordinary call
		}
		// Base::m(...): a non-virtual call of an instance member function
		// with the current this (Q3).
		if c.fs.class == nil || c.fs.static || !c.fs.class.isSubclassOf(k) {
			c.notStatic(fun.Name.NamePos, k, m)
		}
		if hasSpread(e.Args) {
			c.errorf(e.Lparen, "spread arguments are not supported in a call of %s::%s(...)", k.name, m.name)
		}
		if m.class.native {
			// A member function written in Go, also a native constructor
			// (Base::Base(...)): found by name in the native class.
			g := c.globalIndex(m.class.global, fun.Name.NamePos)
			name := c.constIndex(stringValue(m.name), fun.Name.NamePos)
			c.emit(bytecode.OpLoadThis)
			for _, a := range e.Args {
				c.expr(a)
			}
			c.mark(e.Lparen)
			c.emit(bytecode.OpCallNative, g, name, len(e.Args))
			return true
		}
		fn := c.funcIndex(m, fun.Name.NamePos)
		c.checkArgs(m.def, e.Args, e.Lparen)
		c.emit(bytecode.OpLoadThis)
		for _, a := range e.Args {
			c.expr(a)
		}
		c.mark(e.Lparen)
		c.emit(bytecode.OpCallDirect, fn, len(e.Args))
		return true
	}
	return false
}
