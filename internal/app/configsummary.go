package app

import (
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// ConfigSummary is the read-only view Settings shows of the two blocks an
// operator most often wants to check without opening the YAML: where a
// finished run posts, and which inbound hooks are served.
//
// Nothing here is resolved. Every credential a workspace configures is a
// reference ("env:NAME", "keychain:SERVICE"), and even the reference's own
// name is left out: what crosses is the scheme alone — "env", "keychain",
// "file", "cmd" — which says where the secret comes from and names
// neither it nor the variable holding it.
type ConfigSummary struct {
	General     GeneralSummary     `json:"general"`
	Budget      BudgetSummary      `json:"budget"`
	Permissions PermissionsSummary `json:"permissions"`
	Notes       NotesSummary       `json:"notes"`
	MCP         MCPSummary         `json:"mcp"`
	Notify      NotifySummary      `json:"notify"`
	Webhooks    WebhooksSummary    `json:"webhooks"`
	// Sources names the tracker and the helpdesk the workspace reads, so a
	// ticket number can be drawn under its own product's mark.
	Sources SourcesSummary `json:"sources"`
	// Me is who the workspace thinks the reader is, and which rule said
	// so. It is an identity, not a credential: the same address a ticket
	// already carries.
	Me MeSummary `json:"me"`
}

// MeSummary is the reader's identity as the Settings "You" row shows it:
// the address, every other spelling the config named, and which of the four
// rules in config.Config.Self answered — "me", "webhooks", "sources",
// "git", or "" for a workspace that can name nobody.
type MeSummary struct {
	Email  string   `json:"email"`
	Names  []string `json:"names"`
	Source string   `json:"source"`
}

// GeneralSummary is the top of the config file as the General page shows
// it: what the workspace is called, where it is, and which provider a new
// session gets. ConfigPath is the file every other row on the page points
// the operator at, since the app reads it and never writes it.
type GeneralSummary struct {
	Workspace  string `json:"workspace"`
	Root       string `json:"root"`
	ConfigPath string `json:"configPath"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	// Billing is "subscription" or "api".
	Billing string `json:"billing"`
	// FallbackModels is providers.claude.fallbackModels: the models a run
	// moves on to, in order, when the login turns out to have no room for
	// the one it was started with. Empty means a per-model limit stops the
	// run and waits for a person, which is the default. The session
	// screen's banner offers the first of these first.
	FallbackModels []string `json:"fallbackModels,omitempty"`
	// NotesLanguage and CustomerLanguage are the resolved codes, so a
	// workspace that named neither reads "en" and "auto" rather than blank.
	NotesLanguage    string `json:"notesLanguage"`
	CustomerLanguage string `json:"customerLanguage"`
	// RTLMarkup says whether the embedded templates wrap a right-to-left
	// paragraph in a <div dir="rtl"> block.
	RTLMarkup bool `json:"rtlMarkup"`
}

// BudgetSummary is the budget block with the stall timeout resolved: an
// unset stallMinutes reads as the default and 0 as "off".
type BudgetSummary struct {
	MaxTurns     int     `json:"maxTurns"`
	MaxMinutes   int     `json:"maxMinutes"`
	MaxUSD       float64 `json:"maxUsd"`
	StallMinutes int     `json:"stallMinutes"`
}

// PermissionsSummary is every allow-list a session is judged by. They are
// patterns an operator wrote, never credentials, and an empty list is
// reported as an empty list so the page can say what empty means for it.
type PermissionsSummary struct {
	Bash     []string `json:"bash"`
	FixBash  []string `json:"fixBash"`
	Fetch    []string `json:"fetch"`
	ReadAlso []string `json:"readAlso"`
	MCP      []string `json:"mcp"`
}

// NotesSummary is where notes go and what they are called. Templates is
// empty for a workspace on the embedded defaults.
type NotesSummary struct {
	Dir       string        `json:"dir"`
	Templates string        `json:"templates,omitempty"`
	Filenames NoteFilenames `json:"filenames"`
}

// NoteFilenames is the pattern each kind of note is filed under.
type NoteFilenames struct {
	Triage     string `json:"triage"`
	RCA        string `json:"rca"`
	Resolution string `json:"resolution"`
}

// MCPSummary is the one MCP setting that is not a server: whether a
// session sees the workspace's .mcp.json alone.
type MCPSummary struct {
	WorkspaceOnly bool `json:"workspaceOnly"`
}

// NotifySummary is the notify block with its destinations flattened into
// one list, in the order slack, teams, then the generic hooks as written.
type NotifySummary struct {
	// Enabled is false for a workspace with no notify block at all,
	// which posts nothing.
	Enabled      bool                `json:"enabled"`
	On           []string            `json:"on"`
	IncludeTitle bool                `json:"includeTitle"`
	Destinations []NotifyDestination `json:"destinations"`
}

// NotifyDestination is one place a finished run's metadata is posted.
type NotifyDestination struct {
	// Type is "slack", "teams" or "generic".
	Type string `json:"type"`
	// Credential is the scheme of the credential reference behind the
	// destination — "env", "keychain", "file", "cmd" — and empty for a
	// destination that needs none.
	Credential string `json:"credential,omitempty"`
	// Target is where a generic hook posts, cut back to scheme and host:
	// a receiver's path can carry its own authorisation, so only the
	// machine being posted to is reported. Empty for slack and teams,
	// whose whole URL is the secret.
	Target string `json:"target,omitempty"`
	// Headers names the extra headers a generic hook sends. The names
	// only: a header's value is a credential reference or a literal, and
	// neither is reported.
	Headers []string `json:"headers,omitempty"`
	// Signed says whether a generic hook's deliveries carry an HMAC.
	Signed bool `json:"signed,omitempty"`
}

// WebhooksSummary is the inbound half: which sources are served, how they
// authenticate, and the filter and cooldown a delivery has to get past.
type WebhooksSummary struct {
	Enabled bool `json:"enabled"`
	// Cooldown is how long a key is left alone after a triage of it,
	// written the way the config does ("10m0s").
	Cooldown string                 `json:"cooldown"`
	Match    WebhookMatchSummary    `json:"match"`
	Sources  []WebhookSourceSummary `json:"sources"`
}

// WebhookMatchSummary is the filter a verified delivery has to pass.
// Assignee is reported as written, including the literal "me": it is the
// operator's own account name, not a credential.
type WebhookMatchSummary struct {
	Assignee string   `json:"assignee,omitempty"`
	Statuses []string `json:"statuses,omitempty"`
	Labels   []string `json:"labels,omitempty"`
}

// WebhookSourceSummary is one enabled inbound source, by name.
type WebhookSourceSummary struct {
	Name string `json:"name"`
	// Auth is "secret" for a source that signs its deliveries and
	// "basic" for one that authenticates with a username and password.
	Auth string `json:"auth"`
	// Credential is the scheme of the reference holding the secret or
	// the password: "env", "keychain", "file" or "cmd".
	Credential string `json:"credential,omitempty"`
	// Proxy is the public address configured for a source that signs the
	// URL it called. It is an address, never a credential.
	Proxy string `json:"proxy,omitempty"`
}

// ConfigSummary reports the workspace's notify and webhooks configuration
// with every credential reduced to its scheme.
func (s *Service) ConfigSummary(wsID string) (ConfigSummary, error) {
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return ConfigSummary{}, err
	}
	return SummariseConfigWith(cfg, sourceHintsIn(ws.Root)), nil
}

// SummariseConfig builds the summary from a loaded configuration. It is
// exported so the redaction can be tested against a config built by hand.
func SummariseConfig(cfg *config.Config) ConfigSummary {
	return SummariseConfigWith(cfg, SourceHints{})
}

// SummariseConfigWith is SummariseConfig told what the run directories
// know: the ticket URLs an exec adapter's host and name are read from.
func SummariseConfigWith(cfg *config.Config, hints SourceHints) ConfigSummary {
	return ConfigSummary{
		Sources:     summariseSources(cfg, hints),
		General:     summariseGeneral(cfg),
		Budget:      summariseBudget(cfg),
		Permissions: summarisePermissions(cfg),
		Notes:       summariseNotes(cfg),
		MCP:         MCPSummary{WorkspaceOnly: cfg.WorkspaceOnlyMCP()},
		Notify:      summariseNotify(cfg.Notify),
		Webhooks:    summariseWebhooks(&cfg.Webhooks),
		Me:          summariseMe(cfg),
	}
}

// summariseMe reports the resolved identity, names included, so the page can
// say both who you are and how Sirdar worked that out. Names is always a
// list on the wire, never null, so the UI need not test for it.
func summariseMe(cfg *config.Config) MeSummary {
	id := cfg.Self()
	names := id.Names
	if names == nil {
		names = []string{}
	}
	return MeSummary{Email: id.Email, Names: names, Source: id.Source}
}

func summariseGeneral(cfg *config.Config) GeneralSummary {
	out := GeneralSummary{
		Workspace:        cfg.Workspace,
		Root:             cfg.Root,
		Provider:         string(cfg.Provider),
		Model:            cfg.Model,
		Billing:          cfg.Billing,
		NotesLanguage:    cfg.NotesLanguage(),
		CustomerLanguage: cfg.CustomerLanguage(),
		RTLMarkup:        cfg.RTLMarkup(),
		FallbackModels:   cfg.FallbackModels(),
	}
	if cfg.Root != "" {
		out.ConfigPath = filepath.Join(cfg.Root, ".sirdar", "config.yaml")
	}
	return out
}

func summariseBudget(cfg *config.Config) BudgetSummary {
	return BudgetSummary{
		MaxTurns:     cfg.Budget.MaxTurns,
		MaxMinutes:   cfg.Budget.MaxMinutes,
		MaxUSD:       cfg.Budget.MaxUSD,
		StallMinutes: cfg.StallMinutes(),
	}
}

func summarisePermissions(cfg *config.Config) PermissionsSummary {
	return PermissionsSummary{
		Bash:     listOf(cfg.Permissions.Bash),
		FixBash:  listOf(cfg.Permissions.FixBash),
		Fetch:    listOf(cfg.Permissions.Fetch),
		ReadAlso: listOf(cfg.Permissions.ReadAlso),
		MCP:      listOf(cfg.Permissions.MCP),
	}
}

func summariseNotes(cfg *config.Config) NotesSummary {
	out := NotesSummary{}
	if cfg.Notes.Dir != "" {
		out.Dir = cfg.ExpandPath(cfg.Notes.Dir)
	}
	if cfg.Notes.Templates != "" {
		out.Templates = cfg.ExpandPath(cfg.Notes.Templates)
	}
	out.Filenames = NoteFilenames{
		Triage:     cfg.Notes.Filenames.Triage,
		RCA:        cfg.Notes.Filenames.RCA,
		Resolution: cfg.Notes.Filenames.Resolution,
	}
	return out
}

// listOf copies a list so the summary owns it, and turns a nil into an
// empty list: the frontend maps over every one of these.
func listOf(in []string) []string {
	out := make([]string, 0, len(in))
	return append(out, in...)
}

func summariseNotify(n *config.NotifyConfig) NotifySummary {
	out := NotifySummary{On: []string{}, Destinations: []NotifyDestination{}}
	if n == nil {
		return out
	}
	out.Enabled = true
	out.IncludeTitle = n.IncludeTitle
	if len(n.On) > 0 {
		out.On = append(out.On, n.On...)
	} else {
		// An empty list means every terminal state, which is what the
		// operator is being shown rather than a blank.
		for state := range config.NotifyStates {
			out.On = append(out.On, state)
		}
		sort.Strings(out.On)
	}

	for _, d := range []struct {
		kind string
		w    *config.WebhookConfig
	}{{"slack", n.Slack}, {"teams", n.Teams}} {
		if d.w == nil {
			continue
		}
		out.Destinations = append(out.Destinations, NotifyDestination{
			Type:       d.kind,
			Credential: credentialScheme(d.w.WebhookURL),
		})
	}
	for _, g := range n.Generic {
		dest := NotifyDestination{
			Type:    "generic",
			Target:  originOf(g.URL),
			Signed:  g.Secret != "",
			Headers: headerNames(g.Headers),
		}
		if g.Secret != "" {
			dest.Credential = credentialScheme(g.Secret)
		}
		out.Destinations = append(out.Destinations, dest)
	}
	return out
}

func summariseWebhooks(w *config.WebhooksConfig) WebhooksSummary {
	out := WebhooksSummary{
		Enabled: w.Enabled,
		Sources: []WebhookSourceSummary{},
		Match: WebhookMatchSummary{
			Assignee: w.Match.Assignee,
			Statuses: w.Match.Statuses,
			Labels:   w.Match.Labels,
		},
	}
	cooldown := config.DefaultWebhookCooldown
	if w.Cooldown != nil {
		cooldown = *w.Cooldown
	}
	out.Cooldown = cooldown.String()

	names := make([]string, 0, len(w.Sources))
	for name := range w.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sc := w.Sources[name]
		if sc == nil {
			continue
		}
		row := WebhookSourceSummary{Name: name, Auth: "secret", Credential: credentialScheme(sc.Secret)}
		if sc.Password != "" || sc.Username != "" {
			row.Auth = "basic"
			row.Credential = credentialScheme(sc.Password)
		}
		if sc.Proxy != nil {
			row.Proxy = proxyAddress(sc.Proxy)
		}
		out.Sources = append(out.Sources, row)
	}
	return out
}

// credentialScheme reduces a credential reference to the scheme in front
// of it. The name after the colon is dropped with the value: an env var's
// name is a good guess at where the secret is, and a summary has no reason
// to hand it out.
func credentialScheme(ref string) string {
	scheme, _, ok := strings.Cut(ref, ":")
	if !ok || scheme == "" {
		return ""
	}
	return scheme
}

// originOf cuts a URL back to scheme and host. A generic receiver's path
// and query can carry its own authorisation, so only the machine being
// posted to is reported.
func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// headerNames lists a generic hook's header names, sorted. The values are
// left out: one of them is where the receiver's token goes.
func headerNames(headers map[string]string) []string {
	if len(headers) == 0 {
		return nil
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func proxyAddress(p *config.WebhookProxy) string {
	scheme := strings.TrimSpace(p.Scheme)
	host := strings.TrimSpace(p.Host)
	switch {
	case scheme != "" && host != "":
		return scheme + "://" + host
	case host != "":
		return host
	default:
		return scheme + "://"
	}
}
