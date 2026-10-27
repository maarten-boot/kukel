package ast

// Inspect walks the tree rooted at n in depth-first order. It calls f for
// each node; when f returns false, the node's children are skipped.
func Inspect(n Node, f func(Node) bool) {
	if n == nil || !f(n) {
		return
	}
	walk := func(ns ...Node) {
		for _, c := range ns {
			if c != nil {
				Inspect(c, f)
			}
		}
	}
	exprs := func(xs []Expr) {
		for _, x := range xs {
			Inspect(x, f)
		}
	}
	params := func(pl *ParamList) {
		if pl == nil {
			return
		}
		for _, p := range pl.Params {
			walk(p.Name)
			if p.Default != nil {
				walk(p.Default)
			}
		}
	}
	vars := func(vs []*VarSpec) {
		for _, v := range vs {
			walk(v.Name)
			if v.Value != nil {
				walk(v.Value)
			}
		}
	}

	switch n := n.(type) {
	// Definitions
	case *UseDecl:
		walk(n.Name)
	case *ClassDecl:
		walk(n.Name)
		if n.BaseModule != nil {
			walk(n.BaseModule)
		}
		if n.Base != nil {
			walk(n.Base)
		}
		for _, m := range n.Members {
			walk(m)
		}
	case *MethodDecl:
		walk(n.Name)
		for _, p := range n.Params {
			walk(p)
		}
	case *FieldDecl:
		for _, id := range n.Names {
			walk(id)
		}
	case *FuncDecl:
		if n.Class != nil {
			walk(n.Class)
		}
		walk(n.Name)
		params(n.Params)
		walk(n.Body)
	case *GlobalDecl:
		vars(n.Vars)
	case *ConstDecl:
		vars(n.Vars)

	// Statements
	case *Block:
		for _, s := range n.Stmts {
			walk(s)
		}
	case *ExprStmt:
		walk(n.X)
	case *IfStmt:
		walk(n.Cond, n.Then)
		if n.Else != nil {
			walk(n.Else)
		}
	case *SwitchStmt:
		walk(n.Tag, n.Body)
	case *CaseLabel:
		walk(n.Value)
	case *WhileStmt:
		walk(n.Cond, n.Body)
	case *DoStmt:
		walk(n.Body, n.Cond)
	case *ForStmt:
		vars(n.InitVars)
		for _, x := range []Expr{n.Init, n.Cond, n.Post} {
			if x != nil {
				walk(x)
			}
		}
		walk(n.Body)
	case *ForEachStmt:
		walk(n.Key)
		if n.Value != nil {
			walk(n.Value)
		}
		walk(n.X, n.Body)
	case *BreakStmt:
		if n.Label != nil {
			walk(n.Label)
		}
	case *ContinueStmt:
		if n.Label != nil {
			walk(n.Label)
		}
	case *ReturnStmt:
		if n.Value != nil {
			walk(n.Value)
		}
	case *YieldStmt:
		if n.Value != nil {
			walk(n.Value)
		}
	case *LocalDecl:
		vars(n.Vars)
	case *TryStmt:
		walk(n.Body)
		if n.Catch != nil {
			walk(n.CatchVar, n.Catch)
		}
		if n.Finally != nil {
			walk(n.Finally)
		}
	case *WithStmt:
		vars(n.Vars)
		walk(n.Body)
	case *ThrowStmt:
		walk(n.Value)
	case *LabeledStmt:
		walk(n.Label, n.Stmt)

	// Expressions
	case *VectorLit:
		exprs(n.Elems)
	case *MapLit:
		for _, e := range n.Entries {
			walk(e)
		}
	case *MapEntry:
		walk(n.Key, n.Value)
	case *NewExpr:
		if n.Module != nil {
			walk(n.Module)
		}
		walk(n.Class)
		exprs(n.Args)
	case *FuncLit:
		params(n.Params)
		walk(n.Body)
	case *StaticRef:
		if n.Module != nil {
			walk(n.Module)
		}
		walk(n.Class, n.Name)
	case *Spread:
		walk(n.X)
	case *CommaExpr:
		exprs(n.List)
	case *AssignExpr:
		walk(n.LHS, n.RHS)
	case *CondExpr:
		walk(n.Cond, n.Then, n.Else)
	case *BinaryExpr:
		walk(n.X, n.Y)
	case *UnaryExpr:
		walk(n.X)
	case *IncDecExpr:
		walk(n.X)
	case *CallExpr:
		walk(n.Fun)
		exprs(n.Args)
	case *IndexExpr:
		walk(n.X, n.Index)
	case *MethodCall:
		walk(n.X, n.Name)
		exprs(n.Args)
	case *MemberExpr:
		walk(n.X, n.Name)
	}
}
