package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

func init() { commands["ask"] = cmdAsk }

// cmdAsk starts a session: an instruction, with or without a ticket
// reference. The reply comes back in chat rather than as a filed note, so
// the command prints the reply itself, not a note path.
func cmdAsk(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("ask", stderr, `usage: sirdar ask "instruction" [REFERENCE] [--provider NAME] [--model NAME]`)
	providerName := fs.String("provider", "", "override the configured provider")
	model := fs.String("model", "", "override the configured model")
	positional, ok := parseFlags(fs, args, 1, 2, stderr)
	if !ok {
		return exitUsage
	}
	instruction := positional[0]

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	deps, cleanup, err := buildDeps(cfg, *providerName, *model, stdout, stderr)
	defer cleanup()
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	ctx, stop := interruptible()
	defer stop()

	var key, slackMD string
	var reported *runner.ReportedBundle
	noBundle := true
	if len(positional) == 2 {
		resolved, ok := resolveArgs(ctx, cfg, deps, []string{positional[1]}, stderr)
		if !ok {
			return 1
		}
		key = resolved.keys[0]
		slackMD = resolved.slack
		reported = resolved.reported
		noBundle = false
	}

	r := &runner.Runner{Deps: deps}
	out, err := r.Session(ctx, key, runner.Options{
		NoBundle:    noBundle,
		Instruction: instruction,
		Model:       *model,
		Slack:       slackMD,
		Reported:    reported,
	})
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	out = applyHeld(ctx, deps, []runner.Outcome{out})[0]

	if rn, _, err := store.Open(cfg.Root, out.State.RunID); err == nil {
		if reply, err := os.ReadFile(filepath.Join(rn.Dir, "answer.md")); err == nil {
			fmt.Fprint(stdout, string(reply))
		}
	}
	fmt.Fprintf(stderr, "[%s] run %s\n", out.State.Key, out.State.RunID)
	return runner.ExitCode([]runner.Outcome{out})
}
