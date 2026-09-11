//go:build windows

package procgroup

import "os/exec"

// Windows has no Unix-style process group reachable by a negative pid, and
// standing up the Job Object machinery that would give it one is more than
// this needs. Killing the immediate child is the best this platform offers
// here; a grandchild spawned through a shell wrapper that outlives it is
// not reaped.
func setup(cmd *exec.Cmd) {}

func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
