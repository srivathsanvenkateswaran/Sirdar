// Package loginpath gives a GUI process the PATH its owner's terminal has.
//
// A desktop app launched from the Dock, from Spotlight or from a Linux
// application launcher inherits launchd's or the session manager's
// environment, not a shell's. On macOS that PATH is
// /usr/bin:/bin:/usr/sbin:/sbin — no /opt/homebrew/bin, no ~/.local/bin,
// no node or cargo prefix. Everything Sirdar spawns is a program the
// operator installed themselves: the provider CLIs (claude, codex,
// cursor-agent, qwen, and whatever acp.command names — copilot, opencode,
// kimi), the credential helpers (secret-tool, pass), the desktop opener
// (xdg-open), git and gh. A run started from the app therefore failed with
// `exec: "claude": executable file not found in $PATH` while the same run
// started from a terminal worked.
//
// Apply resolves the login shell's own PATH once at startup and installs
// it on the process, then appends the handful of user-level bin
// directories a shell profile may not have exported. It is a no-op on
// Windows, where a GUI process already gets the user's PATH from the
// registry.
package loginpath

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// shellTimeout caps one attempt at running the login shell. A profile that
// blocks — waiting on a network mount, a version manager warming a cache,
// a prompt nobody is there to answer — must not hold the window back, so
// the interactive attempt is abandoned at this point and the
// non-interactive one tried instead.
const shellTimeout = 3 * time.Second

// pathCommand is what the login shell is asked to print. printf rather
// than echo: no trailing newline, no shell builtin that interprets
// backslashes, and nothing that depends on which echo the shell has.
const pathCommand = `printf "%s" "$PATH"`

// extraDirs are the user-level bin directories appended to whatever the
// login shell reported, in this order, when the shell did not already
// name them. A leading "~/" is the operator's home directory.
//
// They are appended rather than prepended: the shell's own PATH is the
// operator's stated preference and stays ahead of a directory Sirdar
// guessed at. ~/.claude/local is where Claude Code's own installer puts
// the `claude` shim; ~/.opencode/bin is OpenCode's.
var extraDirs = []string{
	"~/.local/bin",
	"/opt/homebrew/bin",
	"/usr/local/bin",
	"~/go/bin",
	"~/.npm-global/bin",
	"~/.bun/bin",
	"~/.cargo/bin",
	"~/.claude/local",
	"~/.opencode/bin",
	"~/bin",
}

// Result records what Apply did, so `sirdar doctor`'s environment row can
// say where this process's PATH came from rather than leaving an operator
// to guess whether the app is looking in the same places their terminal
// does.
type Result struct {
	// Source is "process" or "login shell <path>". It is the phrase the
	// doctor row prints.
	Source string
	// Shell is the shell that was run, "" when none was.
	Shell string
	// Flags is the flag word that produced the answer, "-il" or "-l".
	Flags string
	// Path is the PATH now installed on the process.
	Path string
	// Added lists the extraDirs that were appended because neither the
	// shell nor the inherited PATH already had them.
	Added []string
	// Err is why the login shell was not used, when it was not. A Result
	// with Source "process" and no Err is an unchanged PATH on Windows or
	// in a process that never called Apply.
	Err error
}

var (
	mu      sync.Mutex
	current *Result
)

// Current is what Apply last recorded, or the untouched process
// environment when nothing has called Apply — which is every CLI
// invocation, where the PATH is the terminal's already.
func Current() Result {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		return *current
	}
	return Result{Source: "process", Path: os.Getenv("PATH")}
}

// Apply resolves the login shell's PATH, merges in extraDirs, installs the
// result on this process with os.Setenv, and records it for Current. It is
// safe to call once, early, before anything looks a binary up: exec.LookPath
// reads $PATH at each call, so every later lookup and every child process
// that inherits os.Environ() sees the merged value.
func Apply(ctx context.Context) Result {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	res := resolve(ctx, runtime.GOOS, os.Getenv("SHELL"), os.Getenv("PATH"), home, runShell)
	if res.Path != "" {
		os.Setenv("PATH", res.Path)
	}
	mu.Lock()
	current = &res
	mu.Unlock()
	return res
}

// runner is the seam the tests replace: running a real login shell is the
// one thing in here that cannot be exercised deterministically.
type runner func(ctx context.Context, shell, flags string) (string, error)

// resolve is Apply without the environment or the side effect, so every
// branch — each platform, a shell that fails, a shell that hangs, a shell
// that prints a banner before the PATH — is testable from one machine.
func resolve(ctx context.Context, goos, shellEnv, processPath, home string, run runner) Result {
	res := Result{Source: "process", Path: processPath}

	shell := shellFor(goos, shellEnv)
	if shell == "" {
		// Windows, or a platform with no login shell worth asking. The
		// process PATH is already the user's, and the extra directories
		// below are Unix shapes, so nothing is merged either.
		return res
	}
	res.Shell = shell

	// -il first: an interactive login shell is the one that sources the
	// file most people actually edit (~/.zshrc, ~/.bashrc), which is
	// where a Homebrew or nvm prefix usually gets exported. It is also
	// the one that can block on a prompt or a slow plugin, so a failure
	// or a timeout falls back to the plain login shell.
	for _, flags := range []string{"-il", "-l"} {
		attempt, cancel := context.WithTimeout(ctx, shellTimeout)
		out, err := run(attempt, shell, flags)
		cancel()
		if err != nil {
			res.Err = err
			continue
		}
		value := lastLine(out)
		if !looksLikePath(value) {
			continue
		}
		res.Source = "login shell " + shell
		res.Flags = flags
		res.Path = value
		res.Err = nil
		break
	}

	res.Path, res.Added = merge(res.Path, extraDirs, home)
	return res
}

// shellFor names the shell to ask, and "" for a platform that is not
// asked at all. A GUI process on Windows inherits the user's PATH from
// the registry, so there is nothing to recover there and no login shell
// to recover it with.
func shellFor(goos, shellEnv string) string {
	if goos == "windows" {
		return ""
	}
	if s := strings.TrimSpace(shellEnv); s != "" {
		return s
	}
	if goos == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// runShell runs one attempt and returns its stdout.
//
// Stdin is /dev/null, which is what a nil Cmd.Stdin means: an interactive
// shell that decides to read — a profile prompting for something — gets
// EOF instead of inheriting whatever this process had open. Stderr is
// discarded the same way: a profile that complains on every start is not
// this function's business, and its output would otherwise be mistaken
// for part of the answer.
func runShell(ctx context.Context, shell, flags string) (string, error) {
	cmd := exec.CommandContext(ctx, shell, flags, "-c", pathCommand)
	cmd.Stdin = nil
	cmd.Stderr = nil
	out, err := cmd.Output()
	return string(out), err
}

// lastLine is the final non-empty line of the shell's stdout. pathCommand
// prints no newline, so whatever a chatty profile wrote before it ends in
// one and the PATH is what is left at the end.
func lastLine(out string) string {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// looksLikePath rejects an answer that is not a PATH at all — an empty
// string, or a profile's parting message that happened to be the last
// thing on stdout. One absolute entry is the whole test: a PATH without
// one is of no use here whatever else it is.
func looksLikePath(value string) bool {
	for _, entry := range strings.Split(value, string(os.PathListSeparator)) {
		if strings.HasPrefix(entry, "/") {
			return true
		}
	}
	return false
}

// merge dedupes base and appends each of extras that is missing, returning
// the joined PATH and the entries that were added.
//
// Dedupe keeps the first occurrence: a PATH is searched left to right, so
// the earlier copy is the one that decides which binary wins and removing
// the later one changes nothing but the length.
func merge(base string, extras []string, home string) (string, []string) {
	var entries []string
	seen := map[string]bool{}
	add := func(dir string) bool {
		if dir == "" {
			return false
		}
		key := filepath.Clean(dir)
		if seen[key] {
			return false
		}
		seen[key] = true
		entries = append(entries, dir)
		return true
	}

	for _, entry := range strings.Split(base, string(os.PathListSeparator)) {
		add(strings.TrimSpace(entry))
	}

	var added []string
	for _, dir := range extras {
		expanded := expand(dir, home)
		if expanded == "" {
			continue
		}
		if add(expanded) {
			added = append(added, expanded)
		}
	}
	return strings.Join(entries, string(os.PathListSeparator)), added
}

// expand turns a leading "~/" into the home directory, and returns "" for
// a "~" entry when there is no home directory to expand it against —
// joining it literally would put a directory named "~" on the PATH.
func expand(dir, home string) string {
	if !strings.HasPrefix(dir, "~/") {
		return dir
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, dir[2:])
}
