//go:build windows

package processmod

import (
	"errors"
	"os/exec"

	"github.com/maarten-boot/kukel/internal/value"
)

// Windows has no process groups that a signal reaches: a child is ended
// alone (its own children are not; a job object would, later).
func setGroup(cmd *exec.Cmd, grouped bool, pgid int) {}

// signals are the signals kill takes, by name: on Windows each ends the
// program.
var signals = map[string]bool{"TERM": true, "KILL": true, "INT": true, "HUP": true, "QUIT": true, "USR1": true, "USR2": true}

func signalJob(j *job, name string) {
	for _, cmd := range j.cmds {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
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
