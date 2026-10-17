//go:build tk

package tkmod

import (
	"strings"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// widget is the state of a widget object: tk::Frame, tk::Label,
// tk::Button or tk::Entry. They are Tk's themed (ttk) widgets.
type widget struct {
	node
	class   string
	text    string // of a label or button
	onclick value.Value
	onenter value.Value
}

var (
	// Frame is the class tk::Frame: a container for other widgets.
	Frame = &native.Class{Name: "Frame"}
	// Label is the class tk::Label: a line of text.
	Label = &native.Class{Name: "Label"}
	// Button is the class tk::Button.
	Button = &native.Class{Name: "Button"}
	// Entry is the class tk::Entry: a field for a line of text.
	Entry = &native.Class{Name: "Entry"}
)

// textArgs is how a widget's constructor takes its text: Label and Button
// need one, an Entry may have one, a Frame has none.
var textArgs = map[string]struct{ min, max int }{
	"Frame":  {0, 0},
	"Label":  {1, 1},
	"Button": {1, 1},
	"Entry":  {0, 1},
}

// parentArg reads argument 0, the parent of a new widget: a window or a
// frame.
func parentArg(a native.Args) (*node, error) {
	switch {
	case Window.Is(a.V[0]):
		return &a.V[0].AsObject().State.(*window).node, nil
	case Frame.Is(a.V[0]):
		return &a.V[0].AsObject().State.(*widget).node, nil
	}
	return nil, a.TypeError(0, "a tk::Window or tk::Frame")
}

// newWidget makes the constructor of a widget class: new tk::Frame(parent
// [, options]), new tk::Label(parent, text [, options]), new
// tk::Button(parent, text [, options]), new tk::Entry(parent [, text] [,
// options]). The parent is a window or a frame; options are those of
// configure.
func newWidget(class string) value.NativeMethod {
	full := "tk::" + class + "::" + class
	texts := textArgs[class]
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, 1+texts.min, 1+texts.max+1)
		if err != nil {
			return value.NilValue, err
		}
		parent, err := parentArg(a)
		if err != nil {
			return value.NilValue, err
		}
		i, text := 1, ""
		if texts.max > 0 && a.Has(i) && (i < 1+texts.min || a.V[i].Kind() != value.Map) {
			if text, err = a.String(i); err != nil {
				return value.NilValue, err
			}
			i++
		}
		var opts []tk.Opt
		if a.Has(i) {
			if i < len(a.V)-1 {
				return value.NilValue, a.TypeError(i, "a string")
			}
			if opts, err = readOptions(a, i, configOptions[class]); err != nil {
				return value.NilValue, err
			}
		}
		if err := start(a, env); err != nil {
			return value.NilValue, err
		}
		if parent.win.closed {
			return value.NilValue, a.Errorf(value.ValueError, "the window is closed")
		}
		wd := &widget{class: class, text: text}
		wd.win = parent.win
		switch class {
		case "Frame":
			wd.w = parent.w.TFrame(opts...).Window
		case "Label":
			wd.w = parent.w.TLabel(append(opts, tk.Txt(text))...).Window
		case "Button":
			wd.w = parent.w.TButton(append(opts, tk.Txt(text), tk.Command(func() {
				if !wd.onclick.IsNil() {
					call(wd.onclick)
				}
			}))...).Window
		case "Entry":
			wd.w = parent.w.TEntry(append(opts, tk.Textvariable(text))...).Window
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

// widgetMethod makes a member function of a widget.
func widgetMethod(class, name string, min, max int, fn func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error)) value.NativeMethod {
	return method("tk::"+class+"::"+name, min, max, func(a native.Args, this *value.ObjectObj, n *node) (value.Value, error) {
		return fn(a, this, this.State.(*widget))
	})
}

// The member functions every widget has: configure(options),
// pack([options]), grid([options]), bind(sequence, f).
func commonMethods(class string) map[string]value.NativeMethod {
	return map[string]value.NativeMethod{
		"configure": widgetMethod(class, "configure", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
			opts, err := readOptions(a, 0, configOptions[class])
			if err != nil {
				return value.NilValue, err
			}
			wd.w.Configure(opts...)
			return value.ObjectValue(this), nil
		}),
		"pack": widgetMethod(class, "pack", 0, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
			opts := []tk.Opt{wd.w}
			if a.Has(0) {
				more, err := readOptions(a, 0, packOptions)
				if err != nil {
					return value.NilValue, err
				}
				opts = append(opts, more...)
			}
			tk.Pack(opts...)
			return value.ObjectValue(this), mixedManagers(a, "pack")
		}),
		"grid": widgetMethod(class, "grid", 0, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
			var opts []tk.Opt
			if a.Has(0) {
				var err error
				if opts, err = readOptions(a, 0, gridOptions); err != nil {
					return value.NilValue, err
				}
			}
			tk.Grid(wd.w, opts...)
			return value.ObjectValue(this), mixedManagers(a, "grid")
		}),
		"bind": bindMethod(class),
	}
}

// mixedManagers turns Tk's refusal to use pack and grid in one container
// into a ValueError (Tk's message names internal window paths); other
// failures are left for method, as a TkError.
func mixedManagers(a native.Args, using string) error {
	if tk.Error == nil || !strings.Contains(tk.Error.Error(), "cannot use geometry manager") {
		return nil
	}
	tk.Error = nil
	other := map[string]string{"pack": "grid", "grid": "pack"}[using]
	return a.Errorf(value.ValueError, "cannot %s: %s already places the widgets beside it (use one of pack and grid in each window or frame)", using, other)
}

// textMethods are text() and settext(s), of labels, buttons and entries.
func textMethods(class string, m map[string]value.NativeMethod) {
	m["text"] = widgetMethod(class, "text", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		if wd.class == "Entry" {
			return value.MakeString(wd.w.Textvariable()), nil
		}
		return value.MakeString(wd.text), nil
	})
	m["settext"] = widgetMethod(class, "settext", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
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
	})
}

func registerWidgets() {
	for _, c := range []*native.Class{Frame, Label, Button, Entry} {
		c.New = newWidget(c.Name)
		c.Methods = commonMethods(c.Name)
		if c != Frame {
			textMethods(c.Name, c.Methods)
		}
		c.Show = func(o *value.ObjectObj) string {
			wd := o.State.(*widget)
			if wd.class == "Frame" {
				return "tk::Frame"
			}
			text := wd.text
			if wd.class == "Entry" && !wd.win.closed {
				text = wd.w.Textvariable()
			}
			return "tk::" + wd.class + "(" + value.Quote(text) + ")"
		}
	}
	Frame.Methods["rowconfigure"] = gridConfigure("Frame", "rowconfigure")
	Frame.Methods["columnconfigure"] = gridConfigure("Frame", "columnconfigure")

	Button.Methods["onclick"] = widgetMethod("Button", "onclick", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		f, err := handlerArg(a, 0)
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
	// focus(): the keys typed go to the entry.
	Entry.Methods["focus"] = widgetMethod("Entry", "focus", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		tk.Focus(wd.w)
		return value.ObjectValue(this), nil
	})
	Entry.Methods["onenter"] = widgetMethod("Entry", "onenter", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		f, err := handlerArg(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		wd.onenter = f
		return value.ObjectValue(this), nil
	})
}
