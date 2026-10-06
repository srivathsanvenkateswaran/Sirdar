package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// Defaults for the investigation skeleton: the scoped-playbook budget, the
// reply/note phase models and turn caps, the gather step, and staging
// reproduction. None of these are read yet (that lands in later tasks); this
// file only makes the keys parse, default and validate so a workspace can
// start setting them.
const (
	// DefaultPlaybooksMaxBytes bounds how much of the "# Playbooks" section
	// a prompt carries. The OMNI-3413 triage prompt spent 27,724 of its
	// 45,793 bytes there; this cap keeps a future prompt from repeating
	// that without the workspace asking for it.
	DefaultPlaybooksMaxBytes = 16384
	// DefaultNoteMaxTurns is the cap on the note turn. The one recorded
	// note turn took 2 turns; a schema retry costs about 2 more, plus a
	// read or two of answer.md or a bundle file, which makes 6. 8 leaves
	// headroom without giving the note turn room to start investigating
	// again.
	DefaultNoteMaxTurns = 8
	// DefaultGatherTimeoutSeconds is the per-step deadline.
	DefaultGatherTimeoutSeconds = 20
	// DefaultGatherTotalSeconds bounds all gatherers for a run together.
	DefaultGatherTotalSeconds = 60
	// DefaultGatherMaxBytes caps a single gathered file.
	DefaultGatherMaxBytes = 131072
	// DefaultGatherTotalBytes caps everything gathered for one run.
	DefaultGatherTotalBytes = 1048576
	// DefaultGatherPRMax is how many PRs the built-in gatherer takes from
	// a ticket, first seen first.
	DefaultGatherPRMax = 5
	// DefaultGatherVariableMax is how many distinct values a gather
	// variable keeps, first-seen order.
	DefaultGatherVariableMax = 3
	// DefaultReproServer is the MCP server reproduction drives a browser
	// through when the workspace does not say otherwise.
	DefaultReproServer = "playwright"
)

// GatherSources are the texts a gather variable may be read from.
var GatherSources = []string{"ticket", "thread", "slack", "instruction"}

// RunKinds are the four run kinds a playbook or a gather step can name.
var RunKinds = []string{"session", "triage", "rca", "fix"}

// PhaseConfig is one phase's model and turn cap.
type PhaseConfig struct {
	Model    string `yaml:"model"`
	MaxTurns *int   `yaml:"maxTurns"`
}

// PhasesConfig holds the reply and note phase settings (spec §2.1).
type PhasesConfig struct {
	Reply PhaseConfig `yaml:"reply"`
	Note  PhaseConfig `yaml:"note"`
}

// GatherConfig is the gather block (spec §3.4): the built-ins' knobs plus
// the workspace's own config-defined variables and steps.
type GatherConfig struct {
	Enabled        *bool                     `yaml:"enabled"`
	TimeoutSeconds int                       `yaml:"timeoutSeconds"`
	TotalSeconds   int                       `yaml:"totalSeconds"`
	MaxBytes       int                       `yaml:"maxBytes"`
	TotalBytes     int                       `yaml:"totalBytes"`
	Allow          []string                  `yaml:"allow"`
	PR             GatherPRConfig            `yaml:"pr"`
	Variables      map[string]GatherVariable `yaml:"variables"`
	Steps          []GatherStep              `yaml:"steps"`
}

// GatherPRConfig configures the built-in pull-request gatherer.
type GatherPRConfig struct {
	Enabled *bool `yaml:"enabled"`
	AnyRepo bool  `yaml:"anyRepo"`
	Max     int   `yaml:"max"`
	History *bool `yaml:"history"`
}

// GatherVariable is one named capture a gather step can reference as
// "{{name}}".
type GatherVariable struct {
	Pattern string   `yaml:"pattern"`
	From    []string `yaml:"from"`
	Max     int      `yaml:"max"`
}

// GatherStep is one config-defined gatherer: exactly one of Command or MCP.
type GatherStep struct {
	Name           string         `yaml:"name"`
	About          string         `yaml:"about"`
	Kinds          []string       `yaml:"kinds"`
	Command        []string       `yaml:"command"`
	MCP            *GatherMCPStep `yaml:"mcp"`
	TimeoutSeconds int            `yaml:"timeoutSeconds"`
}

// GatherMCPStep is an MCP-backed gather step's call.
type GatherMCPStep struct {
	Server string         `yaml:"server"`
	Tool   string         `yaml:"tool"`
	Args   map[string]any `yaml:"args"`
}

// ReproConfig configures staging reproduction (spec §4.1).
type ReproConfig struct {
	Hosts  []string `yaml:"hosts"`
	Server string   `yaml:"server"`
	Kinds  []string `yaml:"kinds"`
}

// PlaybooksBudget is the byte budget for the "# Playbooks" section of a
// prompt. 0 means no limit.
func (c *Config) PlaybooksBudget() int {
	if c.PlaybooksMaxBytes == nil {
		return DefaultPlaybooksMaxBytes
	}
	return *c.PlaybooksMaxBytes
}

// ReplyModel is the trimmed phases.reply.model, "" when unset.
func (c *Config) ReplyModel() string {
	return strings.TrimSpace(c.Phases.Reply.Model)
}

// NoteModel is the trimmed phases.note.model, "" when unset.
func (c *Config) NoteModel() string {
	return strings.TrimSpace(c.Phases.Note.Model)
}

// ReplyMaxTurns is the reply phase's turn cap. 0 means no cap beyond the
// run's own budget.maxTurns.
func (c *Config) ReplyMaxTurns() int {
	if c.Phases.Reply.MaxTurns == nil {
		return 0
	}
	return *c.Phases.Reply.MaxTurns
}

// NoteMaxTurns is the note phase's turn cap, defaulting to
// DefaultNoteMaxTurns. An explicit 0 turns the cap off.
func (c *Config) NoteMaxTurns() int {
	if c.Phases.Note.MaxTurns == nil {
		return DefaultNoteMaxTurns
	}
	return *c.Phases.Note.MaxTurns
}

// GatherEnabled reports whether gathering runs at all for this workspace.
func (c *Config) GatherEnabled() bool {
	return c.Gather.Enabled == nil || *c.Gather.Enabled
}

// GatherPREnabled reports whether the built-in pull-request gatherer runs.
func (c *Config) GatherPREnabled() bool {
	return c.Gather.PR.Enabled == nil || *c.Gather.PR.Enabled
}

// GatherPRHistory reports whether the built-in file-history gatherer runs
// for a PR whose repository maps to a local clone.
func (c *Config) GatherPRHistory() bool {
	return c.Gather.PR.History == nil || *c.Gather.PR.History
}

// ReproConfigured reports whether the workspace has named any staging
// hosts at all. It does not say whether reproduction is active for a
// given run — that also depends on the run's kind and the provider.
func (c *Config) ReproConfigured() bool {
	return len(c.Repro.Hosts) > 0
}

// applyInvestigationDefaults fills in the defaults for the keys this file
// adds. It is called as the last step of applyDefaults.
func applyInvestigationDefaults(c *Config) {
	if c.PlaybooksMaxBytes == nil {
		v := DefaultPlaybooksMaxBytes
		c.PlaybooksMaxBytes = &v
	}
	if c.Phases.Reply.MaxTurns == nil {
		v := 0
		c.Phases.Reply.MaxTurns = &v
	}
	if c.Phases.Note.MaxTurns == nil {
		v := DefaultNoteMaxTurns
		c.Phases.Note.MaxTurns = &v
	}
	c.Phases.Reply.Model = strings.TrimSpace(c.Phases.Reply.Model)
	c.Phases.Note.Model = strings.TrimSpace(c.Phases.Note.Model)

	if c.Gather.Enabled == nil {
		yes := true
		c.Gather.Enabled = &yes
	}
	if c.Gather.TimeoutSeconds == 0 {
		c.Gather.TimeoutSeconds = DefaultGatherTimeoutSeconds
	}
	if c.Gather.TotalSeconds == 0 {
		c.Gather.TotalSeconds = DefaultGatherTotalSeconds
	}
	if c.Gather.MaxBytes == 0 {
		c.Gather.MaxBytes = DefaultGatherMaxBytes
	}
	if c.Gather.TotalBytes == 0 {
		c.Gather.TotalBytes = DefaultGatherTotalBytes
	}
	if c.Gather.PR.Enabled == nil {
		yes := true
		c.Gather.PR.Enabled = &yes
	}
	if c.Gather.PR.History == nil {
		yes := true
		c.Gather.PR.History = &yes
	}
	if c.Gather.PR.Max == 0 {
		c.Gather.PR.Max = DefaultGatherPRMax
	}
	for name, v := range c.Gather.Variables {
		if len(v.From) == 0 {
			v.From = append([]string(nil), GatherSources...)
		}
		if v.Max == 0 {
			v.Max = DefaultGatherVariableMax
		}
		c.Gather.Variables[name] = v
	}
	for i := range c.Gather.Steps {
		s := &c.Gather.Steps[i]
		if len(s.Kinds) == 0 {
			s.Kinds = []string{"triage", "rca", "session"}
		}
		if s.TimeoutSeconds == 0 {
			s.TimeoutSeconds = c.Gather.TimeoutSeconds
		}
	}

	if c.Repro.Server == "" {
		c.Repro.Server = DefaultReproServer
	}
	if len(c.Repro.Kinds) == 0 {
		c.Repro.Kinds = []string{"triage", "session"}
	}
}

// gatherVariableName matches a gather variable's declared name.
var gatherVariableName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// gatherStepName matches a gather step's declared name.
var gatherStepName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// gatherTemplateRef finds a "{{name}}" reference in a command element or
// an mcp.args string leaf.
var gatherTemplateRef = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// validateInvestigation checks the keys this file adds, returning the
// first violation found, prefixed "config: <key>:" like every other
// Config.Validate check.
func validateInvestigation(c *Config) error {
	if c.PlaybooksMaxBytes != nil && *c.PlaybooksMaxBytes < 0 {
		return fmt.Errorf("config: playbooksMaxBytes: must be >= 0, got %d", *c.PlaybooksMaxBytes)
	}
	if strings.ContainsAny(c.Phases.Reply.Model, " \t\r\n") {
		return fmt.Errorf("config: phases.reply.model: must not contain whitespace, got %q", c.Phases.Reply.Model)
	}
	if strings.ContainsAny(c.Phases.Note.Model, " \t\r\n") {
		return fmt.Errorf("config: phases.note.model: must not contain whitespace, got %q", c.Phases.Note.Model)
	}
	if c.Phases.Reply.MaxTurns != nil && *c.Phases.Reply.MaxTurns < 0 {
		return fmt.Errorf("config: phases.reply.maxTurns: must be >= 0, got %d", *c.Phases.Reply.MaxTurns)
	}
	if c.Phases.Note.MaxTurns != nil && *c.Phases.Note.MaxTurns < 0 {
		return fmt.Errorf("config: phases.note.maxTurns: must be >= 0, got %d", *c.Phases.Note.MaxTurns)
	}

	if c.Gather.TimeoutSeconds < 1 || c.Gather.TimeoutSeconds > 300 {
		return fmt.Errorf("config: gather.timeoutSeconds: must be 1-300, got %d", c.Gather.TimeoutSeconds)
	}
	if c.Gather.TotalSeconds < 1 || c.Gather.TotalSeconds > 600 {
		return fmt.Errorf("config: gather.totalSeconds: must be 1-600, got %d", c.Gather.TotalSeconds)
	}
	if c.Gather.MaxBytes < 1024 || c.Gather.MaxBytes > 4194304 {
		return fmt.Errorf("config: gather.maxBytes: must be 1024-4194304, got %d", c.Gather.MaxBytes)
	}
	if c.Gather.TotalBytes < c.Gather.MaxBytes || c.Gather.TotalBytes > 16777216 {
		return fmt.Errorf("config: gather.totalBytes: must be >= gather.maxBytes and <= 16777216, got %d", c.Gather.TotalBytes)
	}
	for i, entry := range c.Gather.Allow {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			return fmt.Errorf("config: gather.allow[%d]: is empty", i)
		}
		if construct := provider.ShellConstruct(entry); construct != "" {
			return fmt.Errorf("config: gather.allow[%d]: %q carries %s, which gather steps never get", i, entry, construct)
		}
	}
	if c.Gather.PR.Max < 1 || c.Gather.PR.Max > 20 {
		return fmt.Errorf("config: gather.pr.max: must be 1-20, got %d", c.Gather.PR.Max)
	}

	for name, v := range c.Gather.Variables {
		if !gatherVariableName.MatchString(name) {
			return fmt.Errorf("config: gather.variables.%s: name must match ^[a-z][a-z0-9_]{0,31}$", name)
		}
		re, err := regexp.Compile(v.Pattern)
		if err != nil {
			return fmt.Errorf("config: gather.variables.%s.pattern: %s", name, err)
		}
		if re.NumSubexp() != 1 {
			return fmt.Errorf("config: gather.variables.%s.pattern: must have exactly one capture group, got %d", name, re.NumSubexp())
		}
		for _, from := range v.From {
			if !contains(GatherSources, from) {
				return fmt.Errorf("config: gather.variables.%s.from: %q must be one of %v", name, from, GatherSources)
			}
		}
		if v.Max < 1 || v.Max > 10 {
			return fmt.Errorf("config: gather.variables.%s.max: must be 1-10, got %d", name, v.Max)
		}
	}

	seenStepNames := make(map[string]bool, len(c.Gather.Steps))
	for i, s := range c.Gather.Steps {
		if !gatherStepName.MatchString(s.Name) {
			return fmt.Errorf("config: gather.steps[%d].name: %q must match ^[a-z0-9][a-z0-9-]{0,39}$", i, s.Name)
		}
		if seenStepNames[s.Name] {
			return fmt.Errorf("config: gather.steps[%d].name: %q is not unique", i, s.Name)
		}
		seenStepNames[s.Name] = true

		hasCommand := len(s.Command) > 0
		hasMCP := s.MCP != nil
		if hasCommand == hasMCP {
			if hasCommand {
				return fmt.Errorf("config: gather.steps[%d]: exactly one of command or mcp, got both", i)
			}
			return fmt.Errorf("config: gather.steps[%d]: exactly one of command or mcp, got neither", i)
		}
		for _, k := range s.Kinds {
			if !contains(RunKinds, k) {
				return fmt.Errorf("config: gather.steps[%d].kinds: %q must be one of %v", i, k, RunKinds)
			}
		}
		if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 300 {
			return fmt.Errorf("config: gather.steps[%d].timeoutSeconds: must be 1-300, got %d", i, s.TimeoutSeconds)
		}

		declared := func(name string) bool {
			_, ok := c.Gather.Variables[name]
			return ok
		}
		if hasCommand {
			if strings.Contains(s.Command[0], "{{") {
				return fmt.Errorf("config: gather.steps[%d].command[0]: %q must not reference a variable", i, s.Command[0])
			}
			for _, elem := range s.Command {
				for _, m := range gatherTemplateRef.FindAllStringSubmatch(elem, -1) {
					if !declared(m[1]) {
						return fmt.Errorf("config: gather.steps[%d].command: %q names undeclared variable %q", i, elem, m[1])
					}
				}
			}
		}
		if hasMCP {
			if s.MCP.Server == "" {
				return fmt.Errorf("config: gather.steps[%d].mcp.server: must not be empty", i)
			}
			if s.MCP.Tool == "" {
				return fmt.Errorf("config: gather.steps[%d].mcp.tool: must not be empty", i)
			}
			if err := walkGatherArgs(s.MCP.Args, declared); err != nil {
				return fmt.Errorf("config: gather.steps[%d].mcp.args: %w", i, err)
			}
		}
	}

	for i, host := range c.Repro.Hosts {
		if reason := provider.ValidateFetchEntry(host); reason != "" {
			return fmt.Errorf("config: repro.hosts[%d]: %q %s", i, host, reason)
		}
	}
	if !mcpclient.ValidServerName(c.Repro.Server) {
		return fmt.Errorf("config: repro.server: %q is not a usable MCP server name", c.Repro.Server)
	}
	for _, k := range c.Repro.Kinds {
		if k == "rca" {
			return fmt.Errorf("config: repro.kinds: rca is not allowed; reproduction is for triage and session runs")
		}
		if k != "triage" && k != "session" {
			return fmt.Errorf("config: repro.kinds: %q must be one of [triage session]", k)
		}
	}

	return nil
}

// walkGatherArgs checks an mcp.args tree for a "{{...}}" in a map key
// (always an error) and, in every string leaf, that each "{{name}}"
// reference names a declared variable.
func walkGatherArgs(v any, declared func(string) bool) error {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			if strings.Contains(k, "{{") {
				return fmt.Errorf("map key %q must not reference a variable", k)
			}
			if err := walkGatherArgs(sub, declared); err != nil {
				return err
			}
		}
	case []any:
		for _, sub := range t {
			if err := walkGatherArgs(sub, declared); err != nil {
				return err
			}
		}
	case string:
		for _, m := range gatherTemplateRef.FindAllStringSubmatch(t, -1) {
			if !declared(m[1]) {
				return fmt.Errorf("%q names undeclared variable %q", t, m[1])
			}
		}
	}
	return nil
}
