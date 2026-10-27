package vm_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Generators (Q86): a function with yield runs a step at a time; for-each
// walks generators and objects whose iter() gives one, and closes the
// generator however the loop is left.
func TestGenerators(t *testing.T) {
	const common = `
countdown(n) { for (local i = n; i > 0; i--) yield i; }
class R { n; R(n); close(); }
R::R(n) { this->n = n; }
R::close() { print("close " + n + "; "); }
guarded(name) {
    with (local r = new R(name)) {
        try { yield 1; yield 2; yield 3; }
        catch (e) { print("never; "); }
        finally { print("finally; "); }
    }
}
`
	tests := []struct {
		name, src, want string
	}{
		{"for-each", `main() { for (local x in countdown(3)) print(x, ""); println(); }`, "3 2 1 \n"},
		{"two names count", `main() { for (local i, x in countdown(2)) print(i, x, ""); println(); }`, "0 2 1 1 \n"},
		{"next and done", `main() {
			local g = countdown(2);
			println(g->done(), g->next(), g->next(), g->done(), g->next(), g->done(), g->next("end"));
		}`, "false 2 1 false nil true end\n"},
		{"the body runs at the first step", `
			g() { println("started"); yield 1; println("resumed"); }
			main() { local x = g(); println("made"); println(x->next()); println(x->next()); }`,
			"made\nstarted\n1\nresumed\nnil\n"},
		{"a generator is an object of class Generator", `main() {
			local g = countdown(1);
			println(g, typeof(g), instanceof(g, Generator), classof(g) == Generator, g->iter() == g);
		}`, "Generator(countdown) object true true true\n"},
		{"yield without a value; return ends it", `
			g() { yield; yield 1; return; yield 2; }
			main() { for (local x in g()) print(x, ""); println(); }`, "nil 1 \n"},
		{"iter() of an object", `
			class Tree { left, value, right; Tree(l, v, r); iter(); }
			Tree::Tree(l, v, r) { left = l; value = v; right = r; }
			Tree::iter() {
				if (left != nil) for (local x in left) yield x;
				yield value;
				if (right != nil) for (local x in right) yield x;
			}
			leaf(v) { return new Tree(nil, v, nil); }
			main() {
				local t = new Tree(new Tree(leaf(1), 2, leaf(3)), 4, leaf(5));
				for (local i, x in t) print(i, x, "");
				println();
			}`, "0 1 1 2 2 3 3 4 4 5 \n"},
		{"arguments, defaults, rest, this and closures", `
			class C { k; C(k); gen(n); }
			C::C(k) { this->k = k; }
			C::gen(n) { for (local i = 0; i < n; i++) yield k * i; }
			g(a, b = 10, rest..) { yield a; yield b; yield rest; }
			main() {
				for (local x in new C(3)->gen(3)) print(x, "");
				for (local x in g(1)) print(x, "");
				for (local x in g(1, 2, 3, 4)) print(x, "");
				local base = 100;
				local h = fn (n) { for (local i = 0; i < n; i++) { base++; yield base; } };
				for (local x in h(2)) print(x, "");
				println(base);
			}`, "0 3 6 1 10 [] 1 2 [3, 4] 101 102 102\n"},
		{"locals live between steps", `
			fib() { local a = 0, b = 1; for (;;) { yield a; local t = a + b; a = b; b = t; } }
			main() { for (local i, x in fib()) { if (i == 10) break; print(x, ""); } println(); }`,
			"0 1 1 2 3 5 8 13 21 34 \n"},
		{"two generators of one function are independent", `main() {
			local a = countdown(3), b = countdown(3);
			println(a->next(), a->next(), b->next(), a->next(), b->next());
		}`, "3 2 3 1 2\n"},
		{"break closes it", common + `main() {
			for (local x in guarded("a")) { print(x, ""); if (x == 2) break; }
			println("after");
		}`, "1 2 finally; close a; after\n"},
		{"the end of the loop runs nothing more", common + `main() {
			for (local x in guarded("a")) print(x, "");
			println("after");
		}`, "1 2 3 finally; close a; after\n"},
		{"return closes it", common + `
			first() { for (local x in guarded("a")) return x * 10; }
			main() { println(first()); }`, "finally; close a; 10\n"},
		{"an exception closes it, catch blocks do not see the close", common + `main() {
			try { for (local x in guarded("a")) throw new Error("boom"); }
			catch (e) { println(e->message); }
		}`, "finally; close a; boom\n"},
		{"labelled break and continue close the inner loop's generators", common + `main() {
			outer: for (local i = 0; i < 2; i++) {
				for (local x in guarded(str(i))) {
					if (i == 0) continue outer;
					break outer;
				}
			}
			println();
		}`, "finally; close 0; finally; close 1; \n"},
		{"nested loops close inner first", common + `main() {
			for (local x in guarded("out")) { for (local y in guarded("in")) break; break; }
			println();
		}`, "finally; close in; finally; close out; \n"},
		{"close() by hand and with", common + `main() {
			local g = guarded("a");
			println(g->next());
			g->close();
			println(g->done(), g->next());
			local h = guarded("b");
			h->close(); // not started: nothing runs
			println(h->done());
			with (local w = guarded("c")) println(w->next());
			println();
		}`, "1\nfinally; close a; true nil\ntrue\n1\nfinally; close c; \n"},
		{"a generator in a loop in a generator", common + `
			outer() { for (local x in guarded("in")) yield x; }
			main() { for (local x in outer()) { if (x == 2) break; } println(); }`,
			"finally; close in; \n"},
		{"an exception in the body goes to the caller, then it is done", `
			g() { yield 1; throw new Error("in g"); }
			main() {
				local x = g();
				println(x->next());
				try { x->next(); } catch (e) { println(e->message, e->trace[0], e->trace[1]); }
				println(x->done(), x->next());
			}`, "1\nin g g (t.kkl:3) main (t.kkl:7)\ntrue nil\n"},
		{"an exception in a step a for loop runs ends the generator", `
			global saved;
			g() { try { yield 1; throw new Error("in g"); } finally { println("g's finally"); } }
			main() {
				saved = g();
				try { for (local x in saved) println(x); } catch (e) { println(e->message, e->trace[0], e->trace[1]); }
				println(saved->done(), saved->next());
			}`, "1\ng's finally\nin g g (t.kkl:4) main (t.kkl:7)\ntrue nil\n"},
		{"next() and for take turns on one generator", `main() {
			local g = countdown(5);
			println(g->next());
			for (local x in g) { println(x); if (x == 3) break; }
			println(g->done(), g->next());
		}`, "5\n4\n3\ntrue nil\n"},
		{"a for loop inside a generator another for loop walks", `
			pairs(n) { for (local i in countdown(n)) for (local j in countdown(i)) yield [i, j]; }
			main() { for (local p in pairs(2)) print(p, ""); println(); }`, "[2, 2] [2, 1] [1, 1] \n"},
		{"a try in the body catches the body's exceptions across yields", `
			g() { try { yield 1; throw new Error("x"); } catch (e) { yield "caught " + e->message; } }
			main() { for (local x in g()) println(x); }`, "1\ncaught x\n"},
		{"the generator asks itself for a value", `
			global self;
			g() { yield self->next(); }
			main() { self = g(); try { self->next(); } catch (e) { println(e->kind, e->message); } }`,
			"ValueError the generator g is already running (its own body asks it for a value)\n"},
		{"a yield while closing", `
			g() { try { yield 1; } finally { yield 2; } }
			main() {
				local x = g(); x->next();
				try { x->close(); } catch (e) { println(e->kind); }
				println(x->done());
			}`, "ValueError\ntrue\n"},
		{"a close that throws on the normal way out goes on", `
			g() { try { yield 1; } finally { throw new Error("in finally"); } }
			main() {
				try { for (local x in g()) break; } catch (e) { println(e->message); }
			}`, "in finally\n"},
		{"iter() must give a generator", `
			class A { iter(); }
			A::iter() { return [1]; }
			class B { }
			main() {
				try { for (local x in new A()) ; } catch (e) { println(e->message); }
				try { for (local x in new B()) ; } catch (e) { println(e->message); }
			}`, "A::iter() must give a generator, not vector\ncannot iterate over an object of class B: it has no member function iter() (which gives a generator)\n"},
		{"no new Generator(); argument counts", `main() {
			try { new Generator(); } catch (e) { println(e->kind, e->message); }
			local g = countdown(1);
			try { g->done(1); } catch (e) { println(e->kind, e->message); }
			try { g->next(1, 2); } catch (e) { println(e->message); }
		}`, "TypeError there is no new Generator(): generators come from calling generator functions\n" +
			"ArgumentError Generator::done expects 0 arguments, got 1\nGenerator::next expects at most 1 argument, got 2\n"},
		{"vectors, maps and strings as before", `main() {
			for (local i, x in [7, 8]) print(i, x, "");
			for (local k, v in {"a": 1}) print(k, v, "");
			for (local c in "hi") print(c, "");
			println();
		}`, "0 7 1 8 a 1 104 105 \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.src
			if !strings.Contains(src, "countdown(n) {") {
				src = "countdown(n) { for (local i = n; i > 0; i--) yield i; }\n" + src
			}
			if out, _, err := runSource(t, src); err != nil || out != tt.want {
				t.Errorf("got %q, %v\nwant %q", out, err, tt.want)
			}
		})
	}
}

// A deep recursion of generators (a tree as a list) stays within the
// frame limit, and a generator that never ends counts against the step
// limit.
func TestGeneratorLimits(t *testing.T) {
	src := `
class L { v, next; L(v, n); iter(); }
L::L(v, n) { this->v = v; this->next = n; }
L::iter() { yield v; if (next != nil) for (local x in next) yield x; }
forever() { for (;;) yield 1; }
main(args) {
    if (len(args) > 0) { for (local x in forever()) ; }
    local l = nil;
    for (local i = 0; i < 1000; i++) l = new L(i, l);
    local sum = 0;
    for (local x in l) sum += x;
    println(sum);
}`
	if out, _, err := runSource(t, src); err != nil || out != "499500\n" {
		t.Errorf("deep: got %q, %v", out, err)
	}
	prog := compileSource(t, src)
	var out bytes.Buffer
	machine, err := vm.New(prog, builtins.Lookup, &out, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	machine.StepLimit = 10000
	if _, err := machine.Run([]string{"forever"}); err == nil || !strings.Contains(err.Error(), "StepLimitError") {
		t.Errorf("forever: got %v, want a StepLimitError", err)
	}
	// The VM can be used again: no handlers or frames are left over.
	machine.StepLimit = 0
	if _, err := machine.Run(nil); err != nil {
		t.Errorf("again: %v", err)
	}
}

func compileSource(t *testing.T, src string) *bytecode.Program {
	t.Helper()
	f, err := parser.ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return prog
}
