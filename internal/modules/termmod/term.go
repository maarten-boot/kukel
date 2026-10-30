// Package termmod is the native module "term": the terminal a key at a
// time, and its size (TODO.md Q99), for programs that draw the screen
// themselves with ANSI escape codes, such as examples/vi.kkl.
//
//	use term;
//	main() {
//	    term::raw(fn() {
//	        for (local k = term::key(); k != "q"; k = term::key()) print(k, " ");
//	    });
//	}
//
// A key is the character typed ("a", "é", " ") or the name of a key, two or
// more letters: "Enter", "Tab", "Backspace", "Esc", "Up", "Down", "Left",
// "Right", "Home", "End", "PageUp", "PageDown", "Insert", "Delete",
// "F1".."F12", "Ctrl-A".."Ctrl-Z", with "Ctrl-", "Alt-" and "Shift-" before
// the keys a terminal sends them with ("Ctrl-Left", "Shift-Tab").
//
// Keys come from the program's standard input, also when it is not a
// terminal (a pipe, a file: tests write the keys there). Only kukel run
// and exec give a program the terminal itself (native.SetTerminal): an
// interpreter of a Go program reads its standard input as it is, and
// raw changes nothing.
package termmod

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name: "term",
		Doc:  "the terminal a key at a time (raw mode) and its size",
		Funcs: map[string]native.Func{
			"isterm": isterm,
			"size":   size,
			"raw":    raw,
			"key":    key,
		},
	})
}

// escWait is how long a key that starts with Esc waits for the rest of
// its bytes: a terminal sends an arrow key's at once, so an Esc with
// nothing after it within this time is the Esc key.
const escWait = 50 * time.Millisecond

// terminal gives the files of the terminal the program has (nil: none, or
// they are not a terminal).
func terminal(env value.Env) (in, out *os.File) {
	in, out = native.Terminal(env)
	if in == nil || out == nil || !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return nil, nil
	}
	return in, out
}

// term::isterm(): whether the program's standard input and output are a
// terminal.
func isterm(env value.Env, args []value.Value) (value.Value, error) {
	if _, err := native.Check("term::isterm", args, 0, 0); err != nil {
		return value.NilValue, err
	}
	in, _ := terminal(env)
	return value.MakeBool(in != nil), nil
}

// term::size(): [columns, rows] of the terminal; without one, from the
// environment variables COLUMNS and LINES, else [80, 24].
func size(env value.Env, args []value.Value) (value.Value, error) {
	if _, err := native.Check("term::size", args, 0, 0); err != nil {
		return value.NilValue, err
	}
	w, h := 0, 0
	if _, out := terminal(env); out != nil {
		w, h, _ = term.GetSize(int(out.Fd()))
	}
	if w <= 0 || h <= 0 {
		w, h = envSize("COLUMNS", 80), envSize("LINES", 24)
	}
	return value.MakeVector([]value.Value{value.MakeInt(int64(w)), value.MakeInt(int64(h))}), nil
}

func envSize(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 && n < 100000 {
		return n
	}
	return def
}

// term::raw(f): calls f with the terminal in raw mode (each key comes at
// once, nothing is echoed, Ctrl-C is a key) and gives its result; the
// terminal is as it was after, also when f throws or the program exits.
// Without a terminal (or already raw) it only calls f.
func raw(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("term::raw", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	switch a.V[0].Kind() {
	case value.Function, value.Closure, value.Builtin, value.BoundMethod:
	default:
		return value.NilValue, a.TypeError(0, "a function")
	}
	k := keysOf(env)
	if in, out := terminal(env); in != nil && !k.raw {
		native.Flush(env)
		old, err := term.MakeRaw(int(in.Fd()))
		if err != nil {
			return value.NilValue, a.Errorf(value.IOError, "the terminal cannot be made raw: %s", native.IOMessage(err))
		}
		restoreVT := enableVT(out)
		k.raw = true
		defer func() {
			native.Flush(env) // what was drawn, before the terminal is cooked again
			restoreVT()
			term.Restore(int(in.Fd()), old)
			k.raw = false
		}()
	}
	return env.Call(a.V[0], nil)
}

// term::key([timeout]): the next key; nil at the end of the input, or when
// no key came within timeout seconds.
func key(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("term::key", args, 0, 1)
	if err != nil {
		return value.NilValue, err
	}
	wait := time.Duration(-1)
	if a.Has(0) {
		if wait, err = a.Seconds(0, "a usable timeout"); err != nil {
			return value.NilValue, err
		}
	}
	k := keysOf(env)
	b, ok, err := k.next(env, wait)
	if err == nil && ok {
		var name string
		if name, err = k.decode(env, b); err == nil {
			return value.MakeString(name), nil
		}
	}
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		if _, cancelled := err.(*value.Error); !cancelled && err != env.Err() {
			return value.NilValue, a.Errorf(value.IOError, "%s", native.IOMessage(err))
		}
		return value.NilValue, err
	}
	return value.NilValue, nil
}

type keysKey struct{}

// keys reads the bytes of keys from an interpreter's standard input. A
// read that must wait runs in a goroutine, so that a timeout or the
// program's cancellation can end the wait; the byte it reads later is the
// next one taken, nothing is lost.
type keys struct {
	in      *bufio.Reader
	want    chan struct{}   // asks the goroutine for a byte
	got     chan readResult // its answer
	pending bool            // a byte was asked for and not taken yet
	back    []byte          // bytes read ahead that start the next key
	raw     bool            // inside term::raw with a terminal
}

type readResult struct {
	b   byte
	err error
}

func keysOf(env value.Env) *keys {
	return env.Local(keysKey{}, func() any { return &keys{in: env.Stdin()} }).(*keys)
}

// next gives the next byte, waiting at most wait (forever when negative):
// false when none came in time.
func (k *keys) next(env value.Env, wait time.Duration) (byte, bool, error) {
	if len(k.back) > 0 {
		b := k.back[0]
		k.back = k.back[1:]
		return b, true, nil
	}
	if !k.pending && k.in.Buffered() > 0 {
		b, err := k.in.ReadByte()
		return b, err == nil, err
	}
	if !k.pending {
		if k.want == nil {
			k.want, k.got = make(chan struct{}), make(chan readResult, 1)
			go func() {
				for range k.want {
					b, err := k.in.ReadByte()
					k.got <- readResult{b, err}
				}
			}()
		}
		k.want <- struct{}{}
		k.pending = true
	}
	var timeout <-chan time.Time
	if wait >= 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		timeout = t.C
	}
	w := native.Wait(env) // writes out what the program drew first
	defer w.Done()
	select {
	case r := <-k.got:
		k.pending = false
		return r.b, r.err == nil, r.err
	case <-timeout:
		return 0, false, nil
	case <-env.Done():
		return 0, false, env.Err()
	}
}

// more gives the next byte of a key that started with b, if it comes
// within escWait.
func (k *keys) more(env value.Env) (byte, bool, error) {
	b, ok, err := k.next(env, escWait)
	if err == io.EOF {
		return 0, false, nil
	}
	return b, ok, err
}

// decode gives the name of the key whose first byte is b.
func (k *keys) decode(env value.Env, b byte) (string, error) {
	switch {
	case b == 0x1b:
		c, ok, err := k.more(env)
		switch {
		case err != nil:
			return "", err
		case !ok:
			return "Esc", nil
		case c == '[':
			return k.csi(env)
		case c == 'O':
			return k.ss3(env)
		}
		k.back = append(k.back, c) // Esc, then another key (typed fast, or in a file of keys)
		return "Esc", nil
	case b == '\r':
		if !k.pending && len(k.back) == 0 && k.in.Buffered() > 0 {
			if c, _ := k.in.Peek(1); c[0] == '\n' {
				k.in.ReadByte() // a line end "\r\n" in a file of keys is one Enter
			}
		}
		return "Enter", nil
	case b == '\n':
		return "Enter", nil
	case b == '\t':
		return "Tab", nil
	case b == 0x7f || b == 0x08:
		return "Backspace", nil
	case b == 0:
		return "Ctrl-Space", nil
	case b < 0x1b:
		return "Ctrl-" + string(rune('A'+b-1)), nil
	case b < 0x20:
		return "Ctrl-" + string(`\]^_`[b-0x1c]), nil
	case b < utf8.RuneSelf:
		return string(rune(b)), nil
	}
	// The first byte of a character in UTF-8: the others follow at once.
	n := 1
	switch {
	case b&0xe0 == 0xc0:
		n = 2
	case b&0xf0 == 0xe0:
		n = 3
	case b&0xf8 == 0xf0:
		n = 4
	}
	s := []byte{b}
	for len(s) < n {
		c, ok, err := k.more(env)
		if err != nil {
			return "", err
		}
		if !ok {
			break
		}
		if c&0xc0 != 0x80 {
			k.back = append(k.back, c)
			break
		}
		s = append(s, c)
	}
	if !utf8.Valid(s) {
		k.back = append(s[1:], k.back...)
		s = s[:1] // a byte that is not UTF-8 is a key of its own
	}
	return string(s), nil
}

// csiKeys are the keys of "Esc [ ... final": by the final byte, and for
// "~" by the number.
var csiKeys = map[string]string{
	"A": "Up", "B": "Down", "C": "Right", "D": "Left", "H": "Home", "F": "End", "Z": "Shift-Tab",
	"P": "F1", "Q": "F2", "R": "F3", "S": "F4",
	"1~": "Home", "7~": "Home", "4~": "End", "8~": "End", "2~": "Insert", "3~": "Delete",
	"5~": "PageUp", "6~": "PageDown",
	"11~": "F1", "12~": "F2", "13~": "F3", "14~": "F4", "15~": "F5", "17~": "F6", "18~": "F7",
	"19~": "F8", "20~": "F9", "21~": "F10", "23~": "F11", "24~": "F12",
}

// csi decodes the rest of "Esc [": parameters, then a final byte. A
// second parameter is the modifiers (1 + 1 Shift + 2 Alt + 4 Ctrl). An
// unknown sequence is given as it is, "Esc" in front ("Esc[99z").
func (k *keys) csi(env value.Env) (string, error) {
	var params []byte
	for len(params) < 16 {
		c, ok, err := k.more(env)
		if err != nil {
			return "", err
		}
		if !ok {
			return "Esc[" + string(params), nil
		}
		if c < 0x30 || c > 0x3f { // the final byte
			nums := strings.Split(string(params), ";")
			code := string(c)
			if c == '~' {
				code = nums[0] + "~"
			}
			name, known := csiKeys[code]
			if !known {
				return "Esc[" + string(params) + string(c), nil
			}
			if len(nums) == 2 {
				if m, err := strconv.Atoi(nums[1]); err == nil && m >= 2 && m <= 8 {
					name = modifiers(m-1) + name
				}
			}
			return name, nil
		}
		params = append(params, c)
	}
	return "Esc[" + string(params), nil
}

// modifiers names the keys held with another: bits 1 Shift, 2 Alt, 4 Ctrl.
func modifiers(m int) string {
	s := ""
	if m&4 != 0 {
		s += "Ctrl-"
	}
	if m&2 != 0 {
		s += "Alt-"
	}
	if m&1 != 0 {
		s += "Shift-"
	}
	return s
}

// ss3 decodes the rest of "Esc O" (the arrow keys of some terminals, and
// F1 to F4). Anything else is Esc, then O and that key: Esc and O typed
// fast (vi's Esc, then O for a new line above), or in a file of keys.
func (k *keys) ss3(env value.Env) (string, error) {
	c, ok, err := k.more(env)
	if err != nil {
		return "", err
	}
	if ok && strings.IndexByte("ABCDHFPQRS", c) >= 0 {
		return csiKeys[string(c)], nil
	}
	k.back = append(k.back, 'O')
	if ok {
		k.back = append(k.back, c)
	}
	return "Esc", nil
}
