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
	return startKukelEnv(t, nil, file, args...)
}

// startKukelEnv is startKukel with more environment variables ("NAME=value").
func startKukelEnv(t *testing.T, env []string, file string, args ...string) *kukelProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), env...), "KUKEL_TK_EXAMPLE="+file, "KUKEL_TK_ARGS="+strings.Join(args, "\n"))
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

// places waits for the lines "at name x y w h" of lmwgui -v for each of
// names, in any order, and gives their numbers by name.
func (p *kukelProcess) places(t *testing.T, names ...string) map[string][4]int {
	t.Helper()
	got := map[string][4]int{}
	r := regexp.MustCompile(`^at (\S+) (\d+) (\d+) (\d+) (\d+)$`)
	timeout := time.After(10 * time.Second)
	for {
		done := true
		for _, name := range names {
			if _, ok := got[name]; !ok {
				done = false
			}
		}
		if done {
			return got
		}
		select {
		case line, ok := <-p.lines:
			if !ok {
				t.Fatalf("the program ended before the places of %v; its output:\n%s", names, strings.Join(p.seen, "\n"))
			}
			p.seen = append(p.seen, line)
			if m := r.FindStringSubmatch(line); m != nil {
				var v [4]int
				for i := range v {
					v[i], _ = strconv.Atoi(m[i+2])
				}
				got[m[1]] = v
			}
		case <-timeout:
			t.Fatalf("no places of %v within 10 s; the output:\n%s", names, strings.Join(p.seen, "\n"))
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
	wx, wy := lmwWindow(t)

	// The window fits a screen of 1024 by 768 (docs/future.md 13.5), as
	// Xvfb's here.
	geo, _ := exec.Command("xdotool", "search", "--name", "^LMW-Synth: ").Output()
	size, _ := exec.Command("xdotool", "getwindowgeometry", "--shell", strings.Fields(string(geo))[0]).Output()
	var ww, wh int
	for _, line := range strings.Fields(string(size)) {
		fmt.Sscanf(line, "WIDTH=%d", &ww)
		fmt.Sscanf(line, "HEIGHT=%d", &wh)
	}
	if ww == 0 || ww > 1024 || wh > 768 {
		t.Errorf("the window is %dx%d, more than a 1024x768 screen", ww, wh)
	}

	// The cutoff is at its top (18 kHz): its handle is at the top of the
	// fader. Drag it down.
	hx, hy := wx+x+w/2, wy+y+8
	run := func(args ...string) { xdo(t, args...) }
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

// lmwWindow finds the window of lmwgui: its place on the screen.
func lmwWindow(t *testing.T) (x, y int) {
	t.Helper()
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
	for _, line := range strings.Fields(string(geo)) {
		fmt.Sscanf(line, "X=%d", &x)
		fmt.Sscanf(line, "Y=%d", &y)
	}
	return x, y
}

// xdo runs xdotool. A mousemove off the screen (where xdotool would put
// the mouse at the edge, on another widget) fails the test.
func xdo(t *testing.T, args ...string) {
	t.Helper()
	if len(args) >= 3 && args[0] == "mousemove" {
		out, _ := exec.Command("xdotool", "getdisplaygeometry").Output()
		var w, h, x, y int
		fmt.Sscanf(string(out), "%d %d", &w, &h)
		fmt.Sscan(args[1], &x)
		fmt.Sscan(args[2], &y)
		if w > 0 && (x < 0 || y < 0 || x >= w || y >= h) {
			t.Fatalf("xdotool %v: the point is off the %dx%d screen (is the window too wide?)", args, w, h)
		}
	}
	if err := exec.Command("xdotool", args...).Run(); err != nil {
		t.Fatalf("xdotool %v: %v", args, err)
	}
}

// TestLmwguiMatrix uses the Matrix tab of lmwgui against a fake synth: a
// click puts a cable in, a drag sets its level, the keys move the cursor
// and put in a second cable and lower it, a fifth cable on an input is
// refused (the synth's message, and the cell shows what the synth has),
// and another client's cable shows.
func TestLmwguiMatrix(t *testing.T) {
	display(t)
	if _, err := exec.LookPath("xdotool"); err != nil || os.Getenv("KUKEL_TK_NOCLICK") != "" {
		t.Skip("no xdotool")
	}
	dir, err := filepath.Abs("../../../examples/lmw")
	if err != nil {
		t.Fatal(err)
	}
	ctl := filepath.Join(dir, "lmwctl.kkl")
	fake := startKukel(t, ctl, "-fake", "serve", "30")
	addr := fake.expect(t, `^serving (\S+)$`)[1]
	full := startKukel(t, ctl, "-a", addr, "cable", "vcf2.in", "vco1.out+vco2.out+vco3.out+noise.out")
	if err := <-full.done; err != nil {
		t.Fatal(err)
	}
	gui := startKukel(t, filepath.Join(dir, "lmwgui.kkl"), "-a", addr, "-v", "-tab", "Matrix",
		"-config", filepath.Join(t.TempDir(), "lmw-gui.toml"))
	sizes := gui.expect(t, `^matrix cells (\d+) (\d+) (\d+) (\d+)$`)
	gui.expect(t, `^shown all 51$`)
	at := gui.expect(t, `^at matrix (\d+) (\d+) \d+ \d+$`)
	time.Sleep(300 * time.Millisecond)
	wx, wy := lmwWindow(t)
	n := func(s string) int { i, _ := strconv.Atoi(s); return i }
	// The middle of cell (input i, output o): the inputs and outputs in
	// the order of the fake's /ports.
	cellAt := func(i, o int) (string, string) {
		x := wx + n(at[1]) + n(sizes[1]) + o*n(sizes[3]) + n(sizes[3])/2
		y := wy + n(at[2]) + n(sizes[2]) + i*n(sizes[4]) + n(sizes[4])/2
		return fmt.Sprint(x), fmt.Sprint(y)
	}
	const cutoff, vcf2in, lfo1, lfo2 = 13, 15, 8, 9

	x, y := cellAt(cutoff, lfo1)
	xdo(t, "mousemove", x, y, "click", "1")
	gui.expect(t, `^write cables vcf1\.cutoff lfo1\.out$`)
	time.Sleep(700 * time.Millisecond) // not a double click

	xdo(t, "mousemove", x, y, "mousedown", "1")
	for i := 1; i <= 6; i++ {
		xdo(t, "mousemove_relative", "0", "5")
		time.Sleep(10 * time.Millisecond)
	}
	xdo(t, "mouseup", "1")
	gui.expect(t, `^write cables vcf1\.cutoff lfo1\.out@0\.8$`)

	xdo(t, "key", "Right", "space")
	gui.expect(t, `^write cables vcf1\.cutoff lfo1\.out@0\.8\+lfo2\.out$`)
	xdo(t, "key", "minus")
	gui.expect(t, `^write cables vcf1\.cutoff lfo1\.out@0\.8\+lfo2\.out@0\.9$`)
	time.Sleep(700 * time.Millisecond)

	x, y = cellAt(vcf2in, lfo2)
	xdo(t, "mousemove", x, y, "click", "1")
	gui.expect(t, `^refused cable:vcf2\.in vcf2\.in: .*more than 4`)

	other := startKukel(t, ctl, "-a", addr, "cable", "vca.mod", "lfo1.out@0.5")
	gui.expect(t, `^show cables vca\.mod lfo1\.out@0\.5$`)
	if err := <-other.done; err != nil {
		t.Fatal(err)
	}
	check := startKukel(t, ctl, "-a", addr, "cables")
	want := map[string]bool{
		"vcf1.cutoff = lfo1.out@0.8+lfo2.out@0.9":        true,
		"vcf2.in = vco1.out+vco2.out+vco3.out+noise.out": true,
		"vca.mod = lfo1.out@0.5":                         true,
	}
	for line := range check.lines {
		if !want[line] {
			t.Errorf("the synth has %q", line)
		}
		delete(want, line)
	}
	for line := range want {
		t.Errorf("the synth lacks %q", line)
	}
}

// answerFile answers Tk's file dialog with that title: the file name
// field gets path (in place of what it had), then Return.
func answerFile(t *testing.T, title, path string) {
	t.Helper()
	win := findWindow(title)
	if win == "" {
		t.Fatalf("no dialog %q", title)
	}
	time.Sleep(400 * time.Millisecond)
	exec.Command("xdotool", "windowfocus", win).Run() // may complain about the focus it finds
	time.Sleep(200 * time.Millisecond)
	xdo(t, "key", "Home", "shift+End", "BackSpace")
	xdo(t, "type", path)
	time.Sleep(200 * time.Millisecond)
	xdo(t, "key", "Return")
	for i := 0; i < 50; i++ {
		if exec.Command("xdotool", "search", "--onlyvisible", "--name", "^"+title+"$").Run() != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestLmwguiFiles uses the Notes tab, Load and Save, and the list of
// synths of lmwgui, with two fake synths whose sockets are where synths
// put theirs ($XDG_RUNTIME_DIR/lmw-synth): typed notes are written, notes
// written elsewhere show, Ctrl+S saves the synth's patch to a file, Ctrl+O
// loads one (and a bad one is refused with the synth's message), and the
// list switches to the other synth.
func TestLmwguiFiles(t *testing.T) {
	display(t)
	if _, err := exec.LookPath("xdotool"); err != nil || os.Getenv("KUKEL_TK_NOCLICK") != "" {
		t.Skip("no xdotool")
	}
	dir, err := filepath.Abs("../../../examples/lmw")
	if err != nil {
		t.Fatal(err)
	}
	ctl := filepath.Join(dir, "lmwctl.kkl")
	tmp := t.TempDir()
	sockets := filepath.Join(tmp, "lmw-synth")
	if err := os.Mkdir(sockets, 0o700); err != nil {
		t.Fatal(err)
	}
	one, two := "unix://"+filepath.Join(sockets, "1-1.sock"), "unix://"+filepath.Join(sockets, "2-1.sock")
	fake1 := startKukel(t, ctl, "-fakeat", one, "setnotes", "one", ",", "serve", "60")
	fake1.expect(t, `^serving `)
	time.Sleep(50 * time.Millisecond) // the second is newer
	fake2 := startKukel(t, ctl, "-fakeat", two, "set", "vcf1.cutoff", "500", ",", "save", filepath.Join(tmp, "five.lmwpatch"), ",", "serve", "60")
	fake2.expect(t, `^serving `)
	if err := os.WriteFile(filepath.Join(tmp, "bad.lmwpatch"), []byte("[nonsense\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gui := startKukelEnv(t, []string{"XDG_RUNTIME_DIR=" + tmp}, filepath.Join(dir, "lmwgui.kkl"),
		"-a", one, "-v", "-tab", "Notes", "-config", filepath.Join(tmp, "lmw-gui.toml"))
	gui.expect(t, `^shown all 51$`)
	gui.expect(t, `^synths `+regexp.QuoteMeta(two)+` `+regexp.QuoteMeta(one)+`$`)
	at := gui.places(t, "synthlist", "notes", "reconnect")
	list, notes, again := at["synthlist"], at["notes"], at["reconnect"]
	time.Sleep(300 * time.Millisecond)
	wx, wy := lmwWindow(t)

	// Notes: typed after the text ("one"), written a second later.
	xdo(t, "mousemove", fmt.Sprint(wx+notes[0]+40), fmt.Sprint(wy+notes[1]+40), "click", "1")
	xdo(t, "key", "ctrl+End")
	xdo(t, "type", " and more")
	gui.expect(t, `^write notes 12 bytes$`)
	other := startKukel(t, ctl, "-a", one, "notes")
	if got := other.expect(t, `^(.*)$`)[1]; got != "one and more" {
		t.Errorf("the synth's notes are %q", got)
	}
	startKukel(t, ctl, "-a", one, "setnotes", "changed elsewhere")
	gui.expect(t, `^show notes 17 bytes$`)

	// Save, Load, and a patch the synth refuses.
	saved := filepath.Join(tmp, "saved.lmwpatch")
	xdo(t, "key", "ctrl+s")
	answerFile(t, "Save the patch", saved)
	gui.expect(t, `^saved saved\.lmwpatch$`)
	text, err := os.ReadFile(saved)
	if err != nil || !strings.Contains(string(text), "changed elsewhere") {
		t.Errorf("the saved patch (%v):\n%s", err, text)
	}
	xdo(t, "key", "ctrl+o")
	answerFile(t, "Load a patch", filepath.Join(tmp, "five.lmwpatch"))
	gui.expect(t, `^loaded five\.lmwpatch$`)
	gui.expect(t, `^show vcf1\.cutoff 500\.0$`)
	xdo(t, "key", "ctrl+o")
	answerFile(t, "Load a patch", filepath.Join(tmp, "bad.lmwpatch"))
	gui.expect(t, `^refused patch patch: `)

	// The other synth, from the list (it is first: the newest).
	time.Sleep(700 * time.Millisecond)
	xdo(t, "mousemove", fmt.Sprint(wx+list[0]+list[2]/2), fmt.Sprint(wy+list[1]+list[3]/2), "click", "1")
	time.Sleep(500 * time.Millisecond)
	xdo(t, "key", "Up", "Return")
	gui.expect(t, `^connected `+regexp.QuoteMeta(two)+`$`)
	gui.expect(t, `^shown all 51$`)

	// Notes typed, and the synth gone before they are written (a second
	// after typing): they are kept, and written once "Connect again" has
	// the synth back.
	time.Sleep(300 * time.Millisecond)
	xdo(t, "mousemove", fmt.Sprint(wx+notes[0]+40), fmt.Sprint(wy+notes[1]+40), "click", "1")
	xdo(t, "type", "kept")
	fake2.cmd.Process.Kill()
	gui.expect(t, `^lost$`)
	os.Remove(strings.TrimPrefix(two, "unix://")) // a killed server leaves its socket
	fake2 = startKukel(t, ctl, "-fakeat", two, "serve", "60")
	fake2.expect(t, `^serving `)
	xdo(t, "mousemove", fmt.Sprint(wx+again[0]+again[2]/2), fmt.Sprint(wy+again[1]+again[3]/2), "click", "1")
	gui.expect(t, `^connected `+regexp.QuoteMeta(two)+`$`)
	gui.expect(t, `^write notes 4 bytes$`)
	check := startKukel(t, ctl, "-a", two, "notes")
	if got := check.expect(t, `^(.*)$`)[1]; got != "kept" {
		t.Errorf("the synth's notes are %q, want the ones typed before it went", got)
	}
}

// TestLmwguiKeyboard plays the keyboard of lmwgui against its own fake
// synth: a click low on C4 is loud, dragging up to D4 plays it softer, the
// computer keys a and e play C4 and D#4 while the keyboard has the focus
// (held: no repeats), and x shifts them an octave up.
func TestLmwguiKeyboard(t *testing.T) {
	display(t)
	if _, err := exec.LookPath("xdotool"); err != nil || os.Getenv("KUKEL_TK_NOCLICK") != "" {
		t.Skip("no xdotool")
	}
	dir, err := filepath.Abs("../../../examples/lmw")
	if err != nil {
		t.Fatal(err)
	}
	gui := startKukel(t, filepath.Join(dir, "lmwgui.kkl"), "-fake", "-v", "-config", filepath.Join(t.TempDir(), "lmw-gui.toml"))
	gui.expect(t, `^shown all 51$`)
	kb := gui.places(t, "keyboard")["keyboard"]
	time.Sleep(300 * time.Millisecond)
	wx, wy := lmwWindow(t)
	// C4 is white key 23 (A0 and B0, then C1 to B3); keys are 17 pixels wide.
	cx, top := wx+kb[0]+1+23*17+8, wy+kb[1]
	xdo(t, "mousemove", fmt.Sprint(cx), fmt.Sprint(top+40), "mousedown", "1")
	gui.expect(t, `^play on 60 (1[01][0-9]|12[0-7])$`)
	xdo(t, "mousemove", fmt.Sprint(cx+17), fmt.Sprint(top+12))
	gui.expect(t, `^play off 60$`)
	gui.expect(t, `^play on 62 ([1-4]?[0-9])$`)
	xdo(t, "mouseup", "1")
	gui.expect(t, `^play off 62$`)

	xdo(t, "keydown", "a", "keydown", "e")
	time.Sleep(700 * time.Millisecond) // long enough for X to repeat them
	xdo(t, "keyup", "a", "keyup", "e")
	for _, want := range []string{"play on 60 100", "play on 63 100", "play off 60", "play off 63"} {
		if line := gui.expect(t, `^(play .*)$`)[1]; line != want {
			t.Fatalf("got %q, want %q; the output:\n%s", line, want, strings.Join(gui.seen, "\n"))
		}
	}
	xdo(t, "key", "x", "a")
	gui.expect(t, `^play on 72 100$`)
	gui.expect(t, `^play off 72$`)
}
