package main

import (
	"fmt"
	"io"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

func init() { commands["steer"] = cmdSteer }

// cmdSteer continues a finished run with a follow-up instruction: the same
// run goes back to running, its transcript grows, and its note is rendered
// again when the answer changes.
func cmdSteer(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("steer", stderr, "usage: sirdar steer RUN_ID \"instruction\"")
	positional, ok := parseFlags(fs, args, 2, 2, stderr)
	if !ok {
		return exitUsage
	}
	runID, text := positional[0], positional[1]

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

	out, err := app.Steer(ctx, deps, runID, text)
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
