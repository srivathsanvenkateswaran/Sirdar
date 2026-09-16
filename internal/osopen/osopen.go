// Package osopen hands a file, a directory or a URL to whatever the
// operator's desktop has associated with it: the default handler on macOS,
// the default handler on Windows, the freedesktop handler on Linux and the
// BSDs.
//
// Every shell that opens something for the operator goes through here —
// `sirdar serve --open` for the browser, the desktop app's OpenConfig and
// OpenNote for a file — so the per-platform command lives in one place
// rather than being written out again at each call site. Getting it wrong
// on a platform is then one bug, not three.
package osopen

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Open starts the platform's opener on target and returns without waiting
// for it. The thing that gets opened — a browser, an editor, Explorer —
// outlives the process that asked for it, so waiting would mean blocking
// until the operator closed their editor.
//
// A non-nil error means the opener could not be started at all (no handler
// on PATH, most likely). It says nothing about whether the application that
// took over could read the target: that answer never comes back to us on
// any platform.
func Open(target string) error {
	name, args := commandFor(runtime.GOOS, target)
	if name == "" {
		return fmt.Errorf("osopen: %s has no desktop opener, so %q cannot be opened from here", runtime.GOOS, target)
	}
	return start(exec.Command(name, args...))
}

// start is the last step, split out so a test can record the command
// instead of launching an editor on the machine running the tests.
var start = func(cmd *exec.Cmd) error { return cmd.Start() }

// Opener names the program Open would run on goos, or "" where the
// platform has no desktop opener at all.
//
// It exists for `sirdar doctor`, which tells the operator which program
// has to be installed for "Open config" and `serve --open` to work — on
// Linux that is xdg-open, which a minimal or server install does not have.
// Reading it off the same table Open uses is what keeps the report from
// naming a program Open never runs.
func Opener(goos string) string {
	name, _ := commandFor(goos, "")
	return name
}

// commandFor picks the opener for one GOOS. It takes the platform as an
// argument rather than reading runtime.GOOS so the table below is testable
// on whichever machine happens to be running the tests — the whole point of
// this package is the branch that the machine in front of you never takes.
//
// Windows goes through `rundll32 url.dll,FileProtocolHandler` rather than
// `explorer` or `cmd /c start`: it is the one form that takes a file, a
// directory and a URL alike, it flashes no console window, and — unlike
// `cmd /c start` — the target stays a plain argv entry that cmd.exe never
// re-parses, so a path holding a space or an ampersand cannot turn into a
// second command.
func commandFor(goos, target string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{target}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}
	case "android", "ios", "js", "wasip1", "plan9":
		// No desktop to hand anything to. Named rather than left to the
		// default so that a platform Sirdar has never run on reads as
		// unsupported instead of quietly shelling out to a binary that is
		// not there.
		return "", nil
	default:
		// Linux and the BSDs: xdg-open is the freedesktop entry point
		// every desktop environment installs.
		return "xdg-open", []string{target}
	}
}
