//go:build tk

package tkmod

import (
	"math"
	"strconv"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

var (
	// Scale is the class tk::Scale: a slider for a number.
	Scale = &native.Class{Name: "Scale"}
	// Combobox is the class tk::Combobox: a field with a drop-down list of
	// lines to choose from.
	Combobox = &native.Class{Name: "Combobox"}
)

// slider is the state of a tk::Scale: its range, its step (0: any value),
// and the value it last had.
type slider struct {
	scale          *tk.TScaleWidget
	from, to, step float64
	ints           bool // from and step are ints: the values are ints
	last           float64
}

// limit is a number option of a scale: from -1e9 to 1e9.
func limit(v value.Value) (any, string) {
	x, ok := coordinate(v)
	if !ok || math.Abs(x) > 1e9 {
		return nil, " must be a number from -1e9 to 1e9, not " + v.Repr()
	}
	return x, ""
}

// snap gives x on the slider: within its range, and on a step.
func (s *slider) snap(x float64) float64 {
	lo, hi := math.Min(s.from, s.to), math.Max(s.from, s.to)
	if s.step > 0 {
		x = s.from + math.Round((x-s.from)/s.step)*s.step
	}
	return math.Max(lo, math.Min(hi, x))
}

func (s *slider) number(x float64) value.Value {
	if s.ints {
		return value.MakeInt(int64(math.Round(x)))
	}
	return value.MakeFloat(x)
}

// read gives the slider's value from Tk, snapped.
func (s *slider) read() float64 {
	x, err := strconv.ParseFloat(s.scale.Get(), 64)
	if err != nil {
		return s.last
	}
	return s.snap(x)
}

// apply takes from, to and step of an options map (checked already) into
// the slider's state.
func (s *slider) apply(opts value.Value) {
	if opts.Kind() != value.Map {
		return
	}
	for _, name := range []string{"from", "to", "step"} {
		if v := mapGet(opts, name); !v.IsNil() {
			x, _ := coordinate(v)
			switch name {
			case "from":
				s.from = x
			case "to":
				s.to = x
			default:
				s.step = x
			}
		}
	}
	s.ints = s.step > 0 && s.from == math.Trunc(s.from) && s.step == math.Trunc(s.step)
}

func registerInputs() {
	// new tk::Scale(parent [, options]): value(), setvalue(x), onchange(f)
	// (f gets value() when the user moves the slider); the options from
	// and to (0 and 100 when left out), step (0: any value), orient,
	// length, state.
	registerWidget(Scale, widgetSpec{0, 0, false, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, _ string) error {
		s := &slider{to: 100}
		if len(a.V) > 1 {
			s.apply(a.V[len(a.V)-1])
		}
		wd.slider = s
		s.scale = parent.w.TScale(append([]tk.Opt{tk.From(s.from), tk.To(s.to), tk.Value(s.from)}, append(opts, tk.Command(func() {
			x := s.read()
			if x != s.last {
				s.last = x
				s.scale.Configure(tk.Value(x)) // on its step
				if !wd.onchange.IsNil() {
					call(wd.onchange, s.number(x))
				}
			}
		}))...)...)
		s.last = s.from
		wd.w = s.scale.Window
		return nil
	}}, func(wd *widget) string {
		return "tk::Scale(" + wd.slider.number(wd.slider.last).String() + ")"
	})
	Scale.Methods["value"] = widgetMethod("Scale", "value", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		return wd.slider.number(wd.slider.last), nil
	})
	Scale.Methods["setvalue"] = widgetMethod("Scale", "setvalue", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		x, ok := coordinate(a.V[0])
		if !ok {
			return value.NilValue, a.TypeError(0, "a number")
		}
		s := wd.slider
		s.last = s.snap(x)
		s.scale.Configure(tk.Value(s.last))
		return value.ObjectValue(this), nil
	})
	Scale.Methods["onchange"] = handlerSetter("Scale", "onchange", func(wd *widget) *value.Value { return &wd.onchange })

	// new tk::Combobox(parent [, lines] [, options]): text(), settext(s),
	// items(), setitems(v), onselect(f) (f gets the text when the user
	// chooses a line); the options width, height (lines in the list),
	// font, justify, state ("normal": the text can be typed, "readonly":
	// only chosen, "disabled").
	registerWidget(Combobox, widgetSpec{0, 1, false, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, _ string) (err error) {
		if len(args) > 0 {
			if wd.items, err = itemsArg(a, 1); err != nil {
				return err
			}
		}
		cb := parent.w.TCombobox(append(opts, tk.Values(wd.items), tk.Textvariable(""))...)
		wd.w = cb.Window
		tk.Bind(wd.w, "<<ComboboxSelected>>", tk.Command(func() {
			if !wd.onselect.IsNil() {
				call(wd.onselect, value.MakeString(wd.w.Textvariable()))
			}
		}))
		// The text, kept for printing after the window closed.
		tk.Bind(wd.w, "<Destroy>", tk.Command(func() { wd.text = wd.w.Textvariable() }))
		return nil
	}}, func(wd *widget) string {
		text := wd.text
		if !wd.win.closed {
			text = wd.w.Textvariable()
		}
		return "tk::Combobox(" + value.Quote(text) + ")"
	})
	Combobox.Methods["text"] = widgetMethod("Combobox", "text", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		return value.MakeString(wd.w.Textvariable()), nil
	})
	Combobox.Methods["settext"] = widgetMethod("Combobox", "settext", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		s, err := tkString(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		wd.w.Configure(tk.Textvariable(s))
		return value.ObjectValue(this), nil
	})
	Combobox.Methods["items"] = widgetMethod("Combobox", "items", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		v := make([]value.Value, len(wd.items))
		for i, s := range wd.items {
			v[i] = value.MakeString(s)
		}
		return value.MakeVector(v), nil
	})
	Combobox.Methods["setitems"] = widgetMethod("Combobox", "setitems", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		items, err := itemsArg(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		wd.items = items
		wd.w.Configure(tk.Values(items))
		return value.ObjectValue(this), nil
	})
	Combobox.Methods["onselect"] = handlerSetter("Combobox", "onselect", func(wd *widget) *value.Value { return &wd.onselect })
}
