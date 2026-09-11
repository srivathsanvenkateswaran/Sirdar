package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// The two payloads below are the lines the providers actually emit, copied
// from docs/research/06-wire-formats.md. internal/run records them
// verbatim under payload.raw.
const claudeRateLimitRaw = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1789049400,"rateLimitType":"five_hour","overageStatus":"rejected","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.5,"resetsAt":1789049400},"seven_day":{"utilization":0.4,"resetsAt":1789531200}}}}`

const codexRateLimitRaw = `{"jsonrpc":"2.0","method":"account/rateLimits/updated","params":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":91,"windowDurationMins":43200,"resetsAt":1789819250},"secondary":null,"credits":{"balance":0}}}}`

func recorded(t *testing.T, at time.Time, kind, raw string) RunEvent {
	t.Helper()
	return RunEvent{
		T:       at.UTC().Format(time.RFC3339Nano),
		Kind:    kind,
		Payload: EventPayload{Raw: json.RawMessage(raw)},
	}
}

func TestQuotaFromClaudeAndCodexPayloads(t *testing.T) {
	tr := newQuotaTracker()
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	claude, ok := tr.observe(recorded(t, at, "rate_limited", claudeRateLimitRaw))
	if !ok {
		t.Fatal("the claude payload produced no quota")
	}
	if claude.Provider != "claude" || claude.ObservedAt != wireTime(at) {
		t.Fatalf("quota %+v", claude)
	}
	if claude.FiveHour == nil || claude.FiveHour.Utilization != 0.5 ||
		claude.FiveHour.ResetsAt != wireTime(time.Unix(1789049400, 0)) {
		t.Fatalf("five hour window %+v", claude.FiveHour)
	}
	if claude.SevenDay == nil || claude.SevenDay.Utilization != 0.4 ||
		claude.SevenDay.ResetsAt != wireTime(time.Unix(1789531200, 0)) {
		t.Fatalf("seven day window %+v", claude.SevenDay)
	}
	if claude.UsedPercent != nil {
		t.Fatalf("claude carries usedPercent %v", *claude.UsedPercent)
	}

	// Codex reports its window on a plain notification, recorded as a
	// system event until the window is actually exhausted.
	codex, ok := tr.observe(recorded(t, at, "system", codexRateLimitRaw))
	if !ok {
		t.Fatal("the codex payload produced no quota")
	}
	if codex.Provider != "codex" || codex.UsedPercent == nil || *codex.UsedPercent != 91 {
		t.Fatalf("quota %+v", codex)
	}
	if codex.ResetsAt != wireTime(time.Unix(1789819250, 0)) || codex.FiveHour != nil {
		t.Fatalf("quota %+v", codex)
	}

	// The same reading again is not news; a different one is.
	if _, ok := tr.observe(recorded(t, at.Add(time.Minute), "rate_limited", claudeRateLimitRaw)); ok {
		t.Fatal("an unchanged reading was reported as an update")
	}
	moved := `{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"five_hour":{"utilization":0.9,"resetsAt":1789049400}}}}`
	updated, ok := tr.observe(recorded(t, at.Add(2*time.Minute), "rate_limited", moved))
	if !ok || updated.FiveHour.Utilization != 0.9 {
		t.Fatalf("updated %+v ok=%v", updated, ok)
	}

	// An older reading does not overwrite a newer one.
	if _, ok := tr.observe(recorded(t, at, "rate_limited", claudeRateLimitRaw)); ok {
		t.Fatal("a stale reading overwrote the newest one")
	}

	snap := tr.snapshot()
	if len(snap) != 2 || snap[0].Provider != "claude" || snap[1].Provider != "codex" {
		t.Fatalf("snapshot %+v", snap)
	}
	if snap[0].FiveHour.Utilization != 0.9 {
		t.Fatalf("newest claude reading not kept: %+v", snap[0])
	}
}

func TestQuotaIgnoresUnrelatedEvents(t *testing.T) {
	tr := newQuotaTracker()
	at := time.Now()
	for _, ev := range []RunEvent{
		recorded(t, at, "tool_started", `{"type":"assistant"}`),
		recorded(t, at, "system", `{"type":"system","subtype":"init"}`),
		recorded(t, at, "rate_limited", `not json`),
		{T: at.Format(time.RFC3339Nano), Kind: "system"},
		// A rate_limited event recorded before the raw payload carried
		// windows must not invent a reading.
		recorded(t, at, "rate_limited", `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`),
	} {
		if q, ok := tr.observe(ev); ok {
			t.Fatalf("%s produced a quota: %+v", ev.Kind, q)
		}
	}
	if len(tr.snapshot()) != 0 {
		t.Fatalf("snapshot %+v", tr.snapshot())
	}
}

// writeEventLog writes a whole events.jsonl for a run.
func writeEventLog(t *testing.T, root, key, runID string, lines ...string) {
	t.Helper()
	dir := filepath.Join(root, ".sirdar", "runs", key, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaScanAtStartupAndLiveUpdate(t *testing.T) {
	root := newWorkspace(t)
	const key, runID = "OMNI-1", "20260910T090000Z-cccc"
	writeState(t, root, key, runID, store.StatusRunning)
	writeEventLog(t, root, key, runID,
		fmt.Sprintf(`{"t":%q,"kind":"system","payload":{"raw":%s}}`,
			time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), codexRateLimitRaw))

	reg := newRegistry(t, root)
	svc := New(reg, nil, Options{Interval: 20 * time.Millisecond})
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)
	defer svc.Stop()

	e := waitFor(t, events, "quota.updated from the startup scan", func(e Event) bool {
		return e.Kind == KindQuotaUpdate
	})
	if e.Quota.Provider != "codex" || e.Quota.UsedPercent == nil || *e.Quota.UsedPercent != 91 {
		t.Fatalf("quota %+v", e.Quota)
	}
	if got := svc.Quota(); len(got) != 1 || got[0].Provider != "codex" {
		t.Fatalf("Quota() %+v", got)
	}

	// A rate-limit line appended to a running run reaches the tracker
	// through the watcher.
	path := filepath.Join(root, ".sirdar", "runs", key, runID, "events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, `{"t":%q,"kind":"rate_limited","payload":{"raw":%s}}`+"\n",
		time.Now().UTC().Format(time.RFC3339Nano), claudeRateLimitRaw)
	f.Close()

	live := waitFor(t, events, "quota.updated from the watcher", func(e Event) bool {
		return e.Kind == KindQuotaUpdate && e.Quota.Provider == "claude"
	})
	if live.Quota.FiveHour == nil || live.Quota.FiveHour.Utilization != 0.5 {
		t.Fatalf("quota %+v", live.Quota)
	}
	if got := svc.Quota(); len(got) != 2 {
		t.Fatalf("Quota() %+v", got)
	}
}
