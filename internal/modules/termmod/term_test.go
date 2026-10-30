package termmod_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/maarten-boot/kukel"
)

// run runs main of src with stdin as standard input and gives what it
// printed.
func run(t *testing.T, src string, stdin io.Reader) (string, error) {
	t.Helper()
	p, err := kukel.CompileSource("t.kkl", src, kukel.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	in, err := kukel.New(p, kukel.Options{Stdout: &out, Stdin: stdin})
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	_, err = in.Run()
	return out.String(), err
}

const printKeys = `use term;
main() { for (local k = term::key(); k != nil; k = term::key()) print("[" + k + "]"); }`

func TestKeys(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a\xc3\xa9 ", "[a][é][ ]"},
		{"\r\n\n\r\t\x7f\x08", "[Enter][Enter][Enter][Tab][Backspace][Backspace]"},
		{"\x01\x1a\x00\x1c\x1f", "[Ctrl-A][Ctrl-Z][Ctrl-Space][Ctrl-\\][Ctrl-_]"},
		{"\x1b[A\x1b[B\x1b[C\x1b[D\x1b[H\x1b[F\x1b[Z", "[Up][Down][Right][Left][Home][End][Shift-Tab]"},
		{"\x1b[2~\x1b[3~\x1b[5~\x1b[6~\x1b[1~\x1b[4~", "[Insert][Delete][PageUp][PageDown][Home][End]"},
		{"\x1bOP\x1b[15~\x1b[24~\x1bOA", "[F1][F5][F12][Up]"},
		{"\x1b[1;5C\x1b[1;2D\x1b[1;3A\x1b[3;5~", "[Ctrl-Right][Shift-Left][Alt-Up][Ctrl-Delete]"},
		{"\x1b:\x1bOa\x1b", "[Esc][:][Esc][O][a][Esc]"}, // Esc then keys typed fast; a lone Esc at the end
		{"\x1b[99z", "[Esc[99z]"},
		{"\xff\xc3a", "[\xff][\xc3][a]"}, // bytes that are not UTF-8 are keys of their own
	} {
		got, err := run(t, printKeys, strings.NewReader(c.in))
		if err != nil || got != c.want {
			t.Errorf("%q: %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// A key that does not come in time gives nil; it is not lost when it
// comes later.
func TestKeyTimeout(t *testing.T) {
	r, w := io.Pipe()
	go func() {
		time.Sleep(300 * time.Millisecond)
		io.WriteString(w, "x")
		w.Close()
	}()
	got, err := run(t, `use term;
main() { print(term::key(0.05), term::key(0.05), term::key(), term::key(), ""); }`, r)
	if err != nil || got != "nil nil x nil " {
		t.Errorf("%q, %v", got, err)
	}
}

func TestRawSizeIsterm(t *testing.T) {
	t.Setenv("COLUMNS", "132")
	t.Setenv("LINES", "50")
	got, err := run(t, `use term;
main() {
    println(term::isterm(), term::size(), term::raw(fn() { return 42; }));
    try { term::raw(1); } catch (e) { println(e->kind, e->message); }
    try { term::raw(fn() { throw new Error("inside"); }); } catch (e) { println(e->message); }
    try { term::key(-1); } catch (e) { println(e->kind, e->message); }
}`, strings.NewReader(""))
	want := "false [132, 50] 42\n" +
		"TypeError term::raw: argument 1 must be a function, not int\n" +
		"inside\n" +
		"ValueError term::key: -1 seconds is not a usable timeout\n"
	if err != nil || got != want {
		t.Errorf("got %q, %v\nwant %q", got, err, want)
	}
	t.Setenv("COLUMNS", "x")
	t.Setenv("LINES", "")
	if got, _ := run(t, "use term;\nmain() { println(term::size()); }", nil); got != "[80, 24]\n" {
		t.Errorf("size without a terminal or variables: %q", got)
	}
}

// A program waiting for a key is cancelled as other waits are.
func TestKeyCancelled(t *testing.T) {
	p, err := kukel.CompileSource("t.kkl", "use term;\nwaitkey() { return term::key(); }\nmain() {}", kukel.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := io.Pipe() // never written
	in, err := kukel.New(p, kukel.Options{Stdin: r})
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = in.CallContext(ctx, "waitkey")
	if e, ok := err.(*kukel.Error); !ok || e.Kind != "CancelledError" || time.Since(start) > 5*time.Second {
		t.Errorf("%v after %v", err, time.Since(start))
	}
}
