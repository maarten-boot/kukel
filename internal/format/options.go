package format

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/token"
)

// Options are the choices of kukel fmt beyond its fixed layout.
type Options struct {
	// Strict (kukel fmt -strict) puts braces around the statement of each
	// if, else, for, while, do and with that has none (not around the if
	// of an else if, nor around a local declaration or a case label, which
	// may not stand there: such a program stays an error), and puts every
	// function and member function definition on several lines: each
	// statement of its blocks on a line of its own, also in a function
	// that fits on one (f() { return 1; }). Function literals keep their
	// lines (they get the braces); empty blocks stay {}.
	Strict bool
}

// SourceWith formats one source file as Source does, with the choices of
// opt.
func SourceWith(file string, src []byte, opt Options) ([]byte, error) {
	if bytes.IndexByte(src, 0) >= 0 {
		return nil, fmt.Errorf("%s: contains a NUL byte; not formatted", file)
	}
	tree, err := parser.ParseFile(file, src)
	if err != nil {
		return nil, err
	}
	toks, err := tokens(file, src)
	if err != nil {
		return nil, err
	}
	changed := fmt.Errorf("%s: internal error: formatting would change the program; not formatted", file)
	var bare []*ast.Stmt // with Strict: the statements that get braces
	in, inToks := src, toks
	if opt.Strict {
		bare = unbraced(tree)
		in = insertBraces(bare, toks, src)
		braced, err := parser.ParseFile(file, in)
		if err != nil {
			return nil, changed
		}
		if toks, err = tokens(file, in); err != nil {
			return nil, changed
		}
		in = expand(braced, toks, in)
		if inToks, err = tokens(file, in); err != nil {
			return nil, changed
		}
	}
	out := layout(inToks, in)
	again, err := tokens(file, out)
	if err != nil || !same(toks, again) {
		return nil, changed
	}
	if opt.Strict && !sameTree(file, tree, bare, out) {
		return nil, changed
	}
	return out, nil
}

// unbraced gives the statements of if, else, for, while, do and with that
// are not blocks and may be put in one.
func unbraced(tree *ast.File) []*ast.Stmt {
	var bare []*ast.Stmt
	add := func(p *ast.Stmt) {
		switch (*p).(type) {
		case nil, *ast.Block, *ast.LocalDecl, *ast.CaseLabel, *ast.DefaultLabel:
			return
		}
		bare = append(bare, p)
	}
	for _, d := range tree.Decls {
		ast.Inspect(d, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.IfStmt:
				add(&n.Then)
				if _, elseIf := n.Else.(*ast.IfStmt); !elseIf {
					add(&n.Else)
				}
			case *ast.ForStmt:
				add(&n.Body)
			case *ast.ForEachStmt:
				add(&n.Body)
			case *ast.WhileStmt:
				add(&n.Body)
			case *ast.DoStmt:
				add(&n.Body)
			case *ast.WithStmt:
				add(&n.Body)
			}
			return true
		})
	}
	return bare
}

// insertBraces gives src with " {" right after the header of each of the
// statements (its ")", else or do; before comments between the header and
// the statement: while (a) { // why) and " }" after the statement.
func insertBraces(bare []*ast.Stmt, toks []token.Token, src []byte) []byte {
	type insert struct {
		at   int
		text string
	}
	var ins []insert
	for _, p := range bare {
		start := stmtStart(toks, *p)
		for start > 0 && toks[start-1].Kind == token.COMMENT {
			start--
		}
		header, end := toks[start-1], toks[stmtEnd(toks, *p)] // (a statement of a header has its header before it)
		ins = append(ins, insert{header.Pos.Offset + len(header.Text), " {"}, insert{end.Pos.Offset + len(end.Text), " }"})
	}
	sort.SliceStable(ins, func(i, j int) bool { return ins[i].at < ins[j].at })
	var b bytes.Buffer
	last := 0
	for _, in := range ins {
		b.Write(src[last:in.at])
		b.WriteString(in.text)
		last = in.at
	}
	b.Write(src[last:])
	return b.Bytes()
}

// expand gives src with a line break before each statement of the blocks
// of function definitions and before their closing braces, where the token
// before is on the same line. Comments right before a statement on its
// line go with it (a; /* c */ b; breaks before the comment); a comment
// before a closing brace stays with the statement before it. Function
// literals are left alone.
func expand(tree *ast.File, toks []token.Token, src []byte) []byte {
	var at []int              // token indexes that start a line
	closers := map[int]bool{} // those of closing braces
	for _, d := range tree.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.Block:
				if len(n.Stmts) == 0 {
					return true
				}
				for _, s := range n.Stmts {
					at = append(at, stmtStart(toks, s))
				}
				r := tokenAt(toks, n.Rbrace)
				at, closers[r] = append(at, r), true
			}
			return true
		})
	}
	sort.Ints(at)
	var b bytes.Buffer
	last := 0
	endLine := func(t token.Token) int { return t.Pos.Line + bytes.Count([]byte(t.Text), []byte("\n")) }
	for _, i := range at {
		for !closers[i] && i > 0 && toks[i-1].Kind == token.COMMENT && endLine(toks[i-1]) == toks[i].Pos.Line {
			i--
		}
		if i == 0 || endLine(toks[i-1]) < toks[i].Pos.Line || toks[i].Pos.Offset < last {
			continue // on a line of its own already (or seen twice)
		}
		b.Write(src[last:toks[i].Pos.Offset])
		b.WriteByte('\n')
		last = toks[i].Pos.Offset
	}
	b.Write(src[last:])
	return b.Bytes()
}

// sameTree tells whether out parses to tree with each of the bare
// statements in a block of its own: Strict added braces and nothing else.
// It changes tree.
func sameTree(file string, tree *ast.File, bare []*ast.Stmt, out []byte) bool {
	got, err := parser.ParseFile(file, out)
	if err != nil {
		return false
	}
	for _, p := range bare {
		*p = &ast.Block{Stmts: []ast.Stmt{*p}}
	}
	var want, have bytes.Buffer
	ast.FprintFile(&want, tree, false)
	ast.FprintFile(&have, got, false)
	return bytes.Equal(want.Bytes(), have.Bytes())
}

// stmtStart gives the index of the first token of statement s: the token
// at its position, or a "(" before it ((a)->f(); starts at the "(", which
// the tree does not keep).
func stmtStart(toks []token.Token, s ast.Stmt) int {
	i := tokenAt(toks, s.Pos())
	for i > 0 && toks[i-1].Kind == token.LPAREN {
		i--
	}
	return i
}

// stmtEnd gives the index of the last token of statement s: its "}" or
// ";", or that of the statement it ends with.
func stmtEnd(toks []token.Token, s ast.Stmt) int {
	switch s := s.(type) {
	case *ast.Block:
		return tokenAt(toks, s.Rbrace)
	case *ast.IfStmt:
		if s.Else != nil {
			return stmtEnd(toks, s.Else)
		}
		return stmtEnd(toks, s.Then)
	case *ast.WhileStmt:
		return stmtEnd(toks, s.Body)
	case *ast.ForStmt:
		return stmtEnd(toks, s.Body)
	case *ast.ForEachStmt:
		return stmtEnd(toks, s.Body)
	case *ast.WithStmt:
		return stmtEnd(toks, s.Body)
	case *ast.LabeledStmt:
		return stmtEnd(toks, s.Stmt)
	case *ast.SwitchStmt:
		return stmtEnd(toks, s.Body)
	case *ast.TryStmt:
		if s.Finally != nil {
			return stmtEnd(toks, s.Finally)
		}
		if s.Catch != nil {
			return stmtEnd(toks, s.Catch)
		}
		return stmtEnd(toks, s.Body)
	case *ast.DoStmt:
		return semicolon(toks, stmtEnd(toks, s.Body)+1)
	}
	return semicolon(toks, stmtStart(toks, s))
}

// semicolon gives the index of the first ";" from toks[i] on outside
// brackets (a function literal's statements are inside its braces).
func semicolon(toks []token.Token, i int) int {
	depth := 0
	for ; i < len(toks); i++ {
		switch toks[i].Kind {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		case token.SEMICOLON:
			if depth == 0 {
				return i
			}
		}
	}
	return len(toks) - 1
}

// tokenAt gives the index of the token at position p.
func tokenAt(toks []token.Token, p token.Pos) int {
	return sort.Search(len(toks), func(i int) bool { return toks[i].Pos.Offset >= p.Offset })
}
