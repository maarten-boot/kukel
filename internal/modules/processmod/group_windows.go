//go:build windows

package processmod

import (
	"errors"
	"os/exec"

	"github.com/maarten-boot/kukel/internal/value"
)

// Windows has no process groups that a signal reaches: a child is ended
// alone (its own children are not; a job object would, later).
func setGroup(cmd *exec.Cmd, grouped bool) {}

func terminate(cmd *exec.Cmd, grouped bool) { kill(cmd, grouped) }

func kill(cmd *exec.Cmd, grouped bool) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}

// signalNumber: Windows has no signals.
func signalNumber(name string) int { return 0 }

// exitOf gives the exit code; Windows has no signals, so a process that
// was killed has code 1.
func exitOf(cmd *exec.Cmd, err error) (code, signal, signum value.Value) {
	ps := cmd.ProcessState
	if ps == nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return value.NilValue, value.NilValue, value.NilValue
		}
		ps = ee.ProcessState
	}
	return value.MakeInt(int64(ps.ExitCode())), value.NilValue, value.NilValue
}
