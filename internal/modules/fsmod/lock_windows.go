//go:build windows

package fsmod

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock takes or changes the lock on f without waiting (LockFileEx:
// mandatory, the whole file); false when another holds it. Windows cannot
// change a lock's kind, so a held one is given up first.
func tryLock(f *os.File, exclusive, held bool) (bool, error) {
	if held {
		if err := unlockFile(f); err != nil {
			return false, err
		}
	}
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	if err != nil {
		return false, &os.PathError{Op: "LockFileEx", Path: f.Name(), Err: err}
	}
	return true, nil
}

// unlockFile gives up the lock on f.
func unlockFile(f *os.File) error {
	if err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), new(windows.Overlapped)); err != nil {
		return &os.PathError{Op: "UnlockFileEx", Path: f.Name(), Err: err}
	}
	return nil
}
