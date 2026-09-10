package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755)
	os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644)
	return root
}

const minimal = `
workspace: demo
sources:
  helpdesk:
    adapter: zohodesk
    orgId: "1"
    baseUrl: https://desk.example
    token: env:ZOHO
`

func TestLoadAppliesDefaults(t *testing.T) {
	c, err := Load(writeCfg(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "claude" || c.Billing != "subscription" || c.Concurrency != 1 {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if c.Budget.MaxTurns != 60 || c.Budget.MaxMinutes != 25 || c.Budget.MaxUSD != 5 {
		t.Fatalf("budget defaults: %+v", c.Budget)
	}
	if c.Notes.Filenames.RCA != "{key} RCA {slug}.md" {
		t.Fatalf("filename default %q", c.Notes.Filenames.RCA)
	}
	if c.Playbooks != ".sirdar/playbooks" {
		t.Fatalf("playbooks default %q", c.Playbooks)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"\nbogus: 1\n"))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want unknown-key error, got %v", err)
	}
}

func TestValidateExecNeedsCommand(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"  tracker:\n    adapter: exec\n"))
	if err == nil || !strings.Contains(err.Error(), "sources.tracker.command") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateBadProvider(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"provider: gemini\n"))
	if err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateTokenRef(t *testing.T) {
	bad := strings.Replace(minimal, "env:ZOHO", "plaintext-token", 1)
	_, err := Load(writeCfg(t, bad))
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("got %v", err)
	}
}

func TestFindRoot(t *testing.T) {
	root := writeCfg(t, minimal)
	nested := filepath.Join(root, "a", "b")
	os.MkdirAll(nested, 0o755)
	got, err := FindRoot(nested)
	if err != nil || got != root {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := FindRoot(t.TempDir()); err == nil {
		t.Fatal("expected error outside a workspace")
	}
}

func TestExpandPath(t *testing.T) {
	c := &Config{Root: "/ws"}
	home, _ := os.UserHomeDir()
	if got := c.ExpandPath("~/x"); got != filepath.Join(home, "x") {
		t.Fatal(got)
	}
	if got := c.ExpandPath("rel/y"); got != "/ws/rel/y" {
		t.Fatal(got)
	}
	if got := c.ExpandPath("/abs"); got != "/abs" {
		t.Fatal(got)
	}
}
