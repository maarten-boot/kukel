package kukel

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/compiler"
)

// codeLines gives, per function, the lines that have code: where the
// debugger can stop.
func codeLines(t *testing.T, compile func() (*Program, error), fuse bool) [][]int {
	t.Helper()
	compiler.Fuse = fuse
	defer func() { compiler.Fuse = true }()
	p, err := compile()
	if err != nil {
		t.Fatal(err)
	}
	var all [][]int
	for _, fn := range p.prog.Funcs {
		var lines []int
		for _, l := range fn.Lines {
			lines = append(lines, l.Line)
		}
		slices.Sort(lines)
		all = append(all, slices.Compact(lines))
	}
	return all
}

func sameLines(t *testing.T, name string, compile func() (*Program, error)) {
	t.Helper()
	p, err := compile()
	if err != nil && strings.Contains(err.Error(), "is not in this kukel") {
		return // a module left out by build tags (tk)
	}
	on, off := codeLines(t, compile, true), codeLines(t, compile, false)
	for i := range on {
		if !slices.Equal(on[i], off[i]) {
			t.Errorf("%q: %s has code on lines %v with Fuse, %v without", name, p.prog.Funcs[i].FuncName, on[i], off[i])
		}
	}
}

// The instructions that do the work of several leave no line without
// code that has code without them: the debugger stops on the same lines.
func TestFuseKeepsLines(t *testing.T) {
	files, _ := filepath.Glob("tests/run/*.kkl")
	more, _ := filepath.Glob("examples/*.kkl")
	for _, file := range append(files, more...) {
		if filepath.Base(file) == "compile_error.kkl" {
			continue
		}
		sameLines(t, file, func() (*Program, error) {
			return CompileFiles([]string{file}, CompileOptions{Path: []string{"tests/run/lib"}})
		})
	}
	for _, src := range []string{
		"main() {\n local a = 1;\n local r =\n a + 1;\n println(r); }",
		"main() {\n local a = 1, b = 2, r;\n r =\n a - b;\n println(r); }",
		"class P { x; } main() {\n local p = new P(), r;\n r =\n p->x;\n println(r); }",
		"main() {\n local v = [1], i = 0, r;\n r =\n v[i];\n println(r); }",
		"main() {\n local a = 1, b = 2;\n if (\n a < b) println(1);\n if (\n a < 3) println(2); }",
		"main() {\n local a = 1, b = 2;\n if (a < b &&\n b < 3 &&\n a < b) println(1); }",
		"main() {\n local i = 0;\n i\n ++;\n i\n --;\n println(i); }",
		"main() {\n local x = 0;\n x\n += 2;\n x +=\n 3;\n println(x); }",
		"main() {\n local s = \"a\";\n switch (\n s) { case \"a\": println(1); } }",
	} {
		sameLines(t, src, func() (*Program, error) { return CompileSource("t.kkl", src, CompileOptions{}) })
	}
}
