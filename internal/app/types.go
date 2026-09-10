// Package app is the application service both Sirdar desktop shells sit on:
// the Wails app and `sirdar serve`. It owns the workspace registry, watches
// the run directories the core writes, derives provider quota from the
// recorded events, and starts and cancels runs on behalf of a UI.
//
// Every type that crosses to the frontend carries camelCase JSON tags
// matching desktop/frontend/src/api/types.ts.
package app

import (
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// Event kinds published on Subscribe.
const (
	KindRunUpdated  = "run.updated"
	KindRunEvent    = "run.event"
	KindQuotaUpdate = "quota.updated"
	KindJobFinished = "job.finished"
	KindLog         = "log"
)

// Usage is a run's provider consumption.
type Usage struct {
	Turns        int     `json:"turns"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CostUSD      float64 `json:"costUsd"`
}

// Budget is the cap a run was started under.
type Budget struct {
	MaxTurns   int     `json:"maxTurns"`
	MaxMinutes int     `json:"maxMinutes"`
	MaxUSD     float64 `json:"maxUsd"`
}

// RunSummary is the card-sized view of one run.
type RunSummary struct {
	RunID     string   `json:"runId"`
	Key       string   `json:"key"`
	Kind      string   `json:"kind"`
	Status    string   `json:"status"`
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	StartedAt string   `json:"startedAt"`
	UpdatedAt string   `json:"updatedAt"`
	Reason    string   `json:"reason"`
	Usage     Usage    `json:"usage"`
	Notes     []string `json:"notes"`
}

// RunDetail is a run's full view: the summary plus the paths and limits the
// detail screen shows.
type RunDetail struct {
	RunSummary
	PromptPath string   `json:"promptPath"`
	BundleDir  string   `json:"bundleDir"`
	Warnings   []string `json:"warnings"`
	Handle     string   `json:"handle"`
	Budget     Budget   `json:"budget"`
}

// EventPayload mirrors the payload internal/run writes to events.jsonl.
type EventPayload struct {
	Tool     string          `json:"tool,omitempty"`
	Decision string          `json:"decision,omitempty"`
	Text     string          `json:"text,omitempty"`
	Turns    int             `json:"turns,omitempty"`
	CostUSD  float64         `json:"costUsd,omitempty"`
	Raw      json.RawMessage `json:"raw,omitempty"`
}

// RunEvent is one line of a run's events.jsonl.
type RunEvent struct {
	T       string       `json:"t"`
	Kind    string       `json:"kind"`
	Payload EventPayload `json:"payload"`
}

// Ticket is one queue row: the tracker record plus the newest run for it.
type Ticket struct {
	Key         string      `json:"key"`
	Title       string      `json:"title"`
	Priority    string      `json:"priority"`
	Status      string      `json:"status"`
	Assignee    string      `json:"assignee"`
	URL         string      `json:"url"`
	HelpdeskRef string      `json:"helpdeskRef"`
	UpdatedAt   string      `json:"updatedAt"`
	LatestRun   *RunSummary `json:"latestRun,omitempty"`
}

// QuotaWindow is one rate-limit window as Claude reports it.
type QuotaWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resetsAt"`
}

// Quota is the newest rate-limit reading for one provider. Claude fills
// FiveHour and SevenDay; Codex fills UsedPercent and ResetsAt.
type Quota struct {
	Provider    string       `json:"provider"`
	ObservedAt  string       `json:"observedAt"`
	FiveHour    *QuotaWindow `json:"fiveHour,omitempty"`
	SevenDay    *QuotaWindow `json:"sevenDay,omitempty"`
	UsedPercent *float64     `json:"usedPercent,omitempty"`
	ResetsAt    string       `json:"resetsAt,omitempty"`
}

// Check is one Doctor diagnostic result.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// RegisterRow is one line of the workspace's run register, tagged for the
// wire. store.RegisterRow stays the on-disk shape.
type RegisterRow struct {
	Key            string  `json:"key"`
	Kind           string  `json:"kind"`
	RunID          string  `json:"runId"`
	Date           string  `json:"date"`
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	Service        string  `json:"service"`
	Classification string  `json:"classification"`
	Confidence     string  `json:"confidence"`
	Severity       string  `json:"severity"`
	Turns          int     `json:"turns"`
	CostUSD        float64 `json:"costUsd"`
	TriageVerdict  string  `json:"triageVerdict"`
	NotePath       string  `json:"notePath"`
}

// JobID identifies a run this process started, so it can be cancelled.
type JobID string

// QueueFilter narrows the tracker query behind Queue. Zero fields are
// unfiltered; Limit zero means no limit.
type QueueFilter struct {
	Assignee string `json:"assignee"`
	Status   string `json:"status"`
	Limit    int    `json:"limit"`
}

// TriageOptions are the per-invocation overrides a triage job takes.
type TriageOptions struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	DryRun   bool   `json:"dryRun"`
}

// RCAOptions are the two inputs only an RCA run takes.
type RCAOptions struct {
	PRURL      string `json:"prUrl"`
	Resolution string `json:"resolution"`
}

// JobOutcome is one key's result in a finished job.
type JobOutcome struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	RunID  string `json:"runId"`
}

// Event is one message on the fan-out. Kind decides which fields are set;
// the rest are omitted on the wire, so the union in types.ts holds.
type Event struct {
	Kind        string       `json:"kind"`
	WorkspaceID string       `json:"workspaceId,omitempty"`
	Run         *RunSummary  `json:"run,omitempty"`
	RunID       string       `json:"runId,omitempty"`
	Index       int          `json:"index,omitempty"`
	Event       *RunEvent    `json:"event,omitempty"`
	Quota       *Quota       `json:"quota,omitempty"`
	JobID       JobID        `json:"jobId,omitempty"`
	Outcomes    []JobOutcome `json:"outcomes,omitempty"`
	Text        string       `json:"text,omitempty"`
}

// runDir is where the core keeps one run: <root>/.sirdar/runs/<key>/<id>.
func runDir(root, key, runID string) string {
	return filepath.Join(root, ".sirdar", "runs", key, runID)
}

// wireTime renders a timestamp the way the frontend reads it, and an unset
// one as the empty string rather than year 1.
func wireTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// SummaryOf converts a persisted run state into its wire summary.
func SummaryOf(s store.State) RunSummary {
	notes := s.Notes
	if notes == nil {
		notes = []string{}
	}
	return RunSummary{
		RunID:     s.RunID,
		Key:       s.Key,
		Kind:      string(s.Kind),
		Status:    string(s.Status),
		Provider:  s.Provider,
		Model:     s.Model,
		StartedAt: wireTime(s.StartedAt),
		UpdatedAt: wireTime(s.UpdatedAt),
		Reason:    s.Reason,
		Usage: Usage{
			Turns:        s.Usage.Turns,
			InputTokens:  s.Usage.InputTokens,
			OutputTokens: s.Usage.OutputTokens,
			CostUSD:      s.Usage.CostUSD,
		},
		Notes: notes,
	}
}

// DetailOf converts a persisted run state into its wire detail, resolving
// the run's own paths against the workspace root.
func DetailOf(root string, s store.State) RunDetail {
	dir := runDir(root, s.Key, s.RunID)
	warnings := s.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return RunDetail{
		RunSummary: SummaryOf(s),
		PromptPath: filepath.Join(dir, "prompt.md"),
		BundleDir:  filepath.Join(dir, "bundle"),
		Warnings:   warnings,
		Handle:     s.Handle,
		Budget: Budget{
			MaxTurns:   s.Budget.MaxTurns,
			MaxMinutes: s.Budget.MaxMinutes,
			MaxUSD:     s.Budget.MaxUSD,
		},
	}
}

// RegisterRowOf converts a stored register row into its wire shape.
func RegisterRowOf(r store.RegisterRow) RegisterRow {
	return RegisterRow{
		Key:            r.Key,
		Kind:           r.Kind,
		RunID:          r.RunID,
		Date:           r.Date,
		Provider:       r.Provider,
		Model:          r.Model,
		Service:        r.Service,
		Classification: r.Classification,
		Confidence:     r.Confidence,
		Severity:       r.Severity,
		Turns:          r.Turns,
		CostUSD:        r.CostUSD,
		TriageVerdict:  r.TriageVerdict,
		NotePath:       r.NotePath,
	}
}
