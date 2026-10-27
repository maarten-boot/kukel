// Package format formats Kukel source (kukel fmt): it re-indents lines and
// normalizes the spaces between tokens. Line breaks and comments stay where
// they are, and at most one blank line in a row is kept. A line that
// continues a statement outside brackets is indented one level more than
// its first line (see continues). A statement that
// starts with if, while, do, for, switch, try or with gets a blank line
// before it (breathing space), unless it starts a block or follows a label
// (see breathe).
//
// The result is checked: read again, it must give the same tokens and
// comments as the input, or Source refuses it (so formatting can never
// change a program).
package format

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/maarten-boot/kukel/internal/lexer"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/token"
)

// Indent is one level of indentation.
const Indent = "    "

// Source formats one source file. A file that does not parse is not
// formatted: the syntax error is returned.
func Source(file string, src []byte) ([]byte, error) {
	if bytes.IndexByte(src, 0) >= 0 {
		return nil, fmt.Errorf("%s: contains a NUL byte; not formatted", file)
	}
	if _, err := parser.ParseFile(file, src); err != nil {
		return nil, err
	}
	toks, err := tokens(file, src)
	if err != nil {
		return nil, err
	}
	out := layout(toks, src)
	again, err := tokens(file, out)
	if err != nil || !same(toks, again) {
		return nil, fmt.Errorf("%s: internal error: formatting would change the program; not formatted", file)
	}
	return out, nil
}

// tokens reads all tokens of src, comments included, without the EOF.
func tokens(file string, src []byte) ([]token.Token, error) {
	l := lexer.New(file, src)
	l.KeepComments = true
	var toks []token.Token
	for {
		t, err := l.Next()
		if err != nil {
			return nil, err
		}
		if t.Kind == token.EOF {
			return toks, nil
		}
		toks = append(toks, t)
	}
}

func same(a, b []token.Token) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i].Text, b[i].Text
		if a[i].Kind == token.COMMENT {
			x, y = strings.TrimRight(x, " \t\r"), strings.TrimRight(y, " \t\r")
		}
		if a[i].Kind != b[i].Kind || x != y {
			return false
		}
	}
	return true
}

// open is a bracket that is not closed yet.
type open struct {
	kind      token.Kind // LBRACE, LPAREN or LBRACK
	header    bool       // the "(" of if, while, for, switch, catch or with
	line      int        // the source line it was opened on (see openLine)
	hang      int        // the hanging levels of that line (if (...) under a for without braces)
	isMap     bool       // a "{" of a map literal (not a block)
	isSwitch  bool       // the "{" of a switch body
	isDo      bool       // the "{" of the body of a do
	caseBlock bool       // a block opened on the line of a case label
	inCase    bool       // a switch body after its first case or default label
	question  int        // unmatched "?" inside it (for the ":" of ?:)
}

type formatter struct {
	b        strings.Builder
	stack    []*open
	prev     *token.Token // the previous token on the current output line
	last     *token.Token // the previous token of the file (also across lines)
	unary    bool         // prev is a prefix operator
	switchAt int          // len(stack) when a switch keyword was seen, or -1
	lineHead *token.Token // the first token of the current line

	lastClosedMap bool          // the last "}" closed a map literal ({...}[k] indexes it)
	questionTop   int           // unmatched "?" outside any bracket
	closedHeader  bool          // the last ")" closed the parentheses of a control header
	hanging       int           // the next line is the statement of that many headers
	lineHanging   int           // the hanging levels of the current line
	lineCode      *token.Token  // the last token of the current line that is not a comment
	ifs           map[int][]int // per bracket depth: the hanging levels of lines that start with if
	doBody        bool          // the current line is the statement of a do without braces
	doTail        bool          // the current line ended that statement: a while next is the do's
	closedDo      bool          // the current line closed the braces of a do: a while next is the do's
	stmtHanging   int           // the hanging levels of the line that started the current statement
	ternaryColon  bool          // the last ":" was the one of ?:
	openLine      int           // the line brackets opened on the current line count as theirs
}

func layout(toks []token.Token, src []byte) []byte {
	f := &formatter{switchAt: -1, ifs: map[int][]int{}}
	endLine := 0 // the source line the previous token ended on
	for i := range toks {
		t := &toks[i]
		if i == 0 || t.Pos.Line > endLine {
			if i > 0 {
				f.endLine(f.lineCode, f.lineHanging)
				f.b.WriteByte('\n')
				if t.Pos.Line-endLine >= 2 || f.breathe(toks, i) {
					f.b.WriteByte('\n') // one blank line at most
				}
			}
			f.startLine(toks[i:])
		} else {
			f.space(t, src)
		}
		if t.Kind == token.COMMENT {
			f.b.WriteString(strings.TrimRight(t.Text, " \t\r")) // no trailing spaces
		} else {
			f.b.WriteString(t.Text)
		}
		f.after(t)
		endLine = t.Pos.Line + strings.Count(t.Text, "\n")
	}
	if len(toks) > 0 {
		f.b.WriteByte('\n')
	}
	return []byte(f.b.String())
}

// breathe tells whether the line that starts with toks[i] gets a blank
// line before it: it starts a statement with if, while, do, for, switch, try
// or with (maybe after a label), or it is a comment right above such a
// statement. Not at the start of a block, after a label, as the statement
// of a header without braces (else, for (...), ...), as the while of a do,
// or inside an expression.
func (f *formatter) breathe(toks []token.Token, i int) bool {
	if top := f.top(); top != nil && (top.kind != token.LBRACE || top.isMap) {
		return false // inside parentheses, brackets or a map literal
	}
	switch {
	case f.lineCode == nil: // after a line of comments: the blank line went above them
		return false
	case f.lineCode.Kind == token.LBRACE || f.lineCode.Kind == token.COLON:
		return false // the first statement of a block, or after a label
	case f.hanging > 0:
		return false // the statement of else, do, if (...), ... without braces
	}
	j := i
	for j < len(toks) && toks[j].Kind == token.COMMENT { // comments right above it
		end := toks[j].Pos.Line + strings.Count(toks[j].Text, "\n")
		j++
		if j < len(toks) && toks[j].Pos.Line > end+1 {
			return false // a blank line between the comments and what follows
		}
	}
	if j == len(toks) || j > i && toks[j-1].Pos.Line == toks[j].Pos.Line {
		return false
	}
	t := toks[j]
	if t.Kind == token.IDENT && j+2 < len(toks) && toks[j+1].Kind == token.COLON {
		// A label: outer: for (...), also with the label on a line of its
		// own (the blank line goes above the label). Comments may come
		// between.
		k := j + 2
		for k < len(toks)-1 && toks[k].Kind == token.COMMENT {
			k++
		}
		t = toks[k]
	}
	switch t.Kind {
	case token.WHILE:
		return !f.doTail && !f.closedDo
	case token.IF, token.DO, token.FOR, token.SWITCH, token.TRY, token.WITH:
		return true
	}
	return false
}

// startLine writes the indentation of a line that starts with rest[0].
func (f *formatter) startLine(rest []token.Token) {
	t := &rest[0]
	f.prev, f.unary, f.lineHead, f.lineCode = nil, false, t, nil
	// Closing brackets at the start of the line belong to the outer level.
	n := len(f.stack)
	for _, c := range rest {
		if c.Pos.Line != t.Pos.Line || !isCloser(c.Kind) || n == 0 {
			break
		}
		n--
	}
	// It lines up with the line that opened the last of them, so the other
	// brackets opened on that line count no more: the "(" of "f(fn() {"
	// for a line "}, 1);".
	for n > 0 && n < len(f.stack) && f.stack[n-1].line == f.stack[n].line {
		n--
	}
	entries := f.stack[:n]
	// Brackets opened on this line count as opened on that line too: in
	// "}, [" the "[" holds lines one level deeper than "}".
	f.openLine = t.Pos.Line
	if n < len(f.stack) {
		f.openLine = f.stack[n].line
	}
	label := t.Kind == token.CASE || t.Kind == token.DEFAULT
	if label { // a label of the innermost switch around it, also from a nested block
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].isSwitch {
				entries = entries[:i+1]
				break
			}
		}
	}
	level, lastLine := 0, -1
	for i, o := range entries {
		if o.line != lastLine { // brackets opened on one line count once
			level += 1 + o.hang
		}
		lastLine = o.line
		// The statements under a case label are one level deeper than the
		// label, unless a block opened on the label's line holds them.
		caseBlock := i+1 < len(f.stack) && f.stack[i+1].caseBlock
		if o.inCase && !caseBlock && !(label && i == len(entries)-1) {
			level++
		}
	}
	// The statement of an if, while, for, else, ... written on the next
	// line without braces is indented once more (a "{" there is not).
	hanging := f.hanging
	if t.Kind == token.LBRACE {
		hanging = 0
	}
	if n < len(f.stack) {
		hanging = f.stack[n].hang // a closing bracket lines up with the line that opened it
	}
	// An else on its own line lines up with its if: the last if that
	// started a line at this depth.
	depth := len(f.stack)
	if ifs := f.ifs[depth]; t.Kind == token.ELSE && len(ifs) > 0 {
		hanging = ifs[len(ifs)-1]
		f.ifs[depth] = ifs[:len(ifs)-1]
	}
	if t.Kind == token.IF || t.Kind == token.ELSE && len(rest) > 1 && rest[1].Kind == token.IF && rest[1].Pos.Line == t.Pos.Line {
		f.ifs[depth] = append(f.ifs[depth], hanging)
	}
	for d := range f.ifs { // ifs of blocks that are closed are done
		if d > depth {
			delete(f.ifs, d)
		}
	}
	// A line that continues a statement (the line before did not end it)
	// is indented one level more than the statement's first line.
	if f.continues(t) {
		hanging = f.stmtHanging + 1
	} else {
		f.stmtHanging = hanging
	}
	f.lineHanging = hanging
	f.hanging = 0
	f.doTail, f.doBody, f.closedDo = f.doBody, false, false
	f.b.WriteString(strings.Repeat(Indent, level+hanging))
}

// continues tells whether the line that starts with t continues the
// statement of the line before, outside brackets: that line did not end
// with a ";", a block's "{" or "}", a label's or case's ":" or a control
// header (whose statement hangs instead).
func (f *formatter) continues(t *token.Token) bool {
	if top := f.top(); top != nil && (top.kind != token.LBRACE || top.isMap) || f.last == nil || f.hanging > 0 {
		return false
	}
	if t.Kind == token.LBRACE || isCloser(t.Kind) {
		return false // the body of a definition or header; a closing bracket
	}
	switch f.last.Kind {
	case token.SEMICOLON, token.LBRACE, token.RBRACE, token.ELSE, token.DO:
		return false
	case token.COLON:
		return f.ternaryColon
	case token.RPAREN:
		return !f.closedHeader
	}
	return true
}

// endLine notes how the line that ends with token t ends: with a control
// header (if (...), else, do, ...), the next line hangs one level deeper.
func (f *formatter) endLine(t *token.Token, wasHanging int) {
	switch {
	case t == nil: // only comments: the pending statement is still to come
		f.hanging = wasHanging
	case t.Kind == token.ELSE || t.Kind == token.DO || t.Kind == token.RPAREN && f.closedHeader:
		f.hanging = wasHanging + 1
	}
	if t != nil && t.Kind == token.DO {
		f.doBody = true // the next line is its statement; a while after that is the do's
	}
}

// space writes the space (or none) between f.prev and t on one line.
func (f *formatter) space(t *token.Token, src []byte) {
	p := f.prev
	if t.Kind == token.COMMENT {
		// A trailing comment keeps its distance (aligned comments stay
		// aligned).
		gap := t.Pos.Offset - (p.Pos.Offset + len(p.Text))
		if gap < 1 {
			gap = 1
		}
		f.b.WriteString(strings.Repeat(" ", gap))
		return
	}
	if f.needSpace(p, t) {
		f.b.WriteByte(' ')
	}
}

func (f *formatter) needSpace(p, t *token.Token) bool {
	switch {
	case p.Kind == token.LBRACE && t.Kind == token.SEMICOLON:
		return true // { ; }
	case t.Kind == token.COMMA || t.Kind == token.SEMICOLON:
		return false
	case t.Kind == token.RPAREN || t.Kind == token.RBRACK:
		return false
	case p.Kind == token.LPAREN || p.Kind == token.LBRACK:
		return false
	case t.Kind == token.ARROW || t.Kind == token.SCOPE || p.Kind == token.ARROW || p.Kind == token.SCOPE:
		return false
	case t.Kind == token.DOTDOT:
		return false
	case f.unary:
		// No space after a prefix operator, except where two would read as
		// one token ("- -x", "- --x", "! !" is fine without).
		return p.Kind == token.MINUS && (t.Kind == token.MINUS || t.Kind == token.DEC) ||
			p.Kind == token.PLUS && (t.Kind == token.PLUS || t.Kind == token.INC)
	case (t.Kind == token.INC || t.Kind == token.DEC) && operandEnd(p):
		return false // postfix
	case t.Kind == token.LPAREN:
		switch p.Kind {
		case token.IDENT, token.RPAREN, token.RBRACK, token.FN:
			return false // a call, or fn (...)
		}
		return true
	case t.Kind == token.LBRACK:
		return !(p.Kind == token.IDENT || p.Kind == token.RPAREN || p.Kind == token.RBRACK || p.Kind == token.STRING ||
			p.Kind == token.RBRACE && f.lastClosedMap)
	case t.Kind == token.LBRACE:
		return true
	case p.Kind == token.LBRACE:
		top := f.top()
		return t.Kind != token.RBRACE && (top == nil || !top.isMap)
	case t.Kind == token.RBRACE:
		return !f.closingMap(t)
	case t.Kind == token.COLON:
		return f.colonSpaced()
	}
	return true
}

// top is the innermost open bracket, or nil.
func (f *formatter) top() *open {
	if len(f.stack) == 0 {
		return nil
	}
	return f.stack[len(f.stack)-1]
}

func (f *formatter) closingMap(t *token.Token) bool {
	top := f.top()
	return top != nil && top.kind == token.LBRACE && top.isMap
}

// colonSpaced tells whether a ":" gets a space before it: in ?: and after a
// class name, but not after a label, a case value or a map key.
func (f *formatter) colonSpaced() bool {
	if top := f.top(); top != nil && top.question > 0 || top == nil && f.questionTop > 0 {
		return true // the ":" of ?:
	}
	return f.lineHead != nil && f.lineHead.Kind == token.CLASS
}

// after updates the state after token t was written.
func (f *formatter) after(t *token.Token) {
	wasUnary := f.isUnary(t)
	switch t.Kind {
	case token.SWITCH:
		f.switchAt = len(f.stack)
	case token.LPAREN, token.LBRACK:
		o := &open{kind: t.Kind, line: f.openLine, hang: f.lineHanging}
		if t.Kind == token.LPAREN && f.last != nil {
			switch f.last.Kind {
			case token.IF, token.WHILE, token.FOR, token.SWITCH, token.CATCH, token.WITH:
				o.header = true
			}
		}
		f.stack = append(f.stack, o)
	case token.LBRACE:
		o := &open{kind: t.Kind, line: f.openLine, hang: f.lineHanging, isMap: f.mapBrace()}
		o.isDo = f.last != nil && f.last.Kind == token.DO
		if f.switchAt == len(f.stack) && !o.isMap {
			o.isSwitch, f.switchAt = true, -1
		} else if top := f.top(); !o.isMap && top != nil && top.isSwitch && f.lineHead != nil &&
			(f.lineHead.Kind == token.CASE || f.lineHead.Kind == token.DEFAULT) {
			o.caseBlock = true
		}
		f.stack = append(f.stack, o)
	case token.RPAREN, token.RBRACK, token.RBRACE:
		if n := len(f.stack); n > 0 {
			f.lastClosedMap = f.stack[n-1].isMap
			f.closedDo = t.Kind == token.RBRACE && f.stack[n-1].isDo
			f.closedHeader = f.stack[n-1].header
			f.stack = f.stack[:n-1]
		}
	case token.WHILE:
		f.closedDo = false
	case token.CASE, token.DEFAULT:
		if top := f.top(); top != nil && top.isSwitch {
			top.inCase = true
		}
	case token.QUESTION:
		if top := f.top(); top != nil {
			top.question++
		} else {
			f.questionTop++
		}
	case token.COLON:
		f.ternaryColon = true
		if top := f.top(); top != nil && top.question > 0 {
			top.question--
		} else if top == nil && f.questionTop > 0 {
			f.questionTop--
		} else {
			f.ternaryColon = false
		}
	}
	if t.Kind != token.COMMENT {
		f.last, f.lineCode = t, t
	}
	f.prev, f.unary = t, wasUnary
}

// isUnary tells whether t, just written, is a prefix operator.
func (f *formatter) isUnary(t *token.Token) bool {
	switch t.Kind {
	case token.BANG, token.TILDE:
		return true
	case token.MINUS, token.PLUS, token.INC, token.DEC:
		// prefix where no operand comes before it (f.last is still the
		// token before t)
		return f.last == nil || !operandEnd(f.last)
	}
	return false
}

// mapBrace tells whether a "{" (not yet on the stack) starts a map literal:
// it does where an expression is expected.
func (f *formatter) mapBrace() bool {
	p := f.last
	if p == nil {
		return false
	}
	switch p.Kind {
	case token.ASSIGN, token.LPAREN, token.LBRACK, token.COMMA, token.RETURN, token.YIELD, token.QUESTION,
		token.ADD_ASSIGN, token.SUB_ASSIGN, token.MUL_ASSIGN, token.DIV_ASSIGN, token.MOD_ASSIGN,
		token.AND_ASSIGN, token.OR_ASSIGN, token.XOR_ASSIGN, token.SHL_ASSIGN, token.SHR_ASSIGN,
		token.EQ, token.NE, token.LT, token.LE, token.GT, token.GE, token.ANDAND, token.OROR,
		token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT, token.AMP, token.PIPE, token.CARET,
		token.SHL, token.SHR, token.BANG, token.TILDE, token.THROW, token.IN:
		return true
	case token.COLON:
		// "case x: {" and "label: {" are blocks; "k: {" in a map is a map.
		top := f.top()
		return top != nil && top.isMap || top != nil && top.question > 0
	}
	return false
}

// operandEnd tells whether t can end an operand (so that a following "-"
// is binary and "++" is postfix).
func operandEnd(t *token.Token) bool {
	switch t.Kind {
	case token.IDENT, token.INT, token.FLOAT, token.CHAR, token.STRING, token.TRUE, token.FALSE, token.NIL,
		token.THIS, token.RPAREN, token.RBRACK, token.RBRACE, token.INC, token.DEC, token.DOTDOT:
		return true
	}
	return false
}

func isCloser(k token.Kind) bool { return k == token.RPAREN || k == token.RBRACK || k == token.RBRACE }
