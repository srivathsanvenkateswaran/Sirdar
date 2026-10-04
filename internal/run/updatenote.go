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

// UpdateNoteOptions are the inputs one Update note takes beyond the run id.
type UpdateNoteOptions struct{ Model string }

// ErrNoNote says the run cannot file a note; the app layer answers it with a conflict.
var ErrNoNote = errors.New("the run cannot file a note")

// noNote is a refusal that answers to ErrNoNote and keeps its own message and
// cause: a live run is still ErrSteerLive underneath, for a caller that asks.
type noNote struct{ err error }

func (e noNote) Error() string        { return e.err.Error() }
func (e noNote) Unwrap() error        { return e.err }
func (e noNote) Is(target error) bool { return target == ErrNoNote }

// filingAgain is the transcript line an Update note opens with, so a reader can
// tell the note turn it starts from the one the run filed first.
const filingAgain = "Filing the note again"

// UpdateNote runs one note turn on a finished triage or RCA run that has a reply, and files the
// note and its register row again, replacing the run's note file.
func (r *Runner) UpdateNote(ctx context.Context, runID string, o UpdateNoteOptions) (Outcome, error) {
	if r.Config == nil {
		return Outcome{}, fmt.Errorf("run: no workspace configuration")
	}
	rn, state, err := store.Open(r.Config.Root, runID)
	if err != nil {
		return Outcome{}, err
	}
	if err := refuseNote(rn, state); err != nil {
		return Outcome{}, err
	}
	if err := r.refuseSteer(state); err != nil {
		return Outcome{}, noNote{err}
	}
	if r.Provider == nil {
		return Outcome{}, fmt.Errorf("run: no provider configured")
	}
	// A provider that cannot continue a run at all (cursor, agy) is still
	// primed here, as the note turn after a reply is: the note turn runs
	// under the note policy and can call nothing a follow-up could.
	cont, _ := provider.PlanSteer(r.Provider)

	p := &prepared{run: rn, state: state, kind: state.Kind, usageBase: state.Usage}
	bundle, err := readBundleIfAny(rn.BundleDir())
	if err != nil {
		return Outcome{}, err
	}
	p.bundle = bundle
	// A run that stood at a commit is continued where it stood while that
	// worktree is still there, because a provider finds the session to
	// resume by the directory it ran in. One that is gone is no refusal
	// here: the note turn reads the run directory and nothing else.
	if state.At != "" {
		if path := worktree.Path(r.Config.Root, state.RunID); worktree.IsWorktree(path) {
			p.root, p.ownWorktree, p.keepWorktree = path, true, true
		}
	}
	if state.Kind == store.KindRCA {
		notePath, err := store.LatestNote(r.Config.Root, state.Key, store.KindTriage)
		if err != nil {
			return Outcome{}, fmt.Errorf("no triage note for %s; run triage first", state.Key)
		}
		p.triageNotePath = notePath
		p.triageNoteCopy, p.triageLink = triageNoteCopy(r.Config.Root, notePath)
	}

	handle := ""
	if cont == provider.ContinueResume {
		handle = state.Handle
	}
	notePrompt := r.notePrompt(p)
	if handle != "" {
		p.promptText = notePrompt
	} else {
		original, err := os.ReadFile(filepath.Join(rn.Dir, "prompt.md"))
		if err != nil {
			return Outcome{}, fmt.Errorf("run: read the run's prompt: %w", err)
		}
		reply, err := os.ReadFile(filepath.Join(rn.Dir, answerFile))
		if err != nil {
			return Outcome{}, fmt.Errorf("run: read the run's reply: %w", err)
		}
		p.promptText = primedNotePrompt(string(original), strings.TrimRight(string(reply), "\n"), notePrompt)
	}

	// The note is filed afresh, so the run names only the paths this turn
	// writes, and a warning about the last attempt no longer applies.
	p.state.Phase = store.PhaseNote
	p.state.NoteWarning = ""
	p.state.Notes = nil
	applyModel(p, o.Model, "note --model", r.now())

	log, err := r.openEventLog(rn)
	if err != nil {
		return Outcome{}, err
	}
	r.recordSystem(p, log, filingAgain)
	log.Close()

	return r.execute(ctx, p, handle, nil), nil
}

// refuseNote is why a run has no note to file again, from its kind and its reply alone.
func refuseNote(rn store.Run, state store.State) error {
	if state.Kind != store.KindTriage && state.Kind != store.KindRCA {
		return noNote{fmt.Errorf("run: %s is a %s run; only triage and RCA runs file a note", state.RunID, state.Kind)}
	}
	if _, err := os.Stat(filepath.Join(rn.Dir, answerFile)); !state.ReplyFirst || err != nil {
		return noNote{fmt.Errorf("run: %s has no reply to file a note from", state.RunID)}
	}
	return nil
}

// RefuseNote is refuseNote for the app layer, which refuses before it starts a job: the run's
// kind and reply, then whether it is still live.
func RefuseNote(rn store.Run, state store.State) error {
	if err := refuseNote(rn, state); err != nil {
		return err
	}
	if state.Status == store.StatusPreparing || state.Status == store.StatusRunning {
		return noNote{fmt.Errorf("run: %s is %s: %w; wait for it", state.RunID, state.Status, ErrSteerLive)}
	}
	return nil
}
