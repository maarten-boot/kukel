package builtins

import (
	"os"
	"strings"
	"testing"
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
