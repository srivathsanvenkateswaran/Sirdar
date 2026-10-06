package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// This file is the exported surface of shadow mode and of the suggestions
// queue: the methods the HTTP routes, the Wails bridge and the CLI call.
// Each is a thin wrapper over the unexported method that does the work, so
// argument parsing lives here and nowhere deeper.

// ShadowOutcomeInput is a manual outcome as the API and the bridge send it.
type ShadowOutcomeInput struct {
	Key        string   `json:"key"`
	PRURLs     []string `json:"prUrls"`
	Resolution string   `json:"resolution"`
}

// AcceptSuggestionInput is what the operator may change about a suggestion
// as they accept it. Either field left empty keeps the proposal's own.
type AcceptSuggestionInput struct {
	Playbook string `json:"playbook,omitempty"`
	Addition string `json:"addition,omitempty"`
}

// shadowReportWindow is how far back a report reaches when the caller names
// no since: long enough to cover outcomes.windowDays at its default.
const shadowReportWindow = 30 * 24 * time.Hour

// ShadowStatus reports what the shadow watcher is doing for the workspace,
// whichever process is doing it.
func (s *Service) ShadowStatus(wsID string) (ShadowStatus, error) {
	return s.shadowStatus(wsID)
}

// ShadowReport is the scorecard over the keys first started since a point
// in time. since is empty for the last 30 days, a duration back from now
// ("7d", "36h"), a date ("2026-10-01", midnight local time) or an RFC 3339
// time; anything else is ErrBadInput.
func (s *Service) ShadowReport(wsID, since string) (ShadowReport, error) {
	if err := checkID(ErrNoSuchWorkspace, "workspace", wsID); err != nil {
		return ShadowReport{}, err
	}
	from, err := s.reportSince(since)
	if err != nil {
		return ShadowReport{}, err
	}
	return s.shadowReport(wsID, from)
}

// reportSince reads a report's since parameter.
func (s *Service) reportSince(since string) (time.Time, error) {
	since = strings.TrimSpace(since)
	now := s.now()
	if since == "" {
		return now.Add(-shadowReportWindow), nil
	}
	if d, err := config.ParseDuration(since); err == nil {
		// A zero or negative duration would put since at or after now and
		// answer with an empty report that looks like no shadow runs.
		if d <= 0 {
			return time.Time{}, fmt.Errorf("%w: since %q: a duration must be positive", ErrBadInput, since)
		}
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", since, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, since); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%w: since %q: want a duration like 7d, a date like 2026-10-01, or an RFC 3339 time", ErrBadInput, since)
}

// ShadowPoll runs one watcher tick now. Another live process holding the
// workspace's shadow lock refuses it with ErrShadowLocked, alongside the
// status that names the holder.
func (s *Service) ShadowPoll(ctx context.Context, wsID string) (ShadowStatus, error) {
	return s.shadowPoll(ctx, wsID)
}

// ShadowPause stops new shadow starts for forDuration: empty is 24 hours,
// and anything else is read by config.ParseDuration and must be positive.
func (s *Service) ShadowPause(wsID, forDuration string) (ShadowStatus, error) {
	if err := checkID(ErrNoSuchWorkspace, "workspace", wsID); err != nil {
		return ShadowStatus{}, err
	}
	var d time.Duration
	if f := strings.TrimSpace(forDuration); f != "" {
		parsed, err := config.ParseDuration(f)
		if err != nil || parsed <= 0 {
			return ShadowStatus{}, fmt.Errorf("%w: for %q: want a positive duration like 4h or 2d", ErrBadInput, forDuration)
		}
		d = parsed
	}
	return s.shadowPause(wsID, d)
}

// ShadowResume lifts a pause early.
func (s *Service) ShadowResume(wsID string) (ShadowStatus, error) {
	return s.shadowResume(wsID)
}

// ShadowOutcome records an outcome the operator entered by hand, grades the
// key against it, and answers with the key's scorecard row.
func (s *Service) ShadowOutcome(ctx context.Context, wsID string, in ShadowOutcomeInput) (ShadowRow, error) {
	return s.shadowOutcome(ctx, wsID, in.Key, in.PRURLs, in.Resolution)
}

// ShadowRegrade collects outcomes for keys and grades them now; no keys
// means every key in the ledger.
func (s *Service) ShadowRegrade(ctx context.Context, wsID string, keys []string) ([]ShadowRow, error) {
	return s.shadowRegrade(ctx, wsID, keys)
}

// ShadowRun starts a shadow run of each key now, through the watcher's
// gate. backfill triages each ticket as it stood when it was created.
func (s *Service) ShadowRun(ctx context.Context, wsID string, keys []string, backfill bool) ([]ShadowStarted, error) {
	return s.shadowRun(ctx, wsID, keys, backfill)
}

// Remember turns one operator message on a run into a pending playbook
// addition. index 0 is the run's instruction; any other is the 1-based
// position of a steer or answer event.
func (s *Service) Remember(ctx context.Context, wsID, runID string, index int) (Suggestion, error) {
	return s.remember(ctx, wsID, runID, index)
}

// Suggestions lists the workspace's pending playbook additions, oldest
// first, and never nil.
func (s *Service) Suggestions(wsID string) ([]Suggestion, error) {
	return s.suggestions(wsID)
}

// AcceptSuggestion files a pending suggestion into its playbook, or into
// the one in.Playbook names, with in.Addition when the operator edited it.
func (s *Service) AcceptSuggestion(wsID, id string, in AcceptSuggestionInput) (Suggestion, error) {
	if err := checkID(ErrNoSuchSuggestion, "suggestion", id); err != nil {
		return Suggestion{}, err
	}
	return s.acceptSuggestion(wsID, id, in.Playbook, in.Addition)
}

// DismissSuggestion takes a pending suggestion out of the queue.
func (s *Service) DismissSuggestion(wsID, id string) error {
	if err := checkID(ErrNoSuchSuggestion, "suggestion", id); err != nil {
		return err
	}
	return s.dismissSuggestion(wsID, id)
}
