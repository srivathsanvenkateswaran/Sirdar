package provider

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestPathSetting(t *testing.T) {
	cases := map[string]string{
		"claude": "providers.claude.path",
		"codex":  "providers.codex.path",
		"qwen":   "qwen.path",
		"cursor": "cursor.path",
		"agy":    "agy.path",
		"acp":    "acp.command",
		"openai": "",
		"":       "",
	}
	for provider, want := range cases {
		if got := PathSetting(provider); got != want {
			t.Errorf("PathSetting(%q) = %q, want %q", provider, got, want)
		}
	}
}

func TestPathFixNamesTheOverrideAndTheInstallDirectory(t *testing.T) {
	got := pathFixFor("darwin", "claude", "providers.claude.path")
	want := "claude is not on the app's PATH — set providers.claude.path in .sirdar/config.yaml or install it under /opt/homebrew/bin"
	if got != want {
		t.Errorf("pathFixFor = %q, want %q", got, want)
	}
}

// A binary no configuration key points at still gets the install advice,
// without offering a setting that does not exist.
func TestPathFixWithoutASetting(t *testing.T) {
	got := pathFixFor("linux", "secret-tool", "")
	if strings.Contains(got, "config.yaml") {
		t.Errorf("pathFixFor = %q, want no setting offered", got)
	}
	if !strings.Contains(got, "~/.local/bin") {
		t.Errorf("pathFixFor = %q, want the Linux install directory", got)
	}
}

func TestInstallDirPerPlatform(t *testing.T) {
	cases := map[string]string{
		"darwin":  "/opt/homebrew/bin",
		"linux":   "~/.local/bin",
		"freebsd": "~/.local/bin",
		"windows": `%USERPROFILE%\bin`,
	}
	for goos, want := range cases {
		if got := installDir(goos); got != want {
			t.Errorf("installDir(%q) = %q, want %q", goos, got, want)
		}
	}
}

// The failure the bug report carried: the adapter wraps cmd.Start's error,
// and the fix has to be recoverable from the wrapped form.
func TestNotFoundFixReadsTheBinaryOutOfAWrappedExecError(t *testing.T) {
	start := fmt.Errorf("start %s: %w", "claude", &exec.Error{Name: "claude", Err: exec.ErrNotFound})
	fix := NotFoundFix("claude", fmt.Errorf("provider: %w", start))
	if !strings.HasPrefix(fix, "claude is not on the app's PATH") {
		t.Errorf("NotFoundFix = %q, want it to name claude", fix)
	}
	if !strings.Contains(fix, "providers.claude.path") {
		t.Errorf("NotFoundFix = %q, want the override named", fix)
	}
}

// An acp session names the agent it could not start, not "acp", and still
// points at acp.command as the place to fix it.
func TestNotFoundFixNamesTheAgentBinaryForACP(t *testing.T) {
	err := fmt.Errorf("acp: start opencode: %w", &exec.Error{Name: "opencode", Err: exec.ErrNotFound})
	fix := NotFoundFix("acp", err)
	if !strings.HasPrefix(fix, "opencode is not on the app's PATH") {
		t.Errorf("NotFoundFix = %q, want it to name opencode", fix)
	}
	if !strings.Contains(fix, "acp.command") {
		t.Errorf("NotFoundFix = %q, want acp.command named", fix)
	}
}

// Every other failure is left alone: a binary that exists and exits 1, a
// permission error, a cancelled context. Prefixing those with PATH advice
// would send an operator looking in the wrong place.
func TestNotFoundFixIgnoresOtherFailures(t *testing.T) {
	for _, err := range []error{
		errors.New("start claude: signal: killed"),
		fmt.Errorf("start claude: %w", &exec.Error{Name: "claude", Err: errors.New("permission denied")}),
		fmt.Errorf("start claude: %w", exec.ErrDot),
	} {
		if fix := NotFoundFix("claude", err); fix != "" {
			t.Errorf("NotFoundFix(%v) = %q, want none", err, fix)
		}
	}
}
