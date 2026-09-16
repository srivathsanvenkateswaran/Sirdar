//go:build !windows

package procgroup

import (
	"os/exec"
	"syscall"
)

func setup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// interrupt sends SIGINT to the process this program started, and to that
// process only. It is deliberately narrower than kill, which takes the
// whole group: SIGINT is a request, and the process that was asked is the
// one that knows which of its own children to pass it on to and which to
// leave alone. Signalling the group would also mean signalling a pid that
// may not lead one — the callers that never went through setup run inside
// this program's own group, and -pid would then name someone else's
// group, or nothing at all.
func interrupt(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGINT)
}

func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if pid := cmd.Process.Pid; pid > 0 {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
			return nil
		}
	}
	return cmd.Process.Kill()
}

// alive reports whether pid names a running process. Signal 0 delivers
// nothing; the kernel call still fails with ESRCH when no process by that
// pid exists. EPERM means a process by that pid does exist but is owned by
// someone else, so it counts as alive too.
func alive(pid int) bool {
	err := syscall.Kill(pid, syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}
