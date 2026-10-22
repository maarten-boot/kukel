package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/modules/dbmod"
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
			if why := unavailable(string(text)); why != "" {
				t.Skip(why)
			}
			// A directory to write files in, and where the programs are
			// (for worker::startfile): programs read them with getenv.
			t.Setenv("KUKEL_TEST_TMP", t.TempDir())
			dir, _ := filepath.Abs(filepath.Dir(src))
			t.Setenv("KUKEL_TEST_DIR", dir)
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

			// The same without the instructions that do the work of
			// several (Q69): the plain ones must give the same results.
			var pOut, pErr bytes.Buffer
			compiler.Fuse = false
			pCode := run(append([]string{"run", src, "--"}, args...), bytes.NewReader(stdin), &pOut, &pErr)
			compiler.Fuse = true
			if p, e := strings.ReplaceAll(pOut.String(), dirPrefix, ""), strings.ReplaceAll(pErr.String(), dirPrefix, ""); p != gotOut || e != gotErr || pCode != code {
				t.Errorf("without fused instructions:\n stdout %s\n stderr %q\n exit %d", firstDiff(p, gotOut), e, pCode)
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
	if code := run([]string{"check", bad}, nil, &stdout, &stderr); code != 1 || stderr.String() != bad+":3:4: f expects 1 argument, got 0\n" {
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
	// What the examples without an output block print is kept in
	// reference.golden (by a hash of their code; make golden writes it), so
	// that a change in what one prints is noticed: its comments may say the
	// old output.
	goldenFile := filepath.Join(testdir, "reference.golden")
	golden, printed := map[string]string{}, map[string]string{}
	if data, err := os.ReadFile(goldenFile); err == nil {
		if err := json.Unmarshal(data, &golden); err != nil {
			t.Fatal(err)
		}
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
		if unavailable(code) != "" {
			continue
		}
		n++
		src := filepath.Join(dir, "example.kkl")
		os.WriteFile(src, []byte(code), 0o644)
		// A block "$ kukel run x.kkl [-- args]" right after it shows the
		// output.
		args, want, shown := []string{"run", src}, "", false
		if i+2 < len(blocks) {
			var cmd string
			if cmd, shown = strings.CutPrefix(strings.TrimPrefix(blocks[i+2], "\n"), "$ kukel run "); shown {
				cmd, want, _ = strings.Cut(cmd, "\n")
				if _, progArgs, ok := strings.Cut(cmd, " -- "); ok {
					args = append(args, append([]string{"--"}, strings.Fields(progArgs)...)...)
				}
			}
		}
		var stdout, stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Errorf("example %d fails (exit %d): %s\n%s", n, code, stderr.String(), blocks[i])
		}
		if shown && stdout.String() != want {
			t.Errorf("example %d prints\n%s\nbut the documentation shows\n%s", n, stdout.String(), want)
		}
		if !shown && !varies(code) {
			out := strings.ReplaceAll(stdout.String(), dir, "$DIR")
			key := exampleKey(code)
			printed[key] = out
			if old, ok := golden[key]; ok && old != out && !*update {
				t.Errorf("example %d now prints\n%s\nbut printed\n%s\n(check what its comments say, then make golden)\n%s", n, out, old, blocks[i])
			}
		}
	}
	if n < 4 {
		t.Errorf("found only %d examples", n)
	}
	if *update {
		data, _ := json.MarshalIndent(printed, "", "  ")
		if err := os.WriteFile(goldenFile, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// varies reports whether an example's output depends on the clock, random
// numbers, new UUIDs or the files in the current directory: its output is
// not kept.
func varies(code string) bool {
	for _, s := range []string{"time::now", "time(", "clock(", "random", "randint", "uuid::v", "time::sleep", "events::", "use fs;"} {
		if strings.Contains(code, s) {
			return true
		}
	}
	return false
}

// exampleKey names an example by its code.
func exampleKey(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:6])
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
	want := src + ":2:9: unknown escape sequence \\q\n"
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

// fullModules are the modules of a kukel built without tags.
const fullModules = "db (SQLite, PostgreSQL, MySQL), events, fs, json, p9, re, strings, time, uuid, worker, zmq"

// withTk adds tk to the list of modules when this kukel has it (the build
// tag tk).
func withTk(modules string) string {
	if _, ok := native.Lookup("tk"); ok {
		return strings.Replace(modules, "time, ", "time, tk, ", 1)
	}
	return modules
}

// fullBuild reports whether this kukel has every module and database
// (no build tag left one out).
func fullBuild() bool {
	_, zmq := native.Lookup("zmq")
	_, p9 := native.Lookup("p9")
	return zmq && p9 && len(dbmod.Kinds()) == 3
}

// unavailable tells why program src cannot run in this kukel, built
// without a module or database it uses ("" if it can).
func unavailable(src string) string {
	if _, ok := native.Lookup("zmq"); !ok && strings.Contains(src, "use zmq;") {
		return "built without zmq (tag nozmq)"
	}
	if _, ok := native.Lookup("p9"); !ok && strings.Contains(src, "use p9;") {
		return "built without p9 (tag no9p)"
	}
	if strings.Contains(src, "use tk;") {
		if _, ok := native.Lookup("tk"); !ok {
			return "built without tk (tag tk)"
		}
		// They need a display, the main thread and a process of their own
		// (Tk starts once per process): internal/modules/tkmod runs them.
		return "a tk program (tested in internal/modules/tkmod)"
	}
	if !dbmod.Has("SQLite") && strings.Contains(src, "use db;") {
		return "built without SQLite (tag nosqlite)"
	}
	return ""
}

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, nil, &stdout, &stderr); code != 0 ||
		!strings.HasPrefix(stdout.String(), "kukel ") || !strings.Contains(stdout.String(), "bytecode format") ||
		!strings.Contains(stdout.String(), "modules written in Go: "+withTk(fullModules)+"\n") && fullBuild() {
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

// An argument that starts with "-" where a file name is expected is an
// option the command does not have: a usage error. run refuses .kkc files.
func TestUnknownOptions(t *testing.T) {
	for _, args := range [][]string{{"fmt", "-d", "a.kkl"}, {"run", "--help"}, {"check", "-x", "a.kkl"}, {"build", "-q", "a.kkl"},
		{"repl", "-v"}, {"debug", "-g", "a.kkl"}, {"disasm", "-s", "a.kkl"}, {"exec", "-x"}, {"tokens", "-x"}, {"ast", "-x"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "kukel "+args[0]+": unknown option "+args[1]) {
			t.Errorf("%q: exit %d, %q", args, code, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"run", "p.kkc"}, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "run it with kukel exec p.kkc") {
		t.Errorf("run p.kkc: exit %d, %q", code, stderr.String())
	}
	// Only fmt reads standard input ("-").
	for _, args := range [][]string{{"run", "-"}, {"check", "-"}, {"ast", "-pos", "-"}, {"repl", "-"}} {
		stderr.Reset()
		if code := run(args, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "kukel "+args[0]+": - (standard input) is only for kukel fmt") {
			t.Errorf("%q: exit %d, %q", args, code, stderr.String())
		}
	}
}

// Messages that are not at a source position start with "kukel: ".
func TestMessagePrefix(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("util.kkl", []byte("f() {}\n"), 0o644)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"build", "-module", "util.kkl"}, nil, &stdout, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	for _, args := range [][]string{{"nosuch.kkl"}, {"run", "nosuch.kkl"}, {"check", "nosuch.kkl"}, {"fmt", "nosuch.kkl"},
		{"disasm", "util.kkc"}, {"exec", "util.kkc"}, {"disasm", "nosuch.kkc"}, {"repl", "nosuch.kkl"}, {"debug", "nosuch.kkl"}, {"tokens", "nosuch.kkl"}} {
		stderr.Reset()
		if code := run(args, nil, &stdout, &stderr); code != 1 || !strings.HasPrefix(stderr.String(), "kukel: ") {
			t.Errorf("%q: exit %d, %q", args, code, stderr.String())
		}
	}
}

// A NUL byte refused by fmt and build -module is a message with the
// prefix too; so are help and version with arguments (usage errors).
func TestMessagePrefixNUL(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("n.kkl", []byte("f() {}\x00\n"), 0o644)
	for _, args := range [][]string{{"fmt", "n.kkl"}, {"build", "-module", "n.kkl"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, nil, &stdout, &stderr); code != 1 || !strings.HasPrefix(stderr.String(), "kukel: n.kkl: ") {
			t.Errorf("%q: exit %d, %q", args, code, stderr.String())
		}
	}
	for _, args := range [][]string{{"help", "x"}, {"version", "x"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, nil, &stdout, &stderr); code != 2 || !strings.HasPrefix(stderr.String(), "kukel "+args[0]+": takes no arguments") {
			t.Errorf("%q: exit %d, %q", args, code, stderr.String())
		}
	}
}

// A compiled program among the source files is refused with one message
// (only exec, and disasm on its own, take one).
func TestCompiledAmongSources(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("a.kkl", []byte("main() {}\n"), 0o644)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"build", "-o", "r.kkc", "a.kkl"}, nil, &stdout, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	for _, tc := range []struct {
		args []string
		msg  string
	}{
		{[]string{"run", "r.kkc"}, "kukel run: r.kkc is a compiled program: run it with kukel exec r.kkc (or kukel r.kkc)"},
		{[]string{"debug", "a.kkl", "r.kkc"}, "kukel debug: r.kkc is a compiled program: run it with kukel exec r.kkc"},
		{[]string{"profile", "r.kkc"}, "kukel profile: r.kkc is a compiled program: run it with kukel exec r.kkc"},
		{[]string{"check", "r.kkc"}, "kukel check: r.kkc is a compiled program, not a source file (.kkl)"},
		{[]string{"build", "r.kkc"}, "kukel build: r.kkc is a compiled program, not a source file (.kkl)"},
		{[]string{"repl", "r.kkc"}, "kukel repl: r.kkc is a compiled program, not a source file (.kkl)"},
		{[]string{"tokens", "r.kkc"}, "kukel tokens: r.kkc is a compiled program, not a source file (.kkl)"},
		{[]string{"fmt", "r.kkc"}, "kukel fmt: r.kkc is a compiled program, not a source file (.kkl)"},
		{[]string{"disasm", "a.kkl", "r.kkc"}, "kukel disasm: r.kkc is a compiled program: give it alone (kukel disasm r.kkc)"},
		{[]string{"disasm", "r.kkc", "a.kkl"}, "kukel disasm: r.kkc is a compiled program: give it alone (kukel disasm r.kkc)"},
	} {
		stderr.Reset()
		if code := run(tc.args, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.HasPrefix(stderr.String(), tc.msg) {
			t.Errorf("%q: exit %d, %q", tc.args, code, stderr.String())
		}
	}
	if _, err := compileFiles([]string{"a.kkl", "r.kkc"}); err == nil || err.Error() != "kukel: r.kkc is a compiled program, not a source file (.kkl)" {
		t.Errorf("compileFiles (kukel dap): %v", err)
	}
}

// failWriter fails every write, as standard output on a full disk does.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) {
	return 0, errors.New("write /dev/stdout: no space left on device")
}

// Output that cannot be written is reported, with exit code 1.
func TestWriteErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("a.kkl", []byte("main() { println(\"x\"); }\n"), 0o644)
	os.WriteFile("m.kkl", []byte("main(){}\n"), 0o644)
	for _, args := range [][]string{{"run", "a.kkl"}, {"a.kkl"}, {"fmt", "a.kkl"}, {"fmt", "-l", "m.kkl"}, {"fmt", "-"},
		{"tokens", "a.kkl"}, {"ast", "a.kkl"}, {"disasm", "a.kkl"}, {"help"}, {"version"}} {
		var stderr bytes.Buffer
		if code := run(args, strings.NewReader("main() {}\n"), failWriter{}, &stderr); code != 1 || stderr.String() != "kukel: write /dev/stdout: no space left on device\n" {
			t.Errorf("%q: exit %d, %q", args, code, stderr.String())
		}
	}
}

// build -o refuses to write a source file.
func TestBuildOutputNotSource(t *testing.T) {
	t.Chdir(t.TempDir())
	src := "main() {}\n"
	os.WriteFile("a.kkl", []byte(src), 0o644)
	for _, out := range []string{"a.kkl", "./a.kkl", "b.kkl"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"build", "-o", out, "a.kkl"}, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "names a source file") {
			t.Errorf("-o %s: exit %d, %q", out, code, stderr.String())
		}
	}
	if data, _ := os.ReadFile("a.kkl"); string(data) != src {
		t.Errorf("a.kkl was changed: %q", data)
	}
	if _, err := os.Stat("b.kkl"); err == nil {
		t.Errorf("b.kkl was written")
	}
}

func TestBuildMissingOutputName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"build", "a.kkl", "-o"}, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "-o needs a file name") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
}

func TestFmt(t *testing.T) {
	dir := t.TempDir()
	messy := filepath.Join(dir, "messy.kkl")
	tidy := filepath.Join(dir, "tidy.kkl")
	bad := filepath.Join(dir, "bad.kkl")
	os.WriteFile(messy, []byte("main(){\nprintln( 1+2 );\n}\n"), 0o644)
	os.WriteFile(tidy, []byte("main() {\n    println(1 + 2);\n}\n"), 0o644)
	os.WriteFile(bad, []byte("main( {"), 0o644)
	var out, errOut bytes.Buffer
	if code := run([]string{"fmt", "-l", messy, tidy}, nil, &out, &errOut); code != 0 || out.String() != messy+"\n" {
		t.Errorf("-l: exit %d, %q %q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := run([]string{"fmt", messy}, nil, &out, &errOut); code != 0 || out.String() != "main() {\n    println(1 + 2);\n}\n" {
		t.Errorf("print: exit %d, %q", code, out.String())
	}
	if code := run([]string{"fmt", "-w", messy}, nil, &out, &errOut); code != 0 {
		t.Errorf("-w: exit %d", code)
	}
	if got, _ := os.ReadFile(messy); string(got) != "main() {\n    println(1 + 2);\n}\n" {
		t.Errorf("-w wrote %q", got)
	}
	errOut.Reset()
	if code := run([]string{"fmt", "-w", bad}, nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "bad.kkl:1:") {
		t.Errorf("a syntax error: exit %d, %q", code, errOut.String())
	}
	if got, _ := os.ReadFile(bad); string(got) != "main( {" {
		t.Errorf("a file with a syntax error was changed: %q", got)
	}
	if code := run([]string{"fmt", "-w", "-l", messy}, nil, &out, &errOut); code != 2 {
		t.Errorf("-w with -l: exit %d", code)
	}
	// "-": standard input to standard output, alone.
	out.Reset()
	if code := run([]string{"fmt", "-"}, strings.NewReader("main(){\nprintln( 1+2 );\n}\n"), &out, &errOut); code != 0 || out.String() != "main() {\n    println(1 + 2);\n}\n" {
		t.Errorf("fmt -: exit %d, %q", code, out.String())
	}
	errOut.Reset()
	if code := run([]string{"fmt", "-"}, strings.NewReader("main( {"), &out, &errOut); code != 1 || !strings.HasPrefix(errOut.String(), "<stdin>:1:") {
		t.Errorf("fmt - with a syntax error: exit %d, %q", code, errOut.String())
	}
	for _, args := range [][]string{{"fmt", "-w", "-"}, {"fmt", "-l", "-"}, {"fmt", "-", messy}} {
		if code := run(args, strings.NewReader(""), &out, &errOut); code != 2 {
			t.Errorf("%q: exit %d", args, code)
		}
	}
}

func TestRepl(t *testing.T) {
	in := `local n = 10;
n * 2
sq(x) {
    return x * x;
}
sq(n)
class A { v; A(v); get(); }
A::A(v) { this->v = v; }
local a = new A(4);
h() { return 1; } g() { return nope; }
h()
A::get() { return v * n; }
a->get()
local k = fn(x) { return x + n; };
local n = 30;
k(1)
"two
lines";
[1,
 2]

throw new Error("boom");
println("still here");
exit(3);
println("not run");
`
	var out, errOut bytes.Buffer
	// A header on a line of its own (if, while, for) takes the statement
	// on the next line as its body, not the ';' the REPL adds to one line.
	headers := "if (false)\n  println(\"never\");\nlocal i = 0\nwhile (i < 3)\n  i++;\ni\n"
	if code := run([]string{"repl"}, strings.NewReader(headers), &out, &errOut); code != 0 || out.String() != "3\n" {
		t.Errorf("headers: exit %d, %q, %q", code, out.String(), errOut.String())
	}
	out.Reset()
	// An if ending in '}' waits for one more line: an else there joins it,
	// another line runs it and starts the next piece; so does the end of
	// the input.
	elses := "local x = 1;\nif (x > 1) {\n  println(\"big\");\n}\nelse {\n  println(\"small\");\n}\n" +
		"if (x == 1) {\n  println(\"one\");\n}\nprintln(\"two\");\nif (x) { println(\"a\"); }\n\n" +
		"if (x == 2) {\n}\nelse if (x == 1) {\n  println(\"1\");\n}\nelse\n  println(\"never\");\n" +
		"elsewhere = 1\nif (x) {\n  println(\"last\");\n}\n"
	wantElse := "small\none\ntwo\na\n1\nlast\n"
	if code := run([]string{"repl"}, strings.NewReader(elses), &out, &errOut); code != 0 || out.String() != wantElse ||
		!strings.Contains(errOut.String(), "undefined name elsewhere") {
		t.Errorf("else on the next line: exit %d, %q, %q", code, out.String(), errOut.String())
	}
	// Every if without an else waits for the next line, also without
	// braces, after a label or with a comment; comment lines do not count
	// as the next line. The line it looks at stays in the input, for the
	// program's readline() or as the next piece.
	for _, tc := range []struct{ in, want string }{
		{"if (1) { println(readline()); }\nhello\nprintln(\"after\")\n", "hello\nafter\n"},
		{"if (1) println(readline())\nhello\n", "hello\n"},
		{"local m = 0\nif (0) m = {}\nelse println(2)\nm\n", "2\n0\n"},
		{"L: if (0) {}\nelse println(\"L\")\n", "L\n"},
		{"if (0) {} // c\n// between\n/* a block\n   comment */\nelse println(\"c\")\n", "c\n"},
		{"if (1) println(\"x\") // c\n// a comment\nprintln(\"y\")\n", "x\ny\n"},
		{"if (1) if (0) println(\"a\")\nelse println(\"b\")\n", "b\n"},
		{"if (1) println(\"e\")", "e\n"},
		{"if (1) println(\"e\")\n", "e\n"},
		{"if (0) {}\nelse println(2)\nprintln(3)\n", "2\n3\n"},
		{"if (0) {}\nelse\n  println(2)\nprintln(3)\n", "2\n3\n"},
		{"local i = 0\nwhile (i < 3)\n  i++\ni\n", "3\n"},
		{"local j = 0\ndo {\n  j++;\n} while (j < 3)\nj\n", "3\n"},
		{"local k = 0\nwhile (k < 2) if (k == 1) k = 5\nelse k++\nk\n", "5\n"},
		{"if (1) {\n  println(1);\n}\n\nlocal else_ = 2\nelse_\n", "1\n2\n"},
		// with on a line of its own takes the next line as its body
		{"class L { close(); }\nL::close() { println(\"closed\"); }\nwith (local a = new L())\n  println(\"body\");\n", "body\nclosed\n"},
		{"class L { close(); }\nL::close() { println(\"closed\"); }\nwith (local a = new L()) if (0) {}\nelse println(\"else\")\n", "else\nclosed\n"},
		// a try with a catch waits for a finally on the next line
		{"try { throw new Error(\"x\"); } catch (e) { println(1); }\nfinally { println(3); }\nprintln(\"after\")\n", "1\n3\nafter\n"},
		{"try { println(1); }\ncatch (e) {}\n// c\nfinally {\n  println(2);\n}\n", "1\n2\n"},
		{"try { println(4); } catch (e) { }\nprintln(5)\n", "4\n5\n"},
		{"if (0) try {} catch (e) {}\nelse println(\"else\")\n", "else\n"},
		{"if (1) try { println(6); } catch (e) {}\nfinally { println(7); }\n", "6\n7\n"},
		// a line longer than the input's buffer, looked at after an if
		{"if (false) println(1)\nelse println(len(\"" + strings.Repeat("a", 5000) + "\"))\nprintln(\"next\")\n", "5000\nnext\n"},
		{"if (true) println(len(readline()))\n" + strings.Repeat("b", 6000) + "\nprintln(\"after\")\n", "6000\nafter\n"},
	} {
		out.Reset()
		errOut.Reset()
		if code := run([]string{"repl"}, strings.NewReader(tc.in), &out, &errOut); code != 0 || out.String() != tc.want || errOut.Len() > 0 {
			t.Errorf("%q: exit %d, %q, %q; want %q", tc.in, code, out.String(), errOut.String(), tc.want)
		}
	}
	out.Reset()
	errOut.Reset()
	code := run([]string{"repl"}, strings.NewReader(in), &out, &errOut)
	want := "20\n100\n40\n31\n\"two\\nlines\"\n[1, 2]\nstill here\n"
	if code != 3 || out.String() != want {
		t.Errorf("exit %d, output:\n%s\nwant:\n%s", code, out.String(), want)
	}
	wantErr := "<input 8>:1:32: undefined name nope\n<input 9>:1:1: undefined name h\nError: boom\n  at <input> (<input 17>:1)\n"
	if errOut.String() != wantErr {
		t.Errorf("errors:\n%s\nwant:\n%s", errOut.String(), wantErr)
	}

	// The declarations of files first; the end of the input ends the session.
	dir := t.TempDir()
	file := filepath.Join(dir, "lib.kkl")
	os.WriteFile(file, []byte("global base = 40;\nadd(x) { return base + x; }\nmain() { println(\"not run\"); }\n"), 0o644)
	out.Reset()
	errOut.Reset()
	if code := run([]string{"repl", file}, strings.NewReader("add(2)"), &out, &errOut); code != 0 || out.String() != "42\n" || errOut.Len() > 0 {
		t.Errorf("repl with a file: exit %d, %q %q", code, out.String(), errOut.String())
	}
}

func TestBuildModule(t *testing.T) {
	dir := t.TempDir()
	util := filepath.Join(dir, "util.kkl")
	prog := filepath.Join(dir, "main.kkl")
	os.WriteFile(util, []byte("global n = 40;\nadd(x) { return fn(y) { return n + x + y; }; }\n"), 0o644)
	os.WriteFile(prog, []byte("use util;\nmain() { println(util::add(1)(1)); }\n"), 0o644)
	var out, errOut bytes.Buffer
	if code := run([]string{"build", "-module", util}, nil, &out, &errOut); code != 0 {
		t.Fatalf("build -module: exit %d, %s", code, errOut.String())
	}
	os.Rename(util, util+".hidden") // the program uses util.kkc now
	if code := run([]string{"run", prog}, nil, &out, &errOut); code != 0 || out.String() != "42\n" {
		t.Errorf("run with util.kkc: exit %d, %q %q", code, out.String(), errOut.String())
	}
	errOut.Reset()
	if code := run([]string{"exec", filepath.Join(dir, "util.kkc")}, nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "this is a compiled module") {
		t.Errorf("exec of a module: exit %d, %q", code, errOut.String())
	}
	if code := run([]string{"build", "-module", prog, util}, nil, &out, &errOut); code != 2 {
		t.Errorf("build -module of two files: exit %d", code)
	}
	errOut.Reset()
	bad := filepath.Join(dir, "my-util.kkl")
	os.WriteFile(bad, []byte("f() {}\n"), 0o644)
	if code := run([]string{"build", "-module", bad}, nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "a module's file name must be its name") {
		t.Errorf("a module file whose name is not a name: exit %d, %q", code, errOut.String())
	}
}

// In a kukel built without a module (tags), using it names the tags.
func TestOmittedModules(t *testing.T) {
	n := 0
	for _, name := range []string{"db", "p9", "zmq"} {
		tags, ok := native.Omitted(name)
		if !ok {
			continue
		}
		n++
		src := filepath.Join(t.TempDir(), "m.kkl")
		os.WriteFile(src, []byte("use "+name+";\nmain() {}\n"), 0o644)
		var out, errOut bytes.Buffer
		if code := run([]string{"run", src}, nil, &out, &errOut); code != 1 ||
			!strings.Contains(errOut.String(), "module "+name+" is not in this kukel: "+tags) {
			t.Errorf("use %s: exit %d, %q", name, code, errOut.String())
		}
	}
	if n == 0 {
		t.Skip("this kukel has db, p9 and zmq")
	}
}

// TestExamples runs the demo programs in examples/: each name.tests file
// lists runs of name.kkl (see examples/README.md), in that directory.
func TestExamples(t *testing.T) {
	dir, err := filepath.Abs("../../examples")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.tests"))
	if len(files) == 0 {
		t.Fatal("no examples")
	}
	for _, file := range files {
		prog := strings.TrimSuffix(filepath.Base(file), ".tests") + ".kkl"
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src, _ := os.ReadFile(filepath.Join(dir, prog))
		if why := unavailable(string(src)); why != "" {
			t.Logf("%s: %s", prog, why)
			continue
		}
		for _, c := range exampleCases(string(text)) {
			t.Run(prog+" "+c.args, func(t *testing.T) {
				t.Chdir(dir)
				args, stdin := splitArgs(c.args)
				in := strings.NewReader("")
				if stdin != "" {
					data, err := os.ReadFile(stdin)
					if err != nil {
						t.Fatal(err)
					}
					in = strings.NewReader(string(data))
				}
				var out, errOut bytes.Buffer
				code := run(append([]string{"run", prog, "--"}, args...), in, &out, &errOut)
				if out.String() != c.stdout || errOut.String() != c.stderr || code != c.code {
					t.Errorf("exit %d, stdout:\n%s\nstderr:\n%s\nwant exit %d, stdout:\n%s\nstderr:\n%s",
						code, out.String(), errOut.String(), c.code, c.stdout, c.stderr)
				}
			})
		}
	}
}

type exampleCase struct {
	args, stdout, stderr string
	code                 int
}

// exampleCases reads a .tests file: "$ args" (or "$" alone: no
// arguments) starts a case; then the standard output, "! " lines of
// standard error and a last "exit N" line. Blank lines and comments (#) at
// the end of a case, before the next one, are skipped; inside the output
// they belong to it.
func exampleCases(text string) []exampleCase {
	var cases []exampleCase
	var bodies [][]string
	for _, line := range strings.Split(text, "\n") {
		switch {
		case line == "$" || strings.HasPrefix(line, "$ "):
			cases = append(cases, exampleCase{args: strings.TrimPrefix(line[1:], " ")})
			bodies = append(bodies, nil)
		case len(cases) > 0:
			bodies[len(bodies)-1] = append(bodies[len(bodies)-1], line)
		}
	}
	for i, lines := range bodies {
		for n := len(lines); n > 0 && (lines[n-1] == "" || strings.HasPrefix(lines[n-1], "#")); n = len(lines) {
			lines = lines[:n-1]
		}
		c := &cases[i]
		if n := len(lines); n > 0 && strings.HasPrefix(lines[n-1], "exit ") {
			if code, err := strconv.Atoi(lines[n-1][5:]); err == nil {
				c.code, lines = code, lines[:n-1]
			}
		}
		for _, line := range lines {
			if line == "!" || strings.HasPrefix(line, "! ") {
				c.stderr += strings.TrimPrefix(line[1:], " ") + "\n"
			} else {
				c.stdout += line + "\n"
			}
		}
	}
	return cases
}

// Blank lines and comments belong to the output inside a case, and are
// skipped between cases; "$" alone is a case without arguments.
func TestExampleCases(t *testing.T) {
	text := "# header\n\n$\n! no args\nexit 2\n\n# next\n$ a 'b c'\none\n\n# not a comment\ntwo\n!\nexit 1\n\n\n# end\n"
	want := []exampleCase{
		{args: "", stderr: "no args\n", code: 2},
		{args: "a 'b c'", stdout: "one\n\n# not a comment\ntwo\n", stderr: "\n", code: 1},
	}
	if got := exampleCases(text); !slices.Equal(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

// splitArgs splits the arguments of a case at spaces; '...' quotes one, and
// "< file" names the file for standard input.
func splitArgs(s string) (args []string, stdin string) {
	var cur strings.Builder
	quoted, has := false, false
	flush := func() {
		if has {
			args = append(args, cur.String())
		}
		cur.Reset()
		has = false
	}
	for _, r := range s {
		switch {
		case r == '\'':
			quoted, has = !quoted, true
		case r == ' ' && !quoted:
			flush()
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	flush()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "<" {
			stdin = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	return args, stdin
}

// The demo programs are formatted as kukel fmt formats them.
func TestExamplesFormatted(t *testing.T) {
	files, _ := filepath.Glob("../../examples/*.kkl")
	var out, errOut bytes.Buffer
	if code := run(append([]string{"fmt", "-l"}, files...), nil, &out, &errOut); code != 0 || out.Len() > 0 {
		t.Errorf("kukel fmt -l: exit %d, %s%s", code, out.String(), errOut.String())
	}
}

// grep -r and wc report a directory they cannot read and go on with the
// others (the .tests files cannot make one).
func TestExamplesUnreadableDirectory(t *testing.T) {
	examples, err := filepath.Abs("../../examples")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	os.MkdirAll("tree/locked", 0o777)
	os.WriteFile("tree/a.txt", []byte("one error\n"), 0o644)
	os.WriteFile("tree/locked/b.txt", []byte("two errors\n"), 0o644)
	os.WriteFile("c.txt", []byte("an error\n"), 0o644)
	os.Chmod("tree/locked", 0)
	defer os.Chmod("tree/locked", 0o777)
	if _, err := os.ReadDir("tree/locked"); err == nil {
		t.Skip("permissions are not enforced here (root?)")
	}
	for _, tc := range []struct {
		args         []string
		stdout, want string
		code         int
	}{
		{[]string{"grep.kkl", "-r", "error", "tree", "c.txt"}, "tree/a.txt:one error\nc.txt:an error\n", "grep: fs::walk: tree/locked: permission denied\n", 2},
		{[]string{"wc.kkl", "tree", "c.txt"}, "      1       2      10 tree/a.txt\n      1       2       9 c.txt\n      2       4      19 total\n", "wc: fs::walk: tree/locked: permission denied\n", 1},
	} {
		var out, errOut bytes.Buffer
		args := append([]string{"run", filepath.Join(examples, tc.args[0]), "--"}, tc.args[1:]...)
		if code := run(args, nil, &out, &errOut); code != tc.code || out.String() != tc.stdout || errOut.String() != tc.want {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d, %q, %q", tc.args[0], code, out.String(), errOut.String(), tc.code, tc.stdout, tc.want)
		}
	}
}

// A kukel debug session: breakpoints by function and file:line, continue,
// step, finish, print paths, bt, frame, locals, list and the errors.
func TestDebug(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("p.kkl", []byte(debugSrc), 0o644)
	var out bytes.Buffer
	if code := run([]string{"debug", "p.kkl", "--", "hello"}, strings.NewReader(debugCmds), &out, &out); code != 0 || out.String() != debugOut {
		t.Errorf("exit %d:\n%s\nwant:\n%s", code, out.String(), debugOut)
	}
	// A worker runs on: only the main interpreter stops at a breakpoint.
	os.WriteFile("w.kkl", []byte("use worker;\ndouble(x) {\n    return x * 2;\n}\nmain() {\n    local w = worker::start(double, 21);\n    println(double(4), w->wait());\n}\n"), 0o644)
	out.Reset()
	if code := run([]string{"debug", "w.kkl"}, strings.NewReader("b double\nc\np x\nc\n"), &out, &out); code != 0 ||
		!strings.Contains(out.String(), "(kdb) 4\n(kdb) 8 42\nthe program ended (exit code 0)") {
		t.Errorf("a worker: exit %d:\n%s", code, out.String())
	}
	// The first stop is the first line that runs: of a global initializer
	// when the program has one, also in a module (its own file).
	os.WriteFile("gm.kkl", []byte("g() {\n    return 1;\n}\nglobal x = g();\n"), 0o644)
	os.WriteFile("gi.kkl", []byte("use gm;\n\nmain() {\n    println(gm::x);\n}\n"), 0o644)
	out.Reset()
	if code := run([]string{"debug", "gi.kkl"}, strings.NewReader("s\nbt\nc\n"), &out, &out); code != 0 ||
		out.String() != "<init> at gm.kkl:4\n    4  global x = g();\n(kdb) gm::g at gm.kkl:2\n    2      return 1;\n(kdb) *0 gm::g (gm.kkl:2)\n 1 <init> (gm.kkl:4)\n 2 <init> (gi.kkl:1)\n(kdb) 1\nthe program ended (exit code 0)\n" {
		t.Errorf("a global initializer: exit %d:\n%s", code, out.String())
	}
	// A comment on line 1 has no code: the empty <init> is not taken for
	// some.
	os.WriteFile("c.kkl", []byte("// a comment\n\nmain() {\n    println(1);\n}\n"), 0o644)
	out.Reset()
	if run([]string{"debug", "c.kkl"}, strings.NewReader("b 1\nquit\n"), &out, &out); !strings.Contains(out.String(), "(no code on line 1: the next is 4)\nbreakpoint 1 at c.kkl:4") {
		t.Errorf("a comment on line 1:\n%s", out.String())
	}
	// The variables in scope where the program is: a local whose slot a
	// later one reuses, and a catch variable.
	os.WriteFile("v.kkl", []byte("main() {\n    for (local i = 0; i < 1; i++) {\n        println(i);\n    }\n    local w = 5;\n    try {\n        throw new Error(\"boom\");\n    } catch (e) {\n        println(e->message);\n    }\n}\n"), 0o644)
	out.Reset()
	if run([]string{"debug", "v.kkl"}, strings.NewReader("b 3\nb 9\nc\nlocals\np i\nc\nlocals\np i\n"), &out, &out); !strings.Contains(out.String(),
		"(kdb) i = 0\n(kdb) 0\n(kdb) 0\nbreakpoint 2, main at v.kkl:9\n    9          println(e->message);\n(kdb) w = 5\ne = Error{message: \"boom\", kind: \"Error\", trace: [\"main (v.kkl:7)\"]}\n(kdb) no variable i here\n") {
		t.Errorf("variables in scope:\n%s", out.String())
	}
	// print takes expressions without side effects; break takes a condition.
	os.WriteFile("x.kkl", []byte("main() {\n    local v = [10, 20, 30];\n    for (local i = 0; i < 3; i++) {\n        println(v[i]);\n    }\n}\n"), 0o644)
	out.Reset()
	cmds := "b 4 if i == 2\nbreaks\nc\np v[i] + i * 2\np i > 1 && v[0] == 10 ? \"yes\" : \"no\"\np len(v)\np i = 5\np i++\np [i, {\"k\": -i,},]\nc\n"
	if run([]string{"debug", "x.kkl"}, strings.NewReader(cmds), &out, &out); !strings.Contains(out.String(),
		"(kdb) breakpoint 1 at x.kkl:4\n(kdb) 1: x.kkl:4 if i == 2\n(kdb) 10\n20\nbreakpoint 1, main at x.kkl:4\n    4          println(v[i]);\n(kdb) 34\n(kdb) \"yes\"\n"+
			"(kdb) calls are not evaluated: they could change the program\n(kdb) only expressions without side effects are evaluated (names, operators, ?:, [i], ->member)\n"+
			"(kdb) only expressions without side effects are evaluated (names, operators, ?:, [i], ->member)\n(kdb) [2, {\"k\": -2}]\n(kdb) 30\n") {
		t.Errorf("expressions and a condition:\n%s", out.String())
	}
	// The end of the input quits.
	out.Reset()
	if code := run([]string{"debug", "w.kkl"}, strings.NewReader(""), &out, &out); code != 0 || !strings.HasSuffix(out.String(), "(kdb) \nthe program ended (exit code 0)\n") {
		t.Errorf("the end of the input: exit %d:\n%s", code, out.String())
	}
}

// print in a module's function (its globals first), static data members,
// indexes written as in Kukel (an expression: m[x] looks x up), b without an
// argument, and the stop at an exception nothing catches.
func TestDebugLookupAndUncaught(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("mm.kkl", []byte("global n = \"mod n\";\nclass C { static s; f(); static g(); }\nC::f() { s = 1; return n; }\nC::g() { return s; }\nf() {\n    return n;\n}\n"), 0o644)
	os.WriteFile("p.kkl", []byte("use mm;\nglobal n = \"prog n\";\nclass A { static cnt; A(); }\nA::A() {\n    cnt = 5;\n}\n"+
		"main() {\n    local m = {\"a]\": 1, \"q\\\"\": 2, \"\\x41\": 3, -1: 4, 1.5: 6};\n    local a = new A();\n    mm::f();\n    new mm::C()->f();\n    mm::C::g();\n"+
		"    try { sort([2, 1], fn(x, y) { throw new Error(\"in sort\"); }); } catch (e) {}\n    try { throw new Error(\"caught\"); } finally { println(\"finally\"); }\n}\n"), 0o644)
	cmds := "b\nb mm::f\nb mm.kkl:4\nc\np n\np mm::C::s\nc\np n\np s\nc\np m[\"a]\"]\np m[\"q\\\"\"]\np m[\"\\x41\"]\np m[-1]\np m[1.5]\np m[\"a]\"\np m[x]\np A::cnt\np cnt\nbt\nc\n"
	want := "<init> at mm.kkl:1\n    1  global n = \"mod n\";\n" +
		"(kdb) break where? (file:line, line or function)\n(kdb) breakpoint 1 at mm.kkl:6\n(kdb) breakpoint 2 at mm.kkl:4\n" +
		"(kdb) breakpoint 1, mm::f at mm.kkl:6\n    6      return n;\n(kdb) \"mod n\"\n(kdb) nil\n" +
		"(kdb) breakpoint 2, mm::C::g at mm.kkl:4\n    4  C::g() { return s; }\n(kdb) \"mod n\"\n(kdb) 1\n" +
		"(kdb) uncaught Error: caught (continuing ends the program)\nmain at p.kkl:14\n   14      try { throw new Error(\"caught\"); } finally { println(\"finally\"); }\n" +
		"(kdb) 1\n(kdb) 2\n(kdb) 3\n(kdb) 4\n(kdb) 6\n(kdb) expected ']' after index, found end of file\n(kdb) no variable x here\n(kdb) 5\n(kdb) no variable cnt here\n" +
		"(kdb) *0 main (p.kkl:14)\n(kdb) finally\nError: caught\n  at main (p.kkl:14)\nthe program ended (exit code 1)\n"
	var out bytes.Buffer
	if code := run([]string{"debug", "p.kkl"}, strings.NewReader(cmds), &out, &out); code != 1 || out.String() != want {
		t.Errorf("exit %d:\n%s\nwant:\n%s", code, out.String(), want)
	}
	// quit there ends the program with exit code 0, as elsewhere.
	out.Reset()
	if code := run([]string{"debug", "p.kkl"}, strings.NewReader("c\nq\n"), &out, &out); code != 0 || !strings.HasSuffix(out.String(), "(kdb) the program ended (exit code 0)\n") {
		t.Errorf("quit at an uncaught exception: exit %d:\n%s", code, out.String())
	}
}

// Conditions checked when set, breaks with a function's name, a function
// breakpoint on a loop's line (one stop per call), a local before a
// captured variable of its name, this in locals, list past the end, and
// workers that write while the program stops (run with -race).
func TestDebugBreaksAndLocals(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("d.kkl", []byte("class K { n; K(); count(m); }\nK::K() { n = 0; }\nK::count(m) {\n    for (local i = 0; i < m; i++) { n++; }\n    return n;\n}\n"+
		"main() {\n    local x = 1, k = new K();\n    local f = fn() {\n        println(x);\n        {\n            local x = 2;\n            println(x);\n        }\n    };\n    f();\n    println(k->count(3));\n}\n"), 0o644)
	cmds := "b 13 if x ==\nb 13 if len(x) > 0\nb 13 if\nb 13 if x == 2\nb K::count\nb 0\nbreaks\nc\np x\nlocals\nc\nlocals\nlist 100\nc\n"
	want := "main at d.kkl:8\n    8      local x = 1, k = new K();\n" +
		"(kdb) the condition x ==: expected expression, found end of file\n" +
		"(kdb) the condition len(x) > 0: calls are not evaluated: they could change the program\n" +
		"(kdb) break if what? (a condition after if, such as i == 3)\n" +
		"(kdb) breakpoint 1 at d.kkl:13\n(kdb) breakpoint 2 at d.kkl:4\n(kdb) no line 0: lines start at 1\n" +
		"(kdb) 1: d.kkl:13 if x == 2\n2: K::count at d.kkl:4\n" +
		"(kdb) 1\nbreakpoint 1, main/fn@9 at d.kkl:13\n   13              println(x);\n(kdb) 2\n(kdb) x = 1\nx = 2\n" +
		"(kdb) 2\nbreakpoint 2, K::count at d.kkl:4\n    4      for (local i = 0; i < m; i++) { n++; }\n(kdb) this = K{n: 0}\nm = 3\n" +
		"(kdb) no line 100 in d.kkl (it has 18)\n(kdb) 3\nthe program ended (exit code 0)\n"
	var out bytes.Buffer
	if code := run([]string{"debug", "d.kkl"}, strings.NewReader(cmds), &out, &out); code != 0 || out.String() != want {
		t.Errorf("exit %d:\n%s\nwant:\n%s", code, out.String(), want)
	}

	os.WriteFile("w.kkl", []byte("use worker;\nchat(n) {\n    for (local i = 0; i < n; i++) {\n        println(\"w\", i);\n    }\n    return n;\n}\n"+
		"main() {\n    local w = worker::start(chat, 2000);\n    for (local i = 0; i < 5; i++) {\n        println(\"m\", i);\n    }\n    println(w->wait());\n}\n"), 0o644)
	// (The debugger and a worker write at once, as to a terminal: a buffer
	// with a lock.)
	var lout lockedBuffer
	if code := run([]string{"debug", "w.kkl"}, strings.NewReader("b 11\nc\nc\nc\nc\nc\nc\n"), &lout, &lout); code != 0 || !strings.Contains(lout.String(), "\n2000\nthe program ended") {
		t.Errorf("workers: exit %d:\n%.300s", code, lout.String())
	}
}

const debugSrc = "class Acc { total; Acc(); add(x); }\nAcc::Acc() { total = 0; }\nAcc::add(x) {\n    total += x;\n    return this;\n}\n\nsquare(n) {\n    local r = n * n;\n    return r;\n}\n\nmain(args) {\n    local acc = new Acc(), v = [1, 2, 3], m = {\"k\": \"v\"};\n    println(\"start\", args);\n\n    for (local x in v) {\n        acc->add(square(x));\n    }\n    println(\"total\", acc->total);\n    local f = fn(y) { return y + acc->total; };\n    println(f(1));\n}\n"

const debugCmds = "b square\nc\nbt\np n\nfinish\np acc->total\np v[1]\np m[\"k\"]\np v[9]\nframe 9\nd 1\nb p.kkl:22\nbreaks\nc\nstep\nlocals\nlist 22\nb nosuch\nb 999\nwhat\nquit\n"

const debugOut = "main at p.kkl:14\n   14      local acc = new Acc(), v = [1, 2, 3], m = {\"k\": \"v\"};\n(kdb) breakpoint 1 at p.kkl:9\n(kdb) start [\"hello\"]\nbreakpoint 1, square at p.kkl:9\n    9      local r = n * n;\n(kdb) *0 square (p.kkl:9)\n 1 main (p.kkl:18)\n(kdb) 1\n(kdb) main at p.kkl:18\n   18          acc->add(square(x));\n(kdb) 1\n(kdb) 2\n(kdb) \"v\"\n(kdb) index 9 out of range for vector of length 3\n(kdb) no frame \"9\" (0 to 0, as bt numbers them)\n(kdb) breakpoint 1 deleted\n(kdb) breakpoint 2 at p.kkl:22\n(kdb) 2: p.kkl:22\n(kdb) total 14\nbreakpoint 2, main at p.kkl:22\n   22      println(f(1));\n(kdb) main/fn@21 at p.kkl:21\n   21      local f = fn(y) { return y + acc->total; };\n(kdb) acc = Acc{total: 14}\ny = 1\n(kdb)    19      }\n   20      println(\"total\", acc->total);\n>  21      local f = fn(y) { return y + acc->total; };\n   22      println(f(1));\n   23  }\n(kdb) no function \"nosuch\"\n(kdb) no code at or after p.kkl:999\n(kdb) unknown command \"what\" (help lists them)\n(kdb) the program ended (exit code 0)\n"

// An error in a global initializer names the file it is in: a module's,
// or the second file of a program.
func TestInitializerFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("mm.kkl", []byte("\n\n\nglobal u = 1 / 0;\n"), 0o644)
	os.WriteFile("p.kkl", []byte("use mm;\n\nmain() {}\n"), 0o644)
	os.WriteFile("two.kkl", []byte("global a = 1;\nglobal b = [][a];\n"), 0o644)
	os.WriteFile("one.kkl", []byte("main() {}\n"), 0o644)
	for _, tc := range []struct {
		files []string
		want  string
	}{
		{[]string{"p.kkl"}, "at <init> (mm.kkl:4)"},
		{[]string{"one.kkl", "two.kkl"}, "at <init> (two.kkl:2)"},
	} {
		var out, errOut bytes.Buffer
		if code := run(append([]string{"run"}, tc.files...), nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%v: exit %d, %s", tc.files, code, errOut.String())
		}
	}
	// In the REPL too: the files' <init> is at their first file.
	os.WriteFile("three.kkl", []byte("global c = 1 / 0;\n"), 0o644)
	var out, errOut bytes.Buffer
	if run([]string{"repl", "three.kkl"}, strings.NewReader(""), &out, &errOut); !strings.Contains(errOut.String(), "at <init> (three.kkl:1)") {
		t.Errorf("repl: %s", errOut.String())
	}
}

// A file instead of a command is run (or executed): a script with a
// #!/usr/bin/env kukel line gets its arguments as they are.
func TestScript(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("script", []byte("#!/usr/bin/env kukel\nmain(args) {\n    println(args);\n    return len(args);\n}\n"), 0o755)
	os.WriteFile("s.kkl", []byte("main(args) { println(\"kkl\", args); }\n"), 0o644)
	var out, errOut bytes.Buffer
	if code := run([]string{"script", "a", "--", "b c"}, nil, &out, &errOut); code != 3 || out.String() != "[\"a\", \"--\", \"b c\"]\n" {
		t.Errorf("script: exit %d, %q %q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := run([]string{"build", "-o", "s.kkc", "s.kkl"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("build: %s", errOut.String())
	}
	for _, file := range []string{"s.kkl", "s.kkc"} {
		out.Reset()
		if code := run([]string{file, "x"}, nil, &out, &errOut); code != 0 || out.String() != "kkl [\"x\"]\n" {
			t.Errorf("%s: exit %d, %q", file, code, out.String())
		}
	}
	errOut.Reset()
	if code := run([]string{"nosuch.kkl"}, nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "no such file") {
		t.Errorf("a missing file: exit %d, %q", code, errOut.String())
	}
	errOut.Reset()
	if code := run([]string{"frobnicate"}, nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("an unknown command: exit %d, %q", code, errOut.String())
	}
}

// timedWriter notes when each line arrives.
type timedWriter struct {
	mu    sync.Mutex
	lines []string
	at    []time.Time
}

func (w *timedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, line := range strings.SplitAfter(string(p), "\n") {
		if line != "" {
			w.lines = append(w.lines, line)
			w.at = append(w.at, time.Now())
		}
	}
	return len(p), nil
}

// Standard output is buffered, but written out before the program waits:
// each line is seen while the wait after it runs, not at the end.
func TestOutputBeforeWait(t *testing.T) {
	src := `use time;
use events;
use worker;
busy() { // without a wait of its own, which would write out the output
    local t = clock();
    while (clock() - t < 0.6) {}
}
main() {
    println("sleep");
    time::sleep(0.3);
    println("run");
    events::after(0.3, fn() {});
    events::run();
    println("receive");
    local w = worker::start(busy);
    w->receive(0.3);
    println("wait");
    w->wait();
    println("end");
}
`
	path := filepath.Join(t.TempDir(), "wait.kkl")
	os.WriteFile(path, []byte(src), 0o644)
	var out timedWriter
	var errOut bytes.Buffer
	if code := run([]string{"run", path}, nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	want := []string{"sleep\n", "run\n", "receive\n", "wait\n", "end\n"}
	if !slices.Equal(out.lines, want) {
		t.Fatalf("output %q, want %q", out.lines, want)
	}
	for i := 1; i < len(out.at); i++ {
		if d := out.at[i].Sub(out.at[i-1]); d < 200*time.Millisecond {
			t.Errorf("%q came %v after %q: written out only after the wait", out.lines[i], d, out.lines[i-1])
		}
	}
}

// run, build and disasm -nofuse compile without the instructions that do
// the work of several.
func TestNoFuse(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("f.kkl", []byte("main() {\n    local n = 2;\n    n *= 3;\n    println(n + 1);\n}\n"), 0o644)
	for _, nofuse := range []bool{false, true} {
		args := []string{"disasm", "f.kkl"}
		if nofuse {
			args = []string{"disasm", "-nofuse", "f.kkl"}
		}
		var out, errOut bytes.Buffer
		if code := run(args, nil, &out, &errOut); code != 0 || strings.Contains(out.String(), "ARITH_LK") == nofuse {
			t.Errorf("%v: exit %d, %s%s", args, code, out.String(), errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"run", "-nofuse", "f.kkl"}, nil, &out, &errOut); code != 0 || out.String() != "7\n" || !compiler.Fuse {
		t.Errorf("run -nofuse: exit %d, %q %q", code, out.String(), errOut.String())
	}
	if code := run([]string{"build", "-nofuse", "-o", "f.kkc", "f.kkl"}, nil, &out, &errOut); code != 0 {
		t.Errorf("build -nofuse: exit %d, %q", code, errOut.String())
	}
}
