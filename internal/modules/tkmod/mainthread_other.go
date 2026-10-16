//go:build tk && !linux

package tkmod

// onMainThread cannot tell here without cgo: it trusts the caller (the
// interpreter must run in the main goroutine, which init locked to the main
// thread).
func onMainThread() bool { return true }
