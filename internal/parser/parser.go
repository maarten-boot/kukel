// Package parser builds the syntax tree of a Kukel source file
// (syntax.ebnf, sections 1-3) by recursive descent. It reports the first
// syntax error and stops. Semantic rules (the "Rule:" comments of the
// grammar) are checked later, by the compiler.
package parser

import (
	"strings"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/diag"
	"github.com/maarten-boot/kukel/internal/lexer"
	"github.com/maarten-boot/kukel/internal/token"
)

// ParseFile parses one source file.
func ParseFile(name string, src []byte) (f *ast.File, err error) {
	p, err := newParser(name, src)
	if err != nil {
		return nil, err
	}
	defer p.recover(&err)
	f = &ast.File{Name: name}
	for p.at(token.USE) {
		pos := p.tok.Pos
		p.next()
		id := p.ident("module name after 'use'")
		p.expect(token.SEMICOLON, "after use declaration")
		f.Decls = append(f.Decls, &ast.UseDecl{Use: pos, Name: id})
	}
	for !p.at(token.EOF) {
		f.Decls = append(f.Decls, p.parseDecl())
	}
	return f, nil
}

// ParseStmts parses a sequence of statements (what kukel repl reads).
func ParseStmts(name string, src []byte) (stmts []ast.Stmt, err error) {
	p, err := newParser(name, src)
	if err != nil {
		return nil, err
	}
	defer p.recover(&err)
	for !p.at(token.EOF) {
		stmts = append(stmts, p.parseStmt())
	}
	return stmts, nil
}

// ParseExpr parses a single expression (used by tests and tools).
func ParseExpr(src string) (x ast.Expr, err error) {
	p, err := newParser("", []byte(src))
	if err != nil {
		return nil, err
	}
	defer p.recover(&err)
	x = p.parseExpr()
	if !p.at(token.EOF) {
		p.errorExpected("end of expression")
	}
	return x, nil
}

type parser struct {
	toks  []token.Token // ends with EOF
	i     int           // index of the current token
	tok   token.Token   // the current token, toks[i]
	depth int           // nesting of statements and unary expressions
}

// maxDepth bounds the nesting of statements and expressions, so that absurd
// input gives an error instead of exhausting the stack.
const maxDepth = 1000

func (p *parser) enter() {
	p.depth++
	if p.depth > maxDepth {
		p.errorf(p.tok.Pos, "nesting too deep (more than %d levels)", maxDepth)
	}
}

func (p *parser) leave() { p.depth-- }

// bailout carries a syntax error up to recover.
type bailout struct{ err *diag.Error }

func newParser(name string, src []byte) (*parser, error) {
	toks, err := lexer.Tokenize(name, src)
	if err != nil {
		return nil, err
	}
	return &parser{toks: toks, tok: toks[0]}, nil
}

func (p *parser) recover(err *error) {
	if r := recover(); r != nil {
		b, ok := r.(bailout)
		if !ok {
			panic(r)
		}
		*err = b.err
	}
}

// ----------------------------------------------------------------------
// Token helpers

func (p *parser) next() {
	if p.i < len(p.toks)-1 {
		p.i++
		p.tok = p.toks[p.i]
	}
}

// peek returns the token n positions after the current one (EOF at the end).
func (p *parser) peek(n int) token.Token {
	if p.i+n < len(p.toks) {
		return p.toks[p.i+n]
	}
	return p.toks[len(p.toks)-1]
}

func (p *parser) at(k token.Kind) bool { return p.tok.Kind == k }

func (p *parser) accept(k token.Kind) bool {
	if p.at(k) {
		p.next()
		return true
	}
	return false
}

// expect consumes a token of kind k, or reports "expected k <context>".
func (p *parser) expect(k token.Kind, context string) token.Token {
	t := p.tok
	if t.Kind != k {
		p.errorExpected("'" + k.String() + "' " + context)
	}
	p.next()
	return t
}

func (p *parser) errorf(pos token.Pos, format string, args ...any) {
	panic(bailout{diag.Errorf(pos, format, args...)})
}

func (p *parser) errorExpected(what string) {
	p.errorf(p.tok.Pos, "expected %s, found %s", what, describe(p.tok))
}

// describe names a token for an error message.
func describe(t token.Token) string {
	switch t.Kind {
	case token.EOF:
		return "end of file"
	case token.IDENT:
		return "identifier '" + t.Text + "'"
	case token.INT, token.FLOAT:
		return "number " + t.Text
	case token.CHAR:
		return "character " + t.Text
	case token.STRING:
		if len(t.Text) > 20 {
			return "string " + t.Text[:17] + "..."
		}
		return "string " + t.Text
	}
	return "'" + t.Kind.String() + "'"
}

func (p *parser) ident(what string) *ast.Ident {
	t := p.tok
	if t.Kind != token.IDENT {
		p.errorExpected(what)
	}
	p.next()
	return &ast.Ident{NamePos: t.Pos, Name: t.Text}
}

// ----------------------------------------------------------------------
// Definitions

func (p *parser) parseDecl() ast.Decl {
	switch p.tok.Kind {
	case token.CLASS:
		return p.parseClass()
	case token.GLOBAL:
		pos := p.tok.Pos
		p.next()
		vars := p.parseVarList("global variable name", false)
		p.expect(token.SEMICOLON, "after global declaration")
		return &ast.GlobalDecl{Global: pos, Vars: vars}
	case token.CONST:
		pos := p.tok.Pos
		p.next()
		vars := p.parseVarList("constant name", true)
		p.expect(token.SEMICOLON, "after constant declaration")
		return &ast.ConstDecl{Const: pos, Vars: vars}
	case token.IDENT:
		switch p.peek(1).Kind {
		case token.SCOPE, token.LPAREN:
			return p.parseFuncDecl()
		}
		p.next()
		p.errorExpected("'(' or '::' after function name")
	case token.USE:
		p.errorf(p.tok.Pos, "use declarations come before all other definitions of a file")
	case token.SEMICOLON:
		p.errorf(p.tok.Pos, "unexpected ';' (no ';' follows a class or function body)")
	}
	p.errorExpected("a definition (class, function, global or const)")
	return nil
}

// parseVarList parses "name [= value] {, name [= value]}" for global, const
// and local declarations. Constants need a value, which is a conditional
// expression; the others take an assignment.
func (p *parser) parseVarList(what string, isConst bool) []*ast.VarSpec {
	var vars []*ast.VarSpec
	for {
		v := &ast.VarSpec{Name: p.ident(what)}
		if isConst {
			p.expect(token.ASSIGN, "after constant name")
			v.Value = p.parseConditional()
		} else if p.accept(token.ASSIGN) {
			v.Value = p.parseAssignValue()
		}
		vars = append(vars, v)
		if !p.accept(token.COMMA) {
			return vars
		}
	}
}

func (p *parser) parseClass() *ast.ClassDecl {
	c := &ast.ClassDecl{Class: p.tok.Pos}
	p.next()
	c.Name = p.ident("class name")
	if p.accept(token.COLON) {
		c.Base = p.ident("base class name after ':'")
		if p.accept(token.SCOPE) {
			c.BaseModule, c.Base = c.Base, p.ident("class name after '::'")
		}
	}
	lbrace := p.expect(token.LBRACE, "to start the class body")
	for !p.at(token.RBRACE) {
		if p.at(token.EOF) {
			p.errorf(p.tok.Pos, "expected '}' to close the class body opened at %s, found end of file", lbrace.Pos)
		}
		c.Members = append(c.Members, p.parseMember())
	}
	p.next()
	return c
}

func (p *parser) parseMember() ast.Member {
	start := p.tok.Pos
	static := p.accept(token.STATIC)
	name := p.ident("member name")
	if !p.accept(token.LPAREN) {
		f := &ast.FieldDecl{Start: start, Static: static, Names: []*ast.Ident{name}}
		for p.accept(token.COMMA) {
			f.Names = append(f.Names, p.ident("data member name"))
		}
		p.expect(token.SEMICOLON, "after data member declaration")
		return f
	}
	m := &ast.MethodDecl{Start: start, Static: static, Name: name}
	if p.at(token.IDENT) {
		for {
			m.Params = append(m.Params, p.ident("parameter name"))
			if p.at(token.ASSIGN) {
				p.errorf(p.tok.Pos, "a default value belongs in the definition of %s, not in its declaration", name.Name)
			}
			if p.accept(token.DOTDOT) {
				m.Rest = true
				p.accept(token.COMMA) // a trailing comma
				break
			}
			if !p.accept(token.COMMA) || p.at(token.RPAREN) {
				break
			}
		}
	}
	p.expect(token.RPAREN, "after declared parameters")
	p.expect(token.SEMICOLON, "after member function declaration")
	return m
}

func (p *parser) parseFuncDecl() *ast.FuncDecl {
	f := &ast.FuncDecl{}
	f.Name = p.ident("function name")
	if p.accept(token.SCOPE) {
		f.Class = f.Name
		f.Name = p.ident("member function name after '::'")
	}
	p.expect(token.LPAREN, "after function name")
	f.Params = p.parseParamList()
	p.expect(token.RPAREN, "after parameters")
	f.Body = p.parseBlock("to start the function body")
	return f
}

// parseParamList parses the parameters up to the ")".
func (p *parser) parseParamList() *ast.ParamList {
	pl := &ast.ParamList{}
	if p.at(token.IDENT) {
		for {
			prm := &ast.Param{Name: p.ident("parameter name")}
			if p.accept(token.ASSIGN) {
				prm.Default = p.parseAssign()
			} else if p.accept(token.DOTDOT) {
				prm.Rest = true
			}
			pl.Params = append(pl.Params, prm)
			if !p.accept(token.COMMA) || p.at(token.RPAREN) {
				break // a trailing comma may come before the ")"
			}
		}
	}
	if p.at(token.SEMICOLON) {
		// "f(a; t, u)" declared temporaries up to syntax version 3. The
		// advice names them: "local t, u;".
		var names []string
		for i := p.i + 1; i < len(p.toks) && (p.toks[i].Kind == token.IDENT || p.toks[i].Kind == token.COMMA); i++ {
			if p.toks[i].Kind == token.IDENT {
				names = append(names, p.toks[i].Text)
			}
		}
		decl := "local " + strings.Join(names, ", ") + ";"
		if len(names) == 0 {
			decl = "local name;"
		}
		p.errorf(p.tok.Pos, "temporaries after ';' were removed in syntax version 4: declare them at the start of the body instead (%s)", decl)
	}
	return pl
}

// ----------------------------------------------------------------------
// Statements

func (p *parser) parseBlock(context string) *ast.Block {
	b := &ast.Block{Lbrace: p.expect(token.LBRACE, context).Pos}
	for !p.at(token.RBRACE) {
		if p.at(token.EOF) {
			p.errorf(p.tok.Pos, "expected '}' to close the block opened at %s, found end of file", b.Lbrace)
		}
		b.Stmts = append(b.Stmts, p.parseStmt())
	}
	b.Rbrace = p.tok.Pos
	p.next()
	return b
}

func (p *parser) parseStmt() ast.Stmt {
	p.enter()
	defer p.leave()
	pos := p.tok.Pos
	switch p.tok.Kind {
	case token.LBRACE: // a "{" that starts a statement is a block
		switch p.peek(1).Kind {
		case token.STRING, token.INT, token.FLOAT, token.CHAR:
			if p.peek(2).Kind == token.COLON { // {"a": 1} written as a statement
				p.errorf(p.peek(2).Pos, "a '{' that starts a statement is a block, not a map literal (in parentheses, ({...}), it is a map)")
			}
		}
		return p.parseBlock("")
	case token.SEMICOLON:
		p.next()
		return &ast.EmptyStmt{Semi: pos}
	case token.IF:
		return p.parseIf()
	case token.SWITCH:
		p.next()
		tag := p.parseCondition("switch")
		return &ast.SwitchStmt{Switch: pos, Tag: tag, Body: p.parseBlock("to start the switch body")}
	case token.CASE:
		p.next()
		v := p.parseBinary(precLowest)
		p.expect(token.COLON, "after case value")
		return &ast.CaseLabel{Case: pos, Value: v}
	case token.DEFAULT:
		p.next()
		p.expect(token.COLON, "after 'default'")
		return &ast.DefaultLabel{Default: pos}
	case token.WHILE:
		p.next()
		cond := p.parseCondition("while")
		return &ast.WhileStmt{While: pos, Cond: cond, Body: p.parseStmt()}
	case token.DO:
		p.next()
		body := p.parseStmt()
		p.expect(token.WHILE, "after the body of a do loop")
		cond := p.parseCondition("while")
		p.expect(token.SEMICOLON, "after do-while condition")
		return &ast.DoStmt{Do: pos, Body: body, Cond: cond}
	case token.FOR:
		return p.parseFor()
	case token.BREAK, token.CONTINUE:
		kind := p.tok.Kind
		p.next()
		var label *ast.Ident
		if p.at(token.IDENT) {
			label = p.ident("label")
		}
		p.expect(token.SEMICOLON, "after "+kind.String())
		if kind == token.BREAK {
			return &ast.BreakStmt{Break: pos, Label: label}
		}
		return &ast.ContinueStmt{Continue: pos, Label: label}
	case token.RETURN:
		p.next()
		var v ast.Expr
		if !p.at(token.SEMICOLON) {
			v = p.parseExpr()
		}
		p.expect(token.SEMICOLON, "after return")
		return &ast.ReturnStmt{Return: pos, Value: v}
	case token.YIELD:
		p.next()
		if p.atFrom() {
			p.next()
			v := p.parseExpr()
			p.expect(token.SEMICOLON, "after yield from")
			return &ast.YieldStmt{Yield: pos, Value: v, From: true}
		}
		var v ast.Expr
		if !p.at(token.SEMICOLON) {
			v = p.parseExpr()
		}
		p.expect(token.SEMICOLON, "after yield")
		return &ast.YieldStmt{Yield: pos, Value: v}
	case token.LOCAL:
		p.next()
		vars := p.parseVarList("local variable name", false)
		p.expect(token.SEMICOLON, "after local declaration")
		return &ast.LocalDecl{Local: pos, Vars: vars}
	case token.TRY:
		return p.parseTry()
	case token.WITH:
		p.next()
		p.expect(token.LPAREN, "after 'with'")
		p.expect(token.LOCAL, "after 'with (': with declares its variables, as in with (local f = open(...))")
		vars := p.parseVarList("local variable name", false)
		for _, v := range vars {
			if v.Value == nil {
				p.errorf(v.Name.NamePos, "%s needs a value: with closes what its variables hold", v.Name.Name)
			}
		}
		p.expect(token.RPAREN, "after the variables of with")
		return &ast.WithStmt{With: pos, Vars: vars, Body: p.parseStmt()}
	case token.THROW:
		p.next()
		v := p.parseExpr()
		p.expect(token.SEMICOLON, "after throw")
		return &ast.ThrowStmt{Throw: pos, Value: v}
	case token.IDENT:
		// A label: the ":" follows the name on the same line.
		if next := p.peek(1); next.Kind == token.COLON && next.Pos.Line == pos.Line {
			label := p.ident("label")
			p.next()
			return &ast.LabeledStmt{Label: label, Stmt: p.parseStmt()}
		}
	}
	x := p.parseExpr()
	p.expect(token.SEMICOLON, "after expression")
	return &ast.ExprStmt{X: x}
}

// parseCondition parses "( expression )" after if, while or switch.
func (p *parser) parseCondition(keyword string) ast.Expr {
	p.expect(token.LPAREN, "after '"+keyword+"'")
	x := p.parseExpr()
	p.expect(token.RPAREN, "after "+keyword+" condition")
	return x
}

// parseIf parses "if (c) s [else s]".
func (p *parser) parseIf() *ast.IfStmt {
	s := &ast.IfStmt{If: p.tok.Pos}
	p.next()
	s.Cond = p.parseCondition("if")
	s.Then = p.parseStmt()
	if p.accept(token.ELSE) {
		s.Else = p.parseStmt()
	} else if p.at(token.IDENT) && p.tok.Text == "elseif" && p.oldElseif() {
		p.errorf(p.tok.Pos, "elseif was removed in syntax version 5: write 'else if'")
	}
	return s
}

// oldElseif reports whether "elseif (...)" at the current token is the
// elseif of syntax version 4 rather than a call of a function named
// elseif: after the ")" a statement starts (a name, a literal, a keyword or
// "{"), which cannot follow a call. ("elseif (c) ;" parses as a call; the
// compiler then gives the same advice for the undefined name.)
func (p *parser) oldElseif() bool {
	if p.peek(1).Kind != token.LPAREN {
		return false
	}
	depth := 0
	for i := p.i + 1; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case token.LPAREN:
			depth++
		case token.RPAREN:
			if depth--; depth == 0 {
				switch next := p.toks[min(i+1, len(p.toks)-1)].Kind; {
				case next == token.IDENT, next == token.LBRACE, next.IsKeyword(),
					next == token.INT, next == token.FLOAT, next == token.CHAR, next == token.STRING:
					return true
				}
				return false
			}
		case token.EOF:
			return false
		}
	}
	return false
}

func (p *parser) parseFor() ast.Stmt {
	pos := p.tok.Pos
	p.next()
	p.expect(token.LPAREN, "after 'for'")

	// for (local k in x) / for (local k, v in x)
	if p.at(token.LOCAL) && p.peek(1).Kind == token.IDENT {
		one := p.peek(2).Kind == token.IN
		two := p.peek(2).Kind == token.COMMA && p.peek(3).Kind == token.IDENT && p.peek(4).Kind == token.IN
		if one || two {
			p.next()
			s := &ast.ForEachStmt{For: pos, Key: p.ident("loop variable")}
			if two {
				p.next()
				s.Value = p.ident("loop variable")
			}
			p.next() // in
			s.X = p.parseExpr()
			p.expect(token.RPAREN, "after for-each expression")
			s.Body = p.parseStmt()
			return s
		}
	}

	s := &ast.ForStmt{For: pos}
	if p.accept(token.LOCAL) {
		s.InitVars = p.parseVarList("local variable name", false)
		if p.at(token.IN) {
			p.errorf(p.tok.Pos, "a for-each loop takes one or two names before 'in'")
		}
	} else if !p.at(token.SEMICOLON) {
		s.Init = p.parseExprList()
	}
	p.expect(token.SEMICOLON, "after for initializer")
	if !p.at(token.SEMICOLON) {
		s.Cond = p.parseExpr()
	}
	p.expect(token.SEMICOLON, "after for condition")
	if !p.at(token.RPAREN) {
		s.Post = p.parseExprList()
	}
	p.expect(token.RPAREN, "after for update")
	s.Body = p.parseStmt()
	return s
}

func (p *parser) parseTry() *ast.TryStmt {
	s := &ast.TryStmt{Try: p.tok.Pos}
	p.next()
	s.Body = p.parseBlock("after 'try'")
	if p.accept(token.CATCH) {
		p.expect(token.LPAREN, "after 'catch'")
		s.CatchVar = p.ident("catch variable name")
		p.expect(token.RPAREN, "after catch variable")
		s.Catch = p.parseBlock("to start the catch block")
	}
	if p.accept(token.FINALLY) {
		s.Finally = p.parseBlock("after 'finally'")
	}
	if s.Catch == nil && s.Finally == nil {
		p.errorExpected("'catch' or 'finally' after the try block")
	}
	return s
}

// ----------------------------------------------------------------------
// Expressions

// parseExpr parses an expression. The comma operator belongs only to the
// first and last part of a for loop (parseExprList); elsewhere a comma
// after an expression gets a message that says so.
func (p *parser) parseExpr() ast.Expr {
	x := p.parseAssign()
	if p.at(token.COMMA) {
		p.errorf(p.tok.Pos, "a comma between expressions is only allowed in the first and last part of a for loop, as in for (i = 0, j = n; i < j; i++, j--); write separate statements")
	}
	return x
}

// parseExprList parses "a, b, c" (the comma operator) for the first and
// last part of a for loop.
func (p *parser) parseExprList() ast.Expr {
	x := p.parseAssign()
	if !p.at(token.COMMA) {
		return x
	}
	list := []ast.Expr{x}
	for p.accept(token.COMMA) {
		list = append(list, p.parseAssign())
	}
	return &ast.CommaExpr{List: list}
}

func isAssignOp(k token.Kind) bool {
	switch k {
	case token.ASSIGN, token.ADD_ASSIGN, token.SUB_ASSIGN, token.MUL_ASSIGN, token.DIV_ASSIGN,
		token.MOD_ASSIGN, token.AND_ASSIGN, token.OR_ASSIGN, token.XOR_ASSIGN,
		token.SHL_ASSIGN, token.SHR_ASSIGN:
		return true
	}
	return false
}

// parseAssign parses a right-associative assignment.
func (p *parser) parseAssign() ast.Expr {
	lhs := p.parseConditional()
	if !isAssignOp(p.tok.Kind) {
		return lhs
	}
	op := p.tok.Kind
	p.next()
	return &ast.AssignExpr{Op: op, LHS: lhs, RHS: p.parseAssignValue()}
}

// parseAssignValue parses what follows "=" in an assignment or a variable
// declaration: an assignment, or a yield that gives a value (x = yield v).
func (p *parser) parseAssignValue() ast.Expr {
	if p.at(token.YIELD) {
		return p.parseYieldExpr()
	}
	return p.parseAssign()
}

// parseYieldExpr parses "yield [value]" where a value is wanted; the value
// is left out before ";", ")" and ",".
func (p *parser) parseYieldExpr() ast.Expr {
	pos := p.tok.Pos
	p.next()
	if p.atFrom() {
		p.errorf(p.tok.Pos, "yield from is a statement: it gives no value")
	}
	var v ast.Expr
	if !p.at(token.SEMICOLON) && !p.at(token.RPAREN) && !p.at(token.COMMA) {
		v = p.parseAssign()
	}
	return &ast.YieldExpr{Yield: pos, Value: v}
}

// atFrom tells whether the word after yield is the from of "yield from x":
// the name from followed by a token that starts an expression and cannot
// follow one (a name, a literal, new, fn, this, "!", "~"), or by "(", "["
// or "{". from is not a keyword: "yield from;", "yield from - 1;" and
// "yield from++;" yield a variable called from; "yield (from)(x);" and
// "yield (from)[0];" call or index one.
func (p *parser) atFrom() bool {
	if !p.at(token.IDENT) || p.tok.Text != "from" {
		return false
	}
	switch p.peek(1).Kind {
	case token.IDENT, token.INT, token.FLOAT, token.CHAR, token.STRING, token.TRUE, token.FALSE, token.NIL,
		token.THIS, token.NEW, token.FN, token.LPAREN, token.LBRACK, token.LBRACE, token.BANG, token.TILDE:
		return true
	}
	return false
}

// parseConditional parses "c ? expression : assignment".
func (p *parser) parseConditional() ast.Expr {
	c := p.parseBinary(precLowest)
	if !p.accept(token.QUESTION) {
		return c
	}
	then := p.parseExpr()
	p.expect(token.COLON, "in conditional expression")
	return &ast.CondExpr{Cond: c, Then: then, Else: p.parseAssign()}
}

// Binary operator precedences, lowest first. All are left associative.
const (
	precNone = iota
	precOrOr
	precAndAnd
	precBitOr
	precBitXor
	precBitAnd
	precEquality
	precRelational
	precShift
	precAdditive
	precMultiplicative

	precLowest = precOrOr
)

func binaryPrec(k token.Kind) int {
	switch k {
	case token.OROR:
		return precOrOr
	case token.ANDAND:
		return precAndAnd
	case token.PIPE:
		return precBitOr
	case token.CARET:
		return precBitXor
	case token.AMP:
		return precBitAnd
	case token.EQ, token.NE:
		return precEquality
	case token.LT, token.LE, token.GT, token.GE:
		return precRelational
	case token.SHL, token.SHR:
		return precShift
	case token.PLUS, token.MINUS:
		return precAdditive
	case token.STAR, token.SLASH, token.PERCENT:
		return precMultiplicative
	}
	return precNone
}

// parseBinary parses binary operators of precedence minPrec and higher
// (precedence climbing).
func (p *parser) parseBinary(minPrec int) ast.Expr {
	x := p.parseUnary()
	for {
		prec := binaryPrec(p.tok.Kind)
		if prec == precNone || prec < minPrec {
			return x
		}
		op := p.tok
		p.next()
		y := p.parseBinary(prec + 1)
		x = &ast.BinaryExpr{Op: op.Kind, OpPos: op.Pos, X: x, Y: y}
	}
}

func isPostfixStart(k token.Kind) bool {
	switch k {
	case token.LPAREN, token.LBRACK, token.ARROW, token.INC, token.DEC:
		return true
	}
	return false
}

// parseUnary is on the path of every nested expression (parentheses,
// literals, operands), so it also guards the nesting depth.
func (p *parser) parseUnary() ast.Expr {
	p.enter()
	defer p.leave()
	op := p.tok
	switch op.Kind {
	case token.MINUS:
		// "-" directly followed by a literal is folded into the literal,
		// unless a postfix operator applies to the literal first.
		if lit := p.peek(1); (lit.Kind == token.INT || lit.Kind == token.FLOAT || lit.Kind == token.CHAR) &&
			!isPostfixStart(p.peek(2).Kind) {
			p.next()
			return p.parseLiteral(op.Pos, true)
		}
		fallthrough
	case token.BANG, token.TILDE:
		p.next()
		return &ast.UnaryExpr{Op: op.Kind, OpPos: op.Pos, X: p.parseUnary()}
	case token.INC, token.DEC:
		p.next()
		return &ast.IncDecExpr{Op: op.Kind, OpPos: op.Pos, Prefix: true, X: p.parseUnary()}
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() ast.Expr {
	x := p.parsePrimary()
	for {
		switch t := p.tok; t.Kind {
		case token.LPAREN:
			p.next()
			args := p.parseArgs(token.RPAREN)
			p.expect(token.RPAREN, "after arguments")
			x = &ast.CallExpr{Fun: x, Lparen: t.Pos, Args: args}
		case token.LBRACK:
			p.next()
			idx := p.parseExpr()
			p.expect(token.RBRACK, "after index")
			x = &ast.IndexExpr{X: x, Lbrack: t.Pos, Index: idx}
		case token.ARROW:
			p.next()
			name := p.ident("member name after '->'")
			if p.accept(token.LPAREN) {
				args := p.parseArgs(token.RPAREN)
				p.expect(token.RPAREN, "after arguments")
				x = &ast.MethodCall{X: x, Name: name, Args: args}
			} else {
				x = &ast.MemberExpr{X: x, Name: name}
			}
		case token.INC, token.DEC:
			p.next()
			x = &ast.IncDecExpr{Op: t.Kind, OpPos: t.Pos, X: x}
		default:
			return x
		}
	}
}

// parseArgs parses "[argument {, argument} [,]]" up to (not including)
// close: a trailing comma may come before it. An argument may be spread:
// "v..".
func (p *parser) parseArgs(close token.Kind) []ast.Expr {
	if p.at(close) {
		return nil
	}
	var args []ast.Expr
	for {
		x := p.parseAssign()
		if p.accept(token.DOTDOT) {
			x = &ast.Spread{X: x}
		}
		args = append(args, x)
		if !p.accept(token.COMMA) || p.at(close) {
			return args
		}
	}
}

func (p *parser) parsePrimary() ast.Expr {
	t := p.tok
	switch t.Kind {
	case token.LPAREN:
		p.next()
		var x ast.Expr
		if p.at(token.YIELD) {
			x = p.parseYieldExpr()
		} else {
			x = p.parseExpr()
		}
		p.expect(token.RPAREN, "to close '(' at "+t.Pos.String())
		return x
	case token.YIELD:
		p.errorf(t.Pos, "a yield that gives a value stands after = or in parentheses: (yield x)")
	case token.INT, token.FLOAT, token.CHAR, token.STRING:
		return p.parseLiteral(t.Pos, false)
	case token.TRUE, token.FALSE:
		p.next()
		return &ast.BoolLit{ValuePos: t.Pos, Value: t.Kind == token.TRUE}
	case token.NIL:
		p.next()
		return &ast.NilLit{ValuePos: t.Pos}
	case token.THIS:
		p.next()
		return &ast.ThisExpr{This: t.Pos}
	case token.LBRACK:
		p.next()
		elems := p.parseArgs(token.RBRACK)
		p.expect(token.RBRACK, "to close the vector opened at "+t.Pos.String())
		return &ast.VectorLit{Lbrack: t.Pos, Elems: elems}
	case token.LBRACE:
		return p.parseMap()
	case token.NEW:
		p.next()
		var module *ast.Ident
		class := p.ident("class name after 'new'")
		if p.accept(token.SCOPE) {
			module, class = class, p.ident("class name after '::'")
		}
		p.expect(token.LPAREN, "after class name")
		args := p.parseArgs(token.RPAREN)
		p.expect(token.RPAREN, "after arguments")
		return &ast.NewExpr{New: t.Pos, Module: module, Class: class, Args: args}
	case token.FN:
		p.next()
		p.expect(token.LPAREN, "after 'fn'")
		params := p.parseParamList()
		p.expect(token.RPAREN, "after parameters")
		return &ast.FuncLit{Fn: t.Pos, Params: params, Body: p.parseBlock("to start the function body")}
	case token.IDENT:
		id := p.ident("name")
		if p.accept(token.SCOPE) {
			r := &ast.StaticRef{Class: id, Name: p.ident("name after '::'")}
			if p.accept(token.SCOPE) {
				r.Module, r.Class, r.Name = r.Class, r.Name, p.ident("member name after '::'")
			}
			return r
		}
		return id
	}
	p.errorExpected("expression")
	return nil
}

func (p *parser) parseMap() *ast.MapLit {
	m := &ast.MapLit{Lbrace: p.tok.Pos}
	p.next()
	if !p.at(token.RBRACE) {
		for {
			e := &ast.MapEntry{Key: p.parseConditional()}
			p.expect(token.COLON, "after map key")
			e.Value = p.parseAssign()
			m.Entries = append(m.Entries, e)
			if !p.accept(token.COMMA) || p.at(token.RBRACE) {
				break // a trailing comma may come before the "}"
			}
		}
	}
	p.expect(token.RBRACE, "to close the map opened at "+m.Lbrace.String())
	return m
}

// parseLiteral turns the current literal token into a node. With negate it
// is the operand of a folded unary minus at pos.
func (p *parser) parseLiteral(pos token.Pos, negate bool) ast.Expr {
	t := p.tok
	p.next()
	text, sign := t.Text, int64(1)
	if negate {
		text, sign = "-"+t.Text, -1
	}
	switch t.Kind {
	case token.INT:
		if t.IntMin && !negate {
			p.errorf(t.Pos, "integer %s is too large (2^63 is only valid directly after a unary minus)", t.Text)
		}
		// int64(1<<63) is MinInt64, and negating it wraps to itself: exactly
		// the value of -9223372036854775808.
		return &ast.IntLit{ValuePos: pos, Text: text, Value: sign * int64(t.Int)}
	case token.FLOAT:
		return &ast.FloatLit{ValuePos: pos, Text: text, Value: float64(sign) * t.Float}
	case token.CHAR:
		return &ast.CharLit{ValuePos: pos, Text: text, Value: sign * int64(t.Int)}
	}
	return &ast.StringLit{ValuePos: pos, Text: t.Text, Value: t.Str}
}
