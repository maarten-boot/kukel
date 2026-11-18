//go:build tk

package tkmod_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const colorsProgram = `
use tk;
use events;

main() {
    println(tk::colors());
    tk::setcolors({"background": "#ffffff", "foreground": "black"});
    tk::setcolors({"select": "#1a4fa0", "selectforeground": "white", "field": "#ffffff"});
    println(tk::colors());
    local w = new tk::Window("Colours");
    local f = new tk::Frame(w, {"background": "#e3f0ff", "padding": 4});
    f->pack();
    local l = new tk::Label(f, "VCF 1", {"background": "#e3f0ff", "foreground": "#000000"});
    l->pack();
    println(f->configure({"background": "light blue"}) == f, l->configure({"foreground": "#123"}) == l);
    local t = new tk::Text(w, "x", {"height": 2});
    local b = new tk::Listbox(w, ["a"], {"height": 2});
    t->pack();
    b->pack();
    // Menus take the palette, made before it changes or after.
    local bar = new tk::Menu(w);
    bar->submenu("File")->check("Big", nil);
    tk::setcolors({"active": "#e8e8e8", "disabled": "#595959", "trough": "#767676", "border": "#767676"});
    new tk::Menu(w, {"popup": true})->item("Copy", fn() {});
    tk::settheme(tk::theme());   // the palette stays
    println(len(tk::colors()));
    local tests = [
        fn() { tk::setcolors({"bogus": "red"}); },
        fn() { tk::setcolors({"field": "nocolour"}); },
        fn() { tk::setcolors({"field": 3}); },
        fn() { tk::setcolors(1); },
        fn() { tk::setcolors({1: "red"}); },
        fn() { new tk::Frame(w, {"background": "nocolour"}); },
        fn() { new tk::Label(w, "x", {"foreground": "#12345"}); },
        fn() { new tk::Button(w, "x", {"background": "red"}); },
        fn() { tk::colors(1); },
    ];
    for (local f in tests) {
        try { f(); } catch (e) { println(e->kind + ":", e->message); }
    }
    println(tk::colors()["field"]);   // a refused palette changes nothing
    events::after(0.2, fn() { w->close(); });
    events::run();
}
`

const colorsWant = `{}
{"background": "#ffffff", "foreground": "black", "field": "#ffffff", "select": "#1a4fa0", "selectforeground": "white"}
true true
9
ValueError: tk::setcolors: unknown colour "bogus" (the colours are background, foreground, field, select, selectforeground, trough, border, active, disabled)
TkError: tk::setcolors: colour field: unknown color name "nocolour"
ValueError: tk::setcolors: colour field must be a colour, such as "red" or "#ff8000", not 3
TypeError: tk::setcolors: argument 1 must be a map of colours, not int
ValueError: tk::setcolors: unknown colour 1 (the colours are background, foreground, field, select, selectforeground, trough, border, active, disabled)
TkError: tk::Frame::Frame: unknown color name "nocolour"
ValueError: tk::Label::Label: option foreground must be a colour, such as "red" or "#ff8000", not "#12345"
ValueError: tk::Button::Button: unknown option "background" (the options are width, padding, state, font, image and compound)
ArgumentError: tk::colors expects 0 arguments, got 1
#ffffff
`

// TestColors runs its program in a process of its own: the palette of
// tk::setcolors and tk::colors (also on menus), and the colour options of
// frames and labels.
func TestColors(t *testing.T) {
	display(t)
	file := filepath.Join(t.TempDir(), "colors.kkl")
	if err := os.WriteFile(file, []byte(colorsProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "KUKEL_TK_EXAMPLE="+file, "KUKEL_TK_ARG=self")
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
			t.Fatalf("%v\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("did not end\n%s", out.String())
	}
	if got := out.String(); got != colorsWant {
		t.Errorf("got:\n%s\nwant:\n%s", got, colorsWant)
		g, w := strings.Split(got, "\n"), strings.Split(colorsWant, "\n")
		for i := range min(len(g), len(w)) {
			if g[i] != w[i] {
				t.Errorf("first difference, line %d:\n got %s\nwant %s", i+1, g[i], w[i])
				break
			}
		}
	}
}
