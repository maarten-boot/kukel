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
// a tk::Radiogroup holds (what its buttons show), not the module's record.
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
		},
	})
}
