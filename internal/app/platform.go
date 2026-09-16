package app

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/osopen"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// platformCheck reports the three things Sirdar asks of the operating
// system that are answered by a different program on each one: opening a
// file or a URL, reading a `keychain:` credential reference, and — for the
// desktop app — the system webview.
//
// It earns a row because two of the three are a package away on Linux and
// present by definition on macOS and Windows. A server or minimal install
// has no xdg-open and no libsecret, and the symptom without this row is a
// "Open config" button that does nothing and a `keychain:` ref that fails
// with a message about the Secret Service the operator has no reason to
// connect to a missing package.
func platformCheck() Check {
	return platformCheckFor(runtime.GOOS, onPath)
}

// onPath reports whether a program can be found, and is the seam a test
// swaps out: whether the machine running the tests happens to have
// secret-tool installed is not what any of these cases is about.
func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// platformCheckFor takes the platform and the PATH lookup as arguments
// rather than reading runtime.GOOS and exec.LookPath, so the Linux branch
// is testable from the macOS laptop this was written on — the same shape
// internal/osopen and internal/procgroup use.
func platformCheckFor(goos string, have func(string) bool) Check {
	check := Check{Name: "platform", OK: true}
	clauses := []string{goos}

	switch opener := osopen.Opener(goos); {
	case opener == "":
		check.Level = string(provider.LevelWarn)
		clauses = append(clauses, "no desktop opener on this platform, so nothing can be handed to an editor, a file manager or a browser")
	case !have(opener):
		// The one condition here worth a warning: it is a capability both
		// shells use unconditionally, and it is missing.
		check.Level = string(provider.LevelWarn)
		clauses = append(clauses, opener+" is not on PATH"+openerFix(goos)+", so Open config, Open note, Open run directory and `serve --open` cannot reach the desktop")
	default:
		clauses = append(clauses, opener+" opens files and URLs")
	}

	clauses = append(clauses, keychainClause(goos, have))
	if isFreedesktop(goos) {
		// Not probed: `sirdar doctor` is a CLI command that needs no
		// webview, and when the desktop app's Settings screen runs the
		// same report the runtime is present by definition. Saying what
		// the app needs is the useful half; claiming to have checked it
		// would not be true.
		clauses = append(clauses, "the desktop app needs the WebKitGTK 4.1 runtime (libwebkit2gtk-4.1-0 / webkit2gtk4.1)")
	}

	check.Detail = strings.Join(clauses, "; ")
	return check
}

// openerFix names the package an operator installs when the opener is
// missing. Only Linux and the BSDs can be missing one.
func openerFix(goos string) string {
	if isFreedesktop(goos) {
		return " (install xdg-utils)"
	}
	return ""
}

// keychainClause says which store a `keychain:` reference reads, and on the
// freedesktop platforms whether the program that reads it is installed.
//
// A missing helper is stated, not warned about: a workspace that actually
// names a `keychain:` ref already fails in its own sources row, and this
// row's job is to explain why rather than to raise a second alarm about a
// facility the workspace may not use at all.
func keychainClause(goos string, have func(string) bool) string {
	switch goos {
	case "darwin":
		return "keychain: refs read the login keychain through `security`"
	case "windows":
		return "keychain: refs read the Credential Manager"
	}
	if !isFreedesktop(goos) {
		return "keychain: refs are not supported here; use env:, file: or cmd: refs"
	}
	switch {
	case have("secret-tool"):
		return "keychain: refs read the Secret Service through secret-tool"
	case have("pass"):
		return "keychain: refs read pass(1), since secret-tool is not on PATH"
	default:
		return "keychain: refs need secret-tool (libsecret-tools) or pass(1) and neither is on PATH, so use env:, file: or cmd: refs"
	}
}

// isFreedesktop reports whether goos is one of the platforms that keeps its
// desktop integration behind freedesktop.org conventions: xdg-open for
// opening things, the Secret Service for credentials, WebKitGTK for the
// app's webview. It is the same set internal/config's creds_libsecret.go
// selects with `//go:build unix && !darwin`.
func isFreedesktop(goos string) bool {
	switch goos {
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "illumos", "aix":
		return true
	}
	return false
}
