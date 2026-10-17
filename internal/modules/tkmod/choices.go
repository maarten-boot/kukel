//go:build tk

package tkmod

import (
	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

var (
	// Checkbutton is the class tk::Checkbutton: on or off.
	Checkbutton = &native.Class{Name: "Checkbutton"}
	// Radiobutton is the class tk::Radiobutton: one of a tk::Radiogroup.
	Radiobutton = &native.Class{Name: "Radiobutton"}
	// Radiogroup is the class tk::Radiogroup: the radio buttons of which
	// one is chosen. It is not a widget.
	Radiogroup = &native.Class{Name: "Radiogroup"}
)

// radiogroup is the state of a tk::Radiogroup: a Tk variable that its radio
// buttons share, holding the value of the chosen one ("" for none).
type radiogroup struct {
	v        *tk.VariableOpt
	inUse    bool   // a radio button or menu entry uses v (before, v cannot be set)
	value    string // what v holds
	onchange value.Value
}

// set chooses the radio button of value s ("": none).
func (g *radiogroup) set(s string) {
	g.value = s
	if g.inUse {
		g.v.Set(s)
	} else {
		g.v = tk.Variable(s)
	}
}

// linked is called after a radio button or menu entry was made with v.
// tk9.0 then sets the variable to the value v was made with, which is not
// the group's value after a setvalue or a choice: it is set again.
func (g *radiogroup) linked() {
	g.inUse = true
	g.v.Set(g.value)
}

// chose is the user choosing the radio button or entry of value s.
func (g *radiogroup) chose(s string) {
	g.value = s
	if !g.onchange.IsNil() {
		call(g.onchange, value.MakeString(s))
	}
}

func (g *radiogroup) get() value.Value {
	if g.value == "" {
		return value.NilValue
	}
	return value.MakeString(g.value)
}

// groupMethod makes a member function of a radio group. One that changes
// it needs Tk, which must not have ended (the variable is Tk's).
func groupMethod(name string, min, max int, needsTk bool, fn func(a native.Args, this *value.ObjectObj, g *radiogroup) (value.Value, error)) value.NativeMethod {
	full := "tk::Radiogroup::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		if needsTk && gui.ended {
			return value.NilValue, a.Errorf(TkError, "Tk has ended: the first window was closed")
		}
		v, err := fn(a, this, this.State.(*radiogroup))
		if terr := tkError(); err == nil && terr != nil {
			err = a.Errorf(TkError, "%v", terr)
		}
		return v, err
	}
}

// valueArg reads argument i, the value of a radio button: a string that is
// not empty.
func valueArg(a native.Args, i int) (string, error) {
	s, err := tkString(a, i)
	if err == nil && s == "" {
		err = a.Errorf(value.ValueError, "the value of a radio button cannot be \"\"")
	}
	return s, err
}

func registerChoices() {
	// new tk::Checkbutton(parent, text [, options]): checked(),
	// setchecked(b), onchange(f) (f gets checked() when the user changes
	// it), text(), settext(s); the options width, padding, state.
	registerWidget(Checkbutton, widgetSpec{1, 1, false, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, _ string) (err error) {
		if wd.text, err = stringArg(a, args, 0); err != nil {
			return err
		}
		wd.check = tk.Variable(0)
		wd.w = parent.w.TCheckbutton(append(opts, tk.Txt(wd.text), wd.check, tk.Onvalue(1), tk.Offvalue(0), tk.Command(func() {
			wd.checked = wd.check.Get() == "1"
			if !wd.onchange.IsNil() {
				call(wd.onchange, value.MakeBool(wd.checked))
			}
		}))...).Window
		return nil
	}}, func(wd *widget) string {
		return "tk::Checkbutton(" + value.Quote(wd.text) + ", " + value.MakeBool(wd.checked).String() + ")"
	})
	textMethods("Checkbutton", Checkbutton.Methods)
	Checkbutton.Methods["checked"] = widgetMethod("Checkbutton", "checked", 0, 0, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		return value.MakeBool(wd.checked), nil
	})
	Checkbutton.Methods["setchecked"] = widgetMethod("Checkbutton", "setchecked", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		if a.V[0].Kind() != value.Bool {
			return value.NilValue, a.TypeError(0, "true or false")
		}
		wd.checked = a.V[0].AsBool()
		if wd.checked {
			wd.check.Set(1)
		} else {
			wd.check.Set(0)
		}
		return value.ObjectValue(this), nil
	})
	Checkbutton.Methods["onchange"] = handlerSetter("Checkbutton", "onchange", func(wd *widget) *value.Value { return &wd.onchange })

	// new tk::Radiogroup([value]): value() (nil: none chosen),
	// setvalue(s or nil), onchange(f) (f gets value() when the user
	// chooses another radio button).
	Radiogroup.New = func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check("tk::Radiogroup::Radiogroup", args, 0, 1)
		if err != nil {
			return value.NilValue, err
		}
		if gui.ended {
			return value.NilValue, a.Errorf(TkError, "Tk has ended: the first window was closed")
		}
		g := &radiogroup{}
		s := ""
		if a.Has(0) && !a.V[0].IsNil() {
			if s, err = valueArg(a, 0); err != nil {
				return value.NilValue, err
			}
		}
		g.set(s)
		this.State = g
		return value.NilValue, nil
	}
	Radiogroup.Methods = map[string]value.NativeMethod{
		"value": groupMethod("value", 0, 0, false, func(a native.Args, this *value.ObjectObj, g *radiogroup) (value.Value, error) {
			return g.get(), nil
		}),
		"setvalue": groupMethod("setvalue", 1, 1, true, func(a native.Args, this *value.ObjectObj, g *radiogroup) (value.Value, error) {
			s := ""
			if !a.V[0].IsNil() {
				var err error
				if s, err = valueArg(a, 0); err != nil {
					return value.NilValue, err
				}
			}
			g.set(s)
			return value.ObjectValue(this), nil
		}),
		"onchange": groupMethod("onchange", 1, 1, true, func(a native.Args, this *value.ObjectObj, g *radiogroup) (value.Value, error) {
			f, err := handlerArg(a, 0)
			if err != nil {
				return value.NilValue, err
			}
			g.onchange = f
			return value.ObjectValue(this), nil
		}),
	}
	Radiogroup.Show = func(o *value.ObjectObj) string {
		return "tk::Radiogroup(" + o.State.(*radiogroup).get().Repr() + ")"
	}

	// new tk::Radiobutton(parent, text, group, value [, options]): chosen
	// when group's value is value; text(), settext(s); the options width,
	// padding, state.
	registerWidget(Radiobutton, widgetSpec{3, 3, false, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, _ string) (err error) {
		if wd.text, err = stringArg(a, args, 0); err != nil {
			return err
		}
		st, err := a.Object(2, Radiogroup)
		if err != nil {
			return err
		}
		if wd.value, err = valueArg(a, 3); err != nil {
			return err
		}
		g := st.(*radiogroup)
		wd.group = g
		wd.w = parent.w.TRadiobutton(append(opts, tk.Txt(wd.text), g.v, tk.Value(wd.value), tk.Command(func() { g.chose(wd.value) }))...).Window
		g.linked()
		return nil
	}}, func(wd *widget) string {
		return "tk::Radiobutton(" + value.Quote(wd.text) + ", " + value.Quote(wd.value) + ")"
	})
	textMethods("Radiobutton", Radiobutton.Methods)
}
