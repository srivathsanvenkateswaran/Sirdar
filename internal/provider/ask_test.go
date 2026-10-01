package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func bashInput(command string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": command})
	return b
}

// TestAskOnlyWhenEnabled pins that a policy without Ask decides exactly as it
// always has: a refusal carries no question.
func TestAskOnlyWhenEnabled(t *testing.T) {
	p := &PermissionPolicy{BashAllow: []string{"cat *"}, Root: t.TempDir()}
	d := p.Decide("Bash", bashInput("rg foo src"))
	if d.Allow || d.Ask != nil {
		t.Fatalf("got %+v, want a plain refusal", d)
	}
}

func TestAskPerKind(t *testing.T) {
	root := t.TempDir()
	p := &PermissionPolicy{BashAllow: []string{"cat *", "head *"}, Root: root, Ask: true}
	cases := []struct {
		name, tool, input string
		kind, summary     string
		patterns          []string
	}{
		{"bash", "Bash", `{"command":"rg -n foo src | head -5"}`, AskBash, "rg -n foo src | head -5", []string{"rg *"}},
		{"bash subcommand", "Bash", `{"command":"git log --oneline -3"}`, AskBash, "git log --oneline -3", []string{"git log *"}},
		{"bash two segments", "Bash", `{"command":"wc -l a && rg x"}`, AskBash, "wc -l a && rg x", []string{"wc *", "rg *"}},
		{"mcp", "mcp__grafana__create_incident", `{"title":"x"}`, AskMCP, `{"title":"x"}`, []string{"mcp__grafana__create_incident"}},
		{"fetch", "WebFetch", `{"url":"https://docs.example.com/a","prompt":"p"}`, AskFetch, "https://docs.example.com/a", []string{"docs.example.com"}},
		{"unknown tool", "KillShell", `{"id":"1"}`, AskTool, `{"id":"1"}`, []string{"KillShell"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := p.Decide(c.tool, json.RawMessage(c.input))
			if d.Allow {
				t.Fatalf("allowed: %+v", d)
			}
			if d.Ask == nil {
				t.Fatalf("no question on %q", d.Message)
			}
			if d.Ask.Kind != c.kind || d.Ask.Tool != c.tool || d.Ask.Summary != c.summary {
				t.Errorf("ask = %+v", *d.Ask)
			}
			if !reflect.DeepEqual(d.Ask.Patterns, c.patterns) {
				t.Errorf("patterns = %q, want %q", d.Ask.Patterns, c.patterns)
			}
			if d.Ask.Verdict != VerdictDeny || d.Ask.Reason != d.Message {
				t.Errorf("verdict/reason = %q / %q, message %q", d.Ask.Verdict, d.Ask.Reason, d.Message)
			}
		})
	}
}

func TestAskReadOutsideScope(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	file := filepath.Join(outside, "runbook.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &PermissionPolicy{Root: root, Ask: true}
	in, _ := json.Marshal(map[string]string{"file_path": file})
	d := p.Decide("Read", in)
	if d.Ask == nil || d.Ask.Kind != AskTool || d.Ask.Summary != file {
		t.Fatalf("got %+v", d)
	}
	if !reflect.DeepEqual(d.Ask.Patterns, []string{outside}) {
		t.Errorf("patterns = %q, want the directory", d.Ask.Patterns)
	}
}

// TestNoAskWhereNoGrantHelps: the read-only guarantee, a shell construct and
// an IP literal stay plain refusals — a button that cannot help is not
// offered.
func TestNoAskWhereNoGrantHelps(t *testing.T) {
	p := &PermissionPolicy{Root: t.TempDir(), Ask: true}
	cases := []struct{ tool, input string }{
		{"Write", `{"file_path":"x","content":"y"}`},
		{"Edit", `{"file_path":"x"}`},
		{"Bash", `{"command":"rg x > out.txt"}`},
		{"Bash", `{"command":"cat $(whoami)"}`},
		{"Bash", `{"command":"cat ../../etc/passwd"}`},
		{"WebFetch", `{"url":"https://10.0.0.1/"}`},
	}
	for _, c := range cases {
		d := p.Decide(c.tool, json.RawMessage(c.input))
		if d.Allow || d.Ask != nil {
			t.Errorf("%s %s: got %+v, want a plain refusal", c.tool, c.input, d)
		}
	}
	fix := FixPolicy(t.TempDir(), nil, nil, nil)
	fix.Ask = true
	if d := fix.Decide("Edit", json.RawMessage(`{"file_path":"/etc/hosts"}`)); d.Allow || d.Ask != nil {
		t.Errorf("fix write outside the tree: %+v", d)
	}
}

func TestGrantsConsulted(t *testing.T) {
	root := t.TempDir()
	newPolicy := func(gs ...Grant) *PermissionPolicy {
		return &PermissionPolicy{BashAllow: []string{"head *"}, Root: root, Ask: true, Grants: NewGrants(gs...)}
	}
	ask := newPolicy().Decide("Bash", bashInput("rg -n foo src")).Ask
	if ask == nil {
		t.Fatal("no question")
	}

	t.Run("allow once is spent", func(t *testing.T) {
		p := newPolicy(GrantFor(*ask, VerdictAllow, ""))
		if d := p.Decide("Bash", bashInput("rg -n foo src")); !d.Allow || d.Granted != VerdictAllow {
			t.Fatalf("first call: %+v", d)
		}
		if d := p.Decide("Bash", bashInput("rg -n foo src")); d.Allow || d.Ask == nil {
			t.Fatalf("second call: %+v, want a fresh question", d)
		}
	})
	t.Run("allow once covers only that call", func(t *testing.T) {
		p := newPolicy(GrantFor(*ask, VerdictAllow, ""))
		if d := p.Decide("Bash", bashInput("rg -n bar src")); d.Allow {
			t.Fatalf("a different rg was allowed: %+v", d)
		}
	})
	t.Run("allow for this run covers the pattern", func(t *testing.T) {
		p := newPolicy(GrantFor(*ask, VerdictAllowRun, ""))
		for _, cmd := range []string{"rg -n foo src", "rg bar", "rg -l x | head -3"} {
			if d := p.Decide("Bash", bashInput(cmd)); !d.Allow || d.Granted != VerdictAllowRun {
				t.Errorf("%q: %+v", cmd, d)
			}
		}
		if d := p.Decide("Bash", bashInput("rg x | wc -l")); d.Allow || d.Ask == nil || !reflect.DeepEqual(d.Ask.Patterns, []string{"wc *"}) {
			t.Errorf("an ungranted segment: %+v", d)
		}
		if d := p.Decide("Bash", bashInput("rg x > out")); d.Allow {
			t.Errorf("a grant lifted the redirection rule: %+v", d)
		}
	})
	t.Run("deny refuses with the reason and does not ask again", func(t *testing.T) {
		p := newPolicy(GrantFor(*ask, VerdictDeny, "too slow on this repo"))
		d := p.Decide("Bash", bashInput("rg other"))
		if d.Allow || d.Ask != nil || d.Granted != VerdictDeny {
			t.Fatalf("got %+v", d)
		}
		if want := "Sirdar policy: the operator denied rg for this run: too slow on this repo"; d.Message != want {
			t.Errorf("message = %q, want %q", d.Message, want)
		}
	})
	t.Run("deny outranks a broader allow", func(t *testing.T) {
		p := newPolicy(GrantFor(*ask, VerdictAllowRun, ""), GrantFor(*ask, VerdictDeny, ""))
		if d := p.Decide("Bash", bashInput("rg -n foo src")); d.Allow {
			t.Fatalf("got %+v", d)
		}
	})
	t.Run("mcp grant is the tool alone", func(t *testing.T) {
		p := newPolicy()
		q := p.Decide("mcp__db__run_query", nil).Ask
		if q == nil {
			t.Fatal("no question")
		}
		p = newPolicy(GrantFor(*q, VerdictAllowRun, ""))
		if d := p.Decide("mcp__db__run_query", json.RawMessage(`{"sql":"select 2"}`)); !d.Allow {
			t.Errorf("same tool: %+v", d)
		}
		if d := p.Decide("mcp__db__run_migration", nil); d.Allow {
			t.Errorf("another tool: %+v", d)
		}
	})
	t.Run("fetch grant is the host", func(t *testing.T) {
		p := newPolicy()
		q := p.Decide("WebFetch", json.RawMessage(`{"url":"https://docs.example.com/a"}`)).Ask
		p = newPolicy(GrantFor(*q, VerdictAllowRun, ""))
		if d := p.Decide("WebFetch", json.RawMessage(`{"url":"https://docs.example.com/b"}`)); !d.Allow {
			t.Errorf("same host: %+v", d)
		}
		if d := p.Decide("WebFetch", json.RawMessage(`{"url":"https://evil.example.com/"}`)); d.Allow {
			t.Errorf("another host: %+v", d)
		}
	})
}

func TestCommandPattern(t *testing.T) {
	cases := map[string]string{
		"rg -n foo":           "rg *",
		"git log -3":          "git log *",
		"git -C x log":        "git *",
		"FOO=1 go test ./...": "go test *",
		"/usr/bin/grep x":     "/usr/bin/grep *",
		"kubectl get pods -A": "kubectl get *",
		"npm":                 "npm *",
	}
	for in, want := range cases {
		if got := commandPattern(in); got != want {
			t.Errorf("commandPattern(%q) = %q, want %q", in, got, want)
		}
	}
}
