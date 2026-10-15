package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

// fullModules are the modules of a kukel built without tags.
const fullModules = "db (SQLite, PostgreSQL, MySQL), events, fs, json, re, strings, time, uuid, worker, zmq"

// fullBuild reports whether this kukel has every module and database
// (no build tag left one out).
func fullBuild() bool {
	_, zmq := native.Lookup("zmq")
	return zmq && len(dbmod.Kinds()) == 3
}

// unavailable tells why program src cannot run in this kukel, built
// without a module or database it uses ("" if it can).
func unavailable(src string) string {
	if _, ok := native.Lookup("zmq"); !ok && strings.Contains(src, "use zmq;") {
		return "built without zmq (tag nozmq)"
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
		!strings.Contains(stdout.String(), "modules written in Go: "+fullModules+"\n") && fullBuild() {
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
	for _, name := range []string{"db", "zmq"} {
		tags, ok := native.Omitted(name)
		if !ok {
			continue
		}
		n++
		src := filepath.Join(t.TempDir(), "m.kkl")
		os.WriteFile(src, []byte("use "+name+";\nmain() {}\n"), 0o644)
		var out, errOut bytes.Buffer
		if code := run([]string{"run", src}, nil, &out, &errOut); code != 1 ||
			!strings.Contains(errOut.String(), "module "+name+" is not in this kukel: it was built with "+tags) {
			t.Errorf("use %s: exit %d, %q", name, code, errOut.String())
		}
	}
	if n == 0 {
		t.Skip("this kukel has db and zmq")
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

// exampleCases reads a .tests file: "$ args" starts a case; then the
// standard output, "! " lines of standard error and an "exit N" line.
// Comments (#) and blank lines between cases are skipped.
func exampleCases(text string) []exampleCase {
	var cases []exampleCase
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "$ "):
			cases = append(cases, exampleCase{args: line[2:]})
		case len(cases) == 0 || strings.HasPrefix(line, "#") || line == "":
		case strings.HasPrefix(line, "! "):
			cases[len(cases)-1].stderr += line[2:] + "\n"
		case strings.HasPrefix(line, "exit "):
			cases[len(cases)-1].code, _ = strconv.Atoi(line[5:])
		default:
			cases[len(cases)-1].stdout += line + "\n"
		}
	}
	return cases
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
	// The end of the input quits.
	out.Reset()
	if code := run([]string{"debug", "w.kkl"}, strings.NewReader(""), &out, &out); code != 0 || !strings.HasSuffix(out.String(), "(kdb) \nthe program ended (exit code 0)\n") {
		t.Errorf("the end of the input: exit %d:\n%s", code, out.String())
	}
}

const debugSrc = "class Acc { total; Acc(); add(x); }\nAcc::Acc() { total = 0; }\nAcc::add(x) {\n    total += x;\n    return this;\n}\n\nsquare(n) {\n    local r = n * n;\n    return r;\n}\n\nmain(args) {\n    local acc = new Acc(), v = [1, 2, 3], m = {\"k\": \"v\"};\n    println(\"start\", args);\n\n    for (local x in v) {\n        acc->add(square(x));\n    }\n    println(\"total\", acc->total);\n    local f = fn(y) { return y + acc->total; };\n    println(f(1));\n}\n"

const debugCmds = "b square\nc\nbt\np n\nfinish\np acc->total\np v[1]\np m[\"k\"]\np v[9]\nframe 9\nd 1\nb p.kkl:22\nbreaks\nc\nstep\nlocals\nlist 22\nb nosuch\nb 999\nwhat\nquit\n"

const debugOut = "main at p.kkl:14\n   14      local acc = new Acc(), v = [1, 2, 3], m = {\"k\": \"v\"};\n(kdb) breakpoint 1 at p.kkl:9\n(kdb) start [\"hello\"]\nbreakpoint 1, square at p.kkl:9\n    9      local r = n * n;\n(kdb) *0 square (p.kkl:9)\n 1 main (p.kkl:18)\n(kdb) 1\n(kdb) main at p.kkl:18\n   18          acc->add(square(x));\n(kdb) 1\n(kdb) 2\n(kdb) \"v\"\n(kdb) index 9 out of range for vector of length 3\n(kdb) no frame \"9\" (0 to 0, as bt numbers them)\n(kdb) breakpoint 1 deleted\n(kdb) breakpoint 2 at p.kkl:22\n(kdb) 2: p.kkl:22\n(kdb) total 14\nbreakpoint 2, main at p.kkl:22\n   22      println(f(1));\n(kdb) main/fn@21 at p.kkl:21\n   21      local f = fn(y) { return y + acc->total; };\n(kdb) y = 1\nacc = Acc{total: 14}\n(kdb)    19      }\n   20      println(\"total\", acc->total);\n>  21      local f = fn(y) { return y + acc->total; };\n   22      println(f(1));\n   23  }\n   24  \n(kdb) no function \"nosuch\"\n(kdb) no code at or after p.kkl:999\n(kdb) unknown command \"what\" (help lists them)\n(kdb) the program ended (exit code 0)\n"
