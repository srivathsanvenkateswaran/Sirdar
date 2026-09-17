package main

import (
	"fmt"
	"io"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

func init() { commands["resume"] = cmdResume }

func cmdResume(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("resume", stderr, "usage: sirdar resume RUN_ID [--model NAME]")
	model := fs.String("model", "", "continue on this model instead of the run's; the way past a per-model limit")
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
	out, err := r.Resume(ctx, runID, runner.ResumeOptions{Model: *model})
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	for _, path := range out.State.Notes {
		fmt.Fprintln(stdout, path)
	}
	fmt.Fprint(stdout, note.Digest([]note.DigestRow{out.Digest}))
	return runner.ExitCode([]runner.Outcome{out})
}
