package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// keysOf marshals v and returns its top-level JSON field names, sorted.
func keysOf(t *testing.T, v any) []string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func wantKeys(t *testing.T, what string, v any, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := keysOf(t, v)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s fields\n got %v\nwant %v", what, got, want)
	}
}

// TestWireFieldNames pins every JSON name the frontend contract in
// desktop/frontend/src/api/types.ts reads. A rename here breaks the UI
// silently, so it breaks the build instead.
func TestWireFieldNames(t *testing.T) {
	state := store.State{
		RunID: "20260910T090000Z-aaaa", Key: "OMNI-1", Kind: store.KindTriage,
		Status: store.StatusCompleted, Provider: "claude", Model: "m",
		StartedAt: time.Now(), UpdatedAt: time.Now(), Reason: "",
		Notes: []string{"/notes/OMNI-1.md"}, Warnings: []string{"w"},
	}
	state.Usage = store.Usage{Turns: 3, InputTokens: 1, OutputTokens: 2, CostUSD: 0.4}
	state.Budget.MaxTurns, state.Budget.MaxMinutes, state.Budget.MaxUSD = 60, 25, 5

	summary := SummaryOf(state)
	wantKeys(t, "RunSummary", summary,
		"runId", "key", "helpdeskKey", "title", "kind", "status", "provider", "model",
		"startedAt", "updatedAt", "reason", "assignee", "mine", "usage", "notes")
	wantKeys(t, "Usage", summary.Usage, "turns", "inputTokens", "outputTokens", "costUsd")

	detail := DetailOf("/root", state)
	wantKeys(t, "RunDetail", detail,
		"runId", "key", "helpdeskKey", "title", "kind", "status", "provider", "model",
		"startedAt", "updatedAt", "reason", "assignee", "mine", "usage", "notes",
		"promptPath", "bundleDir", "warnings", "handle", "budget")
	wantKeys(t, "Budget", detail.Budget, "maxTurns", "maxMinutes", "maxUsd")
	// The Session topbar reads detail.model, and it is the run record's
	// Model — the id the provider reported — not the configured value the
	// run kept in ModelRequested.
	state.Model, state.ModelRequested = "claude-sonnet-5-20260514", "sonnet"
	if got := DetailOf("/root", state).Model; got != "claude-sonnet-5-20260514" {
		t.Errorf("RunDetail.model = %q, want the reported id", got)
	}
	if got := SummaryOf(state).Model; got != "claude-sonnet-5-20260514" {
		t.Errorf("RunSummary.model = %q, want the reported id", got)
	}
	state.Model, state.ModelRequested = "m", ""

	wantKeys(t, "RunEvent", RunEvent{
		T: "t", Kind: "tool_started",
		Payload: EventPayload{Tool: "Bash", Decision: "deny", Text: "x", Turns: 1, CostUSD: 0.1, Raw: json.RawMessage(`{}`)},
	}, "t", "kind", "payload")
	wantKeys(t, "RunEvent payload", EventPayload{
		Tool: "Bash", Decision: "deny", Text: "x", Turns: 1, CostUSD: 0.1, Raw: json.RawMessage(`{}`),
	}, "tool", "decision", "text", "turns", "costUsd", "raw")
	// The system event a provider's init line became carries the model it
	// reported, and nothing else.
	wantKeys(t, "init payload", EventPayload{Model: "claude-sonnet-5-20260514"}, "model")
	// A review event carries its own three fields and none of the agent's.
	// Hunk 0 is a real index, so it must survive the round trip.
	hunk := 0
	wantKeys(t, "review payload", EventPayload{Action: "drop", Path: "export/csv.go", Hunk: &hunk},
		"action", "path", "hunk")

	wantKeys(t, "RunDiff", RunDiff{Truncated: true},
		"base", "head", "branch", "worktree", "worktreePresent", "pushed",
		"files", "patch", "truncated", "etag")
	wantKeys(t, "DiffFile", DiffFile{}, "path", "status", "additions", "deletions")

	wantKeys(t, "Ticket", Ticket{LatestRun: &summary},
		"key", "title", "priority", "status", "assignee", "url", "helpdeskRef", "updatedAt", "latestRun")

	used := 91.0
	wantKeys(t, "Quota", Quota{
		Provider: "claude", ObservedAt: "t",
		FiveHour: &QuotaWindow{}, SevenDay: &QuotaWindow{}, UsedPercent: &used, ResetsAt: "t",
	}, "provider", "observedAt", "fiveHour", "sevenDay", "usedPercent", "resetsAt")
	wantKeys(t, "QuotaWindow", QuotaWindow{}, "utilization", "resetsAt")

	wantKeys(t, "RegisterRow", RegisterRow{},
		"key", "kind", "runId", "date", "provider", "model", "service",
		"classification", "confidence", "severity", "turns", "costUsd",
		"triageVerdict", "notePath", "title", "company")

	wantKeys(t, "Check", Check{}, "name", "ok", "level", "detail")

	// Each event kind carries only its own fields on the wire.
	wantKeys(t, "run.updated", Event{Kind: KindRunUpdated, WorkspaceID: "w", Run: &summary},
		"kind", "workspaceId", "run")
	wantKeys(t, "run.event", Event{Kind: KindRunEvent, WorkspaceID: "w", RunID: "r", Index: 1, Event: &RunEvent{}},
		"kind", "workspaceId", "runId", "index", "event")
	wantKeys(t, "quota.updated", Event{Kind: KindQuotaUpdate, Quota: &Quota{}}, "kind", "quota")
	wantKeys(t, "job.finished", Event{Kind: KindJobFinished, JobID: "job-1", WorkspaceID: "w", Outcomes: []JobOutcome{{}}},
		"kind", "jobId", "workspaceId", "outcomes")
	wantKeys(t, "job outcome", JobOutcome{}, "key", "status", "runId")
	wantKeys(t, "log", Event{Kind: KindLog, Text: "x"}, "kind", "text")

	wantKeys(t, "Workspace", Workspace{},
		"id", "name", "root", "provider", "model", "notesDir", "billing")
}

func TestConversionsCarryTheState(t *testing.T) {
	state := store.State{
		RunID: "r1", Key: "OMNI-1", Kind: store.KindRCA, Status: store.StatusBlocked,
		Reason: "agent asked: which region?", Handle: "sess-1",
		StartedAt: time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
	}
	detail := DetailOf("/root", state)
	if detail.Kind != "rca" || detail.Status != "blocked" || detail.Handle != "sess-1" {
		t.Fatalf("detail %+v", detail)
	}
	if detail.StartedAt != "2026-09-10T09:00:00Z" || detail.UpdatedAt != "" {
		t.Fatalf("timestamps %q %q", detail.StartedAt, detail.UpdatedAt)
	}
	// Both are filesystem paths DetailOf builds with filepath.Join, so on
	// Windows they come back separated with backslashes. FromSlash keeps
	// the expectation spelled the way the run directory reads.
	if detail.PromptPath != filepath.FromSlash("/root/.sirdar/runs/OMNI-1/r1/prompt.md") {
		t.Fatalf("promptPath %q", detail.PromptPath)
	}
	if detail.BundleDir != filepath.FromSlash("/root/.sirdar/runs/OMNI-1/r1/bundle") {
		t.Fatalf("bundleDir %q", detail.BundleDir)
	}
	// Empty slices marshal as [] rather than null, so the UI can iterate.
	if detail.Notes == nil || detail.Warnings == nil {
		t.Fatalf("nil slices in %+v", detail)
	}

	row := RegisterRowOf(store.RegisterRow{
		Key: "OMNI-1", Kind: "rca", TriageVerdict: "confirmed", CostUSD: 1.5,
		Title: "Export job times out", Company: "NEQSA SWEET",
	})
	if row.Key != "OMNI-1" || row.TriageVerdict != "confirmed" || row.CostUSD != 1.5 {
		t.Fatalf("row %+v", row)
	}
	if row.Title != "Export job times out" || row.Company != "NEQSA SWEET" {
		t.Fatalf("row title/company %+v", row)
	}
}

func TestJobsRejectBadArguments(t *testing.T) {
	root := newWorkspace(t)
	svc := New(newRegistry(t, root), stubBuilder(&stubProvider{script: replay()}, stubTracker{}, stubHelpdesk{}), Options{})
	ctx := context.Background()

	if _, err := svc.StartTriage(ctx, WorkspaceID(root), nil, TriageOptions{}); err == nil {
		t.Fatal("triage with no keys should fail")
	}
	if _, err := svc.StartRCA(ctx, WorkspaceID(root), "", RCAOptions{}); err == nil {
		t.Fatal("rca with no key should fail")
	}
	if _, err := svc.Resume(ctx, WorkspaceID(root), "", "", ""); err == nil {
		t.Fatal("resume with no run should fail")
	}
	for _, err := range []error{
		mustErr(svc.StartTriage(ctx, "deadbeef1234", []string{"OMNI-1"}, TriageOptions{})),
		mustErr(svc.StartRCA(ctx, "deadbeef1234", "OMNI-1", RCAOptions{})),
		mustErr(svc.Resume(ctx, "deadbeef1234", "r1", "", "")),
	} {
		if !errors.Is(err, ErrNoSuchWorkspace) {
			t.Fatalf("unknown workspace: %v", err)
		}
	}
	if _, err := svc.Run(WorkspaceID(root), "nope"); !errors.Is(err, ErrNoSuchRun) {
		t.Fatalf("unknown run: %v", err)
	}
	if _, err := svc.Note(WorkspaceID(root), "r1", "sideways"); err == nil {
		t.Fatal("an unknown note kind should fail")
	}
}

func mustErr(_ JobID, err error) error { return err }
