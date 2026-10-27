package ast

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/maarten-boot/kukel/internal/token"
)

// Fprint writes n as an indented tree, one node per line. With withPos each
// line ends with the node's position.
func Fprint(w io.Writer, n Node, withPos bool) error {
	p := &printer{w: w, withPos: withPos}
	p.node(n)
	return p.err
}

// FprintFile writes a whole file as a tree (see Fprint).
func FprintFile(w io.Writer, f *File, withPos bool) error {
	p := &printer{w: w, withPos: withPos}
	p.line(nil, "File %s", f.Name)
	p.indent++
	for _, d := range f.Decls {
		p.node(d)
	}
	return p.err
}

type printer struct {
	w       io.Writer
	withPos bool
	indent  int
	err     error
}

func (p *printer) line(n Node, format string, args ...any) {
	if p.err != nil {
		return
	}
	s := strings.Repeat("  ", p.indent) + fmt.Sprintf(format, args...)
	if p.withPos && n != nil {
		s += "  @" + posString(n.Pos())
	}
	_, p.err = fmt.Fprintln(p.w, s)
}

func posString(pos token.Pos) string { return fmt.Sprintf("%d:%d", pos.Line, pos.Col) }

// sub prints a labelled group ("Then", "Else", ...) holding one node.
func (p *printer) sub(label string, n Node) {
	p.line(nil, "%s", label)
	p.indent++
	p.node(n)
	p.indent--
}

func (p *printer) children(ns ...Node) {
	p.indent++
	for _, n := range ns {
		p.node(n)
	}
	p.indent--
}

func names(ids []*Ident) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = id.Name
	}
	return strings.Join(s, ", ")
}

// qualified is "module::name", or "name" without a module.
func qualified(module, name *Ident) string {
	if module != nil {
		return module.Name + "::" + name.Name
	}
	return name.Name
}

func static(b bool) string {
	if b {
		return "static "
	}
	return ""
}

func (p *printer) params(pl *ParamList) {
	for _, prm := range pl.Params {
		switch {
		case prm.Rest:
			p.line(prm, "Param %s..", prm.Name.Name)
		case prm.Default != nil:
			p.line(prm, "Param %s =", prm.Name.Name)
			p.children(prm.Default)
		default:
			p.line(prm, "Param %s", prm.Name.Name)
		}
	}
}

func (p *printer) vars(vs []*VarSpec) {
	p.indent++
	for _, v := range vs {
		p.line(v, "Var %s", v.Name.Name)
		if v.Value != nil {
			p.children(v.Value)
		}
	}
	p.indent--
}

func exprs[T Node](xs []T) []Node {
	ns := make([]Node, len(xs))
	for i, x := range xs {
		ns[i] = x
	}
	return ns
}

func (p *printer) node(n Node) {
	switch n := n.(type) {
	// Definitions
	case *UseDecl:
		p.line(n, "Use %s", n.Name.Name)
	case *ClassDecl:
		if n.Base != nil {
			p.line(n, "Class %s : %s", n.Name.Name, qualified(n.BaseModule, n.Base))
		} else {
			p.line(n, "Class %s", n.Name.Name)
		}
		p.children(exprs(n.Members)...)
	case *MethodDecl:
		rest := ""
		if n.Rest {
			rest = ".."
		}
		p.line(n, "Method %s%s(%s%s)", static(n.Static), n.Name.Name, names(n.Params), rest)
	case *FieldDecl:
		p.line(n, "Data %s%s", static(n.Static), names(n.Names))
	case *FuncDecl:
		if n.Class != nil {
			p.line(n, "Func %s::%s", n.Class.Name, n.Name.Name)
		} else {
			p.line(n, "Func %s", n.Name.Name)
		}
		p.indent++
		p.params(n.Params)
		p.node(n.Body)
		p.indent--
	case *GlobalDecl:
		p.line(n, "Global")
		p.vars(n.Vars)
	case *ConstDecl:
		p.line(n, "Const")
		p.vars(n.Vars)

	// Statements
	case *Block:
		p.line(n, "Block")
		p.children(exprs(n.Stmts)...)
	case *ExprStmt:
		p.line(n, "ExprStmt")
		p.children(n.X)
	case *EmptyStmt:
		p.line(n, "Empty")
	case *IfStmt:
		p.line(n, "If")
		p.indent++
		p.node(n.Cond)
		p.sub("Then", n.Then)
		if n.Else != nil {
			p.sub("Else", n.Else)
		}
		p.indent--
	case *SwitchStmt:
		p.line(n, "Switch")
		p.children(n.Tag, n.Body)
	case *CaseLabel:
		p.line(n, "Case")
		p.children(n.Value)
	case *DefaultLabel:
		p.line(n, "Default")
	case *WhileStmt:
		p.line(n, "While")
		p.children(n.Cond, n.Body)
	case *DoStmt:
		p.line(n, "Do")
		p.children(n.Body, n.Cond)
	case *ForStmt:
		p.line(n, "For")
		p.indent++
		if n.InitVars != nil {
			p.line(nil, "Init Local")
			p.vars(n.InitVars)
		} else if n.Init != nil {
			p.sub("Init", n.Init)
		}
		if n.Cond != nil {
			p.sub("Cond", n.Cond)
		}
		if n.Post != nil {
			p.sub("Post", n.Post)
		}
		p.sub("Body", n.Body)
		p.indent--
	case *ForEachStmt:
		if n.Value != nil {
			p.line(n, "ForEach %s, %s", n.Key.Name, n.Value.Name)
		} else {
			p.line(n, "ForEach %s", n.Key.Name)
		}
		p.children(n.X, n.Body)
	case *BreakStmt:
		if n.Label != nil {
			p.line(n, "Break %s", n.Label.Name)
		} else {
			p.line(n, "Break")
		}
	case *ContinueStmt:
		if n.Label != nil {
			p.line(n, "Continue %s", n.Label.Name)
		} else {
			p.line(n, "Continue")
		}
	case *ReturnStmt:
		p.line(n, "Return")
		if n.Value != nil {
			p.children(n.Value)
		}
	case *YieldStmt:
		p.line(n, "Yield")
		if n.Value != nil {
			p.children(n.Value)
		}
	case *LocalDecl:
		p.line(n, "Local")
		p.vars(n.Vars)
	case *TryStmt:
		p.line(n, "Try")
		p.indent++
		p.node(n.Body)
		if n.Catch != nil {
			p.sub("Catch "+n.CatchVar.Name, n.Catch)
		}
		if n.Finally != nil {
			p.sub("Finally", n.Finally)
		}
		p.indent--
	case *WithStmt:
		p.line(n, "With")
		p.vars(n.Vars)
		p.children(n.Body)
	case *ThrowStmt:
		p.line(n, "Throw")
		p.children(n.Value)
	case *LabeledStmt:
		p.line(n, "Label %s", n.Label.Name)
		p.children(n.Stmt)

	// Expressions
	case *Ident:
		p.line(n, "Name %s", n.Name)
	case *IntLit:
		p.line(n, "Int %s", literal(n.Text, strconv.FormatInt(n.Value, 10)))
	case *FloatLit:
		p.line(n, "Float %s", literal(n.Text, strconv.FormatFloat(n.Value, 'g', -1, 64)))
	case *CharLit:
		p.line(n, "Char %s = %d", n.Text, n.Value)
	case *StringLit:
		p.line(n, "String %s", strconv.Quote(n.Value))
	case *BoolLit:
		p.line(n, "Bool %t", n.Value)
	case *NilLit:
		p.line(n, "Nil")
	case *ThisExpr:
		p.line(n, "This")
	case *VectorLit:
		p.line(n, "Vector")
		p.children(exprs(n.Elems)...)
	case *MapLit:
		p.line(n, "Map")
		p.children(exprs(n.Entries)...)
	case *MapEntry:
		p.line(n, "Entry")
		p.children(n.Key, n.Value)
	case *NewExpr:
		p.line(n, "New %s", qualified(n.Module, n.Class))
		p.children(exprs(n.Args)...)
	case *FuncLit:
		p.line(n, "Fn")
		p.indent++
		p.params(n.Params)
		p.node(n.Body)
		p.indent--
	case *StaticRef:
		p.line(n, "Static %s::%s", qualified(n.Module, n.Class), n.Name.Name)
	case *Spread:
		p.line(n, "Spread")
		p.children(n.X)
	case *CommaExpr:
		p.line(n, "Comma")
		p.children(exprs(n.List)...)
	case *AssignExpr:
		p.line(n, "Assign %s", n.Op)
		p.children(n.LHS, n.RHS)
	case *CondExpr:
		p.line(n, "Cond")
		p.children(n.Cond, n.Then, n.Else)
	case *BinaryExpr:
		p.line(n, "Binary %s", n.Op)
		p.children(n.X, n.Y)
	case *UnaryExpr:
		p.line(n, "Unary %s", n.Op)
		p.children(n.X)
	case *IncDecExpr:
		if n.Prefix {
			p.line(n, "Prefix %s", n.Op)
		} else {
			p.line(n, "Postfix %s", n.Op)
		}
		p.children(n.X)
	case *CallExpr:
		p.line(n, "Call")
		p.children(append([]Node{n.Fun}, exprs(n.Args)...)...)
	case *IndexExpr:
		p.line(n, "Index")
		p.children(n.X, n.Index)
	case *MethodCall:
		p.line(n, "MethodCall %s", n.Name.Name)
		p.children(append([]Node{n.X}, exprs(n.Args)...)...)
	case *MemberExpr:
		p.line(n, "Member %s", n.Name.Name)
		p.children(n.X)
	default:
		p.line(nil, "?%T", n)
	}
}

// literal shows a number's source text, plus its value when they differ
// (0xFF = 255).
func literal(text, value string) string {
	if text == value {
		return text
	}
	return text + " = " + value
}
