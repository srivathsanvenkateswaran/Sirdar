package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// slackCLI gives the test a Claude CLI user scope with a slack server, the
// way the operator's ~/.claude.json has one.
func slackCLI(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	body := `{"mcpServers":{"slack":{"type":"http","url":"https://mcp.example/mcp","oauth":{"clientId":"c"}}}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
}

func slackMCPConfig(t *testing.T) *config.Config {
	cfg := &config.Config{Root: t.TempDir(), Provider: "claude"}
	cfg.MCP.UserServers = []string{"slack"}
	return cfg
}

// recordingSlackProvider answers every reading with doc and keeps the specs
// it was started with.
func recordingSlackProvider(doc string) (*stubProvider, func() []provider.SessionSpec) {
	var mu sync.Mutex
	var specs []provider.SessionSpec
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		mu.Lock()
		specs = append(specs, spec)
		mu.Unlock()
		replay(finalEvent(doc))(spec, s)
	}}
	return p, func() []provider.SessionSpec {
		mu.Lock()
		defer mu.Unlock()
		return append([]provider.SessionSpec(nil), specs...)
	}
}

func TestSlackIntakeViaMCP(t *testing.T) {
	slackCLI(t)
	cfg := slackMCPConfig(t)
	prov, specs := recordingSlackProvider(`{"messages":[
		{"author":"سارة","time":"2026-09-02T09:00:00Z","text":"العميل يشتكي، التذكرة #28311"},
		{"author":"sam","time":"1788339600.000100","text":"looking"}],
		"refs":["#28311"]}`)
	sr, err := SlackReaderFor(cfg, prov)
	if err != nil || sr == nil {
		t.Fatalf("SlackReaderFor = %v, %v", sr, err)
	}
	r := newTestResolver(t, startIntakeAdapter(t, "ignore"), nil)
	r.slack = sr
	link := "https://acme.slack.com/archives/C0MCP0001/p1712345678901234"
	in := resolveOK(t, r, link)
	if in.Key != "SBX-2" || in.Summary != "Slack (via MCP) → #28311 → SBX-2 · matched by title" {
		t.Fatalf("intake %+v", in)
	}
	md := in.SlackMarkdown()
	if !strings.Contains(md, "## 2026-09-02T09:00:00Z · slack · سارة") || !strings.Contains(md, "looking") {
		t.Fatalf("slack.md:\n%s", md)
	}

	got := specs()
	if len(got) != 1 {
		t.Fatalf("%d readings, want 1", len(got))
	}
	spec := got[0]
	if !spec.MCPStrict || spec.MCPConfig != "" || len(spec.UserMCPServers) != 1 || spec.UserMCPServers[0].Name != "slack" {
		t.Errorf("the reading must see the slack server and nothing else: %+v", spec)
	}
	if spec.Budget.MaxTurns != slackMCPMaxTurns || spec.Policy == nil || strings.Join(spec.Policy.MCPAllow, ",") != "mcp__slack__slack_read_*" {
		t.Errorf("budget %+v, policy %+v", spec.Budget, spec.Policy)
	}
	if spec.Policy.Decide("mcp__slack__slack_send_message", []byte(`{}`)).Allow {
		t.Error("the reading's policy allowed a Slack write")
	}
	if !strings.Contains(spec.Prompt, "C0MCP0001") || !strings.Contains(spec.Prompt, "1712345678.901234") {
		t.Errorf("prompt does not name the channel and ts:\n%s", spec.Prompt)
	}

	// Starting the run on the same link reads the cached thread rather than
	// asking the model again.
	if _, err := sr.Read(context.Background(), in.thread.Link); err != nil {
		t.Fatal(err)
	}
	if n := len(specs()); n != 1 {
		t.Errorf("a second read of the same link started %d readings, want the cache", n)
	}
}

// TestSlackIntakeViaMCPUsesTheRefs: a reference the model listed is found
// even when the texts carry it in a shape the resolver does not read.
func TestSlackIntakeViaMCPUsesTheRefs(t *testing.T) {
	slackCLI(t)
	prov, _ := recordingSlackProvider(`{"messages":[{"author":"sam","time":"","text":"see the ticket in the unfurl"}],"refs":["SBX-1"]}`)
	sr, err := SlackReaderFor(slackMCPConfig(t), prov)
	if err != nil {
		t.Fatal(err)
	}
	r := newTestResolver(t, nil, nil)
	r.slack = sr
	in := resolveOK(t, r, "https://acme.slack.com/archives/C0MCP0002/p1712345678901234")
	if in.Key != "SBX-1" || in.Summary != "Slack (via MCP) → SBX-1" {
		t.Fatalf("intake %+v", in)
	}
}

// TestSlackIntakeViaMCPNoAnswer: a reading that ends without the document
// is a reason, not a key.
func TestSlackIntakeViaMCPNoAnswer(t *testing.T) {
	slackCLI(t)
	prov := &stubProvider{script: replay()}
	sr, _ := SlackReaderFor(slackMCPConfig(t), prov)
	r := newTestResolver(t, nil, nil)
	r.slack = sr
	in := resolveOK(t, r, "https://acme.slack.com/archives/C0MCP0003/p1712345678901234")
	if in.Key != "" || !strings.Contains(in.Reason, "the Slack MCP reading gave no answer") {
		t.Fatalf("intake %+v", in)
	}
}

func TestSlackReaderFor(t *testing.T) {
	slackCLI(t)
	prov := &stubProvider{}

	cfg := slackMCPConfig(t)
	cfg.Sources.Slack = &config.SlackConfig{Token: "env:SIRDAR_TEST_SLACK_TOKEN_MCP"}
	t.Setenv("SIRDAR_TEST_SLACK_TOKEN_MCP", "xoxp-x")
	if sr, err := SlackReaderFor(cfg, prov); err != nil {
		t.Fatal(err)
	} else if _, mcp := sr.(*slackMCPReader); mcp {
		t.Error("a token wins over the MCP server")
	}

	cfg = slackMCPConfig(t)
	if sr, _ := SlackReaderFor(cfg, nil); sr != nil {
		t.Error("no provider, no MCP reading")
	}
	cfg.MCP.UserServers = nil
	if sr, _ := SlackReaderFor(cfg, prov); sr != nil {
		t.Error("slack not opted in, so there is no reader")
	}

	cfg.MCP.UserServers = []string{"slack"}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if _, err := SlackReaderFor(cfg, prov); err == nil {
		t.Error("slack opted in but missing from the CLI must be an error")
	}
}

func TestSlackNotConfiguredNamesTheMCPRoute(t *testing.T) {
	if !strings.Contains(SlackNotConfigured, "sources.slack.token") || !strings.HasSuffix(SlackNotConfigured, "or add `slack` to mcp.userServers") {
		t.Fatalf("reason %q", SlackNotConfigured)
	}
}
