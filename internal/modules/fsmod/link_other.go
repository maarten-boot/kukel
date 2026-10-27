//go:build !windows

package fsmod

// linkLoop reports whether err is Windows' word for a loop of symbolic
// links; elsewhere a loop is ELOOP.
func linkLoop(err error) bool { return false }
