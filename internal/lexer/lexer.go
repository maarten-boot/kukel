// Package lexer turns Kukel source text into tokens (syntax.ebnf, section 4).
package lexer

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/maarten-boot/kukel/internal/diag"
	"github.com/maarten-boot/kukel/internal/token"
)

const maxNameLen = 255 // identifiers and float literals

// Error is a lexical error at a source position.
type Error = diag.Error

// Lexer scans one source file.
type Lexer struct {
	file string
	src  []byte
	off  int // current byte offset
	line int // line of src[off]
	col  int // column of src[off]
}

// New returns a lexer for src. A raw NUL byte ends its source line: the bytes
// from the NUL up to the next newline are dropped before scanning.
func New(file string, src []byte) *Lexer {
	return &Lexer{file: file, src: dropAfterNUL(src), line: 1, col: 1}
}

func dropAfterNUL(src []byte) []byte {
	if bytes.IndexByte(src, 0) < 0 {
		return src
	}
	out := make([]byte, 0, len(src))
	for len(src) > 0 {
		i := bytes.IndexByte(src, 0)
		if i < 0 {
			out = append(out, src...)
			break
		}
		out = append(out, src[:i]...)
		src = src[i:]
		nl := bytes.IndexByte(src, '\n')
		if nl < 0 {
			break
		}
		src = src[nl:] // keep the newline
	}
	return out
}

// Tokenize scans the whole of src. The result ends with an EOF token. On a
// lexical error it returns the tokens scanned so far and the error.
func Tokenize(file string, src []byte) ([]token.Token, error) {
	l := New(file, src)
	var toks []token.Token
	for {
		t, err := l.Next()
		if err != nil {
			return toks, err
		}
		toks = append(toks, t)
		if t.Kind == token.EOF {
			return toks, nil
		}
	}
}

func (l *Lexer) pos() token.Pos {
	return token.Pos{File: l.file, Line: l.line, Col: l.col, Offset: l.off}
}

// peek returns the byte n positions ahead, or 0 at the end of the source.
// (The source contains no NUL bytes, so 0 always means "end".)
func (l *Lexer) peek(n int) byte {
	if l.off+n < len(l.src) {
		return l.src[l.off+n]
	}
	return 0
}

func (l *Lexer) advance() {
	if l.src[l.off] == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	l.off++
}

func (l *Lexer) errorf(p token.Pos, format string, args ...any) error {
	return diag.Errorf(p, format, args...)
}

// Next returns the next token, or an error.
func (l *Lexer) Next() (token.Token, error) {
	if err := l.skip(); err != nil {
		return token.Token{}, err
	}
	start := l.pos()
	c := l.peek(0)
	switch {
	case l.off >= len(l.src):
		return token.Token{Kind: token.EOF, Pos: start}, nil
	case isLetter(c):
		return l.name(start)
	case isDigit(c), c == '.' && isDigit(l.peek(1)):
		return l.number(start)
	case c == '\'':
		return l.char(start)
	case c == '"':
		return l.string(start)
	}
	return l.operator(start)
}

// skip skips white space and comments.
func (l *Lexer) skip() error {
	for l.off < len(l.src) {
		switch c := l.peek(0); {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			l.advance()
		case c == '#', c == '/' && l.peek(1) == '/':
			for l.off < len(l.src) && l.peek(0) != '\n' {
				l.advance()
			}
		case c == '/' && l.peek(1) == '*':
			start := l.pos()
			l.advance()
			l.advance()
			for {
				if l.off >= len(l.src) {
					return l.errorf(start, "unterminated comment")
				}
				if l.peek(0) == '*' && l.peek(1) == '/' {
					l.advance()
					l.advance()
					break
				}
				l.advance()
			}
		default:
			return nil
		}
	}
	return nil
}

func (l *Lexer) text(start token.Pos) string { return string(l.src[start.Offset:l.off]) }

func (l *Lexer) name(start token.Pos) (token.Token, error) {
	for isLetter(l.peek(0)) || isDigit(l.peek(0)) {
		l.advance()
	}
	text := l.text(start)
	if len(text) > maxNameLen {
		return token.Token{}, l.errorf(start, "identifier longer than %d characters", maxNameLen)
	}
	return token.Token{Kind: token.Lookup(text), Pos: start, Text: text}, nil
}

// digits scans digits accepted by ok, with single "_" separators between
// digits. It returns the number of digits (separators not counted).
func (l *Lexer) digits(ok func(byte) bool) (int, error) {
	n := 0
	for {
		c := l.peek(0)
		if ok(c) {
			l.advance()
			n++
			continue
		}
		if c == '_' {
			p := l.pos()
			if n == 0 || !ok(l.peek(1)) {
				return n, l.errorf(p, "'_' must stand between two digits")
			}
			l.advance()
			continue
		}
		return n, nil
	}
}

func (l *Lexer) number(start token.Pos) (token.Token, error) {
	if l.peek(0) == '0' && (l.peek(1)|0x20 == 'x' || l.peek(1)|0x20 == 'b') {
		return l.radixNumber(start)
	}

	isFloat := false
	if l.peek(0) != '.' {
		if _, err := l.digits(isDigit); err != nil {
			return token.Token{}, err
		}
	}
	if l.peek(0) == '.' {
		// "1.", "1.5", ".5". Longest match: "1.." is "1." and a lone ".".
		isFloat = true
		l.advance()
		if isDigit(l.peek(0)) {
			if _, err := l.digits(isDigit); err != nil {
				return token.Token{}, err
			}
		}
	}
	if c := l.peek(0); c == 'e' || c == 'E' {
		p := l.pos()
		l.advance()
		if c := l.peek(0); c == '+' || c == '-' {
			l.advance()
		}
		if !isDigit(l.peek(0)) {
			return token.Token{}, l.errorf(p, "exponent has no digits")
		}
		if _, err := l.digits(isDigit); err != nil {
			return token.Token{}, err
		}
		isFloat = true
	}
	if err := l.checkNumberEnd(); err != nil {
		return token.Token{}, err
	}

	text := l.text(start)
	clean := strings.ReplaceAll(text, "_", "")
	if isFloat {
		if len(text) > maxNameLen {
			return token.Token{}, l.errorf(start, "number longer than %d characters", maxNameLen)
		}
		f, err := strconv.ParseFloat(clean, 64)
		switch {
		case err != nil && math.IsInf(f, 0):
			return token.Token{}, l.errorf(start, "Number too large")
		case err != nil && !errors.Is(err, strconv.ErrRange): // underflow gives 0
			return token.Token{}, l.errorf(start, "malformed number %q", text)
		}
		return token.Token{Kind: token.FLOAT, Pos: start, Text: text, Float: f}, nil
	}

	v, err := strconv.ParseUint(clean, 10, 64)
	if err != nil || v > 1<<63 {
		return token.Token{}, l.errorf(start, "integer %s is too large (the maximum is 9223372036854775807)", text)
	}
	return token.Token{Kind: token.INT, Pos: start, Text: text, Int: v, IntMin: v == 1<<63}, nil
}

// radixNumber scans 0x... (1-16 digits) or 0b... (1-64 digits).
func (l *Lexer) radixNumber(start token.Pos) (token.Token, error) {
	hex := l.peek(1)|0x20 == 'x'
	l.advance()
	l.advance()
	ok, base, max, what := isBinDigit, 2, 64, "binary"
	if hex {
		ok, base, max, what = isHexDigit, 16, 16, "hexadecimal"
	}
	if l.peek(0) == '_' {
		return token.Token{}, l.errorf(l.pos(), "'_' must stand between two digits")
	}
	n, err := l.digits(ok)
	if err != nil {
		return token.Token{}, err
	}
	if n == 0 {
		return token.Token{}, l.errorf(start, "%s literal has no digits", what)
	}
	if err := l.checkNumberEnd(); err != nil {
		return token.Token{}, err
	}
	text := l.text(start)
	if n > max {
		return token.Token{}, l.errorf(start, "%s literal has more than %d digits", what, max)
	}
	v, err := strconv.ParseUint(strings.ReplaceAll(text[2:], "_", ""), base, 64)
	if err != nil {
		return token.Token{}, l.errorf(start, "malformed number %q", text)
	}
	return token.Token{Kind: token.INT, Pos: start, Text: text, Int: v}, nil
}

// checkNumberEnd rejects a number directly followed by a letter, digit or
// "_" (such as 0x1g, 0b102 or 12abc).
func (l *Lexer) checkNumberEnd() error {
	if c := l.peek(0); isLetter(c) || isDigit(c) {
		return l.errorf(l.pos(), "invalid character %q in number", c)
	}
	return nil
}

func (l *Lexer) char(start token.Pos) (token.Token, error) {
	l.advance() // '
	var code byte
	switch c := l.peek(0); {
	case l.off >= len(l.src):
		return token.Token{}, l.errorf(start, "unterminated character literal")
	case c == '\'':
		return token.Token{}, l.errorf(start, "empty character literal (write '\\'' for the quote character)")
	case c == '\\':
		b, err := l.escape()
		if err != nil {
			return token.Token{}, err
		}
		code = b
	default:
		code = c
		l.advance()
	}
	if l.peek(0) != '\'' {
		if l.off >= len(l.src) {
			return token.Token{}, l.errorf(start, "unterminated character literal")
		}
		return token.Token{}, l.errorf(start, "character literal must contain exactly one character")
	}
	l.advance()
	return token.Token{Kind: token.CHAR, Pos: start, Text: l.text(start), Int: uint64(code)}, nil
}

func (l *Lexer) string(start token.Pos) (token.Token, error) {
	l.advance() // "
	var b strings.Builder
	for {
		if l.off >= len(l.src) {
			return token.Token{}, l.errorf(start, "unterminated string")
		}
		switch c := l.peek(0); c {
		case '"':
			l.advance()
			return token.Token{Kind: token.STRING, Pos: start, Text: l.text(start), Str: b.String()}, nil
		case '\\':
			e, err := l.escape()
			if err != nil {
				return token.Token{}, err
			}
			b.WriteByte(e)
		default:
			b.WriteByte(c)
			l.advance()
		}
	}
}

// escape scans an escape sequence starting at "\" and returns its byte.
func (l *Lexer) escape() (byte, error) {
	p := l.pos()
	l.advance() // backslash
	c := l.peek(0)
	if l.off >= len(l.src) {
		return 0, l.errorf(p, "unterminated escape sequence")
	}
	l.advance()
	switch c {
	case 'n':
		return '\n', nil
	case 't':
		return '\t', nil
	case 'r':
		return '\r', nil
	case '0':
		return 0, nil
	case '\\', '"', '\'':
		return c, nil
	case 'x':
		if !isHexDigit(l.peek(0)) {
			return 0, l.errorf(p, "\\x must be followed by one or two hexadecimal digits")
		}
		v := hexVal(l.peek(0))
		l.advance()
		if isHexDigit(l.peek(0)) {
			v = v<<4 | hexVal(l.peek(0))
			l.advance()
		}
		return v, nil
	}
	return 0, l.errorf(p, "unknown escape sequence \\%s", printable(c))
}

// Operators, longest first within each starting character.
var operators = []struct {
	text string
	kind token.Kind
}{
	{"<<=", token.SHL_ASSIGN}, {">>=", token.SHR_ASSIGN},
	{"<=", token.LE}, {">=", token.GE}, {"==", token.EQ}, {"!=", token.NE},
	{"<<", token.SHL}, {">>", token.SHR}, {"&&", token.ANDAND}, {"||", token.OROR},
	{"++", token.INC}, {"--", token.DEC}, {"->", token.ARROW}, {"::", token.SCOPE},
	{"..", token.DOTDOT},
	{"+=", token.ADD_ASSIGN}, {"-=", token.SUB_ASSIGN}, {"*=", token.MUL_ASSIGN},
	{"/=", token.DIV_ASSIGN}, {"%=", token.MOD_ASSIGN}, {"&=", token.AND_ASSIGN},
	{"|=", token.OR_ASSIGN}, {"^=", token.XOR_ASSIGN},
	{"+", token.PLUS}, {"-", token.MINUS}, {"*", token.STAR}, {"/", token.SLASH},
	{"%", token.PERCENT}, {"&", token.AMP}, {"|", token.PIPE}, {"^", token.CARET},
	{"~", token.TILDE}, {"!", token.BANG}, {"<", token.LT}, {">", token.GT},
	{"=", token.ASSIGN}, {"?", token.QUESTION}, {":", token.COLON}, {",", token.COMMA},
	{";", token.SEMICOLON}, {"(", token.LPAREN}, {")", token.RPAREN},
	{"[", token.LBRACK}, {"]", token.RBRACK}, {"{", token.LBRACE}, {"}", token.RBRACE},
}

func (l *Lexer) operator(start token.Pos) (token.Token, error) {
	rest := l.src[l.off:]
	for _, op := range operators {
		if len(rest) >= len(op.text) && string(rest[:len(op.text)]) == op.text {
			for range op.text {
				l.advance()
			}
			return token.Token{Kind: op.kind, Pos: start, Text: op.text}, nil
		}
	}
	c := l.peek(0)
	if c == '.' {
		return token.Token{}, l.errorf(start, "unexpected '.' (a lone '.' is not a token; did you mean '..' or '->'?)")
	}
	return token.Token{}, l.errorf(start, "unexpected character %s", printable(c))
}

func isLetter(c byte) bool   { return 'a' <= c|0x20 && c|0x20 <= 'z' || c == '_' }
func isDigit(c byte) bool    { return '0' <= c && c <= '9' }
func isBinDigit(c byte) bool { return c == '0' || c == '1' }
func isHexDigit(c byte) bool { return isDigit(c) || 'a' <= c|0x20 && c|0x20 <= 'f' }
func hexVal(c byte) byte {
	if isDigit(c) {
		return c - '0'
	}
	return c | 0x20 - 'a' + 10
}

// printable quotes a byte for an error message.
func printable(c byte) string {
	if c >= 0x20 && c < 0x7f {
		return fmt.Sprintf("'%c'", c)
	}
	return fmt.Sprintf("0x%02X", c)
}
