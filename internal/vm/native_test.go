package vm_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	_ "github.com/maarten-boot/kukel/internal/modules/stringsmod"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Native member functions run through every kind of call: a method call
// (also from the inline cache), a bound method and a spread call.
func TestNativeCalls(t *testing.T) {
	out, _, err := runSource(t, `use strings;
main() {
    local b = new strings::Builder();
    for (local i = 0; i < 3; i++) b->add(i);   // the cached method
    local add = b->add;
    add("-");
    local args = ["x", "y"];
    b->add(args..);
    local k = strings::Builder;
    println(b->str(), new k("z")->str(), instanceof(b, strings::Builder), classof(b) == k);
}`)
	if err != nil || out != "012-xy z true true\n" {
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
