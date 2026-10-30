package termmod

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT makes the console of f understand ANSI escape codes (cursor
// moves, colours), and gives what undoes it.
func enableVT(f *os.File) func() {
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return func() {}
	}
	windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.ENABLE_PROCESSED_OUTPUT)
	return func() { windows.SetConsoleMode(h, mode) }
}
