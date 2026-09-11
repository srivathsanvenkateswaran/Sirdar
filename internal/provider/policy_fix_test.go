package provider

import (
	"encoding/json"
	"os"
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
	fix := FixPolicy(root, []string{"git log*", "go test*"}, nil)
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
	p := FixPolicy(root, nil, nil)

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
	p := FixPolicy(root, nil, nil)

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
	p := FixPolicy("", nil, nil)
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
	fix := FixPolicy("/work", []string{"git *"}, nil)

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
	fix := FixPolicy("/work", []string{"go test*", "git *"}, nil)

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
