package compiler

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/diag"
	"github.com/maarten-boot/kukel/internal/lexer"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/token"
)

// Modules compiled on their own (kukel build -module): a .kkc module holds
// the module's header, its declarations without function bodies and
// initializers, and its code. A program that uses it runs pass 1 on the
// header, as for a source module, so that the module's names, classes and
// functions get their indexes; pass 2 then links the code (link) instead
// of compiling function bodies, relocating the indexes by name.

// CompileModule compiles the last of files, a module (its File.Module is
// set) whose source is src, on its own; the files before it are the
// modules it uses.
func CompileModule(files []*ast.File, src []byte, builtins []string, config *native.Config) (mod *bytecode.Module, err error) {
	target := files[len(files)-1]
	header, err := moduleHeader(src, target)
	if err != nil {
		return nil, err
	}
	c := &compiler{
		config:    config,
		prog:      &bytecode.Program{Main: -1},
		universal: make(map[string]*global),
		modules:   map[string]*module{"": {names: make(map[string]*global)}},
	}
	defer func() {
		if r := recover(); r != nil {
			b, ok := r.(bailout)
			if !ok {
				panic(r)
			}
			mod, err = nil, b.err
		}
	}()
	for _, name := range builtins {
		c.universal[name] = &global{kind: symBuiltin, index: -1, link: name, lkind: bytecode.GlobalBuiltin}
	}
	files = append([]*ast.File{preludeFile()}, files...)
	jobs, globals := c.declarations(files, func(i int, f *ast.File) *fileScope { return c.fileScope(f, i == 0) })
	if len(c.errs) > 0 {
		return nil, diag.Join(c.errs)
	}
	var own []globalInit // the module's global variables
	for _, v := range globals {
		if v.spec != nil && v.file.mod.name == target.Module {
			own = append(own, v)
		}
	}
	pos := token.Pos{File: target.Name, Line: 1, Col: 1}
	c.functions(jobs, globals, pos) // links the modules it uses that were compiled on their own
	var init *bytecode.Function
	c.attempt(func() { init = c.initFunction(own, pos) })
	if len(c.errs) > 0 {
		return nil, diag.Join(c.errs)
	}
	init.FuncName = target.Module + "::<init>"
	c.prog.Funcs = append(c.prog.Funcs, init)

	// The module's own functions, numbered from 0; the functions of others
	// its code refers to come after them.
	mod = &bytecode.Module{Name: target.Module, Header: header}
	index := map[int]int{} // in the program -> in the module
	for i, f := range c.prog.Funcs {
		if f.File == target.Name {
			index[i] = len(mod.Code)
			mod.Code = append(mod.Code, f)
		}
	}
	mod.Init = len(mod.Code) - 1
	for _, g := range c.prog.Globals {
		mod.Globals = append(mod.Globals, bytecode.Global{Name: g.Name, Kind: g.Kind})
	}
	for _, k := range c.prog.Classes {
		mod.Classes = append(mod.Classes, k.Name)
		mod.Layouts = append(mod.Layouts, bytecode.Layout{Fields: c.fieldsOf(k), Statics: k.Statics})
	}
	same := func(i int) (int, error) { return i, nil }
	for i, f := range mod.Code {
		g := *f
		g.Code = slices.Clone(f.Code)
		err := bytecode.Relocate(&g, same, same, func(fn int) (int, error) {
			if j, ok := index[fn]; ok {
				return j, nil
			}
			name := c.prog.Funcs[fn].FuncName
			j := slices.Index(mod.Funcs, name)
			if j < 0 {
				j = len(mod.Funcs)
				mod.Funcs = append(mod.Funcs, name)
			}
			return len(mod.Code) + j, nil
		})
		if err != nil {
			return nil, err
		}
		mod.Code[i] = &g
	}
	return mod, nil
}

// fieldsOf gives the instance data members of class k, its base classes'
// first: in the order of their indexes.
func (c *compiler) fieldsOf(k *bytecode.Class) []string {
	if k.Base < 0 {
		return k.Fields
	}
	return append(slices.Clip(c.fieldsOf(c.prog.Classes[k.Base])), k.Fields...)
}

// moduleHeader gives the declarations of module source src (parsed as f)
// without what pass 1 does not need: the bodies of functions and the
// initializers of global variables are blanked out, keeping the lines.
func moduleHeader(src []byte, f *ast.File) (string, error) {
	if bytes.IndexByte(src, 0) >= 0 {
		return "", fmt.Errorf("%s: a module with a NUL byte cannot be compiled on its own", f.Name)
	}
	toks, err := lexer.Tokenize(f.Name, src)
	if err != nil {
		return "", err
	}
	at := map[int]int{} // token index by offset
	for i, t := range toks {
		at[t.Pos.Offset] = i
	}
	type span struct{ from, to int }
	var cut []span
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			cut = append(cut, span{d.Body.Lbrace.Offset + 1, d.Body.Rbrace.Offset})
		case *ast.GlobalDecl:
			for _, v := range d.Vars {
				if v.Value == nil {
					continue
				}
				i := at[v.Name.NamePos.Offset] + 1 // '='
				j, depth := i+1, 0
			scan:
				for ; toks[j].Kind != token.EOF; j++ {
					switch toks[j].Kind {
					case token.LPAREN, token.LBRACK, token.LBRACE:
						depth++
					case token.RPAREN, token.RBRACK, token.RBRACE:
						depth--
					case token.COMMA, token.SEMICOLON:
						if depth == 0 {
							break scan
						}
					}
				}
				cut = append(cut, span{toks[i].Pos.Offset, toks[j].Pos.Offset})
			}
		}
	}
	var b strings.Builder
	last := 0
	for _, s := range cut {
		b.Write(src[last:s.from])
		b.WriteString(strings.Repeat("\n", bytes.Count(src[s.from:s.to], []byte("\n"))))
		last = s.to
	}
	b.Write(src[last:])
	return b.String(), nil
}

// link puts the code of module s, compiled on its own, into the program:
// its function definitions into the slots of their jobs (from base on),
// its function literals and init function after the functions there are.
func (c *compiler) link(s *fileScope, jobs []funcJob, base int) {
	m := s.compiled
	pos := token.Pos{File: s.name, Line: 1, Col: 1}
	fail := func(format string, args ...any) {
		c.errorf(pos, "%s (compile module %s again)", fmt.Sprintf(format, args...), m.Name)
	}
	if m.Name != s.mod.name {
		c.errorf(pos, "%s is module %s, not %s", s.name, m.Name, s.mod.name)
	}
	slots := map[string]int{}
	for i, job := range jobs {
		if job.file == s {
			name, _ := c.jobName(job)
			slots[name] = base + i
		}
	}
	index := make([]int, len(m.Code))
	for i, f := range m.Code {
		if slot, ok := slots[f.FuncName]; ok {
			index[i] = slot
			delete(slots, f.FuncName)
			continue
		}
		c.checkFuncCount(len(c.prog.Funcs), pos)
		index[i] = len(c.prog.Funcs)
		c.prog.Funcs = append(c.prog.Funcs, nil)
	}
	for name := range slots {
		fail("it has no code for %s", name)
	}

	globals := map[string]int{}
	for i, g := range c.prog.Globals {
		globals[g.Name] = i
	}
	classes := map[string]int{}
	for i, k := range c.prog.Classes {
		classes[k.Name] = i
	}
	checked := map[int]bool{}
	class := func(i int) (int, error) {
		if i >= len(m.Classes) || i >= len(m.Layouts) {
			return 0, fmt.Errorf("bad class index %d", i)
		}
		k, ok := classes[m.Classes[i]]
		if !ok {
			return 0, fmt.Errorf("class %s is not defined", m.Classes[i])
		}
		if !checked[i] {
			checked[i] = true
			bc, want := c.prog.Classes[k], m.Layouts[i]
			if !slices.Equal(c.fieldsOf(bc), want.Fields) || !slices.Equal(bc.Statics, want.Statics) {
				return 0, fmt.Errorf("class %s has changed since the module was compiled", m.Classes[i])
			}
		}
		return k, nil
	}
	for i, name := range m.Classes { // the module's own classes: their data members have indexes in its code
		if strings.HasPrefix(name, m.Name+"::") {
			if _, err := class(i); err != nil {
				fail("%v", err)
			}
		}
	}
	global := func(i int) (int, error) {
		if i >= len(m.Globals) {
			return 0, fmt.Errorf("bad global index %d", i)
		}
		want := m.Globals[i]
		if j, ok := globals[want.Name]; ok && c.prog.Globals[j].Kind == want.Kind {
			return j, nil
		}
		switch want.Kind {
		case bytecode.GlobalBuiltin:
			g, ok := c.universal[want.Name]
			if !ok || g.kind != symBuiltin {
				return 0, fmt.Errorf("built-in function %s does not exist", want.Name)
			}
			return c.globalIndex(g, pos), nil
		case bytecode.GlobalNative:
			mod, _, _ := strings.Cut(want.Name, "::")
			if _, ok := c.config.Lookup(mod); !ok {
				return 0, fmt.Errorf("module %s is not available in this program", mod)
			}
			j := c.addGlobal(want.Name, bytecode.GlobalNative, pos)
			globals[want.Name] = j
			return j, nil
		}
		return 0, fmt.Errorf("%s is not defined", want.Name)
	}
	fn := func(i int) (int, error) {
		if i < len(m.Code) {
			return index[i], nil
		}
		if i-len(m.Code) >= len(m.Funcs) {
			return 0, fmt.Errorf("bad function index %d", i)
		}
		name := m.Funcs[i-len(m.Code)]
		if j, ok := globals[name]; ok && c.prog.Globals[j].Kind == bytecode.GlobalFunc {
			return c.prog.Globals[j].Func, nil
		}
		if cl, member, ok := cutLast(name, "::"); ok {
			if k, ok := classes[cl]; ok {
				for _, me := range c.prog.Classes[k].Methods {
					if me.Name == member && me.Func >= 0 {
						return me.Func, nil
					}
				}
			}
		}
		return 0, fmt.Errorf("function %s is not defined", name)
	}
	for i, f := range m.Code {
		g := *f
		g.Code = slices.Clone(f.Code)
		if err := bytecode.Relocate(&g, global, class, fn); err != nil {
			fail("%v", err)
		}
		c.prog.Funcs[index[i]] = &g
	}
	s.initFn = index[m.Init]
	c.linked = true
}

// cutLast is strings.Cut at the last sep.
func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}
