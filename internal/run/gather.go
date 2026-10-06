package run

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/srivathsanvenkateswaran/sirdar/internal/gather"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// The inline caps the prompt package expects of GatheredEntry.Inline: a
// result this short is a fact worth a tool turn less, and the total keeps
// a ticket that names a dozen short lookups from filling the prompt with
// them.
const (
	inlineMax      = 1500
	inlineTotalMax = 6144
)

// prepareLog opens the run's event log for the lines prepare writes before
// any session exists: the playbooks the run loaded and what it gathered.
// The sink takes a reopen by execute afterwards, so the session's own
// lines follow these in the same file.
func (r *Runner) prepareLog(p *prepared) (*eventLog, error) {
	return r.openEventLog(p.run)
}

// gather collects the run's context before the prompt is built, and
// returns it as the prompt describes it. A replayed bundle keeps the
// context it was recorded with and runs nothing, so an eval never calls a
// live system; an eval without one, a --no-gather run and a workspace that
// turned gathering off get none. Nothing here fails the run: a lookup that
// went wrong is a row saying so.
func (r *Runner) gather(ctx context.Context, p *prepared, log *eventLog, o Options, rca *RCAOptions, bundle ticket.Bundle, slackMD string) []prompt.GatheredEntry {
	cfg := r.Config
	bundleDir := p.run.BundleDir()
	contextDir := filepath.Join(bundleDir, "context")

	if o.BundleDir != "" {
		entries, err := gather.ReadIndex(contextDir)
		if err != nil {
			p.state.Warnings = append(p.state.Warnings, "the replayed bundle's context/index.json is unreadable: "+err.Error())
			return nil
		}
		return gatheredForPrompt(bundleDir, entries)
	}
	if o.Eval || o.NoGather || !cfg.GatherEnabled() {
		return nil
	}
	switch p.kind {
	case store.KindTriage, store.KindRCA, store.KindSession:
	default:
		return nil
	}

	p.state.Phase = store.PhaseGather
	r.writePrepareState(p)

	in := gather.Input{
		Kind:        string(p.kind),
		Config:      cfg,
		ContextDir:  contextDir,
		RunDir:      p.run.Dir,
		BundleDir:   bundleDir,
		Bundle:      bundle,
		Slack:       slackMD,
		Instruction: p.state.Instruction,
		Env:         r.gatherEnv(),
		GOOS:        runtime.GOOS,
		Now:         r.now,
		Started: func(n int) {
			noun := "lookups"
			if n == 1 {
				noun = "lookup"
			}
			r.prepareLine(p, log, "system", eventPayload{Text: fmt.Sprintf("Gathering context: %d %s", n, noun)})
		},
		Done: func(e gather.Entry) {
			// A result is told by what it found; anything else by why
			// it found nothing, which is what the operator needs to act.
			said := e.Summary
			if (e.Status != gather.StatusOK && e.Status != gather.StatusTruncated && e.Reason != "") || said == "" {
				said = e.Reason
			}
			r.prepareLine(p, log, "gather", eventPayload{Tool: e.Name, Text: e.Call + " → " + said, Decision: e.Status, Phase: store.PhaseGather})
			fmt.Fprintf(r.stderr(), "[%s] gather %s: %s\n", p.state.Key, e.Name, e.Status)
		},
	}
	if rca != nil {
		in.SkipPR = rca.PRURL
	}
	res := gather.Run(ctx, in)

	if n := len(res.Entries); n > 0 {
		found := 0
		for _, e := range res.Entries {
			if e.Status == gather.StatusOK || e.Status == gather.StatusTruncated {
				found++
			}
		}
		r.prepareLine(p, log, "system", eventPayload{Text: fmt.Sprintf("Gathered %d of %d in %.1fs", found, n, res.Took.Seconds())})
	}
	// A denied step is the operator's configuration, not evidence about
	// the ticket, so it goes to the run's warnings and never to the
	// bundle's, which the prompt quotes.
	for _, e := range res.Entries {
		if e.Status == gather.StatusDenied {
			p.state.Warnings = append(p.state.Warnings, "gather step "+e.Name+" denied: "+e.Call+": "+e.Reason)
		}
	}
	p.state.Phase = ""
	r.writePrepareState(p)
	return gatheredForPrompt(bundleDir, res.Entries)
}

// gatherEnv is the environment every gather step runs with: the session's
// own child environment, credentials stripped. It is never nil, because a
// nil Env makes exec hand the child this process's whole environment —
// in the desktop app, every token the operator's login shell exported.
func (r *Runner) gatherEnv() []string {
	env := r.childEnv()
	if env == nil {
		env = []string{}
	}
	return env
}

// prepareLine writes one event prepare owns, saying so on stderr when the
// log will not take it: a missing line is worth a word, not a failed run.
func (r *Runner) prepareLine(p *prepared, log *eventLog, kind string, payload eventPayload) {
	if err := log.Append(kind, payload); err != nil {
		fmt.Fprintf(r.stderr(), "[%s] event log: %v\n", p.state.Key, err)
	}
}

// writePrepareState persists the state mid-preparation, saying so on
// stderr when it cannot: the phase it carries is for the live view only.
func (r *Runner) writePrepareState(p *prepared) {
	p.state.UpdatedAt = r.now()
	if err := p.run.WriteState(p.state); err != nil {
		fmt.Fprintf(r.stderr(), "[%s] state: %v\n", p.state.Key, err)
	}
}

// gatheredForPrompt maps the gather's rows to the prompt's, inlining the
// output of each short ok result while the run's inlined total stays
// within inlineTotalMax. File paths in entries are relative to bundleDir.
func gatheredForPrompt(bundleDir string, entries []gather.Entry) []prompt.GatheredEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]prompt.GatheredEntry, 0, len(entries))
	inlined := 0
	for _, e := range entries {
		g := prompt.GatheredEntry{
			Name:      e.Name,
			File:      e.File,
			Status:    e.Status,
			Reason:    e.Reason,
			Summary:   e.Summary,
			Bytes:     e.Bytes,
			Diff:      e.Diff,
			DiffBytes: e.DiffBytes,
		}
		if e.Status == gather.StatusOK && e.File != "" && e.Bytes <= inlineMax {
			if text, err := gather.ReadOutput(filepath.Join(bundleDir, filepath.FromSlash(e.File))); err == nil &&
				text != "" && len(text) <= inlineMax && inlined+len(text) <= inlineTotalMax {
				g.Inline = text
				inlined += len(text)
			}
		}
		out = append(out, g)
	}
	return out
}
