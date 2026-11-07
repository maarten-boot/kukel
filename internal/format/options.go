package format

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

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
	// lines (they get the braces); empty blocks stay {}. It also puts one
	// argument per line in a call with more than 3 arguments, and in a call
	// or definition on a line longer than LineLength (lists.go).
	Strict bool
	// Vertical (kukel fmt -vertical) does what Strict does, and puts one
	// item per line in every call with 2 or more arguments and every
	// function and member function definition with 2 or more parameters.
	Vertical bool
	// LineLength is the longest line, in characters, that Strict leaves a
	// call or definition on (0: DefaultLineLength).
	LineLength int
}

// DefaultLineLength is the line length of Options when it gives none.
const DefaultLineLength = 120

// maxRounds bounds the rounds of breaking lists that are too long: each
// breaks the outermost list of each long line, and a line holds few.
const maxRounds = 50

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
	if opt.Vertical {
		opt.Strict = true
	}
	if opt.LineLength <= 0 {
		opt.LineLength = DefaultLineLength
	}
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
		in = tidy(braced, toks, in)
		if braced, err = parser.ParseFile(file, in); err != nil {
			return nil, changed
		}
		if toks, err = tokens(file, in); err != nil {
			return nil, changed
		}
		in = expand(braced, toks, in)
		// The lists to put one item per line by their number of items, then
		// those on lines too long, a round at a time (the outermost list of
		// a long line first; what is still too long after that, next).
		for round := 0; ; round++ {
			tree, err := parser.ParseFile(file, in)
			if err != nil {
				return nil, changed
			}
			if inToks, err = tokens(file, in); err != nil {
				return nil, changed
			}
			ls := lists(tree, inToks)
			var chosen []list
			if round == 0 {
				chosen = byCount(ls, inToks, opt.Vertical)
			} else {
				chosen = tooLong(ls, inToks, in, opt.LineLength)
			}
			if len(chosen) == 0 && round > 0 || round == maxRounds {
				break
			}
			next := breakLists(chosen, inToks, in)
			nextToks, err := tokens(file, next)
			if err != nil {
				return nil, changed
			}
			in = layout(nextToks, next)
		}
		toks = inToks // the commas added after last items are the program's tokens now
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
// the statement: while (a) { // why) and " }" after the statement and the
// comments at the end of its line (a line comment: "}" on the next line).
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
		e := stmtEnd(toks, *p)
		header, end := toks[start-1], toks[e] // (a statement of a header has its header before it)
		// The "}" goes after the comments at the end of the statement's last
		// line, so that they stay with it: on a line of its own after a
		// line comment.
		closer := " }"
		for e+1 < len(toks) && toks[e+1].Kind == token.COMMENT && toks[e+1].Pos.Line == endLine(toks[e]) {
			e++
			if end = toks[e]; strings.HasPrefix(end.Text, "//") || strings.HasPrefix(end.Text, "#") {
				closer = "\n}"
				break
			}
		}
		ins = append(ins, insert{header.Pos.Offset + len(header.Text), " {"}, insert{end.Pos.Offset + len(end.Text), closer})
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

// tidy prepares src (with its braces) for expand: an else on the line
// after the "}" of its if's block, with nothing between, goes up to it
// ("} else"); and a block on one line whose "}" ends with a comment ends
// the line, in a function definition (the blocks expand puts on several
// lines), gives the comment to the line of its "{" ("f() { return 1; }
// // why" becomes "f() { // why" and the block): it stays with the line it
// was written on, not with the "}" alone. Comments are not in the tree,
// so the program does not change.
func tidy(tree *ast.File, toks []token.Token, src []byte) []byte {
	type edit struct {
		from, to int // the bytes src[from:to] are replaced
		text     string
	}
	var edits []edit
	for _, d := range tree.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.IfStmt:
				if b, ok := n.Then.(*ast.Block); ok && n.Else != nil {
					rb := tokenAt(toks, b.Rbrace)
					if e := rb + 1; e < len(toks) && toks[e].Kind == token.ELSE && toks[e].Pos.Line > endLine(toks[rb]) {
						edits = append(edits, edit{toks[rb].Pos.Offset + 1, toks[e].Pos.Offset, " "})
					}
				}
			case *ast.Block:
				lb, rb := tokenAt(toks, n.Lbrace), tokenAt(toks, n.Rbrace)
				c := rb + 1
				if len(n.Stmts) == 0 || toks[lb].Pos.Line != toks[rb].Pos.Line || c >= len(toks) ||
					toks[c].Kind != token.COMMENT || toks[c].Pos.Line != toks[rb].Pos.Line ||
					c+1 < len(toks) && toks[c+1].Pos.Line == endLine(toks[c]) {
					return true
				}
				comment := strings.TrimRight(toks[c].Text, " \t\r")
				edits = append(edits,
					edit{toks[lb].Pos.Offset + 1, toks[lb].Pos.Offset + 1, " " + comment + "\n"},
					edit{toks[rb].Pos.Offset + 1, toks[c].Pos.Offset + len(toks[c].Text), ""})
			}
			return true
		})
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].from < edits[j].from })
	var b bytes.Buffer
	last := 0
	for _, e := range edits {
		if e.from < last {
			continue // (two edits of one place: not expected)
		}
		b.Write(src[last:e.from])
		b.WriteString(e.text)
		last = e.to
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
