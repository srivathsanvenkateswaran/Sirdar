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

// setup puts the child in a console process group of its own. Windows has
// no Unix-style process group reachable by a negative pid, so this buys
// nothing at kill time — the subtree is dealt with by walking parent pids
// instead; see kill. What it buys is an address for interrupt: a console
// control event can only be sent to a process group, and the only group
// this program can name is one it asked for at CreateProcess time, whose
// id is then the child's own pid. Without the flag the sole addressable
// group is group 0, every process sharing the console, which includes
// Sirdar itself.
//
// The flag has to be set before the child starts, which is why it lives
// here rather than in interrupt.
//
// It costs the child the console's own Ctrl-C: a process in a group
// created this way starts with Ctrl-C handling disabled, so an operator
// pressing Ctrl-C in the terminal running Sirdar no longer reaches it
// directly. That is the same trade the Unix side already makes — Setpgid
// takes the child out of the terminal's foreground group, where a
// terminal-generated SIGINT is delivered — and in both cases the child is
// stopped through Sirdar's own cancellation path instead, which is
// Interrupt followed by Kill.
func setup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// interrupt sends the child's process group a Ctrl+Break, the closest
// thing Windows has to SIGINT. Go's runtime turns CTRL_BREAK_EVENT into
// os.Interrupt in a Go child, and the C runtime raises SIGBREAK in a
// native one, so a CLI that installs a handler gets its chance to write a
// final line before kill takes the tree.
//
// CTRL_BREAK rather than CTRL_C because CTRL_C cannot be delivered to a
// group created with CREATE_NEW_PROCESS_GROUP at all: such a group starts
// with Ctrl-C handling disabled, and GenerateConsoleCtrlEvent's own
// documentation says the event is simply not sent. CTRL_BREAK is never
// disabled and is the event this recipe is built on.
//
// Two ways it does not arrive, both reported as an error so the caller
// escalates to kill rather than waiting out a grace period for a signal
// that was never delivered:
//
//   - The child did not go through setup, so it is not in a group of its
//     own and there is no id to address that does not also name Sirdar.
//   - This process owns no console. GenerateConsoleCtrlEvent needs one to
//     send through, and a Sirdar running as a service, or under a harness
//     that gave it none, has nothing to send. There is no polite path on
//     that machine; the escalation is the whole of the stop.
func interrupt(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return syscall.EINVAL
	}
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}

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
