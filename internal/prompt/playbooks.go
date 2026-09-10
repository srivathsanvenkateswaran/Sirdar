package prompt

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// defaultPlaybooks holds the five scaffold playbooks written by
// ScaffoldPlaybooks, embedded at build time.
//
//go:embed playbooks/*.md
var defaultPlaybooks embed.FS

const defaultPlaybooksSubdir = "playbooks"

// Playbook is one playbook loaded from a workspace's playbooks directory:
// its name (the filename without the .md extension) and its markdown body.
type Playbook struct {
	Name, Body string
}

// LoadPlaybooks reads every *.md file directly inside dir, in filename
// order, and returns one Playbook per file. A dir that does not exist is
// not an error: LoadPlaybooks returns (nil, nil) so a workspace with no
// playbooks configured still gets a valid, empty prompt section.
func LoadPlaybooks(dir string) ([]Playbook, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("prompt: read playbooks dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	playbooks := make([]Playbook, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("prompt: read playbook %s: %w", name, err)
		}
		playbooks = append(playbooks, Playbook{
			Name: strings.TrimSuffix(name, ".md"),
			Body: string(data),
		})
	}
	return playbooks, nil
}

// ScaffoldPlaybooks writes the five embedded default playbooks into dir,
// creating dir if it does not exist. It never overwrites a file that is
// already there, so a workspace's edits to a scaffolded playbook (or a
// playbook it wrote itself under the same name) survive a re-run of
// `sirdar init`.
func ScaffoldPlaybooks(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("prompt: create playbooks dir %s: %w", dir, err)
	}

	entries, err := defaultPlaybooks.ReadDir(defaultPlaybooksSubdir)
	if err != nil {
		return fmt.Errorf("prompt: read embedded playbooks: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		dst := filepath.Join(dir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // never overwrite an existing playbook
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("prompt: stat %s: %w", dst, err)
		}

		data, err := defaultPlaybooks.ReadFile(filepath.Join(defaultPlaybooksSubdir, e.Name()))
		if err != nil {
			return fmt.Errorf("prompt: read embedded playbook %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("prompt: write playbook %s: %w", dst, err)
		}
	}
	return nil
}
