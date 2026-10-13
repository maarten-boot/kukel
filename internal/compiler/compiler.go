// Package compiler translates syntax trees into bytecode for the stack
// machine. It also resolves names (locals, parameters, temporaries, then
// top-level names) and evaluates constants.
//
// Milestone 3 supports: functions with required parameters and
// temporaries, global and constant declarations, locals, expressions on
// numbers, strings and booleans, calls, if/elseif/else, while, do-while,
// for, break, continue and return. Other constructs are reported as "not
// supported yet".
package compiler

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/diag"
	"github.com/maarten-boot/kukel/internal/token"
	"github.com/maarten-boot/kukel/internal/value"
)

const maxIndex = math.MaxUint16 // constants, slots, globals, arguments

type symKind uint8

const (
	symVar symKind = iota
	symFunc
	symBuiltin
	symConst
	symClass
)

// global is a top-level name.
type global struct {
	kind   symKind
	index  int         // global slot; -1 for a built-in not yet used
	value  value.Value // symConst
	class  *classInfo  // symClass
	params *paramShape // symFunc
	pos    token.Pos   // definition (zero for built-ins)
}

type compiler struct {
	prog    *bytecode.Program
	globals map[string]*global
	fs      *funcState // the function being compiled
}

// bailout carries a compile error up to Compile.
type bailout struct{ err *diag.Error }

func (c *compiler) errorf(pos token.Pos, format string, args ...any) {
	panic(bailout{diag.Errorf(pos, format, args...)})
}

func (c *compiler) notYet(pos token.Pos, what string, milestone int) {
	c.errorf(pos, "%s not supported yet (milestone %d)", what, milestone)
}

// Compile compiles the files of one program, in order. builtins lists the
// names of the built-in functions.
func Compile(files []*ast.File, builtins []string) (prog *bytecode.Program, err error) {
	c := &compiler{
		prog:    &bytecode.Program{Main: -1},
		globals: make(map[string]*global),
	}
	defer func() {
		if r := recover(); r != nil {
			b, ok := r.(bailout)
			if !ok {
				panic(r)
			}
			prog, err = nil, b.err
		}
	}()
	for _, name := range builtins {
		c.globals[name] = &global{kind: symBuiltin, index: -1}
	}
	user := files
	files = append([]*ast.File{preludeFile()}, files...)

	// Pass 1: define all top-level names and classes, evaluate constants in
	// order. Every function and member function definition becomes a job;
	// its index is its index in Program.Funcs.
	var jobs []funcJob
	var methodDefs []*ast.FuncDecl
	var globals []*ast.VarSpec
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Class != nil {
					methodDefs = append(methodDefs, d) // after all classes are known
					continue
				}
				g := c.define(d.Name, symFunc)
				c.prog.Globals[g.index].Func = len(jobs)
				g.params = shapeOf(d.Name.Name, d.Params)
				jobs = append(jobs, funcJob{decl: d})
			case *ast.GlobalDecl:
				for _, v := range d.Vars {
					c.define(v.Name, symVar)
					globals = append(globals, v)
				}
			case *ast.ConstDecl:
				for _, v := range d.Vars {
					val := c.evalConst(v.Value)
					c.define(v.Name, symConst).value = val
				}
			case *ast.ClassDecl:
				c.declareClass(d)
			}
		}
	}
	for _, d := range methodDefs {
		k, m := c.methodDefinition(d)
		m.fn = len(jobs)
		k.bc.Methods[m.method].Func = m.fn
		jobs = append(jobs, funcJob{decl: d, class: k, member: m})
	}

	// The entry point.
	pos := token.Pos{Line: 1, Col: 1}
	if len(user) > 0 {
		pos.File = user[0].Name
	}
	m, ok := c.globals["main"]
	if !ok || m.kind != symFunc {
		c.errorf(pos, "the program has no main function")
	}
	if fd := jobs[c.prog.Globals[m.index].Func].decl; len(fd.Params.Params) > 1 {
		c.errorf(fd.Name.NamePos, "main takes at most one parameter (the command-line arguments)")
	}
	c.prog.Main = m.index

	// Pass 2: the global initializers, then the functions.
	c.prog.Funcs = make([]*bytecode.Function, len(jobs)) // function literals are added after these
	c.prog.Init = c.function("<init>", pos, &ast.ParamList{}, nil, false, nil, initializers(globals), func() {
		for _, v := range globals {
			if v.Value != nil {
				c.mark(v.Pos())
				c.expr(v.Value)
				c.emit(bytecode.OpStoreGlobal, c.globals[v.Name.Name].index)
			}
		}
	})
	for i, job := range jobs {
		fd := job.decl
		name, static := fd.Name.Name, false
		if job.class != nil {
			name, static = job.class.name+"::"+name, job.member.static
		}
		c.prog.Funcs[i] = c.function(name, fd.Pos(), fd.Params, job.class, static, nil, fd, func() {
			c.block(fd.Body)
		})
	}
	c.prog.Init.Index = -1
	for i, f := range c.prog.Funcs {
		f.Index = i
	}
	return c.prog, nil
}

// cache reserves an inline cache slot for a member instruction.
func (c *compiler) cache(pos token.Pos) int {
	fn := c.fs.fn
	if fn.NumCaches > maxIndex {
		c.errorf(pos, "too many member accesses in one function (more than %d)", maxIndex+1)
	}
	fn.NumCaches++
	return fn.NumCaches - 1
}

// initializers collects the global initializers (the body of <init>) for
// the capture analysis.
func initializers(vars []*ast.VarSpec) ast.Node {
	var exprs []ast.Expr
	for _, v := range vars {
		if v.Value != nil {
			exprs = append(exprs, v.Value)
		}
	}
	return &ast.VectorLit{Elems: exprs}
}

// funcJob is a function or member function definition to compile.
type funcJob struct {
	decl   *ast.FuncDecl
	class  *classInfo  // for a member function
	member *memberInfo // for a member function
}

// define adds a top-level name.
func (c *compiler) define(id *ast.Ident, kind symKind) *global {
	if old, ok := c.globals[id.Name]; ok {
		if old.kind == symBuiltin {
			c.errorf(id.NamePos, "%s is a built-in function and cannot be redefined", id.Name)
		}
		c.errorf(id.NamePos, "%s is already defined at %s", id.Name, old.pos)
	}
	g := &global{kind: kind, index: -1, pos: id.NamePos}
	switch kind {
	case symVar:
		g.index = c.addGlobal(id.Name, bytecode.GlobalVar, id.NamePos)
	case symFunc:
		g.index = c.addGlobal(id.Name, bytecode.GlobalFunc, id.NamePos)
	case symClass:
		g.index = c.addGlobal(id.Name, bytecode.GlobalClass, id.NamePos)
	}
	c.globals[id.Name] = g
	return g
}

func (c *compiler) addGlobal(name string, kind bytecode.GlobalKind, pos token.Pos) int {
	if len(c.prog.Globals) > maxIndex {
		c.errorf(pos, "too many global names (more than %d)", maxIndex+1)
	}
	c.prog.Globals = append(c.prog.Globals, bytecode.Global{Name: name, Kind: kind})
	return len(c.prog.Globals) - 1
}

// globalIndex returns the global slot of g, allocating one for a built-in
// on first use.
func (c *compiler) globalIndex(name string, g *global, pos token.Pos) int {
	if g.index < 0 {
		g.index = c.addGlobal(name, bytecode.GlobalBuiltin, pos)
	}
	return g.index
}

// ----------------------------------------------------------------------
// Functions

// funcState is the state of the function being compiled.
type funcState struct {
	fn       *bytecode.Function
	parent   *funcState            // the enclosing function of a function literal
	captured map[string]bool       // names used inside nested function literals
	upvalues map[string]int        // a function literal's captured variables
	scopes   []map[string]localVar // innermost last; scopes[0] holds params and temporaries
	nslots   int                   // slots in use
	depth    int                   // operand stack depth at the current instruction
	maxDepth int
	targets  []*target  // enclosing loops, switches and labelled statements
	line     int        // line of the code being emitted
	class    *classInfo // the class of a member function, else nil
	static   bool       // a static member function (no this)
	tries    []*tryCtx  // enclosing try, catch and finally blocks
}

// localVar is a local, parameter or temporary: its slot, and whether the
// slot holds a cell (when a function literal may capture it).
type localVar struct {
	slot int
	cell bool
}

// function compiles one function; body emits the code of its body, and
// tree (the function's syntax, or nil) is used to find captured names.
// parent is the enclosing function of a function literal.
func (c *compiler) function(name string, pos token.Pos, params *ast.ParamList, class *classInfo, static bool,
	parent *funcState, tree ast.Node, body func()) *bytecode.Function {
	outer := c.fs
	fs := &funcState{
		fn:       &bytecode.Function{FuncName: name, File: pos.File, Line: pos.Line},
		parent:   parent,
		captured: capturedNames(tree),
		upvalues: map[string]int{},
		scopes:   []map[string]localVar{{}},
		line:     pos.Line,
		class:    class,
		static:   static,
	}
	c.fs = fs
	defer func() { c.fs = outer }()

	// Parameters take slots 0, 1, ... in order. The code of a default value
	// runs at the start of the function when the argument is missing; it
	// sees only the parameters before it.
	for i, p := range params.Params {
		switch {
		case p.Rest:
			if i != len(params.Params)-1 {
				c.errorf(p.Pos(), "the rest parameter %s must be the last parameter", p.Name.Name)
			}
			fs.fn.HasRest = true
		case p.Default != nil:
			if fs.fn.NumOptional++; fs.fn.NumOptional > 255 {
				c.errorf(p.Pos(), "too many optional parameters (more than 255)")
			}
			skip := c.emit(bytecode.OpJumpIfArg, i, 0)
			c.mark(p.Pos())
			c.expr(p.Default)
			c.emit(bytecode.OpStoreLocal, i)
			c.patch([]int{skip + 2}, c.here())
		default:
			if fs.fn.NumOptional > 0 {
				c.errorf(p.Pos(), "parameter %s needs a default value, because a parameter before it has one", p.Name.Name)
			}
			fs.fn.NumParams++
		}
		if lv := c.declare(p.Name, "parameter"); lv.cell {
			c.emit(bytecode.OpLoadLocal, lv.slot)
			c.emit(bytecode.OpNewCell, lv.slot)
		}
	}
	for _, t := range params.Temps {
		if lv := c.declare(t, "temporary"); lv.cell {
			c.emit(bytecode.OpNil)
			c.emit(bytecode.OpNewCell, lv.slot)
		}
	}

	body()
	c.emit(bytecode.OpReturnNil)

	fs.fn.MaxStack = fs.maxDepth
	return fs.fn
}

// declare adds a name to the innermost scope and gives it a slot. A name
// that a nested function literal uses gets a cell (the caller fills it).
func (c *compiler) declare(id *ast.Ident, what string) localVar {
	fs := c.fs
	scope := fs.scopes[len(fs.scopes)-1]
	if _, ok := scope[id.Name]; ok {
		if len(fs.scopes) == 1 {
			c.errorf(id.NamePos, "%s %s repeats a parameter or temporary name", what, id.Name)
		}
		c.errorf(id.NamePos, "local %s is already declared in this block", id.Name)
	}
	if fs.nslots > maxIndex {
		c.errorf(id.NamePos, "too many local variables (more than %d)", maxIndex+1)
	}
	slot := fs.nslots
	fs.nslots++
	lv := localVar{slot: slot, cell: fs.captured[id.Name]}
	scope[id.Name] = lv
	if slot == len(fs.fn.SlotNames) {
		fs.fn.SlotNames = append(fs.fn.SlotNames, id.Name)
	} else {
		fs.fn.SlotNames[slot] += "/" + id.Name // slot reused by a later block
	}
	if fs.nslots > fs.fn.NumSlots {
		fs.fn.NumSlots = fs.nslots
	}
	return lv
}

// declareInit declares a variable and stores the value on the stack into
// it (into a new cell for a captured one: each run of the declaration makes
// a fresh variable, which closures in loops rely on).
func (c *compiler) declareInit(id *ast.Ident) {
	lv := c.declare(id, "local")
	if lv.cell {
		c.emit(bytecode.OpNewCell, lv.slot)
	} else {
		c.emit(bytecode.OpStoreLocal, lv.slot)
	}
}

func (c *compiler) pushScope() { c.fs.scopes = append(c.fs.scopes, map[string]localVar{}) }

// popScope ends the innermost block; its slots are reused afterwards.
func (c *compiler) popScope() {
	fs := c.fs
	fs.nslots -= len(fs.scopes[len(fs.scopes)-1])
	fs.scopes = fs.scopes[:len(fs.scopes)-1]
}

// ----------------------------------------------------------------------
// Code emission

// mark sets the source line of the code emitted next.
func (c *compiler) mark(pos token.Pos) {
	if pos.Line > 0 {
		c.fs.line = pos.Line
	}
}

// stackEffect is the change in operand stack depth caused by op.
func stackEffect(op bytecode.Op, operands []int) int {
	pops, pushes := bytecode.StackUse(op, operands)
	return pushes - pops
}

// emit appends an instruction and returns the offset of its first operand.
func (c *compiler) emit(op bytecode.Op, operands ...int) int {
	fs := c.fs
	fn := fs.fn
	if n := len(fn.Lines); n == 0 || fn.Lines[n-1].Line != fs.line {
		fn.Lines = append(fn.Lines, bytecode.LineInfo{PC: len(fn.Code), Line: fs.line})
	}
	fn.Code = append(fn.Code, byte(op))
	at := len(fn.Code)
	info, _ := bytecode.Info(op)
	for i, w := range info.Operands {
		switch w {
		case 2:
			fn.Code = binary.LittleEndian.AppendUint16(fn.Code, uint16(operands[i]))
		case 4:
			fn.Code = binary.LittleEndian.AppendUint32(fn.Code, uint32(operands[i]))
		}
	}
	fs.depth += stackEffect(op, operands)
	if fs.depth > fs.maxDepth {
		fs.maxDepth = fs.depth
	}
	return at
}

// here is the offset of the next instruction.
func (c *compiler) here() int { return len(c.fs.fn.Code) }

// jump emits a jump to be patched later and returns its operand offset.
func (c *compiler) jump(op bytecode.Op) int { return c.emit(op, 0) }

// patch makes the jumps at the given operand offsets go to target.
func (c *compiler) patch(at []int, target int) {
	for _, a := range at {
		binary.LittleEndian.PutUint32(c.fs.fn.Code[a:], uint32(target))
	}
}

func (c *compiler) constant(v value.Value, pos token.Pos) {
	c.emit(bytecode.OpConst, c.constIndex(v, pos))
}

// constIndex returns the index of v in the function's constants, adding it
// if needed.
func (c *compiler) constIndex(v value.Value, pos token.Pos) int {
	fn := c.fs.fn
	for i, k := range fn.Consts {
		same := k.Kind() == v.Kind() && value.Equal(k, v)
		if v.Kind() == value.Float { // keep 0.0 and -0.0 apart
			same = k.Kind() == value.Float && math.Float64bits(k.AsFloat()) == math.Float64bits(v.AsFloat())
		}
		if same {
			return i
		}
	}
	if len(fn.Consts) > maxIndex {
		c.errorf(pos, "too many constants in one function (more than %d)", maxIndex+1)
	}
	fn.Consts = append(fn.Consts, v)
	return len(fn.Consts) - 1
}

// nameConst returns the constant index of a member name.
func (c *compiler) nameConst(id *ast.Ident) int {
	return c.constIndex(value.MakeString(id.Name), id.NamePos)
}

func (c *compiler) pushValue(v value.Value, pos token.Pos) {
	switch v.Kind() {
	case value.Nil:
		c.emit(bytecode.OpNil)
	case value.Bool:
		if v.AsBool() {
			c.emit(bytecode.OpTrue)
		} else {
			c.emit(bytecode.OpFalse)
		}
	default:
		c.constant(v, pos)
	}
}

// ----------------------------------------------------------------------
// Statements

// block compiles a block in a new scope. (A function body's block is a scope
// of its own, inside the scope of the parameters and temporaries.)
func (c *compiler) block(b *ast.Block) {
	c.pushScope()
	for _, s := range b.Stmts {
		if d, ok := s.(*ast.LocalDecl); ok {
			c.local(d)
		} else {
			c.stmt(s)
		}
	}
	c.popScope()
}

func (c *compiler) local(d *ast.LocalDecl) {
	for _, v := range d.Vars {
		c.mark(v.Pos())
		if v.Value != nil {
			c.expr(v.Value)
		} else {
			c.emit(bytecode.OpNil)
		}
		// The name becomes visible after its initializer.
		c.declareInit(v.Name)
	}
}

func (c *compiler) stmt(s ast.Stmt) { c.labeledStmt(s, nil) }

// labeledStmt compiles s, which carries the given labels ("a: b: s").
func (c *compiler) labeledStmt(s ast.Stmt, labels []*ast.Ident) {
	c.mark(s.Pos())
	switch s := s.(type) {
	case *ast.LabeledStmt:
		c.checkLabel(s.Label, labels)
		c.labeledStmt(s.Stmt, append(labels[:len(labels):len(labels)], s.Label))
		return
	case *ast.WhileStmt:
		c.whileStmt(s, labels)
	case *ast.DoStmt:
		c.doStmt(s, labels)
	case *ast.ForStmt:
		c.forStmt(s, labels)
	case *ast.SwitchStmt:
		c.switchStmt(s, labels)
	case *ast.ForEachStmt:
		c.forEachStmt(s, labels)
	default:
		if labels == nil {
			c.simpleStmt(s)
			break
		}
		// "label: stmt" for any other statement: break label leaves it.
		t := c.pushTarget(targetPlain, labels)
		c.simpleStmt(s)
		c.popTarget()
		c.patch(t.breaks, c.here())
	}
	if c.fs.depth != 0 {
		c.errorf(s.Pos(), "internal error: operand stack depth %d after statement", c.fs.depth)
	}
}

// simpleStmt compiles a statement that is not a loop or switch.
func (c *compiler) simpleStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.ExprStmt:
		c.exprDiscard(s.X)
	case *ast.EmptyStmt:
	case *ast.Block:
		c.block(s)
	case *ast.LocalDecl:
		c.errorf(s.Pos(), "a local declaration must be a statement directly inside a block")
	case *ast.IfStmt:
		c.ifStmt(s)
	case *ast.BreakStmt:
		c.breakContinue(s.Pos(), s.Label, true)
	case *ast.ContinueStmt:
		c.breakContinue(s.Pos(), s.Label, false)
	case *ast.ReturnStmt:
		c.returnStmt(s)
	case *ast.CaseLabel:
		c.caseLabel(s)
	case *ast.DefaultLabel:
		c.defaultLabel(s)
	case *ast.TryStmt:
		c.tryStmt(s)
	case *ast.ThrowStmt:
		c.expr(s.Value)
		c.mark(s.Throw)
		c.emit(bytecode.OpThrow)
	default:
		c.errorf(s.Pos(), "internal error: unknown statement %T", s)
	}
}

func (c *compiler) ifStmt(s *ast.IfStmt) {
	elseJumps := c.branch(s.Cond, false)
	c.stmt(s.Then)
	if s.Else == nil {
		c.patch(elseJumps, c.here())
		return
	}
	end := c.jump(bytecode.OpJump)
	c.patch(elseJumps, c.here())
	c.stmt(s.Else)
	c.patch([]int{end}, c.here())
}

func (c *compiler) whileStmt(s *ast.WhileStmt, labels []*ast.Ident) {
	top := c.here()
	exit := c.branch(s.Cond, false)
	t := c.pushTarget(targetLoop, labels)
	c.stmt(s.Body)
	c.popTarget()
	c.patch(t.continues, top)
	c.emit(bytecode.OpJump, top)
	c.patch(append(exit, t.breaks...), c.here())
}

func (c *compiler) doStmt(s *ast.DoStmt, labels []*ast.Ident) {
	top := c.here()
	t := c.pushTarget(targetLoop, labels)
	c.stmt(s.Body)
	c.popTarget()
	c.patch(t.continues, c.here())
	c.mark(s.Cond.Pos())
	c.patch(c.branch(s.Cond, true), top)
	c.patch(t.breaks, c.here())
}

func (c *compiler) forStmt(s *ast.ForStmt, labels []*ast.Ident) {
	c.pushScope()
	if s.InitVars != nil {
		c.local(&ast.LocalDecl{Local: s.For, Vars: s.InitVars})
	} else if s.Init != nil {
		c.exprDiscard(s.Init)
	}
	top := c.here()
	var exit []int
	if s.Cond != nil {
		c.mark(s.Cond.Pos())
		exit = c.branch(s.Cond, false)
	}
	t := c.pushTarget(targetLoop, labels)
	c.stmt(s.Body)
	c.popTarget()
	c.patch(t.continues, c.here())
	// Each iteration has its own copy of captured loop variables (Q16): copy
	// them into new cells before the update runs.
	for _, v := range s.InitVars {
		if lv := c.fs.scopes[len(c.fs.scopes)-1][v.Name.Name]; lv.cell {
			c.emit(bytecode.OpLoadCell, lv.slot)
			c.emit(bytecode.OpNewCell, lv.slot)
		}
	}
	if s.Post != nil {
		c.mark(s.Post.Pos())
		c.exprDiscard(s.Post)
	}
	c.emit(bytecode.OpJump, top)
	c.patch(append(exit, t.breaks...), c.here())
	c.popScope()
}

// forEachStmt compiles "for (local k[, v] in x) body":
//
//	x; ITER_INIT; STORE hidden
//	top: ITER_NEXT hidden, n -> end; STORE v; STORE k
//	body; JUMP top
//	end:
func (c *compiler) forEachStmt(s *ast.ForEachStmt, labels []*ast.Ident) {
	c.pushScope()
	it := c.declare(&ast.Ident{NamePos: s.For, Name: "(iterator)"}, "hidden").slot
	c.mark(s.X.Pos())
	c.expr(s.X)
	c.emit(bytecode.OpIterInit)
	c.emit(bytecode.OpStoreLocal, it)

	// The loop variables are visible in the body only (not in x).
	n := 1
	key := c.declare(s.Key, "local")
	var val localVar
	if s.Value != nil {
		n = 2
		val = c.declare(s.Value, "local")
	}
	top := c.here()
	c.mark(s.For)
	next := c.emit(bytecode.OpIterNext, it, n, 0)
	if n == 2 {
		c.storeFresh(val)
	}
	c.storeFresh(key)

	t := c.pushTarget(targetLoop, labels)
	c.stmt(s.Body)
	c.popTarget()
	c.patch(t.continues, top)
	c.emit(bytecode.OpJump, top)
	c.patch(append(t.breaks, next+4), c.here())
	c.popScope()
}

// ----------------------------------------------------------------------
// break and continue targets, labels

type targetKind uint8

const (
	targetPlain  targetKind = iota // a labelled statement other than a loop or switch
	targetLoop                     // while, do, for
	targetSwitch                   // switch
)

// target is a statement that break (and, for loops, continue) can leave. It
// collects the jumps to patch.
type target struct {
	kind      targetKind
	labels    []*ast.Ident
	breaks    []int
	continues []int
	sw        *switchState // targetSwitch
}

func (t *target) hasLabel(name string) bool {
	for _, l := range t.labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

func (c *compiler) pushTarget(kind targetKind, labels []*ast.Ident) *target {
	t := &target{kind: kind, labels: labels}
	c.fs.targets = append(c.fs.targets, t)
	return t
}

func (c *compiler) popTarget() { c.fs.targets = c.fs.targets[:len(c.fs.targets)-1] }

// checkLabel rejects a label that an enclosing statement (or the same
// statement) already uses.
func (c *compiler) checkLabel(label *ast.Ident, chain []*ast.Ident) {
	for _, l := range chain {
		if l.Name == label.Name {
			c.errorf(label.NamePos, "label %s is already used at %s", label.Name, l.NamePos)
		}
	}
	for _, t := range c.fs.targets {
		for _, l := range t.labels {
			if l.Name == label.Name {
				c.errorf(label.NamePos, "label %s is already used by an enclosing statement at %s", label.Name, l.NamePos)
			}
		}
	}
}

func (c *compiler) breakContinue(pos token.Pos, label *ast.Ident, isBreak bool) {
	var t *target
	ti := -1
	for i := len(c.fs.targets) - 1; i >= 0 && t == nil; i-- {
		cand := c.fs.targets[i]
		switch {
		case label != nil:
			if cand.hasLabel(label.Name) {
				t, ti = cand, i
			}
		case cand.kind == targetLoop, cand.kind == targetSwitch && isBreak:
			t, ti = cand, i
		}
	}
	switch {
	case t == nil && label != nil:
		c.errorf(label.NamePos, "no enclosing statement is labelled %s", label.Name)
	case t == nil && isBreak:
		c.errorf(pos, "break outside a loop or switch")
	case t == nil:
		c.errorf(pos, "continue outside a loop")
	case !isBreak && t.kind != targetLoop:
		c.errorf(label.NamePos, "continue %s: %s does not label a loop", label.Name, label.Name)
	}
	what := "continue"
	if isBreak {
		what = "break"
	}
	c.leaveTries(ti, pos, what)
	at := c.jump(bytecode.OpJump)
	if isBreak {
		t.breaks = append(t.breaks, at)
	} else {
		t.continues = append(t.continues, at)
	}
}

// ----------------------------------------------------------------------
// switch

// switchState collects the case and default labels of a switch body.
type switchState struct {
	cases      []switchCase
	dflt       int // address of the default label, or -1
	dfltPos    token.Pos
	firstScope int // index of the first scope opened inside the body
	target     int // index of the switch's target in funcState.targets
}

type switchCase struct {
	value value.Value
	pos   token.Pos
	addr  int
}

// switchStmt compiles the body first and the dispatch after it, because
// case labels may sit anywhere inside the body (also in nested statements):
//
//	tag; STORE hidden; JUMP dispatch
//	body (case and default labels record their addresses); JUMP end
//	dispatch: for each case: LOAD hidden; CONST v; EQ; JUMP_IF_TRUE case
//	          JUMP default (if any)
//	end:
func (c *compiler) switchStmt(s *ast.SwitchStmt, labels []*ast.Ident) {
	c.pushScope()
	tag := c.declare(&ast.Ident{NamePos: s.Switch, Name: "(switch)"}, "hidden").slot
	c.mark(s.Tag.Pos())
	c.expr(s.Tag)
	c.emit(bytecode.OpStoreLocal, tag)
	toDispatch := c.jump(bytecode.OpJump)

	t := c.pushTarget(targetSwitch, labels)
	sw := &switchState{dflt: -1}
	t.sw = sw
	sw.target = len(c.fs.targets) - 1
	c.pushScope()
	sw.firstScope = len(c.fs.scopes)
	for _, st := range s.Body.Stmts {
		if d, ok := st.(*ast.LocalDecl); ok {
			c.errorf(d.Pos(), "a local declaration may not be directly in a switch body (put it in a block)")
		}
		c.stmt(st)
	}
	c.popScope()
	c.popTarget()
	end := c.jump(bytecode.OpJump)

	c.patch([]int{toDispatch}, c.here())
	c.mark(s.Switch)
	for _, cs := range sw.cases {
		c.emit(bytecode.OpLoadLocal, tag)
		c.pushValue(cs.value, cs.pos)
		c.emit(bytecode.OpEq)
		c.emit(bytecode.OpJumpIfTrue, cs.addr)
	}
	if sw.dflt >= 0 {
		c.emit(bytecode.OpJump, sw.dflt)
	}
	c.patch(append(t.breaks, end), c.here())
	c.popScope()
}

// innermostSwitch returns the switch whose body holds a case or default
// label at pos.
func (c *compiler) innermostSwitch(pos token.Pos) *switchState {
	for i := len(c.fs.targets) - 1; i >= 0; i-- {
		if t := c.fs.targets[i]; t.kind == targetSwitch {
			return t.sw
		}
	}
	c.errorf(pos, "case and default labels are only allowed inside a switch body")
	return nil
}

// checkLabelScope rejects a label that a jump would reach past the
// initialization of a local that is still in scope (C++ rule).
func (c *compiler) checkLabelScope(sw *switchState, pos token.Pos, what string) {
	name, slot := "", -1
	for _, scope := range c.fs.scopes[sw.firstScope:] {
		for n, lv := range scope {
			if slot < 0 || lv.slot < slot { // report the first declared
				name, slot = n, lv.slot
			}
		}
	}
	if slot >= 0 {
		c.errorf(pos, "%s label skips the initialization of local %s", what, name)
	}
}

func (c *compiler) caseLabel(s *ast.CaseLabel) {
	sw := c.innermostSwitch(s.Pos())
	v := c.evalConst(s.Value)
	for _, prev := range sw.cases {
		if value.Equal(prev.value, v) {
			c.errorf(s.Value.Pos(), "duplicate case value %s (also at %s)", v.Repr(), prev.pos)
		}
	}
	c.checkLabelTry(sw, s.Pos(), "case")
	c.checkLabelScope(sw, s.Pos(), "case")
	sw.cases = append(sw.cases, switchCase{value: v, pos: s.Value.Pos(), addr: c.here()})
}

func (c *compiler) defaultLabel(s *ast.DefaultLabel) {
	sw := c.innermostSwitch(s.Pos())
	if sw.dflt >= 0 {
		c.errorf(s.Pos(), "a switch has only one default label (the first is at %s)", sw.dfltPos)
	}
	c.checkLabelTry(sw, s.Pos(), "default")
	c.checkLabelScope(sw, s.Pos(), "default")
	sw.dflt, sw.dfltPos = c.here(), s.Pos()
}

// ----------------------------------------------------------------------
// Expressions

// exprDiscard compiles an expression for its side effects only.
func (c *compiler) exprDiscard(e ast.Expr) {
	switch e := e.(type) {
	case *ast.AssignExpr:
		c.assign(e, false)
	case *ast.IncDecExpr:
		c.incDec(e, false)
	case *ast.CommaExpr:
		for _, x := range e.List {
			c.exprDiscard(x)
		}
	default:
		c.expr(e)
		c.emit(bytecode.OpPop)
	}
}

var binaryOps = map[token.Kind]bytecode.Op{
	token.PLUS: bytecode.OpAdd, token.MINUS: bytecode.OpSub, token.STAR: bytecode.OpMul,
	token.SLASH: bytecode.OpDiv, token.PERCENT: bytecode.OpMod,
	token.AMP: bytecode.OpBitAnd, token.PIPE: bytecode.OpBitOr, token.CARET: bytecode.OpBitXor,
	token.SHL: bytecode.OpShl, token.SHR: bytecode.OpShr,
	token.EQ: bytecode.OpEq, token.NE: bytecode.OpNe,
	token.LT: bytecode.OpLt, token.LE: bytecode.OpLe, token.GT: bytecode.OpGt, token.GE: bytecode.OpGe,
}

var assignOps = map[token.Kind]bytecode.Op{
	token.ADD_ASSIGN: bytecode.OpAdd, token.SUB_ASSIGN: bytecode.OpSub, token.MUL_ASSIGN: bytecode.OpMul,
	token.DIV_ASSIGN: bytecode.OpDiv, token.MOD_ASSIGN: bytecode.OpMod,
	token.AND_ASSIGN: bytecode.OpBitAnd, token.OR_ASSIGN: bytecode.OpBitOr, token.XOR_ASSIGN: bytecode.OpBitXor,
	token.SHL_ASSIGN: bytecode.OpShl, token.SHR_ASSIGN: bytecode.OpShr,
}

// expr compiles an expression that leaves its value on the stack.
func (c *compiler) expr(e ast.Expr) {
	switch e := e.(type) {
	case *ast.IntLit:
		c.constant(value.MakeInt(e.Value), e.Pos())
	case *ast.CharLit:
		c.constant(value.MakeInt(e.Value), e.Pos())
	case *ast.FloatLit:
		c.constant(value.MakeFloat(e.Value), e.Pos())
	case *ast.StringLit:
		c.constant(value.MakeString(e.Value), e.Pos())
	case *ast.BoolLit:
		c.pushValue(value.MakeBool(e.Value), e.Pos())
	case *ast.NilLit:
		c.emit(bytecode.OpNil)
	case *ast.Ident:
		c.load(e)
	case *ast.BinaryExpr:
		if e.Op == token.ANDAND || e.Op == token.OROR {
			c.boolValue(e)
			return
		}
		c.expr(e.X)
		c.expr(e.Y)
		c.mark(e.OpPos)
		c.emit(binaryOps[e.Op])
	case *ast.UnaryExpr:
		c.expr(e.X)
		c.mark(e.OpPos)
		switch e.Op {
		case token.MINUS:
			c.emit(bytecode.OpNeg)
		case token.BANG:
			c.emit(bytecode.OpNot)
		case token.TILDE:
			c.emit(bytecode.OpBitNot)
		}
	case *ast.CondExpr:
		elseJumps := c.branch(e.Cond, false)
		c.expr(e.Then)
		end := c.jump(bytecode.OpJump)
		c.fs.depth-- // the else branch starts without the then value
		c.patch(elseJumps, c.here())
		c.expr(e.Else)
		c.patch([]int{end}, c.here())
	case *ast.CommaExpr:
		for _, x := range e.List[:len(e.List)-1] {
			c.exprDiscard(x)
		}
		c.expr(e.List[len(e.List)-1])
	case *ast.AssignExpr:
		c.assign(e, true)
	case *ast.IncDecExpr:
		c.incDec(e, true)
	case *ast.CallExpr:
		if len(e.Args) > maxIndex {
			c.errorf(e.Lparen, "too many arguments (more than %d)", maxIndex)
		}
		if c.memberCall(e) {
			return
		}
		c.checkArgs(c.staticCallee(e.Fun), e.Args, e.Lparen)
		c.expr(e.Fun)
		if hasSpread(e.Args) {
			c.argVector(e.Args)
			c.mark(e.Lparen)
			c.emit(bytecode.OpCallV)
			return
		}
		for _, a := range e.Args {
			c.expr(a)
		}
		c.mark(e.Lparen)
		c.emit(bytecode.OpCall, len(e.Args))
	case *ast.VectorLit:
		if len(e.Elems) > maxIndex {
			c.errorf(e.Lbrack, "too many elements in a vector literal (more than %d)", maxIndex)
		}
		if hasSpread(e.Elems) {
			c.argVector(e.Elems)
			return
		}
		for _, x := range e.Elems {
			c.expr(x)
		}
		c.mark(e.Lbrack)
		c.emit(bytecode.OpMakeVector, len(e.Elems))
	case *ast.MapLit:
		if len(e.Entries) > maxIndex {
			c.errorf(e.Lbrace, "too many entries in a map literal (more than %d)", maxIndex)
		}
		for _, en := range e.Entries {
			c.expr(en.Key)
			c.expr(en.Value)
		}
		c.mark(e.Lbrace)
		c.emit(bytecode.OpMakeMap, len(e.Entries))
	case *ast.IndexExpr:
		c.expr(e.X)
		c.expr(e.Index)
		c.mark(e.Lbrack)
		c.emit(bytecode.OpIndexGet)
	case *ast.ThisExpr:
		c.loadThis(e.Pos())
	case *ast.MemberExpr:
		if f := c.thisField(e); f != nil {
			c.emit(bytecode.OpLoadMember, f.field)
			return
		}
		c.expr(e.X)
		c.mark(e.Name.NamePos)
		c.emit(bytecode.OpGetMember, c.nameConst(e.Name), c.cache(e.Name.NamePos))
	case *ast.MethodCall:
		c.expr(e.X)
		c.callArgs(e.Args, e.Name.NamePos,
			func(argc int) { c.emit(bytecode.OpCallMethod, c.nameConst(e.Name), argc, c.cache(e.Name.NamePos)) },
			func() { c.emit(bytecode.OpCallMethodV, c.nameConst(e.Name), c.cache(e.Name.NamePos)) })
	case *ast.NewExpr:
		c.checkNew(e)
		c.newClassValue(e.Class)
		c.callArgs(e.Args, e.New,
			func(argc int) { c.emit(bytecode.OpNew, argc) },
			func() { c.emit(bytecode.OpNewV) })
	case *ast.StaticRef:
		c.loadStaticRef(e)
	case *ast.FuncLit:
		c.emit(bytecode.OpClosure, c.funcLit(e))
	case *ast.Spread:
		c.errorf(e.Pos(), "a spread (x..) is only allowed in an argument list or vector literal")
	default:
		c.errorf(e.Pos(), "internal error: unknown expression %T", e)
	}
}

func hasSpread(args []ast.Expr) bool {
	for _, a := range args {
		if _, ok := a.(*ast.Spread); ok {
			return true
		}
	}
	return false
}

// argVector builds a vector of the arguments, expanding spread ones:
// f(a, b, v.., c) is  a; b; MAKE_VECTOR 2; v; EXTEND; c; APPEND.
func (c *compiler) argVector(args []ast.Expr) {
	n := 0
	for n < len(args) && !hasSpread(args[n:n+1]) {
		c.expr(args[n])
		n++
	}
	c.emit(bytecode.OpMakeVector, n)
	for _, a := range args[n:] {
		if sp, ok := a.(*ast.Spread); ok {
			c.expr(sp.X)
			c.mark(sp.Pos())
			c.emit(bytecode.OpExtend)
		} else {
			c.expr(a)
			c.emit(bytecode.OpAppend)
		}
	}
}

// boolValue compiles && or || to true or false.
func (c *compiler) boolValue(e ast.Expr) {
	falseJumps := c.branch(e, false)
	c.emit(bytecode.OpTrue)
	end := c.jump(bytecode.OpJump)
	c.fs.depth--
	c.patch(falseJumps, c.here())
	c.emit(bytecode.OpFalse)
	c.patch([]int{end}, c.here())
}

// branch emits code that jumps when the truth of e equals jumpIf and falls
// through otherwise. It returns the jumps to patch. && and || jump directly
// instead of building a boolean.
func (c *compiler) branch(e ast.Expr, jumpIf bool) []int {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		switch {
		case e.Op == token.ANDAND && !jumpIf, e.Op == token.OROR && jumpIf:
			// a && b jumps (false) if a or b is false; a || b jumps (true)
			// if a or b is true.
			return append(c.branch(e.X, jumpIf), c.branch(e.Y, jumpIf)...)
		case e.Op == token.ANDAND || e.Op == token.OROR:
			// a && b jumps if both are true; a || b jumps if both are false.
			skip := c.branch(e.X, !jumpIf)
			jumps := c.branch(e.Y, jumpIf)
			c.patch(skip, c.here())
			return jumps
		}
	case *ast.UnaryExpr:
		if e.Op == token.BANG {
			return c.branch(e.X, !jumpIf)
		}
	}
	if b, ok := e.(*ast.BinaryExpr); ok {
		if cmp, ok := cmpJumps[b.Op]; ok {
			// A comparison and the jump on its result in one instruction.
			c.expr(b.X)
			c.expr(b.Y)
			c.mark(b.OpPos)
			if jumpIf {
				cmp |= bytecode.CmpJumpIfTrue
			}
			return []int{c.emit(bytecode.OpCmpJump, cmp, 0) + 2}
		}
	}
	c.expr(e)
	if jumpIf {
		return []int{c.jump(bytecode.OpJumpIfTrue)}
	}
	return []int{c.jump(bytecode.OpJumpIfFalse)}
}

var cmpJumps = map[token.Kind]int{
	token.LT: bytecode.CmpLt, token.LE: bytecode.CmpLe, token.GT: bytecode.CmpGt,
	token.GE: bytecode.CmpGe, token.EQ: bytecode.CmpEq, token.NE: bytecode.CmpNe,
}

// lvKind is the kind of an assignment target.
type lvKind uint8

const (
	lvLocal lvKind = iota
	lvGlobal
	lvIndex     // x[i]: lvalue has pushed x and i
	lvMember    // p->x: lvalue has pushed p; index is the name constant
	lvThisField // a data member of this: index is its slot
	lvStatic    // a static data member: class and index
	lvCell      // a captured local: index is the slot of its cell
	lvUpvalue   // a variable of an enclosing function: index is the upvalue
)

// variable is a resolved assignment target.
type variable struct {
	kind  lvKind
	index int // slot, global index, field slot or name constant
	class int // lvStatic
}

// pushed reports whether lvalue pushed operands that store consumes.
func (v variable) pushed() bool { return v.kind == lvIndex || v.kind == lvMember }

// load pushes the target's current value (for x[i] keeping x and i).
func (v variable) load(c *compiler) {
	switch v.kind {
	case lvLocal:
		c.emit(bytecode.OpLoadLocal, v.index)
	case lvGlobal:
		c.emit(bytecode.OpLoadGlobal, v.index)
	case lvIndex:
		c.emit(bytecode.OpDup2)
		c.emit(bytecode.OpIndexGet)
	case lvMember:
		c.emit(bytecode.OpDup)
		c.emit(bytecode.OpGetMember, v.index, c.cache(token.Pos{}))
	case lvThisField:
		c.emit(bytecode.OpLoadMember, v.index)
	case lvStatic:
		c.emit(bytecode.OpLoadStatic, v.class, v.index)
	case lvCell:
		c.emit(bytecode.OpLoadCell, v.index)
	case lvUpvalue:
		c.emit(bytecode.OpLoadUpvalue, v.index)
	}
}

// store stores the value on top of the stack into the target. With keep the
// value stays on the stack as the value of the expression.
func (v variable) store(c *compiler, keep bool) {
	if v.pushed() {
		if v.kind == lvIndex {
			c.emit(bytecode.OpIndexSet) // leaves the value
		} else {
			c.emit(bytecode.OpSetMember, v.index, c.cache(token.Pos{})) // leaves the value
		}
		if !keep {
			c.emit(bytecode.OpPop)
		}
		return
	}
	if keep {
		c.emit(bytecode.OpDup)
	}
	switch v.kind {
	case lvLocal:
		c.emit(bytecode.OpStoreLocal, v.index)
	case lvGlobal:
		c.emit(bytecode.OpStoreGlobal, v.index)
	case lvThisField:
		c.emit(bytecode.OpStoreMember, v.index)
	case lvStatic:
		c.emit(bytecode.OpStoreStatic, v.class, v.index)
	case lvCell:
		c.emit(bytecode.OpStoreCell, v.index)
	case lvUpvalue:
		c.emit(bytecode.OpStoreUpvalue, v.index)
	}
}

// lookupLocal finds a local, parameter or temporary of the current
// function, innermost first.
func (c *compiler) lookupLocal(name string) (localVar, bool) {
	return c.fs.lookup(name)
}

func (fs *funcState) lookup(name string) (localVar, bool) {
	for i := len(fs.scopes) - 1; i >= 0; i-- {
		if lv, ok := fs.scopes[i][name]; ok {
			return lv, true
		}
	}
	return localVar{}, false
}

func (c *compiler) load(id *ast.Ident) {
	if lv, ok := c.lookupLocal(id.Name); ok {
		if lv.cell {
			c.emit(bytecode.OpLoadCell, lv.slot)
		} else {
			c.emit(bytecode.OpLoadLocal, lv.slot)
		}
		return
	}
	if up, ok := c.upvalue(c.fs, id.Name); ok {
		c.emit(bytecode.OpLoadUpvalue, up)
		return
	}
	if m := c.memberOf(id.Name); m != nil {
		c.loadMember(m, id.NamePos)
		return
	}
	g, ok := c.globals[id.Name]
	if !ok {
		c.errorf(id.NamePos, "undefined name %s", id.Name)
	}
	if g.kind == symConst {
		c.pushValue(g.value, id.NamePos)
		return
	}
	c.emit(bytecode.OpLoadGlobal, c.globalIndex(id.Name, g, id.NamePos))
}

// lvalue resolves the target of an assignment or ++/--. For x[i] it emits
// the code for x and i, which are evaluated once.
func (c *compiler) lvalue(e ast.Expr) variable {
	switch e := e.(type) {
	case *ast.Ident:
		if lv, ok := c.lookupLocal(e.Name); ok {
			if lv.cell {
				return variable{kind: lvCell, index: lv.slot}
			}
			return variable{kind: lvLocal, index: lv.slot}
		}
		if up, ok := c.upvalue(c.fs, e.Name); ok {
			return variable{kind: lvUpvalue, index: up}
		}
		if m := c.memberOf(e.Name); m != nil {
			return c.memberVariable(m, e.NamePos)
		}
		g, ok := c.globals[e.Name]
		if !ok {
			c.errorf(e.NamePos, "undefined name %s (declare globals with 'global')", e.Name)
		}
		switch g.kind {
		case symConst:
			c.errorf(e.NamePos, "cannot assign to constant %s", e.Name)
		case symFunc:
			c.errorf(e.NamePos, "cannot assign to function %s", e.Name)
		case symBuiltin:
			c.errorf(e.NamePos, "cannot assign to built-in function %s", e.Name)
		case symClass:
			c.errorf(e.NamePos, "cannot assign to class %s", e.Name)
		}
		return variable{kind: lvGlobal, index: g.index}
	case *ast.IndexExpr:
		c.expr(e.X)
		c.expr(e.Index)
		c.mark(e.Lbrack)
		return variable{kind: lvIndex}
	case *ast.MemberExpr:
		if f := c.thisField(e); f != nil {
			return variable{kind: lvThisField, index: f.field}
		}
		c.expr(e.X)
		c.mark(e.Name.NamePos)
		return variable{kind: lvMember, index: c.nameConst(e.Name)}
	case *ast.StaticRef:
		k := c.classNamed(e.Class)
		m := c.classMember(k, e.Name)
		if m.kind != memberStatic {
			c.errorf(e.Name.NamePos, "cannot assign to %s::%s: only static data members can be assigned this way", e.Class.Name, e.Name.Name)
		}
		return variable{kind: lvStatic, class: m.class.index, index: m.slot}
	}
	c.errorf(e.Pos(), "cannot assign to this expression")
	return variable{}
}

func (c *compiler) assign(e *ast.AssignExpr, keep bool) {
	v := c.lvalue(e.LHS)
	if e.Op == token.ASSIGN {
		c.expr(e.RHS)
	} else {
		v.load(c)
		c.expr(e.RHS)
		c.mark(e.LHS.Pos())
		c.emit(assignOps[e.Op])
	}
	v.store(c, keep)
}

func (c *compiler) incDec(e *ast.IncDecExpr, keep bool) {
	v := c.lvalue(e.X)
	op := bytecode.OpInc
	if e.Op == token.DEC {
		op = bytecode.OpDec
	}
	if v.kind == lvLocal && !keep { // i++ as a statement: one instruction
		c.mark(e.OpPos)
		if e.Op == token.DEC {
			c.emit(bytecode.OpDecLocal, v.index)
		} else {
			c.emit(bytecode.OpIncLocal, v.index)
		}
		return
	}
	v.load(c)
	c.mark(e.OpPos)
	switch {
	case e.Prefix || !keep:
		c.emit(op)
		v.store(c, keep)
	case v.pushed():
		// x[i]++ or p->x++ as a value: keep the old value in a hidden slot.
		tmp := c.tempSlot()
		c.emit(bytecode.OpDup)
		c.emit(bytecode.OpStoreLocal, tmp)
		c.emit(op)
		v.store(c, false)
		c.emit(bytecode.OpLoadLocal, tmp)
		c.freeTempSlot()
	default:
		c.emit(bytecode.OpDup)
		c.emit(op)
		v.store(c, false)
	}
}

// tempSlot reserves a nameless slot for use within one expression;
// freeTempSlot releases the most recent one.
func (c *compiler) tempSlot() int {
	fs := c.fs
	slot := fs.nslots
	fs.nslots++
	if slot == len(fs.fn.SlotNames) {
		fs.fn.SlotNames = append(fs.fn.SlotNames, "(temp)")
	}
	if fs.nslots > fs.fn.NumSlots {
		fs.fn.NumSlots = fs.nslots
	}
	return slot
}

func (c *compiler) freeTempSlot() { c.fs.nslots-- }

// ----------------------------------------------------------------------
// Constants

// evalConst evaluates a constant expression at compile time.
func (c *compiler) evalConst(e ast.Expr) value.Value {
	switch e := e.(type) {
	case *ast.IntLit:
		return value.MakeInt(e.Value)
	case *ast.CharLit:
		return value.MakeInt(e.Value)
	case *ast.FloatLit:
		return value.MakeFloat(e.Value)
	case *ast.StringLit:
		return value.MakeString(e.Value)
	case *ast.BoolLit:
		return value.MakeBool(e.Value)
	case *ast.NilLit:
		return value.NilValue
	case *ast.Ident:
		if g, ok := c.globals[e.Name]; ok && g.kind == symConst {
			return g.value
		}
		c.errorf(e.NamePos, "%s is not a constant defined earlier", e.Name)
	case *ast.UnaryExpr:
		x := c.evalConst(e.X)
		var v value.Value
		var err error
		switch e.Op {
		case token.MINUS:
			v, err = value.Neg(x)
		case token.BANG:
			v = value.MakeBool(!x.Truthy())
		case token.TILDE:
			v, err = value.BitNot(x)
		}
		c.constErr(e.OpPos, err)
		return v
	case *ast.BinaryExpr:
		x := c.evalConst(e.X)
		switch e.Op {
		case token.ANDAND:
			return value.MakeBool(x.Truthy() && c.evalConst(e.Y).Truthy())
		case token.OROR:
			return value.MakeBool(x.Truthy() || c.evalConst(e.Y).Truthy())
		}
		y := c.evalConst(e.Y)
		v, err := evalBinary(e.Op, x, y)
		c.constErr(e.OpPos, err)
		return v
	case *ast.CondExpr:
		if c.evalConst(e.Cond).Truthy() {
			return c.evalConst(e.Then)
		}
		return c.evalConst(e.Else)
	}
	c.errorf(e.Pos(), "not a constant expression")
	return value.NilValue
}

func (c *compiler) constErr(pos token.Pos, err error) {
	if err != nil {
		c.errorf(pos, "in constant expression: %v", err)
	}
}

var arithOps = map[token.Kind]value.ArithOp{
	token.PLUS: value.Add, token.MINUS: value.Sub, token.STAR: value.Mul, token.SLASH: value.Div,
	token.PERCENT: value.Mod, token.AMP: value.BitAnd, token.PIPE: value.BitOr, token.CARET: value.BitXor,
	token.SHL: value.Shl, token.SHR: value.Shr,
}

var cmpOps = map[token.Kind]value.CmpOp{
	token.LT: value.Less, token.LE: value.LessEq, token.GT: value.Greater, token.GE: value.GreaterEq,
}

func evalBinary(op token.Kind, x, y value.Value) (value.Value, error) {
	switch op {
	case token.EQ:
		return value.MakeBool(value.Equal(x, y)), nil
	case token.NE:
		return value.MakeBool(!value.Equal(x, y)), nil
	}
	if cop, ok := cmpOps[op]; ok {
		b, err := value.Compare(cop, x, y)
		return value.MakeBool(b), err
	}
	if aop, ok := arithOps[op]; ok {
		return value.Arith(aop, x, y)
	}
	return value.NilValue, fmt.Errorf("unknown operator %s", op)
}
