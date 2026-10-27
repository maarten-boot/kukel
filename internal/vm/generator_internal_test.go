package vm

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/parser"
)

// A fatal exception (here the step limit) in a loop over a generator
// leaves nothing behind: no handlers, frames or loop generators, however
// often the VM is used again.
func TestGeneratorFatalLeavesNothing(t *testing.T) {
	f, err := parser.ParseFile("t.kkl", []byte("forever() { for (;;) yield 1; }\nmain() { for (local x in forever()) ; }\n"))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(prog, builtins.Lookup, &bytes.Buffer{}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	m.StepLimit = 1000
	for range 3 {
		m.ResetSteps()
		if _, err := m.Run(nil); err == nil || !strings.Contains(err.Error(), "StepLimitError") {
			t.Fatalf("got %v, want a StepLimitError", err)
		}
		if len(m.handlers) != 0 || len(m.frames) != 0 || len(m.loops) != 0 {
			t.Errorf("left: %d handlers, %d frames, %d loop generators", len(m.handlers), len(m.frames), len(m.loops))
		}
	}
}

// When an exception leaves a loop and closing the loop's generator throws
// too, that second exception is dropped: the debugger's hook for uncaught
// exceptions hears of the first only.
func TestGeneratorCloseNotUncaught(t *testing.T) {
	f, err := parser.ParseFile("t.kkl", []byte(`throwfin() { try { yield 1; } finally { throw new Error("from finally"); } }
main() { for (local x in throwfin()) { throw new Error("orig"); } }
`))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(prog, builtins.Lookup, &bytes.Buffer{}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	var heard []string
	m.DebugUncaught = func(vm *VM, re *RuntimeError) error {
		heard = append(heard, re.Msg)
		return nil
	}
	if _, err := m.Run(nil); err == nil || !strings.Contains(err.Error(), "orig") {
		t.Errorf("got %v, want the loop's exception", err)
	}
	if len(heard) != 1 || heard[0] != "orig" {
		t.Errorf("the hook heard %q, want only \"orig\"", heard)
	}
}
