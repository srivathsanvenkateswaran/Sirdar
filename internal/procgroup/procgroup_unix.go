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
