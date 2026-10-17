//go:build tk

package tkmod

import (
	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Menu is the class tk::Menu: the menu bar of a window, or a menu in it.
var Menu = &native.Class{Name: "Menu"}

// menu is the state of a tk::Menu.
type menu struct {
	m        *tk.MenuWidget
	win      *window
	label    string // of a submenu ("" for a menu bar)
	labels   []string
	handlers map[string]value.Value // of the items, by label
	submenus map[string]bool
}

// add notes an entry with label; labels are unique in a menu, since the
// program names entries by them.
func (mn *menu) add(a native.Args, label string) error {
	if label == "" {
		return a.Errorf(value.ValueError, "the label of an entry cannot be \"\"")
	}
	if mn.handlers[label].Kind() != value.Nil || mn.submenus[label] {
		return a.Errorf(value.ValueError, "the menu has an entry %s already", value.Quote(label))
	}
	mn.labels = append(mn.labels, label)
	return nil
}

// index is the position of the entry label in the menu (separators
// count), or -1.
func (mn *menu) index(label string) int {
	for i, l := range mn.labels {
		if l == label {
			return i
		}
	}
	return -1
}

func newMenu(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
	a, err := native.Check("tk::Menu::Menu", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	st, err := a.Object(0, Window)
	if err != nil {
		return value.NilValue, err
	}
	win := st.(*window)
	if err := start(a, env); err != nil {
		return value.NilValue, err
	}
	switch {
	case win.closed:
		return value.NilValue, a.Errorf(value.ValueError, "the window is closed")
	case win.menubar:
		return value.NilValue, a.Errorf(value.ValueError, "the window has a menu bar already")
	}
	mn := &menu{win: win, handlers: map[string]value.Value{}, submenus: map[string]bool{}}
	mn.m = win.w.Menu(tk.Tearoff(false))
	win.w.Configure(tk.Mnu(mn.m))
	if err := tkError(); err != nil {
		return value.NilValue, a.Errorf(TkError, "%v", err)
	}
	win.menubar = true
	this.State = mn
	return value.NilValue, nil
}

// menuMethod makes a member function of a menu, which cannot be used once
// its window is closed.
func menuMethod(name string, min, max int, fn func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error)) value.NativeMethod {
	full := "tk::Menu::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		mn := this.State.(*menu)
		if mn.win.closed {
			return value.NilValue, a.Errorf(value.ValueError, "the window is closed")
		}
		v, err := fn(a, this, mn)
		if terr := tkError(); err == nil && terr != nil {
			err = a.Errorf(TkError, "%v", terr)
		}
		return v, err
	}
}

// labelArg reads argument 0, the label of an entry, which must be there.
func labelArg(a native.Args, mn *menu) (string, int, error) {
	label, err := a.String(0)
	if err != nil {
		return "", 0, err
	}
	i := mn.index(label)
	if i < 0 {
		return "", 0, a.Errorf(value.ValueError, "the menu has no entry %s", value.Quote(label))
	}
	return label, i, nil
}

var itemOptions = []option{
	{"accelerator", func(v value.Value) (any, string) {
		if v.Kind() != value.String {
			return nil, " must be a string, not " + v.Kind().String()
		}
		return v.AsString(), ""
	}, func(x any) tk.Opt { return tk.Accelerator(x) }},
}

func registerMenus() {
	Menu.New = newMenu
	Menu.Methods = map[string]value.NativeMethod{
		// submenu(label): a menu that opens from this one.
		"submenu": menuMethod("submenu", 1, 1, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			label, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			if err := mn.add(a, label); err != nil {
				return value.NilValue, err
			}
			sub := &menu{win: mn.win, label: label, handlers: map[string]value.Value{}, submenus: map[string]bool{}}
			sub.m = mn.m.Menu(tk.Tearoff(false))
			mn.m.AddCascade(tk.Lbl(label), tk.Mnu(sub.m))
			mn.submenus[label] = true
			return Menu.Object(sub), nil
		}),
		// item(label, f [, options]): an entry that calls f.
		"item": menuMethod("item", 2, 3, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			label, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			if !callable(a.V[1]) {
				return value.NilValue, a.TypeError(1, "a function")
			}
			var opts []tk.Opt
			if a.Has(2) {
				if opts, err = readOptions(a, 2, itemOptions); err != nil {
					return value.NilValue, err
				}
			}
			if err := mn.add(a, label); err != nil {
				return value.NilValue, err
			}
			mn.handlers[label] = a.V[1]
			mn.m.AddCommand(append(opts, tk.Lbl(label), tk.Command(func() { call(mn.handlers[label]) }))...)
			return value.ObjectValue(this), nil
		}),
		"separator": menuMethod("separator", 0, 0, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			mn.labels = append(mn.labels, "") // keeps the indexes of the entries
			mn.m.AddSeparator()
			return value.ObjectValue(this), nil
		}),
		// enable(label, on): whether the entry can be chosen.
		"enable": menuMethod("enable", 2, 2, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			_, i, err := labelArg(a, mn)
			if err != nil {
				return value.NilValue, err
			}
			if a.V[1].Kind() != value.Bool {
				return value.NilValue, a.TypeError(1, "true or false")
			}
			state := "disabled"
			if a.V[1].AsBool() {
				state = "normal"
			}
			mn.m.EntryConfigure(i, tk.State(state))
			return value.ObjectValue(this), nil
		}),
		// invoke(label) calls the entry's handler as choosing it would, at
		// once: its result and exception come back to the caller.
		"invoke": menuMethod("invoke", 1, 1, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			label, _, err := labelArg(a, mn)
			if err != nil {
				return value.NilValue, err
			}
			if mn.submenus[label] {
				return value.NilValue, a.Errorf(value.ValueError, "%s is a submenu, not an item", value.Quote(label))
			}
			return gui.env.Call(mn.handlers[label], nil)
		}),
	}
	Menu.Show = func(o *value.ObjectObj) string {
		mn := o.State.(*menu)
		if mn.label == "" {
			return "tk::Menu"
		}
		return "tk::Menu(" + value.Quote(mn.label) + ")"
	}
}
