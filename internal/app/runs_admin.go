package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// KindRunRemoved reports a run directory that DeleteRun took off disk, so a
// list that held the run drops it without waiting to be reloaded. It
// carries WorkspaceID and RunID and nothing else: there is no summary left
// to send.
const KindRunRemoved = "run.removed"

// ErrRunLive is a run that cannot be deleted because its runner is still
// writing into the directory. The HTTP layer answers 409: cancel the job,
// or wait, and ask again.
var ErrRunLive = errors.New("app: run is live")

// SearchLimit caps what Search answers with. Fifty hits is more than a
// sidebar lists; past that the query is too broad to be useful.
const SearchLimit = 50

// excerptLen is how much of a matching line the hit carries, in runes,
// centred on the match.
const excerptLen = 160

// SearchHit is one place a query was found: a run, the file it was found
// in, and a line's worth of text around the first match. Source says which
// kind of file — the agent's structured answer ("answer") or a note
// ("note") — so a screen can mark the two apart.
type SearchHit struct {
	RunID   string `json:"runId"`
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Source  string `json:"source"`
	Path    string `json:"path"`
	Excerpt string `json:"excerpt"`
}

// DeleteRun removes one run's directory under .sirdar/runs. The register
// row the run wrote and any note it filed into the notes directory are left
// alone: they are the record; the run directory is the working copy. A run
// whose status says its runner is still writing is refused with ErrRunLive,
// since removing the directory under a session would only make the runner
// fail in a stranger way. A blocked run has no session behind it and can go.
func (s *Service) DeleteRun(wsID, runID string) error {
	rn, state, err := s.openRun(wsID, runID)
	if err != nil {
		return err
	}
	if state.Status == store.StatusPreparing || state.Status == store.StatusRunning {
		return fmt.Errorf("%w: %s is %s", ErrRunLive, runID, state.Status)
	}
	if err := os.RemoveAll(rn.Dir); err != nil {
		return fmt.Errorf("app: delete run %s: %w", runID, err)
	}
	s.publish(Event{Kind: KindRunRemoved, WorkspaceID: wsID, RunID: runID})
	return nil
}

// Search looks for q, case-insensitively, in every run's answer JSON
// (result.json), its own note.md, and each note the run recorded filing.
// One hit per file, newest run first, capped at SearchLimit. An empty
// query answers with nothing rather than everything.
func (s *Service) Search(wsID, q string) ([]SearchHit, error) {
	root, err := s.root(wsID)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(q))
	if needle == "" {
		return []SearchHit{}, nil
	}
	states, err := store.List(root, "")
	if err != nil {
		return nil, err
	}
	hits := []SearchHit{}
	for _, st := range states {
		dir := runDir(root, st.Key, st.RunID)
		for _, f := range searchFiles(dir, st.Notes) {
			data, err := os.ReadFile(f.path)
			if err != nil {
				continue
			}
			excerpt, ok := excerptOf(string(data), needle)
			if !ok {
				continue
			}
			hits = append(hits, SearchHit{
				RunID: st.RunID, Key: st.Key, Kind: string(st.Kind), Status: string(st.Status),
				Source: f.source, Path: f.path, Excerpt: excerpt,
			})
			if len(hits) >= SearchLimit {
				return hits, nil
			}
		}
	}
	return hits, nil
}

type searchFile struct{ path, source string }

// searchFiles is the files Search reads for one run, deduplicated: the
// answer, the run's own note, then every filed note the state recorded.
func searchFiles(dir string, notes []string) []searchFile {
	out := []searchFile{
		{filepath.Join(dir, "result.json"), "answer"},
		{filepath.Join(dir, "note.md"), "note"},
	}
	seen := map[string]bool{out[0].path: true, out[1].path: true}
	for _, n := range notes {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, searchFile{n, "note"})
	}
	return out
}

// excerptOf finds needle in text, case-folded, and answers with excerptLen
// runes around the first match, whitespace collapsed, with an ellipsis at
// each edge that was cut. The second value is false when there is no match.
func excerptOf(text, needle string) (string, bool) {
	lower := strings.ToLower(text)
	at := strings.Index(lower, needle)
	if at < 0 {
		return "", false
	}
	// Work in runes so a cut never lands inside a multibyte character; the
	// byte offset is mapped to a rune offset first.
	runes := []rune(text)
	runeAt := len([]rune(text[:at]))
	needleLen := len([]rune(needle))
	start := runeAt - (excerptLen-needleLen)/2
	if start < 0 {
		start = 0
	}
	end := start + excerptLen
	if end > len(runes) {
		end = len(runes)
		start = end - excerptLen
		if start < 0 {
			start = 0
		}
	}
	excerpt := collapseSpace(string(runes[start:end]))
	if start > 0 {
		excerpt = "…" + excerpt
	}
	if end < len(runes) {
		excerpt += "…"
	}
	return excerpt, true
}

// collapseSpace folds every run of whitespace, newlines included, into one
// space, so an excerpt reads as one line.
func collapseSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}
