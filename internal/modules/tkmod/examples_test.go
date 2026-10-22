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
// each in a process of its own (Tk starts once in a process), in the
// directory examples (where the pictures of the reference are): each must
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
	for i, file := range examples {
		if examples[i], err = filepath.Abs(file); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range examples {
		if src, _ := os.ReadFile(file); strings.Contains(string(src), "use tk;") {
			files = append(files, file)
		}
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(), "KUKEL_TK_EXAMPLE="+file)
			cmd.Dir = "../../../examples"
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

// TestSettings clicks the settings example of docs/reference.md: its
// button "Settings..." twice (one settings window opens), then OK (it
// closes).
func TestSettings(t *testing.T) {
	display(t)
	if _, err := exec.LookPath("xdotool"); err != nil || os.Getenv("KUKEL_TK_NOCLICK") != "" {
		t.Skip("no xdotool")
	}
	doc, err := os.ReadFile("../../../docs/reference.md")
	if err != nil {
		t.Fatal(err)
	}
	var code string
	blocks := strings.Split(string(doc), "```")
	for i := 1; i < len(blocks); i += 2 {
		if strings.Contains(blocks[i], `"Settings..."`) {
			code = strings.TrimPrefix(blocks[i], "\n")
		}
	}
	if code == "" {
		t.Fatal("no settings example in docs/reference.md")
	}
	file := filepath.Join(t.TempDir(), "settings.kkl")
	if err := os.WriteFile(file, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "KUKEL_TK_EXAMPLE="+file, "KUKEL_TK_ARG=stay") // closed by the test
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		cmd.Process.Kill() // it has no way to quit but its window
		<-done
	}()
	main := findWindow("Main")
	if main == "" {
		t.Fatalf("no window Main\n%s", out.String())
	}
	// Without a window manager windows open at 0,0: Main moves away, so
	// that the settings window does not cover it. The button is below the
	// summary: near the bottom of the window.
	exec.Command("xdotool", "windowmove", main, "400", "300").Run()
	time.Sleep(300 * time.Millisecond)
	bottom := func(win string, dx int) (int, int) {
		geo, _ := exec.Command("xdotool", "getwindowgeometry", "--shell", win).Output()
		var x, y, w, h int
		for _, line := range strings.Split(string(geo), "\n") {
			fmt.Sscanf(line, "X=%d", &x)
			fmt.Sscanf(line, "Y=%d", &y)
			fmt.Sscanf(line, "WIDTH=%d", &w)
			fmt.Sscanf(line, "HEIGHT=%d", &h)
		}
		if dx < 0 {
			return x + w + dx, y + h - 22
		}
		return x + w/2, y + h - 22
	}
	x, y := bottom(main, 0)
	for i := 0; i < 2; i++ {
		exec.Command("xdotool", "mousemove", fmt.Sprint(x), fmt.Sprint(y), "click", "1").Run()
		time.Sleep(500 * time.Millisecond)
	}
	got, _ := exec.Command("xdotool", "search", "--onlyvisible", "--name", "^Settings$").Output()
	wins := strings.Fields(string(got))
	if len(wins) != 1 {
		t.Fatalf("%d settings windows open, want 1", len(wins))
	}
	// OK, at the bottom right.
	x, y = bottom(wins[0], -40)
	exec.Command("xdotool", "mousemove", fmt.Sprint(x), fmt.Sprint(y), "click", "1").Run()
	for i := 0; i < 30; i++ {
		if exec.Command("xdotool", "search", "--onlyvisible", "--name", "^Settings$").Run() != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("OK did not close the settings window")
}

// TestNotes presses Ctrl+O and Ctrl+Q in the text of examples/notes.kkl:
// they must not edit the text (Tk's Text makes a new line at Ctrl+O), so
// the text has no changes and Ctrl+O opens the file dialog at once.
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
