//go:build !windows

package termmod

import "os"

// enableVT makes the terminal of f understand ANSI escape codes, and gives
// what undoes it: nothing to do outside Windows.
func enableVT(f *os.File) func() { return func() {} }
