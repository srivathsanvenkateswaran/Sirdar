package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/fix"
)

func init() { commands["fix"] = cmdFix }

func cmdFix(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("fix", stderr,
		"usage: sirdar fix KEY [--dry-run] [--no-pr] [--base BRANCH] [--accept-deviation] [--provider claude|codex|openai] [--model NAME]")
	dryRun := fs.Bool("dry-run", false, "create the branch and the prompt, start no agent and push nothing")
	noPR := fs.Bool("no-pr", false, "push the branch but do not open a pull request")
	base := fs.String("base", "", "branch to cut from and target (default: origin's default branch)")
	accept := fs.Bool("accept-deviation", false, "push even though the agent reported deviating from the note")
	providerName := fs.String("provider", "", "override the configured provider")
	model := fs.String("model", "", "override the configured model")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
		return exitUsage
	}
	key := positional[0]

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

	res, err := fix.Run(ctx, deps, key, fix.Options{
		Model:           *model,
		Base:            *base,
		DryRun:          *dryRun,
		NoPR:            *noPR,
		AcceptDeviation: *accept,
	})
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	return printFix(res, stdout)
}

// printFix reports what the run did, and returns the exit status: non-zero
// when the work is sitting on a local branch waiting for a human.
func printFix(res fix.Result, stdout io.Writer) int {
	fmt.Fprintf(stdout, "branch: %s (from origin/%s)\n", res.Branch, res.Base)
	if res.DryRun {
		fmt.Fprintf(stdout, "dry run: the prompt is in .sirdar/runs/%s/%s/prompt.md; no agent was started\n", res.Key, res.RunID)
		return 0
	}

	fmt.Fprintf(stdout, "commit: %s\n", res.Commit)
	for _, f := range res.Report.FilesChanged {
		fmt.Fprintf(stdout, "  %s\n", f)
	}
	for _, t := range res.Report.TestsRun {
		fmt.Fprintf(stdout, "  %s — %s\n", t.Command, t.Result)
	}

	if res.Blocked != "" {
		fmt.Fprintf(stdout, "\n%s\nThe agent did not implement the note's Proposed Fix as written:\n\n  %s\n\n%s\n",
			strings.Repeat("!", 60), res.Blocked, strings.Repeat("!", 60))
		fmt.Fprintf(stdout, "\nThe commit is on %s and has NOT been pushed. Read the diff, and if you accept it,\nrerun with --accept-deviation, or push the branch yourself.\n", res.Branch)
		return 1
	}

	if !res.Pushed {
		return 0
	}
	fmt.Fprintf(stdout, "pushed: %s\n", res.Branch)
	switch {
	case res.PRURL != "":
		fmt.Fprintf(stdout, "pull request: %s\n", res.PRURL)
	case res.CompareURL != "":
		fmt.Fprintf(stdout, "\nOpen the pull request here:\n  %s\n\nTitle:\n  %s\n\nBody:\n%s\n",
			res.CompareURL, res.PRTitle, res.PRBody)
	default:
		fmt.Fprintf(stdout, "\nOpen the pull request for %s yourself.\n\nTitle:\n  %s\n\nBody:\n%s\n",
			res.Branch, res.PRTitle, res.PRBody)
	}
	for _, path := range res.NotesUpdated {
		fmt.Fprintf(stdout, "updated: %s\n", path)
	}
	return 0
}
