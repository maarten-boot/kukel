//go:build tk

package tkmod

import (
	"strconv"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

var (
	// Text is the class tk::Text: several lines of text to edit or show.
	Text = &native.Class{Name: "Text"}
	// Listbox is the class tk::Listbox: lines to choose from.
	Listbox = &native.Class{Name: "Listbox"}
)

// scrolled puts what make makes into a frame of its own in parent, with
// scrollbars for the directions of scroll ("x", "y", "both"); with "none"
// it makes the widget in parent itself. It gives the widget and what pack
// and grid place. When the view moves, moved (if not nil) gets the axis
// ('x', 'y') and the fraction of the whole that is left of or above it.
func scrolled(parent *node, scroll string, make func(in *tk.Window, opts ...tk.Opt) *tk.Window, moved func(axis byte, first float64)) (w, outer *tk.Window) {
	if scroll == "none" {
		w = make(parent.w)
		return w, w
	}
	frame := parent.w.TFrame().Window
	var xbar, ybar *tk.TScrollbarWidget
	var opts []tk.Opt
	if scroll == "y" || scroll == "both" {
		opts = append(opts, tk.Yscrollcommand(func(e *tk.Event) { e.ScrollSet(ybar); report(moved, 'y', e) }))
	}
	if scroll == "x" || scroll == "both" {
		opts = append(opts, tk.Xscrollcommand(func(e *tk.Event) { e.ScrollSet(xbar); report(moved, 'x', e) }))
	}
	w = make(frame, opts...)
	tk.Grid(w, tk.Row(0), tk.Column(0), tk.Sticky("nsew"))
	if scroll == "y" || scroll == "both" {
		ybar = frame.TScrollbar(tk.Command(func(e *tk.Event) { e.Yview(w) }))
		tk.Grid(ybar, tk.Row(0), tk.Column(1), tk.Sticky("ns"))
	}
	if scroll == "x" || scroll == "both" {
		xbar = frame.TScrollbar(tk.Orient("horizontal"), tk.Command(func(e *tk.Event) { e.Xview(w) }))
		tk.Grid(xbar, tk.Row(1), tk.Column(0), tk.Sticky("ew"))
	}
	tk.GridRowConfigure(frame, 0, tk.Weight(1))
	tk.GridColumnConfigure(frame, 0, tk.Weight(1))
	return w, frame
}

// report passes the view's first fraction, which a scroll command gets, on
// to moved.
func report(moved func(byte, float64), axis byte, e *tk.Event) {
	if moved != nil && len(e.Args) > 0 {
		if first, err := strconv.ParseFloat(e.Args[0], 64); err == nil {
			moved(axis, first)
		}
	}
}

// editable runs f, which changes a text or list box, with its state
// normal for a moment if it is disabled (Tk ignores changes then).
func (wd *widget) editable(f func()) {
	if wd.state == "disabled" {
		wd.w.Configure(tk.State("normal"))
		defer wd.w.Configure(tk.State("disabled"))
	}
	f()
}

// configureAfter applies the constructor's options to a text or list box
// after its contents are in, and notes its state.
func (wd *widget) configureAfter(a native.Args, opts []tk.Opt) {
	wd.state = "normal"
	if len(opts) == 0 {
		return
	}
	if s := mapGet(a.V[len(a.V)-1], "state"); s.Kind() == value.String {
		wd.state = s.AsString()
	}
	wd.w.Configure(opts...)
}

// itemsArg reads argument i, a vector of strings: the lines of a list box.
func itemsArg(a native.Args, i int) ([]string, error) {
	if a.V[i].Kind() != value.Vector {
		return nil, a.TypeError(i, "a vector of strings")
	}
	var items []string
	for _, v := range a.V[i].AsVector().Elems {
		if v.Kind() != value.String {
			return nil, a.Errorf(value.TypeError, "argument %d: the lines must be strings, not %s", i+1, v.Kind())
		}
		if bad := tkUnsafe(v.AsString()); bad != "" {
			return nil, a.Errorf(value.ValueError, "argument %d: a line holds %s", i+1, bad)
		}
		items = append(items, v.AsString())
	}
	return items, nil
}

// selection is the selected lines of a list box: a vector of indexes.
func (wd *widget) selection() value.Value {
	var v []value.Value
	for _, i := range wd.list.Curselection() {
		v = append(v, value.MakeInt(int64(i)))
	}
	return value.MakeVector(v)
}

func registerLists() {
	// new tk::Text(parent [, text] [, options]): its text(), settext(s),
	// append(s) (at the end, which it then shows), and the options width,
	// height, wrap, state; scroll in the constructor.
	registerWidget(Text, widgetSpec{0, 1, true, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, scroll string) error {
		text, err := stringArg(a, args, 0)
		if err != nil {
			return err
		}
		wd.w, wd.outer = scrolled(parent, scroll, func(in *tk.Window, sb ...tk.Opt) *tk.Window {
			wd.textw = in.Text(append(sb, tk.Undo(true))...)
			return wd.textw.Window
		}, nil)
		wd.textw.Insert("end", text)
		wd.configureAfter(a, opts)
		return nil
	}}, func(wd *widget) string { return "tk::Text" })
	Text.Methods["text"] = widgetMethod("Text", "text", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		return value.MakeString(wd.textw.Text()), nil
	})
	Text.Methods["settext"] = widgetMethod("Text", "settext", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		s, err := tkString(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		wd.editable(func() {
			wd.textw.Delete("1.0", "end")
			wd.textw.Insert("end", s)
		})
		return value.ObjectValue(this), nil
	})
	Text.Methods["append"] = widgetMethod("Text", "append", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		s, err := tkString(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		wd.editable(func() { wd.textw.Insert("end", s) })
		wd.textw.See("end")
		return value.ObjectValue(this), nil
	})

	// new tk::Listbox(parent [, lines] [, options]): items(), setitems(v),
	// selected() (a vector of indexes), setselected(v), onselect(f) (f gets
	// selected() when the user changes it); the options width, height,
	// selectmode, state; scroll in the constructor.
	registerWidget(Listbox, widgetSpec{0, 1, true, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, scroll string) (err error) {
		if len(args) > 0 {
			if wd.items, err = itemsArg(a, 1); err != nil {
				return err
			}
		}
		wd.w, wd.outer = scrolled(parent, scroll, func(in *tk.Window, sb ...tk.Opt) *tk.Window {
			// The selection stays when text is selected elsewhere.
			wd.list = in.Listbox(append(sb, tk.Exportselection(false))...)
			return wd.list.Window
		}, nil)
		for _, s := range wd.items {
			wd.list.Insert("end", s)
		}
		wd.configureAfter(a, opts)
		tk.Bind(wd.w, "<<ListboxSelect>>", tk.Command(func() {
			if !wd.onselect.IsNil() {
				call(wd.onselect, wd.selection())
			}
		}))
		return nil
	}}, func(wd *widget) string {
		if len(wd.items) == 1 {
			return "tk::Listbox(1 line)"
		}
		return "tk::Listbox(" + value.MakeInt(int64(len(wd.items))).String() + " lines)"
	})
	Listbox.Methods["items"] = widgetMethod("Listbox", "items", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		v := make([]value.Value, len(wd.items))
		for i, s := range wd.items {
			v[i] = value.MakeString(s)
		}
		return value.MakeVector(v), nil
	})
	Listbox.Methods["setitems"] = widgetMethod("Listbox", "setitems", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		items, err := itemsArg(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		wd.items = items
		wd.editable(func() {
			wd.list.Delete(0, "end")
			for _, s := range items {
				wd.list.Insert("end", s)
			}
		})
		return value.ObjectValue(this), nil
	})
	Listbox.Methods["selected"] = widgetMethod("Listbox", "selected", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		return wd.selection(), nil
	})
	Listbox.Methods["setselected"] = widgetMethod("Listbox", "setselected", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		if a.V[0].Kind() != value.Vector {
			return value.NilValue, a.TypeError(0, "a vector of indexes")
		}
		var lines []int
		for _, v := range a.V[0].AsVector().Elems {
			if v.Kind() != value.Int || v.AsInt() < 0 || v.AsInt() >= int64(len(wd.items)) {
				return value.NilValue, a.Errorf(value.ValueError, "%s is not the index of a line (0 to %d)", v.Repr(), len(wd.items)-1)
			}
			lines = append(lines, int(v.AsInt()))
		}
		wd.editable(func() {
			wd.list.SelectionClear(0, "end")
			for _, i := range lines {
				wd.list.SelectionSet(i)
			}
		})
		if len(lines) > 0 {
			wd.list.See(lines[0])
		}
		return value.ObjectValue(this), nil
	})
	Listbox.Methods["onselect"] = handlerSetter("Listbox", "onselect", func(wd *widget) *value.Value { return &wd.onselect })
}
