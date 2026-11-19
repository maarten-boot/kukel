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

// setGroup puts the child in a process group of its own.
func setGroup(cmd *exec.Cmd, grouped bool) {
	if grouped {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

// terminate sends TERM to the child, or to its whole group.
func terminate(cmd *exec.Cmd, grouped bool) { signalTo(cmd, grouped, syscall.SIGTERM) }

// kill sends KILL to the child, or to its whole group.
func kill(cmd *exec.Cmd, grouped bool) { signalTo(cmd, grouped, syscall.SIGKILL) }

func signalTo(cmd *exec.Cmd, grouped bool, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	if grouped {
		syscall.Kill(-cmd.Process.Pid, sig) // the group's id is the child's pid
		return
	}
	cmd.Process.Signal(sig)
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
