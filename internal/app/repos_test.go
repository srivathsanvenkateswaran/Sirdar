package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// gitIn runs one git command in a throwaway repository the test made. Its
// commits are made with the machine's own identity as it stands; the test
// skips on a machine with none rather than inventing one.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestReposDoctorRowAndSummary pins the doctor's repos row and the Settings
// list: each companion by name with its branch, how far behind its last
// fetch left it, and a missing clone as a warning rather than a failure.
func TestReposDoctorRowAndSummary(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	upstream := t.TempDir()
	gitIn(t, upstream, "init", "-q", "-b", "main")
	if err := exec.Command("git", "-C", upstream, "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
		t.Skip("git has no author identity configured in this environment")
	}
	gitIn(t, upstream, "commit", "-q", "--allow-empty", "-m", "one")
	web := filepath.Join(t.TempDir(), "Acme.Web")
	gitIn(t, filepath.Dir(web), "clone", "-q", upstream, web)
	gitIn(t, upstream, "commit", "-q", "--allow-empty", "-m", "two")
	gitIn(t, web, "fetch", "-q")
	fetched := time.Now()

	cfg := &config.Config{Root: t.TempDir(), Workspace: "acme-api", Repos: []config.RepoConfig{
		{Name: "Acme.Web", Path: web, About: "web frontend"},
		{Name: "reports", Path: filepath.Join(t.TempDir(), "missing")},
	}}
	check, ok := reposCheck(context.Background(), cfg, fetched.Add(2*time.Hour))
	if !ok {
		t.Fatal("no repos row for a workspace with repos:")
	}
	want := "Acme.Web: main · 1 behind origin/main · fetched 2 hours ago; reports: not found; a session cannot read reports until its path exists"
	if check.Name != "repos" || check.Detail != want || check.Level != string(provider.LevelWarn) || !check.OK {
		t.Fatalf("check %+v\nwant detail %q", check, want)
	}

	sum := summariseRepos(context.Background(), cfg, fetched)
	if len(sum) != 3 || !sum[0].Workspace || sum[0].Name != "acme-api" {
		t.Fatalf("summary %+v", sum)
	}
	if sum[1].Name != "Acme.Web" || sum[1].About != "web frontend" || sum[1].Origin != upstream || sum[1].Facts.Behind != 1 || !strings.HasPrefix(sum[1].State, "main · 1 behind") {
		t.Errorf("companion %+v", sum[1])
	}
	if sum[2].Facts.Exists || sum[2].State != "not found" {
		t.Errorf("missing companion %+v", sum[2])
	}

	if _, ok := reposCheck(context.Background(), &config.Config{Root: t.TempDir()}, time.Now()); ok {
		t.Error("a repos row for a workspace with no repos:")
	}
	if s := SummariseConfig(&config.Config{Root: t.TempDir()}); s.Repos == nil {
		t.Error("summary repos is null")
	}
}
