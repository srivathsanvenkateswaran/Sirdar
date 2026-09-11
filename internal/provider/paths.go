package provider

import (
	"context"
	"errors"
	"os/exec"
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
// any depth, the workspace's own ".sirdar", or one of the extra paths this
// run reserved — and returns "" for an ordinary workspace file.
//
// path is expected to be an absolute, symlink-resolved path (what
// ResolveWithin returns). A path outside root is not this function's
// business and comes back as "": that is the confinement check's refusal,
// with its own message.
//
// Every comparison folds case. macOS and Windows both ship a
// case-insensitive filesystem by default, where "<root>/.GIT/hooks/pre-commit"
// and "<root>/.git/hooks/pre-commit" are the same file, so a case-sensitive
// segment test refuses one and waves the other through. The fold is applied
// on every OS rather than sniffing the filesystem: on the case-sensitive
// exception, ".GIT" is a directory nobody has, and refusing a write to it
// costs nothing.
//
// extra holds the paths this particular run reserved on top of the two
// constants — the repository's core.hooksPath, when it sets one, whose
// contents git executes exactly like .git/hooks. Each is taken relative to
// root unless it is already absolute, and a path is reserved when it is one
// of them or lies inside one.
func ReservedWrite(root, path string, extra []string) string {
	realRoot := absEval(root)
	rel, err := filepath.Rel(realRoot, filepath.Clean(path))
	if err != nil {
		return ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	segments := strings.Split(filepath.ToSlash(rel), "/")
	for _, seg := range segments {
		if strings.EqualFold(seg, GitDir) {
			return GitDir
		}
	}
	if len(segments) > 0 && strings.EqualFold(segments[0], SirdarDir) {
		return SirdarDir
	}
	for _, e := range extra {
		if e = strings.TrimSpace(e); e == "" {
			continue
		}
		if !filepath.IsAbs(e) {
			e = filepath.Join(realRoot, e)
		}
		if within(absEval(e), filepath.Clean(path)) {
			return DisplayPath(realRoot, e)
		}
	}
	return ""
}

// DisplayPath renders a path relative to root when it is inside it, and
// leaves it absolute when it is not, so a refusal names ".husky" rather
// than the machine's directory layout.
func DisplayPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.ToSlash(rel)
}

// within reports whether target is dir or lies inside it, comparing one
// path segment at a time and folding case for the same reason
// ReservedWrite does.
func within(dir, target string) bool {
	d := strings.Split(filepath.ToSlash(filepath.Clean(dir)), "/")
	t := strings.Split(filepath.ToSlash(filepath.Clean(target)), "/")
	if len(t) < len(d) {
		return false
	}
	for i := range d {
		if !strings.EqualFold(d[i], t[i]) {
			return false
		}
	}
	return true
}

// absEval is EvalNearest followed by filepath.Abs, falling back to a plain
// Clean for a path neither can make sense of.
func absEval(p string) string {
	out, err := EvalNearest(p)
	if err != nil {
		out = filepath.Clean(p)
	}
	if abs, err := filepath.Abs(out); err == nil {
		return abs
	}
	return filepath.Clean(out)
}

// HooksPath is the repository's configured core.hooksPath, as an absolute
// path, or "" when the repository sets none, is not a repository, or has no
// git to ask.
//
// A repository that sets it — every workspace using husky, lefthook or a
// checked-in .githooks/ does — makes an ordinary-looking source directory
// the place git runs code from on the next commit or push. Without this the
// directory is a writable source file like any other, and the reserved-path
// rule only covers .git/hooks, which such a repository does not use.
//
// The read is one `git config --get core.hooksPath` in root. It is
// deliberately not `git config --local`: a value inherited from the global
// or system configuration still decides where hooks come from.
func HooksPath(ctx context.Context, root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--get", "core.hooksPath")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return ""
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(root, value)
	}
	return filepath.Clean(value)
}

// HooksDir is the directory git would run this repository's hooks from:
// HooksPath when the repository sets one, and <root>/.git/hooks otherwise.
// It is what the fix flow snapshots, so a repository that configures no
// hooksPath is still watched over the directory git would actually use.
func HooksDir(ctx context.Context, root string) string {
	if p := HooksPath(ctx, root); p != "" {
		return p
	}
	if strings.TrimSpace(root) == "" {
		return ""
	}
	return filepath.Join(root, GitDir, "hooks")
}
