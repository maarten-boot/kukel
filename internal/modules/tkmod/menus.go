//go:build tk

package tkmod

import (
	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Menu is the class tk::Menu: the menu bar of a window, a menu in it, or a
// pop-up menu.
var Menu = &native.Class{Name: "Menu"}

// menu is the state of a tk::Menu.
type menu struct {
	m       *tk.MenuWidget
	win     *window
	label   string // of a submenu ("" for a menu bar or pop-up menu)
	popup   bool
	labels  []string // of the entries in order; "" for a separator
	entries map[string]*entry
}

// entry is an entry of a menu, other than a separator.
type entry struct {
	kind    string      // "item", "check", "radio" or "submenu"
	handler value.Value // item: f(); check: f(checked)
	check   *tk.VariableOpt
	checked bool
	group   *radiogroup // radio
	value   string      // radio
}

func newMenuState(win *window, label string) *menu {
	return &menu{win: win, label: label, entries: map[string]*entry{}}
}

// add adds an entry with label; labels are unique in a menu, since the
// program names entries by them.
func (mn *menu) add(a native.Args, label string, e *entry) error {
	if label == "" {
		return a.Errorf(value.ValueError, "the label of an entry cannot be \"\"")
	}
	if mn.entries[label] != nil {
		return a.Errorf(value.ValueError, "the menu has an entry %s already", value.Quote(label))
	}
	mn.labels = append(mn.labels, label)
	mn.entries[label] = e
	return nil
}

// index is the position of the entry label in the menu (separators
// count, but are not found: their label is ""), or -1.
func (mn *menu) index(label string) int {
	for i, l := range mn.labels {
		if l == label && label != "" {
			return i
		}
	}
	return -1
}

var menuOptions = []option{{"popup", boolean, nil}}

// new tk::Menu(window [, {"popup": true}]): the window's menu bar (one per
// window), or a pop-up menu (any number), shown with popup(x, y).
func newMenu(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
	a, err := native.Check("tk::Menu::Menu", args, 1, 2)
	if err != nil {
		return value.NilValue, err
	}
	st, err := a.Object(0, Window)
	if err != nil {
		return value.NilValue, err
	}
	win := st.(*window)
	popup := false
	if a.Has(1) {
		values, err := readValues(a, 1, menuOptions)
		if err != nil {
			return value.NilValue, err
		}
		for _, v := range values {
			popup = v.x.(bool)
		}
	}
	if err := start(a, env); err != nil {
		return value.NilValue, err
	}
	switch {
	case win.closed:
		return value.NilValue, a.Errorf(value.ValueError, "the window is closed")
	case win.menubar && !popup:
		return value.NilValue, a.Errorf(value.ValueError, "the window has a menu bar already")
	}
	mn := newMenuState(win, "")
	mn.popup = popup
	mn.m = win.w.Menu(tk.Tearoff(false))
	if !popup {
		win.w.Configure(tk.Mnu(mn.m))
		win.menubar = true
	}
	if err := tkError(); err != nil {
		return value.NilValue, a.Errorf(TkError, "%v", err)
	}
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
	label, err := tkString(a, 0)
	if err != nil {
		return "", 0, err
	}
	i := mn.index(label)
	if i < 0 {
		return "", 0, a.Errorf(value.ValueError, "the menu has no entry %s", value.Quote(label))
	}
	return label, i, nil
}

// checkArg reads argument 0, the label of a check entry.
func checkArg(a native.Args, mn *menu) (*entry, int, error) {
	label, i, err := labelArg(a, mn)
	if err != nil {
		return nil, 0, err
	}
	e := mn.entries[label]
	if e.kind != "check" {
		return nil, 0, a.Errorf(value.ValueError, "%s is not a check entry", value.Quote(label))
	}
	return e, i, nil
}

var itemOptions = []option{
	{"accelerator", func(v value.Value) (any, string) {
		if v.Kind() != value.String {
			return nil, " must be a string, not " + v.Kind().String()
		}
		if bad := tkUnsafe(v.AsString()); bad != "" {
			return nil, " holds " + bad
		}
		return v.AsString(), ""
	}, func(x any) tk.Opt { return tk.Accelerator(x) }},
}

// itemArgs reads the label (argument 0) and the options (argument at, if
// there) of a new entry.
func itemArgs(a native.Args, at int) (string, []tk.Opt, error) {
	label, err := tkString(a, 0)
	if err != nil {
		return "", nil, err
	}
	var opts []tk.Opt
	if a.Has(at) {
		if opts, err = readOptions(a, at, itemOptions); err != nil {
			return "", nil, err
		}
	}
	return label, opts, nil
}

// toggle flips a check entry as a click does, and gives the new state.
func (e *entry) toggle() value.Value {
	e.checked = !e.checked
	e.check.Set(map[bool]int{false: 0, true: 1}[e.checked])
	return value.MakeBool(e.checked)
}

func registerMenus() {
	Menu.New = newMenu
	Menu.Methods = map[string]value.NativeMethod{
		// submenu(label): a menu that opens from this one.
		"submenu": menuMethod("submenu", 1, 1, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			label, err := tkString(a, 0)
			if err != nil {
				return value.NilValue, err
			}
			if err := mn.add(a, label, &entry{kind: "submenu"}); err != nil {
				return value.NilValue, err
			}
			sub := newMenuState(mn.win, label)
			sub.m = mn.m.Menu(tk.Tearoff(false))
			mn.m.AddCascade(tk.Lbl(label), tk.Mnu(sub.m))
			return Menu.Object(sub), nil
		}),
		// item(label, f [, options]): an entry that calls f.
		"item": menuMethod("item", 2, 3, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			label, opts, err := itemArgs(a, 2)
			if err != nil {
				return value.NilValue, err
			}
			if !callable(a.V[1]) {
				return value.NilValue, a.TypeError(1, "a function")
			}
			e := &entry{kind: "item", handler: a.V[1]}
			if err := mn.add(a, label, e); err != nil {
				return value.NilValue, err
			}
			mn.m.AddCommand(append(opts, tk.Lbl(label), tk.Command(func() { call(e.handler) }))...)
			return value.ObjectValue(this), nil
		}),
		// check(label, f [, checked [, options]]): an entry that is on or
		// off; f(checked) is called when the user changes it (nil: none).
		"check": menuMethod("check", 2, 4, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			label, opts, err := itemArgs(a, 3)
			if err != nil {
				return value.NilValue, err
			}
			f, err := handlerArg(a, 1)
			if err != nil {
				return value.NilValue, err
			}
			e := &entry{kind: "check", handler: f}
			if a.Has(2) {
				if a.V[2].Kind() != value.Bool {
					return value.NilValue, a.TypeError(2, "true or false")
				}
				e.checked = a.V[2].AsBool()
			}
			if err := mn.add(a, label, e); err != nil {
				return value.NilValue, err
			}
			e.check = tk.Variable(map[bool]int{false: 0, true: 1}[e.checked])
			mn.m.AddCheckbutton(append(opts, tk.Lbl(label), tk.Onvalue(1), tk.Offvalue(0), tk.Command(func() {
				e.checked = e.check.Get() == "1"
				if !e.handler.IsNil() {
					call(e.handler, value.MakeBool(e.checked))
				}
			}))...)
			mn.m.EntryConfigure(len(mn.labels)-1, e.check) // a variable cannot be given when adding
			return value.ObjectValue(this), nil
		}),
		// radio(label, group, value [, options]): an entry of a
		// tk::Radiogroup, chosen when the group's value is value.
		"radio": menuMethod("radio", 3, 4, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			label, opts, err := itemArgs(a, 3)
			if err != nil {
				return value.NilValue, err
			}
			st, err := a.Object(1, Radiogroup)
			if err != nil {
				return value.NilValue, err
			}
			val, err := valueArg(a, 2)
			if err != nil {
				return value.NilValue, err
			}
			g := st.(*radiogroup)
			e := &entry{kind: "radio", group: g, value: val}
			if err := mn.add(a, label, e); err != nil {
				return value.NilValue, err
			}
			mn.m.AddRadiobutton(append(opts, tk.Lbl(label), tk.Value(val), tk.Command(func() { g.chose(val) }))...)
			mn.m.EntryConfigure(len(mn.labels)-1, g.v)
			g.linked()
			return value.ObjectValue(this), nil
		}),
		// checked(label), setchecked(label, b): of a check entry.
		"checked": menuMethod("checked", 1, 1, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			e, _, err := checkArg(a, mn)
			if err != nil {
				return value.NilValue, err
			}
			return value.MakeBool(e.checked), nil
		}),
		"setchecked": menuMethod("setchecked", 2, 2, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			e, _, err := checkArg(a, mn)
			if err != nil {
				return value.NilValue, err
			}
			if a.V[1].Kind() != value.Bool {
				return value.NilValue, a.TypeError(1, "true or false")
			}
			if e.checked != a.V[1].AsBool() {
				e.toggle()
			}
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
		// invoke(label) does what choosing the entry does, at once: an item
		// calls its handler, a check entry toggles and calls its handler, a
		// radio entry is chosen and calls the group's; the handler's
		// result and exception come back to the caller.
		"invoke": menuMethod("invoke", 1, 1, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			label, _, err := labelArg(a, mn)
			if err != nil {
				return value.NilValue, err
			}
			switch e := mn.entries[label]; e.kind {
			case "item":
				return gui.env.Call(e.handler, nil)
			case "check":
				on := e.toggle()
				if e.handler.IsNil() {
					return value.NilValue, nil
				}
				return gui.env.Call(e.handler, []value.Value{on})
			case "radio":
				e.group.set(e.value)
				if e.group.onchange.IsNil() {
					return value.NilValue, nil
				}
				return gui.env.Call(e.group.onchange, []value.Value{value.MakeString(e.value)})
			}
			return value.NilValue, a.Errorf(value.ValueError, "%s is a submenu, not an item", value.Quote(label))
		}),
		// popup(x, y): shows a pop-up menu with its corner at x, y on the
		// screen (e["xroot"], e["yroot"] of a mouse event).
		"popup": menuMethod("popup", 2, 2, func(a native.Args, this *value.ObjectObj, mn *menu) (value.Value, error) {
			if !mn.popup {
				return value.NilValue, a.Errorf(value.ValueError, "this is not a pop-up menu (new tk::Menu(window, {\"popup\": true}))")
			}
			var p [2]int64
			for i := range p {
				x, err := a.Int(i)
				if err != nil {
					return value.NilValue, err
				}
				if x < -maxPixels || x > 10*maxPixels {
					return value.NilValue, a.Errorf(value.ValueError, "argument %d must be a position on the screen, not %d", i+1, x)
				}
				p[i] = x
			}
			tk.Popup(mn.m.Window, int(p[0]), int(p[1]), nil)
			return value.ObjectValue(this), nil
		}),
	}
	Menu.Show = func(o *value.ObjectObj) string {
		mn := o.State.(*menu)
		switch {
		case mn.popup:
			return "tk::Menu(popup)"
		case mn.label == "":
			return "tk::Menu"
		}
		return "tk::Menu(" + value.Quote(mn.label) + ")"
	}
}
