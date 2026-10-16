//go:build tk

package tkmod

import (
	"runtime"
	"strings"
	"time"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/modules/eventsmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// TkError is the kind of the exceptions for what Tk cannot do: start
// without a display, or anything after the first window was closed.
const TkError = "TkError"

// pollEvery is how often events::run() has Tk handle its events.
const pollEvery = 10 * time.Millisecond

// Tk is used from one goroutine only: that of the interpreter that used it
// first, locked to its OS thread. Everything below runs there, also the
// handlers (inside tk.Update, called by the event loop), so there is no
// locking.
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

// tkError takes the error Tk collected, if any: its first line.
func tkError() error {
	err := tk.Error
	tk.Error = nil
	if err == nil {
		return nil
	}
	msg, _, _ := strings.Cut(err.Error(), "\n")
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
// Windows

// window is the state of a tk::Window object: Tk's main window (the
// first one), or a toplevel window.
type window struct {
	w       *tk.Window
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

// ----------------------------------------------------------------------
// Widgets

// widget is the state of a tk::Label, tk::Button or tk::Entry object.
type widget struct {
	class   string
	w       *tk.Window
	win     *window
	text    string // of a label or button
	onclick value.Value
	onenter value.Value
}

var (
	// Label is the class tk::Label: a line of text.
	Label = &native.Class{Name: "Label"}
	// Button is the class tk::Button.
	Button = &native.Class{Name: "Button"}
	// Entry is the class tk::Entry: a field for a line of text.
	Entry = &native.Class{Name: "Entry"}
)

// newWidget makes the constructor of a widget class: new tk::Label(window,
// text), new tk::Button(window, text), new tk::Entry(window [, text]).
func newWidget(class string, minArgs int) value.NativeMethod {
	full := "tk::" + class + "::" + class
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, minArgs, 2)
		if err != nil {
			return value.NilValue, err
		}
		st, err := a.Object(0, Window)
		if err != nil {
			return value.NilValue, err
		}
		win := st.(*window)
		text := ""
		if a.Has(1) {
			if text, err = a.String(1); err != nil {
				return value.NilValue, err
			}
		}
		if err := start(a, env); err != nil {
			return value.NilValue, err
		}
		if win.closed {
			return value.NilValue, a.Errorf(value.ValueError, "the window is closed")
		}
		wd := &widget{class: class, win: win, text: text}
		switch class {
		case "Label":
			wd.w = win.w.Label(tk.Txt(text)).Window
		case "Button":
			wd.w = win.w.Button(tk.Txt(text), tk.Command(func() {
				if !wd.onclick.IsNil() {
					call(wd.onclick)
				}
			})).Window
		case "Entry":
			wd.w = win.w.Entry(tk.Textvariable(text)).Window
			tk.Bind(wd.w, "<Return>", tk.Command(func() {
				if !wd.onenter.IsNil() {
					call(wd.onenter, value.MakeString(wd.w.Textvariable()))
				}
			}))
		}
		if err := tkError(); err != nil {
			return value.NilValue, a.Errorf(TkError, "%v", err)
		}
		this.State = wd
		return value.NilValue, nil
	}
}

// widgetMethod makes a member function of a widget: it refuses a widget
// whose window is closed.
func widgetMethod(class, name string, min, max int, fn func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error)) value.NativeMethod {
	full := "tk::" + class + "::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		wd := this.State.(*widget)
		if wd.win.closed {
			return value.NilValue, a.Errorf(value.ValueError, "the window is closed")
		}
		v, err := fn(a, this, wd)
		if err == nil {
			if terr := tkError(); terr != nil {
				err = a.Errorf(TkError, "%v", terr)
			}
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

// handlerArg reads argument 0 as a handler: a function, or nil for none.
func handlerArg(a native.Args) (value.Value, error) {
	if !a.V[0].IsNil() && !callable(a.V[0]) {
		return value.NilValue, a.TypeError(0, "a function or nil")
	}
	return a.V[0], nil
}

// The member functions every widget has: text(), settext(s), pack([opts]).
func commonMethods(class string) map[string]value.NativeMethod {
	return map[string]value.NativeMethod{
		"text": widgetMethod(class, "text", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
			if wd.class == "Entry" {
				return value.MakeString(wd.w.Textvariable()), nil
			}
			return value.MakeString(wd.text), nil
		}),
		"settext": widgetMethod(class, "settext", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
			s, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			if wd.class == "Entry" {
				wd.w.Configure(tk.Textvariable(s))
			} else {
				wd.text = s
				wd.w.Configure(tk.Txt(s))
			}
			return value.ObjectValue(this), nil
		}),
		"pack": widgetMethod(class, "pack", 0, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
			opts := []tk.Opt{wd.w}
			if a.Has(0) {
				more, err := packOptions(a)
				if err != nil {
					return value.NilValue, err
				}
				opts = append(opts, more...)
			}
			tk.Pack(opts...)
			return value.ObjectValue(this), nil
		}),
	}
}

// packOptions reads the options of pack: side, fill, expand, padx, pady.
func packOptions(a native.Args) ([]tk.Opt, error) {
	if a.V[0].Kind() != value.Map {
		return nil, a.TypeError(0, "a map of options")
	}
	var opts []tk.Opt
	var bad error
	oneOf := func(key, s string, allowed ...string) bool {
		for _, x := range allowed {
			if s == x {
				return true
			}
		}
		bad = a.Errorf(value.ValueError, "option %s: %q is not one of %s", key, s, strings.Join(allowed, ", "))
		return false
	}
	a.V[0].AsMap().Each(func(k, v value.Value) {
		if bad != nil {
			return
		}
		key := k.String()
		switch {
		case k.Kind() != value.String:
			bad = a.Errorf(value.ValueError, "an option is named by a string, not %s", k.Kind())
		case key == "side" || key == "fill":
			if v.Kind() != value.String {
				bad = a.Errorf(value.TypeError, "option %s must be a string, not %s", key, v.Kind())
				return
			}
			s := v.AsString()
			if key == "side" && oneOf(key, s, "top", "bottom", "left", "right") {
				opts = append(opts, tk.Side(s))
			}
			if key == "fill" && oneOf(key, s, "none", "x", "y", "both") {
				opts = append(opts, tk.Fill(s))
			}
		case key == "expand":
			if v.Kind() != value.Bool {
				bad = a.Errorf(value.TypeError, "option expand must be true or false, not %s", v.Kind())
				return
			}
			opts = append(opts, tk.Expand(v.AsBool()))
		case key == "padx" || key == "pady":
			if v.Kind() != value.Int || v.AsInt() < 0 || v.AsInt() > 10000 {
				bad = a.Errorf(value.ValueError, "option %s must be a number of pixels (0 to 10000), not %s", key, v.String())
				return
			}
			if key == "padx" {
				opts = append(opts, tk.Padx(v.AsInt()))
			} else {
				opts = append(opts, tk.Pady(v.AsInt()))
			}
		default:
			bad = a.Errorf(value.ValueError, "unknown option %q (the options are side, fill, expand, padx and pady)", key)
		}
	})
	return opts, bad
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
		"settitle": windowMethod("settitle", 1, func(a native.Args, this *value.ObjectObj, win *window) (value.Value, error) {
			s, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			win.title = s
			win.w.WmTitle(s)
			return value.ObjectValue(this), nil
		}),
		"onclose": windowMethod("onclose", 1, func(a native.Args, this *value.ObjectObj, win *window) (value.Value, error) {
			f, err := handlerArg(a)
			if err != nil {
				return value.NilValue, err
			}
			win.onclose = f
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
	}
	Window.Show = func(o *value.ObjectObj) string {
		win := o.State.(*window)
		if win.closed {
			return "tk::Window(" + value.Quote(win.title) + ", closed)"
		}
		return "tk::Window(" + value.Quote(win.title) + ")"
	}

	Label.New = newWidget("Label", 2)
	Label.Methods = commonMethods("Label")
	Button.New = newWidget("Button", 2)
	Button.Methods = commonMethods("Button")
	Button.Methods["onclick"] = widgetMethod("Button", "onclick", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		f, err := handlerArg(a)
		if err != nil {
			return value.NilValue, err
		}
		wd.onclick = f
		return value.ObjectValue(this), nil
	})
	// invoke() calls the onclick handler as a click would, at once: its
	// result and exception come back to the caller.
	Button.Methods["invoke"] = widgetMethod("Button", "invoke", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		if wd.onclick.IsNil() {
			return value.NilValue, nil
		}
		return gui.env.Call(wd.onclick, nil)
	})
	Entry.New = newWidget("Entry", 1)
	Entry.Methods = commonMethods("Entry")
	// focus(): the keys typed go to the entry.
	Entry.Methods["focus"] = widgetMethod("Entry", "focus", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		tk.Focus(wd.w)
		return value.ObjectValue(this), nil
	})
	Entry.Methods["onenter"] = widgetMethod("Entry", "onenter", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		f, err := handlerArg(a)
		if err != nil {
			return value.NilValue, err
		}
		wd.onenter = f
		return value.ObjectValue(this), nil
	})
	for _, c := range []*native.Class{Label, Button, Entry} {
		c.Show = func(o *value.ObjectObj) string {
			wd := o.State.(*widget)
			text := wd.text
			if wd.class == "Entry" && !wd.win.closed {
				text = wd.w.Textvariable()
			}
			return "tk::" + wd.class + "(" + value.Quote(text) + ")"
		}
	}

	native.Register(&native.Module{
		Name: "tk",
		Doc:  "desktop windows with Tk widgets (labels, buttons, entries) and Kukel handlers",
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
		Classes: []*native.Class{Window, Label, Button, Entry},
	})
}

func windowMethod(name string, n int, fn func(a native.Args, this *value.ObjectObj, win *window) (value.Value, error)) value.NativeMethod {
	full := "tk::Window::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, n, n)
		if err != nil {
			return value.NilValue, err
		}
		win := this.State.(*window)
		if win.closed {
			return value.NilValue, a.Errorf(value.ValueError, "the window is closed")
		}
		v, err := fn(a, this, win)
		if err == nil {
			if terr := tkError(); terr != nil {
				err = a.Errorf(TkError, "%v", terr)
			}
		}
		return v, err
	}
}
