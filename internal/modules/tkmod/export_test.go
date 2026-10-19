//go:build tk

package tkmod

import (
	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// CloseAll closes all windows, as a user closing the first one would. It
// runs on the interpreter's goroutine (posted to its event loop).
func CloseAll() {
	for win := range gui.open {
		if win.root {
			tk.Destroy(win.w)
			tkError()
			win.gone()
		}
	}
}

// For the tests only: tktest::tkvalue(group) is the value Tk's variable of
// a tk::Radiogroup holds (what its buttons show), not the module's record;
// tktest::scalelabels(scale) gives, as Tk has them, the state of the label
// above a tk::Scale, and the width and state of the value beside it (nil
// when it has none).
func init() {
	native.Register(&native.Module{
		Name: "tktest",
		Funcs: map[string]native.Func{
			"tkvalue": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("tktest::tkvalue", args, 1, 1)
				if err != nil {
					return value.NilValue, err
				}
				st, err := a.Object(0, Radiogroup)
				if err != nil {
					return value.NilValue, err
				}
				return value.MakeString(st.(*radiogroup).v.Get()), nil
			},
			"scalelabels": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("tktest::scalelabels", args, 1, 1)
				if err != nil {
					return value.NilValue, err
				}
				st, err := a.Object(0, Scale)
				if err != nil {
					return value.NilValue, err
				}
				s := st.(*widget).slider
				v := []value.Value{value.NilValue, value.NilValue, value.NilValue}
				if s.label != nil {
					v[0] = value.MakeString(s.label.State())
				}
				if s.shown != nil {
					v[1], v[2] = value.MakeString(s.shown.Width()), value.MakeString(s.shown.State())
				}
				return value.MakeVector(v), nil
			},
		},
	})
}
