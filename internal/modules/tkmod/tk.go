//go:build tk

package tkmod

import (
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/modules/eventsmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// TkError is the kind of the exceptions for what Tk cannot do: start
// without a display, mix pack and grid in one container, or anything after
// the first window was closed.
const TkError = "TkError"

// pollEvery is how often events::run() has Tk handle its events.
const pollEvery = 10 * time.Millisecond

// Tk is used from one goroutine only: the main goroutine, locked to the
// main thread. Everything below runs there, also the handlers (inside
// tk.Update, called by the event loop), so there is no locking.
var gui struct {
	env        value.Env // the interpreter that uses Tk
	loop       *eventsmod.Loop
	started    bool
	failed     error // Tk could not start
	ended      bool  // the first window was closed: Tk is gone
	rootUsed   bool  // the first window took Tk's main window
	open       map[*window]bool
	pending    []error // exceptions of handlers, for the poll to pass on
	removePoll func()
}

func init() {
	// The main goroutine (where kukel runs programs) stays on the main
	// thread: Tk on macOS needs to run there.
	runtime.LockOSThread()
	tk.ErrorMode = tk.CollectErrors
	gui.open = map[*window]bool{}
	register()
}

// tkError takes the error Tk collected, if any: Tk's message, without the
// Tcl command tk9.0 puts in front of it.
func tkError() error {
	err := tk.Error
	tk.Error = nil
	if err == nil {
		return nil
	}
	msg, _, _ := strings.Cut(err.Error(), "\n")
	if i := strings.LastIndex(msg, " err="); i >= 0 {
		msg = msg[i+len(" err="):]
	}
	return &tkFailure{msg}
}

type tkFailure struct{ msg string }

func (e *tkFailure) Error() string { return e.msg }

// start starts Tk the first time; it fails off the main thread, for
// another interpreter than the one that started it, without a display, and
// after the first window was closed.
//
// Tk stays on the main thread, where init locked the main goroutine: a
// goroutine that ends while locked to its thread has that thread destroyed
// by Go, and with it Tcl's state for the thread (the process then crashes).
func start(a native.Args, env value.Env) error {
	switch {
	case !onMainThread():
		return a.Errorf(value.ValueError, "tk works only on the main thread of the process (not in a worker; a Go program that embeds kukel runs the interpreter in its main goroutine)")
	case gui.env != nil && gui.env != env:
		return a.Errorf(value.ValueError, "tk works in one interpreter of a process: the first that used it (not in a worker)")
	case gui.failed != nil:
		return a.Errorf(TkError, "%v", gui.failed)
	case gui.ended:
		return a.Errorf(TkError, "Tk has ended: the first window was closed")
	case gui.started:
		return nil
	}
	gui.env = env
	tk.Initialize()
	if err := tkError(); err != nil {
		gui.failed = err
		return a.Errorf(TkError, "%v", err)
	}
	gui.loop = eventsmod.For(env)
	gui.started = true
	return nil
}

// call runs a Kukel handler from Tk; its exception waits for the poll,
// which passes it on to the event loop.
func call(fn value.Value, args ...value.Value) value.Value {
	v, err := gui.env.Call(fn, args)
	if err != nil {
		gui.pending = append(gui.pending, err)
	}
	return v
}

// poll lets Tk handle its events (and so call the handlers) and passes on
// an exception of a handler.
func poll() error {
	if !gui.ended {
		tk.Update()
		tkError() // nothing a program could do about it
	}
	if len(gui.pending) > 0 {
		err := gui.pending[0]
		gui.pending = gui.pending[1:]
		return err
	}
	return nil
}

// ----------------------------------------------------------------------
// What windows and widgets share

// node is the part of a window or widget that bind and the containers'
// methods use.
type node struct {
	w     *tk.Window
	win   *window                // the window it is in (a window: itself)
	binds map[string]value.Value // handlers by event sequence
}

// nodeOf gives the node of a tk object.
func nodeOf(this *value.ObjectObj) *node {
	switch st := this.State.(type) {
	case *window:
		return &st.node
	case *widget:
		return &st.node
	}
	return nil
}

// sequenceRE are the event sequences bind takes: one or more events such
// as <Key>, <Control-Key-s>, <Double-Button-1>, and virtual events <<Name>>.
var sequenceRE = regexp.MustCompile(`^(?:<[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*>|<<[A-Za-z0-9_]+>>)+$`)

// bind sets the handler of the event sequence seq (nil: none). The first
// handler of a sequence adds a Tk binding, after those Tk and the module
// have (so a widget keeps working as before); later ones replace the
// handler it calls.
func (n *node) bind(a native.Args, seq string, f value.Value) error {
	if !sequenceRE.MatchString(seq) {
		return a.Errorf(value.ValueError, "%s is not an event sequence such as \"<Key>\", \"<Button-1>\" or \"<Control-Key-s>\"", value.Quote(seq))
	}
	if _, ok := n.binds[seq]; !ok {
		tk.BindAppend(n.w, seq, tk.Command(func(e *tk.Event) {
			if f := n.binds[seq]; !f.IsNil() {
				call(f, eventMap(e))
			}
		}))
		if err := tkError(); err != nil {
			return a.Errorf(TkError, "%v", err)
		}
		if n.binds == nil {
			n.binds = map[string]value.Value{}
		}
	}
	n.binds[seq] = f
	return nil
}

// eventMap is what a bind handler gets: key and char (key events), x and
// y (the mouse, relative to the widget), delta (the mouse wheel), width
// and height (Configure events). What the event does not have is nil.
func eventMap(e *tk.Event) value.Value {
	m := value.NewMap()
	set := func(k string, v value.Value) { m.Set(value.MakeString(k), v) } // string keys cannot fail
	text := func(s string) value.Value {
		if s == "" || s == "??" {
			return value.NilValue
		}
		return value.MakeString(s)
	}
	number := func(s string) value.Value {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return value.MakeInt(n)
		}
		return value.NilValue
	}
	set("key", text(e.Keysym))
	set("char", text(e.Char))
	set("x", value.MakeInt(int64(e.X)))
	set("y", value.MakeInt(int64(e.Y)))
	set("delta", value.MakeInt(int64(e.Delta)))
	set("width", number(e.Width))
	set("height", number(e.Height))
	return value.MakeMap(m)
}

// ----------------------------------------------------------------------
// Windows

// window is the state of a tk::Window object: Tk's main window (the
// first one), or a toplevel window.
type window struct {
	node
	title   string
	root    bool
	closed  bool
	release func() // of the hold on the event loop
	onclose value.Value
}

func newWindow(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
	a, err := native.Check("tk::Window::Window", args, 0, 1)
	if err != nil {
		return value.NilValue, err
	}
	title := "kukel"
	if a.Has(0) {
		if title, err = a.String(0); err != nil {
			return value.NilValue, err
		}
	}
	if err := start(a, env); err != nil {
		return value.NilValue, err
	}
	win := &window{title: title}
	win.win = win
	if !gui.rootUsed {
		win.w, win.root = tk.App, true
		gui.rootUsed = true
	} else {
		win.w = tk.App.Toplevel().Window
	}
	win.w.WmTitle(title)
	path := win.w.String()
	tk.Bind(win.w, "<Destroy>", tk.Command(func(e *tk.Event) {
		if e.EventWindow != nil && e.EventWindow.String() == path { // not a widget in it
			win.gone()
		}
	}))
	tk.WmProtocol(win.w, "WM_DELETE_WINDOW", tk.Command(func() {
		if !win.onclose.IsNil() {
			if v := call(win.onclose); v.Kind() == value.Bool && !v.AsBool() {
				return // the handler keeps the window open
			}
		}
		tk.Destroy(win.w)
	}))
	if err := tkError(); err != nil {
		return value.NilValue, a.Errorf(TkError, "%v", err)
	}
	win.release = gui.loop.Hold()
	gui.open[win] = true
	if gui.removePoll == nil {
		gui.removePoll = gui.loop.Poll(poll, pollEvery)
	}
	this.State = win
	return value.NilValue, nil
}

// gone is a window that Tk destroyed (closed by the program, or by the
// user). Closing the main window ends Tk, with all windows.
func (win *window) gone() {
	if win.closed {
		return
	}
	win.closed = true
	delete(gui.open, win)
	win.release()
	if win.root {
		gui.ended = true
		for other := range gui.open {
			other.gone()
		}
	}
	if len(gui.open) == 0 && gui.removePoll != nil {
		gui.removePoll()
		gui.removePoll = nil
	}
}

// Window is the class tk::Window.
var Window = &native.Class{Name: "Window"}

// method makes a member function of a window or widget: it checks the
// argument count, refuses an object whose window is closed, and turns
// what Tk could not do into a TkError.
func method(full string, min, max int, fn func(a native.Args, this *value.ObjectObj, n *node) (value.Value, error)) value.NativeMethod {
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		n := nodeOf(this)
		if n.win.closed {
			return value.NilValue, a.Errorf(value.ValueError, "the window is closed")
		}
		v, err := fn(a, this, n)
		if terr := tkError(); err == nil && terr != nil {
			err = a.Errorf(TkError, "%v", terr)
		}
		return v, err
	}
}

func callable(v value.Value) bool {
	switch v.Kind() {
	case value.Function, value.Closure, value.Builtin, value.BoundMethod:
		return true
	}
	return false
}

// handlerArg reads argument i as a handler: a function, or nil for none.
func handlerArg(a native.Args, i int) (value.Value, error) {
	if !a.V[i].IsNil() && !callable(a.V[i]) {
		return value.NilValue, a.TypeError(i, "a function or nil")
	}
	return a.V[i], nil
}

// bindMethod is bind(sequence, f), which windows and all widgets have.
func bindMethod(class string) value.NativeMethod {
	return method("tk::"+class+"::bind", 2, 2, func(a native.Args, this *value.ObjectObj, n *node) (value.Value, error) {
		seq, err := a.String(0)
		if err != nil {
			return value.NilValue, err
		}
		f, err := handlerArg(a, 1)
		if err != nil {
			return value.NilValue, err
		}
		return value.ObjectValue(this), n.bind(a, seq, f)
	})
}

// gridConfigure makes rowconfigure(i, options) or columnconfigure(i,
// options) of a container (a window or a frame).
func gridConfigure(class, what string) value.NativeMethod {
	return method("tk::"+class+"::"+what, 2, 2, func(a native.Args, this *value.ObjectObj, n *node) (value.Value, error) {
		i, err := a.Int(0)
		if err != nil {
			return value.NilValue, err
		}
		if i < 0 || i > maxCells {
			return value.NilValue, a.Errorf(value.ValueError, "%s %d is not from 0 to %d", strings.TrimSuffix(what, "configure"), i, maxCells)
		}
		opts, err := readOptions(a, 1, gridConfigureOptions)
		if err != nil {
			return value.NilValue, err
		}
		if what == "rowconfigure" {
			tk.GridRowConfigure(n.w, i, opts...)
		} else {
			tk.GridColumnConfigure(n.w, i, opts...)
		}
		return value.ObjectValue(this), nil
	})
}

func register() {
	Window.New = newWindow
	Window.Methods = map[string]value.NativeMethod{
		"title": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("tk::Window::title", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			return value.MakeString(this.State.(*window).title), nil
		},
		"settitle": method("tk::Window::settitle", 1, 1, func(a native.Args, this *value.ObjectObj, n *node) (value.Value, error) {
			s, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			n.win.title = s
			n.w.WmTitle(s)
			return value.ObjectValue(this), nil
		}),
		"onclose": method("tk::Window::onclose", 1, 1, func(a native.Args, this *value.ObjectObj, n *node) (value.Value, error) {
			f, err := handlerArg(a, 0)
			if err != nil {
				return value.NilValue, err
			}
			n.win.onclose = f
			return value.NilValue, nil
		}),
		"close": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("tk::Window::close", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			if win := this.State.(*window); !win.closed {
				tk.Destroy(win.w) // its <Destroy> handler does the rest
				tkError()
				win.gone() // in case Tk did not report it
			}
			return value.NilValue, nil
		},
		"closed": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("tk::Window::closed", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			return value.MakeBool(this.State.(*window).closed), nil
		},
		"bind":            bindMethod("Window"),
		"rowconfigure":    gridConfigure("Window", "rowconfigure"),
		"columnconfigure": gridConfigure("Window", "columnconfigure"),
	}
	Window.Show = func(o *value.ObjectObj) string {
		win := o.State.(*window)
		if win.closed {
			return "tk::Window(" + value.Quote(win.title) + ", closed)"
		}
		return "tk::Window(" + value.Quote(win.title) + ")"
	}
	registerWidgets()

	native.Register(&native.Module{
		Name: "tk",
		Doc:  "desktop windows with Tk widgets (frames, labels, buttons, entries, texts, list boxes, check and radio buttons), pack and grid, and Kukel handlers",
		Funcs: map[string]native.Func{
			// available(): whether Tk can start (a display is there); it
			// starts Tk.
			"available": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("tk::available", args, 0, 0)
				if err != nil {
					return value.NilValue, err
				}
				return value.MakeBool(start(a, env) == nil), nil
			},
		},
		Classes: []*native.Class{Window, Frame, Label, Button, Entry, Text, Listbox, Checkbutton, Radiobutton, Radiogroup},
	})
}
