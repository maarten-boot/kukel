package vm_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

// runSource compiles and runs a one-file program.
func runSource(t *testing.T, src string, args ...string) (string, int, error) {
	t.Helper()
	out, _, code, err := runSourceIO(t, src, "", args...)
	return out, code, err
}

// runSourceIO runs a program with the given standard input and returns
// what it wrote to standard output and standard error.
func runSourceIO(t *testing.T, src, stdin string, args ...string) (string, string, int, error) {
	t.Helper()
	f, err := parser.ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := bytecode.Verify(prog); err != nil {
		t.Fatalf("the compiler produced code the verifier rejects: %v", err)
	}
	var out, errOut bytes.Buffer
	machine, err := vm.New(prog, builtins.Lookup, &out, strings.NewReader(stdin))
	if err != nil {
		t.Fatalf("vm.New: %v", err)
	}
	machine.SetStderr(&errOut)
	code, err := machine.Run(args)
	return out.String(), errOut.String(), code, err
}

func TestPrograms(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"arithmetic", `main() {
			println(1 + 2 * 3, 7 / 2, -7 % 3, 1.5 + 1, "a" + "b", 1 == 1.0, 0.1 + 0.2);
			println(10 - 2 - 3, 2 * 3 % 4, 1 / 2.0, 7.5 % 2, 6 & 3, 6 | 3, 6 ^ 3, ~5, -(3));
		}`, "7 3 -1 2.5 ab true 0.30000000000000004\n5 2 0.5 1.5 2 7 5 -6 -3\n"},
		{"wraparound", `main() {
			println(0x7FFFFFFFFFFFFFFF + 1, -9223372036854775808, 0xFFFFFFFFFFFFFFFF, 1 << 63, -8 >> 1);
		}`, "-9223372036854775808 -9223372036854775808 -1 -9223372036854775808 -4\n"},
		{"comparisons", `main() {
			println(1 < 2, 2 <= 1.5, "a" < "b", 3 > 2.5, 2 >= 2.0, 1 != 1.0, nil == nil, "x" == "x", 0 == false);
		}`, "true false true true true false true true false\n"},
		{"globals and constants", `
			global g = 10, h;
			const C = 2 * 3 + 1, S = "x" + "y", B = C > 5 && !nil, N = -C;
			main() { g += C; println(g, h, S, B, N); }`, "17 nil xy true -7\n"},
		{"global initializers run in order", `
			global a = 1, b = a + 1;
			global c = f();
			f() { return b * 10; }
			main() { println(a, b, c); }`, "1 2 20\n"},
		{"global used before its initializer", `
			global x = y;
			global y = 5;
			main() { println(x, y); }`, "nil 5\n"},
		{"recursion", `
			fib(n) { if (n < 2) return n; return fib(n - 1) + fib(n - 2); }
			main() { println(fib(20)); }`, "6765\n"},
		{"locals at the start of the body", `
			f(a) { local t, u; t = a * 2; println(u); return t; }
			main() { println(f(21)); }`, "nil\n42\n"},
		{"scopes and slot reuse", `main() {
			local x = 1;
			{ local x = 2; println(x); }
			println(x);
			{ local y; println(y); }
			local z = x + 1, w = z * 10;
			println(z, w);
		}`, "2\n1\nnil\n2 20\n"},
		{"a local is visible after its initializer", `
			global x = "global";
			main() { local x = x + "!"; println(x); }`, "global!\n"},
		{"while, do-while, break, continue", `main() {
			local i = 0, sum = 0;
			while (1) { i++; if (i > 10) break; if (i % 2) continue; sum += i; }
			println(i, sum);
			i = 0;
			do { i++; if (i == 2) continue; print(i, ""); } while (i < 5);
			println();
			do i--; while (i > 100);
			println(i);
		}`, "11 30\n1 3 4 5 \n4\n"},
		{"for loops", `main() {
			for (local i = 0; i < 3; i++) print(i);
			println();
			local j;
			for (j = 10; j > 0; j -= 4) { if (j == 6) continue; print(j, ""); }
			println(j);
			for (local i = 0; ; i++) if (i == 4) { println("stop", i); break; }
			for (local a = 1, b = 2; a < b; a++, b--) println(a, b);
		}`, "012\n10 2 -2\nstop 4\n1 2\n"},
		{"nested loops: break and continue the inner loop", `main() {
			for (local i = 0; i < 3; i++) {
				for (local j = 0; j < 3; j++) { if (j == 1) continue; if (j == 2) break; print(i, j, ""); }
			}
			println();
		}`, "0 0 1 0 2 0 \n"},
		{"&& and || short-circuit and give booleans", `
			global calls = 0;
			t() { calls++; return true; }
			f() { calls++; return false; }
			main() {
				println(f() && t(), t() || f(), calls);
				println(1 && "x", nil || 0, !nil, !0, !"", !!2.5);
				if (t() && !f() || f()) println("yes", calls);
			}`, "false true 2\ntrue false true true false true\nyes 4\n"},
		{"conditional, and the comma in a for loop", `main() {
			local i, j, s = "";
			println(1 ? "a" : "b", 0 ? "a" : "b", 0 ? 1 : 0 ? 2 : 3);
			for (i = 0, j = 3; i < j; i++, j--) s += str(i) + str(j) + " ";
			println(s);
		}`, "a b 3\n03 12 \n"},
		{"assignment values", `main() {
			local a, b;
			a = b = 5;
			println(a, b, (a += 2) * 2, a);
			a <<= 2; a |= 1; a ^= 3; a %= 7; a /= 1;
			println(a);
		}`, "5 5 14 7\n2\n"},
		{"increment and decrement", `
			global g = 1.5;
			main() { local i = 5; println(i++, i, ++i, i--, --i); g++; println(g); }`, "5 6 7 7 5\n2.5\n"},
		{"else if chain", `
			sign(x) { if (x < 0) return "neg"; else if (x == 0) return "zero"; else if (x < 10) return "small"; else return "big"; }
			main() { println(sign(-1), sign(0), sign(5), sign(50)); }`, "neg zero small big\n"},
		{"functions are values", `
			global g;
			sq(x) { return x * x; }
			main() { g = println; g("hi"); local h = sq; println(h(4), sq, println); }`,
			"hi\n16 <function sq> <builtin println>\n"},
		{"builtins str and typeof", `main() {
			println(typeof(1), typeof(1.0), typeof("s"), typeof(nil), typeof(true), typeof(main), typeof(println));
			println(str(1.0) + "!", str("s"), str(nil) + str(2));
		}`, "int float string nil bool function function\n1.0! s nil2\n"},
		{"print formats", `main() { print("a", 1, 2.0, nil, true, "\n"); println(); println(1e300 * 1e10, -1 / 0.0); }`,
			"a 1 2.0 nil true \n\ninf -inf\n"},
		{"function without return gives nil", `
			f() { }
			g(x) { if (x) return; return 1; }
			main() { println(f(), g(1), g(0)); }`, "nil nil 1\n"},
		// milestone 4
		{"default parameter values", `
			global n = 0;
			next() { n++; return n; }
			f(a = 1) { return a; }
			g(a, b = a * 2, c = b + 1) { return str(a) + str(b) + str(c); }
			h(x = next()) { return x; }
			main() {
				println(f(), f(nil), f(2));
				println(g(1), g(1, 5), g(1, 5, 0));
				println(h(), h(), h(10), h());
			}`, "1 nil 2\n123 156 150\n1 2 10 3\n"},
		{"rest parameters", `
			f(a, rest..) { println(a, rest); }
			g(a, b = 0, rest..) { println(a, b, rest); }
			all(xs..) { return xs; }
			main() { f(1); f(1, 2, 3); g(1); g(1, 2); g(1, 2, 3, 4); println(all(), typeof(all(1))); }`,
			"1 []\n1 [2, 3]\n1 0 []\n1 2 []\n1 2 [3, 4]\n[] vector\n"},
		{"spread arguments", `
			pack(xs..) { return xs; }
			f(a, b, c) { println(a, b, c); }
			wrapper(args..) { return f(args..); }
			main() {
				local v = pack(1, 2);
				f(v.., 3);
				f(0, v..);
				f(pack(7, 8, 9)..);
				f(pack().., 4, 5, 6);
				println(v.., "x", v..);
				wrapper("a", "b", "c");
				println(v);
			}`, "1 2 3\n0 1 2\n7 8 9\n4 5 6\n1 2 x 1 2\na b c\n[1, 2]\n"},
		{"labelled break and continue", `main() {
			outer: for (local i = 0; i < 4; i++) {
				for (local j = 0; j < 4; j++) {
					if (j > i) continue outer;
					if (i == 3) break outer;
					print(i, j, "");
				}
			}
			println();
			blk: { print("a"); break blk; print("b"); }
			skip: if (1) { break skip; println("no"); }
			println("c");
			a: b: while (1) { while (1) { break b; } }
			local k = 0;
			again: do { k++; if (k < 3) continue again; } while (k < 5);
			println(k);
		}`, "0 0 1 0 1 1 2 0 2 1 2 2 \nac\n5\n"},
		{"switch", `
			name(n) {
				local r;
				switch (n) {
					case 1: r = "one"; break;
					case 2:
					case 3: r = "two or three"; break;
					case "x": r = "ex"; break;
					case nil: r = "nil"; break;
					default: r = "other";
				}
				return r;
			}
			main() { println(name(1), name(3), name(1.0), name("x"), name(nil), name(9)); }`,
			"one two or three one ex nil other\n"},
		{"switch fall-through and default anywhere", `main() {
			switch (2) { case 1: print("1"); case 2: print("2"); case 3: print("3"); }
			switch (5) { case 1: print("no"); }
			switch (9) { case 1: print(1); default: print("d"); case 2: print(2); }
			switch (1) { }
			println();
		}`, "23d2\n"},
		{"Duff's device", `
			count(n) {
			    local out, rounds;
				out = 0;
				rounds = (n + 3) / 4;
				switch (n % 4) {
					case 0: do { out++;
					case 3: out++;
					case 2: out++;
					case 1: out++;
					} while (--rounds > 0);
				}
				return out;
			}
			main() { println(count(1), count(5), count(8), count(9), count(11)); }`, "1 5 8 9 11\n"},
		{"switch with loops: break and continue", `
			global evals = 0;
			tag(x) { evals++; return x; }
			main() {
				for (local i = 0; i < 4; i++) {
					switch (i) { case 1: continue; case 2: break; }
					print(i);
				}
				println();
				sw: switch (tag(1)) {
					case 1:
						while (1) { switch (2) { case 2: break sw; } }
				}
				switch (tag(1)) { case 1: for (;;) break; print("after"); }
				println("", evals);
				switch (3) { case 3: switch (4) { case 4: print("inner"); break; } print("outer"); }
				println();
			}`, "023\nafter 2\ninnerouter\n"},
		// milestone 5
		{"vectors", `main() {
			local v = [1, [2, 3], "x"], w = v, e = [];
			v[0] = 10; v[1][0] *= 5; w[2] += "y";
			println(v, len(v), len(e), v[1][1], v == w, [1] == [1], v != e);
			local p = [0, v.., 99];
			println(p, len(p));
		}`, "[10, [10, 3], \"xy\"] 3 0 3 true false true\n[0, 10, [10, 3], \"xy\", 99] 5\n"},
		{"index assignment values and ++/--", `main() {
			local v = [1, 2, 3], m = {"n": 5};
			println(v[0] = 7, v[1] += 10, v[2]++, v[2], ++v[2], v[0]--, --m["n"], m["n"]++, m);
			v[0]++; m["n"] += 1;
			println(v, m);
		}`, "7 12 3 4 5 7 4 4 {\"n\": 5}\n[7, 12, 5] {\"n\": 6}\n"},
		{"index operands are evaluated once", `
			global calls = 0;
			at(i) { calls++; return i; }
			main() { local v = [1, 2]; v[at(1)] += 5; v[at(0)]++; println(v, calls); }`, "[2, 7] 2\n"},
		{"maps", `main() {
			local m = {"b": 2, "a": 1, 1: "one", 2.5: "x"};
			m[1.0] = "uno"; m["c"] = 3;
			println(m, m["a"], m["zz"], m[1], m[2.5], len(m));
			delete(m, "b"); m["b"] = 20;
			println(keys(m), values(m), haskey(m, "a"), haskey(m, "zz"), delete(m, "zz"));
			local name = "k";
			println({name: 1}, {"name": 1}, {}, {1: {2: [3]}});
			local n = {-0.0: "zero", 1e300: "big"};
			println(n[0], n[1e300]);
		}`, "{\"b\": 2, \"a\": 1, 1: \"uno\", 2.5: \"x\", \"c\": 3} 1 nil uno x 5\n" +
			"[\"a\", 1, 2.5, \"c\", \"b\"] [1, \"uno\", \"x\", 3, 20] true false false\n" +
			"{\"k\": 1} {\"name\": 1} {} {1: {2: [3]}}\nzero big\n"},
		{"map compaction keeps order", `main() {
			local m = {};
			for (local i = 0; i < 100; i++) m[i] = i;
			for (local i = 0; i < 95; i++) delete(m, i);
			m["new"] = 1;
			println(keys(m), len(m), m[97]);
		}`, "[95, 96, 97, 98, 99, \"new\"] 6 97\n"},
		{"strings", `main() {
			local s = "héllo";
			println(len(s), s[0], s[1], slice(s, 3), slice(s, 0, 1), find(s, "l"), find(s, "l", 5), find(s, "z"));
			println(split("a,b,,c", ","), split("abc", ""), join(["x", 1, 2.5, nil], "-"), join([]), join(["a", "b"]));
			println(upper("Hi there!"), lower("ABC é"), trim("  \t x y \n"), replace("a-b-c", "-", "+"));
			println(char(65) + char(0x42), ord("A"), ord("AB", 1), slice("hello", 1, 3), slice("hello", 2));
		}`, "6 104 195 llo h 3 -1 -1\n[\"a\", \"b\", \"\", \"c\"] [\"a\", \"b\", \"c\"] x-1-2.5-nil  ab\n" +
			"HI THERE! abc é x y a+b+c\nAB 65 66 el llo\n"},
		{"vector built-ins", `
			desc(a, b) { return a > b; }
			byLen(a, b) { return len(a) < len(b); }
			main() {
				local v = [5, 3, 8, 1];
				println(push(v, 9, 2), v, pop(v), v);
				insert(v, 0, 0); insert(v, len(v), 100); insert(v, 2, 7);
				println(v, remove(v, 2), v, slice(v, 1, 3), slice(v, 6), slice(v, 0, 0));
				sort(v); println(v);
				sort(v, desc); println(v);
				local words = ["ccc", "a", "bb", "dd", "e"];
				sort(words, byLen); println(words);
				sort(words); println(words);
				local copy = slice(v, 0); copy[0] = -1; println(v[0], copy[0]);
			}`, "6 [5, 3, 8, 1, 9] 2 [5, 3, 8, 1, 9]\n" +
			"[0, 5, 3, 8, 1, 9, 100] 7 [0, 5, 3, 8, 1, 9, 100] [5, 3] [100] []\n" +
			"[0, 1, 3, 5, 8, 9, 100]\n[100, 9, 8, 5, 3, 1, 0]\n" +
			"[\"a\", \"e\", \"bb\", \"dd\", \"ccc\"]\n[\"a\", \"bb\", \"ccc\", \"dd\", \"e\"]\n100 -1\n"},
		{"for-each", `main() {
			for (local x in [1, 2, 3]) print(x, "");
			for (local i, x in ["a", "b"]) print(i, x, "");
			println();
			local m = {"x": 1, "y": 2};
			for (local k in m) print(k, "");
			for (local k, v in m) print(k, v, "");
			for (local c in "AB") print(c, "");
			for (local i, c in "AB") print(i, c, "");
			for (local x in []) print("never");
			for (local k in {}) print("never");
			println();
		}`, "1 2 3 0 a 1 b \nx y x 1 y 2 65 66 0 65 1 66 \n"},
		{"for-each with break, continue and labels", `main() {
			outer: for (local row in [[1, 2], [3, 4], [5, 6]]) {
				for (local x in row) {
					if (x == 2) continue outer;
					if (x == 5) break outer;
					print(x, "");
				}
			}
			println();
			for (local x in [1, 2, 3, 4]) { if (x % 2) continue; print(x, ""); }
			println();
		}`, "1 3 4 \n2 4 \n"},
		{"for-each sees changes made during the loop", `main() {
			local v = [1, 2];
			for (local x in v) { print(x, ""); if (x < 4) push(v, x + 2); }
			println(v);
			local m = {"a": 1, "b": 2, "c": 3};
			for (local k, x in m) { print(k, ""); delete(m, "b"); m["d"] = 4; m["c"] = 30; if (k == "c") println(x); }
			println(m);
		}`, "1 2 3 4 5 [1, 2, 3, 4, 5]\na c 30\n{\"a\": 1, \"c\": 30, \"d\": 4}\n"},
		{"for-each variables are scoped to the loop", `
			global x = "global";
			main() { for (local x in [1]) print(x, ""); println(x); local v = [7]; for (local v in v) println(v); }`,
			"1 global\n7\n"},
		{"printing nested and cyclic containers", `main() {
			local v = [1], m = {"self": nil};
			push(v, v); m["self"] = m; m["v"] = v;
			println(v, m, [m, m]);
		}`, "[1, [...]] {\"self\": {...}, \"v\": [1, [...]]} [{\"self\": {...}, \"v\": [1, [...]]}, {\"self\": {...}, \"v\": [1, [...]]}]\n"},
		{"typeof and str of containers", `main() { println(typeof([]), typeof({}), str([1, "a"]) + "!", str({"k": "v"})); }`,
			"vector map [1, \"a\"]! {\"k\": \"v\"}\n"},
		// milestone 6
		{"classes, constructors and members", `
			class Point {
				x, y;
				Point(x, y);
				len2();
				moved(dx, dy);
			}
			Point::Point(x, y = 0) { this->x = x; this->y = y; return 42; }
			Point::len2() { return x * x + y * y; }
			Point::moved(dx, dy) { return new Point(x + dx, y + dy); }
			main() {
				local p = new Point(3, 4), q = p->moved(1, 1), r = new Point(7);
				println(p, q, r, p->len2(), p->x, q->y);
				p->x = 0; p->y += 1; q->x++;
				println(p, q, p == p, p == new Point(0, 5), typeof(p), typeof(Point), Point);
			}`, "Point{x: 3, y: 4} Point{x: 4, y: 5} Point{x: 7, y: 0} 25 3 5\n" +
			"Point{x: 0, y: 5} Point{x: 5, y: 5} true false object class <class Point>\n"},
		{"inheritance and virtual dispatch", `
			class Animal { name; Animal(name); speak(); sound(); }
			Animal::Animal(n) { name = n; }
			Animal::speak() { return name + " says " + sound(); }
			Animal::sound() { return "..."; }
			class Dog : Animal { Dog(); sound(); }
			Dog::Dog() { Animal::Animal("dog"); }
			Dog::sound() { return "woof"; }
			class Puppy : Dog { Puppy(); sound(); }
			Puppy::Puppy() { Dog::Dog(); name = "puppy"; }
			Puppy::sound() { return Dog::sound() + "!"; }
			main() {
				for (local a in [new Animal("thing"), new Dog(), new Puppy()]) println(a->speak(), a);
				println(instanceof(new Puppy(), Animal), instanceof(new Dog(), Puppy), instanceof(1, Dog), classof(new Puppy()));
			}`, "thing says ... Animal{name: \"thing\"}\ndog says woof Dog{name: \"dog\"}\npuppy says woof! Puppy{name: \"puppy\"}\n" +
			"true false false <class Puppy>\n"},
		{"static members", `
			class Counter { static count, label; n; Counter(); static report(); bump(); }
			Counter::Counter() { if (count == nil) count = 0; count++; n = count; }
			Counter::report() { return label + ": " + str(count); }
			Counter::bump() { Counter::count += 10; return count; }
			class Sub : Counter { Sub(); }
			Sub::Sub() { Counter::Counter(); }
			main() {
				Counter::label = "made";
				local a = new Counter(), b = new Sub();
				println(Counter::report(), a->n, b->n, Sub::count, b->bump(), Counter::count);
				local r = Counter::report;
				println(r(), a->report(), typeof(r));
			}`, "made: 2 1 2 2 12 12\nmade: 12 made: 12 function\n"},
		{"bound methods", `
			class Box { v; Box(v); get(); static less(a, b); }
			Box::Box(v) { this->v = v; }
			Box::get() { return v; }
			Box::less(a, b) { return a->v < b->v; }
			main() {
				local b = new Box(5), g = b->get;
				b->v = 6;
				println(g(), g == b->get, g == new Box(6)->get, g);
				local boxes = [new Box(3), new Box(1), new Box(2)];
				sort(boxes, Box::less);
				println(boxes);
			}`, "6 true false <method Box::get>\n[Box{v: 1}, Box{v: 2}, Box{v: 3}]\n"},
		{"unqualified member calls are virtual", `
			class A { run(); step(); static helper(); }
			A::run() { return step() + helper(); }
			A::step() { return "A"; }
			A::helper() { return "!"; }
			class B : A { step(); }
			B::step() { return "B"; }
			main() { println(new A()->run(), new B()->run()); }`, "A! B!\n"},
		{"new: classes in variables, no constructor, data members in subclasses", `
			class Plain { a, b; }
			class Base { info(); }
			Base::info() { return this->extra; }
			class Derived : Base { extra; Derived(); }
			Derived::Derived() { extra = "from derived"; }
			main() {
				local kinds = [Plain, Derived];
				for (local k in kinds) println(new k());
				println(new Derived()->info());
			}`, "Plain{a: nil, b: nil}\nDerived{extra: \"from derived\"}\nfrom derived\n"},
		{"methods with default and rest parameters, spread", `
			class Log { lines; Log(); add(level, parts..); }
			Log::Log() { lines = []; }
			Log::add(level = "info", parts..) { push(lines, level + ": " + join(parts, " ")); return this; }
			main() {
				local l = new Log(), words = ["a", "b"];
				l->add()->add("warn", "x", "y")->add("debug", words..);
				println(l->lines);
			}`, "[\"info: \", \"warn: x y\", \"debug: a b\"]\n"},
		{"objects inside themselves print safely", `
			class Node { next; }
			main() { local n = new Node(); n->next = n; println(n, [n]); }`,
			"Node{next: Node{...}} [Node{next: Node{...}}]\n"},
		// milestone 7
		{"catching runtime errors", "f(x) {\n return 10 / x;\n}\nmain() {\n try { f(0); } catch (e) {\n  println(e->kind, \"|\", e->message, \"|\", e->trace, instanceof(e, Error));\n }\n println(\"after\");\n}",
			"ZeroDivisionError | integer division by zero | [\"f (t.kkl:2)\", \"main (t.kkl:5)\"] true\nafter\n"},
		{"only Error objects can be thrown", `main() {
			for (local v in [1, "s", [2], {"k": 3}, nil]) {
				try { throw v; } catch (e) { print(e->kind, ""); }
			}
			println();
		}`, "TypeError TypeError TypeError TypeError TypeError \n"},
		{"finally runs on every way out", `
			global trail = [];
			f(n) {
				try {
					if (n == 0) return "returned";
					if (n == 1) throw new Error("thrown");
					push(trail, "body " + str(n));
				} finally {
					push(trail, "finally " + str(n));
				}
				return "end";
			}
			main() {
				for (local n = 0; n < 3; n++) {
					try { push(trail, f(n)); } catch (e) { push(trail, "caught " + e->message); }
				}
				for (local i = 0; i < 3; i++) {
					try { if (i == 0) continue; if (i == 2) break; push(trail, "loop " + str(i)); }
					finally { push(trail, "loop finally " + str(i)); }
				}
				println(join(trail, "; "));
			}`, "finally 0; returned; finally 1; caught thrown; body 2; finally 2; end; " +
			"loop finally 0; loop 1; loop finally 1; loop finally 2\n"},
		{"the return value is computed before finally runs", `
			f() { local x; x = 1; try { return x; } finally { x = 2; println("x is", x); } }
			main() { println(f()); }`, "x is 2\n1\n"},
		{"nested finally blocks run innermost first", `main() {
			outer: for (local i = 0; i < 2; i++) {
				try {
					try {
						for (local j = 0; j < 2; j++) { try { break outer; } finally { print("a "); } }
					} finally { print("b "); }
				} finally { print("c "); }
			}
			try { try { throw new Error("x"); } finally { print("d "); } } catch (e) { print("caught", e->message); }
			println();
		}`, "a b c d caught x\n"},
		{"exceptions in catch and finally", `main() {
			try {
				try { throw new Error("first"); } catch (e) { throw new Error("from catch: " + e->message); } finally { print("cleanup "); }
			} catch (e) { println(e->message); }
			try {
				try { throw new Error("lost"); } finally { throw new Error("from finally"); }
			} catch (e) { println(e->message); }
		}`, "cleanup from catch: first\nfrom finally\n"},
		{"rethrowing keeps the original trace", "f() {\n [1][2];\n}\nmain() {\n try {\n  try { f(); } catch (e) { throw e; }\n } catch (e2) { println(e2->trace); }\n}",
			"[\"f (t.kkl:2)\", \"main (t.kkl:6)\"]\n"},
		{"Error classes", `
			class ParseError : Error { line; ParseError(msg, line); }
			ParseError::ParseError(msg, line) { Error::Error(msg); this->line = line; }
			main() {
				try { throw new ParseError("bad token", 7); }
				catch (e) { println(e->kind, e->message, e->line, instanceof(e, Error), classof(e)); }
				try { throw new Error("custom", "MyKind"); } catch (e) { println(e->kind, e->message); }
				local e = new Error("not thrown");
				println(e->kind, e->trace);
			}`, "ParseError bad token 7 true <class ParseError>\nMyKind custom\nnil nil\n"},
		{"stack overflow can be caught", `
			down(n) { return down(n + 1); }
			count(n) { if (n == 0) return 0; return 1 + count(n - 1); }
			main() {
				try { down(0); } catch (e) { println(e->kind, len(e->trace)); }
				println(count(5000));
			}`, "StackOverflowError 10000\n5000\n"},
		{"exceptions cross built-ins", `
			bad(a, b) { throw new Error("from comparator"); }
			main() {
				try { sort([2, 1], bad); } catch (e) { println("caught:", e->message); }
				try { sort([2, 1], fn2); } catch (e) { println(e->kind, e->trace); }
			}
			fn2(a, b) { return a < nil; }`,
			"caught: from comparator\nTypeError [\"fn2 (t.kkl:7)\", \"main (t.kkl:5)\"]\n"},
		{"the stack is restored after a catch", `
			g() { try { return 1 / 0; } catch (e) { return 10; } }
			h(n) { if (n == 0) throw new Error("deep"); return h(n - 1) + 1; }
			main() {
				local a = 5;
				println(1 + g() * 2, a);
				local v = [];
				for (local i = 0; i < 3; i++) {
					try { push(v, i + h(i)); } catch (e) { push(v, e->message); }
				}
				println(v, a + 1);
			}`, "21 5\n[\"deep\", \"deep\", \"deep\"] 6\n"},
		{"a throwing constructor", `
			class A { A(x); }
			A::A(x) { if (x) throw new Error("no"); }
			main() {
				local a;
				try { a = new A(1); } catch (e) { println("caught", e->message, a); }
				println(new A(0));
			}`, "caught no nil\nA{}\n"},
		// milestone 8
		{"closures share captured variables", `
			counter(start = 0) {
			    local n;
				n = start;
				return [fn() { return ++n; }, fn() { return n; }, fn(v) { n = v; }];
			}
			main() {
				local a = counter(10), b = counter();
				a[0](); a[0](); b[0]();
				println(a[1](), b[1](), a[0](), a[1]());
				a[2](100);
				println(a[1](), b[1]());
				local y = 1, setY = fn(v) { y = v; };
				setY(42);
				println(y, typeof(setY));
			}`, "12 1 13 13\n100 1\n42 function\n"},
		{"fresh loop variables for every iteration", `main() {
			local fs = [];
			for (local i = 0; i < 3; i++) push(fs, fn() { return i; });
			for (local x in ["a", "b"]) push(fs, fn() { return x; });
			for (local k, v in {"p": 1}) push(fs, fn() { return k + str(v); });
			local j = 0;
			while (j < 2) { local w = j * 10; push(fs, fn() { return w; }); j++; }
			local out = [];
			for (local f in fs) push(out, f());
			println(out);
			// the loop sees changes a closure makes in its own iteration
			for (local i = 0; i < 6; i++) { local skip = fn() { i++; }; skip(); print(i, ""); }
			println();
		}`, "[0, 1, 2, \"a\", \"b\", \"p1\", 0, 10]\n1 3 5 \n"},
		{"nested function literals", `main() {
			local add = fn(a) { return fn(b) { return fn(c) { return a + b + c; }; }; };
			println(add(1)(2)(3));
			local x = 0;
			local outer = fn() { local mid = fn() { local inner = fn() { x += 5; }; inner(); }; mid(); };
			outer(); outer();
			println(x);
		}`, "6\n10\n"},
		{"recursion through a captured variable", `main() {
			local fact;
			fact = fn(n) { return n <= 1 ? 1 : n * fact(n - 1); };
			println(fact(10));
		}`, "3628800\n"},
		{"closures capture parameters, locals and catch variables", `
			make(p) { local t; t = p * 2; return fn() { return [p, t]; }; }
			catcher() { try { throw new Error("e1"); } catch (e) { return fn() { return e->message; }; } }
			main() { println(make(4)(), catcher()()); }`, "[4, 8] e1\n"},
		{"closures in member functions see this and members", `
			class Button { label, clicks; Button(l); onClick(); }
			Button::Button(l) { label = l; clicks = 0; }
			Button::onClick() { return fn(who) { clicks++; return who + " clicked " + label + " " + str(this->clicks); }; }
			main() {
				local b = new Button("ok"), h = b->onClick();
				println(h("ann"));
				println(h("bob"), b->clicks);
			}`, "ann clicked ok 1\nbob clicked ok 2 2\n"},
		{"function literals as values", `
			global square = fn(x) { return x * x; };
			apply(f, args..) { return f(args..); }
			main() {
				local words = ["ccc", "a", "bb"];
				sort(words, fn(a, b) { return len(a) < len(b); });
				println(words, apply(square, 9), apply(fn(a, b = 10) { return a + b; }, 1));
				local f = fn() { };
				println(f == f, f == fn() { }, f(), square);
			}`, "[\"a\", \"bb\", \"ccc\"] 81 11\ntrue false nil <function <init>/fn@2>\n"},
		{"constructors are inherited when a class declares none", `
			class ValidationError : Error { }
			class A { x; A(x); }
			A::A(x) { this->x = x * 10; }
			class B : A { }
			class C : B { }
			class D : C { y; D(); }
			D::D() { y = "own"; }
			main() {
				try { throw new ValidationError("age must not be negative"); }
				catch (e) { println(e->kind, e->message); }
				println(new C(5), new D(), classof(new C(1)));
			}`, "ValidationError age must not be negative\nC{x: 50} D{x: nil, y: \"own\"} <class C>\n"},
		// milestone 11
		{"conversions", `main() {
			println(int("42"), int(" -0x10 "), int("1_000"), int("0b101"), int("0xFFFFFFFFFFFFFFFF"), int("-9223372036854775808"));
			println(int(3.9), int(-3.9), int(true), int(false), int(7));
			println(float("2.5"), float(" 1e3 "), float(3), float("inf"), float("-0"), float(1.5), nil == nil, 0 == nil);
		}`, "42 -16 1000 5 -1 -9223372036854775808\n3 -3 1 0 7\n2.5 1000.0 3.0 inf -0.0 1.5 true false\n"},
		{"math", `main() {
			println(abs(-5), abs(-2.5), abs(-9223372036854775808), min(3, 1, 2), max([4, 9, 2]), min("b", "a"), max(1, 2.5));
			println(pow(2, 10), pow(2, 64), pow(-2, 3), pow(2, -1), pow(2.0, 3), pow(4, 0.5));
			println(floor(2.7), ceil(2.1), round(2.5), round(-2.5), floor(-0.5), round(7), typeof(floor(1.5)));
			println(sqrt(16), atan2(1, 1) * 4, log(exp(1)), sin(0), cos(0), tan(0));
		}`, "5 2.5 -9223372036854775808 1 9 a 2.5\n1024 0 -8 0.5 8.0 2.0\n2 3 3 -3 -1 7 int\n4.0 3.141592653589793 1.0 0.0 1.0 0.0\n"},
		{"random numbers", `main() {
			seed(42);
			local a = [random(), randint(1, 6), randint(-5, 5)];
			seed(42);
			local b = [random(), randint(1, 6), randint(-5, 5)];
			println(a == b, str(a) == str(b), a[0] >= 0 && a[0] < 1, a[1] >= 1 && a[1] <= 6);
			local seen = {};
			for (local i = 0; i < 200; i++) seen[randint(1, 3)] = true;
			println(len(seen), randint(5, 5), typeof(randint(-9223372036854775808, 9223372036854775807)));
		}`, "false true true true\n3 5 int\n"},
		{"time, clock, getenv", `main() {
			println(typeof(time()), time() > 1.7e9, clock() >= 0, clock() < 60, getenv("KUKEL_NO_SUCH_VARIABLE"));
		}`, "float true true true nil\n"},
		{"deep recursion within the limit", `
			down(n) { if (n == 0) return 0; return 1 + down(n - 1); }
			main() { println(down(9000)); }`, "9000\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code, err := runSource(t, tt.src)
			if err != nil {
				t.Fatalf("runtime error: %v\noutput so far: %q", err, out)
			}
			if out != tt.want || code != 0 {
				t.Errorf("got %q (exit %d), want %q", out, code, tt.want)
			}
		})
	}
}

func TestMainArgsAndExitCode(t *testing.T) {
	out, code, err := runSource(t, `main(args) { println(args, typeof(args)); return 3; }`, "a", "b c")
	if err != nil || out != "[\"a\", \"b c\"] vector\n" || code != 3 {
		t.Errorf("got %q, exit %d, %v", out, code, err)
	}
	_, code, err = runSource(t, `main() { return "x"; }`)
	if err != nil || code != 0 {
		t.Errorf("non-int result: exit %d, %v", code, err)
	}
}

func TestRuntimeErrors(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"division by zero, with trace", `f(x) {
	return 1 / x;
}
main() {
	println("before");
	f(0);
}`, "ZeroDivisionError: integer division by zero\n  at f (t.kkl:2)\n  at main (t.kkl:6)"},
		{"wrong argument count", "f() {}\nmain() { local g = f; g(1); }",
			"ArgumentError: f expects 0 argument(s), got 1\n  at main (t.kkl:2)"},
		{"calling a non-function", "main() {\n local x = 1;\n x();\n}",
			"TypeError: cannot call int\n  at main (t.kkl:3)"},
		{"type error", `main() { println(1 + "a"); }`,
			"TypeError: cannot apply + to int and string\n  at main (t.kkl:1)"},
		{"compare error", `main() { if (nil < 1) ; }`,
			"TypeError: cannot compare nil and int with <\n  at main (t.kkl:1)"},
		{"shift error", `main() { local n = 64; println(1 << n); }`,
			"RangeError: shift count 64 out of range 0..63\n  at main (t.kkl:1)"},
		{"builtin error", `main() { str(); }`,
			"ArgumentError: str expects 1 argument(s), got 0\n  at main (t.kkl:1)"},
		{"error in a global initializer", "global g = 1 % 0;\nmain() { }",
			"ZeroDivisionError: integer modulo by zero\n  at <init> (t.kkl:1)"},
		{"too few arguments with optional parameters", "f(a, b = 1, c = 2) {}\nmain() { local g = f; g(); }",
			"ArgumentError: f expects 1 to 3 arguments, got 0\n  at main (t.kkl:2)"},
		{"too many arguments with optional parameters", "f(a = 1) {}\nmain() { local g = f; g(1, 2); }",
			"ArgumentError: f expects 0 to 1 arguments, got 2\n  at main (t.kkl:2)"},
		{"too few arguments with a rest parameter", "f(a, b, c..) {}\nmain() { local g = f; g(1); }",
			"ArgumentError: f expects at least 2 argument(s), got 1\n  at main (t.kkl:2)"},
		{"spread of a non-vector", "f(x..) {}\nmain() { local n = 1; f(n..); }",
			"TypeError: cannot spread int (a vector is needed)\n  at main (t.kkl:2)"},
		{"spread of a non-vector after a spread", "f(x..) { return x; }\nmain() { f(f()..,\n 1, nil..); }",
			"TypeError: cannot spread nil (a vector is needed)\n  at main (t.kkl:3)"},
		{"error in a default value", "f(a = 1 / 0) {}\nmain() { f(); }",
			"ZeroDivisionError: integer division by zero\n  at f (t.kkl:1)\n  at main (t.kkl:2)"},
		{"vector index out of range", "main() {\n local v = [1, 2, 3];\n v[3] = 0;\n}",
			"RangeError: index 3 out of range for vector of length 3\n  at main (t.kkl:3)"},
		{"negative index", "main() { local v = [1]; println(v[-1]); }",
			"RangeError: index -1 out of range for vector of length 1\n  at main (t.kkl:1)"},
		{"index with a non-int", `main() { local v = [1]; println(v["0"]); }`,
			"TypeError: vector index must be an int, not string\n  at main (t.kkl:1)"},
		{"indexing an int", "main() { local n = 1; println(n[0]); }",
			"TypeError: cannot index int\n  at main (t.kkl:1)"},
		{"strings cannot be changed", `main() { local s = "abc"; s[0] = 65; }`,
			"TypeError: strings cannot be changed (s[i] = x)\n  at main (t.kkl:1)"},
		{"bad map key in a literal", "main() { local m = {[1]: 2}; }",
			"TypeError: map keys must be strings or numbers, not vector\n  at main (t.kkl:1)"},
		{"nan as a map key", "main() { local m = {}; m[0.0 / 0.0] = 1; }",
			"TypeError: nan cannot be a map key\n  at main (t.kkl:1)"},
		{"for-each over an int", "main() {\n for (local x in 42) ;\n}",
			"TypeError: cannot iterate over int\n  at main (t.kkl:2)"},
		{"len of an int", "main() { len(1); }",
			"TypeError: int has no length\n  at main (t.kkl:1)"},
		{"built-in argument type", "main() { slice(1, 0); }",
			"TypeError: slice: argument 1 must be a vector or string, not int\n  at main (t.kkl:1)"},
		{"built-in argument range", `main() { slice("abc", 4); }`,
			"RangeError: slice: start 4 out of range 0..3\n  at main (t.kkl:1)"},
		{"pop from an empty vector", "main() { pop([]); }",
			"RangeError: pop from an empty vector\n  at main (t.kkl:1)"},
		{"sort of mixed values", `main() { sort([1, "a"]); }`,
			"TypeError: cannot compare string and int with <\n  at main (t.kkl:1)"},
		{"error inside a sort comparator", "bad(a, b) {\n return a < nil;\n}\nmain() { sort([2, 1], bad); }",
			"TypeError: cannot compare int and nil with <\n  at bad (t.kkl:2)\n  at main (t.kkl:4)"},
		{"unknown member", "class A { x; }\nmain() { local a = new A();\n println(a->y); }",
			"MemberError: class A has no member y\n  at main (t.kkl:3)"},
		{"unknown method", "class A { x; }\nmain() { new A()->go(); }",
			"MemberError: class A has no member function go\n  at main (t.kkl:2)"},
		{"calling a data member as a method", "class A { x; }\nmain() { new A()->x(); }",
			"MemberError: x is a data member of class A, not a member function (call its value with (p->x)(...))\n  at main (t.kkl:2)"},
		{"setting an unknown data member", "class A { }\nmain() { local a = new A(); a->x = 1; }",
			"MemberError: class A has no data member x\n  at main (t.kkl:2)"},
		{"assigning to a member function", "class A { f(); }\nA::f() {}\nmain() { local a = new A(); a->f = 1; }",
			"MemberError: cannot assign to member function f of class A\n  at main (t.kkl:3)"},
		{"member function declared but not defined", "class A { area(); }\nmain() { new A()->area(); }",
			"MemberError: A::area is declared but not defined\n  at main (t.kkl:2)"},
		{"constructor declared but not defined", "class A { A(); }\nmain() { local c = A; new c(); }",
			"MemberError: A::A is declared but not defined\n  at main (t.kkl:2)"},
		{"new with arguments and no constructor", "class A { }\nmain() { local c = A; new c(1); }",
			"ArgumentError: class A has no constructor, so new A() takes no arguments (got 1)\n  at main (t.kkl:2)"},
		{"inherited constructor argument count", "class A { A(x); }\nA::A(x) {}\nclass B : A { }\nmain() { local c = B; new c(); }",
			"ArgumentError: A::A expects 1 argument(s), got 0\n  at main (t.kkl:4)"},
		{"constructor argument count", "class A { A(x); }\nA::A(x) {}\nmain() { local c = A; new c(); }",
			"ArgumentError: A::A expects 1 argument(s), got 0\n  at main (t.kkl:3)"},
		{"new of a non-class", "global g = 1;\nmain() { new g(); }",
			"TypeError: cannot create an object of int with new\n  at main (t.kkl:2)"},
		{"calling a class", "class A { }\nmain() { local c = A; c(); }",
			"TypeError: cannot call class A (use new A(...))\n  at main (t.kkl:2)"},
		{"member of a non-object", "main() { local n = 1; println(n->x); }",
			"TypeError: cannot get member x of int\n  at main (t.kkl:1)"},
		{"method call on a non-object", "main() { local s = \"x\"; s->len(); }",
			"TypeError: cannot call method len on string\n  at main (t.kkl:1)"},
		{"error inside a method, with trace", "class A { f(); g(); }\nA::f() { return g(); }\nA::g() { return 1 / 0; }\nmain() { new A()->f(); }",
			"ZeroDivisionError: integer division by zero\n  at A::g (t.kkl:3)\n  at A::f (t.kkl:2)\n  at main (t.kkl:4)"},
		{"classof of a non-object", "main() { classof(1); }",
			"TypeError: classof: argument 1 must be an object, not int\n  at main (t.kkl:1)"},
		{"throwing a value that is not an Error object", "main() {\n local v = \"oops\";\n throw v;\n}",
			"TypeError: only Error objects can be thrown, not string (throw new Error(message, kind))\n  at main (t.kkl:3)"},
		{"uncaught Error object", "main() {\n throw new Error(\"bad\");\n}",
			"Error: bad\n  at main (t.kkl:2)"},
		{"uncaught Error without a message", "class Oops : Error { }\nmain() {\n throw new Oops();\n}",
			"Oops\n  at main (t.kkl:3)"},
		{"exception escaping a finally", "main() {\n try { 1 / 0; } finally { println(\"cleanup\"); }\n}",
			"ZeroDivisionError: integer division by zero\n  at main (t.kkl:2)"},
		{"error inside a function literal", "main() {\n local f = fn(x) {\n  return 1 / x;\n };\n f(0);\n}",
			"ZeroDivisionError: integer division by zero\n  at main/fn@2 (t.kkl:3)\n  at main (t.kkl:5)"},
		{"wrong argument count for a function literal", "main() {\n local f = fn(x) { };\n f();\n}",
			"ArgumentError: main/fn@2 expects 1 argument(s), got 0\n  at main (t.kkl:3)"},
		{"int of a bad string", `main() { int("12x"); }`,
			"ValueError: int: \"12x\" is not an integer\n  at main (t.kkl:1)"},
		{"int that does not fit", `main() { int("9223372036854775808"); }`,
			"RangeError: int: \"9223372036854775808\" is too large\n  at main (t.kkl:1)"},
		{"int of a float that does not fit", `main() { int(1e19); }`,
			"RangeError: int: 1e+19 does not fit in an int\n  at main (t.kkl:1)"},
		{"float of a bad string", `main() { float("one"); }`,
			"ValueError: float: \"one\" is not a number\n  at main (t.kkl:1)"},
		{"floor of nan", `main() { floor(0.0 / 0.0); }`,
			"RangeError: floor: nan does not fit in an int\n  at main (t.kkl:1)"},
		{"min of nothing", `main() { min([]); }`,
			"ArgumentError: min of no values\n  at main (t.kkl:1)"},
		{"max of mixed values", `main() { max(1, "a"); }`,
			"TypeError: cannot compare string and int with >\n  at main (t.kkl:1)"},
		{"sqrt of a string", `main() { sqrt("4"); }`,
			"TypeError: sqrt: argument 1 must be a number, not string\n  at main (t.kkl:1)"},
		{"randint with bounds reversed", `main() { randint(6, 1); }`,
			"RangeError: randint: 6 is greater than 1\n  at main (t.kkl:1)"},
		{"reading a missing file", `main() { readfile("/no/such/kukel/file"); }`,
			"IOError: readfile: /no/such/kukel/file: no such file or directory\n  at main (t.kkl:1)"},
		{"++ on a string", `main() { local s = "a"; s++; }`,
			"TypeError: cannot apply ++ to string\n  at main (t.kkl:1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runSource(t, tt.src)
			if err == nil || err.Error() != tt.want {
				t.Errorf("got  %v\nwant %s", err, tt.want)
			}
		})
	}
}

func TestStackOverflow(t *testing.T) {
	_, _, err := runSource(t, "r(n) { return r(n + 1); }\nmain() { r(0); }")
	re, ok := err.(*vm.RuntimeError)
	if !ok || re.Kind != "StackOverflowError" {
		t.Fatalf("got %v", err)
	}
	if len(re.Trace) != vm.MaxFrames {
		t.Errorf("trace has %d entries, want %d", len(re.Trace), vm.MaxFrames)
	}
	msg := err.Error()
	if lines := strings.Count(msg, "\n"); lines != 21 || !strings.Contains(msg, "... 9980 more calls ...") ||
		!strings.HasSuffix(msg, "at main (t.kkl:2)") {
		t.Errorf("shortened trace (%d lines):\n%s", lines, msg)
	}
}

func TestOutputBeforeError(t *testing.T) {
	out, _, err := runSource(t, `main() { println("partial"); local x = 1 / 0; }`)
	if err == nil || out != "partial\n" {
		t.Errorf("got %q, %v", out, err)
	}
}

func benchmarkSource(b *testing.B, src string) {
	f, err := parser.ParseFile("b.kkl", []byte(src))
	if err != nil {
		b.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		machine, _ := vm.New(prog, builtins.Lookup, &bytes.Buffer{}, nil)
		if _, err := machine.Run(nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFib27(b *testing.B) {
	benchmarkSource(b, `fib(n) { if (n < 2) return n; return fib(n - 1) + fib(n - 2); } main() { fib(27); }`)
}

func BenchmarkLoop1M(b *testing.B) {
	benchmarkSource(b, `main() { local sum = 0; for (local i = 0; i < 1000000; i++) sum += i; }`)
}

func BenchmarkMethodCalls1M(b *testing.B) {
	benchmarkSource(b, `
		class C { n; C(); inc(); }
		C::C() { n = 0; }
		C::inc() { n++; }
		main() { local c = new C(); for (local i = 0; i < 1000000; i++) c->inc(); }`)
}

func BenchmarkClosureCalls1M(b *testing.B) {
	benchmarkSource(b, `main() { local n = 0; local inc = fn() { n++; }; for (local i = 0; i < 1000000; i++) inc(); }`)
}

func TestReadline(t *testing.T) {
	out, _, _, err := runSourceIO(t, `main() {
	    local line, n;
		n = 0;
		print("name? ");
		while ((line = readline()) != nil) { n++; println("[" + line + "]"); }
		println(n, readline());
	}`, "ann\r\nbob\n\nlast without newline")
	if err != nil || out != "name? [ann]\n[bob]\n[]\n[last without newline]\n4 nil\n" {
		t.Errorf("got %q, %v", out, err)
	}
}

func TestFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	out, _, err := runSource(t, `main(args) {
	    local p;
		p = args[0];
		writefile(p, "one\n");
		appendfile(p, "two\n");
		println(readfile(p) == "one\ntwo\n", len(split(readfile(p), "\n")));
		writefile(p, "new");
		println(readfile(p));
	}`, path)
	if err != nil || out != "true 3\nnew\n" {
		t.Errorf("got %q, %v", out, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Errorf("file holds %q", data)
	}
}

func TestStderrAndExit(t *testing.T) {
	out, errOut, code, err := runSourceIO(t, `
		cmp(a, b) { exit(4); }
		main() {
			print("out ");
			eprint("err", 1, "");
			eprintln("line");
			try { sort([2, 1], cmp); } catch (e) { println("never"); } finally { println("never either"); }
		}`, "")
	if err != nil || code != 4 || out != "out " || errOut != "err 1 line\n" {
		t.Errorf("got out %q, err %q, code %d, %v", out, errOut, code, err)
	}
	_, _, code, err = runSourceIO(t, `main() { local f = fn() { exit(); }; f(); return 9; }`, "")
	if err != nil || code != 0 {
		t.Errorf("exit(): code %d, %v", code, err)
	}
}

func BenchmarkStringBuilding(b *testing.B) {
	benchmarkSource(b, `main() { local parts; parts = []; for (local i = 0; i < 100000; i++) push(parts, str(i)); join(parts, ","); }`)
}

func BenchmarkMapsAndVectors(b *testing.B) {
	benchmarkSource(b, `main() {
	    local m, v;
		m = {}; v = [];
		for (local i = 0; i < 100000; i++) { m[i % 1000] = i; push(v, i); }
		local sum = 0;
		for (local k, x in m) sum += x;
		for (local x in v) sum += x;
	}`)
}

func BenchmarkSwitchDense1M(b *testing.B) {
	benchmarkSource(b, `main() {
		local n = 0;
		for (local i = 0; i < 1000000; i++) {
			switch (i % 10) {
				case 0: n += 1; break;
				case 1: n += 2; break;
				case 2: n += 3; break;
				case 3: n += 4; break;
				case 4: n += 5; break;
				case 5: n += 6; break;
				case 6: n += 7; break;
				case 7: n += 8; break;
				case 8: n += 9; break;
				default: n += 10;
			}
		}
	}`)
}

func BenchmarkStringConcat(b *testing.B) {
	benchmarkSource(b, `main() { local s = ""; for (local i = 0; i < 20000; i++) s += "x"; local n = 0; for (local i = 0; i < 20000; i++) if (s[i] == "x") n++; }`)
}

func BenchmarkStringCompare1M(b *testing.B) {
	benchmarkSource(b, `main() { local a = "alpha", n = 0; for (local i = 0; i < 1000000; i++) if (a == "alpha" && a < "beta") n++; }`)
}

func BenchmarkFieldAccess1M(b *testing.B) {
	benchmarkSource(b, `
		class P { x, y; P(x, y); }
		P::P(x, y) { this->x = x; this->y = y; }
		main() { local p = new P(1, 2), s = 0; for (local i = 0; i < 1000000; i++) { s += p->x + p->y; p->x = i; } }`)
}
