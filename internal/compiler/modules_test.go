package compiler

import (
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/parser"
)

// compileModules compiles files given as module/source pairs, in order; the
// file of module m is m.kkl, the program's files are t.kkl.
func compileModules(t *testing.T, pairs ...string) error {
	t.Helper()
	var files []*ast.File
	for i := 0; i < len(pairs); i += 2 {
		name := pairs[i] + ".kkl"
		if pairs[i] == "" {
			name = "t.kkl"
		}
		f, err := parser.ParseFile(name, []byte(pairs[i+1]))
		if err != nil {
			t.Fatalf("parse %q: %v", pairs[i+1], err)
		}
		f.Module = pairs[i]
		files = append(files, f)
	}
	_, err := Compile(files, testBuiltins)
	return err
}

const utilSrc = `const N = 3; global count;
class Base { x; Base(x); m(); static make(); }
Base::Base(x) { this->x = x; }
Base::m() { return x; }
Base::make() { return new Base(N); }
f(a) { return a + _g(); }
_g() { return N; }
class _Hidden {}`

func TestModules(t *testing.T) {
	ok := []string{
		"use util; main() { println(util::f(1), util::N, util::count); }",
		"use util; const M = util::N * 2; main() { println(M); }",
		"use util; class D : util::Base { D(); } D::D() { util::Base::Base(1); } main() { new D()->m(); }",
		"use util; main() { local b = util::Base::make(), k = util::Base; new k(1); new util::Base(2); util::count = 5; util::count++; }",
		"use util; main() { local util = 1; println(util, util::N); }", // a local does not hide the module
		"use util; f() {} main() { f(); }",                             // the program may reuse a module's names
	}
	for _, src := range ok {
		if err := compileModules(t, "util", utilSrc, "", src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}

	bad := []struct{ src, msg string }{
		{"use util; main() { util::_g(); }", "t.kkl:1:26: util::_g is private to module util (names starting with '_' are)"},
		{"use util; class X : util::_Hidden {} main() {}", "t.kkl:1:27: util::_Hidden is private to module util"},
		{"use util; main() { util::h(); }", "t.kkl:1:26: module util has no h"},
		{"use util; class D : util::Base { D(); } D::D() { util::Base(1); } main() {}", "t.kkl:1:60: util::Base(...) calls the class; call its constructor as util::Base::Base(...)"},
		{"main() { util::f(1); }", "t.kkl:1:10: util is not a module used by this file (add 'use util;' at the top)"},
		{"use util; main() { util::f(); }", "t.kkl:1:27: util::f expects 1 argument, got 0"},
		{"use util; main() { new util::Base(); }", "t.kkl:1:20: util::Base::Base expects 1 argument, got 0"},
		{"use util; main() { new util::f(); }", "t.kkl:1:30: util::f is not a class"},
		{"use util; main() { util::N = 1; }", "t.kkl:1:26: cannot assign to constant util::N"},
		{"use util; main() { util::f = 1; }", "t.kkl:1:26: cannot assign to function util::f"},
		{"use util; main() { util::Base::Base(1); }", "t.kkl:1:32: util::Base::Base is not static"},
		{"use util; main() { util::Base::y; }", "t.kkl:1:32: class util::Base has no member y"},
		{"use util; const M = util::f; main() {}", "t.kkl:1:27: util::f is not a constant"},
		{"use util; global util; main() {}", "t.kkl:1:18: util has the name of a module used by a file of this program"},
		{"use util; use util; main() {}", "t.kkl:1:15: module util is already used in this file"},
		{"use other; main() {}", "t.kkl:1:5: module other is not loaded"},
		{"use println; main() {}", "t.kkl:1:5: module println is not loaded"},
		{"use Error; main() {}", "t.kkl:1:5: a module cannot be called Error"},
		{"use Generator; main() {}", "t.kkl:1:5: a module cannot be called Generator (the name of the prelude's class Generator)"},
		{"use util; util::Base::m() {} main() {}", "t.kkl:1:21: expected '(' after function name"},
	}
	for _, tc := range bad {
		err := compileAny(t, tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s:\n got %v\nwant %s", tc.src, err, tc.msg)
		}
	}

	// A module sees only its own names, the built-ins and the modules it uses.
	if err := compileModules(t, "util", "f() { return main; }", "", "use util; main() {}"); err == nil || !strings.Contains(err.Error(), "util.kkl:1:14: undefined name main") {
		t.Errorf("a module sees the program's names: %v", err)
	}
	if err := compileModules(t, "a", "fa() {}", "b", "fb() { a::fa(); }", "", "use a; use b; main() {}"); err == nil || !strings.Contains(err.Error(), "b.kkl:1:8: a is not a module used by this file") {
		t.Errorf("a module sees modules it does not use: %v", err)
	}
	if err := compileModules(t, "a", "use b; fa() {}", "b", "fb() {}", "", "main() {}"); err == nil || !strings.Contains(err.Error(), "a.kkl:1:5: module b is not loaded") {
		t.Errorf("a module used before it is loaded: %v", err)
	}
	if err := compileModules(t, "a", "use a; fa() {}", "", "main() {}"); err == nil || !strings.Contains(err.Error(), "module a cannot use itself") {
		t.Errorf("a module using itself: %v", err)
	}
	if err := compileModules(t, "a", "main() {}", "", "use a; f() {}"); err == nil || !strings.Contains(err.Error(), "the program has no main function") {
		t.Errorf("main of a module counted as the program's: %v", err)
	}
	if err := compileModules(t, "a", "Error() {}", "", "main() {}"); err == nil || !strings.Contains(err.Error(), "a.kkl:1:1: Error is already defined") {
		t.Errorf("a module redefining Error: %v", err)
	}
}

// compileAny compiles src after module util, also reporting a parse error.
func compileAny(t *testing.T, src string) error {
	if _, err := parser.ParseFile("t.kkl", []byte(src)); err != nil {
		return err
	}
	return compileModules(t, "util", utilSrc, "", src)
}
