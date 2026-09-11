package main

import (
	"fmt"
	"io"

	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
)

func init() { commands["golden"] = cmdGolden }

func cmdGolden(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: sirdar golden add KEY [--from RUN_ID] [--golden DIR] [--force]")
		return exitUsage
	}
	switch args[0] {
	case "add":
		return cmdGoldenAdd(args[1:], stdout, stderr)
	case "list":
		return cmdGoldenList(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "sirdar golden: unknown subcommand %q; use add or list\n", args[0])
		return exitUsage
	}
}

func cmdGoldenAdd(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("golden add", stderr,
		"usage: sirdar golden add KEY [--from RUN_ID] [--golden DIR] [--force]")
	from := fs.String("from", "", "run id to copy the bundle from (default: the newest completed triage run)")
	golden := fs.String("golden", "", "golden set directory (default ~/.sirdar/golden)")
	force := fs.Bool("force", false, "write the bundle even though the golden set is inside a git work tree")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
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
