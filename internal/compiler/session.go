package compiler

import (
	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/diag"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/token"
)

// Session compiles a program piece by piece, as kukel repl reads it: each
// piece adds to the same Program, so that a VM can run the pieces one
// after the other and keep its state (vm.Extend). A piece is declarations,
// or statements; a local variable declared by a statement at the top
// level of a piece is a variable of the session, a global of the program.
//
// A piece with an error adds nothing: the session compiles the pieces
// before it again, into the same indexes.
type Session struct {
	c        *compiler
	builtins []string
	config   *native.Config
	file     *fileScope      // the scope of the pieces: their uses add up
	vars     map[string]bool // the session's variables
	inits    []globalInit    // the global variables so far, for Program.Init
	done     []func() error  // the pieces compiled so far, to compile them again
}

// NewSession starts a session with the prelude.
func NewSession(builtins []string, config *native.Config) (*Session, error) {
	s := &Session{builtins: builtins, config: config}
	if err := s.reset(); err != nil {
		return nil, err
	}
	return s, nil
}

// reset starts again from the prelude.
func (s *Session) reset() (err error) {
	c := &compiler{
		config:    s.config,
		prog:      &bytecode.Program{Main: -1},
		universal: make(map[string]*global),
		modules:   map[string]*module{"": {names: make(map[string]*global)}},
	}
	for _, name := range s.builtins {
		c.universal[name] = &global{kind: symBuiltin, index: -1, link: name, lkind: bytecode.GlobalBuiltin}
	}
	s.c, s.vars, s.inits = c, map[string]bool{}, nil
	s.file = &fileScope{mod: c.modules[""], uses: map[string]*module{}}
	defer s.recover(&err)
	f := preludeFile()
	jobs, globals := c.declarations([]*ast.File{f}, func(int, *ast.File) *fileScope { return c.fileScope(f, true) })
	c.prog.Init = c.functions(jobs, globals, token.Pos{File: PreludeName, Line: 1, Col: 1})
	if len(c.errs) > 0 {
		return diag.Join(c.errs)
	}
	c.prog.Init.Index = -1
	s.number()
	return nil
}

// Program is the program compiled so far. After an error it is a new
// Program, with the same globals, classes and functions as before.
func (s *Session) Program() *bytecode.Program { return s.c.prog }

// Declare compiles declarations: the files of modules (those already in
// the session are skipped) and of the program, then the declarations
// typed at the prompt (input, or nil), which share one scope. It gives the
// function that runs the initializers of the new global variables.
func (s *Session) Declare(files []*ast.File, input *ast.File) (*bytecode.Function, error) {
	return s.piece(func() (*bytecode.Function, error) {
		c := s.c
		var add []*ast.File
		for _, f := range files {
			if _, ok := c.modules[f.Module]; !ok || f.Module == "" {
				add = append(add, f)
			}
		}
		pos := token.Pos{Line: 1, Col: 1}
		for _, f := range add {
			if f.Module == "" { // the program's first file
				pos.File = f.Name
				break
			}
		}
		if input != nil {
			add = append(add, input)
			pos.File = input.Name
		}
		jobs, globals := c.declarations(add, func(_ int, f *ast.File) *fileScope {
			if f == input {
				return s.file
			}
			return c.fileScope(f, false)
		})
		if len(c.errs) > 0 {
			return nil, diag.Join(c.errs)
		}
		init := c.functions(jobs, globals, pos)
		if len(c.errs) > 0 {
			return nil, diag.Join(c.errs)
		}
		c.prog.Funcs = append(c.prog.Funcs, init)
		// Program.Init initializes all the global variables so far: a
		// worker runs it.
		s.inits = append(s.inits, globals...)
		c.prog.Init = c.initFunction(s.inits, token.Pos{File: PreludeName, Line: 1, Col: 1})
		c.prog.Init.Index = -1
		return init, nil
	})
}

// Statements compiles statements typed at the prompt into a function that
// runs them. A local variable declared at their top level becomes a
// variable of the session (declared again, it keeps its slot). If show is
// set, the last statement is an expression other than an assignment: the
// function returns its value.
func (s *Session) Statements(name string, stmts []ast.Stmt) (fn *bytecode.Function, show bool, err error) {
	body := make([]ast.Stmt, 0, len(stmts))
	for _, st := range stmts {
		d, ok := st.(*ast.LocalDecl)
		if !ok {
			body = append(body, st)
			continue
		}
		for _, v := range d.Vars {
			var x ast.Expr = v.Value
			if x == nil {
				x = &ast.NilLit{ValuePos: v.Name.NamePos}
			}
			body = append(body, &ast.ExprStmt{X: &ast.AssignExpr{Op: token.ASSIGN, LHS: v.Name, RHS: x}})
		}
	}
	if n := len(body); n > 0 {
		if e, ok := body[n-1].(*ast.ExprStmt); ok && shown(e.X) {
			body[n-1] = &ast.ReturnStmt{Return: e.X.Pos(), Value: e.X}
			show = true
		}
	}
	block := &ast.Block{Stmts: body}
	fn, err = s.piece(func() (*bytecode.Function, error) {
		c := s.c
		c.file = s.file
		for _, st := range stmts {
			if d, ok := st.(*ast.LocalDecl); ok {
				for _, v := range d.Vars {
					if !s.vars[v.Name.Name] {
						c.define(v.Name, symVar)
						s.vars[v.Name.Name] = true
					}
				}
			}
		}
		idx := len(c.prog.Funcs)
		c.prog.Funcs = append(c.prog.Funcs, nil) // function literals come after it
		pos := token.Pos{File: name, Line: 1, Col: 1}
		c.prog.Funcs[idx] = c.function("<input>", pos, &ast.ParamList{}, nil, false, nil, block, func() {
			c.block(block)
		})
		return c.prog.Funcs[idx], nil
	})
	return fn, show, err
}

// shown reports whether the REPL shows the value of expression statement
// x: not of an assignment or ++/--.
func shown(x ast.Expr) bool {
	switch x.(type) {
	case *ast.AssignExpr, *ast.IncDecExpr:
		return false
	}
	return true
}

// piece compiles one piece. After an error it compiles the pieces before
// it again, so that the session is as it was.
func (s *Session) piece(compile func() (*bytecode.Function, error)) (*bytecode.Function, error) {
	vars := make(map[string]bool, len(s.vars))
	for k := range s.vars {
		vars[k] = true
	}
	fn, err := s.try(compile)
	if err == nil {
		s.done = append(s.done, func() error { _, err := s.try(compile); return err })
		return fn, nil
	}
	if rerr := s.reset(); rerr != nil {
		panic("kukel session: " + rerr.Error())
	}
	for _, redo := range s.done {
		if rerr := redo(); rerr != nil {
			panic("kukel session: a piece compiled before fails now: " + rerr.Error())
		}
	}
	s.vars = vars
	return nil, err
}

// try runs compile, turning a compile error into an error.
func (s *Session) try(compile func() (*bytecode.Function, error)) (fn *bytecode.Function, err error) {
	s.c.errs = nil
	defer s.recover(&err)
	if fn, err = compile(); err != nil {
		return nil, err
	}
	if len(s.c.errs) > 0 {
		return nil, diag.Join(s.c.errs)
	}
	s.number()
	return fn, nil
}

// number gives every function its index.
func (s *Session) number() {
	for i, f := range s.c.prog.Funcs {
		f.Index = i
	}
}

func (s *Session) recover(err *error) {
	if r := recover(); r != nil {
		b, ok := r.(bailout)
		if !ok {
			panic(r)
		}
		*err = b.err
	}
	s.c.fs = nil
}
