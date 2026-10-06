package app

import (
	"errors"
	"testing"
	"time"
)

// TestShadowArgumentsAreBadInput: a since or a for that cannot be read is
// the caller's mistake, which the HTTP layer answers with 400.
func TestShadowArgumentsAreBadInput(t *testing.T) {
	root := newWorkspace(t)
	reg := newRegistry(t, root)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	svc := New(reg, nil, Options{Now: func() time.Time { return now }})
	list, err := reg.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("registry %v %v", list, err)
	}
	ws := list[0].ID

	for _, since := range []string{"last tuesday", "0d", "-4h", "2026-13-01"} {
		if _, err := svc.ShadowReport(ws, since); !errors.Is(err, ErrBadInput) {
			t.Errorf("ShadowReport since %q: %v, want ErrBadInput", since, err)
		}
	}
	for since, want := range map[string]time.Time{
		"":           now.Add(-30 * 24 * time.Hour),
		"7d":         now.Add(-7 * 24 * time.Hour),
		"2026-10-01": time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
	} {
		rep, err := svc.ShadowReport(ws, since)
		if err != nil {
			t.Fatalf("ShadowReport since %q: %v", since, err)
		}
		if rep.Since != want.Format(time.RFC3339) {
			t.Errorf("ShadowReport since %q reports from %s, want %s", since, rep.Since, want.Format(time.RFC3339))
		}
	}

	for _, d := range []string{"soon", "0h", "-1h"} {
		if _, err := svc.ShadowPause(ws, d); !errors.Is(err, ErrBadInput) {
			t.Errorf("ShadowPause for %q: %v, want ErrBadInput", d, err)
		}
	}
	st, err := svc.ShadowPause(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := wireTime(now.Add(24 * time.Hour)); st.State != "paused" || st.PausedUntil != want {
		t.Fatalf("an empty for paused until %q (%s), want %q", st.PausedUntil, st.State, want)
	}

	if err := svc.DismissSuggestion(ws, "../x"); !errors.Is(err, ErrNoSuchSuggestion) {
		t.Fatalf("DismissSuggestion with a path in the id: %v", err)
	}
	if _, err := svc.AcceptSuggestion(ws, "s-000000000000", AcceptSuggestionInput{}); !errors.Is(err, ErrNoSuchSuggestion) {
		t.Fatalf("AcceptSuggestion unknown: %v", err)
	}
}
