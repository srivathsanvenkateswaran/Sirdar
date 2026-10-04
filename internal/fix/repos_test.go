package fix

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// TestAFixInACompanionRepositoryIsRefused pins the one rule repos: adds to
// the fix flow: a note whose proposed fix names files in a companion
// repository is refused before a branch or a worktree exists, with the
// repository to run Sirdar from.
func TestAFixInACompanionRepositoryIsRefused(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	w.cfg.Repos = []config.RepoConfig{{Name: "Acme.Web", Path: filepath.Join(t.TempDir(), "Acme.Web")}}
	doc := strings.Replace(triageDocJSON, `"files":["export/csv.go"]`, `"files":["Acme.Web/src/app/cart.ts"]`, 1)
	if doc == triageDocJSON {
		t.Fatal("the fixture's proposedFix.files moved; update this test")
	}
	mustWrite(t, filepath.Join(filepath.Dir(w.runNote), "result.json"), doc)

	p := &stubProvider{report: fixReport, t: t}
	_, err := Run(t.Context(), newDeps(w, p), "OMNI-1", Options{})
	if err == nil || err.Error() != "fix: the fix is in Acme.Web; run Sirdar from that repository" {
		t.Fatalf("error %v", err)
	}
	if got := w.worktrees(t); len(got) != 0 {
		t.Errorf("a refused fix left worktrees %v", got)
	}
	if out := run(t, w.root, "git", "branch", "--list", "sirdar/*"); out != "" {
		t.Errorf("a refused fix left a branch: %s", out)
	}
}
