package provider

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrPathEscape is returned for a path argument that resolves outside the
// workspace root. The text is stable: adapters and Sirdar's own agent loop
// show it to the model.
var ErrPathEscape = errors.New("path escapes workspace")

// ResolveWithin turns an agent-supplied path into a real absolute path
// inside root, or returns ErrPathEscape.
//
// A relative path is joined onto root; an absolute path is taken as given.
// Both the candidate and the root are then resolved through symlinks — for
// a path that does not exist yet, the nearest existing ancestor is resolved
// and the missing tail re-appended — so a symlink inside the workspace
// pointing out of it is refused like any other outside path, and a file
// that is about to be created under a symlinked directory still lands on
// its real location before it is judged.
//
// It is the one confinement every path-taking surface goes through: the
// tools in internal/agenttools, and the permission policy that judges the
// editing tools a provider CLI runs on its own.
func ResolveWithin(root, path string) (string, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return "", ErrPathEscape
	}
	realRoot, err := EvalNearest(root)
	if err != nil {
		return "", ErrPathEscape
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return "", ErrPathEscape
	}

	// A leading "~" is a home directory to whatever expands it, and the
	// workspace is not inside one. Nothing here expands it, so joining it
	// onto the root would approve the path by making it mean a directory
	// literally named "~" — an approval that stops being harmless the
	// moment any layer below does expand it.
	if strings.HasPrefix(path, "~") {
		return "", ErrPathEscape
	}

	candidate := filepath.Clean(path)
	if !filepath.IsAbs(candidate) {
		candidate, err = filepath.Abs(filepath.Join(root, path))
		if err != nil {
			return "", ErrPathEscape
		}
	}
	real, err := EvalNearest(candidate)
	if err != nil {
		return "", ErrPathEscape
	}
	if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
		return "", ErrPathEscape
	}
	return real, nil
}

// EvalNearest resolves symlinks in p. When p itself does not exist it walks
// up to the nearest existing ancestor, resolves that, and re-appends the
// missing tail.
func EvalNearest(p string) (string, error) {
	current := p
	rest := ""
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if rest == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, rest), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		rest = filepath.Join(filepath.Base(current), rest)
		current = parent
	}
}

// Reserved directory names a write may never reach, whatever else the
// policy allows.
const (
	// GitDir is refused at any depth. A file written under it is a file
	// the next `git commit` executes (hooks), reads as configuration, or
	// takes as object storage; none of that is the source change a fix was
	// approved to make.
	GitDir = ".git"
	// SirdarDir is refused at the workspace root, where Sirdar keeps the
	// run records, the register and the workspace configuration — the
	// permission lists a session is judged by among them.
	SirdarDir = ".sirdar"
)

// ReservedWrite names the reserved directory a path lies inside — ".git" at
// any depth, or the workspace's own ".sirdar" — and returns "" for an
// ordinary workspace file.
//
// path is expected to be an absolute, symlink-resolved path (what
// ResolveWithin returns). A path outside root is not this function's
// business and comes back as "": that is the confinement check's refusal,
// with its own message.
func ReservedWrite(root, path string) string {
	realRoot, err := EvalNearest(root)
	if err != nil {
		realRoot = filepath.Clean(root)
	}
	if abs, err := filepath.Abs(realRoot); err == nil {
		realRoot = abs
	}
	rel, err := filepath.Rel(realRoot, filepath.Clean(path))
	if err != nil {
		return ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	segments := strings.Split(filepath.ToSlash(rel), "/")
	for _, seg := range segments {
		if seg == GitDir {
			return GitDir
		}
	}
	if len(segments) > 0 && segments[0] == SirdarDir {
		return SirdarDir
	}
	return ""
}
