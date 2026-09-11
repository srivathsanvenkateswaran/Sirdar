package main

import (
	"fmt"
	"io"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

func init() { commands["triage"] = cmdTriage }

func cmdTriage(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("triage", stderr,
		"usage: sirdar triage KEY [KEY...] [--provider claude|codex|openai|acp|qwen] [--model NAME] [--concurrency N] [--dry-run] [--no-notify]")
	providerName := fs.String("provider", "", "override the configured provider")
	model := fs.String("model", "", "override the configured model")
	concurrency := fs.Int("concurrency", 0, "parallel runs across keys (default from config)")
	dryRun := fs.Bool("dry-run", false, "write the bundle and prompt, do not start the agent")
	noNotify := fs.Bool("no-notify", false, "do not post the configured run-completion notifications")
	keys, ok := parseFlags(fs, args, 1, -1, stderr)
	if !ok {
		return exitUsage
	}

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

	r := &runner.Runner{Deps: deps}
	outs, err := r.Triage(ctx, keys, runner.Options{
		Model:       *model,
		Concurrency: *concurrency,
		DryRun:      *dryRun,
		NoNotify:    *noNotify,
	})
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	rows := make([]note.DigestRow, len(outs))
	for i, o := range outs {
		rows[i] = o.Digest
	}
	fmt.Fprint(stdout, note.Digest(rows))
	return runner.ExitCode(outs)
}
