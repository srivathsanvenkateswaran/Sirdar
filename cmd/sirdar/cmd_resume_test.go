package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

func TestResumeDecisionFlags(t *testing.T) {
	cases := []struct {
		allow, allowRun, deny bool
		reason                string
		want                  *runner.Decision
		err                   string
	}{
		{want: nil},
		{allow: true, want: &runner.Decision{Verdict: "allow"}},
		{allowRun: true, want: &runner.Decision{Verdict: "allow_run"}},
		{deny: true, reason: " too broad ", want: &runner.Decision{Verdict: "deny", Reason: "too broad"}},
		{allow: true, deny: true, err: "one answer"},
		{reason: "why", err: "--reason goes with"},
	}
	for _, c := range cases {
		got, msg := decisionFlags(c.allow, c.allowRun, c.deny, c.reason)
		if c.err != "" {
			if !strings.Contains(msg, c.err) {
				t.Errorf("%+v: message %q, want %q", c, msg, c.err)
			}
			continue
		}
		if msg != "" || (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%+v: got %+v %q", c, got, msg)
		}
	}
}

func TestResumeConflictingDecisionsAreAUsageError(t *testing.T) {
	chdir(t, t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"resume", "r1", "--allow", "--deny"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "one answer") {
		t.Errorf("stderr %q", errb.String())
	}
}

// TestResumeRefusesAShadowRun: a shadow run is a measurement. Resuming it
// by hand would spend around the shadow caps and leave its ledger line
// stale, so the command refuses before any session starts.
func TestResumeRefusesAShadowRun(t *testing.T) {
	root := shadowWorkspace(t)
	const runID = "20261006T090000Z-ab12"
	dir := filepath.Join(root, ".sirdar", "runs", "OMNI-1", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := store.State{RunID: runID, Key: "OMNI-1", Kind: store.KindTriage, Status: store.StatusBlocked,
		Reason: "agent asked: which tenant?", Provider: "claude", Shadow: true}
	if err := (store.Run{Dir: dir}).WriteState(st); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"resume", runID}, &out, &errb); code != 1 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if want := "is a shadow run; it is a measurement and is not resumed"; !strings.Contains(errb.String(), want) {
		t.Errorf("stderr %q, want %q", errb.String(), want)
	}
}
