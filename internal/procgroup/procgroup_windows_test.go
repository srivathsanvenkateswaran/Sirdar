//go:build windows

package procgroup

import (
	"os/exec"
	"strings"
	"testing"
)

func TestTaskkillArgsKillTheWholeTree(t *testing.T) {
	got := strings.Join(taskkillArgs(4321), " ")
	if got != "/PID 4321 /T /F" {
		t.Fatalf("taskkillArgs = %q, want the tree-and-force form", got)
	}
}

func TestTaskkillPathPrefersTheSystemDirectory(t *testing.T) {
	path, err := taskkillPath()
	if err != nil {
		t.Skipf("no taskkill on this machine: %v", err)
	}
	if !strings.HasSuffix(strings.ToLower(path), `system32\taskkill.exe`) {
		t.Fatalf("taskkillPath = %q, want the copy in the system directory", path)
	}
}

// Kill goes to the tree first and only falls back to the single process
// when taskkill could not be run at all.
func TestKillPrefersTheTree(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "ping -n 20 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	var got int
	restore := killTree
	killTree = func(pid int) error { got = pid; return nil }
	t.Cleanup(func() { killTree = restore })

	if err := Kill(cmd); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got != cmd.Process.Pid {
		t.Fatalf("killTree got pid %d, want %d", got, cmd.Process.Pid)
	}
}

// A real kill still takes the child down, and Alive agrees afterwards.
func TestKillEndsTheProcess(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "ping -n 30 127.0.0.1 >NUL")
	Setup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	if !Alive(pid) {
		t.Fatal("the process is not alive right after Start")
	}
	if err := Kill(cmd); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	_ = cmd.Wait()
	if Alive(pid) {
		t.Fatalf("pid %d is still alive after Kill", pid)
	}
}
