package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// suggestionsVersion is written on every suggestions line so a later
// format can tell old lines from new ones without guessing from the fields
// present.
const suggestionsVersion = 1

// maxSuggestionLine bounds one suggestions line. An RCA run and the
// operator's own Remember and Accept calls append to the same file without
// a lock, relying on a single small O_APPEND write landing whole, the same
// reliance the shadow ledger makes.
const maxSuggestionLine = 64 << 10

// SuggestionsPath is where a workspace's queue of pending playbook
// additions lives.
func SuggestionsPath(root string) string {
	return filepath.Join(root, ".sirdar", "playbook-suggestions.jsonl")
}

// SuggestionLine is one line of the suggestions queue. Ev is proposed,
// accepted or dismissed. A proposed line carries the whole addition; an
// accepted line names the playbook it went into and whether the operator
// changed it on the way; a dismissed line carries only its id.
type SuggestionLine struct {
	V        int       `json:"v"`
	At       time.Time `json:"at"`
	Ev       string    `json:"ev"` // proposed accepted dismissed
	ID       string    `json:"id"`
	Source   string    `json:"source,omitempty"` // remember rca
	RunID    string    `json:"runId,omitempty"`
	Key      string    `json:"key,omitempty"`
	Playbook string    `json:"playbook,omitempty"`
	Addition string    `json:"addition,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	CostUSD  float64   `json:"costUSD,omitempty"`
	Edited   bool      `json:"edited,omitempty"`
}

// SuggestionID names a proposal by what it says, so the same addition to
// the same playbook proposed twice is recognised as one suggestion.
func SuggestionID(playbook, addition string) string {
	sum := sha256.Sum256([]byte(playbook + addition))
	return "s-" + hex.EncodeToString(sum[:6])
}

// AppendSuggestion writes one line to the queue in a single Write on an
// O_APPEND file. A proposal gets its id filled in when it has none, and is
// not written at all when that id is already pending: appended is then
// false, and the error nil, because the operator's suggestion is already
// in the queue.
func AppendSuggestion(root string, l SuggestionLine) (appended bool, err error) {
	l.V = suggestionsVersion
	if l.At.IsZero() {
		l.At = time.Now()
	}
	if l.Ev == "proposed" {
		if l.ID == "" {
			l.ID = SuggestionID(l.Playbook, l.Addition)
		}
		lines, err := ReadSuggestions(root)
		if err != nil {
			return false, err
		}
		for _, p := range PendingSuggestions(lines) {
			if p.ID == l.ID {
				return false, nil
			}
		}
	}
	line, err := json.Marshal(l)
	if err != nil {
		return false, fmt.Errorf("store: encode suggestion: %w", err)
	}
	line = append(line, '\n')
	if len(line) >= maxSuggestionLine {
		return false, fmt.Errorf("store: suggestion line is %d bytes, over the 64 KiB limit", len(line))
	}
	path := SuggestionsPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("store: %w", err)
	}
	// A crash mid-write leaves a last line with no newline, and appending
	// straight after it would glue this line onto the fragment so that
	// neither parses. Closing the fragment off first loses only it.
	if torn, err := endsTorn(path); err != nil {
		return false, fmt.Errorf("store: %w", err)
	} else if torn {
		line = append([]byte{'\n'}, line...)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return false, fmt.Errorf("store: append suggestion: %w", err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("store: %w", err)
	}
	return true, nil
}

// endsTorn reports whether the file is non-empty and its last byte is not
// a newline. A missing file is not torn.
func endsTorn(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// ReadSuggestions returns every line of the queue in file order. A
// workspace that never had a suggestion has no file, which is not an
// error, and a line that does not parse is skipped: the only way to get
// one is a crash partway through a write.
func ReadSuggestions(root string) ([]SuggestionLine, error) {
	raw, err := os.ReadFile(SuggestionsPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	var out []SuggestionLine
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var l SuggestionLine
		if json.Unmarshal(line, &l) != nil {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

// PendingSuggestions is the proposals no later line accepted or dismissed,
// oldest first. A proposal made again after it was dismissed is pending
// again: the operator dismissed the earlier one, not every future one.
func PendingSuggestions(lines []SuggestionLine) []SuggestionLine {
	// at holds the position of each pending id's proposal, so a proposal
	// made again after a dismissal is ordered by when it was made again.
	at := map[string]int{}
	for i, l := range lines {
		switch l.Ev {
		case "proposed":
			if _, ok := at[l.ID]; !ok {
				at[l.ID] = i
			}
		case "accepted", "dismissed":
			delete(at, l.ID)
		}
	}
	out := []SuggestionLine{}
	for i, l := range lines {
		if p, ok := at[l.ID]; ok && p == i {
			out = append(out, l)
		}
	}
	return out
}
