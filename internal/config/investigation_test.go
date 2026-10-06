package config

import (
	"strings"
	"testing"
)

func TestInvestigationDefaults(t *testing.T) {
	c, err := Load(writeCfg(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.PlaybooksBudget(); got != DefaultPlaybooksMaxBytes {
		t.Fatalf("PlaybooksBudget() = %d, want %d", got, DefaultPlaybooksMaxBytes)
	}
	if got := c.NoteMaxTurns(); got != DefaultNoteMaxTurns {
		t.Fatalf("NoteMaxTurns() = %d, want %d", got, DefaultNoteMaxTurns)
	}
	if got := c.ReplyMaxTurns(); got != 0 {
		t.Fatalf("ReplyMaxTurns() = %d, want 0", got)
	}
	if !c.GatherEnabled() {
		t.Fatal("GatherEnabled() want true")
	}
	if !c.GatherPREnabled() {
		t.Fatal("GatherPREnabled() want true")
	}
	if !c.GatherPRHistory() {
		t.Fatal("GatherPRHistory() want true")
	}
	if c.Gather.TimeoutSeconds != DefaultGatherTimeoutSeconds {
		t.Fatalf("gather.timeoutSeconds = %d, want %d", c.Gather.TimeoutSeconds, DefaultGatherTimeoutSeconds)
	}
	if c.Gather.TotalSeconds != DefaultGatherTotalSeconds {
		t.Fatalf("gather.totalSeconds = %d, want %d", c.Gather.TotalSeconds, DefaultGatherTotalSeconds)
	}
	if c.Gather.MaxBytes != DefaultGatherMaxBytes {
		t.Fatalf("gather.maxBytes = %d, want %d", c.Gather.MaxBytes, DefaultGatherMaxBytes)
	}
	if c.Gather.TotalBytes != DefaultGatherTotalBytes {
		t.Fatalf("gather.totalBytes = %d, want %d", c.Gather.TotalBytes, DefaultGatherTotalBytes)
	}
	if c.Gather.PR.Max != DefaultGatherPRMax {
		t.Fatalf("gather.pr.max = %d, want %d", c.Gather.PR.Max, DefaultGatherPRMax)
	}
	if c.Repro.Server != DefaultReproServer {
		t.Fatalf("repro.server = %q, want %q", c.Repro.Server, DefaultReproServer)
	}
	if got, want := c.Repro.Kinds, []string{"triage", "session"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("repro.kinds = %v, want %v", got, want)
	}
	if c.ReproConfigured() {
		t.Fatal("ReproConfigured() want false for an empty repro.hosts")
	}
}

func TestNoteMaxTurnsZeroTurnsTheCapOff(t *testing.T) {
	c, err := Load(writeCfg(t, minimal+"phases:\n  note:\n    maxTurns: 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.NoteMaxTurns(); got != 0 {
		t.Fatalf("NoteMaxTurns() = %d, want 0", got)
	}
}

func TestPlaybooksMaxBytesZeroIsNoLimit(t *testing.T) {
	c, err := Load(writeCfg(t, minimal+"playbooksMaxBytes: 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.PlaybooksBudget(); got != 0 {
		t.Fatalf("PlaybooksBudget() = %d, want 0", got)
	}
}

func TestGatherVariableAndStepDefaults(t *testing.T) {
	c, err := Load(writeCfg(t, minimal+`gather:
  variables:
    domain:
      pattern: '(?i)(?:domain)\W{0,6}([a-z0-9][a-z0-9-]{1,62})'
  steps:
    - name: company
      mcp:
        server: metabase
        tool: resolve_company
        args: {domain: "{{domain}}"}
`))
	if err != nil {
		t.Fatal(err)
	}
	v := c.Gather.Variables["domain"]
	if len(v.From) != len(GatherSources) {
		t.Fatalf("variable.From = %v, want all of %v", v.From, GatherSources)
	}
	if v.Max != DefaultGatherVariableMax {
		t.Fatalf("variable.Max = %d, want %d", v.Max, DefaultGatherVariableMax)
	}
	if len(c.Gather.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(c.Gather.Steps))
	}
	s := c.Gather.Steps[0]
	if got, want := s.Kinds, []string{"triage", "rca", "session"}; len(got) != len(want) {
		t.Fatalf("step.Kinds = %v, want %v", got, want)
	}
	if s.TimeoutSeconds != DefaultGatherTimeoutSeconds {
		t.Fatalf("step.TimeoutSeconds = %d, want %d", s.TimeoutSeconds, DefaultGatherTimeoutSeconds)
	}
}

func TestInvestigationValidation(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			"bad reply model whitespace",
			"phases:\n  note:\n    model: \"claude sonnet\"\n",
			"phases.note.model",
		},
		{
			"gather timeout too high",
			"gather:\n  timeoutSeconds: 301\n",
			"gather.timeoutSeconds",
		},
		{
			"total bytes below max bytes",
			"gather:\n  maxBytes: 4096\n  totalBytes: 2048\n",
			"gather.totalBytes",
		},
		{
			// provider.ShellConstruct, the check the brief names for this
			// key, does not flag a pipe (matchCommand splits on "|" and
			// checks each segment separately; there is no single-entry
			// equivalent here), so this uses a construct ShellConstruct
			// does flag: command substitution.
			"allow entry carries a shell construct",
			"gather:\n  allow: [\"git log $(rm -rf /)\"]\n",
			"gather.allow",
		},
		{
			"pr max too high",
			"gather:\n  pr:\n    max: 21\n",
			"gather.pr.max",
		},
		{
			"bad variable name",
			"gather:\n  variables:\n    Domain:\n      pattern: '(a)'\n",
			"gather.variables.Domain",
		},
		{
			"variable pattern has two groups",
			"gather:\n  variables:\n    domain:\n      pattern: '(a)(b)'\n",
			"gather.variables.domain.pattern",
		},
		{
			"variable from not a gather source",
			"gather:\n  variables:\n    domain:\n      pattern: '(a)'\n      from: [email]\n",
			"gather.variables.domain.from",
		},
		{
			"bad step name",
			"gather:\n  steps:\n    - name: Company\n      command: [git, status]\n",
			"gather.steps[0].name",
		},
		{
			"duplicate step names",
			"gather:\n  steps:\n    - name: a\n      command: [git, status]\n    - name: a\n      command: [git, log]\n",
			"gather.steps[1].name",
		},
		{
			"step with both command and mcp",
			"gather:\n  steps:\n    - name: both\n      command: [git, status]\n      mcp: {server: x, tool: y}\n",
			"gather.steps[0]",
		},
		{
			"step with neither command nor mcp",
			"gather:\n  steps:\n    - name: neither\n",
			"gather.steps[0]",
		},
		{
			"command argv0 templated",
			"gather:\n  variables:\n    domain:\n      pattern: '(a)'\n  steps:\n    - name: x\n      command: [\"{{domain}}\", x]\n",
			"gather.steps[0].command[0]",
		},
		{
			"command references undeclared variable",
			"gather:\n  steps:\n    - name: x\n      command: [git, \"{{nope}}\"]\n",
			"gather.steps[0].command",
		},
		{
			"mcp args map key templated",
			"gather:\n  steps:\n    - name: x\n      mcp: {server: s, tool: t, args: {\"{{domain}}\": x}}\n",
			"gather.steps[0].mcp.args",
		},
		{
			"bad repro host",
			"repro:\n  hosts: [\"bad host\"]\n",
			"repro.hosts[0]",
		},
		{
			"bad repro server name",
			"repro:\n  hosts: [staging.example]\n  server: \"a b\"\n",
			"repro.server",
		},
		{
			"rca not allowed in repro.kinds",
			"repro:\n  hosts: [staging.example]\n  kinds: [rca]\n",
			"repro.kinds",
		},
		{
			"unknown key under a gather step",
			"gather:\n  steps:\n    - name: x\n      command: [git, status]\n      shell: x\n",
			"shell",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeCfg(t, minimal+tc.yaml))
			if err == nil {
				t.Fatalf("want an error containing %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// fullInvestigationYAML is the complete YAML from spec §2.1, §3.4 and §4.1,
// combined under one workspace: every new key in one load, none of it
// rejected.
const fullInvestigationYAML = `
phases:
  reply:
    model: ""
    maxTurns: 0
  note:
    model: ""
    maxTurns: 8
gather:
  enabled: true
  timeoutSeconds: 20
  totalSeconds: 60
  maxBytes: 131072
  totalBytes: 1048576
  allow: []
  pr: {enabled: true, anyRepo: false, max: 5, history: true}
  variables:
    domain:
      pattern: '(?i)(?:domain|الدومين)\W{0,6}([a-z0-9][a-z0-9-]{1,62})'
      from: [ticket, thread, slack, instruction]
      max: 3
  steps:
    - name: company
      about: Company row for the ticket's domain
      kinds: [triage, rca, session]
      mcp:
        server: metabase
        tool: resolve_company
        args: {domain: "{{domain}}"}
    - name: systems-tags
      about: Latest OXO.Systems release tags
      command: [git, -C, ~/Documents/Work/Coding/GitHub/OXO.Systems, tag, --sort=-creatordate, --list, "v*"]
      timeoutSeconds: 10
repro:
  hosts: ["staging.oxo.example", "*.stg.oxo.example"]
  server: playwright
  kinds: [triage, session]
`

func TestInvestigationValid(t *testing.T) {
	c, err := Load(writeCfg(t, minimal+fullInvestigationYAML))
	if err != nil {
		t.Fatalf("got %v, want the full §2.1/§3.4/§4.1 example to load", err)
	}
	if !c.ReproConfigured() {
		t.Fatal("ReproConfigured() want true")
	}
}
