package kukel_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBookletGoPrograms runs the Go programs of the booklet (a ```go block
// with "package main") with go run, as programs of this module, and checks
// that each prints what the block after it shows ("$ go run ." and the
// output), when there is one. The appendices on embedding must not show Go
// code that fails.
func TestBookletGoPrograms(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program per example")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	chapters, err := filepath.Glob("docs/booklet/*.md")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, chapter := range chapters {
		text, err := os.ReadFile(chapter)
		if err != nil {
			t.Fatal(err)
		}
		blocks := strings.Split(string(text), "```")
		for i := 1; i < len(blocks); i += 2 {
			code, ok := strings.CutPrefix(blocks[i], "go\n")
			if !ok || !strings.Contains(code, "package main") {
				continue
			}
			n++
			want, shown := "", false
			if i+2 < len(blocks) {
				var out string
				if out, shown = strings.CutPrefix(strings.TrimPrefix(blocks[i+2], "\n"), "$ go run .\n"); shown {
					want = out
				}
			}
			t.Run(fmt.Sprintf("%s-%d", filepath.Base(chapter), n), func(t *testing.T) {
				// A directory in the module (a name with _ is left out of ./...),
				// so that the program builds against this kukel.
				dir, err := os.MkdirTemp(".", "_booklet-")
				if err != nil {
					t.Fatal(err)
				}
				defer os.RemoveAll(dir)
				if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0o644); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("go", "run", "./"+dir)
				var stdout, stderr strings.Builder
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err != nil {
					t.Fatalf("%v\n%s%s\n%s", err, stdout.String(), stderr.String(), code)
				}
				if shown && stdout.String() != want {
					t.Errorf("prints\n%s\nbut the booklet shows\n%s", stdout.String(), want)
				}
			})
		}
	}
	if n == 0 {
		t.Error("no Go programs in the booklet")
	}
}
