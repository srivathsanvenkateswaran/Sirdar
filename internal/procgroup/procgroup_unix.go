//go:build !windows

package procgroup

import (
	"os/exec"
	"syscall"
)

func setup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
