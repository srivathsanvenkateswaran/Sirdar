package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

// goldenAddUsage covers both shapes of the command: the ordinary one, which
// copies a completed run's bundle, and the retrospective one, which builds
// the bundle out of a closed ticket as it stood before the fix.
const goldenAddUsage = "usage: sirdar golden add KEY [--from RUN_ID] [--golden DIR] [--force]\n" +
	"       sirdar golden add KEY --retro --pr URL [--pr URL...] [--as-of RFC3339] [--golden DIR] [--force]"

// repeatedFlag collects a flag the operator may pass more than once, which
// is how a fix spread over several pull requests is named.
type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ",") }

func (r *repeatedFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func init() { commands["golden"] = cmdGolden }

func cmdGolden(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, goldenAddUsage)
		fmt.Fprintln(stderr, "       sirdar golden list [--golden DIR]")
		fmt.Fprintln(stderr, "       sirdar golden migrate [KEY...] [--golden DIR]")
		return exitUsage
	}
	switch args[0] {
	case "add":
		return cmdGoldenAdd(args[1:], stdout, stderr)
	case "list":
		return cmdGoldenList(args[1:], stdout, stderr)
	case "migrate":
		return cmdGoldenMigrate(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "sirdar golden: unknown subcommand %q; use add, list, or migrate\n", args[0])
		return exitUsage
	}
}

func cmdGoldenAdd(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("golden add", stderr, goldenAddUsage)
	from := fs.String("from", "", "run id to copy the bundle from (default: the newest completed triage run)")
	golden := fs.String("golden", "", "golden set directory (default ~/.sirdar/golden)")
	force := fs.Bool("force", false, "write the bundle even though the golden set is inside a git work tree")
	retro := fs.Bool("retro", false, "build the bundle from the ticket as it stood before the fix, with the merged pull request as ground truth")
	asOf := fs.String("as-of", "", "RFC3339 cutoff for --retro (default: the earliest of the first in_progress transition and the first pull request's created_at)")
	var prs repeatedFlag
	fs.Var(&prs, "pr", "merged pull request URL for --retro; repeat for a fix spread over several")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
		return exitUsage
	}
	if *retro {
		return cmdGoldenAddRetro(positional[0], *golden, *asOf, prs, *force, stdout, stderr)
	}
	switch {
	case len(prs) > 0:
		fmt.Fprintln(stderr, "sirdar golden add: --pr is only read with --retro")
		return exitUsage
	case *asOf != "":
		fmt.Fprintln(stderr, "sirdar golden add: --as-of is only read with --retro")
		return exitUsage
	}

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	added, err := eval.Add(cfg.Root, *golden, positional[0], *from, *force)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "copied run %s bundle to %s\n", added.RunID, added.BundleDir)
	if added.SkeletonWritten {
		fmt.Fprintf(stdout, "wrote %s — trim it to the assertions you are prepared to stand behind\n", added.ExpectedPath)
	} else {
		fmt.Fprintf(stdout, "%s already exists and was left alone\n", added.ExpectedPath)
	}
	return 0
}

// cmdGoldenAddRetro builds a golden entry out of a closed ticket: the
// bundle as it stood when an engineer picked it up, and the merged pull
// request beside it as the answer to score against.
//
// It reads the ticket through the workspace's own configured adapters —
// never an MCP server, never a provider session — and it starts no
// provider at all, so a workspace whose agent CLI is not installed here can
// still build one.
func cmdGoldenAddRetro(key, golden, asOfText string, prs repeatedFlag, force bool, stdout, stderr io.Writer) int {
	if len(prs) == 0 {
		fmt.Fprintln(stderr, "sirdar golden add: --retro needs at least one --pr URL")
		fmt.Fprintln(stderr, goldenAddUsage)
		return exitUsage
	}
	var asOf time.Time
	if asOfText != "" {
		parsed, err := time.Parse(time.RFC3339, asOfText)
		if err != nil {
			fmt.Fprintf(stderr, "sirdar golden add: --as-of %q is not an RFC3339 timestamp (e.g. 2026-03-02T10:00:00Z)\n", asOfText)
			return exitUsage
		}
		asOf = parsed
	}

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	tracker, helpdesk, cleanup, err := app.BuildSources(cfg, stderr)
	defer cleanup()
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	if tracker == nil && helpdesk == nil {
		fmt.Fprintln(stderr, "sirdar: no ticket source configured; set sources.tracker or sources.helpdesk")
		return 1
	}

	ctx, stop := interruptible()
	defer stop()

	fetcher := &runner.Fetcher{Config: cfg, Tracker: tracker, Helpdesk: helpdesk, Stderr: stderr}
	added, err := eval.AddRetro(ctx, fetcher, key, eval.RetroOptions{
		GoldenDir: golden,
		PRURLs:    prs,
		AsOf:      asOf,
		Force:     force,
	})
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	for _, w := range added.Warnings {
		fmt.Fprintf(stderr, "sirdar: %s\n", w)
	}
	fmt.Fprintf(stdout, "wrote %s as of %s (%s)\n",
		added.BundleDir, added.Retro.AsOf.Format(time.RFC3339), added.AsOfSource)
	fmt.Fprintf(stdout, "wrote %s: base %s, %d changed file(s)\n",
		added.RetroPath, shortCommit(added.Retro.BaseCommit), len(added.Retro.PRFiles))
	fmt.Fprintf(stdout, "wrote %s\n", added.DiffPath)
	if added.SkeletonWritten {
		fmt.Fprintf(stdout, "wrote %s — add the assertions you are prepared to stand behind\n", added.ExpectedPath)
	} else {
		fmt.Fprintf(stdout, "%s already exists and was left alone\n", added.ExpectedPath)
	}
	return 0
}

// shortCommit renders a commit sha the way git log does.
func shortCommit(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func cmdGoldenMigrate(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("golden migrate", stderr, "usage: sirdar golden migrate [KEY...] [--golden DIR]")
	golden := fs.String("golden", "", "golden set directory (default ~/.sirdar/golden)")
	positional, ok := parseFlags(fs, args, 0, -1, stderr)
	if !ok {
		return exitUsage
	}

	root := eval.ExpandDir(*golden)
	keys := positional
	if len(keys) == 0 {
		found, err := eval.LegacyKeys(root)
		if err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n", err)
			return 1
		}
		if len(found) == 0 {
			fmt.Fprintf(stdout, "no golden entries under %s use the pre-eval layout\n", root)
			return 0
		}
		keys = found
	}

	status := 0
	for _, key := range keys {
		m, err := eval.Migrate(*golden, key)
		if err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n", err)
			status = 1
			continue
		}
		fmt.Fprintf(stdout, "%s: moved %s into %s\n", m.Key, strings.Join(m.Moved, ", "), m.BundleDir)
	}
	return status
}

func cmdGoldenList(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("golden list", stderr, "usage: sirdar golden list [--golden DIR]")
	golden := fs.String("golden", "", "golden set directory (default ~/.sirdar/golden)")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}
	root := eval.ExpandDir(*golden)
	keys, err := eval.Keys(root)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	for _, key := range keys {
		g, err := eval.Load(root, key)
		if err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n", err)
			continue
		}
		expected := "no expected.md"
		if g.ExpectedMD != "" {
			expected = "expected.md"
		}
		fmt.Fprintf(stdout, "%s\t%d assertions\t%s\n", key, len(g.Expected), expected)
	}
	return 0
}
