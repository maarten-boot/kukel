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

	"kukel/internal/ast"
	"kukel/internal/bytecode"
	"kukel/internal/diag"
	"kukel/internal/token"
	"kukel/internal/value"
)

const maxIndex = math.MaxUint16 // constants, slots, globals, arguments

type symKind uint8

const (
	symVar symKind = iota
	symFunc
	symBuiltin
	symConst
)

// global is a top-level name.
type global struct {
	kind  symKind
	index int         // global slot; -1 for a built-in not yet used
	value value.Value // symConst
	pos   token.Pos   // definition (zero for built-ins)
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

	// Pass 1: define all top-level names, evaluate constants in order.
	var funcs []*ast.FuncDecl
	var globals []*ast.VarSpec
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Class != nil {
					c.notYet(d.Pos(), "member functions are", 6)
				}
				g := c.define(d.Name, symFunc)
				c.prog.Globals[g.index].Func = len(funcs)
				funcs = append(funcs, d)
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
				c.notYet(d.Pos(), "classes are", 6)
			}
		}
	}

	// The entry point.
	pos := token.Pos{Line: 1, Col: 1}
	if len(files) > 0 {
		pos.File = files[0].Name
	}
	m, ok := c.globals["main"]
	if !ok || m.kind != symFunc {
		c.errorf(pos, "the program has no main function")
	}
	if fd := funcs[c.prog.Globals[m.index].Func]; len(fd.Params.Params) > 1 {
		c.errorf(fd.Name.NamePos, "main takes at most one parameter (the command-line arguments)")
	}
	c.prog.Main = m.index

	// Pass 2: the global initializers, then the functions.
	c.prog.Init = c.function("<init>", pos, &ast.ParamList{}, func() {
		for _, v := range globals {
			if v.Value != nil {
				c.mark(v.Pos())
				c.expr(v.Value)
				c.emit(bytecode.OpStoreGlobal, c.globals[v.Name.Name].index)
			}
		}
	})
	for _, fd := range funcs {
		fd := fd
		c.prog.Funcs = append(c.prog.Funcs, c.function(fd.Name.Name, fd.Name.NamePos, fd.Params, func() {
			c.block(fd.Body)
		}))
	}
	return c.prog, nil
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
	scopes   []map[string]int // innermost last; scopes[0] holds params and temporaries
	nslots   int              // slots in use
	depth    int              // operand stack depth at the current instruction
	maxDepth int
	loops    []*loop
	line     int // line of the code being emitted
}

// loop collects the jumps of break and continue statements to patch.
type loop struct {
	breaks    []int
	continues []int
}

// function compiles one function; body emits the code of its body.
func (c *compiler) function(name string, pos token.Pos, params *ast.ParamList, body func()) *bytecode.Function {
	outer := c.fs
	fs := &funcState{
		fn:     &bytecode.Function{FuncName: name, File: pos.File, Line: pos.Line},
		scopes: []map[string]int{{}},
		line:   pos.Line,
	}
	c.fs = fs
	defer func() { c.fs = outer }()

	for _, p := range params.Params {
		if p.Default != nil {
			c.notYet(p.Pos(), "default parameter values are", 4)
		}
		if p.Rest {
			c.notYet(p.Pos(), "rest parameters are", 4)
		}
		c.declare(p.Name, "parameter")
	}
	for _, t := range params.Temps {
		c.declare(t, "temporary")
	}
	fs.fn.NumParams = len(params.Params)

	body()
	c.emit(bytecode.OpReturnNil)

	fs.fn.MaxStack = fs.maxDepth
	return fs.fn
}

// declare adds a name to the innermost scope and gives it a slot.
func (c *compiler) declare(id *ast.Ident, what string) int {
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
	scope[id.Name] = slot
	if slot == len(fs.fn.SlotNames) {
		fs.fn.SlotNames = append(fs.fn.SlotNames, id.Name)
	} else {
		fs.fn.SlotNames[slot] += "/" + id.Name // slot reused by a later block
	}
	if fs.nslots > fs.fn.NumSlots {
		fs.fn.NumSlots = fs.nslots
	}
	return slot
}

func (c *compiler) pushScope() { c.fs.scopes = append(c.fs.scopes, map[string]int{}) }

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
func stackEffect(op bytecode.Op, operand int) int {
	switch op {
	case bytecode.OpConst, bytecode.OpNil, bytecode.OpTrue, bytecode.OpFalse, bytecode.OpDup,
		bytecode.OpLoadLocal, bytecode.OpLoadGlobal:
		return 1
	case bytecode.OpPop, bytecode.OpStoreLocal, bytecode.OpStoreGlobal,
		bytecode.OpAdd, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod,
		bytecode.OpBitAnd, bytecode.OpBitOr, bytecode.OpBitXor, bytecode.OpShl, bytecode.OpShr,
		bytecode.OpEq, bytecode.OpNe, bytecode.OpLt, bytecode.OpLe, bytecode.OpGt, bytecode.OpGe,
		bytecode.OpJumpIfFalse, bytecode.OpJumpIfTrue, bytecode.OpReturn:
		return -1
	case bytecode.OpCall:
		return -operand
	}
	return 0
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
	operand := 0
	if len(operands) > 0 {
		operand = operands[0]
	}
	fs.depth += stackEffect(op, operand)
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
	fn := c.fs.fn
	for i, k := range fn.Consts {
		same := k.Kind() == v.Kind() && value.Equal(k, v)
		if v.Kind() == value.Float { // keep 0.0 and -0.0 apart
			same = k.Kind() == value.Float && math.Float64bits(k.AsFloat()) == math.Float64bits(v.AsFloat())
		}
		if same {
			c.emit(bytecode.OpConst, i)
			return
		}
	}
	if len(fn.Consts) > maxIndex {
		c.errorf(pos, "too many constants in one function (more than %d)", maxIndex+1)
	}
	fn.Consts = append(fn.Consts, v)
	c.emit(bytecode.OpConst, len(fn.Consts)-1)
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
		c.emit(bytecode.OpStoreLocal, c.declare(v.Name, "local"))
	}
}

func (c *compiler) stmt(s ast.Stmt) {
	c.mark(s.Pos())
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
	case *ast.WhileStmt:
		top := c.here()
		exit := c.branch(s.Cond, false)
		l := c.pushLoop()
		c.stmt(s.Body)
		c.patch(l.continues, top)
		c.emit(bytecode.OpJump, top)
		c.popLoop()
		c.patch(append(exit, l.breaks...), c.here())
	case *ast.DoStmt:
		top := c.here()
		l := c.pushLoop()
		c.stmt(s.Body)
		c.popLoop()
		c.patch(l.continues, c.here())
		c.mark(s.Cond.Pos())
		c.patch(c.branch(s.Cond, true), top)
		c.patch(l.breaks, c.here())
	case *ast.ForStmt:
		c.forStmt(s)
	case *ast.BreakStmt:
		c.breakContinue(s.Pos(), s.Label, true)
	case *ast.ContinueStmt:
		c.breakContinue(s.Pos(), s.Label, false)
	case *ast.ReturnStmt:
		if s.Value != nil {
			c.expr(s.Value)
			c.emit(bytecode.OpReturn)
		} else {
			c.emit(bytecode.OpReturnNil)
		}
	case *ast.ForEachStmt:
		c.notYet(s.Pos(), "for-each loops are", 5)
	case *ast.SwitchStmt:
		c.notYet(s.Pos(), "switch statements are", 4)
	case *ast.CaseLabel, *ast.DefaultLabel:
		c.errorf(s.Pos(), "case and default labels are only allowed inside a switch body")
	case *ast.LabeledStmt:
		c.notYet(s.Pos(), "labelled statements are", 4)
	case *ast.TryStmt:
		c.notYet(s.Pos(), "try statements are", 7)
	case *ast.ThrowStmt:
		c.notYet(s.Pos(), "throw statements are", 7)
	default:
		c.errorf(s.Pos(), "internal error: unknown statement %T", s)
	}
	if c.fs.depth != 0 {
		c.errorf(s.Pos(), "internal error: operand stack depth %d after statement", c.fs.depth)
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

func (c *compiler) forStmt(s *ast.ForStmt) {
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
	l := c.pushLoop()
	c.stmt(s.Body)
	c.popLoop()
	c.patch(l.continues, c.here())
	if s.Post != nil {
		c.mark(s.Post.Pos())
		c.exprDiscard(s.Post)
	}
	c.emit(bytecode.OpJump, top)
	c.patch(append(exit, l.breaks...), c.here())
	c.popScope()
}

func (c *compiler) pushLoop() *loop {
	l := &loop{}
	c.fs.loops = append(c.fs.loops, l)
	return l
}

func (c *compiler) popLoop() { c.fs.loops = c.fs.loops[:len(c.fs.loops)-1] }

func (c *compiler) breakContinue(pos token.Pos, label *ast.Ident, isBreak bool) {
	if label != nil {
		c.notYet(label.NamePos, "labelled break and continue are", 4)
	}
	if len(c.fs.loops) == 0 {
		if isBreak {
			c.errorf(pos, "break outside a loop or switch")
		}
		c.errorf(pos, "continue outside a loop")
	}
	l := c.fs.loops[len(c.fs.loops)-1]
	at := c.jump(bytecode.OpJump)
	if isBreak {
		l.breaks = append(l.breaks, at)
	} else {
		l.continues = append(l.continues, at)
	}
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
		c.expr(e.Fun)
		if len(e.Args) > maxIndex {
			c.errorf(e.Lparen, "too many arguments (more than %d)", maxIndex)
		}
		for _, a := range e.Args {
			if sp, ok := a.(*ast.Spread); ok {
				c.notYet(sp.Pos(), "spread arguments are", 4)
			}
			c.expr(a)
		}
		c.mark(e.Lparen)
		c.emit(bytecode.OpCall, len(e.Args))
	case *ast.VectorLit:
		c.notYet(e.Pos(), "vector literals are", 5)
	case *ast.MapLit:
		c.notYet(e.Pos(), "map literals are", 5)
	case *ast.IndexExpr:
		c.notYet(e.Lbrack, "indexing is", 5)
	case *ast.NewExpr, *ast.ThisExpr, *ast.StaticRef, *ast.MethodCall, *ast.MemberExpr:
		c.notYet(e.Pos(), "classes and objects are", 6)
	case *ast.FuncLit:
		c.notYet(e.Pos(), "function literals are", 8)
	case *ast.Spread:
		c.errorf(e.Pos(), "a spread (x..) is only allowed in an argument list or vector literal")
	default:
		c.errorf(e.Pos(), "internal error: unknown expression %T", e)
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
	c.expr(e)
	if jumpIf {
		return []int{c.jump(bytecode.OpJumpIfTrue)}
	}
	return []int{c.jump(bytecode.OpJumpIfFalse)}
}

// variable is a resolved assignable name.
type variable struct {
	local bool
	index int
}

func (v variable) load(c *compiler) {
	if v.local {
		c.emit(bytecode.OpLoadLocal, v.index)
	} else {
		c.emit(bytecode.OpLoadGlobal, v.index)
	}
}

func (v variable) store(c *compiler) {
	if v.local {
		c.emit(bytecode.OpStoreLocal, v.index)
	} else {
		c.emit(bytecode.OpStoreGlobal, v.index)
	}
}

// lookupLocal finds a local, parameter or temporary, innermost first.
func (c *compiler) lookupLocal(name string) (int, bool) {
	for i := len(c.fs.scopes) - 1; i >= 0; i-- {
		if slot, ok := c.fs.scopes[i][name]; ok {
			return slot, true
		}
	}
	return 0, false
}

func (c *compiler) load(id *ast.Ident) {
	if slot, ok := c.lookupLocal(id.Name); ok {
		c.emit(bytecode.OpLoadLocal, slot)
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

// lvalue resolves the target of an assignment or ++/--.
func (c *compiler) lvalue(e ast.Expr) variable {
	switch e := e.(type) {
	case *ast.Ident:
		if slot, ok := c.lookupLocal(e.Name); ok {
			return variable{local: true, index: slot}
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
		}
		return variable{index: g.index}
	case *ast.IndexExpr:
		c.notYet(e.Lbrack, "indexing is", 5)
	case *ast.MemberExpr, *ast.StaticRef:
		c.notYet(e.Pos(), "classes and objects are", 6)
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
	if keep {
		c.emit(bytecode.OpDup)
	}
	v.store(c)
}

func (c *compiler) incDec(e *ast.IncDecExpr, keep bool) {
	v := c.lvalue(e.X)
	op := bytecode.OpInc
	if e.Op == token.DEC {
		op = bytecode.OpDec
	}
	v.load(c)
	c.mark(e.OpPos)
	if e.Prefix {
		c.emit(op)
		if keep {
			c.emit(bytecode.OpDup)
		}
	} else {
		if keep {
			c.emit(bytecode.OpDup)
		}
		c.emit(op)
	}
	v.store(c)
}

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
