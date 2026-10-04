package provider

import (
	"os"
	"path/filepath"
	"testing"
)

// companionPolicy is a read-only policy whose read scope takes in one
// companion repository beside the workspace, as permissions.readAlso or
// repos: would.
func companionPolicy(t *testing.T) (p *PermissionPolicy, companion, elsewhere string) {
	t.Helper()
	p, root, _ := readPolicy(t)
	parent := filepath.Dir(root)
	companion = filepath.Join(parent, "Companion.Web")
	elsewhere = filepath.Join(parent, "Unlisted")
	for _, d := range []string{filepath.Join(companion, "src"), elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p.ReadAlso = []string{companion}
	p.BashAllow = []string{"rg *", "grep *", "find *", "ls *", "git log*", "git show*", "head *"}
	return p, companion, elsewhere
}

// The 2026-10-04 OMNI-3413 rerun: OXO.Systems was in the read scope, Read
// reached it, and every one of these was refused for naming a path outside
// the workspace root.
func TestReadOnlyBashReachesACompanionRepository(t *testing.T) {
	p, companion, _ := companionPolicy(t)
	rel, err := filepath.Rel(p.Root, companion)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{
		"rg -il barcode " + filepath.Join(companion, "src") + " --glob '*.ts'",
		"grep -n root " + filepath.Join(companion, "angular.json"),
		"find " + filepath.Join(companion, "src") + " -type d -iname '*open*'",
		"ls " + companion,
		"rg -l select " + filepath.Join(companion, "src") + " | head -20",
		"git -C " + companion + " log --oneline -5 -- src/app/components/select-search",
		"git -C " + companion + " show HEAD~1 --stat",
		"rg -n x " + rel + "/src",
	} {
		if d := p.Decide("Bash", bashInput(c)); !d.Allow {
			t.Errorf("refused %q: %s", c, d.Message)
		}
	}
}

func TestBashStillStopsAtTheReadScope(t *testing.T) {
	p, companion, elsewhere := companionPolicy(t)
	for _, c := range []string{
		"rg -n x " + elsewhere,
		"git -C " + elsewhere + " log",
		"ls /etc",
		// Allow-listed command, companion path, but not an allowed subcommand.
		"git -C " + companion + " push",
		// A flag's value is where a command may write; it stays in the workspace.
		"rg --files x --pre=" + filepath.Join(companion, "x.sh"),
	} {
		if d := p.Decide("Bash", bashInput(c)); d.Allow {
			t.Errorf("allowed %q", c)
		}
	}
}

func TestFixBashStaysInTheWorktree(t *testing.T) {
	p, companion, _ := companionPolicy(t)
	p.Mode = ModeFix
	for _, c := range []string{"rg -n x " + companion, "git -C " + companion + " log"} {
		if d := p.Decide("Bash", bashInput(c)); d.Allow {
			t.Errorf("fix run allowed %q", c)
		}
	}
}
