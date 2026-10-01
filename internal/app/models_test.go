package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

func TestModelLabel(t *testing.T) {
	for id, want := range map[string]string{
		"claude-opus-4-5-20251101":   "Opus 4.5",
		"claude-sonnet-4-5-20250929": "Sonnet 4.5",
		"claude-haiku-4-5-20251001":  "Haiku 4.5",
		"claude-sonnet-4-20250514":   "Sonnet 4",
		"claude-3-5-sonnet-20241022": "Sonnet 3.5",
		"claude-opus-5":              "Opus 5",
		"claude-fable-5-1":           "Fable 5.1",
		"claude-opus-5[1m]":          "Opus 5 (1M)",
		"opus":                       "Opus",
		"sonnet":                     "Sonnet",
		"gpt-5.6-luna":               "gpt-5.6-luna",
		"qwen3-coder":                "qwen3-coder",
		"claude-":                    "claude-",
		"claude-a-b":                 "claude-a-b",
		"":                           "",
	} {
		if got := ModelLabel(id); got != want {
			t.Errorf("ModelLabel(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestMergeModelsDedupesAcrossSources(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	cache := &modelCache{
		ProbedAt: now.Add(-time.Hour),
		Models: []probedModel{
			{Alias: "opus", ID: "claude-opus-4-5-20251101"},
			{Alias: "sonnet", ID: "claude-sonnet-4-5-20250929"},
		},
		Errors: []string{"haiku: no init"},
	}
	runs := []RunSummary{
		{Provider: "claude", Model: "claude-opus-4-5-20251101", UpdatedAt: "2026-09-30T10:00:00Z"},
		{Provider: "codex", Model: "gpt-5.6-luna", UpdatedAt: "2026-09-30T09:00:00Z"},
		{Provider: "claude", Model: "claude-opus-5[1m]", UpdatedAt: "2026-09-29T10:00:00Z"},
		{Provider: "claude", Model: "", UpdatedAt: "2026-09-28T10:00:00Z"},
		{Provider: "claude", Model: "claude-opus-5[1m]", UpdatedAt: "2026-09-20T10:00:00Z"},
	}
	pins := []config.ModelPin{{ID: "claude-sonnet-4-5-20250929", Label: "Sonnet (pinned)"}, {ID: "claude-opus-4-1"}}
	got := mergeModels("claude", cache, runs, pins, now)

	if !got.CanProbe || got.ProbeDue || got.ProbedAt == "" || len(got.ProbeErrors) != 1 {
		t.Fatalf("probe fields %+v", got)
	}
	want := []ModelInfo{
		{ID: "claude-opus-4-5-20251101", Label: "Opus 4.5", Source: "probe", SeenAt: got.ProbedAt, Alias: "opus"},
		{ID: "claude-sonnet-4-5-20250929", Label: "Sonnet (pinned)", Source: "probe", SeenAt: got.ProbedAt, Alias: "sonnet"},
		{ID: "claude-opus-5[1m]", Label: "Opus 5 (1M)", Source: "run", SeenAt: "2026-09-29T10:00:00Z"},
		{ID: "claude-opus-4-1", Label: "Opus 4.1", Source: "config"},
	}
	if !slices.Equal(got.Models, want) {
		t.Fatalf("models\n got %+v\nwant %+v", got.Models, want)
	}
}

func TestMergeModelsWithoutAProbe(t *testing.T) {
	now := time.Now()
	codex := mergeModels("codex", nil, []RunSummary{{Provider: "codex", Model: "gpt-5.6-luna"}}, nil, now)
	if codex.CanProbe || codex.ProbeDue || len(codex.Models) != 1 || codex.Models[0].Source != "run" {
		t.Fatalf("codex %+v", codex)
	}
	empty := mergeModels("claude", nil, nil, nil, now)
	if !empty.ProbeDue || empty.Models == nil || len(empty.Models) != 0 {
		t.Fatalf("an empty claude list is due a probe and is [] not null: %+v", empty)
	}
	stale := mergeModels("claude", &modelCache{ProbedAt: now.Add(-ModelsTTL - time.Minute)}, nil, nil, now)
	if !stale.ProbeDue {
		t.Fatal("a probe older than the TTL is due again")
	}
	fresh := mergeModels("claude", &modelCache{ProbedAt: now.Add(-ModelsTTL + time.Minute)}, nil, nil, now)
	if fresh.ProbeDue {
		t.Fatal("a probe inside the TTL is reused")
	}
}

// probeRecorder is a ProbeFunc that resolves the three aliases and counts
// every call, so a test can say how many times the CLI would have started.
type probeRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (p *probeRecorder) probe(_ context.Context, binary, alias string, _ []string) (string, error) {
	p.mu.Lock()
	p.calls = append(p.calls, alias)
	p.mu.Unlock()
	switch alias {
	case "opus":
		return "claude-opus-4-5-20251101", nil
	case "sonnet":
		return "claude-sonnet-4-5-20250929", nil
	}
	return "", errors.New("no init line")
}

func (p *probeRecorder) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func TestModelsNeverProbesAndRefreshCaches(t *testing.T) {
	root := newWorkspace(t)
	writeState(t, root, "OMNI-1", "20260930T100000Z-aaaa", store.StatusCompleted)
	rec := &probeRecorder{}
	clock := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	svc := New(newRegistry(t, root), nil, Options{
		ModelsDir:  dir,
		ProbeModel: rec.probe,
		Now:        func() time.Time { return clock },
	})
	ws := WorkspaceID(root)

	list, err := svc.Models(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.count() != 0 {
		t.Fatal("Models started the CLI")
	}
	if list.Provider != "claude" || !list.ProbeDue || len(list.Models) != 1 || list.Models[0].ID != "claude-haiku-4-5" {
		t.Fatalf("before a probe: %+v", list)
	}

	list, err = svc.RefreshModels(context.Background(), ws, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if rec.count() != 3 {
		t.Fatalf("probed %v, want opus, sonnet and haiku once each", rec.calls)
	}
	if list.ProbeDue || len(list.Models) != 3 || list.Models[0].Label != "Opus 4.5" || len(list.ProbeErrors) != 1 {
		t.Fatalf("after a probe: %+v", list)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude.json")); err != nil {
		t.Fatalf("no cache file: %v", err)
	}

	// A second refresh inside the debounce answers from the cache.
	clock = clock.Add(5 * time.Second)
	if _, err := svc.RefreshModels(context.Background(), ws, "claude"); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 3 {
		t.Fatalf("a refresh inside %s probed again: %v", refreshDebounce, rec.calls)
	}

	// The cache survives the service, and is reused until the TTL runs out.
	svc2 := New(newRegistry(t, root), nil, Options{ModelsDir: dir, ProbeModel: rec.probe, Now: func() time.Time { return clock.Add(23 * time.Hour) }})
	if l, _ := svc2.Models(ws, "claude"); l.ProbeDue || len(l.Models) != 3 {
		t.Fatalf("cache not reused within the TTL: %+v", l)
	}
	svc3 := New(newRegistry(t, root), nil, Options{ModelsDir: dir, ProbeModel: rec.probe, Now: func() time.Time { return clock.Add(25 * time.Hour) }})
	if l, _ := svc3.Models(ws, "claude"); !l.ProbeDue {
		t.Fatalf("cache past the TTL is not due: %+v", l)
	}

	// A provider that cannot be probed refreshes without starting anything.
	before := rec.count()
	if l, err := svc.RefreshModels(context.Background(), ws, "codex"); err != nil || l.CanProbe {
		t.Fatalf("codex refresh: %+v %v", l, err)
	}
	if rec.count() != before {
		t.Fatal("a codex refresh started the claude CLI")
	}
	if _, err := svc.Models(ws, "nope"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("an unknown provider: %v", err)
	}
}
