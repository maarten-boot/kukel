//go:build !unix && !windows

package fsmod

import (
	"errors"
	"os"
)

var errNoLocks = errors.New("locks are not available on this system")

func tryLock(f *os.File, exclusive, held bool) (bool, error) {
	return false, &os.PathError{Op: "lock", Path: f.Name(), Err: errNoLocks}
}

func unlockFile(f *os.File) error {
	return &os.PathError{Op: "unlock", Path: f.Name(), Err: errNoLocks}
}
