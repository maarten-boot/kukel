//go:build unix

package processmod

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/maarten-boot/kukel/internal/value"
)

// setGroup puts the child in a process group: a new one (pgid 0), or that
// of the pipeline's first program.
func setGroup(cmd *exec.Cmd, grouped bool, pgid int) {
	if grouped {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}
	}
}

// signals are the signals kill takes, by name.
var signals = map[string]syscall.Signal{
	"TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL, "INT": syscall.SIGINT, "HUP": syscall.SIGHUP,
	"QUIT": syscall.SIGQUIT, "USR1": syscall.SIGUSR1, "USR2": syscall.SIGUSR2,
}

// signalJob sends a signal to the job's group, or to each of its programs
// when they have none of their own.
func signalJob(j *job, name string) {
	sig := signals[name]
	if j.grouped && j.pgid > 0 {
		syscall.Kill(-j.pgid, sig)
		return
	}
	for _, cmd := range j.cmds {
		if cmd.Process != nil {
			cmd.Process.Signal(sig)
		}
	}
}

// signalNumber gives the number of a signal by its name without SIG, or 0.
func signalNumber(name string) int { return int(unix.SignalNum("SIG" + name)) }

// exitOf gives the exit code, or the signal that ended the process (its
// name without SIG, and its number).
func exitOf(cmd *exec.Cmd, err error) (code, signal, signum value.Value) {
	code, signal, signum = value.NilValue, value.NilValue, value.NilValue
	ps := cmd.ProcessState
	if ps == nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return
		}
		ps = ee.ProcessState
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		name := strings.TrimPrefix(unix.SignalName(ws.Signal()), "SIG")
		if name == "" {
			name = ws.Signal().String()
		}
		return code, value.MakeString(name), value.MakeInt(int64(ws.Signal()))
	}
	return value.MakeInt(int64(ps.ExitCode())), signal, signum
}
