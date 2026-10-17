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
	// TestExamples runs each example in a process of its own: this one.
	if file := os.Getenv("KUKEL_TK_EXAMPLE"); file != "" {
		os.Exit(runExample(file))
	}
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
use tktest;

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
                     fn() { new tk::Radiogroup(""); },
                     fn() { t->settext("a\x0cb"); },
                     fn() { box->settitle("x\x0by"); },
                     fn() { l->setitems(["\x00"]); }])
        try { h(); } catch (e) { println(e->kind + ":", e->message); }

    // Menus, and what dialogs refuse (click() answers the dialogs).
    local bar = new tk::Menu(box);
    local file = bar->submenu("File");
    file->item("Open", fn() { return "opened"; }, {"accelerator": "Ctrl+O"})->separator()->item("Quit", fn() { throw new Error("quit"); });
    println(bar, file, file->invoke("Open"), file->enable("Open", false));
    try { file->invoke("Quit"); } catch (e) { println("invoke:", e->message); }

    // Check and radio entries; a group shared with radio buttons keeps its
    // value when another button joins it (tk9.0 would reset Tk's variable).
    local view = bar->submenu("View"), size = new tk::Radiogroup("small");
    view->check("Wrap lines", fn(on) { println("wrap:", on); }, true, {"accelerator": "Ctrl+W"});
    view->separator()->radio("Small", size, "small")->radio("Large", size, "large");
    println(view->checked("Wrap lines"), view->invoke("Wrap lines"), view->checked("Wrap lines"), view->setchecked("Wrap lines", true)->checked("Wrap lines"));
    size->onchange(fn(v) { println("size:", v); return v; });
    println(view->invoke("Large"), size->value(), tktest::tkvalue(size));
    new tk::Radiobutton(box, "Large", size, "large")->pack();
    new tk::Radiobutton(box, "Small", size, "small")->pack();
    println(size->value(), tktest::tkvalue(size), new tk::Menu(box, {"popup": true}), view);

    // Fonts (straight to Tk, or through a style for the themed buttons) and
    // themes (clam on X11).
    local big = {"family": "DejaVu Sans", "size": 16, "bold": true};
    new tk::Label(box, "big", {"font": big})->pack();
    new tk::Button(box, "big", {"font": big})->configure({"font": {"italic": true, "underline": true}})->pack();
    new tk::Text(box, "big", {"font": big})->pack();
    println(tk::theme(), len(tk::themes()) > 1, tk::settheme("clam"), tk::theme());
    for (local h in [fn() { new tk::Label(box, "x", {"font": "Helvetica 12"}); },
                     fn() { new tk::Label(box, "x", {"font": {"weight": "bold"}}); },
                     fn() { new tk::Label(box, "x", {"font": {"size": 0}}); },
                     fn() { tk::settheme("fancy"); }])
        try { h(); } catch (e) { println(e->kind + ":", e->message); }
    for (local h in [fn() { new tk::Menu(box); },
                     fn() { file->item("Open", fn() {}); },
                     fn() { file->invoke("Nope"); },
                     fn() { bar->invoke("File"); },
                     fn() { file->item("x", 1); },
                     fn() { file->item("", fn() {}); },
                     fn() { file->item("y", fn() {}, {"accel": "x"}); },
                     fn() { tk::messagebox(1); },
                     fn() { tk::messagebox("x", {"icon": "smile"}); },
                     fn() { tk::openfile({"types": [["Kukel"]]}); },
                     fn() { tk::savefile({"parent": 3}); },
                     fn() { file->invoke(""); },
                     fn() { view->checked("Small"); },
                     fn() { view->popup(1, 1); },
                     fn() { view->radio("x", 1, "v"); },
                     fn() { view->check("y", 5); },
                     fn() { new tk::Menu(box, {"popup": 1}); }])
        try { h(); } catch (e) { println(e->kind + ":", e->message); }
    box->close();
    try { c->checked(); } catch (e) { println(e->kind + ":", e->message); }
    try { file->invoke("Open"); } catch (e) { println(e->kind + ":", e->message); }
    println(g->value()); // a group outlives the window of its buttons

    // A canvas: items, finding them, changing them.
    local draw = new tk::Window("Drawing");
    local cv = new tk::Canvas(draw, {"width": 200, "height": 150, "background": "white"});
    cv->pack();
    local r = cv->rect(10, 10, 60, 40, {"fill": "#ff8000", "outline": "", "width": 2});
    local o = cv->oval(80, 10, 140, 50, {"fill": "light blue"});
    local ln = cv->line([10, 100, 100, 100, 150, 140], {"color": "red", "width": 3, "arrow": "last", "smooth": true});
    local p = cv->polygon([150, 10, 190, 10, 170, 40], {"fill": "green"});
    local tx = cv->text(100, 75, "Hello [x] {y} $z", {"anchor": "center", "color": "blue", "font": {"size": 9}});
    println(cv, cv->items(), [r, o, ln, p, tx]);
    println(cv->itemat(20, 20), cv->itemat(110, 30), cv->itemat(50, 100), cv->itemat(170, 20), cv->itemat(100, 75), cv->itemat(5, 145));
    println(cv->move(r, 5, 5.5)->coords(r), cv->coords(ln));
    cv->setcoords(ln, [0, 0, 10, 10])->configureitem(o, {"fill": "pink", "outline": "black"})->raise(r)->lower(tx);
    println(cv->items(), cv->coords(ln), cv->itemat(20, 20));
    for (local h in [fn() { cv->rect(0, 0, 1, "x"); },
                     fn() { cv->line([1, 2, 3]); },
                     fn() { cv->polygon([1, 2, 3, 4]); },
                     fn() { cv->rect(0, 0, 1, 1, {"color": "red"}); },
                     fn() { cv->oval(0, 0, 1, 1, {"fill": "#12"}); },
                     fn() { cv->line([0, 0, 1, 1], {"color": ""}); },
                     fn() { cv->move(99, 1, 1); },
                     fn() { cv->setcoords(r, 1, 2); },
                     fn() { cv->rect(0, 0, 2e6, 1); },
                     fn() { cv->oval(0, 0, 1, 1, {"fill": "notacolour"}); },
                     fn() { cv->configureitem(o, {"fill": "notacolour"}); },
                     fn() { cv->move(r, 1e6, 0); }])
        try { h(); } catch (e) { println(e->kind + ":", e->message); }
    println(cv, cv->remove(p)->items(), cv->clear()->items());
    draw->close();

    // A rectangle for click() to drag.
    local area = new tk::Canvas(new tk::Window("Canvas"), {"width": 200, "height": 150});
    area->pack();
    area->rect(10, 10, 190, 140, {"fill": "gray"});
    local dragged = nil, last = nil;
    area->bind("<Button-1>", fn(e) { dragged = area->itemat(e["x"], e["y"]); last = e; });
    area->bind("<B1-Motion>", fn(e) { area->move(dragged, e["x"] - last["x"], e["y"] - last["y"]); last = e; });
    area->bind("<ButtonRelease-1>", fn(e) { println("dragged:", dragged, area->coords(dragged)); });

    // What a user does: one widget per window, for click() to find.
    local check = new tk::Checkbutton(new tk::Window("Check"), "Check me");
    check->pack({"padx": 20, "pady": 20});
    check->onchange(fn(on) { println("check:", on, check->checked()); });
    local list = new tk::Listbox(new tk::Window("List"), ["a", "b", "c"], {"height": 3});
    list->pack();
    list->onselect(fn(lines) { println("select:", lines, list->selected()); });
    local radios = new tk::Radiogroup();
    radios->onchange(fn(v) {
        println("radio:", v, radios->value());
        // The dialogs, answered by click(): Return, a file name and Return,
        // Return, Escape (cancelled).
        println("message:", tk::messagebox("Proceed?", {"title": "Ask", "icon": "question", "buttons": "yesno", "detail": "more", "parent": w}));
        println("open:", tk::openfile({"title": "Open", "dir": args[1], "types": [["Kukel", ".kkl"], ["All files", "*"]]}) == args[1] + "/a.kkl");
        println("save:", tk::savefile({"title": "Save", "dir": args[1], "file": "out.txt"}) == args[1] + "/out.txt");
        println("dir:", tk::choosedir({"title": "Dir", "dir": args[1]}));
    });
    new tk::Radiobutton(new tk::Window("Radio"), "Pick me", radios, "picked")->pack({"padx": 20, "pady": 20});
    local popwin = new tk::Window("Popup"), pop = new tk::Menu(popwin, {"popup": true});
    new tk::Label(popwin, "right-click", {"padding": 20})->pack();
    pop->item("Hello", fn() { println("pop-up: Hello"); });
    popwin->bind("<Button-3>", fn(e) { pop->popup(e["xroot"], e["yroot"]); });

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
    try { new tk::Radiogroup(); } catch (e) { println(e->kind + ":", e->message); }
    println(entry, radios->value()); // what is kept after Tk ended
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
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/a.kkl", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := runProgram(t, program, true, clicks, dir)
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
ValueError: tk::Label::configure: unknown option "text" (the options are width, font, anchor, justify, wraplength, padding and state)
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
ValueError: tk::Entry::Entry: unknown option "colour" (the options are width, font, justify, show and state)
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
ValueError: tk::Text::Text: unknown option "colour" (the options are width, height, font, wrap, state and scroll)
ValueError: tk::Listbox::setselected: 5 is not the index of a line (0 to 2)
TypeError: tk::Listbox::setitems: argument 1: the lines must be strings, not int
TypeError: tk::Listbox::Listbox: argument 2 must be a vector of strings, not string
TypeError: tk::Checkbutton::setchecked: argument 1 must be true or false, not int
ValueError: tk::Radiobutton::Radiobutton: the value of a radio button cannot be ""
TypeError: tk::Radiobutton::Radiobutton: argument 3 must be a tk::Radiogroup, not tk::Checkbutton
ValueError: tk::Radiogroup::Radiogroup: the value of a radio button cannot be ""
ValueError: tk::Text::settext: argument 1 holds the byte 0x0c (Tk takes no NUL, vertical tab or form feed)
ValueError: tk::Window::settitle: argument 1 holds the byte 0x0b (Tk takes no NUL, vertical tab or form feed)
ValueError: tk::Listbox::setitems: argument 1: a line holds the byte 0x00 (Tk takes no NUL, vertical tab or form feed)
tk::Menu tk::Menu("File") opened tk::Menu("File")
invoke: quit
wrap: false
true nil false true
size: large
large large large
large large tk::Menu(popup) tk::Menu("View")
clam true nil clam
ValueError: tk::Label::Label: option font must be a map such as {"family": "Helvetica", "size": 12, "bold": true}, not "Helvetica 12"
ValueError: tk::Label::Label: option font: unknown part "weight" (the parts are family, size, bold, italic and underline)
ValueError: tk::Label::Label: option font: size must be a number of points (1 to 500), not 0
ValueError: tk::settheme: there is no theme "fancy" (the themes are clam, alt, default, classic)
ValueError: tk::Menu::Menu: the window has a menu bar already
ValueError: tk::Menu::item: the menu has an entry "Open" already
ValueError: tk::Menu::invoke: the menu has no entry "Nope"
ValueError: tk::Menu::invoke: "File" is a submenu, not an item
TypeError: tk::Menu::item: argument 2 must be a function, not int
ValueError: tk::Menu::item: the label of an entry cannot be ""
ValueError: tk::Menu::item: unknown option "accel" (the options are accelerator)
TypeError: tk::messagebox: argument 1 must be a string, not int
ValueError: tk::messagebox: option icon: "smile" is not one of info, warning, error, question
ValueError: tk::openfile: option types must be a vector of [name, pattern...] vectors of strings, such as [["Kukel", ".kkl"], ["All files", "*"]], not [["Kukel"]]
ValueError: tk::savefile: option parent must be an open tk::Window, not 3
ValueError: tk::Menu::invoke: the menu has no entry ""
ValueError: tk::Menu::checked: "Small" is not a check entry
ValueError: tk::Menu::popup: this is not a pop-up menu (new tk::Menu(window, {"popup": true}))
TypeError: tk::Menu::radio: argument 2 must be a tk::Radiogroup, not int
TypeError: tk::Menu::check: argument 2 must be a function or nil, not int
ValueError: tk::Menu::Menu: option popup must be true or false, not int
ValueError: tk::Checkbutton::checked: the window is closed
ValueError: tk::Menu::invoke: the window is closed
nil
tk::Canvas(5 items) [1, 2, 3, 4, 5] [1, 2, 3, 4, 5]
1 2 3 4 5 nil
[15, 15.5, 65, 45.5] [10, 100, 100, 100, 150, 140]
[5, 2, 3, 4, 1] [0, 0, 10, 10] 1
ValueError: tk::Canvas::rect: argument 4 must be a number from -1e6 to 1e6, not "x"
ValueError: tk::Canvas::line: argument 1 must be a vector of at least 2 points [x1, y1, x2, y2, ...] (numbers from -1e6 to 1e6), not [1, 2, 3]
ValueError: tk::Canvas::polygon: argument 1 must be a vector of at least 3 points [x1, y1, x2, y2, ...] (numbers from -1e6 to 1e6), not [1, 2, 3, 4]
ValueError: tk::Canvas::rect: unknown option "color" (the options are fill, outline and width)
ValueError: tk::Canvas::oval: option fill must be a colour, such as "red" or "#ff8000" (or "" for none), not "#12"
ValueError: tk::Canvas::line: option color must be a colour, such as "red" or "#ff8000", not ""
ValueError: tk::Canvas::move: the canvas has no item 99
ValueError: tk::Canvas::setcoords: a rect takes 4 coordinate arguments, not 2
ValueError: tk::Canvas::rect: argument 3 must be a number from -1e6 to 1e6, not 2e+06
TkError: tk::Canvas::oval: unknown color name "notacolour"
TkError: tk::Canvas::configureitem: unknown color name "notacolour"
ValueError: tk::Canvas::move: the item would leave the canvas (coordinates from -1e6 to 1e6)
tk::Canvas(0 items) [5, 2, 3, 1] []
`
	end := map[string]string{
		"self":   "invoke: three\n",
		"clicks": "check: true true\nselect: [1] [1]\nradio: picked picked\nmessage: yes\nopen: true\nsave: true\ndir: nil\ndragged: 1 [40, 30, 220, 160]\npop-up: Hello\nhandler: three\nenter: [puts hi] {x} $y \\ end 3\n",
	}[clicks]
	bound := map[string]string{"self": "[]", "clicks": "[true, true]"}[clicks]
	picked := map[string]string{"self": "nil", "clicks": "picked"}[clicks]
	want := common + end + "tk::Window(\"TkTest\", closed) 3 " + bound + "\n" +
		"TkError: tk::Radiogroup::Radiogroup: Tk has ended: the first window was closed\n" +
		"tk::Entry(\"[puts hi] {x} $y \\\\ end\") " + picked + "\n"
	if err != nil || got != want {
		t.Errorf("%s: %v; got:\n%s\nwant:\n%s", clicks, err, got, want)
	}
}

// click clicks the check button, the middle line of the list box and the
// radio button of the windows Check, List and Radio, answers the dialogs
// the radio button's handler opens, then clicks the button of the window
// TkTest twice, and presses Enter (the click handler gave the entry the
// focus back).
func click(t *testing.T) error {
	wins := map[string]string{}
	for _, name := range []string{"TkTest", "Check", "List", "Radio", "Canvas", "Popup"} {
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
	for name, at := range map[string][2]int{"Check": {300, 0}, "List": {0, 300}, "Radio": {300, 300}, "Canvas": {540, 300}, "Popup": {540, 0}} {
		if err := exec.Command("xdotool", "windowmove", wins[name], fmt.Sprint(at[0]), fmt.Sprint(at[1])).Run(); err != nil {
			return err
		}
	}
	time.Sleep(300 * time.Millisecond)
	for _, name := range []string{"Check", "List", "Radio"} {
		if err := clickCenter(wins[name]); err != nil {
			return err
		}
	}
	for _, d := range []struct{ title, typed, key string }{{"Ask", "", "Return"}, {"Open", "a.kkl", "Return"}, {"Save", "", "Return"}, {"Dir", "", "Escape"}} {
		if err := answer(d.title, d.typed, d.key); err != nil {
			return err
		}
	}
	if err := drag(wins["Canvas"], 30, 20); err != nil {
		return err
	}
	// Right-click for the pop-up menu, then click its entry, which opens at
	// the pointer.
	x, y, err := center(wins["Popup"])
	if err != nil {
		return err
	}
	for _, step := range [][]string{{"mousemove", fmt.Sprint(x), fmt.Sprint(y), "click", "3"}, {"mousemove", fmt.Sprint(x + 20), fmt.Sprint(y + 10), "click", "1"}} {
		if err := exec.Command("xdotool", step...).Run(); err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
	for i := 0; i < 2; i++ { // one click was invoke()
		if err := clickCenter(wins["TkTest"]); err != nil {
			return err
		}
	}
	time.Sleep(200 * time.Millisecond)
	// Enter closes the window, often before xdotool sends the key's release
	// (which then fails): whether Enter arrived shows in the output.
	exec.Command("xdotool", "key", "--window", wins["TkTest"], "Return").Run()
	return nil
}

// answer answers the dialog with that title: it gives it the focus (no
// window manager does), types typed and presses key.
func answer(title, typed, key string) error {
	var win string
	for i := 0; i < 100 && win == ""; i++ {
		out, _ := exec.Command("xdotool", "search", "--name", "^"+title+"$").Output()
		win = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		time.Sleep(100 * time.Millisecond)
	}
	if win == "" {
		return fmt.Errorf("no dialog %s", title)
	}
	time.Sleep(400 * time.Millisecond)
	exec.Command("xdotool", "windowfocus", win).Run() // may complain about the focus it finds
	time.Sleep(200 * time.Millisecond)
	if typed != "" {
		if err := exec.Command("xdotool", "type", typed).Run(); err != nil {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := exec.Command("xdotool", "key", key).Run(); err != nil {
		return err
	}
	// The next dialog may reuse this window (Tk's file dialogs do): wait
	// until this one is gone.
	for i := 0; i < 50; i++ {
		if exec.Command("xdotool", "search", "--onlyvisible", "--name", "^"+title+"$").Run() != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// drag drags the mouse from the middle of window win by dx, dy, in three
// steps.
func drag(win string, dx, dy int) error {
	x, y, err := center(win)
	if err != nil {
		return err
	}
	steps := [][]string{
		{"mousemove", fmt.Sprint(x), fmt.Sprint(y), "mousedown", "1"},
		{"mousemove", fmt.Sprint(x + dx/3), fmt.Sprint(y + dy/3)},
		{"mousemove", fmt.Sprint(x + dx), fmt.Sprint(y + dy)},
		{"mouseup", "1"},
	}
	for _, step := range steps {
		if err := exec.Command("xdotool", step...).Run(); err != nil {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// center gives the middle of window win on the screen.
func center(win string) (int, int, error) {
	geo, err := exec.Command("xdotool", "getwindowgeometry", "--shell", win).Output()
	if err != nil {
		return 0, 0, err
	}
	var x, y, w, h int
	for _, line := range strings.Split(string(geo), "\n") {
		fmt.Sscanf(line, "X=%d", &x)
		fmt.Sscanf(line, "Y=%d", &y)
		fmt.Sscanf(line, "WIDTH=%d", &w)
		fmt.Sscanf(line, "HEIGHT=%d", &h)
	}
	return x + w/2, y + h/2, nil
}

// clickCenter clicks the middle of window win.
func clickCenter(win string) error {
	x, y, err := center(win)
	if err != nil {
		return err
	}
	if err := exec.Command("xdotool", "mousemove", fmt.Sprint(x), fmt.Sprint(y), "click", "1").Run(); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}
