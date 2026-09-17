package provider

import (
	"errors"
	"os/exec"
	"runtime"
)

// PathSetting names the configuration key that tells Sirdar where a
// provider's binary is, for a provider that has one. It is "" for
// `provider: openai`, whose agent loop runs in this process and spawns
// nothing.
//
// The keys are not uniform, and the shape is deliberate: claude and codex
// have nothing to configure but a path, so they sit together under
// `providers:`; qwen, cursor and agy each carry an endpoint, a model or a
// mode that belongs with their path, so each has a block of its own.
// docs/config.md's Providers section is the same list.
func PathSetting(provider string) string {
	switch provider {
	case "claude":
		return "providers.claude.path"
	case "codex":
		return "providers.codex.path"
	case "qwen":
		return "qwen.path"
	case "cursor":
		return "cursor.path"
	case "agy":
		return "agy.path"
	case "acp":
		// There is no acp.path: the agent's whole launch command is
		// acp.command, and that is what an operator edits to give it an
		// absolute path.
		return "acp.command"
	default:
		return ""
	}
}

// PathFix is the sentence an operator gets when a binary Sirdar has to
// spawn is not on the PATH this process is using. It names the binary,
// the configuration key that overrides the lookup, and the directory an
// installed copy would sit in on this platform.
//
// It exists in one place because two surfaces say it: `sirdar doctor`'s
// environment row, before a run is ever started, and the failed-run banner
// afterwards, where the raw `exec: "claude": executable file not found in
// $PATH` is true and tells an operator nothing about what to do.
//
// setting may be "" for a binary no configuration key points at — git, gh,
// secret-tool — and the sentence then only offers the install directory.
func PathFix(binary, setting string) string {
	return pathFixFor(runtime.GOOS, binary, setting)
}

func pathFixFor(goos, binary, setting string) string {
	fix := binary + " is not on the app's PATH — "
	if setting != "" {
		fix += "set " + setting + " in .sirdar/config.yaml or "
	}
	return fix + "install it under " + installDir(goos)
}

// installDir is the directory this platform's package managers put a
// user-installed CLI in, and one Sirdar puts on the PATH itself at desktop
// startup (internal/loginpath). Naming it makes the advice actionable:
// a binary moved there is found without any configuration at all.
func installDir(goos string) string {
	switch goos {
	case "darwin":
		return "/opt/homebrew/bin"
	case "windows":
		return `%USERPROFILE%\bin`
	default:
		return "~/.local/bin"
	}
}

// NotFoundFix returns PathFix for an error that is an "executable file not
// found" from os/exec, and "" for every other error. provider is the
// provider whose session failed to start, which is what decides the
// configuration key; the binary is read out of the error, so an `acp`
// session that could not find `opencode` names opencode and not acp.
func NotFoundFix(provider string, err error) string {
	var execErr *exec.Error
	if !errors.As(err, &execErr) || !errors.Is(execErr.Err, exec.ErrNotFound) {
		return ""
	}
	return PathFix(execErr.Name, PathSetting(provider))
}
