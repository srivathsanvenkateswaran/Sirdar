package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// writePlaybook files one playbook in the workspace's playbooks directory.
func writePlaybook(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, ".sirdar", "playbooks", name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A run loads the always-on playbooks and the ones whose `when` names a
// word in the ticket, lists the rest with the path to read them from, and
// says on its event log which was which. A playbook whose frontmatter is
// broken loads as if it had none, and the run says why in its warnings.
func TestPromptLoadsScopedPlaybooks(t *testing.T) {
	cfg := newWorkspace(t)
	writePlaybook(t, cfg.Root, "10-always.md", "# Always\n\nRead this every time.\n")
	writePlaybook(t, cfg.Root, "20-logs.md", "---\nwhen: [error]\n---\n# Logs\n\nQuery the error logs first.\n")
	apm := writePlaybook(t, cfg.Root, "30-apm.md", "---\nwhen: [slow]\n---\n# APM\n\nRead the traces for slow requests.\n")
	writePlaybook(t, cfg.Root, "40-bad.md", "---\nkinds: [nope]\n---\n# Bad\n\nA playbook with a typo in its kinds.\n")

	stub := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, stub, descTracker{description: "an error on save"}, stubHelpdesk{})
	out := triageOne(t, r, Options{NoteOnly: true})
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	dir := runDir(t, cfg, out)

	text := readFile(t, filepath.Join(dir, "prompt.md"))
	for _, want := range []string{"## 00-test", "## 10-always", "## 20-logs", "## 40-bad", "## Other playbooks"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt.md is missing %q", want)
		}
	}
	if strings.Contains(text, "## 30-apm") {
		t.Error("prompt.md loaded 30-apm, whose keyword the ticket never names")
	}
	abs, err := filepath.Abs(apm)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "- 30-apm") || !strings.Contains(text, "`"+abs+"`") {
		t.Errorf("prompt.md does not list 30-apm with its path %s:\n%s", abs, text)
	}

	if !hasLine(t, dir, `"text":"Playbooks: loaded 00-test, 10-always, 20-logs, 40-bad; listed 30-apm"`) {
		t.Errorf("events.jsonl has no playbooks line:\n%s", strings.Join(eventLogLines(t, dir), "\n"))
	}
	if !contains(out.State.Warnings, `playbook 40-bad: frontmatter ignored: unknown kind "nope"`) {
		t.Errorf("warnings %q do not report 40-bad's frontmatter", out.State.Warnings)
	}
}

// A workspace whose playbooks carry no frontmatter gets the prompt it got
// before playbooks could be scoped: every playbook in full and no listing.
func TestWorkspaceWithoutFrontmatterPromptUnchanged(t *testing.T) {
	cfg := newWorkspace(t)
	writePlaybook(t, cfg.Root, "10-more.md", "# More\n\nMORE-MARKER: a second playbook.\n")

	stub := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, stub, stubTracker{}, stubHelpdesk{})
	out := triageOne(t, r, Options{})
	text := readFile(t, filepath.Join(runDir(t, cfg, out), "prompt.md"))
	if strings.Contains(text, "## Other playbooks") {
		t.Errorf("prompt.md lists playbooks although none is scoped:\n%s", text)
	}
	for _, want := range []string{"## 00-test\n\n# Exports\n\n" + playbookMarker, "## 10-more\n\n# More\n\nMORE-MARKER: a second playbook."} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt.md does not carry %q in full:\n%s", want, text)
		}
	}
}
