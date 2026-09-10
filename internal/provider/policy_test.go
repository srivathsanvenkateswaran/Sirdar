package provider

import (
	"encoding/json"
	"testing"
)

func TestPolicy(t *testing.T) {
	p := &PermissionPolicy{BashAllow: []string{"git log*", "rg *", "dotnet build*"}}
	cases := []struct {
		tool, input string
		allow       bool
	}{
		{"Read", `{"file_path":"x"}`, true},
		{"mcp__grafana__query", `{}`, true},
		{"Write", `{"file_path":"x","content":"y"}`, false},
		{"Bash", `{"command":"git log --oneline -5"}`, true},
		{"Bash", `{"command":"  rg foo src/ "}`, true},
		{"Bash", `{"command":"rm -rf /"}`, false},
		{"Bash", `{"command":"git logs"}`, true}, // glob "git log*" matches "git logs"
		{"Bash", `{"command":"dotnet test"}`, false},
		{"Unknown", `{}`, false},
	}
	for _, c := range cases {
		d := p.Decide(c.tool, json.RawMessage(c.input))
		if d.Allow != c.allow {
			t.Errorf("%s %s: got allow=%v msg=%q", c.tool, c.input, d.Allow, d.Message)
		}
		if !d.Allow && d.Message == "" {
			t.Errorf("deny without message for %s", c.tool)
		}
	}
}

func TestMatchGlob(t *testing.T) {
	if !MatchGlob("git show*", "git show HEAD:path/file.cs") {
		t.Fatal("slash in run")
	}
	if MatchGlob("git show*", "gitshow") {
		t.Fatal("space is literal")
	}
	if !MatchGlob("?cho hi", "echo hi") {
		t.Fatal("?")
	}
}
