//go:build tk

package tkmod_test

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	_ "github.com/maarten-boot/kukel/internal/modules/eventsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/tkmod"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

// mainFuncs are run by TestMain on the main goroutine: the only one that
// may use Tk.
var mainFuncs = make(chan func())

func TestMain(m *testing.M) {
	done := make(chan int)
	go func() { done <- m.Run() }()
	for {
		select {
		case f := <-mainFuncs:
			f()
		case code := <-done:
			os.Exit(code)
		}
	}
}

// onMain runs f on the main goroutine and waits for it. f must not call
// t.Fatal or t.Skip (they would end the main goroutine).
func onMain(f func()) {
	done := make(chan struct{})
	mainFuncs <- func() { defer close(done); f() }
	<-done
}

// runProgram compiles a Kukel program and runs it with args, on the main
// goroutine or (onmain false) on the test's own, giving its output.
func runProgram(t *testing.T, src string, onmain bool, args ...string) (string, error) {
	f, err := parser.ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	m, err := vm.New(prog, builtins.Lookup, &out, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if onmain {
		onMain(func() { _, err = m.Run(args) })
	} else {
		_, err = m.Run(args)
	}
	return out.String(), err
}

// display makes sure there is an X display: $DISPLAY, or an Xvfb of our
// own; without either the test is skipped.
func display(t *testing.T) {
	if os.Getenv("DISPLAY") != "" {
		return
	}
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("no display and no Xvfb")
	}
	// Xvfb picks a free display number and writes it to file descriptor 3.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	x := exec.Command("Xvfb", "-displayfd", "3", "-screen", "0", "800x600x24")
	x.ExtraFiles = []*os.File{w}
	err = x.Start()
	w.Close()
	if err != nil {
		t.Skip("Xvfb does not start:", err)
	}
	// SIGTERM, not a kill: Xvfb then removes its socket and lock file.
	t.Cleanup(func() { x.Process.Signal(syscall.SIGTERM); x.Wait() })
	number := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(r).ReadString('\n')
		number <- strings.TrimSpace(line)
	}()
	select {
	case n := <-number:
		if n == "" {
			t.Skip("Xvfb did not come up")
		}
		t.Setenv("DISPLAY", ":"+n)
	case <-time.After(10 * time.Second):
		t.Skip("Xvfb did not come up")
	}
}

const program = `use tk;
use events;

main(args) {
    local clicks = args[0] == "clicks";
    println(tk::available());
    local w = new tk::Window("TkTest"), n = 0;
    local label = new tk::Label(w, "0");
    local button = new tk::Button(w, "Click");
    local entry = new tk::Entry(w, "[puts hi] {x} $y \\ end");
    button->onclick(fn() {
        n++;
        label->settext(str(n));
        entry->focus(); // a themed button takes the focus when clicked
        if (n == 3) throw new Error("three");
    });
    local guard = clicks ? events::after(20, fn() { println("no Enter: timed out"); w->close(); }) : nil;
    entry->onenter(fn(text) {
        println("enter:", text, n);
        guard->cancel();
        w->close();
    });
    label->pack();
    button->pack({"side": "top", "padx": 4, "pady": 2});
    entry->pack({"fill": "x", "expand": true})->focus();
    println(w, label, button, entry);
    println(button->invoke(), label->text(), entry->text());
    for (local f in [fn() { label->pack({"side": "middle"}); }, fn() { label->pack({"colour": 1}); },
                     fn() { label->pack({"padx": -1}); }, fn() { new tk::Label(5, "x"); },
                     fn() { button->onclick(5); }, fn() { new tk::Window(1); }])
        try { f(); } catch (e) { println(e->kind + ":", e->message); }
    local second = new tk::Window("Second");
    new tk::Label(second, "in the second window")->pack();
    second->settitle("Second!");
    println(second, second->title());
    second->close();
    second->close();
    println(second, second->closed());
    try { new tk::Label(second, "x"); } catch (e) { println(e->kind + ":", e->message); }

    // Layout: frames, grid and configure, in a window of their own.
    local form = new tk::Window("Form");
    local f = new tk::Frame(form, {"padding": [4, 2], "relief": "groove", "borderwidth": 2});
    f->pack({"fill": "both", "expand": true});
    local name = new tk::Label(f, "Name:", {"anchor": "w", "width": 8});
    local field = new tk::Entry(f, {"width": 20});
    local pw = new tk::Entry(f, "secret", {"show": "*"});
    name->grid({"row": 0, "column": 0, "sticky": "w"});
    field->grid({"row": 0, "column": 1, "sticky": "ew", "padx": 2});
    pw->grid({"row": 1, "column": 0, "columnspan": 2, "sticky": "nsew"});
    println(f->columnconfigure(1, {"weight": 1, "minsize": 50, "uniform": "a"})->rowconfigure(0, {"weight": 0}));
    println(name->configure({"state": "disabled", "justify": "right", "padding": 3}), field->configure({"state": "readonly"}), pw, pw->text());
    for (local g in [fn() { new tk::Label(f, "x")->pack(); },
                     fn() { name->configure({"text": "x"}); },
                     fn() { name->configure({"state": "readonly"}); },
                     fn() { field->configure({"show": "**"}); },
                     fn() { f->configure({"padding": [1, 2, 3]}); },
                     fn() { name->grid({"sticky": "nn"}); },
                     fn() { name->grid({"rowspan": 0}); },
                     fn() { f->columnconfigure(-1, {}); },
                     fn() { f->rowconfigure(0, {"uniform": "a b"}); },
                     fn() { name->bind("Key", fn(e) {}); },
                     fn() { name->bind("<Nonsense>", fn(e) {}); },
                     fn() { name->bind("<Key>", 1); },
                     fn() { new tk::Label(name, "x"); },
                     fn() { new tk::Frame(f, "x"); },
                     fn() { new tk::Entry(f, "a", {"colour": 1}); }])
        try { g(); } catch (e) { println(e->kind + ":", e->message); }
    form->close();
    try { f->grid(); } catch (e) { println(e->kind + ":", e->message); }

    // Text, list box, check and radio buttons, in a window of their own.
    local box = new tk::Window("Choices");
    local t = new tk::Text(box, "line 1\n[puts x] {y} $z \\", {"width": 30, "height": 4, "wrap": "word", "scroll": "y"});
    t->pack({"fill": "both", "expand": true});
    t->append("\nmore");
    println(t, t->text() == "line 1\n[puts x] {y} $z \\\nmore");
    t->configure({"state": "disabled"});
    println(t->settext("set while disabled")->append("!")->text());
    local l = new tk::Listbox(box, ["one", "two [x]", "{three}", "$four"], {"height": 3, "selectmode": "extended", "scroll": "both", "state": "disabled"});
    l->pack();
    println(l, l->items(), l->selected());
    println(l->setitems(["a", "b", "c"])->setselected([0, 2])->selected(), l);
    local c = new tk::Checkbutton(box, "Bold");
    c->pack();
    println(c->checked(), c->setchecked(true)->checked(), c);
    local g = new tk::Radiogroup("green");
    local r1 = new tk::Radiobutton(box, "Red", g, "red"), r2 = new tk::Radiobutton(box, "Green", g, "green");
    r1->pack();
    r2->pack({"side": "left"});
    println(g, g->value());
    println(g->setvalue("red")->value(), g->setvalue(nil)->value(), g, r1, r2);
    for (local h in [fn() { t->configure({"scroll": "x"}); },
                     fn() { new tk::Text(box, {"scroll": "z"}); },
                     fn() { new tk::Text(box, {"colour": 1}); },
                     fn() { l->setselected([5]); },
                     fn() { l->setitems(["a", 1]); },
                     fn() { new tk::Listbox(box, "x"); },
                     fn() { c->setchecked(1); },
                     fn() { new tk::Radiobutton(box, "x", g, ""); },
                     fn() { new tk::Radiobutton(box, "x", c, "v"); },
                     fn() { new tk::Radiogroup(""); }])
        try { h(); } catch (e) { println(e->kind + ":", e->message); }
    box->close();
    try { c->checked(); } catch (e) { println(e->kind + ":", e->message); }
    println(g->value()); // a group outlives the window of its buttons

    // What a user does: one widget per window, for click() to find.
    local check = new tk::Checkbutton(new tk::Window("Check"), "Check me");
    check->pack({"padx": 20, "pady": 20});
    check->onchange(fn(on) { println("check:", on, check->checked()); });
    local list = new tk::Listbox(new tk::Window("List"), ["a", "b", "c"], {"height": 3});
    list->pack();
    list->onselect(fn(lines) { println("select:", lines, list->selected()); });
    local radios = new tk::Radiogroup();
    radios->onchange(fn(v) { println("radio:", v, radios->value()); });
    new tk::Radiobutton(new tk::Window("Radio"), "Pick me", radios, "picked")->pack({"padx": 20, "pady": 20});

    // bind: the clicks on the button; a second bind replaces the handler.
    local bound = [];
    button->bind("<Button-1>", fn(e) { push(bound, "old"); });
    button->bind("<Button-1>", fn(e) { push(bound, e["x"] >= 0 && e["key"] == nil && e["width"] == nil); });
    events::onerror(fn(e) { println("handler:", e->message); });
    if (!clicks) // drive it with the program's own calls
        events::after(0.05, fn() {
            try { button->invoke(); button->invoke(); } catch (e) { println("invoke:", e->message); }
            w->close();
        });
    events::run();
    println(w, n, bound);
}
`

// Off the main thread tk refuses to start (Go would destroy the thread Tk
// is on when its goroutine ends, and the process would crash).
func TestOffMainThread(t *testing.T) {
	got, err := runProgram(t, `use tk;
main() {
    println(tk::available());
    try { new tk::Window("x"); } catch (e) { println(e->kind + ":", e->message); }
}
`, false)
	want := "false\nValueError: tk::Window::Window: tk works only on the main thread of the process (not in a worker; a Go program that embeds kukel runs the interpreter in its main goroutine)\n"
	if err != nil || got != want {
		t.Errorf("%v; got:\n%s\nwant:\n%s", err, got, want)
	}
}

func TestTk(t *testing.T) {
	display(t)
	clicks := "self"
	if _, err := exec.LookPath("xdotool"); err == nil && os.Getenv("KUKEL_TK_NOCLICK") == "" {
		clicks = "clicks"
	}
	clicked := make(chan error, 1)
	if clicks == "clicks" {
		go func() { clicked <- click(t) }()
	}
	got, err := runProgram(t, program, true, clicks)
	if clicks == "clicks" {
		if cerr := <-clicked; cerr != nil {
			t.Errorf("xdotool: %v", cerr)
		}
	}
	// The end differs: the program closes the window, or the Enter key does.
	common := `true
tk::Window("TkTest") tk::Label("0") tk::Button("Click") tk::Entry("[puts hi] {x} $y \\ end")
nil 1 [puts hi] {x} $y \ end
ValueError: tk::Label::pack: option side: "middle" is not one of top, bottom, left, right
ValueError: tk::Label::pack: unknown option "colour" (the options are side, fill, expand, padx and pady)
ValueError: tk::Label::pack: option padx must be a number of pixels (0 to 10000), not -1
TypeError: tk::Label::Label: argument 1 must be a tk::Window or tk::Frame, not int
TypeError: tk::Button::onclick: argument 1 must be a function or nil, not int
TypeError: tk::Window::Window: argument 1 must be a string, not int
tk::Window("Second!") Second!
tk::Window("Second!", closed) true
ValueError: tk::Label::Label: the window is closed
tk::Frame
tk::Label("Name:") tk::Entry("") tk::Entry("secret") secret
ValueError: tk::Label::pack: cannot pack: grid already places the widgets beside it (use one of pack and grid in each window or frame)
ValueError: tk::Label::configure: unknown option "text" (the options are width, anchor, justify, wraplength, padding and state)
ValueError: tk::Label::configure: option state: "readonly" is not one of normal, disabled
ValueError: tk::Entry::configure: option show must be one character, or "" to show the text, not "**"
ValueError: tk::Frame::configure: option padding must be a number of pixels (0 to 10000) or a vector of 2 or 4 of them, not [1, 2, 3]
ValueError: tk::Label::grid: option sticky must be a string of the sides n, s, e and w (such as "nsew" or "w"), not "nn"
ValueError: tk::Label::grid: option rowspan must be a number of rows or columns (1 to 10000), not 0
ValueError: tk::Frame::columnconfigure: column -1 is not from 0 to 10000
ValueError: tk::Frame::rowconfigure: option uniform must be a name of letters, digits and _ ("" for none), not "a b"
ValueError: tk::Label::bind: "Key" is not an event sequence such as "<Key>", "<Button-1>" or "<Control-Key-s>"
TkError: tk::Label::bind: bad event type or keysym "Nonsense"
TypeError: tk::Label::bind: argument 2 must be a function or nil, not int
TypeError: tk::Label::Label: argument 1 must be a tk::Window or tk::Frame, not tk::Label
TypeError: tk::Frame::Frame: argument 2 must be a map of options, not string
ValueError: tk::Entry::Entry: unknown option "colour" (the options are width, justify, show and state)
ValueError: tk::Frame::grid: the window is closed
tk::Text true
set while disabled!
tk::Listbox(4 lines) ["one", "two [x]", "{three}", "$four"] []
[0, 2] tk::Listbox(3 lines)
false true tk::Checkbutton("Bold", true)
tk::Radiogroup("green") green
red nil tk::Radiogroup(nil) tk::Radiobutton("Red", "red") tk::Radiobutton("Green", "green")
ValueError: tk::Text::configure: option scroll can only be given to the constructor
ValueError: tk::Text::Text: option scroll: "z" is not one of none, x, y, both
ValueError: tk::Text::Text: unknown option "colour" (the options are width, height, wrap, state and scroll)
ValueError: tk::Listbox::setselected: 5 is not the index of a line (0 to 2)
TypeError: tk::Listbox::setitems: argument 1: the lines must be strings, not int
TypeError: tk::Listbox::Listbox: argument 2 must be a vector of strings, not string
TypeError: tk::Checkbutton::setchecked: argument 1 must be true or false, not int
ValueError: tk::Radiobutton::Radiobutton: the value of a radio button cannot be ""
TypeError: tk::Radiobutton::Radiobutton: argument 3 must be a tk::Radiogroup, not tk::Checkbutton
ValueError: tk::Radiogroup::Radiogroup: the value of a radio button cannot be ""
ValueError: tk::Checkbutton::checked: the window is closed
nil
`
	end := map[string]string{
		"self":   "invoke: three\n",
		"clicks": "check: true true\nselect: [1] [1]\nradio: picked picked\nhandler: three\nenter: [puts hi] {x} $y \\ end 3\n",
	}[clicks]
	bound := map[string]string{"self": "[]", "clicks": "[true, true]"}[clicks]
	want := common + end + "tk::Window(\"TkTest\", closed) 3 " + bound + "\n"
	if err != nil || got != want {
		t.Errorf("%s: %v; got:\n%s\nwant:\n%s", clicks, err, got, want)
	}
}

// click clicks the check button, the middle line of the list box and the
// radio button of the windows Check, List and Radio, then the button of
// the window TkTest twice, and presses Enter (the click handler gave the
// entry the focus back).
func click(t *testing.T) error {
	wins := map[string]string{}
	for _, name := range []string{"TkTest", "Check", "List", "Radio"} {
		for i := 0; i < 100 && wins[name] == ""; i++ {
			out, _ := exec.Command("xdotool", "search", "--name", "^"+name+"$").Output()
			wins[name] = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
			time.Sleep(100 * time.Millisecond)
		}
		if wins[name] == "" {
			return fmt.Errorf("no window %s", name)
		}
	}
	// Without a window manager all windows are at 0,0: move them apart.
	for name, at := range map[string][2]int{"Check": {300, 0}, "List": {0, 300}, "Radio": {300, 300}} {
		if err := exec.Command("xdotool", "windowmove", wins[name], fmt.Sprint(at[0]), fmt.Sprint(at[1])).Run(); err != nil {
			return err
		}
	}
	time.Sleep(300 * time.Millisecond)
	for _, name := range []string{"Check", "List", "Radio", "TkTest", "TkTest"} { // one TkTest click was invoke()
		if err := clickCenter(wins[name]); err != nil {
			return err
		}
	}
	time.Sleep(200 * time.Millisecond)
	// Enter closes the window, often before xdotool sends the key's release
	// (which then fails): whether Enter arrived shows in the output.
	exec.Command("xdotool", "key", "--window", wins["TkTest"], "Return").Run()
	return nil
}

// clickCenter clicks the middle of window win.
func clickCenter(win string) error {
	geo, err := exec.Command("xdotool", "getwindowgeometry", "--shell", win).Output()
	if err != nil {
		return err
	}
	var x, y, w, h int
	for _, line := range strings.Split(string(geo), "\n") {
		fmt.Sscanf(line, "X=%d", &x)
		fmt.Sscanf(line, "Y=%d", &y)
		fmt.Sscanf(line, "WIDTH=%d", &w)
		fmt.Sscanf(line, "HEIGHT=%d", &h)
	}
	if err := exec.Command("xdotool", "mousemove", fmt.Sprint(x+w/2), fmt.Sprint(y+h/2), "click", "1").Run(); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}
