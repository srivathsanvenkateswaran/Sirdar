package note

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UpdateTriageStatus rewrites the leading YAML frontmatter block of the
// triage note at path in place: it sets (or adds) the "status:" line, and
// for each of "rca" and "resolution" present in links, sets (or adds
// immediately before the closing "---") the matching "<key>:" line.
// Everything after the frontmatter is copied through byte for byte. The
// file is replaced atomically.
func UpdateTriageStatus(path, status string, links map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("note: read %s: %w", path, err)
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) < 1 || strings.TrimRight(lines[0], "\r") != "---" {
		return fmt.Errorf("note: %s has no frontmatter", path)
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fmt.Errorf("note: %s frontmatter is not closed", path)
	}

	fm := append([]string(nil), lines[1:end]...)
	body := lines[end:] // starts with the closing "---" line

	fm = setFrontmatterKey(fm, "status", status)
	for _, key := range []string{"rca", "resolution"} {
		val, ok := links[key]
		if !ok {
			continue
		}
		fm = setFrontmatterKey(fm, key, val)
	}

	out := append([]string{"---"}, fm...)
	out = append(out, body...)

	return atomicWriteFile(path, []byte(strings.Join(out, "\n")))
}

// setFrontmatterKey replaces the value of an existing "<key>:" line in fm,
// or appends a new one when none exists.
func setFrontmatterKey(fm []string, key, value string) []string {
	prefix := key + ":"
	for i, l := range fm {
		if strings.HasPrefix(l, prefix) {
			fm[i] = key + ": " + value
			return fm
		}
	}
	return append(fm, key+": "+value)
}

// atomicWriteFile writes data to path by writing a temp file in the same
// directory and renaming it over path.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".note-*.tmp")
	if err != nil {
		return fmt.Errorf("note: create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("note: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("note: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("note: rename temp file: %w", err)
	}
	return nil
}
