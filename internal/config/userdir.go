package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// userDirName is the directory Sirdar's user-level state has always lived
// in, under the operator's home directory, on every platform.
const userDirName = ".sirdar"

// UserDir is where Sirdar keeps the state that belongs to the operator
// rather than to one workspace: the workspace registry the desktop app and
// `sirdar serve` read, and the default golden set. A workspace's own
// `.sirdar/` is a different thing entirely and is always beside its config.
//
// There is no user-level *configuration* file — every setting lives in a
// workspace's `.sirdar/config.yaml` — so `$XDG_CONFIG_HOME` and
// `os.UserConfigDir` have nothing to point at here. What Sirdar does keep
// is data, which on the freedesktop platforms belongs under
// `$XDG_DATA_HOME`, and that is the one variable this honours.
//
// See userDir for the order the candidates are tried in.
func UserDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: home directory: %w", err)
	}
	return userDir(runtime.GOOS, home, os.Getenv), nil
}

// userDir picks the directory. It takes the platform and the environment as
// arguments rather than reading runtime.GOOS and os.Getenv so that the
// branch a macOS laptop never takes is still testable on one — the same
// shape internal/osopen and internal/procgroup use.
//
// The order is:
//
//  1. An existing ~/.sirdar wins everywhere. Nobody's registry moves out
//     from under them because they set a variable, or upgraded.
//  2. On Linux and the BSDs, $XDG_DATA_HOME when it is set to an absolute
//     path: an operator who has relocated their data home asked for
//     everything to follow, and Sirdar is not an exception.
//  3. ~/.sirdar otherwise — including the freedesktop default case, where
//     $XDG_DATA_HOME is unset. The bare default is deliberately *not*
//     ~/.local/share/sirdar: one spelling on all three platforms is worth
//     more here than the letter of the spec, and docs/release.md says so in
//     the same words for Windows.
func userDir(goos, home string, getenv func(string) string) string {
	legacy := filepath.Join(home, userDirName)
	if fi, err := os.Stat(legacy); err == nil && fi.IsDir() {
		return legacy
	}
	if goos == "darwin" || goos == "windows" {
		return legacy
	}
	if data := getenv("XDG_DATA_HOME"); filepath.IsAbs(data) {
		return filepath.Join(data, "sirdar")
	}
	return legacy
}
