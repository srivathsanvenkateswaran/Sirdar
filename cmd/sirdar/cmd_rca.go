package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

func init() { commands["rca"] = cmdRCA }

func cmdRCA(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("rca", stderr,
		"usage: sirdar rca KEY [--pr URL] [--resolution TEXT|@FILE] [--provider claude|codex|openai] [--model NAME]")
	prURL := fs.String("pr", "", "merged pull request; its diff is read with gh when available")
	resolution := fs.String("resolution", "", "what was done, as text or @path to a file")
	providerName := fs.String("provider", "", "override the configured provider")
	model := fs.String("model", "", "override the configured model")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
		return exitUsage
	}
	key := positional[0]

	text, err := resolutionText(*resolution)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
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
	out, err := r.RCA(ctx, key, runner.RCAOptions{
		Options:    runner.Options{Model: *model},
		PRURL:      *prURL,
		Resolution: text,
	})
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	for _, path := range out.State.Notes {
		fmt.Fprintln(stdout, path)
	}
	return runner.ExitCode([]runner.Outcome{out})
}

// resolutionText reads the --resolution value: "@path" means the file at
// that path, anything else is the text itself.
func resolutionText(value string) (string, error) {
	if !strings.HasPrefix(value, "@") {
		return value, nil
	}
	body, err := os.ReadFile(value[1:])
	if err != nil {
		return "", fmt.Errorf("--resolution: %w", err)
	}
	return strings.TrimRight(string(body), "\n"), nil
}
