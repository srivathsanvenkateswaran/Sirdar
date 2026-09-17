package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

func TestSourcesSummaryNamesBuiltInAdapters(t *testing.T) {
	cases := []struct {
		sc         config.SourceConfig
		name, host string
	}{
		{config.SourceConfig{Adapter: "jira", BaseURL: "https://acme.atlassian.net/"}, "Jira", "acme.atlassian.net"},
		{config.SourceConfig{Adapter: "linear"}, "Linear", "api.linear.app"},
		{config.SourceConfig{Adapter: "azdo", OrgURL: "https://dev.azure.com/acme", Project: "Omni"}, "Azure DevOps", "dev.azure.com"},
		{config.SourceConfig{Adapter: "rally"}, "Rally", "rally1.rallydev.com"},
		{config.SourceConfig{Adapter: "servicenow", Instance: "acme"}, "ServiceNow", "acme.service-now.com"},
		{config.SourceConfig{Adapter: "zohodesk"}, "Zoho Desk", "desk.zoho.com"},
		{config.SourceConfig{Adapter: "zohodesk", BaseURL: "https://desk.zoho.eu"}, "Zoho Desk", "desk.zoho.eu"},
		{config.SourceConfig{Adapter: "zendesk", Subdomain: "Acme"}, "Zendesk", "acme.zendesk.com"},
		{config.SourceConfig{Adapter: "freshdesk", Domain: "acme.freshdesk.com"}, "Freshdesk", "acme.freshdesk.com"},
		{config.SourceConfig{Adapter: "helpscout"}, "Help Scout", "api.helpscout.net"},
		{config.SourceConfig{Adapter: "intercom"}, "Intercom", "api.intercom.io"},
		{config.SourceConfig{Adapter: "hubspot"}, "HubSpot", "api.hubapi.com"},
		{config.SourceConfig{Adapter: "front"}, "Front", "api2.frontapp.com"},
		{config.SourceConfig{Adapter: "gorgias", Account: "acme"}, "Gorgias", "acme.gorgias.com"},
	}
	for _, c := range cases {
		sc := c.sc
		got := summariseSource(&sc, "")
		if got == nil || got.Adapter != sc.Adapter || got.Name != c.name || got.Host != c.host {
			t.Errorf("%s: got %+v, want name %q host %q", sc.Adapter, got, c.name, c.host)
		}
	}
	if got := summariseSource(nil, ""); got != nil {
		t.Errorf("unconfigured role = %+v, want nil", got)
	}
}

func TestSourcesSummaryNamesAnExecAdapterFromTheTicketURL(t *testing.T) {
	sc := &config.SourceConfig{Adapter: "exec", Command: "~/bin/sirdar-janus --token env:JANUS_TOKEN"}

	// With a bundle behind it, the ticket URL's host says where and what.
	got := summariseSource(sc, "https://janus.example.com/browse/OMNI-2815")
	if got.Name != "Janus" || got.Host != "janus.example.com" || got.Adapter != "exec" {
		t.Fatalf("exec with URL = %+v", got)
	}

	// Before any run, the command's own name stands in and the host is
	// unknown rather than guessed.
	got = summariseSource(sc, "")
	if got.Name != "Janus" || got.Host != "" {
		t.Fatalf("exec without URL = %+v", got)
	}
	if got := summariseSource(&config.SourceConfig{Adapter: "exec", Command: "/opt/my_desk-bridge.exe"}, ""); got.Name != "My Desk Bridge" {
		t.Fatalf("command name = %q, want title-cased words", got.Name)
	}
}

func TestSourcesSummaryOnTheWire(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sources.Tracker = &config.SourceConfig{Adapter: "jira", BaseURL: "https://acme.atlassian.net"}
	got := SummariseConfig(cfg)

	raw, err := json.Marshal(got.Sources)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"tracker":{"adapter":"jira","name":"Jira","host":"acme.atlassian.net"},"queueTypes":["bug"]}`
	if string(raw) != want {
		t.Fatalf("sources = %s, want %s", raw, want)
	}

	// The configured list reaches the wire resolved, not as it was typed.
	all := []string{"*"}
	cfg.Sources.Tracker.Queue = &config.QueueConfig{Types: &all}
	if got := SummariseConfig(cfg).Sources.QueueTypes; len(got) != 1 || got[0] != "*" {
		t.Fatalf("queueTypes = %v, want [*]", got)
	}
}

func TestSourceHintsReadTheNewestBundle(t *testing.T) {
	root := t.TempDir()
	write := func(key, id, bundle string) {
		dir := filepath.Join(root, ".sirdar", "runs", key, id)
		if err := os.MkdirAll(filepath.Join(dir, "bundle"), 0o755); err != nil {
			t.Fatal(err)
		}
		st := store.State{RunID: id, Key: key}
		raw, _ := json.Marshal(st)
		if err := os.WriteFile(filepath.Join(dir, "state.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if bundle != "" {
			if err := os.WriteFile(filepath.Join(dir, "bundle", "ticket.json"), []byte(bundle), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The newest run has no bundle; the one before it names both URLs.
	write("OMNI-2", "20260916T090000Z-bbbb", "")
	write("OMNI-1", "20260915T090000Z-aaaa", `{"Tracker":{"Key":"OMNI-1","URL":"https://janus.example.com/browse/OMNI-1"},"Helpdesk":{"ID":"25312","URL":"https://desk.zoho.com/agent/acme/tickets/25312"}}`)

	got := sourceHintsIn(root)
	if got.TrackerURL != "https://janus.example.com/browse/OMNI-1" {
		t.Fatalf("TrackerURL = %q", got.TrackerURL)
	}
	if got.HelpdeskURL != "https://desk.zoho.com/agent/acme/tickets/25312" {
		t.Fatalf("HelpdeskURL = %q", got.HelpdeskURL)
	}
	if got := sourceHintsIn(t.TempDir()); got != (SourceHints{}) {
		t.Fatalf("empty root = %+v", got)
	}
}
