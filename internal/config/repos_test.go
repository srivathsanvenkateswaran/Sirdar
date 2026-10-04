package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// gitInit makes dir a throwaway repository with an origin remote. Nothing
// is fetched or pushed; the remote only has to be named.
func gitInit(t *testing.T, dir, origin string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", origin}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestReposLoad(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	web := filepath.Join(home, "code", "Acme.Web")
	if err := os.MkdirAll(web, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, web, "git@github.com:acme/Acme.Web.git")
	plain := filepath.ToSlash(filepath.Join(t.TempDir(), "docs"))
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	root := writeCfg(t, minimal+`
repos:
  - name: Acme.Web
    path: ~/code/Acme.Web
    about: web frontend
  - name: handbook
    path: "`+plain+`"
    origin: https://github.com/acme/handbook
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	list := cfg.Repositories()
	if len(list) != 3 {
		t.Fatalf("repositories %+v", list)
	}
	if !list[0].Workspace || list[0].Name != "demo" || list[0].Path != root {
		t.Errorf("workspace repository %+v", list[0])
	}
	if list[1].Name != "Acme.Web" || list[1].Path != web || list[1].Origin != "git@github.com:acme/Acme.Web.git" || list[1].About != "web frontend" {
		t.Errorf("companion read from its clone %+v", list[1])
	}
	if list[2].Origin != "https://github.com/acme/handbook" || list[2].Path != filepath.Clean(plain) {
		t.Errorf("plain directory with a configured origin %+v", list[2])
	}

	// Both companions join the read scope beside readAlso.
	policy := &provider.PermissionPolicy{Root: cfg.Root, ReadAlso: cfg.ReadAlso()}
	for _, p := range []string{filepath.Join(web, "src", "app.ts"), plain + "/guide.md"} {
		in, _ := json.Marshal(map[string]string{"file_path": p})
		if d := policy.Decide("Read", in); !d.Allow {
			t.Errorf("a companion path was denied: %s: %s", p, d.Message)
		}
	}
	if d := policy.Decide("Read", json.RawMessage(`{"file_path":"`+filepath.ToSlash(filepath.Join(home, "code", "other.txt"))+`"}`)); d.Allow {
		t.Error("a sibling of a companion was allowed")
	}
}

func TestReposValidate(t *testing.T) {
	cases := map[string]string{
		"repos:\n  - path: /src/web\n":                                           "repos[0].name: is required",
		"repos:\n  - name: web\n":                                                "repos[0].path: is required",
		"repos:\n  - name: web\n    path: src/web\n":                             "must be an absolute path or start with ~",
		"repos:\n  - name: acme/web\n    path: /src/web\n":                       "may not contain",
		"repos:\n  - name: web\n    path: /a\n  - name: WEB\n    path: /b\n":     `"WEB" is already repos[0]`,
		"repos:\n  - name: web\n    path: /a\n    origin: not a remote\n":        "repos[0].origin",
		"repos:\n  - name: web\n    path: /a\n    url: https://github.com/a/b\n": "field url not found",
	}
	for body, want := range cases {
		_, err := Load(writeCfg(t, minimal+body))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want one containing %q", body, err, want)
		}
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := minimal + "repos:\n  - name: self\n    path: \"" + filepath.ToSlash(root) + "\"\n"
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "is the workspace itself") {
		t.Errorf("the workspace as its own companion: %v", err)
	}

	bare, err := Load(writeCfg(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if got := bare.ReadAlso(); len(got) != 0 {
		t.Errorf("ReadAlso with no repos and no readAlso = %v", got)
	}
}
