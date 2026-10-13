package compiler

import (
	"strings"
	"testing"

	"kukel/internal/ast"
	"kukel/internal/diag"
	"kukel/internal/parser"
)

var testBuiltins = []string{"print", "println", "str"}

func compileSrc(t *testing.T, srcs ...string) error {
	t.Helper()
	var files []*ast.File
	for i, src := range srcs {
		name := "t.kkl"
		if i > 0 {
			name = "u.kkl"
		}
		f, err := parser.ParseFile(name, []byte(src))
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		files = append(files, f)
	}
	_, err := Compile(files, testBuiltins)
	return err
}

func TestCompileErrors(t *testing.T) {
	tests := []struct{ src, pos, msg string }{
		// names
		{"main() { x = 1; }", "1:10", "undefined name x (declare globals with 'global')"},
		{"main() { println(y); }", "1:18", "undefined name y"},
		{"f() {}", "1:1", "the program has no main function"},
		{"global main; f() {}", "1:1", "the program has no main function"},
		{"main(a, b) {}", "1:1", "main takes at most one parameter"},
		{"f() {}\nglobal f;\nmain() {}", "2:8", "f is already defined at t.kkl:1:1"},
		{"const A = 1; global A;", "1:21", "A is already defined at t.kkl:1:7"},
		{"println() {}", "1:1", "println is a built-in function and cannot be redefined"},
		{"global str;", "1:8", "str is a built-in function and cannot be redefined"},
		// assignments
		{"const C = 1; main() { C = 2; }", "1:23", "cannot assign to constant C"},
		{"main() { main = 1; }", "1:10", "cannot assign to function main"},
		{"main() { print++; }", "1:10", "cannot assign to built-in function print"},
		{"main() { 1 = 2; }", "1:10", "cannot assign to this expression"},
		{"main() { main() = 2; }", "1:10", "cannot assign to this expression"},
		{"main() { (1 + 2)++; }", "1:11", "cannot assign to this expression"},
		// constants
		{"f() {} const A = f();", "1:18", "not a constant expression"},
		{"const B = C; const C = 1;", "1:11", "C is not a constant defined earlier"},
		{"global g; const D = g;", "1:21", "g is not a constant defined earlier"},
		{"const D = 1 / 0;", "1:13", "in constant expression: integer division by zero"},
		{`const E = "a" - 1;`, "1:15", "in constant expression: cannot apply - to string and int"},
		{"const F = -nil;", "1:11", "in constant expression: cannot apply - to nil"},
		// locals and parameters
		{"main() { local x; local x; }", "1:25", "local x is already declared in this block"},
		{"main() { local x, x; }", "1:19", "local x is already declared in this block"},
		{"f(a, a) {} main() {}", "1:6", "parameter a repeats a parameter or temporary name"},
		{"f(a; a) {} main() {}", "1:6", "temporary a repeats a parameter or temporary name"},
		{"main() { if (1) local x; }", "1:17", "a local declaration must be a statement directly inside a block"},
		{"main() { while (1) local x; }", "1:20", "a local declaration must be a statement directly inside a block"},
		// break and continue
		{"main() { break; }", "1:10", "break outside a loop or switch"},
		{"main() { continue; }", "1:10", "continue outside a loop"},
		{"main() { if (1) { continue; } }", "1:19", "continue outside a loop"},
		{"main() { case 1: ; }", "1:10", "case and default labels are only allowed inside a switch body"},
		// parameters
		{"f(a = 1, b) {} main() {}", "1:10", "parameter b needs a default value, because a parameter before it has one"},
		{"f(a.., b) {} main() {}", "1:3", "the rest parameter a must be the last parameter"},
		{"f(a = b, b = 1) {} main() {}", "1:7", "undefined name b"}, // a default sees only earlier parameters
		// labels
		{"main() { x: while (1) { x: ; } }", "1:25", "label x is already used by an enclosing statement at t.kkl:1:10"},
		{"main() { x: x: ; }", "1:13", "label x is already used at t.kkl:1:10"},
		{"main() { while (1) break y; }", "1:26", "no enclosing statement is labelled y"},
		{"main() { x: { while (1) continue x; } }", "1:34", "continue x: x does not label a loop"},
		{"main() { x: ; while (1) break x; }", "1:31", "no enclosing statement is labelled x"},
		{"main() { x: local y; }", "1:13", "a local declaration must be a statement directly inside a block"},
		// switch
		{"main() { switch (1) { local x; } }", "1:23", "a local declaration may not be directly in a switch body"},
		{"main() { switch (1) { case 1: case 1.0: } }", "1:36", "duplicate case value 1.0 (also at t.kkl:1:28)"},
		{"const A = 2; main() { switch (1) { case A: case 1 + 1: } }", "1:49", "duplicate case value 2 (also at t.kkl:1:41)"},
		{"main() { switch (1) { default: default: } }", "1:32", "a switch has only one default label (the first is at t.kkl:1:23)"},
		{"main() { local y; switch (1) { case y: } }", "1:37", "y is not a constant defined earlier"},
		{"main() { switch (1) { case 1: { local y = 2; case 2: ; } } }", "1:46", "case label skips the initialization of local y"},
		{"main() { switch (1) { case 1: for (local i = 0; ; ) { default: } } }", "1:55", "default label skips the initialization of local i"},
		{"main() { switch (1) { case 1: continue; } }", "1:31", "continue outside a loop"},
		{"main() { switch (1) { case 1: } case 2: ; }", "1:33", "case and default labels are only allowed inside a switch body"},
		// later milestones
		{"class A {}", "1:1", "classes are not supported yet (milestone 6)"},
		{"main() { local v = [1]; }", "1:20", "vector literals are not supported yet (milestone 5)"},
		{"main() { for (local x in y) ; }", "1:10", "for-each loops are not supported yet (milestone 5)"},
		{"main() { try { } finally { } }", "1:10", "try statements are not supported yet (milestone 7)"},
		{"main() { local f = fn() { }; }", "1:20", "function literals are not supported yet (milestone 8)"},
		{"main() { this; }", "1:10", "classes and objects are not supported yet (milestone 6)"},
	}
	for _, tt := range tests {
		err := compileSrc(t, tt.src)
		e, ok := err.(*diag.Error)
		if !ok || e.Pos.String() != "t.kkl:"+tt.pos || !strings.Contains(e.Msg, tt.msg) {
			t.Errorf("%q:\n got  %v\n want t.kkl:%s: ...%s...", tt.src, err, tt.pos, tt.msg)
		}
	}
}

func TestMultipleFiles(t *testing.T) {
	// Names are shared across files; a function may be used before (and in
	// another file than) its definition.
	if err := compileSrc(t, "main() { helper(); println(G); }", "global G = 1; helper() {}"); err != nil {
		t.Errorf("two files: %v", err)
	}
	err := compileSrc(t, "main() {}", "main() {}")
	if err == nil || err.Error() != "u.kkl:1:1: main is already defined at t.kkl:1:1" {
		t.Errorf("duplicate main across files: %v", err)
	}
}

func TestMaxStack(t *testing.T) {
	f, err := parser.ParseFile("t.kkl", []byte(`main() { local a = 1; println(a + (a * (a - (a / 2))), a ? 1 : 2, a && a); }`))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := Compile([]*ast.File{f}, testBuiltins)
	if err != nil {
		t.Fatal(err)
	}
	// println, a, a, a, a, 2 -> 6 deep while computing the first argument.
	if got := prog.Funcs[0].MaxStack; got != 6 {
		t.Errorf("MaxStack = %d, want 6", got)
	}
}

// FuzzCompile checks that the compiler never panics and never reports an
// internal error (such as a wrong stack depth) for any parsable input.
func FuzzCompile(f *testing.F) {
	for _, s := range []string{
		"main() { }",
		"global g = 1; const C = 2 * 3; f(a; t) { t = a; return t ? -a : !a; } main() { f(C); g++; }",
		"main() { for (local i = 0; i < 3; i++) { if (i && g || !i) continue; else break; } }",
		"main() { local a, b = 1; a = b = (1, 2) ? a += 1 : b--; do { while (a) a--; } while (b); }",
		"main(args) { println(args, 1 < 2 == 3 > 4, 1 << 2 >> 3 & 4 | 5 ^ ~6 % 7); }",
		"f(a, b = a, c..) { return f(c.., a, b..); } main() { a: b: for (;;) { c: { break a; } continue b; } }",
		"main() { switch (1) { case 1: do { { case 2: ; } case 3: break; } while (0); default: } }",
		"main() { s: switch (1) { case -1: while (1) { switch (2) { case 'x': break s; } continue; } } }",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		file, err := parser.ParseFile("f.kkl", []byte(src))
		if err != nil {
			return
		}
		if _, err := Compile([]*ast.File{file}, testBuiltins); err != nil && strings.Contains(err.Error(), "internal error") {
			t.Fatalf("%q: %v", src, err)
		}
	})
}
