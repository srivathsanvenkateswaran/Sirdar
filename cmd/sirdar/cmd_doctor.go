package main

import (
	"context"
	"fmt"
	"io"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

func init() { commands["doctor"] = cmdDoctor }

// cmdDoctor checks everything a run depends on and prints one line per
// check, marked [OK], [!!] for a warning, or [XX] for a failure. It exits 1
// only on a failure, so a CI gate is not tripped by an advisory row — "the
// agent will see no MCP servers" is worth reading and is not a broken
// workspace. The checks themselves live in internal/app, so the desktop
// Settings screen reports the same list.
func cmdDoctor(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("doctor", stderr, "usage: sirdar doctor")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}

	return printChecks(stdout, app.RunDoctor(context.Background(), cfg))
}

// printChecks writes the report and returns the exit code: 1 when a check
// failed, 0 when the worst of them only warned.
func printChecks(stdout io.Writer, checks []app.Check) int {
	failed, warned := 0, 0
	for _, c := range checks {
		mark := "[OK]"
		// A check that named no level is read off the bool, so a caller
		// that filled only OK still prints correctly.
		level := provider.Level(c.Level)
		if level == "" {
			level = provider.LevelFail
			if c.OK {
				level = provider.LevelOK
			}
		}
		switch level {
		case provider.LevelWarn:
			mark = "[!!]"
			warned++
		case provider.LevelFail:
			mark = "[XX]"
			failed++
		}
		if c.Detail == "" {
			fmt.Fprintf(stdout, "%s %s\n", mark, c.Name)
			continue
		}
		fmt.Fprintf(stdout, "%s %s — %s\n", mark, c.Name, c.Detail)
	}
	// One summary line, not up to two: a report with both a failure and a
	// warning used to print "N warned" followed by "M failed" on the next
	// line, and a reader skimming for the last line saw only the failure
	// count, missing that something also warned.
	switch {
	case failed > 0 && warned > 0:
		fmt.Fprintf(stdout, "\n%d of %d checks failed, %d warned\n", failed, len(checks), warned)
	case failed > 0:
		fmt.Fprintf(stdout, "\n%d of %d checks failed\n", failed, len(checks))
	case warned > 0:
		fmt.Fprintf(stdout, "\n%d of %d checks warned\n", warned, len(checks))
	}
	if failed > 0 {
		return 1
	}
	return 0
}
