package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// reproConfigYAML is the test workspace with one staging host to reproduce on.
const reproConfigYAML = configYAML + `repro:
  hosts: ["staging.oxo.example"]
`

// reproMCPJSON declares the playwright server the way a workspace that
// confines it at the browser too would: with --allowed-origins.
const reproMCPJSON = `{"mcpServers":{"playwright":{"command":"npx","args":["@playwright/mcp@latest","--allowed-origins","https://staging.oxo.example"]}}}`

// reproWorkspace is a workspace with repro.hosts set and, when mcpJSON is
// not empty, that .mcp.json at its root.
func reproWorkspace(t *testing.T, mcpJSON string) *config.Config {
	t.Helper()
	cfg := newWorkspaceWith(t, reproConfigYAML)
	if mcpJSON != "" {
		if err := os.WriteFile(filepath.Join(cfg.Root, ".mcp.json"), []byte(mcpJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

// reproSession runs a session with no ticket on the given stub provider.
func reproSession(t *testing.T, cfg *config.Config, p *stubProvider) Outcome {
	t.Helper()
	if p.script == nil {
		p.script = replay(provider.Event{Kind: provider.EvFinal, Text: sessionReply})
	}
	r := newRunner(cfg, p, nil, nil)
	out, err := r.Session(context.Background(), "", Options{Instruction: sessionInstruction, NoBundle: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	return out
}

func notActive(reason string) string { return "repro configured but not active: " + reason }

// hasReproWarning reports whether any warning is a repro-not-active one.
func hasReproWarning(warnings []string) bool {
	for _, w := range warnings {
		if strings.HasPrefix(w, "repro configured but not active") {
			return true
		}
	}
	return false
}

func TestReproActiveAddsGuardAndSection(t *testing.T) {
	cfg := reproWorkspace(t, reproMCPJSON)
	p := &stubProvider{}
	out := reproSession(t, cfg, p)

	g := p.spec(0).Policy.Repro
	if g == nil {
		t.Fatal("the session's policy carries no repro guard")
	}
	if g.Server != "playwright" || len(g.Hosts) != 1 || g.Hosts[0] != "staging.oxo.example" || !g.Evaluate {
		t.Errorf("guard %+v, want playwright on staging.oxo.example with evaluate", g)
	}
	dir := runDir(t, cfg, out)
	text := readFile(t, filepath.Join(dir, "prompt.md"))
	repro := strings.Index(text, "# Reproduce on staging\n")
	task := strings.Index(text, "# Task")
	if repro < 0 || task < 0 || !strings.Contains(text[repro:task], "showed it.\n\n") || strings.Contains(text[repro+1:task], "\n# ") {
		t.Errorf("prompt.md does not carry the reproduction section right before # Task:\n%s", text)
	}
	if !strings.Contains(text, "(the mcp__playwright__ tools) and these staging hosts: staging.oxo.example.") {
		t.Errorf("the reproduction section does not name the server and hosts:\n%s", text)
	}
	if !hasLine(t, dir, "Reproduction on staging: staging.oxo.example through mcp__playwright__ tools") {
		t.Errorf("events.jsonl has no reproduction line:\n%s", strings.Join(eventLogLines(t, dir), "\n"))
	}
	if hasReproWarning(out.State.Warnings) {
		t.Errorf("warnings %q on an active run", out.State.Warnings)
	}
}

// Evaluate follows the server's own --allowed-origins, in either spelling,
// and stays off when the server starts without it.
func TestReproEvaluateFollowsAllowedOrigins(t *testing.T) {
	for _, tc := range []struct {
		args string
		want bool
	}{
		{`["@playwright/mcp","--allowed-origins","https://staging.oxo.example"]`, true},
		{`["@playwright/mcp","--allowed-origins=https://staging.oxo.example"]`, true},
		{`["@playwright/mcp","--isolated"]`, false},
	} {
		cfg := reproWorkspace(t, `{"mcpServers":{"playwright":{"command":"npx","args":`+tc.args+`}}}`)
		r := newRunner(cfg, &stubProvider{}, nil, nil)
		plan, reason := r.reproFor(&prepared{kind: store.KindSession})
		if plan == nil {
			t.Fatalf("%s: not active: %s", tc.args, reason)
		}
		if plan.Evaluate != tc.want {
			t.Errorf("%s: evaluate %v, want %v", tc.args, plan.Evaluate, tc.want)
		}
	}
}

// A server the operator's Claude CLI carries through mcp.userServers is as
// much the session's as a workspace one, and its own args decide Evaluate.
func TestReproServerFromUserServers(t *testing.T) {
	cli := t.TempDir()
	if err := os.WriteFile(filepath.Join(cli, ".claude.json"), []byte(`{"mcpServers":{
		"playwright":{"type":"stdio","command":"npx","args":["@playwright/mcp","--allowed-origins=https://staging.oxo.example"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := reproWorkspace(t, "")
	cfg.MCP.UserServers = []string{"playwright"}
	r := newRunner(cfg, &stubProvider{}, nil, nil)
	r.Env = append(r.Env, "CLAUDE_CONFIG_DIR="+cli)
	plan, reason := r.reproFor(&prepared{kind: store.KindSession})
	if plan == nil || !plan.Evaluate {
		t.Fatalf("plan %+v reason %q, want active with evaluate", plan, reason)
	}
}

func TestReproGuardIsFreshPerSession(t *testing.T) {
	cfg := reproWorkspace(t, reproMCPJSON)
	p := &stubProvider{}
	first := reproSession(t, cfg, p)

	// The first session's browser reached staging; the operator then logs
	// in and steers. The new session has not navigated anywhere yet.
	g0 := p.spec(0).Policy.Repro
	if d := p.spec(0).Policy.Decide("mcp__playwright__browser_navigate", json.RawMessage(`{"url":"https://staging.oxo.example/login"}`)); !d.Allow || !g0.Confined() {
		t.Fatalf("the first guard did not confine on a staging navigate: %+v", d)
	}
	p.script = replay(provider.Event{Kind: provider.EvFinal, Text: "Logged in; it reproduces."})
	r := newRunner(cfg, p, nil, nil)
	out, err := r.Steer(context.Background(), first.State.RunID, "logged in, carry on", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("steer status %q reason %q", out.State.Status, out.State.Reason)
	}
	g1 := p.spec(1).Policy.Repro
	if g1 == nil || g1 == g0 {
		t.Fatalf("the steer's guard %p, want a new one beside %p", g1, g0)
	}
	if g1.Confined() {
		t.Error("the steer's guard starts confined; it must navigate first")
	}
}

func TestReproOffForCodex(t *testing.T) {
	for _, name := range []string{"codex", "cursor", "agy"} {
		cfg := reproWorkspace(t, reproMCPJSON)
		p := &stubProvider{name: name}
		out := reproSession(t, cfg, p)
		if p.spec(0).Policy.Repro != nil {
			t.Errorf("%s: the policy carries a repro guard", name)
		}
		if strings.Contains(p.spec(0).Prompt, "# Reproduce on staging") {
			t.Errorf("%s: the prompt carries the reproduction section", name)
		}
		want := notActive("provider " + name + " does not hand Sirdar the call's arguments")
		if !contains(out.State.Warnings, want) {
			t.Errorf("%s: warnings %q, want %q", name, out.State.Warnings, want)
		}
	}
}

// The providers whose permission calls carry the tool's arguments are the
// ones a guard can judge a URL on.
func TestReproOnForMediatingProviders(t *testing.T) {
	for _, name := range []string{"claude", "openai", "qwen", "acp"} {
		cfg := reproWorkspace(t, reproMCPJSON)
		r := newRunner(cfg, &stubProvider{name: name}, nil, nil)
		if plan, reason := r.reproFor(&prepared{kind: store.KindTriage}); plan == nil {
			t.Errorf("%s: not active: %s", name, reason)
		}
	}
}

func TestReproOffForRCA(t *testing.T) {
	cfg := reproWorkspace(t, reproMCPJSON)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	triageOne(t, r, Options{NoteOnly: true})

	p.script = replyThenNote("The fix streamed the export.", finalEvent(rcaDoc))
	out, err := r.RCA(context.Background(), "OMNI-1", RCAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := notActive("kind rca is not in repro.kinds"); !contains(out.State.Warnings, want) {
		t.Errorf("warnings %q, want %q", out.State.Warnings, want)
	}
	for i := 1; i < p.startCount(); i++ {
		if p.spec(i).Policy.Repro != nil {
			t.Errorf("rca session %d carries a repro guard", i)
		}
	}
	if text := readFile(t, filepath.Join(runDir(t, cfg, out), "prompt.md")); strings.Contains(text, "# Reproduce on staging") {
		t.Error("the rca prompt carries the reproduction section")
	}
}

func TestReproOffForEval(t *testing.T) {
	cfg := reproWorkspace(t, reproMCPJSON)
	r := newRunner(cfg, &stubProvider{}, stubTracker{}, stubHelpdesk{})
	out := triageOne(t, r, Options{Eval: true, NoteOnly: true, DryRun: true})
	if want := notActive("eval runs do not reproduce"); !contains(out.State.Warnings, want) {
		t.Errorf("warnings %q, want %q", out.State.Warnings, want)
	}
	if plan, _ := r.reproFor(&prepared{kind: store.KindTriage, state: store.State{Eval: true}}); plan != nil {
		t.Error("an eval run has a repro plan")
	}
}

func TestReproOffWhenServerAbsent(t *testing.T) {
	cfg := reproWorkspace(t, "")
	p := &stubProvider{}
	out := reproSession(t, cfg, p)
	if p.spec(0).Policy.Repro != nil {
		t.Error("the policy carries a repro guard with no browser server")
	}
	if strings.Contains(p.spec(0).Prompt, "# Reproduce on staging") {
		t.Error("the prompt carries the reproduction section with no browser server")
	}
	if want := notActive("mcp server playwright is not configured for this session"); !contains(out.State.Warnings, want) {
		t.Errorf("warnings %q, want %q", out.State.Warnings, want)
	}
}

func TestNoteTurnHasNoGuard(t *testing.T) {
	cfg := reproWorkspace(t, reproMCPJSON)
	p := &stubProvider{script: replyThenNote("It is the export loop.", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	out := triageOne(t, r, Options{})
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if p.startCount() != 2 {
		t.Fatalf("started %d sessions, want the reply and the note", p.startCount())
	}
	if p.spec(0).Policy.Repro == nil {
		t.Error("the reply turn carries no repro guard")
	}
	if !strings.Contains(p.spec(0).Prompt, "# Reproduce on staging") {
		t.Error("the triage reply prompt carries no reproduction section")
	}
	if p.spec(1).Policy.Repro != nil {
		t.Error("the note turn carries a repro guard")
	}
}

func TestPlaybooksDirIsReadable(t *testing.T) {
	books := t.TempDir()
	if err := os.WriteFile(filepath.Join(books, "10-elsewhere.md"), []byte("# Elsewhere\n\nKept outside the workspace.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := newWorkspaceWith(t, strings.Replace(configYAML, "playbooks: .sirdar/playbooks", "playbooks: "+books, 1))
	p := &stubProvider{script: replyThenNote("It is the export loop.", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	out := triageOne(t, r, Options{})
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if !contains(p.spec(0).Policy.ReadRoots, books) {
		t.Errorf("reply turn read roots %q do not include the playbooks directory %s", p.spec(0).Policy.ReadRoots, books)
	}
	if contains(p.spec(1).Policy.ReadRoots, books) {
		t.Errorf("note turn read roots %q include the playbooks directory", p.spec(1).Policy.ReadRoots)
	}

	// A fix session stands in a worktree that holds no playbooks either.
	fp := &prepared{kind: store.KindFix, run: store.Run{Dir: t.TempDir()}}
	if spec := r.sessionSpec(fp, ""); !contains(spec.Policy.ReadRoots, books) {
		t.Errorf("fix read roots %q do not include the playbooks directory", spec.Policy.ReadRoots)
	}
}

func TestReproNotConfiguredIsSilent(t *testing.T) {
	cfg := newWorkspace(t)
	if err := os.WriteFile(filepath.Join(cfg.Root, ".mcp.json"), []byte(reproMCPJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &stubProvider{}
	out := reproSession(t, cfg, p)
	if p.spec(0).Policy.Repro != nil {
		t.Error("the policy carries a repro guard with no repro.hosts")
	}
	if strings.Contains(p.spec(0).Prompt, "# Reproduce on staging") {
		t.Error("the prompt carries the reproduction section with no repro.hosts")
	}
	if hasReproWarning(out.State.Warnings) {
		t.Errorf("warnings %q with no repro configured", out.State.Warnings)
	}
	if hasLine(t, runDir(t, cfg, out), "Reproduction on staging") {
		t.Error("events.jsonl reports reproduction with none configured")
	}
}

// Looking for the browser among the user servers reads them early; a read
// that failed still fails the run when execute asks for them.
func TestUserServersErrorSurvivesReproRead(t *testing.T) {
	cfg := reproWorkspace(t, "")
	cfg.MCP.UserServers = []string{"gone"}
	r := newRunner(cfg, &stubProvider{}, nil, nil)
	r.Env = append(r.Env, "CLAUDE_CONFIG_DIR="+t.TempDir())
	p := &prepared{kind: store.KindSession}
	if plan, _ := r.reproFor(p); plan != nil {
		t.Fatal("active with no browser server")
	}
	if err := r.userServers(p); err == nil {
		t.Fatal("the user-server error was lost to the repro read")
	}
}
