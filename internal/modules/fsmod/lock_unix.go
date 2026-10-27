//go:build unix

package fsmod

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes or changes the lock on f without waiting (flock: advisory,
// between open files); false when another holds it.
func tryLock(f *os.File, exclusive, held bool) (bool, error) {
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
