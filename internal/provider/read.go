package provider

import (
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"strings"
)

// ErrReadEscape is returned for a read-class path that lands outside the
// run's read scope. The text is stable: adapters and Sirdar's own agent
// loop show it to the model.
var ErrReadEscape = errors.New("read outside the workspace")

// readPathArgs names, per read-class tool, the arguments that carry a path
// the call would read. Both naming conventions are here: the capitalised
// names are Claude Code's, which the qwen adapter also maps its own tool
// ids onto (policyNames), and the lower-case ones are the tools Sirdar's
// own agent loop offers (internal/agenttools).
//
// Only arguments that really are paths are listed. Grep's "pattern" is a
// regular expression — a search for the literal text "/etc/passwd" inside
// the workspace is a read of the workspace, not of /etc — while Glob's
// "pattern" is the path expression the tool walks, so it is judged.
// "paths" is Qwen Code's read_many_files, which arrives under the name
// Read.
//
// A tool that names none of its path arguments is reading the session's
// own working directory, which is the workspace root: nothing to judge, so
// the call is allowed. That is the opposite of how a write or a fetch
// naming no target is treated, and deliberately — an LS with no arguments
// is the most ordinary call a session makes.
var readPathArgs = map[string][]string{
	"Read": {"file_path", "path", "notebook_path", "paths"},
	"Glob": {"path", "pattern"},
	"Grep": {"path"},
	"LS":   {"path"},

	"read_file": {"path", "file_path", "paths"},
	"list_dir":  {"path"},
	"grep":      {"path"},
	"glob":      {"path", "pattern"},
}

// IsReadTool reports whether a tool name is one whose target the read
// scope judges.
func IsReadTool(tool string) bool { return len(readPathArgs[tool]) > 0 }

// ReadScope is where a read-class tool may look: the directories a path
// has to land inside, and the permissions.readAlso globs that widen it.
//
// Roots[0] is the workspace root — the directory a relative path is
// resolved against — followed by whatever else this run reads from: its
// own run directory and the bundle of ticket text and attachments staged
// inside it, which in a fix run sit outside the session's worktree
// entirely.
//
// A scope with no roots confines nothing. That is not a run: it is the
// eval rubric's judge session and the provider fallbacks, which have no
// workspace to judge a path against. Every run internal/run starts names
// one.
type ReadScope struct {
	Roots []string
	Also  []string
}

// ReadScope is the scope this policy judges reads against.
func (p *PermissionPolicy) ReadScope() ReadScope {
	if p == nil {
		return ReadScope{}
	}
	roots := make([]string, 0, 1+len(p.ReadRoots))
	if strings.TrimSpace(p.Root) != "" {
		roots = append(roots, p.Root)
	}
	for _, r := range p.ReadRoots {
		if strings.TrimSpace(r) != "" {
			roots = append(roots, r)
		}
	}
	return ReadScope{Roots: roots, Also: p.ReadAlso}
}

// Confined reports whether this scope judges anything at all.
func (s ReadScope) Confined() bool { return len(s.Roots) > 0 }

// Resolve turns an agent-supplied read path into the real absolute path it
// names, or returns ErrReadEscape when that path is outside the scope.
//
// A relative path is resolved against the first root, the workspace; an
// absolute one is taken as given. Either is then resolved through symlinks
// — with the nearest existing ancestor standing in for a path that does
// not exist — so a link inside the workspace pointing out of it is refused
// like any other outside path.
//
// A leading "~" is left alone rather than expanded, the same stance
// ResolveWithin takes: the workspace is not inside a home directory, and a
// path nothing here expands must not be approved by pretending it names a
// directory literally called "~". It can still be allowed by a readAlso
// entry, which is matched against the path as the agent wrote it as well
// as against the resolved form.
func (s ReadScope) Resolve(path string) (string, error) {
	raw := strings.TrimSpace(path)
	if raw == "" {
		return "", ErrReadEscape
	}
	if !s.Confined() {
		return raw, nil
	}

	var candidate string
	switch {
	case strings.HasPrefix(raw, "~"):
		candidate = filepath.Clean(raw)
	case IsRooted(raw):
		// filepath.Abs, not Clean: a Windows path that is rooted without
		// being absolute ("\etc\hosts", "/etc/passwd", "D:sub") has to be
		// resolved against the drive it names before it can be compared
		// with a root, or it matches nothing and is denied for the wrong
		// reason.
		if abs, err := filepath.Abs(raw); err == nil {
			candidate = abs
		} else {
			candidate = filepath.Clean(raw)
		}
	default:
		candidate = filepath.Join(s.Roots[0], raw)
	}

	real := candidate
	if resolved, err := EvalNearest(candidate); err == nil {
		real = resolved
	}

	if !strings.HasPrefix(raw, "~") {
		for _, root := range s.Roots {
			realRoot := absEval(root)
			if real == realRoot || strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
				return real, nil
			}
		}
	}
	for _, pattern := range s.Also {
		if matchReadAlso(pattern, raw, real) {
			return real, nil
		}
	}
	return "", ErrReadEscape
}

// matchReadAlso reports whether one permissions.readAlso entry covers a
// path. The entry is tried as written and with a leading "~"/"~user"
// expanded, against both the path the agent wrote and its resolved form,
// so an operator does not have to guess which spelling a given agent will
// send.
//
// An entry carrying no wildcard names a directory and everything under it:
// "/opt/reference" covers "/opt/reference/runbook.md". One carrying a
// wildcard is a glob, matched by MatchGlob, where "*" spans "/" — so
// "~/.claude/skills/*" covers a file at any depth under it.
func matchReadAlso(pattern, raw, real string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	patterns := []string{pattern}
	if expanded, ok := expandHome(pattern); ok {
		patterns = append(patterns, expanded)
	}
	// Both sides are compared with "/" separators, on every OS. The
	// entry is written by hand in config.yaml, where a Windows operator
	// may reasonably spell a path either way — the documentation's own
	// examples use "/" — while the path being judged arrives however the
	// agent wrote it and the resolved form always carries the platform's
	// own separator. Matching the two spellings literally meant a
	// readAlso entry on Windows widened nothing whichever way it was
	// written, since one side or the other always disagreed.
	for _, p := range patterns {
		p = filepath.ToSlash(p)
		for _, target := range []string{raw, real} {
			if target == "" {
				continue
			}
			target = path.Clean(filepath.ToSlash(target))
			if strings.ContainsAny(p, "*?") {
				if MatchGlob(p, target) {
					return true
				}
				continue
			}
			clean := path.Clean(p)
			if target == clean || strings.HasPrefix(target, clean+"/") {
				return true
			}
		}
	}
	return false
}

// decideRead judges where a read-class tool would look. Being a read is
// only half the permission: a triage session's Read, Glob, Grep and LS
// take an absolute path, so without this the read-only guarantee said
// nothing at all about what the session could read — a live run read a
// skill file out of the operator's home directory, which is neither the
// workspace nor anything the ticket points at.
//
// Bash is not judged here. Its own allow-list already refuses an argument
// that looks like a path outside the root (see MatchCommand), and it reads
// the command as text rather than resolving a named argument.
func (p *PermissionPolicy) decideRead(tool string, input json.RawMessage) Decision {
	scope := p.ReadScope()
	if !scope.Confined() {
		return Decision{Allow: true}
	}
	targets, ok := readTargets(tool, input)
	if !ok {
		return Decision{Allow: false, Message: "Sirdar policy: " + tool +
			" arguments do not parse, so what it would read cannot be checked"}
	}
	for _, target := range targets {
		if _, err := scope.Resolve(target); err != nil {
			return Decision{Allow: false,
				Message: "Sirdar policy: " + ErrReadEscape.Error() + ": " + target}
		}
	}
	return Decision{Allow: true}
}

// readTargets lifts the paths out of a read-class call. It reports false
// when the arguments are there but cannot be read as an object, which is
// the one case a read is refused rather than judged: what such a call
// would read cannot be seen.
func readTargets(tool string, input json.RawMessage) ([]string, bool) {
	names := readPathArgs[tool]
	if len(names) == 0 {
		return nil, true
	}
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" || trimmed == "null" {
		return nil, true
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return nil, false
	}
	var out []string
	for _, name := range names {
		raw, ok := obj[name]
		if !ok {
			continue
		}
		var one string
		if err := json.Unmarshal(raw, &one); err == nil {
			if strings.TrimSpace(one) != "" {
				out = append(out, one)
			}
			continue
		}
		var many []string
		if err := json.Unmarshal(raw, &many); err == nil {
			for _, p := range many {
				if strings.TrimSpace(p) != "" {
					out = append(out, p)
				}
			}
		}
	}
	return out, true
}
