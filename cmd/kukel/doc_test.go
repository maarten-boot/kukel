package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/format"
)

// TestLarkGrammar parses the programs of the repository with syntax.lark,
// so that the grammar keeps up with the language: examples/ (also the
// embedding example's), tests/run (with its modules), tests/ast and the
// programs of docs/reference.md and the README. It
// needs Python with lark (pip install lark), else it skips; CI has them.
func TestLarkGrammar(t *testing.T) {
	if err := exec.Command("python3", "-c", "import lark").Run(); err != nil {
		t.Skip("needs python3 with lark (pip install lark)")
	}
	var files []string
	for _, pattern := range []string{"../../examples/*.kkl", "../../examples/embed/*.kkl", "../../tests/run/*.kkl", "../../tests/run/lib/*.kkl", "../../tests/ast/*.kkl"} {
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

// TestReferenceSessions replays the sessions docs/reference.md shows: a
// REPL transcript (a block of "> " and "... " lines, with the output
// between them) gets those lines as its input; a debugger session ("$
// kukel debug name.kkl", the program in the block before) gets the
// commands after "(kdb) ". The output must be what the transcript shows.
func TestReferenceSessions(t *testing.T) {
	text, err := os.ReadFile("../../docs/reference.md")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	blocks := strings.Split(string(text), "```")
	repls, debugs := 0, 0
	for i := 1; i < len(blocks); i += 2 {
		block := strings.TrimPrefix(blocks[i], "\n")
		var in, want strings.Builder
		var args []string
		switch {
		case strings.HasPrefix(block, "> "):
			repls++
			args = []string{"repl"}
			for _, line := range strings.SplitAfter(block, "\n") {
				if rest, ok := strings.CutPrefix(line, "> "); ok {
					in.WriteString(rest)
				} else if rest, ok := strings.CutPrefix(line, "... "); ok {
					in.WriteString(rest)
				} else {
					want.WriteString(line)
				}
			}
		case strings.HasPrefix(block, "$ kukel debug "):
			debugs++
			first, rest, _ := strings.Cut(block, "\n")
			file := strings.Fields(first)[3]
			if i < 2 {
				t.Fatalf("no program before %q", first)
			}
			os.WriteFile(file, []byte(strings.TrimPrefix(blocks[i-2], "\n")), 0o644)
			args = strings.Fields(first)[2:]
			for _, line := range strings.SplitAfter(rest, "\n") {
				if cmd, ok := strings.CutPrefix(line, "(kdb) "); ok {
					in.WriteString(cmd)
					want.WriteString("(kdb) ")
				} else {
					want.WriteString(line)
				}
			}
		default:
			continue
		}
		var out bytes.Buffer
		run(args, strings.NewReader(in.String()), &out, &out)
		if out.String() != want.String() {
			t.Errorf("kukel %s with\n%s\ngives\n%s\nbut the reference shows\n%s", args[0], in.String(), out.String(), want.String())
		}
	}
	if repls < 2 || debugs < 1 {
		t.Errorf("found %d REPL and %d debugger sessions", repls, debugs)
	}
}

// TestDocsFormatted checks that the programs of the documents are written
// as kukel fmt writes them, as the examples are.
func TestDocsFormatted(t *testing.T) {
	for i, code := range docPrograms(t) {
		out, err := format.Source(fmt.Sprintf("doc%d.kkl", i+1), []byte(code))
		if err != nil {
			t.Errorf("program %d: %v", i+1, err)
		} else if string(out) != code {
			t.Errorf("program %d is not formatted; kukel fmt gives\n%s\ninstead of\n%s", i+1, out, code)
		}
	}
}

// docPrograms gives the programs of docs/reference.md, docs/tutorial.md and
// the README:
// the code blocks with a main function, and the modules ("// name.kkl"
// first).
func docPrograms(t *testing.T) []string {
	var progs []string
	for _, name := range []string{"../../docs/reference.md", "../../docs/tutorial.md", "../../README.md"} {
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
