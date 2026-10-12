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

// TestTokensGolden compares `kukel tokens` output for every tests/tokens/*.kkl
// with the matching .tokens file. Run `go test ./cmd/kukel -update` to
// rewrite the golden files after an intended change.
func TestTokensGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(testdir, "tokens", "*.kkl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files found: %v", err)
	}
	for _, src := range files {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"tokens", src}, &stdout, &stderr); code != 0 {
			t.Errorf("%s: exit code %d: %s", src, code, stderr.String())
			continue
		}
		// Golden files use paths relative to tests/tokens.
		got := strings.ReplaceAll(stdout.String(), filepath.Dir(src)+string(filepath.Separator), "")
		golden := strings.TrimSuffix(src, ".kkl") + ".tokens"
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
			t.Errorf("%s: output differs from %s (run with -update after checking the change)\n%s",
				src, golden, firstDiff(got, string(want)))
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
	if code := run([]string{"run", "x.kkl"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "not implemented yet") {
		t.Errorf("run: exit %d, stderr %q", code, stderr.String())
	}
}
