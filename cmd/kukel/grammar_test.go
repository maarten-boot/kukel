package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLarkGrammar parses the programs of the repository with syntax.lark,
// so that the grammar keeps up with the language: examples/, tests/run (with
// its modules) and the programs of docs/reference.md and the README. It
// needs Python with lark (pip install lark), else it skips; CI has them.
func TestLarkGrammar(t *testing.T) {
	if err := exec.Command("python3", "-c", "import lark").Run(); err != nil {
		t.Skip("needs python3 with lark (pip install lark)")
	}
	var files []string
	for _, pattern := range []string{"../../examples/*.kkl", "../../tests/run/*.kkl", "../../tests/run/lib/*.kkl"} {
		more, _ := filepath.Glob(pattern)
		files = append(files, more...)
	}
	dir := t.TempDir()
	for i, code := range docPrograms(t) {
		name := filepath.Join(dir, fmt.Sprintf("doc%d.kkl", i+1))
		if err := os.WriteFile(name, []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, name)
	}
	script := filepath.Join(dir, "check.py")
	os.WriteFile(script, []byte(larkCheck), 0o644)
	out, err := exec.Command("python3", append([]string{script, "../../syntax.lark"}, files...)...).CombinedOutput()
	if err != nil {
		t.Errorf("syntax.lark does not parse (docN.kkl is the Nth program of the documents, in %s):\n%s", dir, out)
	}
	if !strings.HasPrefix(string(out), "parsed ") {
		t.Errorf("%s", out)
	}
}

// larkCheck parses the files after the grammar and prints the ones that
// fail.
const larkCheck = `import sys
from lark import Lark
parser = Lark(open(sys.argv[1]).read(), parser="lalr", lexer="contextual")
bad = []
for name in sys.argv[2:]:
    try:
        parser.parse(open(name, encoding="utf-8").read())
    except Exception as e:
        bad.append("%s: %s" % (name, str(e).strip().splitlines()[0] if str(e).strip() else type(e).__name__))
print("parsed %d files" % (len(sys.argv) - 2))
print("\n".join(bad))
sys.exit(1 if bad else 0)
`

// docPrograms gives the programs of docs/reference.md and the README:
// the code blocks with a main function, and the modules ("// name.kkl"
// first).
func docPrograms(t *testing.T) []string {
	var progs []string
	for _, name := range []string{"../../docs/reference.md", "../../README.md"} {
		text, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		blocks := strings.Split(string(text), "```")
		for i := 1; i < len(blocks); i += 2 {
			code := strings.TrimPrefix(blocks[i], "\n")
			first := strings.SplitN(code, "\n", 2)[0]
			if strings.Contains(code, "main(") || strings.HasPrefix(first, "// ") && strings.HasSuffix(first, ".kkl") {
				progs = append(progs, code)
			}
		}
	}
	if len(progs) < 10 {
		t.Fatalf("found only %d programs in the documents", len(progs))
	}
	return progs
}
