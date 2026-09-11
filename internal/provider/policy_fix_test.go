package provider

import (
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// writeCall builds one editing-tool call the way a provider CLI sends it:
// the tool's own argument name carrying the path it would write.
func writeCall(tool, path string) json.RawMessage {
	key := "file_path"
	switch tool {
	case "NotebookEdit":
		key = "notebook_path"
	case "write_file", "edit_file":
		key = "path"
	}
	in, _ := json.Marshal(map[string]string{key: path})
	return in
}

// TestFixPolicyAllowsTheEditingTools is the whole difference between the
// two modes: a fix may edit a file in the workspace, a triage may not, and
// both are judged by the same policy object.
func TestFixPolicyAllowsTheEditingTools(t *testing.T) {
	root := t.TempDir()
	fix := FixPolicy(root, []string{"git log*", "go test*"}, nil, nil)
	triage := &PermissionPolicy{BashAllow: []string{"git log*"}, Root: root}

	for _, tool := range []string{"Edit", "Write", "MultiEdit", "write_file", "edit_file"} {
		in := writeCall(tool, filepath.Join(root, "export", "csv.go"))
		if d := fix.Decide(tool, in); !d.Allow {
			t.Errorf("fix policy denied %s inside the workspace: %s", tool, d.Message)
		}
		if d := triage.Decide(tool, in); d.Allow {
			t.Errorf("triage policy allowed %s", tool)
		}
	}
}

// TestFixPolicyConfinesWritesToTheWorkspace is the check the tool name on
// its own does not make. Claude Code's Edit and Write take an absolute
// path, and a fix session is the one session they are not refused in, so a
// path that does not land inside the workspace root has to be read off the
// arguments and refused here.
func TestFixPolicyConfinesWritesToTheWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "victim.txt"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "export"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "export", "csv.go"), []byte("package export\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	p := FixPolicy(root, nil, nil, nil)

	allowed := []struct{ name, path string }{
		{"a file in the workspace", filepath.Join(root, "export", "csv.go")},
		{"a relative path in the workspace", "export/csv.go"},
		{"a file that does not exist yet", filepath.Join(root, "export", "stream", "writer.go")},
		{"the workspace's own README", "README.md"},
	}
	for _, tc := range allowed {
		for _, tool := range []string{"Edit", "Write", "MultiEdit", "write_file", "edit_file"} {
			if d := p.Decide(tool, writeCall(tool, tc.path)); !d.Allow {
				t.Errorf("%s denied %s (%s): %s", tool, tc.name, tc.path, d.Message)
			}
		}
	}

	denied := []struct{ name, path string }{
		{"a climb out of the workspace", "../victim.txt"},
		{"a deeper climb", "../../victim.txt"},
		{"an absolute path elsewhere", filepath.Join(outside, "victim.txt")},
		{"a symlink inside the workspace pointing out of it", "escape/victim.txt"},
		{"an absolute path through that symlink", filepath.Join(root, "escape", "victim.txt")},
		{"the machine's password file", "/etc/passwd"},
		{"a home-directory shell profile", "~/.zshrc"},
		{"no path at all", ""},
	}
	for _, tc := range denied {
		for _, tool := range []string{"Edit", "Write", "MultiEdit", "write_file", "edit_file"} {
			d := p.Decide(tool, writeCall(tool, tc.path))
			if d.Allow {
				t.Errorf("%s allowed %s (%s)", tool, tc.name, tc.path)
			}
			if !strings.Contains(d.Message, "Sirdar policy") {
				t.Errorf("%s refused %s without saying why: %q", tool, tc.name, d.Message)
			}
		}
	}
	if d := p.Decide("Write", json.RawMessage(`{}`)); d.Allow {
		t.Error("a Write with no arguments at all was allowed")
	}
}

// TestFixPolicyRefusesGitAndSirdarDirectories: inside the workspace is not
// enough. A file written under .git/ is code the commit Sirdar makes would
// run (hooks) or state git reads; .sirdar/ holds the run records and the
// config whose permission lists this policy is built from.
func TestFixPolicyRefusesGitAndSirdarDirectories(t *testing.T) {
	root := t.TempDir()
	p := FixPolicy(root, nil, nil, nil)

	for _, path := range []string{
		".git/hooks/pre-commit",
		".git/config",
		filepath.Join(root, ".git", "hooks", "post-checkout"),
		"vendor/thing/.git/hooks/pre-commit",
		"./.git/hooks/pre-push",
		".sirdar/config.yaml",
		".sirdar/runs/OMNI-1/state.json",
		filepath.Join(root, ".sirdar", "playbooks", "50-code.md"),
	} {
		for _, tool := range []string{"Edit", "Write", "MultiEdit", "write_file", "edit_file"} {
			d := p.Decide(tool, writeCall(tool, path))
			if d.Allow {
				t.Errorf("%s was allowed to write %s", tool, path)
			}
		}
	}
	// A file whose name merely starts the same way is ordinary source.
	for _, path := range []string{".gitignore", ".github/workflows/ci.yml", ".sirdarish/notes.md"} {
		if d := p.Decide("Write", writeCall("Write", path)); !d.Allow {
			t.Errorf("Write was refused an ordinary file %s: %s", path, d.Message)
		}
	}
}

// TestFixPolicyWithNoRootRefusesEveryWrite: a policy that cannot say where
// the workspace is cannot confine anything, and fails closed.
func TestFixPolicyWithNoRootRefusesEveryWrite(t *testing.T) {
	p := FixPolicy("", nil, nil, nil)
	for _, tool := range []string{"Edit", "Write", "MultiEdit", "write_file", "edit_file"} {
		if d := p.Decide(tool, writeCall(tool, "main.go")); d.Allow {
			t.Errorf("%s was allowed with no workspace root", tool)
		}
	}
}

// TestFixPolicyStillRefusesEverythingElse guards the half of the rule that
// is easy to lose: "write-enabled" is two named tools wider than read-only,
// not open season.
func TestFixPolicyStillRefusesEverythingElse(t *testing.T) {
	fix := FixPolicy("/work", []string{"git *"}, nil, nil)

	if d := fix.Decide("NotebookEdit", nil); d.Allow {
		t.Error("fix policy allowed NotebookEdit; nothing in the flow edits a notebook")
	}
	if d := fix.Decide("SomeNewTool", nil); d.Allow {
		t.Error("fix policy allowed an unknown tool")
	}
	if d := fix.Decide("Read", nil); !d.Allow {
		t.Errorf("fix policy denied Read: %s", d.Message)
	}
}

// TestFixPolicyUsesItsOwnBashList: a fix's shell allow-list is
// permissions.fixBash, so widening it for builds does not widen what a
// triage run may do.
func TestFixPolicyUsesItsOwnBashList(t *testing.T) {
	fix := FixPolicy("/work", []string{"go test*", "git *"}, nil, nil)

	bash := func(cmd string) Decision {
		in, _ := json.Marshal(map[string]string{"command": cmd})
		return fix.Decide("Bash", in)
	}
	if d := bash("go test ./..."); !d.Allow {
		t.Errorf("fix policy denied an allow-listed build command: %s", d.Message)
	}
	if d := bash("rm -rf /"); d.Allow {
		t.Error("fix policy allowed a command outside its list")
	}
	if d := bash("git show > /tmp/out"); d.Allow {
		t.Error("fix policy allowed a redirection it cannot see through")
	}
}

func TestModeIsFix(t *testing.T) {
	if !ModeFix.IsFix() {
		t.Error("ModeFix.IsFix() is false")
	}
	for _, m := range []Mode{"", ModeTriage} {
		if m.IsFix() {
			t.Errorf("%q.IsFix() is true; the zero value must mean triage", m)
		}
	}
	var nilPolicy *PermissionPolicy
	if nilPolicy.IsFix() {
		t.Error("a nil policy reported itself as a fix policy")
	}
}

// TestFixPolicyFoldsCaseForReservedDirectories is the macOS half of the
// rule, checked on the real filesystem the test is running on. APFS and
// NTFS are case-insensitive by default, so "<root>/.GIT/hooks/pre-commit"
// and "<root>/.git/hooks/pre-commit" are the same file — and a
// case-sensitive segment comparison refused the second and approved the
// first. The directories are really created, so on a case-insensitive
// filesystem the path resolves to an existing .git and the refusal has to
// come from the fold rather than from the path not existing.
func TestFixPolicyFoldsCaseForReservedDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".git/hooks", ".sirdar/playbooks"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := FixPolicy(root, nil, nil, nil)

	for _, path := range []string{
		".GIT/hooks/pre-commit",
		".Git/hooks/pre-commit",
		".giT/config",
		filepath.Join(root, ".GIT", "hooks", "pre-push"),
		"vendor/thing/.Git/hooks/pre-commit",
		".SIRDAR/config.yaml",
		".Sirdar/config.yaml",
		filepath.Join(root, ".SIRDAR", "playbooks", "50-code.md"),
	} {
		for _, tool := range []string{"Edit", "Write", "MultiEdit", "write_file", "edit_file"} {
			if d := p.Decide(tool, writeCall(tool, path)); d.Allow {
				t.Errorf("%s was allowed to write %s", tool, path)
			}
		}
	}
}

// TestFixPolicyReservesTheRepositoryHooksPath: a repository with husky,
// lefthook or a checked-in .githooks/ sets core.hooksPath, and git then
// runs code from an ordinary source directory that the .git rule never
// sees. The run reserves that directory for itself, case folded like the
// two constants.
func TestFixPolicyReservesTheRepositoryHooksPath(t *testing.T) {
	root := t.TempDir()
	hooks := filepath.Join(root, ".husky")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	p := FixPolicy(root, nil, nil, []string{hooks})

	for _, path := range []string{
		".husky/pre-commit",
		".husky/_/husky.sh",
		".HUSKY/pre-push",
		"./.husky/pre-commit",
		hooks,
		filepath.Join(hooks, "pre-commit"),
	} {
		for _, tool := range []string{"Edit", "Write", "MultiEdit", "write_file", "edit_file"} {
			d := p.Decide(tool, writeCall(tool, path))
			if d.Allow {
				t.Errorf("%s was allowed to write the hooks directory entry %s", tool, path)
			}
			if !strings.Contains(d.Message, ".husky") {
				t.Errorf("the refusal for %s does not name the reserved directory: %s", path, d.Message)
			}
		}
	}
	// A relative entry in the reserved list means the same directory.
	rel := FixPolicy(root, nil, nil, []string{".husky"})
	if d := rel.Decide("Write", writeCall("Write", ".husky/pre-commit")); d.Allow {
		t.Error("a relative reserved entry did not reserve the directory")
	}
	// Neighbours with the same prefix are ordinary source.
	for _, path := range []string{".huskyrc", ".husky-notes/readme.md", "husky/pre-commit"} {
		if d := p.Decide("Write", writeCall("Write", path)); !d.Allow {
			t.Errorf("Write was refused an ordinary file %s: %s", path, d.Message)
		}
	}
	// Without the extra list, the same directory is ordinary source: the
	// reservation is per run, not a new constant.
	plain := FixPolicy(root, nil, nil, nil)
	if d := plain.Decide("Write", writeCall("Write", ".husky/pre-commit")); !d.Allow {
		t.Errorf("a run that reserved nothing still refused .husky: %s", d.Message)
	}
}

// TestGitFlagsAreDeniedWhateverThePatternSays covers the hole under the
// allow-list: a pattern approves the words of a command, and git has flags
// that change where it reads configuration, writes output and runs code
// from. `git -c core.hooksPath=/tmp/h status` matches "git status*" style
// patterns and installs a hook directory; `git diff --output=/path` matches
// "git diff*" and writes a file anywhere on the machine.
func TestGitFlagsAreDeniedWhateverThePatternSays(t *testing.T) {
	root := t.TempDir()
	allow := []string{"git status*", "git diff*", "git log*", "git grep*", "git *", "go test*"}

	denied := []string{
		"git -c core.hooksPath=/tmp/hooks status",
		"git -c core.hooksPath=.husky status",
		"git -C /etc log",
		"git --git-dir=/tmp/other/.git log",
		"git --work-tree=/tmp/other status",
		"git --exec-path=/tmp/bin status",
		"git diff --output=" + filepath.Join(root, "out.diff"),
		"git diff --output " + filepath.Join(root, "out.diff"),
		"git diff -o " + filepath.Join(root, "out.diff"),
		"git format-patch --output-directory=/tmp/p HEAD~1",
		"git config --get core.hooksPath",
		"git config core.hooksPath .husky",
		"go test ./... | git config --global alias.x '!sh'",
	}
	for _, cmd := range denied {
		if ok, _ := MatchCommand(root, allow, cmd); ok {
			t.Errorf("MatchCommand allowed %q", cmd)
		}
	}

	allowed := []string{
		"git status --porcelain",
		"git diff --stat",
		"git diff --cached HEAD~1",
		"git log --pretty=format:%H --max-count=5",
		"git log --since=2 days ago",
		"git grep -n foo internal/",
		"git show HEAD:go.mod",
	}
	for _, cmd := range allowed {
		if ok, reason := MatchCommand(root, allow, cmd); !ok {
			t.Errorf("MatchCommand refused a read-only git command %q: %s", cmd, reason)
		}
	}
}

// TestFlagValuesAreCheckedAgainstTheRoot: escapesRoot used to skip every
// token starting with "-", so a path riding in on the same token as its
// flag was never looked at.
func TestFlagValuesAreCheckedAgainstTheRoot(t *testing.T) {
	root := t.TempDir()
	allow := []string{"go test*", "make *", "npm test*"}

	for _, cmd := range []string{
		"go test --coverprofile=/Users/you/.zshrc ./...",
		"go test --coverprofile=~/.zshrc ./...",
		"go test --coverprofile=../../escape.out ./...",
		"npm test --prefix=/etc",
	} {
		if ok, _ := MatchCommand(root, allow, cmd); ok {
			t.Errorf("MatchCommand allowed %q", cmd)
		}
	}
	for _, cmd := range []string{
		"go test -run=TestThing ./...",
		"go test --coverprofile=cover.out ./...",
		"go test --coverprofile=" + filepath.Join(root, "cover.out") + " ./...",
		"make -j4",
	} {
		if ok, reason := MatchCommand(root, allow, cmd); !ok {
			t.Errorf("MatchCommand refused %q: %s", cmd, reason)
		}
	}
}

// TestEscapesRootConsultsReservedWrite: escapesRoot used to check only
// whether a path argument landed inside the workspace root, not whether it
// landed inside a reserved directory within it, so `go test
// -coverprofile=.git/hooks/pre-commit` matched a `go test*` pattern and
// wrote a hook the next commit runs, and a build's `-o` output directory
// could land inside the run's own reserved core.hooksPath.
func TestEscapesRootConsultsReservedWrite(t *testing.T) {
	root := t.TempDir()
	allow := []string{"go test*", "dotnet build*"}

	if ok, reason := MatchCommand(root, allow, "go test -coverprofile=.git/hooks/pre-commit ./..."); ok {
		t.Error("MatchCommand allowed a coverage profile written into .git/hooks")
	} else if !strings.Contains(reason, ".git/") {
		t.Errorf("the refusal does not name the reserved directory: %s", reason)
	}

	// The same rule reaches an absolute path argument, not only a relative
	// one.
	abs := filepath.Join(root, ".git", "hooks", "pre-commit")
	if ok, _ := MatchCommand(root, allow, "go test -coverprofile="+abs+" ./..."); ok {
		t.Error("MatchCommand allowed an absolute coverage-profile path inside .git/hooks")
	}

	// A directory this run reserved on top of .git and .sirdar — the
	// repository's own core.hooksPath — is refused the same way once it is
	// passed as MatchCommand's extra-reserved list.
	if ok, reason := MatchCommand(root, allow, "dotnet build -o .githooks", ".githooks"); ok {
		t.Error("MatchCommand allowed a build output directory inside the reserved hooks path")
	} else if !strings.Contains(reason, ".githooks") {
		t.Errorf("the refusal does not name the reserved directory: %s", reason)
	}

	// Without that reservation the same directory is an ordinary build
	// output: the reservation is per run, not a standing rule.
	if ok, reason := MatchCommand(root, allow, "dotnet build -o .githooks"); !ok {
		t.Errorf("MatchCommand refused an ordinary output directory once nothing reserved it: %s", reason)
	}

	// An ordinary path elsewhere in the workspace is unaffected.
	if ok, reason := MatchCommand(root, allow, "go test -coverprofile=cover.out ./..."); !ok {
		t.Errorf("MatchCommand refused an ordinary coverage-profile path: %s", reason)
	}
}

// TestGitDenialAddsConfigEnvUploadPackAndReceivePack covers the three
// flags round 3 adds to the denied set: --config-env reads a config value
// out of a caller-named environment variable, and --upload-pack /
// --receive-pack run an arbitrary program in place of git's own, on
// `fetch`/`clone` and `push` respectively.
func TestGitDenialAddsConfigEnvUploadPackAndReceivePack(t *testing.T) {
	for _, cmd := range []string{
		"git --config-env=user.name=SOME_ENV status",
		"git fetch --upload-pack=/tmp/evil",
		"git push --receive-pack=/tmp/evil",
		"git clone --upload-pack=/tmp/evil https://example.com/x.git",
	} {
		if denial := gitDenial(cmd); denial == "" {
			t.Errorf("gitDenial(%q) = \"\", want a refusal", cmd)
		}
	}
}

// TestGitDenialCatchesEnvAssignmentsBeforeGit: GIT_DIR, GIT_WORK_TREE and
// the rest of git's own environment variables move the same configuration
// a denied -c/--git-dir flag would, whether they arrive as a bare
// `NAME=value` prefix, through an explicit `env`, or ahead of a command
// that is not literally "git" (a Makefile target invokes git internally
// too).
func TestGitDenialCatchesEnvAssignmentsBeforeGit(t *testing.T) {
	denied := []string{
		"GIT_DIR=/tmp/other/.git git log",
		"env GIT_DIR=/tmp/other/.git git log",
		"env GIT_WORK_TREE=/tmp/other git status",
		"GIT_CONFIG=/tmp/x git log",
		"GIT_DIR=/tmp/other/.git make test",
	}
	for _, cmd := range denied {
		denial := gitDenial(cmd)
		if denial == "" {
			t.Errorf("gitDenial(%q) = \"\", want a refusal", cmd)
			continue
		}
		if !strings.Contains(denial, "GIT_") {
			t.Errorf("gitDenial(%q) = %q, does not name the environment variable", cmd, denial)
		}
	}

	// An assignment that does not name a GIT_* variable, and a bare `env`
	// with no assignment at all, are not refused by this rule; the command
	// underneath is still judged normally by everything else in gitDenial.
	for _, cmd := range []string{"LANG=C git log", "env git log", "FOO=bar make test"} {
		if denial := gitDenial(cmd); denial != "" {
			t.Errorf("gitDenial(%q) = %q, want no refusal", cmd, denial)
		}
	}
}

// TestGitFlagsArePositionScoped is round 3's item (d): -c, -C, --git-dir,
// --work-tree, --exec-path and --config-env are valid only before the git
// subcommand, so denying them only there recovers the read-only uses that
// reuse the same short flag after the subcommand for an unrelated meaning
// — `git grep -c` counts matches, `git rev-parse --git-dir` prints a path
// — while -o/--output* stay denied wherever they fall, because git accepts
// those after the subcommand too.
func TestGitFlagsArePositionScoped(t *testing.T) {
	root := t.TempDir()
	allow := []string{"git *"}

	for _, cmd := range []string{"git grep -c foo", "git rev-parse --git-dir"} {
		if ok, reason := MatchCommand(root, allow, cmd); !ok {
			t.Errorf("MatchCommand refused %q: %s", cmd, reason)
		}
	}
	for _, cmd := range []string{"git -c core.hooksPath=x status", "git diff --output=x"} {
		if ok, _ := MatchCommand(root, allow, cmd); ok {
			t.Errorf("MatchCommand allowed %q", cmd)
		}
	}
}

// TestHooksPathReadsTheRepositoryConfiguration is the per-run half of the
// reservation: where the directory comes from.
func TestHooksPathReadsTheRepositoryConfiguration(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)

	if p := HooksPath(t.Context(), root); p != "" {
		t.Errorf("HooksPath on a repository that sets none = %q, want \"\"", p)
	}
	if got, want := HooksDir(t.Context(), root), filepath.Join(root, ".git", "hooks"); got != want {
		t.Errorf("HooksDir = %q, want %q", got, want)
	}

	gitRun(t, root, "config", "core.hooksPath", ".husky")
	if got, want := HooksPath(t.Context(), root), filepath.Join(root, ".husky"); got != want {
		t.Errorf("HooksPath = %q, want %q", got, want)
	}
	if got, want := HooksDir(t.Context(), root), filepath.Join(root, ".husky"); got != want {
		t.Errorf("HooksDir = %q, want %q", got, want)
	}

	// An absolute value is taken as it stands.
	outside := t.TempDir()
	gitRun(t, root, "config", "core.hooksPath", outside)
	if got := HooksPath(t.Context(), root); got != outside {
		t.Errorf("HooksPath = %q, want %q", got, outside)
	}

	// Somewhere that is not a repository at all answers with nothing
	// rather than failing the run.
	if p := HooksPath(t.Context(), t.TempDir()); p != "" {
		t.Errorf("HooksPath outside a repository = %q, want \"\"", p)
	}
}

// TestHooksPathExpandsTilde: git tilde-expands a path-type config value
// itself, so `core.hooksPath = ~/x` names a directory under the caller's
// home, not a literal "~x" entry inside the workspace. Before round 3,
// HooksPath's IsAbs check saw "~/x", judged it relative, and joined it onto
// root — the reservation and the hooks snapshot then watched a path that
// does not exist while the real hooks directory went unwatched.
func TestHooksPathExpandsTilde(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)

	home := t.TempDir()
	t.Setenv("HOME", home)

	gitRun(t, root, "config", "core.hooksPath", "~/x")
	want := filepath.Join(home, "x")
	if got := HooksPath(t.Context(), root); got != want {
		t.Errorf(`HooksPath with core.hooksPath="~/x" = %q, want %q`, got, want)
	}
	if got := HooksDir(t.Context(), root); got != want {
		t.Errorf(`HooksDir with core.hooksPath="~/x" = %q, want %q`, got, want)
	}

	// A bare "~" names the home directory itself.
	gitRun(t, root, "config", "core.hooksPath", "~")
	if got := HooksPath(t.Context(), root); got != home {
		t.Errorf(`HooksPath with core.hooksPath="~" = %q, want %q`, got, home)
	}
}

// TestExpandHomeHandlesTildeUser covers the "~user" form ("core.hooksPath
// = ~alice/x"), which is not the caller's own home and so is not reached
// by setting $HOME.
func TestExpandHomeHandlesTildeUser(t *testing.T) {
	cur, err := user.Current()
	if err != nil {
		t.Skipf("cannot look up the current user: %v", err)
	}
	value := "~" + cur.Username + "/sub"
	got, ok := expandHome(value)
	if !ok {
		t.Fatalf("expandHome(%q) did not expand", value)
	}
	if want := filepath.Join(cur.HomeDir, "sub"); got != want {
		t.Errorf("expandHome(%q) = %q, want %q", value, got, want)
	}

	if _, ok := expandHome("relative/path"); ok {
		t.Error("expandHome expanded a value with no leading ~")
	}
	if got, ok := expandHome("~sirdar-nonexistent-user-12345"); ok {
		t.Errorf("expandHome expanded an unknown user to %q", got)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	gitRun(t, dir, "init", "-q")
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}
