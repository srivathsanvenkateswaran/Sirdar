package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/worktree"
)

// SteerOptions are the inputs only a steer takes beyond the instruction.
type SteerOptions struct {
	// Root is the tree the session stands in, for a fix run: the linked
	// worktree the first session worked in, which internal/fix has already
	// checked is still there. Empty means the workspace root, which is what
	// a fix under fix.inPlace used. It is ignored for every other kind: a
	// triage or rca run finds its own --at worktree.
	Root string

	// Model overrides the model the continued session asks for, and every
	// session of this run after it. It is what `sirdar steer RUN "…"
	// --model NAME` carries, and what the session screen's model picker
	// sends when a reader changes it on a finished run. Empty leaves the
	// run on the model it has, which is every steer that does not name
	// one.
	Model string
}

// ErrSteerLive says the run is still going. It is the one refusal the HTTP
// layer answers with a conflict rather than a failure, because the caller
// can simply wait.
var ErrSteerLive = errors.New("the run is still going")

// Steer continues a finished run with a follow-up instruction. The same
// run goes back to running, its transcript grows in place, its usage and
// wall-clock time keep counting against the same caps, and its note is
// rendered again when the answer changes.
//
// Every refusal happens before the run's state is touched: a live run, an
// eval, a run over any budget, a provider that cannot continue (see
// provider.PlanSteer), a run made under a different provider than the one
// in use, or a run whose worktree is gone.
func (r *Runner) Steer(ctx context.Context, runID, text string, o SteerOptions) (Outcome, error) {
	if r.Config == nil {
		return Outcome{}, fmt.Errorf("run: no workspace configuration")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Outcome{}, fmt.Errorf("run: the instruction is empty")
	}
	rn, state, err := store.Open(r.Config.Root, runID)
	if err != nil {
		return Outcome{}, err
	}
	if err := r.refuseSteer(state); err != nil {
		return Outcome{}, err
	}
	if r.Provider == nil {
		return Outcome{}, fmt.Errorf("run: no provider configured")
	}
	cont, err := provider.PlanSteer(r.Provider)
	if err != nil {
		return Outcome{}, err
	}
	// A provider that resumes by handle can only do so with one. A run
	// that recorded none — the provider never reported it, or the run
	// failed before it did — is continued in a primed session instead,
	// and the transcript says so.
	if cont == provider.ContinueResume && state.Handle == "" {
		cont = provider.ContinuePrimed
	}

	p := &prepared{run: rn, state: state, kind: state.Kind, root: o.Root, usageBase: state.Usage}
	switch state.Kind {
	case store.KindFix:
		// No bundle: a fix session reads the note internal/fix put in its
		// prompt and the tree it stands in.
		if p.root != "" && p.root != r.Config.Root {
			p.state.Fix.Worktree = p.root
		}
	default:
		bundle, err := readBundle(rn.BundleDir())
		if err != nil {
			return Outcome{}, err
		}
		p.bundle = bundle
		// A run that stood at a commit has to be continued there. The
		// worktree is the operator's to keep or remove; a steer never
		// takes away one it found, and one that is gone is a refusal
		// rather than a session quietly run against today's tree.
		if state.At != "" {
			path := worktree.Path(r.Config.Root, state.RunID)
			if !worktree.IsWorktree(path) {
				return Outcome{}, fmt.Errorf("run: %s stood at %s in %s, and that worktree is gone; re-run with --at %s --keep-worktree to steer it",
					runID, short(state.At), worktree.RelToRoot(r.Config.Root, path), short(state.At))
			}
			p.root, p.ownWorktree, p.keepWorktree = path, true, true
		}
		if state.Kind == store.KindRCA {
			notePath, err := store.LatestNote(r.Config.Root, state.Key, store.KindTriage)
			if err != nil {
				return Outcome{}, fmt.Errorf("no triage note for %s; run triage first", state.Key)
			}
			p.triageNotePath = notePath
			p.triageNoteCopy, p.triageLink = triageNoteCopy(r.Config.Root, notePath)
		}
	}

	previous, _ := os.ReadFile(filepath.Join(rn.Dir, "result.json"))
	p.previousFinal = previous

	handle := ""
	switch cont {
	case provider.ContinueResume:
		handle = state.Handle
		p.promptText = steerPrompt(text, state.Kind)
	default:
		original, err := os.ReadFile(filepath.Join(rn.Dir, "prompt.md"))
		if err != nil {
			return Outcome{}, fmt.Errorf("run: read the run's prompt: %w", err)
		}
		earlier := previous
		if len(earlier) == 0 {
			earlier, _ = os.ReadFile(filepath.Join(rn.Dir, "result.raw.txt"))
		}
		p.promptText = primedPrompt(string(original), string(earlier), text, state.Kind)
	}

	applyModel(p, o.Model, "steer --model", r.now())

	s := store.Steer{At: r.now(), Text: text, Continuation: string(cont)}
	p.steer = &s
	p.state.Steers = append(p.state.Steers, s)

	return r.execute(ctx, p, handle, nil), nil
}

// refuseSteer is every run-level reason a steer cannot start. It reads the
// state alone, so a caller that has no provider yet — the HTTP layer,
// answering before its job builds one — can ask it too through
// RefuseSteer.
func (r *Runner) refuseSteer(state store.State) error {
	if err := RefuseSteer(r.Config.Budget.MaxTurns, r.Config.Budget.MaxMinutes, r.Config.Budget.MaxUSD, state); err != nil {
		return err
	}
	if name := r.providerName(); name != "" && state.Provider != "" && state.Provider != name {
		return fmt.Errorf("run: %s was made under provider %s and the workspace now uses %s; the session handle would name a session %s never held, so steer it under provider %s",
			state.RunID, state.Provider, name, name, state.Provider)
	}
	return nil
}

// RefuseSteer says why a run cannot be steered, from its state and the
// workspace's caps alone, or nil when nothing in the state refuses it. A
// live run wraps ErrSteerLive.
func RefuseSteer(maxTurns, maxMinutes int, maxUSD float64, state store.State) error {
	switch state.Status {
	case store.StatusPreparing, store.StatusRunning:
		return fmt.Errorf("run: %s is %s: %w; wait for it, or cancel it and resume", state.RunID, state.Status, ErrSteerLive)
	case store.StatusOverBudget:
		return fmt.Errorf("run: %s ran over budget (%s); a steer counts against the same caps and cannot continue it", state.RunID, state.Reason)
	}
	if state.Eval {
		return fmt.Errorf("run: %s is an eval replay; its note is a measurement and is not steered", state.RunID)
	}
	u := state.Usage
	switch {
	case maxTurns > 0 && u.Turns >= maxTurns:
		return fmt.Errorf("run: %s has used %d of its %d turns; a steer counts against the same cap", state.RunID, u.Turns, maxTurns)
	case maxUSD > 0 && u.CostUSD >= maxUSD:
		return fmt.Errorf("run: %s has spent $%.2f of its $%.2f budget; a steer counts against the same cap", state.RunID, u.CostUSD, maxUSD)
	case maxMinutes > 0 && u.ElapsedSeconds >= float64(maxMinutes*60):
		return fmt.Errorf("run: %s has used its %d minute wall-clock budget; a steer counts against the same cap", state.RunID, maxMinutes)
	}
	return nil
}

// answerNoun names the document a steered session has to end with.
func answerNoun(kind store.Kind) string {
	switch kind {
	case store.KindRCA:
		return "RCA and resolution document"
	case store.KindFix:
		return "fix report"
	default:
		return "triage note"
	}
}

// steerPrompt is the message a resumed session opens with: the operator's
// instruction, and the reminder that the reply is the run's document again,
// whole, in the same schema.
func steerPrompt(text string, kind store.Kind) string {
	return "Follow-up instruction from the operator:\n\n" + text + "\n\n" +
		"Carry it out. Then reply with the " + answerNoun(kind) + " as one JSON object matching the same " +
		"schema as before: the whole document, changed where the instruction changes it and kept as it " +
		"was everywhere else. If nothing in it changes, reply with the same document."
}

// primedPrompt is the opening message of a fresh session standing in for
// the one that wrote the note: the run's original prompt, the answer it
// produced, and the instruction. The bundle is still on disk where the
// original prompt points, so the new session has what the first one had.
func primedPrompt(original, earlier, text string, kind store.Kind) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(original, "\n"))
	b.WriteString("\n\n---\n\n## Earlier answer\n\n")
	if strings.TrimSpace(earlier) == "" {
		b.WriteString("A previous session worked on the task above but produced no valid answer. You are continuing its work in a new session.\n\n")
	} else {
		b.WriteString("A previous session answered the task above with this document. You are continuing its work in a new session, so read it as your own:\n\n```json\n")
		b.WriteString(strings.TrimRight(earlier, "\n"))
		b.WriteString("\n```\n\n")
	}
	b.WriteString(steerPrompt(text, kind))
	return b.String()
}

// short abbreviates a sha for a message.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
