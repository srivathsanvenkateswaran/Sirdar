package main

import (
	"fmt"
	"io"

	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
)

func init() { commands["eval"] = cmdEval }

func cmdEval(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("eval", stderr,
		"usage: sirdar eval [KEY...] [--golden DIR] [--provider claude|codex|openai] [--model NAME] [--concurrency N]")
	golden := fs.String("golden", "", "golden set directory (default ~/.sirdar/golden)")
	providerName := fs.String("provider", "", "override the configured provider")
	model := fs.String("model", "", "override the configured model")
	concurrency := fs.Int("concurrency", 0, "keys replayed at once (default from config)")
	keys, ok := parseFlags(fs, args, 0, -1, stderr)
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
