//go:build tk

package tkmod

import "syscall"

// onMainThread tells whether the calling goroutine runs on the process's
// main thread (whose thread id is the process id).
func onMainThread() bool { return syscall.Gettid() == syscall.Getpid() }
