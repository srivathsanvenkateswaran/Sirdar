package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

func init() { commands["runs"] = cmdRuns }

// runRow is one line of `sirdar runs`, and the element type of its --json
// output: a stable, documented shape rather than the internal state file.
type runRow struct {
	RunID   string `json:"runId"`
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	State   string `json:"state"`
	Updated string `json:"updated"`
	Reason  string `json:"reason,omitempty"`
}

func cmdRuns(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("runs", stderr, "usage: sirdar runs [KEY] [--json]")
	asJSON := fs.Bool("json", false, "print the runs as a JSON array")
	positional, ok := parseFlags(fs, args, 0, 1, stderr)
	if !ok {
		return exitUsage
	}
	key := ""
	if len(positional) == 1 {
		key = positional[0]
	}

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	states, err := store.List(cfg.Root, key)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	rows := make([]runRow, 0, len(states))
	for _, s := range states {
		rows = append(rows, runRow{
			RunID:   s.RunID,
			Key:     s.Key,
			Kind:    string(s.Kind),
			State:   string(s.Status),
			Updated: formatTime(s.UpdatedAt),
			Reason:  s.Reason,
		})
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n", err)
			return 1
		}
		return 0
	}

	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUN_ID\tKEY\tKIND\tSTATE\tUPDATED\tREASON")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.RunID, r.Key, r.Kind, r.State, r.Updated, r.Reason)
	}
	return flush(w, stderr)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

func flush(w *tabwriter.Writer, stderr io.Writer) int {
	if err := w.Flush(); err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	return 0
}
