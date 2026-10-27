package stdlib_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/format"
	"github.com/maarten-boot/kukel/internal/loader"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/stdlib"
)

// Every standard module written in Kukel is formatted, compiles and passes
// the verifier when a program uses it, also with no files to read.
func TestModules(t *testing.T) {
	names := stdlib.Names()
	if len(names) == 0 {
		t.Fatal("no standard modules written in Kukel")
	}
	for _, name := range names {
		src, ok := stdlib.Source(name)
		if !ok {
			t.Fatalf("%s: no source", name)
		}
		if got, err := format.Source(stdlib.File(name), src); err != nil || !bytes.Equal(got, src) {
			t.Errorf("%s is not formatted (kukel fmt): %v", stdlib.File(name), err)
		}
		if back, ok := stdlib.SourceOf(stdlib.File(name)); !ok || !bytes.Equal(back, src) {
			t.Errorf("SourceOf(%s) does not give its source", stdlib.File(name))
		}
		f, err := parser.ParseFile("p.kkl", []byte("use "+name+";\n\nmain() {}\n"))
		if err != nil {
			t.Fatal(err)
		}
		files, err := loader.Resolve([]*ast.File{f}, nil, false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		prog, err := compiler.Compile(files, builtins.Names())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := bytecode.Verify(prog); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, bad := range []string{"../iter", "iter.kkl", "nosuch", ""} {
		if _, ok := stdlib.Source(bad); ok {
			t.Errorf("Source(%q) found something", bad)
		}
	}
}

// The reference describes every function of the standard modules written
// in Kukel (section 12), as "name::f(".
func TestReferenceDescribesModules(t *testing.T) {
	ref, err := os.ReadFile("../../docs/reference.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range stdlib.Names() {
		src, _ := stdlib.Source(name)
		f, err := parser.ParseFile(stdlib.File(name), src)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Class != nil || strings.HasPrefix(fd.Name.Name, "_") {
				continue
			}
			if want := "`" + name + "::" + fd.Name.Name + "("; !bytes.Contains(ref, []byte(want)) {
				t.Errorf("docs/reference.md does not describe %s::%s (looked for %s)", name, fd.Name.Name, want)
			}
		}
	}
}
