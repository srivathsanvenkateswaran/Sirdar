package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
)

// intakeTickets is a tracker synced from a helpdesk: the issue is titled
// with the helpdesk number, and the helpdesk record does not name the key.
const intakeTickets = `{
  "tracker": {"SBX-1": {"Key":"SBX-1","Title":"#28310 Export fails","Status":"open","URL":"https://t/SBX-1","HelpdeskRef":"900001"}},
  "helpdesk": {"900001": {
    "ticket": {"ID":"900001","Subject":"تصدير","Customer":"شركة","URL":"https://h/900001"},
    "thread": [{"At":"2026-09-10T08:30:00+03:00","Author":"Customer","Role":"customer","Text":"لا يعمل التصدير"}],
    "attachments": []
  }}
}`

func newIntakeWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	tickets := filepath.Join(t.TempDir(), "tickets.json")
	if err := os.WriteFile(tickets, []byte(intakeTickets), 0o644); err != nil {
		t.Fatal(err)
	}
	command := fileAdapter + " -file " + tickets
	body := fmt.Sprintf(`workspace: sirdar-test
provider: claude
sources:
  tracker:
    adapter: exec
    command: %s
  helpdesk:
    adapter: exec
    command: %s
notes:
  dir: %s
playbooks: .sirdar/playbooks
providers:
  claude:
    path: %s
`, command, command, filepath.Join(t.TempDir(), "notes"), fakeClaude)
	if err := os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prompt.ScaffoldPlaybooks(filepath.Join(root, ".sirdar", "playbooks")); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestTriageTakesAHelpdeskNumber is `sirdar triage #28310`: the number is
// resolved to the tracker key, the resolution is printed, and the run is
// filed under the key.
func TestTriageTakesAHelpdeskNumber(t *testing.T) {
	root := newIntakeWorkspace(t)
	chdir(t, root)

	_, stderr := mustRun(t, 0, "triage", "--dry-run", "#28310")
	if !strings.Contains(stderr, "#28310 → SBX-1 · matched by title") {
		t.Errorf("the resolution was not printed:\n%s", stderr)
	}
	if prompts, _ := filepath.Glob(filepath.Join(root, ".sirdar", "runs", "SBX-1", "*", "prompt.md")); len(prompts) != 1 {
		t.Fatalf("want one SBX-1 prompt, got %v", prompts)
	}
}

func TestTriageRefusesWhatResolvesToNothing(t *testing.T) {
	root := newIntakeWorkspace(t)
	chdir(t, root)

	_, stderr := mustRun(t, 1, "triage", "--dry-run", "#99999")
	if !strings.Contains(stderr, "no tracker issue names #99999") {
		t.Errorf("stderr:\n%s", stderr)
	}
	_, stderr = mustRun(t, 1, "rca", "https://acme.slack.com/archives/C0123ABCD/p1712345678901234")
	if !strings.Contains(stderr, app.SlackNotConfigured) {
		t.Errorf("stderr:\n%s", stderr)
	}
	if runs, _ := filepath.Glob(filepath.Join(root, ".sirdar", "runs", "*", "*")); len(runs) != 0 {
		t.Fatalf("nothing should have run: %v", runs)
	}
}
