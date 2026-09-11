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

// Kill sends the strongest available signal to cmd's whole process group,
// falling back to killing just cmd.Process when the group cannot be
// reached (already gone, or the platform has no addressable process
// groups — see procgroup_windows.go).
func Kill(cmd *exec.Cmd) error { return kill(cmd) }
