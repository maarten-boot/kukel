//go:build tk

package tkmod_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/modules/eventsmod"
	"github.com/maarten-boot/kukel/internal/modules/tkmod"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

// TestExamples runs the tk examples of docs/reference.md and examples/,
// each in a process of its own (Tk starts once in a process): each must
// open its windows, and end without an error once they are closed (a
// second after it started, as a user closing the first window would).
func TestExamples(t *testing.T) {
	display(t)
	dir := t.TempDir()
	doc, err := os.ReadFile("../../../docs/reference.md")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	blocks := strings.Split(string(doc), "```")
	for i := 1; i < len(blocks); i += 2 {
		code := strings.TrimPrefix(blocks[i], "\n")
		if strings.Contains(code, "use tk;") && strings.Contains(code, "main(") {
			file := filepath.Join(dir, fmt.Sprintf("reference-%d.kkl", len(files)+1))
			if err := os.WriteFile(file, []byte(code), 0o644); err != nil {
				t.Fatal(err)
			}
			files = append(files, file)
		}
	}
	if len(files) < 4 {
		t.Fatalf("%d tk examples in docs/reference.md, want at least 4", len(files))
	}
	examples, _ := filepath.Glob("../../../examples/*.kkl")
	for _, file := range examples {
		if src, _ := os.ReadFile(file); strings.Contains(string(src), "use tk;") {
			files = append(files, file)
		}
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(), "KUKEL_TK_EXAMPLE="+file)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("%v\n%s", err, out.String())
				}
			case <-time.After(30 * time.Second):
				cmd.Process.Kill()
				t.Errorf("did not end\n%s", out.String())
			}
		})
	}
}

// TestNotes types the shortcuts of examples/notes.kkl into its text: they
// must not edit the text (Tk's Text makes a new line at Ctrl+O), so the
// text has no changes and Ctrl+O opens the file dialog at once.
func TestNotes(t *testing.T) {
	display(t)
	if _, err := exec.LookPath("xdotool"); err != nil || os.Getenv("KUKEL_TK_NOCLICK") != "" {
		t.Skip("no xdotool")
	}
	file := filepath.Join(t.TempDir(), "n.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "KUKEL_TK_EXAMPLE=../../../examples/notes.kkl", "KUKEL_TK_ARG="+file)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%v\n%s", err, out.String())
			}
		case <-time.After(30 * time.Second):
			cmd.Process.Kill()
			t.Errorf("did not end\n%s", out.String())
		}
	}()
	title := file + " - notes"
	var win string
	for i := 0; i < 100 && win == ""; i++ {
		got, _ := exec.Command("xdotool", "search", "--name", "^"+regexp.QuoteMeta(title)+"$").Output()
		win = strings.TrimSpace(strings.SplitN(string(got), "\n", 2)[0])
		time.Sleep(100 * time.Millisecond)
	}
	if win == "" {
		t.Errorf("no window %q", title)
		return
	}
	// Into the text, then Ctrl+O: the dialog to open a file, cancelled.
	if err := clickCenter(win); err != nil {
		t.Error(err)
		return
	}
	if err := exec.Command("xdotool", "key", "ctrl+o").Run(); err != nil {
		t.Error(err)
		return
	}
	if err := answer("Open", "", "Escape"); err != nil {
		t.Error(err)
		return
	}
	if got, _ := exec.Command("xdotool", "getwindowname", win).Output(); strings.TrimSpace(string(got)) != title {
		t.Errorf("the title is %q, want %q (no changes)", strings.TrimSpace(string(got)), title)
	}
	// Ctrl+Q quits: there is nothing to save.
	clickCenter(win)
	exec.Command("xdotool", "key", "ctrl+q").Run()
}

// runExample runs the program in file, on the main goroutine, and closes
// its windows after a second (unless $KUKEL_TK_ARG gives it an argument:
// a test then closes it); it gives the exit code.
func runExample(file string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	src, err := os.ReadFile(file)
	if err != nil {
		return fail(err)
	}
	f, err := parser.ParseFile(file, src)
	if err != nil {
		return fail(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		return fail(err)
	}
	m, err := vm.New(prog, builtins.Lookup, os.Stdout, strings.NewReader(""))
	if err != nil {
		return fail(err)
	}
	args := []string{}
	if arg := os.Getenv("KUKEL_TK_ARG"); arg != "" {
		args = append(args, arg)
	} else {
		loop := eventsmod.For(m)
		go func() {
			time.Sleep(time.Second)
			loop.Post(func() error { tkmod.CloseAll(); return nil })
		}()
	}
	if _, err := m.Run(args); err != nil {
		return fail(err)
	}
	return 0
}
