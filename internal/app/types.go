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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Event kinds published on Subscribe.
const (
	KindRunUpdated  = "run.updated"
	KindRunEvent    = "run.event"
	KindQuotaUpdate = "quota.updated"
	KindJobFinished = "job.finished"
	KindLog         = "log"
	// KindHookReceived reports one inbound webhook delivery: which source
	// sent it, which ticket it named, and what came of it.
	KindHookReceived = "hook.received"
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
//
// Title is the ticket's, read off the run directory: the bundle's tracker
// title or helpdesk subject, else the title of the first note the run
// wrote, else empty. A card shows it over the key; without it the key is
// the title.
type RunSummary struct {
	RunID     string   `json:"runId"`
	Key       string   `json:"key"`
	Title     string   `json:"title"`
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
	// Fix is set only for a fix run that recorded something: the branch
	// the work sits on, the commit, the pull request once it exists, and
	// the deviation a person has to accept before the commit is pushed.
	Fix *FixInfo `json:"fix,omitempty"`

	// Steers lists the follow-up instructions the run has taken, oldest
	// first, and who answered each: "resume" for the session that wrote
	// the note, "primed" for a fresh one handed it.
	Steers []SteerInfo `json:"steers,omitempty"`
}

// SteerInfo is one follow-up instruction on a run, for the run detail.
type SteerInfo struct {
	At           string `json:"at"`
	Text         string `json:"text"`
	Continuation string `json:"continuation"`
}

// FixInfo is where a fix run's work went, read off the run's state.json.
// Nothing in it is a secret: a branch name, a commit sha, a pull request
// URL, and what the agent said it did instead of the note.
type FixInfo struct {
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	Commit string `json:"commit,omitempty"`
	PRURL  string `json:"prUrl,omitempty"`
	// Pushed says the branch reached the remote. It is what tells work that
	// has left the machine from work still waiting on a person: a `--no-pr`
	// run and one whose `gh` call failed are both pushed with no PRURL.
	Pushed bool `json:"pushed,omitempty"`
	// Deviation is non-empty when the agent reported doing something other
	// than the triage note's Proposed Fix. The commit is on the branch and
	// has not been pushed; a rerun with acceptDeviation pushes the commit
	// that was reviewed rather than starting a second session.
	Deviation string `json:"deviation,omitempty"`
}

// EventPayload mirrors the payload internal/run writes to events.jsonl,
// and the one internal/fix writes for a "review" event.
type EventPayload struct {
	Tool     string          `json:"tool,omitempty"`
	Decision string          `json:"decision,omitempty"`
	Text     string          `json:"text,omitempty"`
	Turns    int             `json:"turns,omitempty"`
	CostUSD  float64         `json:"costUsd,omitempty"`
	Raw      json.RawMessage `json:"raw,omitempty"`

	// Continuation is carried by a `steer` event alone: "resume" or
	// "primed", saying whether the session answering the instruction is
	// the one that wrote the note.
	Continuation string `json:"continuation,omitempty"`

	// Action, Path and Hunk carry a "review" event: what a person did to
	// the fix commit after the session ended. Hunk is a pointer because
	// its zero is a real index — the first hunk of a file is the one most
	// often dropped — and omitting it would say a hunk was dropped
	// without saying which.
	Action string `json:"action,omitempty"`
	Path   string `json:"path,omitempty"`
	Hunk   *int   `json:"hunk,omitempty"`
}

// RunEvent is one line of a run's events.jsonl.
type RunEvent struct {
	T       string       `json:"t"`
	Kind    string       `json:"kind"`
	Payload EventPayload `json:"payload"`
}

// DiffFile is one file in a fix run's change, with the counts a file list
// shows beside it. Status is "added", "modified", "deleted" or "renamed";
// a renamed file is named by the path it now has.
type DiffFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// RunDiff is one fix run's change as a reviewer reads it: what it is
// against, where it lives, the file list, and the unified patch.
//
// Worktree is where the run committed and WorktreePresent whether that
// directory is still there — a change is readable either way, but only a
// worktree that is still there can have a hunk dropped out of it. Pushed
// says the branch has left the machine, which closes the drop too.
type RunDiff struct {
	Base            string     `json:"base"`
	Head            string     `json:"head"`
	Branch          string     `json:"branch"`
	Worktree        string     `json:"worktree"`
	WorktreePresent bool       `json:"worktreePresent"`
	Pushed          bool       `json:"pushed"`
	Files           []DiffFile `json:"files"`
	Patch           string     `json:"patch"`
	// Truncated says the patch was cut at 2 MiB. The file list is whole
	// either way: it is the counts, not the text.
	Truncated bool `json:"truncated,omitempty"`
	// ETag is a hash of the patch this call computed. A drop carries it
	// back, so a hunk index cannot be applied to a patch other than the
	// one it was read from.
	ETag string `json:"etag"`
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

// Check is one Doctor diagnostic result. Level is "ok", "warn" or "fail";
// OK stays what it always was — false only for "fail" — so an older client
// reading the bool alone sees a warning as a passing check, which is what
// it is.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Level  string `json:"level"`
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
	Title          string  `json:"title"`
	Company        string  `json:"company"`
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
	// At runs the session against the repository as it stood at this
	// commit, in a linked worktree of the run's own, and KeepWorktree
	// leaves that directory on disk afterwards.
	At           string `json:"at"`
	KeepWorktree bool   `json:"keepWorktree"`
}

// RCAOptions are the inputs an RCA run takes: the two only it has, and the
// same one-off provider and model override every other start accepts.
type RCAOptions struct {
	PRURL      string `json:"prUrl"`
	Resolution string `json:"resolution"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	// At and KeepWorktree mean here what they mean on a triage: read the
	// repository as it stood at that commit, and keep the worktree.
	At           string `json:"at"`
	KeepWorktree bool   `json:"keepWorktree"`
}

// FixOptions are the flags of one fix job, matching `sirdar fix`.
//
// AcceptDeviation is the human gate: a fix whose agent reported deviating
// from the note's Proposed Fix commits locally and stops, and a rerun with
// this set pushes the commit that was reviewed rather than starting a
// second session over it.
type FixOptions struct {
	DryRun          bool   `json:"dryRun"`
	NoPR            bool   `json:"noPr"`
	Base            string `json:"base"`
	AcceptDeviation bool   `json:"acceptDeviation"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	// At cuts the fix branch from this commit instead of origin/<base>.
	// Local stops the flow at the commit: nothing is pushed, no pull
	// request is opened, the worktree is kept and the commit's diff is
	// written to fix.diff in the run directory.
	At    string `json:"at"`
	Local bool   `json:"local"`
}

// EvalOptions are the per-invocation overrides an eval job takes. The
// golden set itself is the service's, not the caller's: a UI that could
// name a directory could read any bundle on the machine through it.
type EvalOptions struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Concurrency int    `json:"concurrency"`

	// Retro replays each key at the commit its fix branched from and
	// scores what comes back against the pull request that fixed it,
	// rather than against the assertions a human wrote. Only the golden
	// keys carrying a retro.json can be replayed this way.
	Retro bool `json:"retro"`
	// WithRCA adds the blind RCA stage to a retro, and Rubric the one
	// judged provider call. Both are ignored without Retro.
	WithRCA bool `json:"withRca"`
	Rubric  bool `json:"rubric"`
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
	// Source, Key and Outcome carry a hook.received event: the webhook
	// source name, the ticket key the delivery named (empty when it named
	// none), and what the receiver did with it: "started", "skipped",
	// "filtered", "ignored" or "rejected", which is the whole set the hook
	// route emits.
	Source  string `json:"source,omitempty"`
	Key     string `json:"key,omitempty"`
	Outcome string `json:"outcome,omitempty"`
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

// SummaryOf converts a persisted run state into its wire summary. It reads
// nothing off disk, so Title stays empty; SummaryAt fills it from the run
// directory.
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

// SummaryAt is SummaryOf with the ticket title the run directory holds.
func SummaryAt(dir string, s store.State) RunSummary {
	out := SummaryOf(s)
	out.Title = titleOf(dir, s.Notes)
	return out
}

// titleOf finds the ticket title for a run kept at dir: the bundle's
// tracker title, else its helpdesk subject, else the first note's own
// title, else "". A run directory with neither is not an error; the key
// stands in.
func titleOf(dir string, notes []string) string {
	if data, err := os.ReadFile(filepath.Join(dir, "bundle", "ticket.json")); err == nil {
		var b ticket.Bundle
		if json.Unmarshal(data, &b) == nil {
			if b.Tracker != nil && strings.TrimSpace(b.Tracker.Title) != "" {
				return strings.TrimSpace(b.Tracker.Title)
			}
			if b.Helpdesk != nil && strings.TrimSpace(b.Helpdesk.Subject) != "" {
				return strings.TrimSpace(b.Helpdesk.Subject)
			}
		}
	}
	for _, path := range notes {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if t := noteTitle(string(data)); t != "" {
			return t
		}
	}
	return ""
}

// noteTitle is a note's title: a `title` key in its frontmatter when it
// has one, else the first H1, which is where the shipped templates put it.
func noteTitle(text string) string {
	if t := strings.TrimSpace(note.Frontmatter(text, "title")); t != "" {
		return t
	}
	inFrontmatter := false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" && (i == 0 || inFrontmatter) {
			inFrontmatter = !inFrontmatter
			continue
		}
		if inFrontmatter {
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return ""
}

// DetailOf converts a persisted run state into its wire detail, resolving
// the run's own paths against the workspace root.
func DetailOf(root string, s store.State) RunDetail {
	dir := runDir(root, s.Key, s.RunID)
	warnings := s.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	d := RunDetail{
		RunSummary: SummaryAt(dir, s),
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
	if f := (FixInfo{
		Branch:    s.Fix.Branch,
		Base:      s.Fix.Base,
		Commit:    s.Fix.Commit,
		PRURL:     s.Fix.PRURL,
		Pushed:    s.Fix.Pushed,
		Deviation: s.Fix.Deviation,
	}); f != (FixInfo{}) {
		d.Fix = &f
	}
	for _, st := range s.Steers {
		d.Steers = append(d.Steers, SteerInfo{At: wireTime(st.At), Text: st.Text, Continuation: st.Continuation})
	}
	return d
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
		Title:          r.Title,
		Company:        r.Company,
		NotePath:       r.NotePath,
	}
}

// --- identifiers ------------------------------------------------------

// ErrInvalidArgument marks an identifier this package refuses to look up.
// Ids reach the service straight from an HTTP route or query string, where
// ServeMux has already unescaped each segment: `..%2F..` arrives as `../..`
// and `%2A` as `*`. Both would otherwise reach filepath.Join and
// filepath.Glob inside internal/store, so they are rejected here rather
// than sanitised deeper down.
//
// Every invalid id is also reported as the matching "no such thing"
// sentinel, so the HTTP layer answers 404 — an id that cannot name
// anything names nothing — without needing to know about this error.
var ErrInvalidArgument = errors.New("app: invalid identifier")

// idRejects are the characters that make an id something other than one
// plain path element: separators, and the glob metacharacters
// filepath.Glob would act on.
const idRejects = `/\*?[`

// validateID rejects an identifier that could escape the run tree or turn
// a lookup into a pattern match.
func validateID(s string) error {
	switch {
	case s == "":
		return fmt.Errorf("%w: empty", ErrInvalidArgument)
	case s == "." || s == "..":
		return fmt.Errorf("%w: %q is a directory reference", ErrInvalidArgument, s)
	case s != strings.TrimSpace(s):
		// " OMNI-1" and "OMNI-1" are two keys as far as the run store is
		// concerned, so a padded one would have its own run directory and
		// miss both the running check and the cooldown. Refused rather
		// than trimmed: the caller should send the key it means.
		return fmt.Errorf("%w: %q is padded with whitespace", ErrInvalidArgument, s)
	case strings.ContainsAny(s, idRejects):
		return fmt.Errorf("%w: %q contains a path separator or a glob character", ErrInvalidArgument, s)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %q contains a control character", ErrInvalidArgument, s)
		}
	}
	return nil
}

// checkID validates one identifier and, when it fails, reports it as both
// invalid and unknown to whichever sentinel names that kind of id.
func checkID(unknown error, what, value string) error {
	if err := validateID(value); err != nil {
		return fmt.Errorf("%w: %s %q: %w", unknown, what, value, ErrInvalidArgument)
	}
	return nil
}
