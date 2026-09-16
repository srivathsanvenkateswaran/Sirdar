// Package procgroup starts a child process in its own process group, where
// the platform has one, so a timeout or a Close can kill the whole subtree
// the child may have spawned (through a shell wrapper, npx, uvx, and the
// like) rather than just the immediate child, which would otherwise be left
// running with Wait blocked on the pipes it still holds open.
package procgroup

import "os/exec"

// Setup configures cmd to run in its own process group. Call it before
// cmd.Start or cmd.Run.
func Setup(cmd *exec.Cmd) { setup(cmd) }

// Kill takes down cmd and everything it started: the process group on
// Unix, the parent-pid tree through `taskkill /T` on Windows, which has no
// process group to address. Either way it falls back to killing just
// cmd.Process when the subtree cannot be reached — see
// procgroup_windows.go for what that costs there.
func Kill(cmd *exec.Cmd) error { return kill(cmd) }

// Alive reports whether pid names a running process, independent of
// whether it was started by this program. See procgroup_unix.go and
// procgroup_windows.go for the platform-specific caveats.
func Alive(pid int) bool { return alive(pid) }
