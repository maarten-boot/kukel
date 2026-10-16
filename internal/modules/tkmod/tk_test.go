//go:build tk

package tkmod_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	for n := 90; n < 110; n++ {
		if _, err := os.Stat(fmt.Sprintf("/tmp/.X11-unix/X%d", n)); err == nil {
			continue // in use
		}
		x := exec.Command("Xvfb", fmt.Sprintf(":%d", n), "-screen", "0", "800x600x24")
		if err := x.Start(); err != nil {
			t.Skip("Xvfb does not start:", err)
		}
		t.Cleanup(func() { x.Process.Kill(); x.Wait() })
		for i := 0; i < 50; i++ {
			if _, err := os.Stat(fmt.Sprintf("/tmp/.X11-unix/X%d", n)); err == nil {
				t.Setenv("DISPLAY", fmt.Sprintf(":%d", n))
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Skip("Xvfb did not come up")
	}
	t.Skip("no free display number")
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
        if (n == 3) throw new Error("three");
    });
    entry->onenter(fn(text) {
        println("enter:", text, n);
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
    events::onerror(fn(e) { println("handler:", e->message); });
    if (!clicks) // drive it with the program's own calls
        events::after(0.05, fn() {
            try { button->invoke(); button->invoke(); } catch (e) { println("invoke:", e->message); }
            w->close();
        });
    events::run();
    println(w, n);
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
TypeError: tk::Label::Label: argument 1 must be a tk::Window, not int
TypeError: tk::Button::onclick: argument 1 must be a function or nil, not int
TypeError: tk::Window::Window: argument 1 must be a string, not int
tk::Window("Second!") Second!
tk::Window("Second!", closed) true
ValueError: tk::Label::Label: the window is closed
`
	end := map[string]string{
		"self":   "invoke: three\n",
		"clicks": "handler: three\nenter: [puts hi] {x} $y \\ end 3\n",
	}[clicks]
	want := common + end + "tk::Window(\"TkTest\", closed) 3\n"
	if err != nil || got != want {
		t.Errorf("%s: %v; got:\n%s\nwant:\n%s", clicks, err, got, want)
	}
}

// click clicks the button of the window TkTest three times, then presses
// Enter (the entry has the focus).
func click(t *testing.T) error {
	var win string
	for i := 0; i < 100 && win == ""; i++ {
		out, _ := exec.Command("xdotool", "search", "--name", "^TkTest$").Output()
		win = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		time.Sleep(100 * time.Millisecond)
	}
	if win == "" {
		return fmt.Errorf("no window TkTest")
	}
	time.Sleep(300 * time.Millisecond)
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
	for i := 0; i < 2; i++ { // the button is in the middle; one click was invoke()
		if err := exec.Command("xdotool", "mousemove", fmt.Sprint(x+w/2), fmt.Sprint(y+h/2), "click", "1").Run(); err != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	return exec.Command("xdotool", "key", "--window", win, "Return").Run()
}
