package run

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestTriageNamesCompanionRepositories pins what repos: does to a triage:
// the prompt lists the companion with its purpose, names what the Slack
// thread and the operator mentioned, and the session's read scope admits
// the companion's clone.
func TestTriageNamesCompanionRepositories(t *testing.T) {
	web := filepath.ToSlash(filepath.Join(t.TempDir(), "Acme.Web"))
	cfg := newWorkspaceWith(t, configYAML+`
repos:
  - name: Acme.Web
    path: "`+web+`"
    origin: git@github.com:acme/Acme.Web.git
    about: web frontend
`)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	const slackMD = "# Slack thread\n\n## 2026-09-02T09:00:00Z · slack · Sam\n\nbroke after acme/Acme.Web#828 and other/Billing.Service#3\n"
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{Slack: slackMD, Instruction: "look in Acme.Web first"})
	if err != nil {
		t.Fatal(err)
	}
	prompt := readFile(t, filepath.Join(runDir(t, cfg, outs[0]), "prompt.md"))
	for _, want := range []string{
		"# Repositories",
		"- Acme.Web — " + filepath.Clean(web) + " (origin git@github.com:acme/Acme.Web.git): web frontend",
		"- mentions Acme.Web (companion repo)",
		"- mentions Billing.Service (not configured — add it under repos:) — named by other/Billing.Service#3",
		"The operator asked you to look in Acme.Web.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.specs) == 0 || !slices.Contains(p.specs[0].Policy.ReadAlso, filepath.Clean(web)) {
		t.Errorf("the session's read scope does not name the companion: %+v", p.specs)
	}
}
