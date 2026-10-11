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
	p.BashAllow = []string{"rg *", "grep *", "find *", "ls *", "git log*", "git show*", "head *", "wc *", "sort *", "go test*"}
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
		// The 2026-10-11 OMNI-3420 rerun: a path right after a bare flag
		// (`-l`, `-A6`) was read as that flag's value and judged as a place
		// to write, so a read of the companion was refused.
		"wc -l " + filepath.Join(companion, "src") + "/*",
		"ls " + filepath.Join(companion, "src") + "/; wc -l " + filepath.Join(companion, "src") + "/*",
		"rg -n select -A6 " + filepath.Join(companion, "src", "app.html"),
		"grep -rn root -- " + filepath.Join(companion, "src"),
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
		// The same with the value as its own token: a flag that names an
		// output or a program never takes a read path, however it is spelled.
		"rg --files x --pre " + filepath.Join(companion, "x.sh"),
		"sort -o " + filepath.Join(companion, "out.txt") + " x",
		"git log --output " + filepath.Join(companion, "log.txt"),
		"rg -n x --log-file " + filepath.Join(companion, "rg.log"),
		// find's own writing actions write wherever they point, the
		// workspace included, so no pattern approves them.
		"find " + filepath.Join(companion, "src") + " -fprint " + filepath.Join(companion, "list.txt"),
		"find . -fprintf out.txt %p",
		"find . -fls out.txt",
		"find . -fprint0 out.txt",
		// A bare flag this list does not know still cannot name a reserved
		// directory: the run may read .git, but a value there may be written.
		"go test -coverprofile .git/hooks/pre-commit ./...",
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
