package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/loginpath"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// environmentCheck says where this process's PATH came from and whether
// the binaries a run will spawn can be found on it.
//
// It exists because the two shells do not see the same PATH. A terminal
// gives `sirdar` the operator's own, profile and all. A desktop app
// launched from the Dock, from Spotlight or from a Linux launcher gets
// launchd's or the session manager's — on macOS
// /usr/bin:/bin:/usr/sbin:/sbin, which has none of the directories a
// provider CLI is installed in. The symptom is a run that fails at its
// first step with `exec: "claude": executable file not found in $PATH`
// from one shell and works from the other, and nothing else in the report
// distinguishes the two. internal/loginpath is the fix; this row is how an
// operator sees whether it took.
func environmentCheck(cfg *config.Config) Check {
	return environmentCheckFor(loginpath.Current(), providerBinaries(cfg), lookupBinary)
}

// binaryRef is one program a run in this workspace will spawn, and the
// configuration key that says where it is.
type binaryRef struct {
	// name is the binary as os/exec will be given it: a bare name to look
	// up on PATH, or the path the workspace configured.
	name string
	// setting is the config key that overrides the lookup — one of the
	// keys docs/config.md's Providers section lists.
	setting string
}

// providerBinaries lists the programs whose absence stops a run in this
// workspace: the active provider's binary, and any other provider the
// workspace still carries a path for — a config that names
// providers.codex.path is a config someone switches back to.
//
// provider: openai contributes nothing. Its agent loop runs in this
// process and spawns no CLI at all, so there is no binary to look for and
// the row says as much rather than inventing one.
func providerBinaries(cfg *config.Config) []binaryRef {
	var refs []binaryRef
	seen := map[string]bool{}
	add := func(ref binaryRef) {
		if ref.name == "" || seen[ref.name] {
			return
		}
		seen[ref.name] = true
		refs = append(refs, ref)
	}

	// A configured value wins over the default binary name. It is
	// expanded only when it is written as a path: `~/bin/claude` is a
	// location, `claude` is a name for PATH to answer, and joining the
	// second onto the workspace root would look for it in the repository.
	pick := func(setting, configured, fallback string) binaryRef {
		ref := binaryRef{name: fallback, setting: setting}
		if v := strings.TrimSpace(configured); v != "" {
			ref.name = expandIfPath(cfg, v)
		}
		return ref
	}

	switch cfg.Provider {
	case "codex":
		add(pick("providers.codex.path", cfg.Providers.Codex.Path, "codex"))
	case "qwen":
		path := ""
		if cfg.Qwen != nil {
			path = cfg.Qwen.Path
		}
		add(pick("qwen.path", path, "qwen"))
	case "cursor":
		path := ""
		if cfg.Cursor != nil {
			path = cfg.Cursor.Path
		}
		add(pick("cursor.path", path, "cursor-agent"))
	case "agy":
		path := ""
		if cfg.Agy != nil {
			path = cfg.Agy.Path
		}
		add(pick("agy.path", path, "agy"))
	case "acp":
		// acp.command is the program — `copilot`, `opencode`, `kimi` —
		// with its flags in acp.args, so the whole value is the binary.
		if cfg.ACP != nil && strings.TrimSpace(cfg.ACP.Command) != "" {
			add(binaryRef{name: expandIfPath(cfg, strings.TrimSpace(cfg.ACP.Command)), setting: "acp.command"})
		}
	case "openai":
		// Nothing to add.
	default:
		add(pick("providers.claude.path", cfg.Providers.Claude.Path, "claude"))
	}

	// Any other provider the workspace still points somewhere specific.
	// The active one is already in, and add() drops the repeat.
	for _, other := range []struct{ setting, path string }{
		{"providers.claude.path", cfg.Providers.Claude.Path},
		{"providers.codex.path", cfg.Providers.Codex.Path},
	} {
		if v := strings.TrimSpace(other.path); v != "" {
			add(binaryRef{name: expandIfPath(cfg, v), setting: other.setting})
		}
	}
	return refs
}

// expandIfPath expands a value that names a location and leaves a bare
// program name alone, since a bare name is PATH's to answer and
// Config.ExpandPath would make it relative to the workspace root.
func expandIfPath(cfg *config.Config, value string) string {
	if isPathLike(value) {
		return cfg.ExpandPath(value)
	}
	return value
}

// isPathLike reports whether a value names a location rather than a
// program for PATH to find. Both separators are tested on every platform:
// Windows accepts "/" in a path, and a config file written on one machine
// is read on another.
func isPathLike(value string) bool {
	return strings.HasPrefix(value, "~") ||
		strings.ContainsRune(value, '/') ||
		strings.ContainsRune(value, filepath.Separator)
}

// lookupBinary resolves one reference the way os/exec will when the run
// starts it: a bare name goes through PATH, a path is taken as given. It
// returns the resolved path, or "" when nothing answers.
func lookupBinary(name string) string {
	if isPathLike(name) {
		info, err := os.Stat(name)
		if err != nil || info.IsDir() {
			return ""
		}
		return name
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

// environmentCheckFor takes the PATH record, the binaries and the lookup
// as arguments rather than reading the process, so every case — a login
// shell that answered, one that did not, a configured path that is wrong,
// a provider with no binary at all — is testable without a machine that
// happens to have those CLIs installed.
func environmentCheckFor(path loginpath.Result, refs []binaryRef, lookup func(string) string) Check {
	check := Check{Name: "environment", OK: true}

	clauses := []string{"PATH from " + pathSourcePhrase(path)}
	if n := len(path.Added); n > 0 {
		clauses = append(clauses, fmt.Sprintf("%d user bin %s appended", n, plural(n, "directory", "directories")))
	}
	if path.Err != nil {
		// Worth saying and not worth failing on: the process PATH still
		// stands, and on a machine whose binaries are on it anyway
		// nothing is wrong. It is the explanation for the rows below when
		// something is.
		check.Level = string(provider.LevelWarn)
		clauses = append(clauses, "the login shell could not be read ("+path.Err.Error()+")")
	}

	if len(refs) == 0 {
		clauses = append(clauses, "this provider spawns no binary of its own")
	}
	for _, ref := range refs {
		if resolved := lookup(ref.name); resolved != "" {
			clauses = append(clauses, filepath.Base(ref.name)+" → "+resolved)
			continue
		}
		// A run cannot start without it, so the row fails rather than
		// warns, and drops any warning level the PATH clause had set.
		check.OK = false
		check.Level = ""
		if isPathLike(ref.name) {
			clauses = append(clauses, ref.setting+" names "+ref.name+", and there is no such file")
			continue
		}
		clauses = append(clauses, provider.PathFix(ref.name, ref.setting))
	}

	check.Detail = strings.Join(clauses, "; ")
	return check
}

// pathSourcePhrase renders loginpath's record of where the PATH came from.
// "process" is the ordinary case for `sirdar doctor` in a terminal, where
// the inherited PATH is the operator's already; the desktop app is the one
// that goes looking for a login shell.
func pathSourcePhrase(path loginpath.Result) string {
	entries := 0
	if path.Path != "" {
		entries = len(strings.Split(path.Path, string(os.PathListSeparator)))
	}
	source := "this process's environment"
	if path.Source != "process" {
		source = "the " + path.Source
	}
	return fmt.Sprintf("%s, %d %s", source, entries, plural(entries, "entry", "entries"))
}

// plural picks the form for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
