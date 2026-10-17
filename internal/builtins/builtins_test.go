package builtins

import (
	"os"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/value"
)

// TestReferenceDocumentsAllBuiltins keeps docs/reference.md complete.
func TestReferenceDocumentsAllBuiltins(t *testing.T) {
	doc, err := os.ReadFile("../../docs/reference.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Names() {
		if !strings.Contains(string(doc), "`"+name+"(") && !strings.Contains(string(doc), ", "+name+"(") {
			t.Errorf("built-in %s is not described in docs/reference.md", name)
		}
	}
}

// min and max name themselves, and the kinds in order, for values they
// cannot compare.
func TestMinMaxMessages(t *testing.T) {
	for name, want := range map[string]string{"min": "min: cannot compare int and string", "max": "max: cannot compare int and string"} {
		b, _ := Lookup(name)
		_, err := b.Fn(nil, []value.Value{value.MakeInt(1), value.MakeString("a")})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
	}
}
