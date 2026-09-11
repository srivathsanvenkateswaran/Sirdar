//go:build windows

package procgroup

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// stillActive is STILL_ACTIVE from the Windows SDK (winbase.h): the exit
// code GetExitCodeProcess reports for a process that has not yet
// terminated. x/sys/windows does not expose it as a named constant.
const stillActive = 259

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

// alive reports whether pid names a running process. It opens the process
// with the lightest access right that still permits querying its exit
// code, rather than going through os.FindProcess: on Windows the latter
// only ever succeeds or fails at open time and gives no live-liveness
// check afterward, so a still-open handle to an already-exited process
// would otherwise read as alive.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		// Handle opened but the exit code couldn't be read; treat the
		// process as alive rather than silently sweeping a live lock.
		return true
	}
	return code == stillActive
}
