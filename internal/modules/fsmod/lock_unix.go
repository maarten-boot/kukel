//go:build unix

package fsmod

import (
	"errors"
	"os"
	"syscall"
)

// openFlags are the flags for os.OpenFile of a file opened as mode.
func openFlags(mode string, flags int) int { return flags }

// tryLock takes or changes the lock on f without waiting (flock: advisory,
// between open files); false when another holds it. A change of kind that
// fails has given up the lock there was (flock removes it first): held
// says f had one, and kept whether it is taken again (without waiting).
func tryLock(f *os.File, exclusive, held bool) (ok, kept bool, err error) {
	if ok, err = flock(f, exclusive); ok || err != nil || !held {
		return ok, false, err
	}
	kept, _ = flock(f, !exclusive)
	return false, kept, nil
}

func flock(f *os.File, exclusive bool) (bool, error) {
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	if err != nil {
		return false, &os.PathError{Op: "flock", Path: f.Name(), Err: err}
	}
	return true, nil
}

// unlockFile gives up the lock on f.
func unlockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return &os.PathError{Op: "flock", Path: f.Name(), Err: err}
	}
	return nil
}
