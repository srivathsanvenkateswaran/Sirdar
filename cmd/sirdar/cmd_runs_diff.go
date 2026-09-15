package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/srivathsanvenkateswaran/sirdar/internal/fix"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

const runsDiffUsage = "usage: sirdar runs diff RUN_ID [--drop PATH:N]"

// cmdRunsDiff is `sirdar runs diff RUN_ID`: the change one fix run made,
// printed as a unified patch, and `--drop PATH:N` to revert one hunk of it
// out of the commit.
//
// It is the same reading the API serves and the same drop it does, through
// the same two calls into internal/fix — so the terminal and a desktop
// screen cannot come to different answers about what a fix run changed.
// Nothing here starts a session and nothing here pushes.
func cmdRunsDiff(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("runs diff", stderr, runsDiffUsage)
	drop := fs.String("drop", "", "revert one hunk out of the commit, as PATH:N with N the 0-based hunk index in that file")
	files := fs.Bool("files", false, "print the file list instead of the patch")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
		return exitUsage
	}
	runID := positional[0]

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	_, state, err := store.Open(cfg.Root, runID)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	ctx, stop := interruptible()
	defer stop()

	d, err := fix.ReviewDiff(ctx, cfg.Root, state)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	if *drop != "" {
		path, hunk, err := parseDrop(*drop)
		if err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n%s\n", err, runsDiffUsage)
			return exitUsage
		}
		// The etag is the one this command just read, so a drop from the
		// terminal is judged by the same staleness rule a drop from the
		// UI is: what is reverted is what was printed.
		d, err = fix.Drop(ctx, cfg.Root, state, path, hunk, d.ETag)
		if err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "dropped %s hunk %d; %s now points at %s\n", path, hunk, d.Branch, short(d.Head))
	}

	if *files {
		return printDiffFiles(stdout, stderr, d)
	}
	if d.Truncated {
		fmt.Fprintf(stderr, "sirdar: the patch is larger than the 2 MiB cap and is printed cut short; `git -C %s diff %s..%s` has all of it\n",
			fallbackDir(d), short(d.Base), short(d.Head))
	}
	_, err = io.WriteString(stdout, d.Patch)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	return 0
}

// printDiffFiles writes the file list `--files` asks for: the same rows the
// API's `files` array carries, one per line.
func printDiffFiles(stdout, stderr io.Writer, d fix.Diff) int {
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tADDED\tREMOVED\tPATH")
	for _, f := range d.Files {
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\n", f.Status, f.Additions, f.Deletions, f.Path)
	}
	return flush(w, stderr)
}

// parseDrop reads `--drop PATH:N`. The index is separated from the path at
// the last colon, so a path that contains one of its own still parses.
func parseDrop(spec string) (string, int, error) {
	i := strings.LastIndexByte(spec, ':')
	if i < 0 {
		return "", 0, fmt.Errorf("--drop wants PATH:N, with N the 0-based hunk index; got %q", spec)
	}
	path, raw := spec[:i], spec[i+1:]
	hunk, err := strconv.Atoi(raw)
	if err != nil || hunk < 0 || path == "" {
		return "", 0, fmt.Errorf("--drop wants PATH:N, with N the 0-based hunk index; got %q", spec)
	}
	return path, hunk, nil
}

// fallbackDir names the tree the operator can run git in themselves: the
// run's worktree while it is there, else the workspace they are standing in.
func fallbackDir(d fix.Diff) string {
	if d.WorktreePresent {
		return d.Worktree
	}
	return "."
}

// short abbreviates a sha for a message, leaving anything that is not one
// as it stands.
func short(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
