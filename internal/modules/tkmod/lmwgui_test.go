//go:build tk

package tkmod_test

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A kukelProcess is a program run by this test binary in a process of its
// own (TestMain runs it), whose output lines arrive on lines.
type kukelProcess struct {
	cmd   *exec.Cmd
	lines chan string
	done  chan error
	seen  []string
}

func startKukel(t *testing.T, file string, args ...string) *kukelProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "KUKEL_TK_EXAMPLE="+file, "KUKEL_TK_ARGS="+strings.Join(args, "\n"))
	cmd.Dir = filepath.Dir(file)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &kukelProcess{cmd: cmd, lines: make(chan string, 1000), done: make(chan error, 1)}
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			p.lines <- s.Text()
		}
		close(p.lines)
		p.done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
	})
	return p
}

// expect waits for a line that matches re (at most 10 s), and gives its
// submatches; the lines before it are kept in seen.
func (p *kukelProcess) expect(t *testing.T, re string) []string {
	t.Helper()
	r := regexp.MustCompile(re)
	timeout := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				t.Fatalf("the program ended without %q; its output:\n%s", re, strings.Join(p.seen, "\n"))
			}
			p.seen = append(p.seen, line)
			if m := r.FindStringSubmatch(line); m != nil {
				return m
			}
		case <-timeout:
			t.Fatalf("no line %q within 10 s; the output:\n%s", re, strings.Join(p.seen, "\n"))
		}
	}
}

// TestLmwgui runs examples/lmw/lmwgui.kkl against a fake synth served by
// lmwctl.kkl in another process (docs/future.md §13.8): the panels show
// what the synth has, a fader dragged with xdotool writes its parameter
// (at most every 20 ms) and the synth has the last value, a change made by
// another client shows, the contrast comes from the settings file, and a
// synth that goes away greys the window.
func TestLmwgui(t *testing.T) {
	display(t)
	if _, err := exec.LookPath("xdotool"); err != nil || os.Getenv("KUKEL_TK_NOCLICK") != "" {
		t.Skip("no xdotool")
	}
	dir, err := filepath.Abs("../../../examples/lmw")
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "lmw-gui.toml")
	if err := os.WriteFile(config, []byte("[contrast]\npreset = \"Custom\"\nratio = 10.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := startKukel(t, filepath.Join(dir, "lmwctl.kkl"), "-fake", "serve", "30")
	addr := fake.expect(t, `^serving (\S+)$`)[1]
	gui := startKukel(t, filepath.Join(dir, "lmwgui.kkl"), "-a", addr, "-v", "-config", config)
	gui.expect(t, `^contrast Custom 10\.0$`)
	gui.expect(t, `^shown all 51$`)
	at := gui.expect(t, `^at vcf1\.cutoff (\d+) (\d+) (\d+) (\d+)$`)
	x, _ := strconv.Atoi(at[1])
	y, _ := strconv.Atoi(at[2])
	w, _ := strconv.Atoi(at[3])
	time.Sleep(300 * time.Millisecond) // the last places settle

	var win string
	for i := 0; i < 50 && win == ""; i++ {
		got, _ := exec.Command("xdotool", "search", "--name", "^LMW-Synth: ").Output()
		win = strings.TrimSpace(strings.SplitN(string(got), "\n", 2)[0])
		time.Sleep(100 * time.Millisecond)
	}
	if win == "" {
		t.Fatal("no window LMW-Synth")
	}
	geo, _ := exec.Command("xdotool", "getwindowgeometry", "--shell", win).Output()
	var wx, wy int
	for _, line := range strings.Fields(string(geo)) {
		fmt.Sscanf(line, "X=%d", &wx)
		fmt.Sscanf(line, "Y=%d", &wy)
	}

	// The cutoff is at its top (18 kHz): its handle is at the top of the
	// fader. Drag it down.
	hx, hy := wx+x+w/2, wy+y+8
	run := func(args ...string) {
		if err := exec.Command("xdotool", args...).Run(); err != nil {
			t.Fatalf("xdotool %v: %v", args, err)
		}
	}
	run("mousemove", fmt.Sprint(hx), fmt.Sprint(hy), "mousedown", "1")
	for i := 1; i <= 15; i++ {
		run("mousemove", fmt.Sprint(hx), fmt.Sprint(hy+4*i))
		time.Sleep(5 * time.Millisecond)
	}
	run("mouseup", "1")
	writes := 0
	var last string
	timeout := time.After(3 * time.Second)
wait:
	for {
		select {
		case line := <-gui.lines:
			gui.seen = append(gui.seen, line)
			if m := regexp.MustCompile(`^write vcf1\.cutoff (\S+)$`).FindStringSubmatch(line); m != nil {
				writes++
				last = m[1]
			}
		case <-timeout:
			break wait
		}
	}
	if writes == 0 || writes > 15 {
		t.Fatalf("%d writes of the cutoff for 15 moves; the output:\n%s", writes, strings.Join(gui.seen, "\n"))
	}
	if v, _ := strconv.ParseFloat(last, 64); v >= 18000 || v < 20 {
		t.Errorf("the last write is %s, want below 18000", last)
	}
	get := startKukel(t, filepath.Join(dir, "lmwctl.kkl"), "-a", addr, "get", "vcf1.cutoff")
	if got := get.expect(t, `^(.+)$`)[1]; got != last && got != last+".0" {
		t.Errorf("the synth has the cutoff %s, the GUI wrote %s last", got, last)
	}

	// Another client changes the volume and a choice: the GUI shows both.
	startKukel(t, filepath.Join(dir, "lmwctl.kkl"), "-a", addr, "set", "out.gain", "0.5", ",", "set", "vcf2.type", "notch")
	gui.expect(t, `^show (out\.gain 0\.5|vcf2\.type Notch)$`)
	gui.expect(t, `^show (out\.gain 0\.5|vcf2\.type Notch)$`)

	// The synth goes away.
	fake.cmd.Process.Kill()
	gui.expect(t, `^lost$`)
}
