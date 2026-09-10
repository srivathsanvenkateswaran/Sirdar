package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

func init() { commands["register"] = cmdRegister }

// registerEntry is everything the register knows about one ticket, gathered
// from the (up to three) lines its runs appended.
type registerEntry struct {
	key                           string
	triageDate, confidence, class string
	rcaDate, verdict, severity    string
	resolutionType                string
	triagePath, rcaPath, resPath  string
}

func cmdRegister(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("register", stderr, "usage: sirdar register [--markdown]")
	markdown := fs.Bool("markdown", false, "print the rows in the vault's issue-register table shape")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	entries := groupRegister(rows)

	if *markdown {
		printRegisterMarkdown(stdout, entries)
		return 0
	}
	return printRegisterTable(stdout, stderr, entries)
}

// groupRegister folds the register's per-note lines into one entry per key,
// in the order the keys first appear. A later line for the same kind wins,
// so a re-run replaces what it supersedes.
func groupRegister(rows []store.RegisterRow) []registerEntry {
	index := map[string]int{}
	var entries []registerEntry

	for _, row := range rows {
		i, ok := index[row.Key]
		if !ok {
			i = len(entries)
			index[row.Key] = i
			entries = append(entries, registerEntry{key: row.Key})
		}
		e := &entries[i]
		switch note.Kind(row.Kind) {
		case note.Triage:
			e.triageDate, e.confidence, e.class, e.triagePath = row.Date, row.Confidence, row.Classification, row.NotePath
		case note.RCA:
			e.rcaDate, e.verdict, e.severity, e.rcaPath = row.Date, row.TriageVerdict, row.Severity, row.NotePath
			if row.Classification != "" {
				e.class = row.Classification
			}
			if row.Confidence != "" {
				e.confidence = row.Confidence
			}
		case note.Resolution:
			e.resolutionType, e.resPath = row.Classification, row.NotePath
		}
	}
	return entries
}

func printRegisterTable(stdout, stderr io.Writer, entries []registerEntry) int {
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tTRIAGE\tCONF\tCLASS\tRCA\tVERDICT\tSEV\tRESOLUTION\tNOTES")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			e.key, e.triageDate, e.confidence, e.class,
			e.rcaDate, e.verdict, e.severity, e.resolutionType, e.notesPresent())
	}
	return flush(w, stderr)
}

// notesPresent shows which of the three notes are on disk, as "TRS" with a
// dash for each one that is missing.
func (e registerEntry) notesPresent() string {
	marks := []struct {
		letter rune
		path   string
	}{{'T', e.triagePath}, {'R', e.rcaPath}, {'S', e.resPath}}

	var b strings.Builder
	for _, m := range marks {
		if m.path != "" && fileExists(m.path) {
			b.WriteRune(m.letter)
			continue
		}
		b.WriteByte('-')
	}
	return b.String()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// printRegisterMarkdown prints the vault's `_Issue Register` table shape for
// pasting. The register records the notes and the run, not the customer or
// the ticket URLs, so Company, Helpdesk and Tracker are left as empty cells
// for the human who pastes the rows to fill in.
func printRegisterMarkdown(stdout io.Writer, entries []registerEntry) {
	fmt.Fprintln(stdout, "| # | Issue | Company | Helpdesk | Tracker | Triage | RCA | Resolution | Status |")
	fmt.Fprintln(stdout, "|---|---|---|---|---|---|---|---|---|")
	for i, e := range entries {
		fmt.Fprintf(stdout, "| %d | %s |  |  |  | %s | %s | %s | %s |\n",
			i+1, cell(e.key), wikiLink(e.triagePath), wikiLink(e.rcaPath), wikiLink(e.resPath), e.status())
	}
}

// status is where the ticket has got to: resolved once the rca run has
// filed its notes, triaged before that.
func (e registerEntry) status() string {
	if e.resPath != "" || e.rcaPath != "" {
		return "resolved"
	}
	return "triaged"
}

// wikiLink turns a note path into the `[[stem]]` link the vault uses, or an
// empty cell when that note was never written.
func wikiLink(path string) string {
	if path == "" {
		return ""
	}
	return "[[" + cell(strings.TrimSuffix(filepath.Base(path), ".md")) + "]]"
}

// cell escapes the one character that would break a markdown table row.
func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
