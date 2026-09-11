package main

import (
	"context"
	"fmt"
	"io"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

func init() { commands["doctor"] = cmdDoctor }

// cmdDoctor checks everything a run depends on and prints one line per
// check. It exits 1 when any check failed, so it can gate a CI job. The
// checks themselves live in internal/app, so the desktop Settings screen
// reports the same list.
func cmdDoctor(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("doctor", stderr, "usage: sirdar doctor")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}

	checks := app.RunDoctor(context.Background(), cfg)

	failed := 0
	for _, c := range checks {
		mark := "[OK]"
		if !c.OK {
			mark = "[!!]"
			failed++
		}
		if c.Detail == "" {
			fmt.Fprintf(stdout, "%s %s\n", mark, c.Name)
			continue
		}
		fmt.Fprintf(stdout, "%s %s — %s\n", mark, c.Name, c.Detail)
	}
	if failed > 0 {
		fmt.Fprintf(stdout, "\n%d of %d checks failed\n", failed, len(checks))
		return 1
	}
	return 0
}
