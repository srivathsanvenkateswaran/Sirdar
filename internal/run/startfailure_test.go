package run

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// The bug: a triage started from the desktop app failed with the raw
// os/exec sentence, which says nothing about where the app is looking.
// The fix comes first, and the raw error is still there behind it.
func TestStartFailureLeadsWithTheFixForAMissingBinary(t *testing.T) {
	err := fmt.Errorf("start %s: %w", "claude", &exec.Error{Name: "claude", Err: exec.ErrNotFound})
	got := startFailure("claude", err)

	if !strings.HasPrefix(got, "claude is not on the app's PATH") {
		t.Errorf("reason = %q, want it to lead with the fix", got)
	}
	if !strings.Contains(got, "providers.claude.path in .sirdar/config.yaml") {
		t.Errorf("reason = %q, want the override named", got)
	}
	if !strings.Contains(got, "executable file not found") {
		t.Errorf("reason = %q, want the raw error kept after it", got)
	}
}

// Every other start failure reads exactly as it did before.
func TestStartFailureLeavesOtherErrorsAlone(t *testing.T) {
	err := errors.New("codex: start codex: app-server handshake failed")
	if got, want := startFailure("codex", err), "provider: "+err.Error(); got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
}
