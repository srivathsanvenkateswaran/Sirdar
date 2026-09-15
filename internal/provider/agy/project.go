package agy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file is how a Sirdar triage gets to read anything at all.
//
// The Antigravity CLI decides permissions from files, not from a channel a
// parent process can answer (see the package comment). In a headless run a
// tool that needs approval is auto-denied, and round 1 of this adapter
// watched that happen to `view_file`: the CLI refused the read, said so on
// stderr — "a tool required the \"read_file\" permission that headless mode
// cannot prompt for, so it was auto-denied. Add an allow-rule under
// permissions.allow in settings.json" — and the agent answered the ticket
// out of the ticket text alone. A triage that reads nothing is not a
// triage.
//
// The allow rule the CLI asks for lives in the operator's own
// ~/.gemini/antigravity-cli/settings.json, and Sirdar does not write to
// that file: it is shared with the Antigravity IDE and every other `agy`
// session on the machine, and an allow rule left behind there would outlive
// the run that needed it.
//
// There is a second place the same rules can go, and it outranks the first.
// The CLI's own changelog: "Improved permission config merging priorities
// by ensuring project-specific configurations (located in
// ~/.gemini/config/projects/) take precedence over global settings in
// ~/.gemini/antigravity-cli/settings.json". A project is one JSON file in
// that directory named after its id, `--project <id>` selects it for the
// session, and the message the CLI logs when it loads one —
// "ApplyProjectPermissionGrants: stored %d allow, %d deny grants from
// project %q" — is the grants being applied.
//
// So Sirdar writes **its own** project file, for one session, carrying
// nothing but the permission rules that session needs; passes
// `--project <id>`; and deletes the file when the session ends. It is still
// the operator's directory, which is why the file is named so it cannot be
// mistaken for theirs (`sirdar-<hex>.json`), is never reused across runs,
// and is swept on the next start if a crash left one behind.
//
// The schema is the `exa.project_pb.Project` message the binary carries:
//
//	Project {
//	  id, name
//	  project_resources → Resources { resources: [ Resource { folder_uri } ] }
//	  permission_grants → PermissionGrants {
//	      permission_grants → PermissionGrantsConfig { allow, deny, ask }
//	      v2_migrated }
//	  settings → ProjectSettings {
//	      file_access_policy, internet_policy, sandbox_mode,
//	      auto_execution_policy, artifact_review_mode, permission_preset, … }
//	}
//
// written as protojson (camelCase keys), which is the encoding the CLI's
// own `--new-project` file uses.

const (
	// configDirName is ~/.gemini/config, the directory the CLI shares with
	// the Antigravity IDE. projectsDirName is the projects directory
	// inside it, one JSON file per project, each named after its id.
	configDirName   = "config"
	projectsDirName = "projects"

	// projectIDPrefix marks a project file as Sirdar's own. Project ids
	// are free-form — the CLI's own default project is
	// `default-cli-project` — so the id doubles as the marker, and because
	// the file is named after the id, a stale file is recognisable from
	// its name alone without parsing it.
	projectIDPrefix = "sirdar-"

	// staleProjectAge is how long a Sirdar project file has to have been
	// untouched before a later run treats it as abandoned and deletes it.
	// It is generous on purpose: the sweep must never remove a file
	// belonging to a run that is still going, and the longest budget a
	// workspace can set is measured in minutes.
	staleProjectAge = 12 * time.Hour
)

// projectAllowRules is what a triage session is allowed to do, in the
// CLI's own rule vocabulary. That vocabulary has four verbs — `read_file`,
// `write_file`, `command` and `execute_url` (plus the deprecated
// `unsandboxed`) — and no way to name an individual tool, so "allow the
// read tools" is exactly one rule. `view_file`, `grep_search`, `list_dir`
// and `find_by_name` all go through `read_file`; the round-1 refusal names
// it in as many words.
//
// The target is `*` and not the workspace path. The CLI documents prefix
// matching for `command` ("'git' matches 'git add', 'git commit'") and
// documents nothing for `read_file`, whose canonical forms in the binary
// are `read_file(*)` and `read_file(/)`. Narrowing the rule to the
// workspace on a guess and having the CLI read it as a literal path would
// deny every read and put the run back where round 1 was, so the rule is
// the one whose meaning is not in doubt. What that costs is stated in
// docs/config.md: the session can read a file outside the workspace. What
// it does not cost is exfiltration — `command`, `execute_url` and every
// write are denied below, so a read that wanders has nowhere to go but the
// triage note.
var projectAllowRules = []string{"read_file(*)"}

// projectDenyRules is everything else. `run_command` stays denied in
// triage: Sirdar cannot mediate an agy tool call, so a command allowed
// here would be a command judged by nobody — `permissions.bash`, which is
// what decides this on every other provider, has no attachment point on
// this wire. A triage that genuinely needs a command's output is a triage
// for a provider Sirdar can mediate.
//
// These are denials Sirdar states rather than relies on. Plan mode already
// refuses a write, and the breach watch (see observeWrite) ends the run
// over one that completes anyway. The rules are here so that an operator's
// own `permissions.allow` — which Sirdar can neither see nor edit — is
// outranked by something for the length of the session.
var projectDenyRules = []string{
	"write_file(*)",
	"command(*)",
	"execute_url(*)",
}

// projectFile is the subset of the Project message Sirdar writes. Every
// field here was read off the descriptor embedded in the binary; nothing
// is invented, because the CLI parses this file as protojson and a key it
// does not know fails the parse ("unmarshal project %s: %w").
type projectFile struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	ProjectResources *projectResources `json:"projectResources,omitempty"`
	PermissionGrants *projectGrants    `json:"permissionGrants,omitempty"`
	Settings         *projectSettings  `json:"settings,omitempty"`
}

type projectResources struct {
	Resources []projectResource `json:"resources"`
}

type projectResource struct {
	FolderURI string `json:"folderUri"`
}

type projectGrants struct {
	PermissionGrants grantsConfig `json:"permissionGrants"`
	// V2Migrated says the grants are already in the current format, so
	// the CLI's GrantsV2 migration leaves the file alone rather than
	// rewriting a file that is about to be deleted.
	V2Migrated bool `json:"v2Migrated"`
}

type grantsConfig struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
	Ask   []string `json:"ask,omitempty"`
}

// projectSettings carries only the two settings whose enum values say
// plainly what they do.
//
// The rest of ProjectSettings is deliberately left out. `fileAccessPolicy`
// and `internetPolicy` are both `AgentSettingPolicy`
// (ALLOW / ASK / DENY) and neither the descriptor nor the CLI's help says
// what surface each governs — a `fileAccessPolicy: DENY` meant as "no
// files outside the workspace" and read as "no files" would blind the run
// it was added to protect. `sandboxMode` and `artifactReviewMode` are the
// same kind of guess. A setting whose effect cannot be predicted is worth
// less than a permission rule whose effect can, and the rules above are
// doing the work.
type projectSettings struct {
	// AutoExecutionPolicy OFF: never run a command without review. The
	// deny rule above already refuses one; this says the same thing to
	// the layer that decides whether to ask.
	AutoExecutionPolicy string `json:"autoExecutionPolicy,omitempty"`
	// PermissionPreset REQUEST_REVIEW is the mode the CLI reports on its
	// own `init` line for a default headless session. Naming it pins it,
	// so a session does not inherit an operator's global preset of
	// TURBO — which approves tools without asking.
	PermissionPreset string `json:"permissionPreset,omitempty"`
}

const (
	autoExecutionOff       = "CASCADE_COMMANDS_AUTO_EXECUTION_OFF"
	permissionPresetReview = "AGENT_PERMISSION_PRESET_REQUEST_REVIEW"
)

// projectsDir is ~/.gemini/config/projects.
func projectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, stateDirParent, configDirName, projectsDirName), nil
}

// newProjectID mints an id for one session. It is random rather than
// derived from the workspace so two runs of the same workspace never share
// a file, and prefixed so the sweep and an operator reading their own
// projects directory can both tell whose it is.
func newProjectID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return projectIDPrefix + hex.EncodeToString(b[:]), nil
}

// writeProject creates the session's project file and returns its id and
// path. cwd is the workspace the session runs in; it is named as the
// project's resource so the CLI resolves the project against the same
// directory it was started in.
func writeProject(cwd string) (id, path string, err error) {
	dir, err := projectsDir()
	if err != nil {
		return "", "", fmt.Errorf("home directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("create %s: %w", dir, err)
	}
	id, err = newProjectID()
	if err != nil {
		return "", "", fmt.Errorf("project id: %w", err)
	}

	p := projectFile{
		ID:   id,
		Name: "Sirdar triage (temporary)",
		PermissionGrants: &projectGrants{
			PermissionGrants: grantsConfig{
				Allow: append([]string(nil), projectAllowRules...),
				Deny:  append([]string(nil), projectDenyRules...),
			},
			V2Migrated: true,
		},
		Settings: &projectSettings{
			AutoExecutionPolicy: autoExecutionOff,
			PermissionPreset:    permissionPresetReview,
		},
	}
	if abs := absWorkspace(cwd); abs != "" {
		p.ProjectResources = &projectResources{Resources: []projectResource{{FolderURI: "file://" + abs}}}
	}

	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", "", err
	}
	path = filepath.Join(dir, id+".json")
	// 0o600: the file says what one session may do, and nothing else on
	// the machine has a reason to read it.
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return "", "", fmt.Errorf("write %s: %w", path, err)
	}
	return id, path, nil
}

// absWorkspace resolves the session's working directory to the absolute
// path a folderUri needs. An empty or unresolvable cwd yields no resource
// block at all rather than a URI naming the wrong directory.
func absWorkspace(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	return abs
}

// removeProject deletes the session's project file. It is called from both
// Cancel and Wait and may therefore run twice; a file already gone is not
// an error, and neither is one an operator deleted by hand.
func removeProject(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		// Reported, not returned: a project file left behind is a stray
		// row in the operator's project list, which the next run's sweep
		// clears. It is not a reason to fail a run that has its answer.
		fmt.Fprintf(os.Stderr, "sirdar: remove agy project file %s: %v\n", path, err)
	}
}

// sweepStaleProjects deletes Sirdar project files older than maxAge, and
// returns how many it removed.
//
// A run that is killed between writing the file and reaping the process —
// SIGKILL, a power cut, a `pkill sirdar` — leaves one behind, and the
// operator sees it in their own project list. Nothing else cleans it up,
// so the next session does. Only files whose name carries the Sirdar
// prefix are considered, so nothing of the operator's is ever a candidate,
// and the age bound keeps a concurrent Sirdar run's own file out of reach.
func sweepStaleProjects(now time.Time, maxAge time.Duration) int {
	dir, err := projectsDir()
	if err != nil {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, projectIDPrefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < maxAge {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed++
		}
	}
	return removed
}
