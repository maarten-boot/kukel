// Package ast declares the syntax tree of Kukel programs (syntax.ebnf,
// sections 1-3).
package ast

import "github.com/maarten-boot/kukel/internal/token"

// Node is any node of the tree. Pos is the position of its first token.
type Node interface {
	Pos() token.Pos
}

// Decl is a top-level definition.
type Decl interface {
	Node
	declNode()
}

// Stmt is a statement.
type Stmt interface {
	Node
	stmtNode()
}

// Expr is an expression.
type Expr interface {
	Node
	exprNode()
}

// File is one parsed source file.
type File struct {
	Name   string
	Module string // the module the file is, "" for the program's own files (set by the loader)
	Decls  []Decl // the use declarations come first
}

// ----------------------------------------------------------------------
// Definitions

// UseDecl is "use name;": the file uses module name.
type UseDecl struct {
	Use  token.Pos
	Name *Ident
}

// ClassDecl is "class Name [: [module::]Base] { members }".
type ClassDecl struct {
	Class      token.Pos
	Name       *Ident
	BaseModule *Ident // the module of the base class, or nil
	Base       *Ident // nil without a base class
	Members    []Member
}

// Member is a member declaration inside a class: *MethodDecl or *FieldDecl.
type Member interface {
	Node
	memberNode()
}

// MethodDecl declares a (static) member function: "[static] name(a, b..);".
// The parameter names are documentation only.
type MethodDecl struct {
	Start  token.Pos
	Static bool
	Name   *Ident
	Params []*Ident
	Rest   bool // the last parameter is a rest parameter
}

// FieldDecl declares (static) data members: "[static] a, b;".
type FieldDecl struct {
	Start  token.Pos
	Static bool
	Names  []*Ident
}

// FuncDecl is a function definition "name(params) { ... }" or, with Class
// set, a member function definition "Class::name(params) { ... }".
type FuncDecl struct {
	Class  *Ident // nil for a plain function
	Name   *Ident
	Params *ParamList
	Body   *Block
}

// GlobalDecl is "global a, b = 1;".
type GlobalDecl struct {
	Global token.Pos
	Vars   []*VarSpec
}

// ConstDecl is "const A = 1, B = A + 1;". Every Value is set.
type ConstDecl struct {
	Const token.Pos
	Vars  []*VarSpec
}

// VarSpec is "name [= value]" in global, const and local declarations.
type VarSpec struct {
	Name  *Ident
	Value Expr // nil without an initializer
}

// ParamList is the parameter list of a function definition or function
// literal.
type ParamList struct {
	Params []*Param
}

// Param is a parameter: "a", "a = default" or "a..".
type Param struct {
	Name    *Ident
	Default Expr // nil for a required or rest parameter
	Rest    bool
}

// ----------------------------------------------------------------------
// Statements

// Block is "{ stmts }".
type Block struct {
	Lbrace token.Pos
	Stmts  []Stmt
	Rbrace token.Pos
}

// ExprStmt is "expr;".
type ExprStmt struct {
	X Expr
}

// EmptyStmt is ";".
type EmptyStmt struct {
	Semi token.Pos
}

// IfStmt is "if (cond) then [else else]". "elseif (c) s" is parsed as
// "else if (c) s": Else is then an *IfStmt.
type IfStmt struct {
	If   token.Pos
	Cond Expr
	Then Stmt
	Else Stmt // nil without else
}

// SwitchStmt is "switch (tag) body". Case and default labels are statements
// somewhere inside Body.
type SwitchStmt struct {
	Switch token.Pos
	Tag    Expr
	Body   *Block
}

// CaseLabel is "case value:".
type CaseLabel struct {
	Case  token.Pos
	Value Expr
}

// DefaultLabel is "default:".
type DefaultLabel struct {
	Default token.Pos
}

// WhileStmt is "while (cond) body".
type WhileStmt struct {
	While token.Pos
	Cond  Expr
	Body  Stmt
}

// DoStmt is "do body while (cond);".
type DoStmt struct {
	Do   token.Pos
	Body Stmt
	Cond Expr
}

// ForStmt is "for (init; cond; post) body". The initializer is either a
// local list (InitVars) or an expression (Init); both may be absent.
type ForStmt struct {
	For      token.Pos
	InitVars []*VarSpec // "for (local i = 0, j; ...)"
	Init     Expr
	Cond     Expr // nil: loop until break
	Post     Expr
	Body     Stmt
}

// ForEachStmt is "for (local key[, value] in x) body".
type ForEachStmt struct {
	For   token.Pos
	Key   *Ident // the only name with one name
	Value *Ident // nil with one name
	X     Expr
	Body  Stmt
}

// BreakStmt is "break [label];".
type BreakStmt struct {
	Break token.Pos
	Label *Ident // nil without a label
}

// ContinueStmt is "continue [label];".
type ContinueStmt struct {
	Continue token.Pos
	Label    *Ident
}

// ReturnStmt is "return [value];".
type ReturnStmt struct {
	Return token.Pos
	Value  Expr // nil: return nil
}

// LocalDecl is "local a, b = 1;".
type LocalDecl struct {
	Local token.Pos
	Vars  []*VarSpec
}

// TryStmt is "try body [catch (v) catch] [finally finally]"; at least one of
// Catch and Finally is set.
type TryStmt struct {
	Try      token.Pos
	Body     *Block
	CatchVar *Ident
	Catch    *Block
	Finally  *Block
}

// ThrowStmt is "throw value;".
type ThrowStmt struct {
	Throw token.Pos
	Value Expr
}

// LabeledStmt is "label: stmt".
type LabeledStmt struct {
	Label *Ident
	Stmt  Stmt
}

// ----------------------------------------------------------------------
// Expressions

// Ident is a name.
type Ident struct {
	NamePos token.Pos
	Name    string
}

// IntLit is an integer literal. A "-" directly before a literal is folded
// into it, so Value may be negative; Text is then "-" plus the literal.
type IntLit struct {
	ValuePos token.Pos
	Text     string
	Value    int64
}

// FloatLit is a floating-point literal (possibly folded with "-").
type FloatLit struct {
	ValuePos token.Pos
	Text     string
	Value    float64
}

// CharLit is a character literal; Value is its code (negative when folded
// with "-").
type CharLit struct {
	ValuePos token.Pos
	Text     string
	Value    int64
}

// StringLit is a string literal; Value holds the decoded bytes.
type StringLit struct {
	ValuePos token.Pos
	Text     string
	Value    string
}

// BoolLit is "true" or "false".
type BoolLit struct {
	ValuePos token.Pos
	Value    bool
}

// NilLit is "nil".
type NilLit struct {
	ValuePos token.Pos
}

// ThisExpr is "this".
type ThisExpr struct {
	This token.Pos
}

// VectorLit is "[a, b, c..]".
type VectorLit struct {
	Lbrack token.Pos
	Elems  []Expr // may contain *Spread
}

// MapLit is "{k: v, ...}".
type MapLit struct {
	Lbrace  token.Pos
	Entries []*MapEntry
}

// MapEntry is "key: value" in a map literal.
type MapEntry struct {
	Key   Expr
	Value Expr
}

// NewExpr is "new [module::]Class(args)".
type NewExpr struct {
	New    token.Pos
	Module *Ident // nil without a module
	Class  *Ident
	Args   []Expr // may contain *Spread
}

// FuncLit is "fn (params) { ... }".
type FuncLit struct {
	Fn     token.Pos
	Params *ParamList
	Body   *Block
}

// StaticRef is "Class::name" (a static member or static function),
// "module::name" (a top-level name of a module; the compiler tells the two
// apart) or "module::Class::name".
type StaticRef struct {
	Module *Ident // set only for module::Class::name
	Class  *Ident
	Name   *Ident
}

// Spread is "x.." in an argument list or vector literal.
type Spread struct {
	X Expr
}

// CommaExpr is "a, b, c": evaluated left to right, value of the last. It
// occurs only as the first and last part of a for loop.
type CommaExpr struct {
	List []Expr
}

// AssignExpr is "lhs op rhs" with op one of = += -= *= /= %= &= |= ^= <<= >>=.
type AssignExpr struct {
	Op  token.Kind
	LHS Expr
	RHS Expr
}

// CondExpr is "cond ? then : else".
type CondExpr struct {
	Cond Expr
	Then Expr
	Else Expr
}

// BinaryExpr is "x op y".
type BinaryExpr struct {
	Op    token.Kind
	OpPos token.Pos
	X     Expr
	Y     Expr
}

// UnaryExpr is "op x" with op one of - ! ~.
type UnaryExpr struct {
	Op    token.Kind
	OpPos token.Pos
	X     Expr
}

// IncDecExpr is "++x", "--x", "x++" or "x--".
type IncDecExpr struct {
	Op     token.Kind // token.INC or token.DEC
	OpPos  token.Pos
	Prefix bool
	X      Expr
}

// CallExpr is "fun(args)".
type CallExpr struct {
	Fun    Expr
	Lparen token.Pos
	Args   []Expr // may contain *Spread
}

// IndexExpr is "x[index]".
type IndexExpr struct {
	X      Expr
	Lbrack token.Pos
	Index  Expr
}

// MethodCall is "x->name(args)".
type MethodCall struct {
	X    Expr
	Name *Ident
	Args []Expr // may contain *Spread
}

// MemberExpr is "x->name" without a call: a data member or bound method.
type MemberExpr struct {
	X    Expr
	Name *Ident
}

// ----------------------------------------------------------------------
// Positions and marker methods

func (d *UseDecl) Pos() token.Pos   { return d.Use }
func (d *ClassDecl) Pos() token.Pos { return d.Class }
func (d *FuncDecl) Pos() token.Pos {
	if d.Class != nil {
		return d.Class.NamePos
	}
	return d.Name.NamePos
}
func (d *GlobalDecl) Pos() token.Pos { return d.Global }
func (d *ConstDecl) Pos() token.Pos  { return d.Const }
func (m *MethodDecl) Pos() token.Pos { return m.Start }
func (m *FieldDecl) Pos() token.Pos  { return m.Start }
func (v *VarSpec) Pos() token.Pos    { return v.Name.NamePos }
func (p *Param) Pos() token.Pos      { return p.Name.NamePos }

func (s *Block) Pos() token.Pos        { return s.Lbrace }
func (s *ExprStmt) Pos() token.Pos     { return s.X.Pos() }
func (s *EmptyStmt) Pos() token.Pos    { return s.Semi }
func (s *IfStmt) Pos() token.Pos       { return s.If }
func (s *SwitchStmt) Pos() token.Pos   { return s.Switch }
func (s *CaseLabel) Pos() token.Pos    { return s.Case }
func (s *DefaultLabel) Pos() token.Pos { return s.Default }
func (s *WhileStmt) Pos() token.Pos    { return s.While }
func (s *DoStmt) Pos() token.Pos       { return s.Do }
func (s *ForStmt) Pos() token.Pos      { return s.For }
func (s *ForEachStmt) Pos() token.Pos  { return s.For }
func (s *BreakStmt) Pos() token.Pos    { return s.Break }
func (s *ContinueStmt) Pos() token.Pos { return s.Continue }
func (s *ReturnStmt) Pos() token.Pos   { return s.Return }
func (s *LocalDecl) Pos() token.Pos    { return s.Local }
func (s *TryStmt) Pos() token.Pos      { return s.Try }
func (s *ThrowStmt) Pos() token.Pos    { return s.Throw }
func (s *LabeledStmt) Pos() token.Pos  { return s.Label.NamePos }

func (x *Ident) Pos() token.Pos     { return x.NamePos }
func (x *IntLit) Pos() token.Pos    { return x.ValuePos }
func (x *FloatLit) Pos() token.Pos  { return x.ValuePos }
func (x *CharLit) Pos() token.Pos   { return x.ValuePos }
func (x *StringLit) Pos() token.Pos { return x.ValuePos }
func (x *BoolLit) Pos() token.Pos   { return x.ValuePos }
func (x *NilLit) Pos() token.Pos    { return x.ValuePos }
func (x *ThisExpr) Pos() token.Pos  { return x.This }
func (x *VectorLit) Pos() token.Pos { return x.Lbrack }
func (x *MapLit) Pos() token.Pos    { return x.Lbrace }
func (x *MapEntry) Pos() token.Pos  { return x.Key.Pos() }
func (x *NewExpr) Pos() token.Pos   { return x.New }
func (x *FuncLit) Pos() token.Pos   { return x.Fn }
func (x *StaticRef) Pos() token.Pos {
	if x.Module != nil {
		return x.Module.NamePos
	}
	return x.Class.NamePos
}
func (x *Spread) Pos() token.Pos     { return x.X.Pos() }
func (x *CommaExpr) Pos() token.Pos  { return x.List[0].Pos() }
func (x *AssignExpr) Pos() token.Pos { return x.LHS.Pos() }
func (x *CondExpr) Pos() token.Pos   { return x.Cond.Pos() }
func (x *BinaryExpr) Pos() token.Pos { return x.X.Pos() }
func (x *UnaryExpr) Pos() token.Pos  { return x.OpPos }
func (x *IncDecExpr) Pos() token.Pos {
	if x.Prefix {
		return x.OpPos
	}
	return x.X.Pos()
}
func (x *CallExpr) Pos() token.Pos   { return x.Fun.Pos() }
func (x *IndexExpr) Pos() token.Pos  { return x.X.Pos() }
func (x *MethodCall) Pos() token.Pos { return x.X.Pos() }
func (x *MemberExpr) Pos() token.Pos { return x.X.Pos() }

func (*UseDecl) declNode()    {}
func (*ClassDecl) declNode()  {}
func (*FuncDecl) declNode()   {}
func (*GlobalDecl) declNode() {}
func (*ConstDecl) declNode()  {}

func (*MethodDecl) memberNode() {}
func (*FieldDecl) memberNode()  {}

func (*Block) stmtNode()        {}
func (*ExprStmt) stmtNode()     {}
func (*EmptyStmt) stmtNode()    {}
func (*IfStmt) stmtNode()       {}
func (*SwitchStmt) stmtNode()   {}
func (*CaseLabel) stmtNode()    {}
func (*DefaultLabel) stmtNode() {}
func (*WhileStmt) stmtNode()    {}
func (*DoStmt) stmtNode()       {}
func (*ForStmt) stmtNode()      {}
func (*ForEachStmt) stmtNode()  {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
func (*ReturnStmt) stmtNode()   {}
func (*LocalDecl) stmtNode()    {}
func (*TryStmt) stmtNode()      {}
func (*ThrowStmt) stmtNode()    {}
func (*LabeledStmt) stmtNode()  {}

func (*Ident) exprNode()      {}
func (*IntLit) exprNode()     {}
func (*FloatLit) exprNode()   {}
func (*CharLit) exprNode()    {}
func (*StringLit) exprNode()  {}
func (*BoolLit) exprNode()    {}
func (*NilLit) exprNode()     {}
func (*ThisExpr) exprNode()   {}
func (*VectorLit) exprNode()  {}
func (*MapLit) exprNode()     {}
func (*NewExpr) exprNode()    {}
func (*FuncLit) exprNode()    {}
func (*StaticRef) exprNode()  {}
func (*Spread) exprNode()     {}
func (*CommaExpr) exprNode()  {}
func (*AssignExpr) exprNode() {}
func (*CondExpr) exprNode()   {}
func (*BinaryExpr) exprNode() {}
func (*UnaryExpr) exprNode()  {}
func (*IncDecExpr) exprNode() {}
func (*CallExpr) exprNode()   {}
func (*IndexExpr) exprNode()  {}
func (*MethodCall) exprNode() {}
func (*MemberExpr) exprNode() {}
