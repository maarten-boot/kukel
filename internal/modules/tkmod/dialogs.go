//go:build tk

package tkmod

import (
	"slices"
	"strings"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// The dialogs are modal: until the user answers, the dialog has Tk's grab,
// so the program's other windows take no input (they still redraw), and
// the event loop does not run, so timers and other events wait.

func text(v value.Value) (any, string) {
	if v.Kind() != value.String {
		return nil, " must be a string, not " + v.Kind().String()
	}
	if bad := tkUnsafe(v.AsString()); bad != "" {
		return nil, " holds " + bad
	}
	return v.AsString(), ""
}

// openWindow is an option whose value is an open tk::Window: the parent of
// a dialog, which shows on top of it.
func openWindow(v value.Value) (any, string) {
	if !Window.Is(v) || v.AsObject().State.(*window).closed {
		return nil, " must be an open tk::Window, not " + v.Repr()
	}
	return v.AsObject().State.(*window).w, ""
}

// fileTypes is the option types: a vector of [name, pattern...] vectors,
// such as [["Kukel", ".kkl"], ["All files", "*"]].
func fileTypes(v value.Value) (any, string) {
	const want = ` must be a vector of [name, pattern...] vectors of strings, such as [["Kukel", ".kkl"], ["All files", "*"]], not `
	if v.Kind() != value.Vector {
		return nil, want + v.Repr()
	}
	var types []tk.FileType
	for _, t := range v.AsVector().Elems {
		if t.Kind() != value.Vector || len(t.AsVector().Elems) < 2 {
			return nil, want + v.Repr()
		}
		var s []string
		for _, e := range t.AsVector().Elems {
			if e.Kind() != value.String || e.AsString() == "" || tkUnsafe(e.AsString()) != "" {
				return nil, want + v.Repr()
			}
			s = append(s, e.AsString())
		}
		types = append(types, tk.FileType{TypeName: s[0], Extensions: s[1:]})
	}
	return types, ""
}

var (
	optTitle  = option{"title", text, func(x any) tk.Opt { return tk.Title(x) }}
	optParent = option{"parent", openWindow, func(x any) tk.Opt { return tk.Parent(x) }}
	optDir    = option{"dir", text, func(x any) tk.Opt { return tk.Initialdir(x) }}
	optFile   = option{"file", text, func(x any) tk.Opt { return tk.Initialfile(x) }}
	optTypes  = option{"types", fileTypes, func(x any) tk.Opt { return tk.Filetypes(x) }}

	messageboxOptions = []option{
		optTitle,
		{"detail", text, func(x any) tk.Opt { return tk.Detail(x) }},
		{"icon", oneOf("info", "warning", "error", "question"), func(x any) tk.Opt { return tk.Icon(x) }},
		{"buttons", oneOf("ok", "okcancel", "yesno", "yesnocancel", "retrycancel", "abortretryignore"), func(x any) tk.Opt { return tk.Type(x) }},
		{"default", oneOf("ok", "cancel", "yes", "no", "retry", "abort", "ignore"), func(x any) tk.Opt { return tk.Default(x) }},
		optParent,
	}
	openfileOptions = []option{optTitle, optDir, optFile, optTypes,
		{"multiple", boolean, func(x any) tk.Opt { return tk.Multiple(x) }},
		optParent}
	savefileOptions = []option{optTitle, optDir, optFile, optTypes,
		{"ext", text, func(x any) tk.Opt { return tk.Defaultextension(x) }},
		optParent}
	choosedirOptions = []option{optTitle, optDir,
		{"mustexist", boolean, func(x any) tk.Opt { return tk.Mustexist(x) }},
		optParent}
)

// dialog makes a module function that shows a dialog: its arguments are
// the strings named in texts, then a map of options; check (if not nil)
// checks what the options cannot alone, and show shows it.
func dialog(name string, texts int, options []option, check func(a native.Args) error, show func(a native.Args, opts []tk.Opt) (value.Value, error)) native.Func {
	full := "tk::" + name
	return func(env value.Env, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, texts, texts+1)
		if err != nil {
			return value.NilValue, err
		}
		for i := 0; i < texts; i++ {
			if _, err := tkString(a, i); err != nil {
				return value.NilValue, err
			}
		}
		var opts []tk.Opt
		if a.Has(texts) {
			if opts, err = readOptions(a, texts, options); err != nil {
				return value.NilValue, err
			}
			if check != nil {
				if err := check(a); err != nil {
					return value.NilValue, err
				}
			}
		}
		if err := start(a, env); err != nil {
			return value.NilValue, err
		}
		native.Flush(env) // it waits for the user
		v, err := show(a, opts)
		if terr := tkError(); err == nil && terr != nil {
			err = a.Errorf(TkError, "%v", terr)
		}
		return v, err
	}
}

// messageButtons are the buttons of a message box, by its option buttons.
var messageButtons = map[string][]string{
	"ok":               {"ok"},
	"okcancel":         {"ok", "cancel"},
	"yesno":            {"yes", "no"},
	"yesnocancel":      {"yes", "no", "cancel"},
	"retrycancel":      {"retry", "cancel"},
	"abortretryignore": {"abort", "retry", "ignore"},
}

// defaultButton checks that the option default of a message box (its
// options are argument 1) is one of its buttons: Tk's message would name
// Tcl's options.
func defaultButton(a native.Args) error {
	def := mapGet(a.V[1], "default")
	if def.IsNil() {
		return nil
	}
	kind := "ok"
	if b := mapGet(a.V[1], "buttons"); !b.IsNil() {
		kind = b.AsString() // checked already
	}
	if buttons := messageButtons[kind]; !slices.Contains(buttons, def.AsString()) {
		if len(buttons) == 1 {
			return a.Errorf(value.ValueError, "option default: %s is not a button (the only button is %s)", value.Quote(def.AsString()), buttons[0])
		}
		return a.Errorf(value.ValueError, "option default: %s is not one of the buttons %s", value.Quote(def.AsString()), strings.Join(buttons, ", "))
	}
	return nil
}

// pathOrNil is the answer of a file dialog: nil when cancelled.
func pathOrNil(s string) value.Value {
	if s == "" {
		return value.NilValue
	}
	return value.MakeString(s)
}

func dialogFuncs() map[string]native.Func {
	return map[string]native.Func{
		// messagebox(text [, options]): the name of the button pressed.
		"messagebox": dialog("messagebox", 1, messageboxOptions, defaultButton, func(a native.Args, opts []tk.Opt) (value.Value, error) {
			return value.MakeString(tk.MessageBox(append(opts, tk.Msg(a.V[0].AsString()))...)), nil
		}),
		// openfile([options]): the path of the file chosen; with multiple, a
		// vector of paths; nil when cancelled.
		"openfile": dialog("openfile", 0, openfileOptions, nil, func(a native.Args, opts []tk.Opt) (value.Value, error) {
			paths := tk.GetOpenFile(opts...)
			multiple := a.Has(0) && mapGet(a.V[0], "multiple").Kind() == value.Bool && mapGet(a.V[0], "multiple").AsBool()
			if len(paths) == 0 || len(paths) == 1 && paths[0] == "" {
				return value.NilValue, nil
			}
			if !multiple {
				return value.MakeString(paths[0]), nil
			}
			v := make([]value.Value, len(paths))
			for i, p := range paths {
				v[i] = value.MakeString(p)
			}
			return value.MakeVector(v), nil
		}),
		// savefile([options]): the path to save to; nil when cancelled.
		"savefile": dialog("savefile", 0, savefileOptions, nil, func(a native.Args, opts []tk.Opt) (value.Value, error) {
			return pathOrNil(tk.GetSaveFile(opts...)), nil
		}),
		// choosedir([options]): the directory chosen; nil when cancelled.
		"choosedir": dialog("choosedir", 0, choosedirOptions, nil, func(a native.Args, opts []tk.Opt) (value.Value, error) {
			return pathOrNil(tk.ChooseDirectory(opts...)), nil
		}),
	}
}
