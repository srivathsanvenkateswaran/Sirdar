package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/shadow"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// shadowWorkspace is a temporary workspace with the working directory in
// it and a home of its own, so the registry these commands write to is the
// test's and not the operator's.
func shadowWorkspace(t *testing.T) string {
	t.Helper()
	root, _ := newWorkspace(t, fakeClaude)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	chdir(t, root)
	return root
}

// writeShadowLedger records three shadow runs started in the last few
// hours, two of them graded for good.
func writeShadowLedger(t *testing.T, root string) {
	t.Helper()
	now := time.Now()
	for i, r := range []struct{ key, grade string }{
		{"OMNI-1", "confirmed"},
		{"OMNI-2", "wrong"},
		{"OMNI-3", ""},
	} {
		at := now.Add(-time.Duration(3-i) * time.Hour)
		run := "20261006T000000Z-000" + string(rune('1'+i))
		lines := []shadow.Entry{
			{At: at.Add(-time.Minute), Ev: "seen", Key: r.key, Title: "title " + r.key},
			{At: at, Ev: "started", Key: r.key, RunID: run, Attempt: 1},
			{At: at.Add(10 * time.Minute), Ev: "finished", Key: r.key, RunID: run, Status: "completed",
				CostUSD: 0.5, ElapsedSec: 250,
				Verdict: &shadow.Verdict{Classification: "code", Confidence: "medium", Hypothesis: "The cache is stale. It never expires."}},
		}
		if r.grade != "" {
			lines = append(lines, shadow.Entry{At: at.Add(time.Hour), Ev: "graded", Key: r.key, ShadowRunID: run,
				Grade: r.grade, Final: true, Basis: "pr", Reason: "because"})
		}
		for _, e := range lines {
			if err := shadow.Append(root, e); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestShadowReportJSON(t *testing.T) {
	root := shadowWorkspace(t)
	writeShadowLedger(t, root)

	out, _ := mustRun(t, 0, "shadow", "report", "--json")
	var rep shadow.ShadowReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if rep.Headline.Graded != 2 || rep.Headline.Started != 3 {
		t.Fatalf("headline %+v", rep.Headline)
	}
}

func TestShadowReportText(t *testing.T) {
	root := shadowWorkspace(t)
	writeShadowLedger(t, root)

	out, _ := mustRun(t, 0, "shadow", "report", "--since", "7d")
	for _, want := range []string{
		"Graded 2 of 3 started",
		"Confirmed 50% (50% confirmed or partial)",
		"KEY", "HYPOTHESIS",
		"OMNI-1", "confirmed",
		"The cache is stale.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not say %q:\n%s", want, out)
		}
	}
}

func TestShadowReportRefusesABadSince(t *testing.T) {
	shadowWorkspace(t)
	_, errb := mustRun(t, 1, "shadow", "report", "--since", "last tuesday")
	if !strings.Contains(errb, "last tuesday") {
		t.Fatalf("stderr does not name the value: %s", errb)
	}
}

func TestShadowOutcomeRefusesABadURL(t *testing.T) {
	root := shadowWorkspace(t)
	writeShadowLedger(t, root)

	_, errb := mustRun(t, 1, "shadow", "outcome", "OMNI-1", "--pr", "notaurl")
	if !strings.Contains(errb, "notaurl") {
		t.Fatalf("stderr does not name the URL: %s", errb)
	}
}

func TestShadowPauseThenStatus(t *testing.T) {
	shadowWorkspace(t)

	out, _ := mustRun(t, 0, "shadow", "pause", "--for", "4h")
	if !strings.Contains(out, "state: paused") {
		t.Fatalf("pause did not print the paused status:\n%s", out)
	}
	out, _ = mustRun(t, 0, "shadow", "status")
	for _, want := range []string{"state: paused", "reason: paused until", "today: 0 of 10 runs, $0.00 of $10.00", "next poll: none", "in flight: none"} {
		if !strings.Contains(out, want) {
			t.Errorf("status does not say %q:\n%s", want, out)
		}
	}

	out, _ = mustRun(t, 0, "shadow", "resume")
	if strings.Contains(out, "state: paused") {
		t.Fatalf("resume left it paused:\n%s", out)
	}
}

func TestShadowPauseRefusesABadDuration(t *testing.T) {
	shadowWorkspace(t)
	mustRun(t, 1, "shadow", "pause", "--for", "soon")
}

func TestShadowUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"shadow"},
		{"shadow", "bogus"},
		{"shadow", "run"},
		{"shadow", "outcome"},
		{"shadow", "status", "extra"},
		{"suggestions", "accept"},
		{"suggestions", "bogus"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: exit %d, want %d; stderr %s", args, code, exitUsage, errb.String())
		}
	}
}

func proposeSuggestion(t *testing.T, root string) store.SuggestionLine {
	t.Helper()
	line := store.SuggestionLine{
		Ev: "proposed", Source: "remember", RunID: "20261006T000000Z-0001", Key: "OMNI-1",
		Playbook: "20-logs.md", Addition: "- Read the gateway log before the app log.",
		Reason: "the operator corrected this twice",
	}
	line.ID = store.SuggestionID(line.Playbook, line.Addition)
	if _, err := store.AppendSuggestion(root, line); err != nil {
		t.Fatal(err)
	}
	return line
}

func TestSuggestionsListsAProposal(t *testing.T) {
	root := shadowWorkspace(t)
	line := proposeSuggestion(t, root)

	out, _ := mustRun(t, 0, "suggestions")
	for _, want := range []string{"ID", "PLAYBOOK", "SOURCE", line.ID, "20-logs.md", "remember", "    - Read the gateway log before the app log."} {
		if !strings.Contains(out, want) {
			t.Errorf("list does not say %q:\n%s", want, out)
		}
	}
	listed, _ := mustRun(t, 0, "suggestions", "list")
	if listed != out {
		t.Fatalf("`suggestions list` differs from `suggestions`:\n%s\n---\n%s", listed, out)
	}
}

func TestSuggestionsWithNonePending(t *testing.T) {
	shadowWorkspace(t)
	out, _ := mustRun(t, 0, "suggestions")
	if !strings.Contains(out, "No pending suggestions.") {
		t.Fatalf("output %q", out)
	}
}

func TestSuggestionsDismissUnknownID(t *testing.T) {
	shadowWorkspace(t)
	mustRun(t, 1, "suggestions", "dismiss", "s-000000000000")
}

func TestSuggestionsDismissThenGone(t *testing.T) {
	root := shadowWorkspace(t)
	line := proposeSuggestion(t, root)

	out, _ := mustRun(t, 0, "suggestions", "dismiss", line.ID)
	if !strings.Contains(out, "Dismissed "+line.ID) {
		t.Fatalf("output %q", out)
	}
	out, _ = mustRun(t, 0, "suggestions")
	if strings.Contains(out, line.ID) {
		t.Fatalf("a dismissed suggestion is still listed:\n%s", out)
	}
}

// TestSuggestionsAcceptHintsAtAPlaybookName: a target that is neither an
// existing playbook nor a name a new one may take is refused, and the
// command says what to pass instead.
func TestSuggestionsAcceptHintsAtAPlaybookName(t *testing.T) {
	root := shadowWorkspace(t)
	line := proposeSuggestion(t, root)

	_, errb := mustRun(t, 1, "suggestions", "accept", line.ID, "--playbook", "Logs.md")
	if !strings.Contains(errb, "pass --playbook with an existing playbook, or a new name like 20-logs.md") {
		t.Fatalf("no hint on stderr: %s", errb)
	}
}

func TestSuggestionsAcceptFilesIt(t *testing.T) {
	root := shadowWorkspace(t)
	line := proposeSuggestion(t, root)

	out, _ := mustRun(t, 0, "suggestions", "accept", line.ID)
	if !strings.Contains(out, "Accepted "+line.ID+" into 20-logs.md") {
		t.Fatalf("output %q", out)
	}
}

func TestUsageListsShadowAndSuggestions(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb); code != exitUsage {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"  shadow      watch, run, grade and report shadow triages (rung 1)",
		"  suggestions list, accept or dismiss pending playbook additions",
	} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("usage does not list %q:\n%s", want, errb.String())
		}
	}
}

// TestShadowWatchRefusesALockHeldElsewhere: a live watcher in another
// process holds the workspace, so neither a one-off tick nor a watcher
// starts here, and both say who holds it.
func TestShadowWatchRefusesALockHeldElsewhere(t *testing.T) {
	root := shadowWorkspace(t)
	cfgPath := filepath.Join(root, ".sirdar", "config.yaml")
	f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("shadow:\n  enabled: true\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	lock := `{"pid":4242,"host":"another-host","by":"serve","startedAt":"2026-10-06T09:00:00Z"}`
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "shadow.lock"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"shadow", "watch", "--once"}, {"shadow", "watch"}} {
		_, errb := mustRun(t, 1, args...)
		if !strings.Contains(errb, "shadow: watched by pid 4242 (serve)") {
			t.Errorf("%v: stderr does not name the holder: %s", args, errb)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".sirdar", "shadow.lock")); err != nil {
		t.Fatalf("the other process's lock was removed: %v", err)
	}
}

// TestShadowCommandsExitCodes: no workspace to work in is a usage error, as
// for serve; a registry that cannot be read is a failure, exit 1.
func TestShadowCommandsExitCodes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	chdir(t, t.TempDir())
	mustRun(t, exitUsage, "shadow", "status")
	mustRun(t, exitUsage, "suggestions")

	shadowWorkspace(t)
	// A file where the user directory should be makes the registry
	// unreadable.
	home, _ = os.UserHomeDir()
	if err := os.WriteFile(filepath.Join(home, ".sirdar"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, 1, "shadow", "status")
	mustRun(t, 1, "suggestions")
}

// The 2026-10-11 backfill smoke: `shadow run` with three keys and
// shadow.concurrency 1 started the first and turned the other two away.
// Named keys run one after another instead.
func TestShadowRunTakesNamedKeysInTurn(t *testing.T) {
	root := shadowWorkspace(t)
	raw, err := os.ReadFile(filepath.Join(testdataDir, "tickets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	second := strings.ReplaceAll(string(fixture["tracker"]["OMNI-1"]), "OMNI-1", "OMNI-2")
	fixture["tracker"]["OMNI-2"] = json.RawMessage(second)
	both, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	tickets := filepath.Join(t.TempDir(), "tickets.json")
	if err := os.WriteFile(tickets, both, 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, ".sirdar", "config.yaml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(string(cfg), filepath.Join(testdataDir, "tickets.json"), tickets) +
		"shadow:\n  enabled: false\n  concurrency: 1\n  maxPerDay: 5\n  maxUSDPerDay: 10\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := runCLI(t, "shadow", "run", "OMNI-1", "OMNI-2")
	if strings.Contains(stdout, "skipped") {
		t.Fatalf("a named key was skipped:\n%s\nstderr: %s", stdout, stderr)
	}
	for _, key := range []string{"OMNI-1", "OMNI-2"} {
		if !regexp.MustCompile(`(?m)^` + key + `\s+\d{8}T\d{6}Z-`).MatchString(stdout) {
			t.Errorf("%s has no run:\n%s\nstderr: %s", key, stdout, stderr)
		}
	}
}
