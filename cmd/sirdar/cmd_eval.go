package main

import (
	"context"
	"fmt"
	"io"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

func init() { commands["eval"] = cmdEval }

func cmdEval(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("eval", stderr,
		"usage: sirdar eval [KEY...] [--retro [--with-rca] [--rubric]] [--golden DIR] [--provider claude|codex|openai] [--model NAME] [--concurrency N]")
	golden := fs.String("golden", "", "golden set directory (default ~/.sirdar/golden)")
	providerName := fs.String("provider", "", "override the configured provider")
	model := fs.String("model", "", "override the configured model")
	concurrency := fs.Int("concurrency", 0, "keys replayed at once (default from config)")
	retro := fs.Bool("retro", false, "replay each key at the commit its fix branched from — triage, then a local fix — and score what came back against the merged pull request")
	withRCA := fs.Bool("with-rca", false, "add a blind RCA run to each retro key, a third session (--retro only)")
	rubric := fs.Bool("rubric", false, "ask the provider once per key whether the two diffs are the same change (--retro only)")
	keys, ok := parseFlags(fs, args, 0, -1, stderr)
	if !ok {
		return exitUsage
	}
	if !*retro && (*withRCA || *rubric) {
		fmt.Fprintln(stderr, "sirdar: --with-rca and --rubric apply to --retro")
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

	if *retro {
		return runRetro(ctx, cfg, deps, keys, stdout, stderr, eval.RetroOptions{
			GoldenDir:   *golden,
			Model:       *model,
			Concurrency: *concurrency,
			WithRCA:     *withRCA,
			Rubric:      *rubric,
		})
	}

	report, err := eval.Run(ctx, deps, keys, eval.Options{
		GoldenDir:   *golden,
		Model:       *model,
		Concurrency: *concurrency,
	})
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	fmt.Fprint(stdout, report.Table())
	path, err := report.Write(cfg.Root)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nreport: %s\n", path)
	return eval.ExitCode(report)
}

// runRetro replays each key as the ticket stood before its fix and prints
// the comparison against the change that was merged.
//
// It exits 0 whatever the table says. A retro is a measurement of how close
// the agent got to a change a human already made; there is no threshold it
// passes or fails, and an exit code would invite one to be invented. A
// failure that stops the command before any key runs — no golden set, no
// key carrying a retro.json — is still an error.
func runRetro(ctx context.Context, cfg *config.Config, deps runner.Deps, keys []string, stdout, stderr io.Writer, o eval.RetroOptions) int {
	rd := eval.NewRetroDeps(deps, o.Model)
	rd.Root = cfg.Root
	report, err := eval.RunRetro(ctx, rd, keys, o)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, report.Table())
	path, err := report.Write(cfg.Root)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nreport: %s\n", path)
	return 0
}
