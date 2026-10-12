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
			if code := run([]string{tc.cmd, src}, &stdout, &stderr); code != 0 {
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
	if code := run([]string{"tokens", src}, &stdout, &stderr); code != 1 {
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
	if code := run([]string{"ast", src}, &stdout, &stderr); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	want := src + ":3:1: expected ';' after expression, found '}'\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage: kukel") {
		t.Errorf("no args: exit %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"frobnicate"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), `unknown command "frobnicate"`) {
		t.Errorf("unknown command: exit %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"ast"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage: kukel ast") {
		t.Errorf("ast without files: exit %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"run", "x.kkl"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "not implemented yet") {
		t.Errorf("run: exit %d, stderr %q", code, stderr.String())
	}
}
