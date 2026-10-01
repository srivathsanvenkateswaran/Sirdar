package main

import (
	"bytes"
	"strings"
	"testing"

	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
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
