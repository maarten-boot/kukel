//go:build windows

package fsmod

import (
	"errors"

	"golang.org/x/sys/windows"
)

// linkLoop reports whether err is Windows' word for a loop of symbolic
// links (Unix says ELOOP).
func linkLoop(err error) bool { return errors.Is(err, windows.ERROR_CANT_RESOLVE_FILENAME) }
