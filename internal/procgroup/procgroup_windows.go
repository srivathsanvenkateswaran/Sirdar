//go:build windows

package procgroup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// stillActive is STILL_ACTIVE from the Windows SDK (winbase.h): the exit
// code GetExitCodeProcess reports for a process that has not yet
// terminated. x/sys/windows does not expose it as a named constant.
const stillActive = 259

// Windows has no Unix-style process group reachable by a negative pid, so
// there is nothing to configure before the child starts. The subtree is
// dealt with at kill time instead; see kill.
func setup(cmd *exec.Cmd) {}

// kill takes down the child and everything below it. `taskkill /T` walks
// the parent-pid chain and kills each descendant, which is what the Unix
// side gets from signalling a process group: without it a command run
// through a shell wrapper — `sh -c "rg ... | head"`, an `npx` that forks a
// node — leaves grandchildren holding the output pipe open, and Wait stays
// blocked on them long after the process we started is gone.
//
// Two things it does not do, both inherent to walking parent pids rather
// than holding a kernel object: a descendant that re-parents itself
// (a detached service, a process whose parent exited first) is out of
// reach, and a pid recycled between the child exiting and this call could
// name someone else's process. A Job Object would close both, at the cost
// of machinery os/exec gives no hook for — the handle has to be assigned
// between CreateProcess and the child's first spawn, and exec.Cmd hands
// out neither the suspended thread nor a post-start callback.
//
// Falling back to killing the one process keeps the old behaviour for the
// case where taskkill cannot be found or reports failure.
func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if pid := cmd.Process.Pid; pid > 0 {
		if err := killTree(pid); err == nil {
			return nil
		}
	}
	return cmd.Process.Kill()
}

// killTree is the taskkill call, a variable so a test can record it.
var killTree = func(pid int) error {
	exe, err := taskkillPath()
	if err != nil {
		return err
	}
	kill := exec.Command(exe, taskkillArgs(pid)...)
	// No console window flashes up for what is a background cleanup.
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return kill.Run()
}

// taskkillArgs is split out so the argument order is pinned by a test
// rather than by reading it back off the line above.
//
// /F is unconditional: this is the strongest-signal path, the caller has
// already decided the command is over, and taskkill's polite form only
// posts WM_CLOSE, which a console process ignores.
func taskkillArgs(pid int) []string {
	return []string{"/PID", strconv.Itoa(pid), "/T", "/F"}
}

// taskkillPath prefers the copy in the system directory over whatever PATH
// happens to resolve: this runs to clean up after a command the model
// asked for, and a taskkill.exe dropped into the workspace must not be the
// one that gets a process handle.
func taskkillPath() (string, error) {
	if root := os.Getenv("SystemRoot"); root != "" {
		full := filepath.Join(root, "System32", "taskkill.exe")
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}
	return exec.LookPath("taskkill")
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
