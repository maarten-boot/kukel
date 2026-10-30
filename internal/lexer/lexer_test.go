package lexer

import (
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/token"
)

// kinds tokenizes src and returns the token kinds, without the final EOF.
func kinds(t *testing.T, src string) []token.Kind {
	t.Helper()
	toks, err := Tokenize("t.kkl", []byte(src))
	if err != nil {
		t.Fatalf("Tokenize(%q): %v", src, err)
	}
	var ks []token.Kind
	for _, tk := range toks[:len(toks)-1] {
		ks = append(ks, tk.Kind)
	}
	return ks
}

func one(t *testing.T, src string) token.Token {
	t.Helper()
	toks, err := Tokenize("t.kkl", []byte(src))
	if err != nil {
		t.Fatalf("Tokenize(%q): %v", src, err)
	}
	if len(toks) != 2 {
		t.Fatalf("Tokenize(%q): got %d tokens, want 1", src, len(toks)-1)
	}
	return toks[0]
}

func TestOperatorsLongestMatch(t *testing.T) {
	tests := []struct {
		src  string
		want []token.Kind
	}{
		{"<<= << <= <", []token.Kind{token.SHL_ASSIGN, token.SHL, token.LE, token.LT}},
		{">>= >> >= >", []token.Kind{token.SHR_ASSIGN, token.SHR, token.GE, token.GT}},
		{":: :", []token.Kind{token.SCOPE, token.COLON}},
		{":::", []token.Kind{token.SCOPE, token.COLON}},
		{"a..", []token.Kind{token.IDENT, token.DOTDOT}},
		{"++ + += --- -> -=", []token.Kind{token.INC, token.PLUS, token.ADD_ASSIGN, token.DEC, token.MINUS, token.ARROW, token.SUB_ASSIGN}},
		{"&& & &= || | |= ^ ^= ~ ! != = ==", []token.Kind{token.ANDAND, token.AMP, token.AND_ASSIGN, token.OROR, token.PIPE, token.OR_ASSIGN,
			token.CARET, token.XOR_ASSIGN, token.TILDE, token.BANG, token.NE, token.ASSIGN, token.EQ}},
		{"* *= / /= % %= ? , ; ( ) [ ] { }", []token.Kind{token.STAR, token.MUL_ASSIGN, token.SLASH, token.DIV_ASSIGN, token.PERCENT, token.MOD_ASSIGN,
			token.QUESTION, token.COMMA, token.SEMICOLON, token.LPAREN, token.RPAREN, token.LBRACK, token.RBRACK, token.LBRACE, token.RBRACE}},
	}
	for _, tt := range tests {
		got := kinds(t, tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%q: got %v, want %v", tt.src, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%q: got %v, want %v", tt.src, got, tt.want)
				break
			}
		}
	}
}

func TestKeywordsAndIdentifiers(t *testing.T) {
	for _, kw := range []string{"break", "case", "catch", "class", "const", "continue", "default", "do",
		"else", "false", "finally", "fn", "for", "global", "if", "in", "local", "new", "nil",
		"return", "static", "switch", "this", "throw", "true", "try", "while"} {
		tk := one(t, kw)
		if !tk.Kind.IsKeyword() || tk.Kind.String() != kw {
			t.Errorf("%q: got %v, want keyword", kw, tk.Kind)
		}
	}
	for _, id := range []string{"x", "_", "_a1", "iffy", "This", "in_", "fnord", strings.Repeat("a", 255)} {
		if tk := one(t, id); tk.Kind != token.IDENT || tk.Text != id {
			t.Errorf("%q: got %v %q, want IDENT", id, tk.Kind, tk.Text)
		}
	}
}

func TestIntegers(t *testing.T) {
	tests := []struct {
		src    string
		want   uint64
		intMin bool
	}{
		{"0", 0, false},
		{"42", 42, false},
		{"007", 7, false},
		{"1_000_000", 1000000, false},
		{"9223372036854775807", 1<<63 - 1, false},
		{"9223372036854775808", 1 << 63, true},
		{"0xFF", 255, false},
		{"0Xff_ff", 0xffff, false},
		{"0xFFFFFFFFFFFFFFFF", 1<<64 - 1, false},
		{"0b1010", 10, false},
		{"0B1010_0101", 0xa5, false},
		{"0b" + strings.Repeat("1", 64), 1<<64 - 1, false},
	}
	for _, tt := range tests {
		tk := one(t, tt.src)
		if tk.Kind != token.INT || tk.Int != tt.want || tk.IntMin != tt.intMin || tk.Text != tt.src {
			t.Errorf("%q: got %v %d min=%v, want INT %d min=%v", tt.src, tk.Kind, tk.Int, tk.IntMin, tt.want, tt.intMin)
		}
	}
}

func TestFloats(t *testing.T) {
	tests := []struct {
		src  string
		want float64
	}{
		{"1.5", 1.5},
		{"1.", 1},
		{".5", 0.5},
		{"2e10", 2e10},
		{"1.5E-3", 1.5e-3},
		{"1.e2", 100},
		{"1_000.25", 1000.25},
		{"1e1_0", 1e10},
		{"99999999999999999999.0", 1e20}, // integer part beyond 2^63
		{"1e-400", 0},                    // underflow is not an error
	}
	for _, tt := range tests {
		tk := one(t, tt.src)
		if tk.Kind != token.FLOAT || tk.Float != tt.want {
			t.Errorf("%q: got %v %g, want FLOAT %g", tt.src, tk.Kind, tk.Float, tt.want)
		}
	}
}

func TestCharsAndStrings(t *testing.T) {
	chars := map[string]uint64{
		`'a'`: 'a', `' '`: ' ', `'"'`: '"', `'\''`: '\'', `'\\'`: '\\', `'\n'`: '\n',
		`'\t'`: '\t', `'\r'`: '\r', `'\0'`: 0, `'\x41'`: 'A', `'\xf'`: 15, "'\n'": '\n',
	}
	for src, want := range chars {
		if tk := one(t, src); tk.Kind != token.CHAR || tk.Int != want {
			t.Errorf("%s: got %v %d, want CHAR %d", src, tk.Kind, tk.Int, want)
		}
	}
	strs := map[string]string{
		`""`:                     "",
		`"abc"`:                  "abc",
		`"a\"b\\c\'d"`:           `a"b\c'd`,
		`"\n\t\r\0"`:             "\n\t\r\x00",
		`"\x41\x4a\x4AB"`:        "AJJB",
		`"\x7z"`:                 "\x07z",
		"\"two\nlines\"":         "two\nlines",
		`"it's 'quoted'"`:        "it's 'quoted'",
		"\"\xc3\xa9t\xc3\xa9\"":  "\xc3\xa9t\xc3\xa9", // UTF-8 bytes pass through
		`"\u{E9}t\u{e9}"`:        "\xc3\xa9t\xc3\xa9", // a character, written as UTF-8
		`"\u{41}\u{0}\u{7F}"`:    "A\x00\x7f",
		`"\u{20AC}\u{1F600}"`:    "€😀",
		`"\u{00000A}\u{10FFFF}"`: "\n\U0010FFFF", // six digits, leading zeros included
		`"\u{FFFD}\xff"`:         "\ufffd\xff",
	}
	for src, want := range strs {
		if tk := one(t, src); tk.Kind != token.STRING || tk.Str != want || tk.Text != src {
			t.Errorf("%s: got %v %q, want STRING %q", src, tk.Kind, tk.Str, want)
		}
	}
	// Adjacent strings are not joined.
	if ks := kinds(t, `"a" "b"`); len(ks) != 2 {
		t.Errorf(`"a" "b": got %v, want two STRING tokens`, ks)
	}
}

func TestCommentsAndWhiteSpace(t *testing.T) {
	src := "#!/usr/bin/env kukel\n" +
		"a // line comment\n" +
		"b # hash comment\n" +
		"/* block\n comment */ c\v\f\r\n" +
		"d /**/ e /* * / ** */ f // at end of file"
	toks, err := Tokenize("t.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tk := range toks {
		if tk.Kind == token.IDENT {
			names = append(names, tk.Text)
		}
	}
	if got := strings.Join(names, " "); got != "a b c d e f" {
		t.Errorf("got %q, want %q", got, "a b c d e f")
	}
	// "/" followed by something else is still division.
	if ks := kinds(t, "a / b /= c"); len(ks) != 5 || ks[1] != token.SLASH || ks[3] != token.DIV_ASSIGN {
		t.Errorf("division: got %v", ks)
	}
}

func TestPositions(t *testing.T) {
	src := "a\n  bb /* x\ny */ c\n\t\"s\ns\" d"
	toks, err := Tokenize("f.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"f.kkl:1:1", "f.kkl:2:3", "f.kkl:3:6", "f.kkl:4:2", "f.kkl:5:4", "f.kkl:5:5"}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(toks), len(want))
	}
	for i, tk := range toks {
		if tk.Pos.String() != want[i] {
			t.Errorf("token %d (%q): position %s, want %s", i, tk.Text, tk.Pos, want[i])
		}
	}
}

func TestNULEndsLine(t *testing.T) {
	// Everything from a raw NUL byte to the end of its line is dropped.
	src := "a\x00 b c\nd \"x\x00\ny\" e\x00"
	toks, err := Tokenize("t.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tk := range toks[:len(toks)-1] {
		got = append(got, tk.Kind.String()+":"+tk.Text)
	}
	want := "IDENT:a IDENT:d STRING:\"x\ny\" IDENT:e"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		src, pos, msg string
	}{
		{"a /* open", "t.kkl:1:3", "unterminated comment"},
		{`"abc`, "t.kkl:1:1", "unterminated string"},
		{`x = "a\qb"`, "t.kkl:1:7", `unknown escape sequence \q`},
		{"x = \"a\\\x01\"", "t.kkl:1:7", `unknown escape sequence: \ followed by 0x01`},
		{`"\x"`, "t.kkl:1:2", `\x must be followed`},
		{`"a\u00E9"`, "t.kkl:1:3", `\u must be followed by hexadecimal digits in braces, as in \u{E9}`},
		{`"\u{}"`, "t.kkl:1:2", `\u{...} takes 1 to 6 hexadecimal digits`},
		{`"\u{0000041}"`, "t.kkl:1:2", `\u{...} takes 1 to 6 hexadecimal digits`},
		{`"\u{E9"`, "t.kkl:1:2", `\u{ must be closed by } after its hexadecimal digits, not followed by '"'`},
		{`"\u{E 9}"`, "t.kkl:1:2", `not followed by ' '`},
		{`"\u{E9`, "t.kkl:1:2", "unterminated escape sequence"},
		{`"\u{110000}"`, "t.kkl:1:2", `\u{110000} is beyond the last Unicode character, 10FFFF`},
		{`"\u{FFFFFF}"`, "t.kkl:1:2", `\u{FFFFFF} is beyond the last Unicode character`},
		{`"\u{D800}"`, "t.kkl:1:2", `\u{D800} is a surrogate, not a character`},
		{`"\u{dfff}"`, "t.kkl:1:2", `\u{DFFF} is a surrogate`},
		{`'\u{E9}'`, "t.kkl:1:2", `a character literal is one byte: \u{...} is for strings (utf8::code("\u{E9}") gives its code)`},
		{`'\u{41}'`, "t.kkl:1:2", `a character literal is one byte`},
		{"'''", "t.kkl:1:1", "empty character literal"},
		{"''", "t.kkl:1:1", "empty character literal"},
		{"'ab'", "t.kkl:1:1", "exactly one character"},
		{"'a", "t.kkl:1:1", "unterminated character literal"},
		{"a.b", "t.kkl:1:2", "lone '.'"},
		{"1..", "t.kkl:1:3", "a number followed by '..' reads as a float and a lone '.': only a vector can be spread"},
		{"f(1_0..)", "t.kkl:1:7", "a number followed by '..'"},
		{"1__0", "t.kkl:1:2", "'_' must stand between two digits"},
		{"1_", "t.kkl:1:2", "'_' must stand between two digits"},
		{"0x_1", "t.kkl:1:3", "'_' must stand between two digits"},
		{"0x", "t.kkl:1:1", "hexadecimal literal has no digits"},
		{"0b", "t.kkl:1:1", "binary literal has no digits"},
		{"0b2", "t.kkl:1:3", "invalid digit 2 in binary literal"},
		{"0b12", "t.kkl:1:4", "invalid digit 2 in binary literal"},
		{"0x1g", "t.kkl:1:4", "invalid digit g in hexadecimal literal"},
		{"0xg", "t.kkl:1:3", "invalid digit g in hexadecimal literal"},
		{"12abc", "t.kkl:1:3", "invalid character 'a' in number"},
		{"1e", "t.kkl:1:2", "exponent has no digits"},
		{"1e+", "t.kkl:1:2", "exponent has no digits"},
		{"0x" + strings.Repeat("F", 17), "t.kkl:1:1", "more than 16 digits"},
		{"0b" + strings.Repeat("1", 65), "t.kkl:1:1", "more than 64 digits"},
		{"9223372036854775809", "t.kkl:1:1", "too large"},
		{"99999999999999999999999", "t.kkl:1:1", "too large"},
		{"1e999", "t.kkl:1:1", "number too large"},
		{strings.Repeat("a", 256), "t.kkl:1:1", "identifier longer than 255"},
		{"1." + strings.Repeat("0", 254), "t.kkl:1:1", "number longer than 255"},
		{"a @ b", "t.kkl:1:3", "unexpected character '@'"},
		{"a \xc3\xa9", "t.kkl:1:3", "unexpected character 0xC3"},
		{"a\n  $", "t.kkl:2:3", "unexpected character '$'"},
	}
	for _, tt := range tests {
		_, err := Tokenize("t.kkl", []byte(tt.src))
		if err == nil {
			t.Errorf("%q: no error, want %q", tt.src, tt.msg)
			continue
		}
		e, ok := err.(*Error)
		if !ok || e.Pos.String() != tt.pos || !strings.Contains(e.Msg, tt.msg) {
			t.Errorf("%q: got %v, want %s: ...%s...", tt.src, err, tt.pos, tt.msg)
		}
	}
}

// FuzzLexer checks that the lexer never panics, always terminates, and that
// token positions move forward.
func FuzzLexer(f *testing.F) {
	for _, s := range []string{
		"", "main(args) { return 0; }", "a <<= 0xFF_FF + 0b1 - 1.5e-3 .. ->",
		`"a\x41\n" '\'' /* c */ // d`, `"\u{E9}\u{1F600}" "\u{D800}" "\u{"`, "# x\n1__0", "'''", "\x00\n\x00", "1..2", ".5.5",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		l := New("f.kkl", src)
		last := -1
		for i := 0; ; i++ {
			if i > len(src)+1 {
				t.Fatalf("more tokens than bytes in %q", src)
			}
			tk, err := l.Next()
			if err != nil {
				return
			}
			if tk.Pos.Offset <= last && tk.Kind != token.EOF {
				t.Fatalf("position did not advance at token %d in %q", i, src)
			}
			last = tk.Pos.Offset
			if tk.Kind == token.EOF {
				return
			}
		}
	})
}
