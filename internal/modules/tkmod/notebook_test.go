//go:build tk

package tkmod_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const notebookProgram = `
use tk;
use events;

main(args) {
    local w = new tk::Window("Book");
    local nb = new tk::Notebook(w, {"width": 300, "height": 120, "padding": 4});
    nb->pack({"fill": "both", "expand": true});
    println(nb, nb->selected());

    local pages = [];
    for (local name in ["One", "Two", "Three"]) {
        local f = new tk::Frame(nb, {"padding": 8});
        new tk::Label(f, "page " + name)->pack();
        nb->add(f, name);
        push(pages, f);
    }
    println(nb, nb->selected());

    local changes = [], fallback = nil;
    nb->onchange(fn(i) {
        push(changes, i);
        println("changed:", i, nb->selected());
        if (len(changes) == 2) {
            fallback->cancel();
            w->close();
        }
    });
    println(nb->select(2) == nb, nb->selected());   // no handler
    nb->select(pages[1]);
    println(nb->selected());

    local other = new tk::Notebook(w);
    local stray = new tk::Frame(other);
    local tests = [
        fn() { new tk::Label(nb, "x"); },
        fn() { nb->add(new tk::Frame(w), "x"); },
        fn() { nb->add(stray, "x"); },
        fn() { nb->add(pages[0], "again"); },
        fn() { nb->add(new tk::Label(w, "x"), "x"); },
        fn() { nb->select(3); },
        fn() { nb->select(-1); },
        fn() { nb->select(stray); },
        fn() { nb->select("One"); },
        fn() { other->select(0); },
        fn() { new tk::Notebook(w, {"colour": "red"}); },
        fn() { nb->add(new tk::Frame(nb), "a\x0bb"); },
    ];
    for (local f in tests) {
        try { f(); } catch (e) { println(e->kind + ":", e->message); }
    }
    println(other, other->selected());

    nb->select(2);
    if (args[0] == "self") {
        events::after(0.2, fn() { w->close(); });
    } else {
        println("ready");
        fallback = events::after(10, fn() { w->close(); });   // if the clicks do not come
    }
    events::run();
    println(changes, nb->selected(), nb);
}
`

// TestNotebook runs its program in a process of its own: Tk works in one
// interpreter of a process, and TestTk's is another.
func TestNotebook(t *testing.T) {
	display(t)
	mode := "self"
	if _, err := exec.LookPath("xdotool"); err == nil && os.Getenv("KUKEL_TK_NOCLICK") == "" {
		mode = "clicks"
	}
	file := filepath.Join(t.TempDir(), "notebook.kkl")
	if err := os.WriteFile(file, []byte(notebookProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "KUKEL_TK_EXAMPLE="+file, "KUKEL_TK_ARG="+mode)
	out := &output{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if mode == "clicks" {
		if err := clickTabs(out); err != nil {
			t.Errorf("xdotool: %v", err)
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatalf("did not end\n%s", out.String())
	}
	got := out.String()
	want := `tk::Notebook(0 pages) nil
tk::Notebook(3 pages) 0
true 2
1
ValueError: tk::Label::Label: a tk::Notebook holds only tk::Frame pages: make the Label in a frame
ValueError: tk::Notebook::add: the frame was not made in this notebook (new tk::Frame(notebook))
ValueError: tk::Notebook::add: the frame was not made in this notebook (new tk::Frame(notebook))
ValueError: tk::Notebook::add: the frame is a page already
TypeError: tk::Notebook::add: argument 1 must be a tk::Frame, not tk::Label
ValueError: tk::Notebook::select: no page 3 (the notebook has 3)
ValueError: tk::Notebook::select: no page -1 (the notebook has 3)
ValueError: tk::Notebook::select: the frame is not a page of this notebook
TypeError: tk::Notebook::select: argument 1 must be an int or a tk::Frame, not string
ValueError: tk::Notebook::select: no page 0 (the notebook has 0)
ValueError: tk::Notebook::Notebook: unknown option "colour" (the options are width, height and padding)
ValueError: tk::Notebook::add: argument 2 holds the byte 0x0b (Tk takes no NUL, vertical tab or form feed)
tk::Notebook(0 pages) nil
`
	if mode == "self" {
		want += "[] 2 tk::Notebook(3 pages)\n"
	} else {
		// A click on the first tab, then the Right key.
		want += "ready\nchanged: 0 0\nchanged: 1 1\n[0, 1] 1 tk::Notebook(3 pages)\n"
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// clickTabs clicks the first tab of the window Book, once the program is
// ready, and presses the Right key, which shows the next page.
func clickTabs(out *output) error {
	win := findWindow("Book")
	if win == "" {
		return fmt.Errorf("no window Book")
	}
	for i := 0; i < 100 && !strings.Contains(out.String(), "ready"); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	// Where the tabs are depends on the theme: try down the left of the
	// window until a click picks the first tab.
	picked := false
	for y := 4; y <= 60 && !picked; y += 4 {
		if err := exec.Command("xdotool", "mousemove", "--window", win, "28", fmt.Sprint(y), "click", "1").Run(); err != nil {
			return err
		}
		for i := 0; i < 5 && !picked; i++ {
			time.Sleep(60 * time.Millisecond)
			picked = strings.Contains(out.String(), "changed:")
		}
	}
	if !picked {
		return fmt.Errorf("no click picked the first tab")
	}
	time.Sleep(200 * time.Millisecond)
	// Right shows the second tab and the program closes the window, often
	// before xdotool sends the key's release: its error is no failure.
	exec.Command("xdotool", "key", "--window", win, "Right").Run()
	return nil
}
