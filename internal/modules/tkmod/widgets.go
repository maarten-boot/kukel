//go:build tk

package tkmod

import (
	"strings"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// widget is the state of a widget object. Frame, Label, Button, Entry,
// Checkbutton and Radiobutton are Tk's themed (ttk) widgets; Text and
// Listbox are classic ones (ttk has none).
type widget struct {
	node
	class string
	outer *tk.Window // what pack and grid place: w, or the frame around w and its scrollbars
	text  string     // of a label, button, check or radio button

	onclick value.Value // Button
	onenter value.Value // Entry

	textw    *tk.TextWidget // Text
	state    string         // Text: "normal" or "disabled"
	list     *tk.ListboxWidget
	items    []string    // Listbox
	onselect value.Value // Listbox

	checked  bool // Checkbutton
	check    *tk.VariableOpt
	onchange value.Value // Checkbutton
	group    *radiogroup // Radiobutton
	value    string      // Radiobutton

	canvas *canvas // Canvas
	slider *slider // Scale
}

// A widgetSpec is how a widget class is made: the arguments between the
// parent and the options (at least min, at most max), whether the
// constructor takes the option scroll, and make, which makes the Tk
// widget from them and the other options.
type widgetSpec struct {
	min, max int
	scroll   bool
	make     func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, scroll string) error
}

var specs = map[string]widgetSpec{}

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

// stringArg reads argument i of the constructor's middle arguments (args),
// which must be a string; "" when there is none.
func stringArg(a native.Args, args []value.Value, i int) (string, error) {
	if i >= len(args) {
		return "", nil
	}
	return tkString(a, 1+i)
}

// newWidget makes the constructor of a widget class: new tk::C(parent,
// arguments... [, options]). The parent is a window or a frame; options
// are those of configure, and for some classes scroll.
func newWidget(class string) value.NativeMethod {
	full := "tk::" + class + "::" + class
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		spec := specs[class]
		a, err := native.Check(full, args, 1+spec.min, 1+spec.max+1)
		if err != nil {
			return value.NilValue, err
		}
		parent, err := parentArg(a)
		if err != nil {
			return value.NilValue, err
		}
		mid := a.V[1:]
		var values []optionValue
		scroll := "none"
		if last := len(mid) - 1; last >= spec.min && mid[last].Kind() == value.Map {
			mid = mid[:last]
			known := configOptions[class]
			if spec.scroll {
				if a, scroll, err = scrollOption(a, 1+last); err != nil {
					return value.NilValue, err
				}
				// Taken out already: here for the list of the options.
				known = append(known[:len(known):len(known)], option{name: "scroll"})
			}
			if values, err = readValues(a, 1+last, known); err != nil {
				return value.NilValue, err
			}
		}
		if len(mid) > spec.max {
			return value.NilValue, a.TypeError(1+spec.max, "a map of options")
		}
		if err := start(a, env); err != nil {
			return value.NilValue, err
		}
		if parent.win.closed {
			return value.NilValue, a.Errorf(value.ValueError, "the window is closed")
		}
		opts := makeOpts(values) // after start: a font is made in Tk
		wd := &widget{class: class}
		wd.win = parent.win
		if err := spec.make(a, mid, parent, wd, opts, scroll); err != nil {
			return value.NilValue, err
		}
		if wd.outer == nil {
			wd.outer = wd.w
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
			if a.V[0].Kind() == value.Map && specs[class].scroll && !mapGet(a.V[0], "scroll").IsNil() {
				return value.NilValue, a.Errorf(value.ValueError, "option scroll can only be given to the constructor")
			}
			opts, err := readOptions(a, 0, configOptions[class])
			if err != nil {
				return value.NilValue, err
			}
			wd.w.Configure(opts...)
			if s := wd.slider; s != nil { // its range or step may change
				s.apply(a.V[0])
				s.last = s.snap(s.read())
				s.scale.Configure(tk.Value(s.last))
			}
			if class == "Text" || class == "Listbox" {
				if s := mapGet(a.V[0], "state"); s.Kind() == value.String {
					wd.state = s.AsString()
				}
			}
			return value.ObjectValue(this), nil
		}),
		"pack": widgetMethod(class, "pack", 0, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
			opts := []tk.Opt{wd.outer}
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
			tk.Grid(wd.outer, opts...)
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

// textMethods are text() and settext(s): of labels, buttons, entries, and
// check and radio buttons.
func textMethods(class string, m map[string]value.NativeMethod) {
	m["text"] = widgetMethod(class, "text", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		if wd.class == "Entry" {
			return value.MakeString(wd.w.Textvariable()), nil
		}
		return value.MakeString(wd.text), nil
	})
	m["settext"] = widgetMethod(class, "settext", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		s, err := tkString(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		wd.text = s
		if wd.class == "Entry" {
			wd.w.Configure(tk.Textvariable(s))
		} else {
			wd.w.Configure(tk.Txt(s))
		}
		return value.ObjectValue(this), nil
	})
}

// handlerSetter makes a member function that sets a handler, such as
// onclick(f): f is a function, or nil for none.
func handlerSetter(class, name string, field func(wd *widget) *value.Value) value.NativeMethod {
	return widgetMethod(class, name, 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		f, err := handlerArg(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		*field(wd) = f
		return value.ObjectValue(this), nil
	})
}

// register adds class c, made as spec says; Show is how it prints.
func registerWidget(c *native.Class, spec widgetSpec, show func(wd *widget) string) {
	specs[c.Name] = spec
	c.New = newWidget(c.Name)
	c.Methods = commonMethods(c.Name)
	c.Show = func(o *value.ObjectObj) string { return show(o.State.(*widget)) }
}

// showText prints a widget as tk::C("its text").
func showText(wd *widget) string {
	text := wd.text
	if wd.class == "Entry" && !wd.win.closed {
		text = wd.w.Textvariable()
	}
	return "tk::" + wd.class + "(" + value.Quote(text) + ")"
}

func registerWidgets() {
	registerWidget(Frame, widgetSpec{0, 0, false, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, _ string) error {
		wd.w = parent.w.TFrame(opts...).Window
		return nil
	}}, func(wd *widget) string { return "tk::Frame" })
	Frame.Methods["rowconfigure"] = gridConfigure("Frame", "rowconfigure")
	Frame.Methods["columnconfigure"] = gridConfigure("Frame", "columnconfigure")

	registerWidget(Label, widgetSpec{1, 1, false, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, _ string) (err error) {
		if wd.text, err = stringArg(a, args, 0); err != nil {
			return err
		}
		wd.w = parent.w.TLabel(append(opts, tk.Txt(wd.text))...).Window
		return nil
	}}, showText)
	textMethods("Label", Label.Methods)

	registerWidget(Button, widgetSpec{1, 1, false, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, _ string) (err error) {
		if wd.text, err = stringArg(a, args, 0); err != nil {
			return err
		}
		wd.w = parent.w.TButton(append(opts, tk.Txt(wd.text), tk.Command(func() {
			if !wd.onclick.IsNil() {
				call(wd.onclick)
			}
		}))...).Window
		return nil
	}}, showText)
	textMethods("Button", Button.Methods)
	Button.Methods["onclick"] = handlerSetter("Button", "onclick", func(wd *widget) *value.Value { return &wd.onclick })
	// invoke() calls the onclick handler as a click would, at once: its
	// result and exception come back to the caller.
	Button.Methods["invoke"] = widgetMethod("Button", "invoke", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		if wd.onclick.IsNil() {
			return value.NilValue, nil
		}
		return gui.env.Call(wd.onclick, nil)
	})

	registerWidget(Entry, widgetSpec{0, 1, false, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, _ string) error {
		text, err := stringArg(a, args, 0)
		if err != nil {
			return err
		}
		wd.text = text
		wd.w = parent.w.TEntry(append(opts, tk.Textvariable(text))...).Window
		tk.Bind(wd.w, "<Return>", tk.Command(func() {
			if !wd.onenter.IsNil() {
				call(wd.onenter, value.MakeString(wd.w.Textvariable()))
			}
		}))
		// The text the user typed, kept for printing after the window closed.
		tk.Bind(wd.w, "<Destroy>", tk.Command(func() { wd.text = wd.w.Textvariable() }))
		return nil
	}}, showText)
	textMethods("Entry", Entry.Methods)
	// focus(): the keys typed go to the entry.
	Entry.Methods["focus"] = widgetMethod("Entry", "focus", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		tk.Focus(wd.w)
		return value.ObjectValue(this), nil
	})
	Entry.Methods["onenter"] = handlerSetter("Entry", "onenter", func(wd *widget) *value.Value { return &wd.onenter })

	registerLists()
	registerChoices()
	registerCanvas()
	registerInputs()
	registerImages()
}
