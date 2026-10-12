package parser

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"kukel/internal/ast"
	"kukel/internal/diag"
)

// sexpr writes an expression compactly, with explicit structure:
// "a + b * c" is "(+ a (* b c))".
func sexpr(x ast.Expr) string {
	list := func(op string, xs ...ast.Expr) string {
		s := make([]string, 0, len(xs)+1)
		if op != "" {
			s = append(s, op)
		}
		for _, x := range xs {
			s = append(s, sexpr(x))
		}
		return "(" + strings.Join(s, " ") + ")"
	}
	switch x := x.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.IntLit:
		return x.Text
	case *ast.FloatLit:
		return x.Text
	case *ast.CharLit:
		return x.Text
	case *ast.StringLit:
		return x.Text
	case *ast.BoolLit:
		return fmt.Sprint(x.Value)
	case *ast.NilLit:
		return "nil"
	case *ast.ThisExpr:
		return "this"
	case *ast.VectorLit:
		return "[" + strings.TrimSuffix(strings.TrimPrefix(list("", x.Elems...), "("), ")") + "]"
	case *ast.MapLit:
		s := make([]string, len(x.Entries))
		for i, e := range x.Entries {
			s[i] = sexpr(e.Key) + ":" + sexpr(e.Value)
		}
		return "{" + strings.Join(s, " ") + "}"
	case *ast.NewExpr:
		return list("new "+x.Class.Name, x.Args...)
	case *ast.FuncLit:
		return "fn"
	case *ast.StaticRef:
		return x.Class.Name + "::" + x.Name.Name
	case *ast.Spread:
		return list("..", x.X)
	case *ast.CommaExpr:
		return list(",", x.List...)
	case *ast.AssignExpr:
		return list(x.Op.String(), x.LHS, x.RHS)
	case *ast.CondExpr:
		return list("?", x.Cond, x.Then, x.Else)
	case *ast.BinaryExpr:
		return list(x.Op.String(), x.X, x.Y)
	case *ast.UnaryExpr:
		return list(x.Op.String(), x.X)
	case *ast.IncDecExpr:
		if x.Prefix {
			return list(x.Op.String()+"pre", x.X)
		}
		return list(x.Op.String()+"post", x.X)
	case *ast.CallExpr:
		return list("call", append([]ast.Expr{x.Fun}, x.Args...)...)
	case *ast.IndexExpr:
		return list("index", x.X, x.Index)
	case *ast.MethodCall:
		return list("method", append([]ast.Expr{x.X, x.Name}, x.Args...)...)
	case *ast.MemberExpr:
		return list("member", x.X, x.Name)
	}
	return fmt.Sprintf("?%T", x)
}

func TestExpressions(t *testing.T) {
	tests := []struct{ src, want string }{
		// precedence and associativity
		{"a + b * c", "(+ a (* b c))"},
		{"a * b + c", "(+ (* a b) c)"},
		{"a - b - c", "(- (- a b) c)"},
		{"a / b % c * d", "(* (% (/ a b) c) d)"},
		{"a || b && c | d ^ e & f == g < h << i + j * k",
			"(|| a (&& b (| c (^ d (& e (== f (< g (<< h (+ i (* j k))))))))))"},
		{"a * b + c << d < e == f & g ^ h | i && j || k",
			"(|| (&& (| (^ (& (== (< (<< (+ (* a b) c) d) e) f) g) h) i) j) k)"},
		{"a < b == c > d", "(== (< a b) (> c d))"},
		{"a <= b != c >= d", "(!= (<= a b) (>= c d))"},
		{"a >> b << c", "(<< (>> a b) c)"},
		{"(a + b) * c", "(* (+ a b) c)"},
		// assignment, conditional, comma
		{"a = b = c", "(= a (= b c))"},
		{"a += b -= c", "(+= a (-= b c))"},
		{"a <<= b >>= c &= d |= e ^= f *= g /= h %= i",
			"(<<= a (>>= b (&= c (|= d (^= e (*= f (/= g (%= h i))))))))"},
		{"a += b ? c : d", "(+= a (? b c d))"},
		{"a ? b, c : d = e", "(? a (, b c) (= d e))"},
		{"a ? b : c ? d : e", "(? a b (? c d e))"},
		{"a = b ? c : d", "(= a (? b c d))"},
		{"a || b ? c : d", "(? (|| a b) c d)"},
		{"x = y, z", "(, (= x y) z)"},
		// unary operators and literal folding
		{"-x", "(- x)"},
		{"-5", "-5"},
		{"-5 - 3", "(- -5 3)"},
		{"a-5", "(- a 5)"},
		{"- -5", "(- -5)"},
		{"- -x", "(- (- x))"},
		{"!!x", "(! (! x))"},
		{"!-x", "(! (- x))"},
		{"~a & !b", "(& (~ a) (! b))"},
		{"-x * y", "(* (- x) y)"},
		{"-5[0]", "(- (index 5 0))"},
		{"-5(1)", "(- (call 5 1))"},
		{"-1.5e3 * -'a'", "(* -1.5e3 -'a')"},
		{"--x", "(--pre x)"},
		{"++x--", "(++pre (--post x))"},
		{"x++ + ++y", "(+ (++post x) (++pre y))"},
		{"-x++", "(- (++post x))"},
		// postfix operators
		{"f(a, b..)(c)", "(call (call f a (.. b)) c)"},
		{"f()", "(call f)"},
		{"a[i][j]", "(index (index a i) j)"},
		{"a[i, j]", "(index a (, i j))"},
		{"a->b->c(1)", "(method (member a b) c 1)"},
		{"a->m()->n(x, y..)", "(method (method a m) n x (.. y))"},
		{"(p->f)(x)", "(call (member p f) x)"},
		{"this->x", "(member this x)"},
		{"new C(1)->m()", "(method (new C 1) m)"},
		{"new C()", "(new C)"},
		{"new C(v..)[0]", "(index (new C (.. v)) 0)"},
		{"C::x = C::f(1)", "(= C::x (call C::f 1))"},
		// literals
		{"[1, v.., [2]]", "[1 (.. v) [2]]"},
		{"[]", "[]"},
		{"{}", "{}"},
		{`{a: 1, "k": [2], 1 ? 2 : 3: x = 4}`, `{a:1 "k":[2] (? 1 2 3):(= x 4)}`},
		{"fn(a; t) { return a; }(1)", "(call fn 1)"},
		{"true && false || nil", "(|| (&& true false) nil)"},
		{`"a" + 'b'`, `(+ "a" 'b')`},
		{"0xFF | 0b1010_0101", "(| 0xFF 0b1010_0101)"},
	}
	for _, tt := range tests {
		x, err := ParseExpr(tt.src)
		if err != nil {
			t.Errorf("%q: %v", tt.src, err)
			continue
		}
		if got := sexpr(x); got != tt.want {
			t.Errorf("%q:\n got  %s\n want %s", tt.src, got, tt.want)
		}
	}
}

func TestLiteralValues(t *testing.T) {
	ints := map[string]int64{
		"9223372036854775807":  math.MaxInt64,
		"-9223372036854775808": math.MinInt64,
		"0xFFFFFFFFFFFFFFFF":   -1,
		"-0x8000000000000000":  math.MinInt64, // wraps
		"-0b11":                -3,
		"-'a'":                 -97,
	}
	for src, want := range ints {
		x, err := ParseExpr(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		var got int64
		switch x := x.(type) {
		case *ast.IntLit:
			got = x.Value
		case *ast.CharLit:
			got = x.Value
		default:
			t.Errorf("%q: got %T, want a literal", src, x)
			continue
		}
		if got != want {
			t.Errorf("%q: got %d, want %d", src, got, want)
		}
	}
	x, err := ParseExpr("-2.5")
	if err != nil || x.(*ast.FloatLit).Value != -2.5 {
		t.Errorf("-2.5: got %v, %v", x, err)
	}
}

func TestStatementShapes(t *testing.T) {
	src := `
main() {
    if (a) x; elseif (b) y; elseif (c) z; else w;
    for (local k in v) ;
    for (local k, v in m) ;
    for (local i = 0, j; i < j; i++) ;
    for (i = 0; ; ) ;
    for (;;) ;
    lbl: while (1) { break lbl; continue; }
    { a: 1; }
    try { } catch (e) { }
    try { } finally { }
    try { } catch (e) { } finally { }
}`
	f, err := ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	body := f.Decls[0].(*ast.FuncDecl).Body.Stmts

	// elseif chains become nested ifs
	s := body[0].(*ast.IfStmt)
	s2 := s.Else.(*ast.IfStmt)
	s3 := s2.Else.(*ast.IfStmt)
	if sexpr(s2.Cond) != "b" || sexpr(s3.Cond) != "c" || s3.Else.(*ast.ExprStmt).X.(*ast.Ident).Name != "w" {
		t.Errorf("elseif chain parsed wrongly")
	}
	if fe := body[1].(*ast.ForEachStmt); fe.Key.Name != "k" || fe.Value != nil {
		t.Errorf("for-each with one name: %+v", fe)
	}
	if fe := body[2].(*ast.ForEachStmt); fe.Key.Name != "k" || fe.Value.Name != "v" {
		t.Errorf("for-each with two names: %+v", fe)
	}
	if fs := body[3].(*ast.ForStmt); len(fs.InitVars) != 2 || fs.InitVars[1].Value != nil || fs.Cond == nil || fs.Post == nil {
		t.Errorf("for with locals: %+v", fs)
	}
	if fs := body[4].(*ast.ForStmt); fs.InitVars != nil || fs.Init == nil || fs.Cond != nil || fs.Post != nil {
		t.Errorf("for with expression init: %+v", fs)
	}
	if fs := body[5].(*ast.ForStmt); fs.Init != nil || fs.Cond != nil || fs.Post != nil {
		t.Errorf("for (;;): %+v", fs)
	}
	if ls := body[6].(*ast.LabeledStmt); ls.Label.Name != "lbl" {
		t.Errorf("label: %+v", ls)
	}
	// "{" starting a statement is a block, holding a labelled statement
	if b := body[7].(*ast.Block); b.Stmts[0].(*ast.LabeledStmt).Label.Name != "a" {
		t.Errorf("block with label: %+v", b)
	}
	for i, want := range [][2]bool{{true, false}, {false, true}, {true, true}} {
		ts := body[8+i].(*ast.TryStmt)
		if (ts.Catch != nil) != want[0] || (ts.Finally != nil) != want[1] {
			t.Errorf("try %d: catch=%v finally=%v", i, ts.Catch != nil, ts.Finally != nil)
		}
	}
}

func TestErrors(t *testing.T) {
	tests := []struct{ src, pos, msg string }{
		{"main() { x = 1 }", "1:16", "expected ';' after expression, found '}'"},
		{"main() { x = ; }", "1:14", "expected expression, found ';'"},
		{"main() {", "1:9", "expected '}' to close the block opened at t.kkl:1:8, found end of file"},
		{"main() { x\n: 1; }", "2:1", "expected ';' after expression, found ':'"},
		{`main() { {"a": 1}; }`, "1:14", "expected ';' after expression, found ':'"},
		{"main() { x = 9223372036854775808; }", "1:14", "only valid directly after a unary minus"},
		{"main() { for (local a, b, c in m) ; }", "1:29", "a for-each loop takes one or two names"},
		{"main() { for (x in m) ; }", "1:17", "expected ';' after for initializer, found 'in'"},
		{"main() { try { } }", "1:18", "expected 'catch' or 'finally' after the try block, found '}'"},
		{"main() { if x {} }", "1:13", "expected '(' after 'if', found identifier 'x'"},
		{"main() { elseif (x) y; }", "1:10", "expected expression, found 'elseif'"},
		{"main() { case a ? b : c: }", "1:17", "expected ':' after case value, found '?'"},
		{"main() { local this; }", "1:16", "expected local variable name, found 'this'"},
		{"main() { x = [1, 2; }", "1:19", "expected ']' to close the vector opened at t.kkl:1:14, found ';'"},
		{"main() { x = {1: 2; }", "1:19", "expected '}' to close the map opened at t.kkl:1:14"},
		{"main() { f(a..b); }", "1:15", "expected ')' after arguments, found identifier 'b'"},
		{"main() { x = a->; }", "1:17", "expected member name after '->', found ';'"},
		{"main() { new A; }", "1:15", "expected '(' after class name, found ';'"},
		{"main() { do x; until (y); }", "1:16", "expected 'while' after the body of a do loop"},
		{"main() { return 1 }", "1:19", "expected ';' after return"},
		{"main() { break 1; }", "1:16", "expected ';' after break, found number 1"},
		{"class A { x = 1; }", "1:13", "expected ';' after data member declaration, found '='"},
		{"class A { f(a = 1); }", "1:15", "a default value belongs in the definition of f"},
		{"class A { f(a.., b); }", "1:16", "expected ')' after declared parameters, found ','"},
		{"class A {}\n;", "2:1", "unexpected ';' (no ';' follows a class or function body)"},
		{"class A { x;", "1:13", "expected '}' to close the class body opened at t.kkl:1:9"},
		{"class : B {}", "1:7", "expected class name, found ':'"},
		{"foo", "1:4", "expected '(' or '::' after function name, found end of file"},
		{"42", "1:1", "expected a definition (class, function, global or const), found number 42"},
		{"A::(){}", "1:4", "expected member function name after '::'"},
		{"f(a b) {}", "1:5", "expected ')' after parameters, found identifier 'b'"},
		{"f() x", "1:5", "expected '{' to start the function body, found identifier 'x'"},
		{"const A;", "1:8", "expected '=' after constant name, found ';'"},
		{"global x = 1", "1:13", "expected ';' after global declaration, found end of file"},
		{"main() { a.b; }", "1:11", "lone '.'"}, // a lexer error
	}
	for _, tt := range tests {
		_, err := ParseFile("t.kkl", []byte(tt.src))
		if err == nil {
			t.Errorf("%q: no error, want %q", tt.src, tt.msg)
			continue
		}
		e, ok := err.(*diag.Error)
		if !ok || e.Pos.String() != "t.kkl:"+tt.pos || !strings.Contains(e.Msg, tt.msg) {
			t.Errorf("%q:\n got  %v\n want t.kkl:%s: ...%s...", tt.src, err, tt.pos, tt.msg)
		}
	}
	deep := "main() { x = " + strings.Repeat("(", 1001) + "1" + strings.Repeat(")", 1001) + "; }"
	if _, err := ParseFile("t.kkl", []byte(deep)); err == nil || !strings.Contains(err.Error(), "nesting too deep") {
		t.Errorf("deep parentheses: got %v", err)
	}
	deep = "main() " + strings.Repeat("{", 1100) + strings.Repeat("}", 1100)
	if _, err := ParseFile("t.kkl", []byte(deep)); err == nil || !strings.Contains(err.Error(), "nesting too deep") {
		t.Errorf("deep blocks: got %v", err)
	}
	ok := "main() { x = " + strings.Repeat("(", 300) + "1" + strings.Repeat(")", 300) + "; }"
	if _, err := ParseFile("t.kkl", []byte(ok)); err != nil {
		t.Errorf("300 nested parentheses: %v", err)
	}
	if _, err := ParseExpr("a b"); err == nil || !strings.Contains(err.Error(), "expected end of expression, found identifier 'b'") {
		t.Errorf("ParseExpr(\"a b\"): got %v", err)
	}
}

// FuzzParser checks that the parser never panics (other than through its
// own error reporting) and never loops forever.
func FuzzParser(f *testing.F) {
	for _, s := range []string{
		"", "main(args) { return 0; }", "class A : B { x; static f(a..); }",
		"A::f(a, b = 1, c..; t) { for (local k, v in m) { lbl: x = -5 ? y : z; } }",
		"main() { switch (x) { case 1: { local y; case 2: ; } default: break; } }",
		"main() { try { throw new E(1)->m(); } catch (e) { } finally { } }",
		"global g = [1, v.., {a: fn() { return this->x; }}]; const C = 1 << 2;",
		"main() { if (a) b; elseif (c) d; else { do e; while (f); } }",
		"main() { x = - - -1; !!y; a->b->c(); (p->f)(); C::x++; }",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		ParseFile("f.kkl", src)
	})
}
