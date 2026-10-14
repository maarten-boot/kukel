// Package token defines the lexical tokens of Kukel (syntax.ebnf, section 4).
package token

import "fmt"

// Kind is the kind of a token.
type Kind int

const (
	ILLEGAL Kind = iota
	EOF
	COMMENT // only from a lexer with KeepComments (kukel fmt)

	// Literals and names.
	IDENT  // main
	INT    // 42, 0xFF, 0b1010
	FLOAT  // 1.5, .5, 2e10
	CHAR   // 'a'
	STRING // "abc"

	// Operators and punctuation.
	PLUS      // +
	MINUS     // -
	STAR      // *
	SLASH     // /
	PERCENT   // %
	AMP       // &
	PIPE      // |
	CARET     // ^
	TILDE     // ~
	BANG      // !
	LT        // <
	GT        // >
	ASSIGN    // =
	QUESTION  // ?
	COLON     // :
	COMMA     // ,
	SEMICOLON // ;
	LPAREN    // (
	RPAREN    // )
	LBRACK    // [
	RBRACK    // ]
	LBRACE    // {
	RBRACE    // }

	LE     // <=
	GE     // >=
	EQ     // ==
	NE     // !=
	SHL    // <<
	SHR    // >>
	ANDAND // &&
	OROR   // ||
	INC    // ++
	DEC    // --
	ARROW  // ->
	SCOPE  // ::
	DOTDOT // ..

	ADD_ASSIGN // +=
	SUB_ASSIGN // -=
	MUL_ASSIGN // *=
	DIV_ASSIGN // /=
	MOD_ASSIGN // %=
	AND_ASSIGN // &=
	OR_ASSIGN  // |=
	XOR_ASSIGN // ^=
	SHL_ASSIGN // <<=
	SHR_ASSIGN // >>=

	// Keywords.
	keywordBeg
	BREAK
	CASE
	CATCH
	CLASS
	CONST
	CONTINUE
	DEFAULT
	DO
	ELSE
	FALSE
	FINALLY
	FN
	FOR
	GLOBAL
	IF
	IN
	LOCAL
	NEW
	NIL
	RETURN
	STATIC
	SWITCH
	THIS
	THROW
	TRUE
	TRY
	USE
	WHILE
	WITH
	keywordEnd
)

var names = [...]string{
	ILLEGAL: "ILLEGAL",
	EOF:     "EOF",
	COMMENT: "COMMENT",

	IDENT:  "IDENT",
	INT:    "INT",
	FLOAT:  "FLOAT",
	CHAR:   "CHAR",
	STRING: "STRING",

	PLUS:      "+",
	MINUS:     "-",
	STAR:      "*",
	SLASH:     "/",
	PERCENT:   "%",
	AMP:       "&",
	PIPE:      "|",
	CARET:     "^",
	TILDE:     "~",
	BANG:      "!",
	LT:        "<",
	GT:        ">",
	ASSIGN:    "=",
	QUESTION:  "?",
	COLON:     ":",
	COMMA:     ",",
	SEMICOLON: ";",
	LPAREN:    "(",
	RPAREN:    ")",
	LBRACK:    "[",
	RBRACK:    "]",
	LBRACE:    "{",
	RBRACE:    "}",

	LE:     "<=",
	GE:     ">=",
	EQ:     "==",
	NE:     "!=",
	SHL:    "<<",
	SHR:    ">>",
	ANDAND: "&&",
	OROR:   "||",
	INC:    "++",
	DEC:    "--",
	ARROW:  "->",
	SCOPE:  "::",
	DOTDOT: "..",

	ADD_ASSIGN: "+=",
	SUB_ASSIGN: "-=",
	MUL_ASSIGN: "*=",
	DIV_ASSIGN: "/=",
	MOD_ASSIGN: "%=",
	AND_ASSIGN: "&=",
	OR_ASSIGN:  "|=",
	XOR_ASSIGN: "^=",
	SHL_ASSIGN: "<<=",
	SHR_ASSIGN: ">>=",

	BREAK:    "break",
	CASE:     "case",
	CATCH:    "catch",
	CLASS:    "class",
	CONST:    "const",
	CONTINUE: "continue",
	DEFAULT:  "default",
	DO:       "do",
	ELSE:     "else",
	FALSE:    "false",
	FINALLY:  "finally",
	FN:       "fn",
	FOR:      "for",
	GLOBAL:   "global",
	IF:       "if",
	IN:       "in",
	LOCAL:    "local",
	NEW:      "new",
	NIL:      "nil",
	RETURN:   "return",
	STATIC:   "static",
	SWITCH:   "switch",
	THIS:     "this",
	THROW:    "throw",
	TRUE:     "true",
	TRY:      "try",
	USE:      "use",
	WHILE:    "while",
	WITH:     "with",
}

// String returns the name of a literal kind (IDENT, INT, ...) or the source
// spelling of an operator or keyword.
func (k Kind) String() string {
	if k >= 0 && int(k) < len(names) && names[k] != "" {
		return names[k]
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// IsKeyword reports whether k is a keyword.
func (k Kind) IsKeyword() bool { return k > keywordBeg && k < keywordEnd }

// IsLiteral reports whether k is a literal (INT, FLOAT, CHAR or STRING).
func (k Kind) IsLiteral() bool { return k >= INT && k <= STRING }

var keywords map[string]Kind

func init() {
	keywords = make(map[string]Kind, keywordEnd-keywordBeg)
	for k := keywordBeg + 1; k < keywordEnd; k++ {
		keywords[names[k]] = k
	}
}

// Lookup returns the keyword kind for name, or IDENT.
func Lookup(name string) Kind {
	if k, ok := keywords[name]; ok {
		return k
	}
	return IDENT
}

// Pos is a source position. Line and Col are 1-based; Col counts bytes.
type Pos struct {
	File   string
	Line   int
	Col    int
	Offset int // byte offset in the (NUL-filtered) source
}

func (p Pos) String() string {
	if p.File == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Token is one lexical token.
type Token struct {
	Kind Kind
	Pos  Pos
	Text string // the source text of the token

	// Values of literals:
	//   INT:    Int holds the 64-bit pattern. IntMin is set for the decimal
	//           literal 9223372036854775808 (2^63), which is valid only
	//           directly after a unary minus (the parser checks that).
	//   CHAR:   Int holds the character code.
	//   FLOAT:  Float.
	//   STRING: Str holds the decoded bytes.
	Int    uint64
	IntMin bool
	Float  float64
	Str    string
}
