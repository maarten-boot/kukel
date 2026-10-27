//go:build windows

package fsmod

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openFlags are the flags for os.OpenFile of a file opened as mode. Go
// opens an append-only file ("a") with neither read nor write access (only
// FILE_APPEND_DATA, so that writes go to the end), and LockFileEx needs one
// of them: it gets read access too (the fs::File still does not read).
func openFlags(mode string, flags int) int {
	if mode == "a" {
		return flags&^os.O_WRONLY | os.O_RDWR
	}
	return flags
}

// tryLock takes or changes the lock on f without waiting (LockFileEx:
// mandatory, the whole file); false when another holds it. Windows cannot
// change a lock's kind, so a held one (of the other kind) is given up
// first, and taken again when the new one is not to be had: as with flock,
// a change that fails keeps the lock there was.
func tryLock(f *os.File, exclusive, held bool) (bool, error) {
	if held {
		if err := unlockFile(f); err != nil {
			return false, err
		}
	}
	ok, err := lockFile(f, exclusive)
	if held && !ok {
		if _, rerr := lockFile(f, !exclusive); rerr != nil && err == nil {
			err = rerr
		}
	}
	return ok, err
}

func lockFile(f *os.File, exclusive bool) (bool, error) {
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
