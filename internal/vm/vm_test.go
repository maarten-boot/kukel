package vm_test

import (
	"bytes"
	"strings"
	"testing"

	"kukel/internal/ast"
	"kukel/internal/builtins"
	"kukel/internal/compiler"
	"kukel/internal/parser"
	"kukel/internal/vm"
)

// runSource compiles and runs a one-file program.
func runSource(t *testing.T, src string, args ...string) (string, int, error) {
	t.Helper()
	f, err := parser.ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var out bytes.Buffer
	machine, err := vm.New(prog, builtins.Lookup, &out, strings.NewReader(""))
	if err != nil {
		t.Fatalf("vm.New: %v", err)
	}
	code, err := machine.Run(args)
	return out.String(), code, err
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
		{"temporaries", `
			f(a; t, u) { t = a * 2; println(u); return t; }
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
		{"conditional and comma", `main() {
			local x;
			println(1 ? "a" : "b", 0 ? "a" : "b", 0 ? 1 : 0 ? 2 : 3);
			x = (1, 2, 3);
			println(x);
		}`, "a b 3\n3\n"},
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
		{"elseif chain", `
			sign(x) { if (x < 0) return "neg"; elseif (x == 0) return "zero"; elseif (x < 10) return "small"; else return "big"; }
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
		{"wrong argument count", "f() {}\nmain() { f(1); }",
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
