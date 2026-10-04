package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// sessionNoteMeta is the frontmatter a saved session reply opens with, in the order a reader
// scans it: what it is filed under, which run answered, when, and what was asked.
type sessionNoteMeta struct {
	Key         string    `yaml:"key"`
	Run         string    `yaml:"run"`
	Created     time.Time `yaml:"created"`
	Instruction string    `yaml:"instruction"`
}

// SaveSessionNote writes a session run's reply into the notes directory under
// notes.filenames.session and records the path on the run. It calls no model.
func SaveSessionNote(cfg *config.Config, runID string, now time.Time) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("run: no workspace configuration")
	}
	rn, state, err := store.Open(cfg.Root, runID)
	if err != nil {
		return "", err
	}
	if state.Kind != store.KindSession {
		return "", noNote{fmt.Errorf("run: %s is a %s run; only a session's reply is saved as a note", runID, state.Kind)}
	}
	reply, err := os.ReadFile(filepath.Join(rn.Dir, answerFile))
	if err != nil {
		return "", noNote{fmt.Errorf("run: %s has no reply yet", runID)}
	}
	if state.Status == store.StatusPreparing || state.Status == store.StatusRunning {
		return "", noNote{fmt.Errorf("run: %s is still working", runID)}
	}

	// Values go through the encoder, so an instruction with a colon or a
	// line break is quoted rather than breaking the frontmatter.
	meta, err := yaml.Marshal(sessionNoteMeta{
		Key: state.Key,
		Run: state.RunID,
		// Whole seconds in UTC: the encoder writes RFC 3339 and would
		// otherwise carry the nanoseconds the run was stamped with.
		Created:     state.StartedAt.UTC().Truncate(time.Second),
		Instruction: state.Instruction,
	})
	if err != nil {
		return "", fmt.Errorf("run: frontmatter for %s: %w", runID, err)
	}
	var body bytes.Buffer
	body.WriteString("---\n")
	body.Write(meta)
	body.WriteString("---\n\n")
	body.Write(reply)

	name := note.Filename(cfg.Notes.Filenames.Session, state.Key, note.Slug(firstLine(state.Instruction)))
	path := filepath.Join(cfg.ExpandPath(cfg.Notes.Dir), filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("run: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("run: write %s: %w", path, err)
	}

	// Saving again overwrites the same file, so the run names it once.
	if !slices.Contains(state.Notes, path) {
		state.Notes = append(state.Notes, path)
	}
	state.UpdatedAt = now
	if err := rn.WriteState(state); err != nil {
		return "", err
	}
	return path, nil
}
