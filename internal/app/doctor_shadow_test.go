package app

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// describingTracker is an exec adapter as the doctor sees it: a tracker
// that lists and a describe that names its optional methods.
type describingTracker struct {
	shadowTracker
	methods []string
}

func (d *describingTracker) Describe(ctx context.Context) (plugin.Describe, error) {
	return plugin.Describe{Name: "stub", Roles: []string{"tracker"}, Version: "1", Methods: d.methods}, nil
}

func stubDoctorSources(t *testing.T, tr source.Tracker) {
	t.Helper()
	prev := shadowDoctorSources
	shadowDoctorSources = func(cfg *config.Config, stderr io.Writer) (source.Tracker, source.Helpdesk, func(), error) {
		return tr, nil, func() {}, nil
	}
	t.Cleanup(func() { shadowDoctorSources = prev })
}

func loadShadowConfig(t *testing.T, root, body string) *config.Config {
	t.Helper()
	writeConfig(t, root, body)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestShadowCheckMeUnresolved(t *testing.T) {
	tr := &describingTracker{}
	tr.set(bug("OMNI-1", time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)))
	stubDoctorSources(t, tr)
	// A temp directory is no git repository, so no committer stands in
	// for the missing me: block.
	root := newWorkspace(t)
	cfg := loadShadowConfig(t, root, shadowConfig(5, ""))

	c, ok := shadowCheck(context.Background(), cfg)
	if !ok {
		t.Fatal("no shadow row for a workspace with a tracker")
	}
	if c.Name != "shadow" || c.Level != "fail" || c.OK {
		t.Errorf("check %+v, want a failing shadow row", c)
	}
	if !strings.Contains(c.Detail, "assignee me resolves to nothing; set me: in config.yaml") {
		t.Errorf("detail %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "tracker.outcome unsupported; outcomes come from your runs and manual entries") {
		t.Errorf("detail %q does not say how outcomes arrive", c.Detail)
	}
}

func TestShadowCheckLedgerNotIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	tr := &describingTracker{methods: []string{"tracker.outcome"}}
	tr.set(bug("OMNI-1", time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)), ticket.TrackerTicket{Key: "OMNI-2", Type: "task"})
	stubDoctorSources(t, tr)
	root := newWorkspace(t)
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	cfg := loadShadowConfig(t, root, shadowConfig(5, "")+"me:\n  email: op@example.com\n")

	c, ok := shadowCheck(context.Background(), cfg)
	if !ok {
		t.Fatal("no shadow row")
	}
	if c.Level != "warn" {
		t.Errorf("level %q, want warn: %s", c.Level, c.Detail)
	}
	for _, want := range []string{
		"assignee op@example.com",
		"lists 1 open bug tickets",
		".sirdar/shadow.jsonl is not git-ignored",
		".sirdar/playbook-suggestions.jsonl is not git-ignored",
		"tracker.outcome supported",
	} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q lacks %q", c.Detail, want)
		}
	}
}

func TestShadowCheckNoTracker(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := shadowCheck(context.Background(), cfg); ok {
		t.Errorf("a workspace with no tracker got a shadow row: %+v", c)
	}
}

func TestShadowCheckOff(t *testing.T) {
	tr := &describingTracker{}
	tr.set(bug("OMNI-1", time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)))
	stubDoctorSources(t, tr)
	root := newWorkspace(t)
	body := strings.Replace(shadowConfig(5, ""), "  enabled: true\n", "  enabled: false\n", 1)
	cfg := loadShadowConfig(t, root, body)
	c, ok := shadowCheck(context.Background(), cfg)
	if !ok {
		t.Fatal("no shadow row")
	}
	// Every part is there to check before switching on, and the missing
	// me: block that would fail an enabled row only warns.
	if c.Level != "warn" || !c.OK || !strings.HasPrefix(c.Detail, "off; ") ||
		!strings.Contains(c.Detail, "assignee me resolves to nothing") || !strings.Contains(c.Detail, "lists 1 open bug tickets") {
		t.Errorf("check %+v", c)
	}
}
