package compiler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/diag"
	"github.com/maarten-boot/kukel/internal/parser"
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
		// the operands that && || ?: skip must be constant too
		{"const A = 1 || nosuchname; main() {}", "1:16", "nosuchname is not a constant defined earlier"},
		{"const A = 1 || f(5); main() {}", "1:16", "not a constant expression"},
		{"const A = 1 ? 2 : len(1); main() {}", "1:19", "not a constant expression"},
		{"const A = 0 && this; main() {}", "1:16", "not a constant expression"},
		{"main() { switch (false) { case 0 && nosuch: ; } }", "1:37", "nosuch is not a constant defined earlier"},
		// locals and parameters
		{"main() { local x; local x; }", "1:25", "local x is already declared in this block"},
		{"main() { local x, x; }", "1:19", "local x is already declared in this block"},
		{"f(a, a) {} main() {}", "1:6", "parameter a repeats a parameter name"},
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
		// classes
		{"main() { this; }", "1:10", "'this' is only available in member functions"},
		{"class A : B {}", "1:11", "base class B is not defined (a base class must be defined before the classes derived from it)"},
		{"class A : A {}", "1:11", "base class A is not defined"},
		{"global B; class A : B {}", "1:21", "B is not a class"},
		{"class A { x; x; }", "1:14", "member x is already declared in class A at t.kkl:1:11"},
		{"class A { x; } class B : A { x; }", "1:30", "member x conflicts with x declared in base class A at t.kkl:1:11"},
		{"class A { f(); } class B : A { static f(); }", "1:39", "member f conflicts with f declared in base class A"},
		{"class A { f(); } class B : A { f; }", "1:32", "member f conflicts with f declared in base class A"},
		{"class A { static A(); }", "1:11", "the constructor A::A cannot be static"},
		{"A::f() {}", "1:1", "undefined class A"},
		{"f() {} f::g() {}", "1:8", "f is not a class"},
		{"class A {} A::f() {}", "1:15", "class A does not declare a member function f"},
		{"class A { f(); } class B : A {} B::f() {}", "1:36", "(it is declared in base class A; declare it in B to override it)"},
		{"class A { x; } A::x() {}", "1:19", "A::x is a data member, not a member function"},
		{"class A { f(); } A::f() {} A::f() {}", "1:31", "A::f is already defined at t.kkl:1:21"},
		{"class A { f(a, b); } A::f(a) {}", "1:25", "the definition of A::f has 1 parameter, but its declaration at t.kkl:1:11 has 2 parameters"},
		{"class A { f(a..); } A::f(a) {}", "1:24", "has 1 parameter, but its declaration at t.kkl:1:11 has 0 parameters and a rest parameter"},
		{"class A { x; static f(); } A::f() { x = 1; } main() {}", "1:37", "data member x is not available in static member function A::f (it has no 'this')"},
		{"class A { static f(); } A::f() { this; } main() {}", "1:34", "'this' is not available in static member function A::f"},
		{"class A { g(); static f(); } A::f() { g(); } main() {}", "1:39", "member function g is not available in static member function A::f"},
		{"class A { x; } main() { A::x; }", "1:28", "A::x is an instance data member: use an object (p->x)"},
		{"class A { f(); } A::f() {} main() { A::f(); }", "1:40", "A::f is not static: call it on an object"},
		{"class A { f(); } class B { g(); } A::f() {} B::g() { A::f(); } main() {}", "1:57", "A::f is not static"},
		{"class A {} main() { A::y; }", "1:24", "class A has no member y"},
		{"class A { static f(); } main() { A::f(); }", "1:37", "A::f is declared but not defined"},
		{"class A {} main() { A = 1; }", "1:21", "cannot assign to class A"},
		{"class A { f(); } A::f() { f = 1; } main() {}", "1:27", "cannot assign to member function f"},
		{"class A { x; } main() { A::x = 1; }", "1:28", "cannot assign to A::x: only static data members can be assigned this way"},
		{"main() { new println(); }", "1:14", "println is not a class"},
		{"main() { new Nope(); }", "1:14", "undefined class Nope"},
		{"class A { f(); } A::f() {} class B : A { g(); } B::g() { A::f(main..); } main() {}", "1:62", "spread arguments are not supported in a A::f(...) call"},
		// exceptions
		{"main() { while (1) { try { } finally { break; } } }", "1:40", "break out of a finally block is not allowed"},
		{"main() { a: { try { } finally { while (1) { continue a; } } } }", "1:54", "continue a: a does not label a loop"},
		{"main() { a: while (1) { try { } finally { if (1) continue a; } } }", "1:50", "continue out of a finally block is not allowed"},
		{"f() { try { } finally { return 1; } } main() {}", "1:25", "return out of a finally block is not allowed"},
		{"main() { switch (1) { case 1: try { case 2: ; } catch (e) { } } }", "1:37", "a case label may not be inside a try, catch or finally block of its switch"},
		{"main() { switch (1) { case 1: try { } catch (e) { default: } } }", "1:51", "a default label may not be inside a try, catch or finally block"},
		{"main() { switch (1) { case 1: try { } finally { case 2: } } }", "1:49", "a case label may not be inside a try, catch or finally block"},
		{"main() { try { } catch (e) { local e; } }", "1:36", "local e is already declared in this block"},
		{"class Error { }", "1:7", "Error is already defined at <prelude>:2:7"},
		// function literals
		{"main() { while (1) { local f = fn() { break; }; } }", "1:39", "break outside a loop or switch"},
		{"main() { a: while (1) { local f = fn() { continue a; }; } }", "1:51", "no enclosing statement is labelled a"},
		{"main() { local f = fn() { return this; }; }", "1:34", "'this' is only available in member functions"},
		{"class A { static s(); } A::s() { local f = fn() { this; }; } main() {}", "1:51", "'this' is not available in static member function A::s"},
		{"main() { local f = fn() { return q; }; }", "1:34", "undefined name q"},
		{"main() { local f = fn(a, a) { }; }", "1:26", "parameter a repeats a parameter name"},
		{"main() { local f = fn() { f(); }; }", "1:27", "undefined name f"}, // not visible in its own initializer
		// argument counts of callees known at compile time (Q32)
		{"f(a) {} main() { f(); }", "1:19", "f expects 1 argument(s), got 0"},
		{"f(a, b = 1) {} main() { f(1, 2, 3); }", "1:26", "f expects 1 to 2 arguments, got 3"},
		{"f(a, r..) {} main() { f(); }", "1:24", "f expects at least 1 argument(s), got 0"},
		{"class A { static s(x); } A::s(x = 0) {} main() { A::s(1, 2); }", "1:54", "A::s expects 0 to 1 arguments, got 2"},
		{"class A { static s(); g(); } A::s() {} A::g() { s(1); } main() {}", "1:50", "A::s expects 0 argument(s), got 1"},
		{"class A { m(x); } A::m(x) {} class B : A { g(); } B::g() { A::m(); } main() {}", "1:64", "A::m expects 1 argument(s), got 0"},
		{"class A { A(x); } A::A(x) {} main() { new A(); }", "1:39", "A::A expects 1 argument(s), got 0"},
		{"class A { } main() { new A(1); }", "1:22", "class A has no constructor, so new A() takes no arguments (got 1)"},
		{"class A { A(); } main() { new A(); }", "1:27", "A::A is declared but not defined"},
		{"class A { } class B : A { B(x); } B::B(x) {} main() { new B(); }", "1:55", "B::B expects 1 argument(s), got 0"},
		{"class A { A(x); } A::A(x) {} class B : A { } main() { new B(); }", "1:55", "A::A expects 1 argument(s), got 0"}, // inherited (Q33)
		{"class A { A(); } class B : A { } class C : B { } main() { new C(); }", "1:59", "A::A is declared but not defined"},
		// later milestones
		{"main() { for (local k, k in [1]) ; }", "1:24", "local k is already declared in this block"},
		{"main() { for (local x in x) ; }", "1:26", "undefined name x"}, // the loop variable is not visible in the expression
	}
	for _, tt := range tests {
		err := compileSrc(t, tt.src)
		e, ok := err.(*diag.Error)
		if !ok || e.Pos.String() != "t.kkl:"+tt.pos || !strings.Contains(e.Msg, tt.msg) {
			t.Errorf("%q:\n got  %v\n want t.kkl:%s: ...%s...", tt.src, err, tt.pos, tt.msg)
		}
	}
}

func TestCallsCheckedAtRunTime(t *testing.T) {
	// Callees that are not known at compile time are not checked: values in
	// variables, members, spread calls, virtual method calls.
	for _, src := range []string{
		"f(a) {} main() { local g = f; g(); }",
		"f(a) {} main() { local v = []; f(v..); }",
		"global h = fn(x) { }; main() { h(); }",
		"class A { m(x); } A::m(x) {} main() { new A()->m(); }",
		"class A { A(x); } A::A(x) {} main() { local c = A; new c(); }",
		"class A { cb; run(); } A::run() { cb(1, 2, 3); } main() {}",
		"f(a) {} main() { local f = fn() { }; f(); }", // the local shadows the function
	} {
		if err := compileSrc(t, src); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestJumpsInsideFinallyAreAllowed(t *testing.T) {
	// break/continue that stay inside the finally block, and a try inside
	// a switch without labels in it, are fine.
	for _, src := range []string{
		"main() { try { } finally { while (1) { break; } for (local x in []) continue; } }",
		"main() { switch (1) { case 1: try { } catch (e) { } break; } }",
		"main() { while (1) { switch (1) { case 1: try { break; } finally { } } } }",
		"f() { try { return 1; } catch (e) { return 2; } finally { } } main() {}",
	} {
		if err := compileSrc(t, src); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestOnlyCapturedLocalsGetCells(t *testing.T) {
	f, err := parser.ParseFile("t.kkl", []byte(`g(p, q) { local t; local a = 1, b = 2; local f = fn() { return a + p + t; }; } main() {}`))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := Compile([]*ast.File{f}, testBuiltins)
	if err != nil {
		t.Fatal(err)
	}
	var main *bytecode.Function
	for _, fn := range prog.Funcs {
		if fn.FuncName == "g" {
			main = fn
		}
	}
	cells := map[int]bool{}
	for pc := 0; pc < len(main.Code); pc += bytecode.Op(main.Code[pc]).Size() {
		if bytecode.Op(main.Code[pc]) == bytecode.OpNewCell {
			cells[bytecode.ReadU16(main.Code, pc+1)] = true
		}
	}
	// slots: p=0, q=1, t=2, a=3, b=4, f=5; captured: p, t, a
	if len(cells) != 3 || !cells[0] || !cells[2] || !cells[3] {
		t.Errorf("cells in slots %v, want 0, 2 and 3", cells)
	}
	lit := prog.Funcs[len(prog.Funcs)-1]
	if lit.FuncName != "g/fn@1" || len(lit.Upvalues) != 3 {
		t.Errorf("function literal %s has %d upvalues, want g/fn@1 with 3", lit.FuncName, len(lit.Upvalues))
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
		"global g = 1; const C = 2 * 3; f(a) { local t; t = a; return t ? -a : !a; } main() { f(C); g++; }",
		"main() { for (local i = 0; i < 3; i++) { if (i && g || !i) continue; else break; } }",
		"main() { local a, b = 1; a = b = (1, 2) ? a += 1 : b--; do { while (a) a--; } while (b); }",
		"main(args) { println(args, 1 < 2 == 3 > 4, 1 << 2 >> 3 & 4 | 5 ^ ~6 % 7); }",
		"f(a, b = a, c..) { return f(c.., a, b..); } main() { a: b: for (;;) { c: { break a; } continue b; } }",
		"main() { switch (1) { case 1: do { { case 2: ; } case 3: break; } while (0); default: } }",
		"main() { s: switch (1) { case -1: while (1) { switch (2) { case 'x': break s; } continue; } } }",
		"main() { local v = [1, [2], {3: 4}], m = {\"a\": v..}; v[0] += v[1][0]++; m[v]--; for (local k, x in m) for (local y in x) ; }",
		"main() { local v = [[1]]; println(v[0][0] = v[0][0]++ + --v[0][0], [v.., v..], {v: v}[v]); }",
		"class A { x; static s; A(a = 1); f(b..); static g(); } A::A(a) { x = a; s = this; } A::f(b) { return x++ + this->x-- + A::g(); } A::g() { return s->x; } main() { new A()->f(1, 2); }",
		"class B { y; m(); } class C : B { z; C(); m(); } C::C() { B::m(); z = y = this->m; } C::m() { return B::m() + z->y++; } B::m() { return y; } main() { local c = C; new c()->z = new C()->m; }",
		"f(x) { a: for (;;) { try { try { if (x) return x; break a; } catch (e) { continue; } finally { x++; } } finally { } } } main() { f(1); }",
		"main() { switch (1) { case 1: try { throw new Error(); } catch (e) { try { } finally { while (1) break; } } default: } }",
		"class E : Error { E(); } E::E() { Error::Error(\"m\"); } main() { try { throw new E(); } catch (e) { throw e; } finally { return; } }",
		"main(a) { local t; local f = fn(x = a, y..) { local z; return fn() { a++; t = x; return z = y; }; }; for (local i = 0; i < 2; i++) f(i)(); }",
		"class K { v; m(); } K::m() { return fn() { return fn() { v++; return this; }; }; } main() { try { new K()->m()()(); } catch (e) { local g = fn() { return e; }; } }",
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

// Function indexes are 16-bit operands: more functions than that are a
// compile error, not a call of the wrong function.
func TestTooManyFunctions(t *testing.T) {
	var b strings.Builder
	for _, class := range []string{"A", "B"} { // member functions: they use no global names
		fmt.Fprintf(&b, "class %s {", class)
		for i := 0; i < 33000; i++ {
			fmt.Fprintf(&b, " static m%d();", i)
		}
		b.WriteString(" }\n")
		for i := 0; i < 33000; i++ {
			fmt.Fprintf(&b, "%s::m%d() {}\n", class, i)
		}
	}
	b.WriteString("main() {}\n")
	err := compileSrc(t, b.String())
	if err == nil || !strings.Contains(err.Error(), "too many functions in the program (more than 65536)") {
		t.Errorf("got %v", err)
	}
}

func TestMainParameter(t *testing.T) {
	for _, src := range []string{"main(a..) {}", "main(a = 1) {}"} {
		if err := compileSrc(t, src); err == nil || !strings.Contains(err.Error(), "cannot have a default or be a rest parameter") {
			t.Errorf("%s: %v", src, err)
		}
	}
}
