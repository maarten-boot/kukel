package vm_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	_ "github.com/maarten-boot/kukel/internal/modules/eventsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/stringsmod"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Native member functions run through every kind of call: a method call
// (also from the inline cache), a bound method and a spread call.
func TestNativeCalls(t *testing.T) {
	out, _, err := runSource(t, `use strings; use events;
main() {
    local b = new strings::Builder();
    for (local i = 0; i < 3; i++) b->add(i);   // the cached method
    local add = b->add;
    add("-");
    local args = ["x", "y"];
    b->add(args..);
    local k = strings::Builder;
    println(b->str(), new k("z")->str(), instanceof(b, strings::Builder), classof(b) == k);
    local T = events::Timer;                  // no constructor: new is refused, not a crash
    try { new T(); } catch (e) { println(e->kind); }
}`)
	if err != nil || out != "012-xy z true true\nTypeError\n" {
		t.Errorf("got %q, %v", out, err)
	}
}

// A program that uses a native name this kukel does not have is refused
// when the VM is set up.
func TestMissingNative(t *testing.T) {
	f, err := parser.ParseFile("t.kkl", []byte(`use strings; main() { new strings::Builder(); }`))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	for i, g := range prog.Globals {
		if g.Kind == bytecode.GlobalNative {
			prog.Globals[i].Name = "nosuch::Thing"
		}
	}
	_, err = vm.New(prog, builtins.Lookup, &bytes.Buffer{}, strings.NewReader(""))
	if err == nil || err.Error() != "nosuch::Thing is not part of this kukel (the native module is missing)" {
		t.Errorf("got %v", err)
	}
}

// A fatal error (the step limit) inside a try must not leave its handler
// behind: the VM is used again by embedding programs.
func TestNoStaleHandlersAfterFatal(t *testing.T) {
	f, err := parser.ParseFile("t.kkl", []byte(`
spin() { try { while (true) {} } catch (e) { return "caught?"; } }
quits() { try { exit(3); } finally { println("finally?"); } }
boom() { try { throw "x"; } catch (e) { return "caught " + e; } }
bare() { throw "loose"; }
main() {}`))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	m, err := vm.New(prog, builtins.Lookup, &out, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	m.StepLimit = 1000
	call := func(name string) (string, error) {
		m.ResetSteps()
		fn, _ := m.Global(name)
		r, err := m.Start(fn, nil)
		return r.String(), err
	}
	if _, err := call("spin"); err == nil || !strings.Contains(err.Error(), "StepLimitError") {
		t.Fatalf("spin: %v", err)
	}
	if _, err := call("quits"); err == nil || out.String() != "" {
		t.Fatalf("quits: %v, output %q", err, out.String())
	}
	for i := 0; i < 2; i++ {
		if r, err := call("boom"); err != nil || r != "caught x" {
			t.Errorf("boom: %q, %v", r, err)
		}
		if _, err := call("bare"); err == nil || !strings.Contains(err.Error(), "loose") {
			t.Errorf("bare: %v", err)
		}
	}
}
