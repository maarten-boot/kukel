package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// On a terminal (through script(1), which gives the REPL a pseudo
// terminal), the banner comes before the first prompt, and a piece's output
// after the line typed.
func TestReplTerminal(t *testing.T) {
	if os.Getenv("KUKEL_TEST_AS_KUKEL") == "1" {
		os.Exit(run([]string{"repl"}, os.Stdin, os.Stdout, os.Stderr))
	}
	script, err := exec.LookPath("script")
	if err != nil || runtime.GOOS != "linux" {
		t.Skip("needs script(1) of util-linux")
	}
	cmd := exec.Command(script, "-qfec", os.Args[0]+" -test.run=^TestReplTerminal$", "/dev/null")
	cmd.Env = append(os.Environ(), "KUKEL_TEST_AS_KUKEL=1", "TERM=dumb")
	stdin, _ := cmd.StdinPipe()
	out := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Skip("script does not start:", err)
	}
	// Type when the prompt is there (the editor has the terminal then),
	// and Ctrl-D at the prompt after the result: typed while the REPL gives
	// the terminal back to the editor, the terminal (not yet raw) could
	// take Ctrl-D as its own end of file, and the REPL would not see it
	// (a CI run failed so).
	waitFor := func(text string) {
		for start := time.Now(); !strings.Contains(out.String(), text); time.Sleep(10 * time.Millisecond) {
			if time.Since(start) > 10*time.Second {
				cmd.Process.Kill()
				t.Fatalf("no %q:\n%q", text, out.String())
			}
		}
	}
	waitFor("> ")
	io.WriteString(stdin, "println(\"re\" + \"sult\")\r")
	waitFor("result\r\n> ")
	io.WriteString(stdin, "\x04")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("the REPL did not end:\n%q", out.String())
	}
	s := out.String()
	banner, typed, result := strings.Index(s, "type declarations"), strings.Index(s, `println("re" + "sult")`), strings.Index(s, "result")
	if banner < 0 || typed < 0 || result < 0 || !(banner < typed && typed < result) {
		t.Errorf("banner at %d, input at %d, output at %d:\n%q", banner, typed, result, s)
	}
}

// The history keeps the last historyMax lines that are not blank, and none
// while a program reads.
func TestHistory(t *testing.T) {
	h := &history{}
	for i := 0; i < historyMax+500; i++ {
		h.Add(fmt.Sprint("line ", i))
		h.Add("  ")
	}
	h.off = true
	h.Add("read by a program")
	h.off = false
	if h.Len() != historyMax || h.At(0) != fmt.Sprint("line ", historyMax+499) || h.At(historyMax-1) != "line 500" {
		t.Errorf("Len %d, newest %q, oldest %q", h.Len(), h.At(0), h.At(h.Len()-1))
	}
}

// lockedBuffer is a bytes.Buffer several goroutines may use.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// On a terminal (script(1) again), examples/vi.kkl draws the file on the
// alternate screen, takes keys one at a time (module term in raw mode:
// nothing waits for Enter), writes the file with :wq and gives the screen
// back.
func TestViTerminal(t *testing.T) {
	if os.Getenv("KUKEL_TEST_AS_VI") != "" {
		os.Exit(run([]string{"run", "../../examples/vi.kkl", "--", os.Getenv("KUKEL_TEST_AS_VI")}, os.Stdin, os.Stdout, os.Stderr))
	}
	script, err := exec.LookPath("script")
	if err != nil || runtime.GOOS != "linux" {
		t.Skip("needs script(1) of util-linux")
	}
	file := t.TempDir() + "/notes.txt"
	os.WriteFile(file, []byte("first line\nsecond line\n"), 0o644)
	cmd := exec.Command(script, "-qfec", os.Args[0]+" -test.run=^TestViTerminal$", "/dev/null")
	cmd.Env = append(os.Environ(), "KUKEL_TEST_AS_VI="+file, "TERM=xterm")
	stdin, _ := cmd.StdinPipe()
	out := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Skip("script does not start:", err)
	}
	waitFor := func(text string) {
		for start := time.Now(); !strings.Contains(out.String(), text); time.Sleep(10 * time.Millisecond) {
			if time.Since(start) > 10*time.Second {
				cmd.Process.Kill()
				t.Fatalf("no %q:\n%q", text, out.String())
			}
		}
	}
	waitFor("second line")
	// One key at a time, as typed: each must arrive without an Enter.
	for _, k := range []string{"j", "A", "!", "\x1b", "g", "g", "x", ":", "w", "q", "\r"} {
		io.WriteString(stdin, k)
		time.Sleep(60 * time.Millisecond) // longer than the wait after Esc
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("%v:\n%q", err, out.String())
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("vi did not end:\n%q", out.String())
	}
	if got, _ := os.ReadFile(file); string(got) != "irst line\nsecond line!\n" {
		t.Errorf("the file is %q", got)
	}
	s := out.String()
	if on, off := strings.Index(s, "\x1b[?1049h"), strings.LastIndex(s, "\x1b[?1049l"); on < 0 || off < on {
		t.Errorf("the alternate screen: on at %d, off at %d", on, off)
	}
}
