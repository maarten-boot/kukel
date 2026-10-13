package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/native"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const testdir = "../../tests"

// TestGolden compares the output of `kukel tokens` for tests/tokens/*.kkl
// with the .tokens files, and of `kukel ast` for tests/ast/*.kkl with the .ast
// files. Run `make golden` (go test ./cmd/kukel -update) to rewrite the
// golden files after an intended change.
func TestGolden(t *testing.T) {
	for _, tc := range []struct{ cmd, dir, ext string }{
		{"tokens", "tokens", ".tokens"},
		{"ast", "ast", ".ast"},
	} {
		files, err := filepath.Glob(filepath.Join(testdir, tc.dir, "*.kkl"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no test files in %s: %v", tc.dir, err)
		}
		for _, src := range files {
			var stdout, stderr bytes.Buffer
			if code := run([]string{tc.cmd, src}, nil, &stdout, &stderr); code != 0 {
				t.Errorf("kukel %s %s: exit code %d: %s", tc.cmd, src, code, stderr.String())
				continue
			}
			// Golden files use paths relative to their directory.
			got := strings.ReplaceAll(stdout.String(), filepath.Dir(src)+string(filepath.Separator), "")
			golden := strings.TrimSuffix(src, ".kkl") + tc.ext
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("kukel %s %s: output differs from %s (run with -update after checking the change)\n%s",
					tc.cmd, src, golden, firstDiff(got, string(want)))
			}
		}
	}
}

// TestRunGolden runs every tests/run/*.kkl with `kukel run` and compares
// stdout with the .out file and stderr with the .err file (empty if there
// is none). A .in file is the program's standard input. Comment lines at
// the top set the arguments and the expected exit code: "// args: a b" and
// "// exit: 3" (default 0, or 1 with a .err file). Modules are found in
// tests/run/lib (through KUKEL_PATH). `make golden` rewrites .out and .err
// files.
func TestRunGolden(t *testing.T) {
	t.Setenv("KUKEL_PATH", filepath.Join(testdir, "run", "lib"))
	files, err := filepath.Glob(filepath.Join(testdir, "run", "*.kkl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files in run: %v", err)
	}
	for _, src := range files {
		t.Run(filepath.Base(src), func(t *testing.T) {
			text, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			base := strings.TrimSuffix(src, ".kkl")
			_, statErr := os.Stat(base + ".err")
			wantCode := 0
			if statErr == nil {
				wantCode = 1
			}
			var args []string
			for _, line := range strings.Split(string(text), "\n") {
				if !strings.HasPrefix(line, "//") {
					break
				}
				line = strings.TrimSpace(strings.TrimPrefix(line, "//"))
				if v, ok := strings.CutPrefix(line, "args:"); ok {
					args = strings.Fields(v)
				}
				if v, ok := strings.CutPrefix(line, "exit:"); ok {
					wantCode, _ = strconv.Atoi(strings.TrimSpace(v))
				}
			}
			stdin, _ := os.ReadFile(base + ".in")
			var stdout, stderr bytes.Buffer
			code := run(append([]string{"run", src, "--"}, args...), bytes.NewReader(stdin), &stdout, &stderr)
			// Paths in traces (also in Error objects a program prints) are
			// relative to tests/run.
			dirPrefix := filepath.Dir(src) + string(filepath.Separator)
			gotOut := strings.ReplaceAll(stdout.String(), dirPrefix, "")
			gotErr := strings.ReplaceAll(stderr.String(), dirPrefix, "")

			// The same through a bytecode file: build, then exec.
			kkc := filepath.Join(t.TempDir(), "prog.kkc")
			var bOut, bErr bytes.Buffer
			bCode := run([]string{"build", "-o", kkc, src}, nil, &bOut, &bErr)
			if bCode == 0 {
				bCode = run(append([]string{"exec", kkc}, args...), bytes.NewReader(stdin), &bOut, &bErr)
			}
			if b, e := strings.ReplaceAll(bOut.String(), dirPrefix, ""), strings.ReplaceAll(bErr.String(), dirPrefix, ""); b != gotOut || e != gotErr || bCode != code {
				t.Errorf("build+exec differs from run:\n stdout %q\n stderr %q\n exit %d", b, e, bCode)
			}
			if *update {
				if err := os.WriteFile(base+".out", []byte(gotOut), 0o644); err != nil {
					t.Fatal(err)
				}
				if gotErr != "" {
					if err := os.WriteFile(base+".err", []byte(gotErr), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			wantOut, err := os.ReadFile(base + ".out")
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			wantErr, _ := os.ReadFile(base + ".err")
			if gotOut != string(wantOut) {
				t.Errorf("stdout differs from %s.out\n%s", base, firstDiff(gotOut, string(wantOut)))
			}
			if gotErr != string(wantErr) {
				t.Errorf("stderr differs from %s.err\n got:  %q\n want: %q", base, gotErr, wantErr)
			}
			if code != wantCode {
				t.Errorf("exit code %d, want %d", code, wantCode)
			}
		})
	}
}

func TestRunSeveralFiles(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.kkl"), filepath.Join(dir, "b.kkl")
	os.WriteFile(a, []byte("main(args) { greet(args); }\n"), 0o644)
	os.WriteFile(b, []byte("global greeting = \"hello\";\ngreet(who) { println(greeting, who); }\n"), 0o644)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"run", a, b, "--", "x"}, nil, &stdout, &stderr); code != 0 || stdout.String() != "hello [\"x\"]\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return "line " + strconv.Itoa(i+1) + ":\n  got:  " + gl + "\n  want: " + wl
		}
	}
	return ""
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.kkl"), filepath.Join(dir, "bad.kkl")
	os.WriteFile(good, []byte("main() { println(\"not run\"); }\n"), 0o644)
	os.WriteFile(bad, []byte("f(a) {}\nmain() {\n  f();\n}\n"), 0o644)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"check", good}, nil, &stdout, &stderr); code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("check good: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if code := run([]string{"check", bad}, nil, &stdout, &stderr); code != 1 || stderr.String() != bad+":3:4: f expects 1 argument(s), got 0\n" {
		t.Errorf("check bad: exit %d, stderr %q", code, stderr.String())
	}
}

func TestBuildExecDisasm(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.kkl")
	os.WriteFile(src, []byte("main(args) { println(\"hello\", args); return len(args); }\n"), 0o644)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"build", src}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("build: exit %d: %s", code, stderr.String())
	}
	kkc := filepath.Join(dir, "hello.kkc") // the default output name
	if code := run([]string{"exec", kkc, "--", "a", "b"}, nil, &stdout, &stderr); code != 2 || stdout.String() != "hello [\"a\", \"b\"]\n" {
		t.Errorf("exec: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"disasm", kkc}, nil, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "function main") {
		t.Errorf("disasm: exit %d, stderr %q", code, stderr.String())
	}
	// A damaged file is refused with a message, not run.
	data, _ := os.ReadFile(kkc)
	data[len(data)/2] ^= 1
	os.WriteFile(kkc, data, 0o644)
	stderr.Reset()
	if code := run([]string{"exec", kkc}, nil, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "checksum mismatch") {
		t.Errorf("damaged file: exit %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"exec", src}, nil, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "not a kukel bytecode file") {
		t.Errorf("source as bytecode: exit %d, stderr %q", code, stderr.String())
	}
}

// TestReferenceExamples runs every example in docs/reference.md and the
// README that has a main function: the documentation must not show
// programs that fail.
func TestReferenceExamples(t *testing.T) {
	var doc []byte
	for _, name := range []string{"../../docs/reference.md", "../../README.md"} {
		text, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		doc = append(doc, text...)
	}
	// A block without main that starts with "// name.kkl" is a module: it is
	// written to the directory the examples run in.
	dir := t.TempDir()
	blocks := strings.Split(string(doc), "```")
	n := 0
	for i := 1; i < len(blocks); i += 2 {
		code := strings.TrimPrefix(blocks[i], "\n")
		if !strings.Contains(code, "main(") {
			if name, ok := strings.CutPrefix(strings.SplitN(code, "\n", 2)[0], "// "); ok && strings.HasSuffix(name, ".kkl") {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(code), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			continue
		}
		n++
		src := filepath.Join(dir, "example.kkl")
		os.WriteFile(src, []byte(code), 0o644)
		var stdout, stderr bytes.Buffer
		if code := run([]string{"run", src}, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Errorf("example %d fails (exit %d): %s\n%s", n, code, stderr.String(), blocks[i])
		}
	}
	if n < 4 {
		t.Errorf("found only %d examples", n)
	}
}

func TestTokensError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bad.kkl")
	if err := os.WriteFile(src, []byte("main() {\n  x = \"a\\qb\";\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"tokens", src}, nil, &stdout, &stderr); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	want := src + ":2:9: unknown escape sequence \\'q'\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestASTError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bad.kkl")
	if err := os.WriteFile(src, []byte("main() {\n  x = 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ast", src}, nil, &stdout, &stderr); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	want := src + ":3:1: expected ';' after expression, found '}'\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, nil, &stdout, &stderr); code != 0 ||
		!strings.HasPrefix(stdout.String(), "kukel ") || !strings.Contains(stdout.String(), "bytecode format") ||
		!strings.Contains(stdout.String(), "modules written in Go: json, strings, time\n") {
		t.Errorf("version: exit %d, stdout %q", code, stdout.String())
	}
}

func TestUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage: kukel") {
		t.Errorf("no args: exit %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"frobnicate"}, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), `unknown command "frobnicate"`) {
		t.Errorf("unknown command: exit %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"ast"}, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage: kukel ast") {
		t.Errorf("ast without files: exit %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"exec"}, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage: kukel exec") {
		t.Errorf("exec without a file: exit %d, stderr %q", code, stderr.String())
	}
}

// TestReferenceDocumentsNativeModules keeps section 11 of docs/reference.md
// complete: every function, class, constructor and member function of the
// modules written in Go is described there.
func TestReferenceDocumentsNativeModules(t *testing.T) {
	text, err := os.ReadFile("../../docs/reference.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(text)
	want := func(what string, forms ...string) {
		for _, f := range forms {
			if strings.Contains(doc, f) {
				return
			}
		}
		t.Errorf("%s is not described in docs/reference.md (looked for %q)", what, forms)
	}
	for _, name := range native.Names() {
		m, _ := native.Lookup(name)
		want("module "+name, "### "+name)
		for f := range m.Funcs {
			want(name+"::"+f, "`"+name+"::"+f+"(")
		}
		for k := range m.Consts {
			want(name+"::"+k, "`"+name+"::"+k+"`")
		}
		for _, c := range m.Classes {
			q := c.QualifiedName()
			if c.New != nil {
				want("new "+q, "`new "+q+"(")
			}
			for s := range c.Statics {
				want(q+"::"+s, "`"+q+"::"+s+"(")
			}
			for meth := range c.Methods {
				want(q+"::"+meth, "->"+meth+"(")
			}
		}
	}
}
