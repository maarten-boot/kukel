//go:build tk

package tkmod

import tk "modernc.org/tk9.0"

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
