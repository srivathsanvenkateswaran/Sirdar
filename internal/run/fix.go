package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// FixOptions are the inputs to a fix run. Prompt is assembled by
// internal/fix, which is also what decides whether a fix may happen at all:
// the run package's job here is to drive one session and file its answer,
// not to judge the ticket.
type FixOptions struct {
	Options

	// Prompt is the fully assembled fix prompt.
	Prompt string

	// Branch is the branch the workspace has already been put on, and Base
	// the branch it was cut from. Both are recorded in the run state, so
	// `sirdar runs` says where the work went and a later rerun can find
	// the commit this run produced.
	Branch string
	Base   string
}

// Fix runs one write-enabled agent session against the workspace and files
// its JSON report as result.json in the run directory. It makes no branch,
// no commit and no pull request: internal/fix does all of that around this
// call, so the git side of the flow is testable without an agent and the
// agent side is testable without a remote.
//
// The run directory is the same shape every other run gets,
// .sirdar/runs/<KEY>/<run-id>, with kind "fix" in state.json.
func (r *Runner) Fix(ctx context.Context, key string, o FixOptions) (Outcome, error) {
	if r.Config == nil {
		return Outcome{}, fmt.Errorf("run: no workspace configuration")
	}
	p, err := r.prepareFix(key, o)
	if err != nil {
		return r.prepareFailed(p, key, store.KindFix, err), err
	}
	if o.DryRun {
		return r.finish(p, store.StatusCompleted, "dry-run", note.DigestRow{}), nil
	}
	return r.execute(ctx, p, "", newPool(r.onPause)), nil
}

// prepareFix creates the run directory and writes the prompt. There is no
// bundle: a fix reads the triage note, which internal/fix has already put
// into the prompt, and the workspace it is standing in.
func (r *Runner) prepareFix(key string, o FixOptions) (*prepared, error) {
	cfg := r.Config
	now := r.now()

	if err := validateKey(key); err != nil {
		return nil, err
	}
	if o.Prompt == "" {
		return nil, fmt.Errorf("run: a fix run needs a prompt")
	}

	rn, err := store.Create(cfg.Root, key, now)
	if err != nil {
		return nil, err
	}
	model := o.Model
	if model == "" {
		model = cfg.Model
	}

	p := &prepared{run: rn, kind: store.KindFix, promptText: o.Prompt}
	p.state = store.State{
		RunID:     filepath.Base(rn.Dir),
		Key:       key,
		Kind:      store.KindFix,
		Status:    store.StatusPreparing,
		Provider:  r.providerName(),
		Model:     model,
		StartedAt: now,
		UpdatedAt: now,
	}
	p.state.Budget.MaxTurns = cfg.Budget.MaxTurns
	p.state.Budget.MaxMinutes = cfg.Budget.MaxMinutes
	p.state.Budget.MaxUSD = cfg.Budget.MaxUSD
	p.state.Fix.Branch = o.Branch
	p.state.Fix.Base = o.Base
	if o.Branch != "" {
		p.state.Warnings = append(p.state.Warnings, "fix branch: "+o.Branch)
	}
	if err := rn.WriteState(p.state); err != nil {
		return p, err
	}
	if err := os.WriteFile(filepath.Join(rn.Dir, "prompt.md"), []byte(o.Prompt), 0o644); err != nil {
		return p, fmt.Errorf("run: write prompt: %w", err)
	}
	return p, nil
}

// fixFields are the parts of a validated fix report the run itself reads.
type fixFields struct {
	Summary           string   `json:"summary"`
	FilesChanged      []string `json:"filesChanged"`
	DeviationFromNote string   `json:"deviationFromNote"`
}

// completeFix records a fix session's report. result.json is already on
// disk by the time this is called; there is no note to render and no
// register row to append, because what a fix is worth recording for — the
// commit, the branch, the pull request — does not exist yet.
func (r *Runner) completeFix(p *prepared, doc []byte) (note.DigestRow, error) {
	var f fixFields
	if err := json.Unmarshal(doc, &f); err != nil {
		return note.DigestRow{}, fmt.Errorf("run: parse fix report: %w", err)
	}
	if f.DeviationFromNote != "" {
		p.state.Warnings = append(p.state.Warnings, "the agent deviated from the note: "+f.DeviationFromNote)
	}
	return note.DigestRow{
		Issue:          firstSentence(f.Summary),
		Classification: "fix",
	}, nil
}

// FixReport reads back the JSON report a fix run filed, given the run's
// directory. It is how internal/fix gets the agent's answer without the
// two packages sharing a type through the Outcome.
func FixReport(root, key, runID string) ([]byte, error) {
	path := filepath.Join(root, ".sirdar", "runs", key, runID, "result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("run: read fix report: %w", err)
	}
	return data, nil
}
