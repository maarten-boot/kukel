package compiler

import (
	"strings"

	"github.com/maarten-boot/kukel/internal/ast"
)

// fileScope sets up the scope of file f: the prelude (first) defines the
// universal names; any other file belongs to the program or to its module.
func (c *compiler) fileScope(f *ast.File, prelude bool) *fileScope {
	if prelude {
		return &fileScope{mod: &module{names: c.universal}}
	}
	m, ok := c.modules[f.Module]
	if !ok {
		m = &module{name: f.Module, names: make(map[string]*global)}
		c.modules[f.Module] = m
	}
	return &fileScope{mod: m, uses: map[string]*module{}}
}

// use makes module d.Name visible in the current file as name::x.
func (c *compiler) use(d *ast.UseDecl) {
	name := d.Name.Name
	if g, ok := c.universal[name]; ok {
		if g.kind == symBuiltin {
			c.errorf(d.Name.NamePos, "a module cannot have the name of built-in function %s", name)
		}
		c.errorf(d.Name.NamePos, "a module cannot have the name %s, defined at %s", name, g.pos)
	}
	m, ok := c.modules[name]
	switch {
	case name == c.file.mod.name:
		c.errorf(d.Name.NamePos, "module %s cannot use itself", name)
	case !ok:
		c.errorf(d.Name.NamePos, "module %s is not loaded", name)
	case c.file.uses[name] != nil:
		c.errorf(d.Name.NamePos, "module %s is already used in this file", name)
	}
	c.file.uses[name] = m
	c.file.useDecl = append(c.file.useDecl, d)
}

// checkUses reports a module used in a file whose own module also defines
// that name: name::x would be ambiguous.
func (c *compiler) checkUses(s *fileScope) {
	for _, d := range s.useDecl {
		if g, ok := s.mod.names[d.Name.Name]; ok {
			name := d.Name.Name
			c.errorf(g.pos, "%s has the name of a module used by a file of this %s", name, s.mod.what())
		}
	}
}

func (m *module) what() string {
	if m.name == "" {
		return "program"
	}
	return "module " + m.name
}

// qualify returns the name a top-level name of the current module has in
// the program: "name" in the program itself, "module::name" in a module.
func (c *compiler) qualify(name string) string {
	if c.file.mod.name == "" {
		return name
	}
	return c.file.mod.name + "::" + name
}

// lookup finds an unqualified top-level name: one of the current module,
// a built-in, or a prelude name.
func (c *compiler) lookup(name string) (*global, bool) {
	if g, ok := c.file.mod.names[name]; ok {
		return g, true
	}
	g, ok := c.universal[name]
	return g, ok
}

// usedModule returns the module named id that the current file uses, or nil.
func (c *compiler) usedModule(id *ast.Ident) *module {
	return c.file.uses[id.Name]
}

// moduleMember resolves module::name. Names starting with "_" are private
// to their module.
func (c *compiler) moduleMember(mod, name *ast.Ident) *global {
	m := c.usedModule(mod)
	if m == nil {
		c.errorf(mod.NamePos, "%s is not a module used by this file (add 'use %s;' at the top)", mod.Name, mod.Name)
	}
	g, ok := m.names[name.Name]
	if !ok {
		c.errorf(name.NamePos, "module %s has no %s", mod.Name, name.Name)
	}
	if strings.HasPrefix(name.Name, "_") {
		c.errorf(name.NamePos, "%s::%s is private to module %s (names starting with '_' are)", mod.Name, name.Name, mod.Name)
	}
	return g
}

// isModuleRef reports whether e is module::name rather than Class::name.
func (c *compiler) isModuleRef(e *ast.StaticRef) bool {
	return e.Module == nil && c.usedModule(e.Class) != nil
}
