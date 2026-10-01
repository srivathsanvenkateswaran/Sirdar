package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

func init() { commands["resume"] = cmdResume }

func cmdResume(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("resume", stderr,
		"usage: sirdar resume RUN_ID [--allow | --allow-run | --deny [--reason TEXT]] [--model NAME]")
	model := fs.String("model", "", "continue on this model instead of the run's; the way past a per-model limit")
	allow := fs.Bool("allow", false, "answer a permission question: allow the call it asked about, once")
	allowRun := fs.Bool("allow-run", false, "answer a permission question: allow the call, and any later one matching the same pattern, for the rest of this run")
	deny := fs.Bool("deny", false, "answer a permission question: refuse the call, and any later one matching the same pattern, for the rest of this run")
	reason := fs.String("reason", "", "with --deny (or either allow), a reason the agent is shown")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
		return exitUsage
	}
	runID := positional[0]

	decision, msg := decisionFlags(*allow, *allowRun, *deny, *reason)
	if msg != "" {
		fmt.Fprintf(stderr, "sirdar resume: %s\n", msg)
		return exitUsage
	}

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
	out, err := r.Resume(ctx, runID, runner.ResumeOptions{Model: *model, Decision: decision})
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

// decisionFlags reads --allow, --allow-run, --deny and --reason into a
// decision, or says what is wrong with them. None at all is a resume that
// is not answering a permission question here — the terminal asks, if the
// run has one.
func decisionFlags(allow, allowRun, deny bool, reason string) (*runner.Decision, string) {
	var verdicts []string
	if allow {
		verdicts = append(verdicts, provider.VerdictAllow)
	}
	if allowRun {
		verdicts = append(verdicts, provider.VerdictAllowRun)
	}
	if deny {
		verdicts = append(verdicts, provider.VerdictDeny)
	}
	switch {
	case len(verdicts) > 1:
		return nil, "--allow, --allow-run and --deny are one answer; give one of them"
	case len(verdicts) == 0 && strings.TrimSpace(reason) != "":
		return nil, "--reason goes with --allow, --allow-run or --deny"
	case len(verdicts) == 0:
		return nil, ""
	}
	return &runner.Decision{Verdict: verdicts[0], Reason: strings.TrimSpace(reason)}, ""
}
