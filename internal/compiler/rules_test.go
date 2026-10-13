package compiler

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"kukel/internal/diag"
)

// ruleCases lists, for every grammar rule of syntax.ebnf that carries a
// "Rule:" comment, programs that must compile and programs that must fail
// with a given message. Rules that are not checked by the compiler name the
// tests that cover them instead.
var ruleCases = map[string]struct {
	ok        []string
	bad       [][2]string // source, part of the message
	elsewhere string
}{
	"program": {
		ok: []string{"global a; f() {} class C {} const K = 1; main() {}"},
		bad: [][2]string{
			{"f() {} f() {} main() {}", "f is already defined"},
			{"class f {} f() {} main() {}", "f is already defined"},
			{"global print; main() {}", "print is a built-in function and cannot be redefined"},
		},
	},
	"global_variable": {elsewhere: "internal/vm TestPrograms: \"global initializers run in order\", \"global used before its initializer\""},
	"const_definition": {
		ok: []string{`const A = 1, B = A * 2 + (A > 0 ? 1 : 0), S = "s" + "t", N = nil, T = !N && A != 2; main() {}`},
		bad: [][2]string{
			{"const A = B; const B = 1; main() {}", "B is not a constant defined earlier"},
			{"const D = 1 / 0; main() {}", "in constant expression: integer division by zero"},
			{"f() {} const F = f(); main() {}", "not a constant expression"},
			{"const A = [1]; main() {}", "not a constant expression"},
			{"const A = 1; main() { A = 2; }", "cannot assign to constant A"},
		},
	},
	"class_definition": {
		ok:  []string{"class A {} class B : A {} class C : B {} main() {}"},
		bad: [][2]string{{"class B : A {} class A {} main() {}", "base class A is not defined"}},
	},
	"declared_parameters": {elsewhere: "internal/parser TestErrors: \"a default value belongs in the definition\""},
	"function_definition": {
		ok: []string{"class A { m(a, b..); static s(); } A::m(x, y..) {} A::s() {} main() {}"},
		bad: [][2]string{
			{"class A {} A::m() {} main() {}", "class A does not declare a member function m"},
			{"X::m() {} main() {}", "undefined class X"},
			{"class A { m(a); } A::m() {} main() {}", "the definition of A::m has 0 parameters"},
			{"class A { m(a..); } A::m(a) {} main() {}", "has 1 parameter, but its declaration"},
			{"class A { x; static s(); } A::s() { return x; } main() {}", "not available in static member function A::s"},
		},
	},
	"parameter": {
		ok: []string{"f(a, b = a, c = b + 1, d..; t) {} main() {}"},
		bad: [][2]string{
			{"f(a = 1, b) {} main() {}", "parameter b needs a default value"},
			{"f(a.., b) {} main() {}", "the rest parameter a must be the last parameter"},
			{"f(a = b, b = 1) {} main() {}", "undefined name b"},
		},
	},
	"default_label": {
		ok: []string{"const K = 3; main() { switch (1) { case K: case -1: case 'x': case \"s\": case nil: { case 2: ; } default: } }"},
		bad: [][2]string{
			{"main() { switch (1) { case 1: case 1.0: } }", "duplicate case value"},
			{"main() { switch (1) { default: default: } }", "only one default label"},
			{"main() { local v; switch (1) { case v: } }", "v is not a constant defined earlier"},
			{"main() { case 1: ; }", "only allowed inside a switch body"},
			{"main() { switch (1) { case 1: try { case 2: ; } finally { } } }", "may not be inside a try"},
			{"main() { switch (1) { case 1: { local y; case 2: ; } } }", "skips the initialization of local y"},
		},
	},
	"foreach_statement": {elsewhere: "internal/vm TestRuntimeErrors: \"for-each over an int\""},
	"continue_statement": {
		ok: []string{"main() { a: while (1) { b: for (;;) { if (1) continue a; break b; } break; } switch (1) { case 1: break; } }"},
		bad: [][2]string{
			{"main() { break; }", "break outside a loop or switch"},
			{"main() { switch (1) { case 1: continue; } }", "continue outside a loop"},
			{"main() { while (1) break x; }", "no enclosing statement is labelled x"},
			{"main() { a: { while (1) continue a; } }", "a does not label a loop"},
			{"main() { while (1) { local f = fn() { break; }; } }", "break outside a loop or switch"},
		},
	},
	"labelled_statement": {
		ok:  []string{"main() { a: { b: while (1) break a; } a: ; }"},
		bad: [][2]string{{"main() { a: while (1) { a: ; } }", "label a is already used by an enclosing statement"}},
	},
	"local_variable": {
		ok: []string{"global x; main() { local x = x, y; for (local i = 0, j; ; ) break; { local x; } }"},
		bad: [][2]string{
			{"main() { if (1) local x; }", "must be a statement directly inside a block"},
			{"main() { switch (1) { local x; } }", "may not be directly in a switch body"},
			{"main() { local x; local x; }", "local x is already declared in this block"},
			{"main() { local x = x; }", "undefined name x"},
		},
	},
	"finally_part": {
		ok: []string{"f() { try { return 1; } finally { while (1) break; } } main() {}"},
		bad: [][2]string{
			{"f() { try { } finally { return; } } main() {}", "return out of a finally block is not allowed"},
			{"main() { while (1) try { } finally { break; } }", "break out of a finally block is not allowed"},
			{"main() { while (1) try { } finally { continue; } }", "continue out of a finally block is not allowed"},
		},
	},
	"assignment": {
		ok: []string{`global g; class A { x; static s; m(); } A::m() { x = 1; this->x += 1; A::s = 2; s--; local f = fn() { x++; };
			} main(; t) { local l, v = [0], m = {}; g = l = t = 1; v[0] += 1; m["k"] = 2; new A()->x = 3; local f = fn() { l = 4; }; }`},
		bad: [][2]string{
			{"const C = 1; main() { C = 2; }", "cannot assign to constant C"},
			{"f() {} main() { f = 1; }", "cannot assign to function f"},
			{"class A {} main() { A = 1; }", "cannot assign to class A"},
			{"main() { println = 1; }", "cannot assign to built-in function println"},
			{"class A { m(); } A::m() { this = 1; } main() {}", "cannot assign to this expression"},
			{"main() { 1 = 2; }", "cannot assign to this expression"},
			{"f() {} main() { f() = 1; }", "cannot assign to this expression"},
			{"class A { m(); } A::m() { m = 1; } main() {}", "cannot assign to member function m"},
			{"class A { x; } main() { A::x = 1; }", "only static data members"},
		},
	},
	"unary": {
		ok:  []string{"main(; x) { x = 1; ++x; --x; - -x; !!x; ~~x; }"},
		bad: [][2]string{{"main() { ++1; }", "cannot assign to this expression"}, {"class A { m(); } A::m() { --this; } main() {}", "cannot assign to this expression"}},
	},
	"postfix_operator": {
		ok:  []string{"main(; x) { x = [1]; x[0]++; x--; }"},
		bad: [][2]string{{"f() {} main() { f()++; }", "cannot assign to this expression"}},
	},
	"argument": {elsewhere: "internal/vm TestRuntimeErrors: \"spread of a non-vector\""},
	"primary": {
		ok: []string{"class A { x; m(); } A::m() { return this->x + x; } main() { local f = fn() { return 1; }; }"},
		bad: [][2]string{
			{"main() { return y; }", "undefined name y"},
			{"main() { this; }", "'this' is only available in member functions"},
			{"class A { static s(); } A::s() { local f = fn() { this; }; } main() {}", "is not available in static member function"},
		},
	},
	"map_entry": {elsewhere: "internal/vm TestRuntimeErrors: \"bad map key in a literal\"; internal/value TestMapKeys"},
	"Float":     {elsewhere: "internal/lexer TestErrors: \"Number too large\", \"number longer than 255\""},
	"Escape":    {elsewhere: "internal/lexer TestErrors: \"unknown escape sequence\""},
	"Comment":   {elsewhere: "internal/lexer TestErrors: \"unterminated comment\""},
}

func TestGrammarRules(t *testing.T) {
	for name, rc := range ruleCases {
		if rc.elsewhere != "" {
			continue
		}
		if len(rc.ok) == 0 || len(rc.bad) == 0 {
			t.Errorf("rule %s needs at least one valid and one invalid program", name)
		}
		for _, src := range rc.ok {
			if err := compileSrc(t, src); err != nil {
				t.Errorf("rule %s: %q should compile: %v", name, src, err)
			}
		}
		for _, b := range rc.bad {
			err := compileSrc(t, b[0])
			if _, ok := err.(*diag.Error); !ok || !strings.Contains(err.Error(), b[1]) {
				t.Errorf("rule %s: %q: got %v, want an error containing %q", name, b[0], err, b[1])
			}
		}
	}
}

// TestEveryGrammarRuleIsCovered keeps ruleCases in step with syntax.ebnf:
// every rule whose comment says "Rule:" or "Rules:" must have an entry.
func TestEveryGrammarRuleIsCovered(t *testing.T) {
	src, err := os.ReadFile("../../syntax.ebnf")
	if err != nil {
		t.Fatal(err)
	}
	defRe := regexp.MustCompile(`^([A-Za-z_]+)\s*=`)
	var current string
	inHeader := true
	found := map[string]bool{}
	for _, line := range strings.Split(string(src), "\n") {
		if m := defRe.FindStringSubmatch(line); m != nil {
			current, inHeader = m[1], false
		}
		if !inHeader && (strings.Contains(line, "Rule:") || strings.Contains(line, "Rules:")) {
			found[current] = true
		}
	}
	var missing []string
	for name := range found {
		if _, ok := ruleCases[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("grammar rules without test cases: %v", missing)
	}
	for name := range ruleCases {
		if !found[name] {
			t.Errorf("ruleCases has %s, which has no Rule: comment in syntax.ebnf", name)
		}
	}
}
