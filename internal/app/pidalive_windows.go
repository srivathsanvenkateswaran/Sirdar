//go:build windows

package app

import "os"

// pidAlive reports whether a process with this pid exists. On Windows
// FindProcess opens a handle to the process, which fails when there is none.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	p.Release()
	return true
}
