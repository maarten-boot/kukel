package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
// is none). Comment lines at the top set the arguments and the expected
// exit code: "// args: a b" and "// exit: 3" (default 0, or 1 with a .err
// file). `make golden` rewrites .out and .err files.
func TestRunGolden(t *testing.T) {
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
			var stdout, stderr bytes.Buffer
			code := run(append([]string{"run", src, "--"}, args...), strings.NewReader(""), &stdout, &stderr)
			gotErr := strings.ReplaceAll(stderr.String(), filepath.Dir(src)+string(filepath.Separator), "")
			if *update {
				if err := os.WriteFile(base+".out", stdout.Bytes(), 0o644); err != nil {
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
			if stdout.String() != string(wantOut) {
				t.Errorf("stdout differs from %s.out\n%s", base, firstDiff(stdout.String(), string(wantOut)))
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
	if code := run([]string{"build", "x.kkl"}, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "not implemented yet") {
		t.Errorf("build: exit %d, stderr %q", code, stderr.String())
	}
}
