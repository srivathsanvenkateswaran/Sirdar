package main

import (
	"fmt"
	"io"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

func init() { commands["note"] = cmdNote }

// cmdNote files the note again for a triage or RCA run that has already
// replied: one more note turn, replacing the run's note file and register
// row.
func cmdNote(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("note", stderr, "usage: sirdar note RUN_ID [--model NAME]")
	model := fs.String("model", "", "continue on this model instead of the run's")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
		return exitUsage
	}
	runID := positional[0]

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	deps, cleanup, err := buildDeps(cfg, "", "", stdout, stderr)
	defer cleanup()
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	ctx, stop := interruptible()
	defer stop()

	r := &runner.Runner{Deps: deps}
	out, err := r.UpdateNote(ctx, runID, runner.UpdateNoteOptions{Model: *model})
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	out = applyHeld(ctx, deps, []runner.Outcome{out})[0]
	for _, path := range out.State.Notes {
		fmt.Fprintln(stdout, path)
	}
	fmt.Fprint(stdout, note.Digest([]note.DigestRow{out.Digest}))
	if out.State.NoteWarning != "" {
		fmt.Fprintf(stderr, "sirdar: %s\n", out.State.NoteWarning)
		return 1
	}
	return runner.ExitCode([]runner.Outcome{out})
}
