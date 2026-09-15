package app

import (
	"net/url"
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
	Notify   NotifySummary   `json:"notify"`
	Webhooks WebhooksSummary `json:"webhooks"`
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
	_, cfg, err := s.load(wsID)
	if err != nil {
		return ConfigSummary{}, err
	}
	return SummariseConfig(cfg), nil
}

// SummariseConfig builds the summary from a loaded configuration. It is
// exported so the redaction can be tested against a config built by hand.
func SummariseConfig(cfg *config.Config) ConfigSummary {
	return ConfigSummary{
		Notify:   summariseNotify(cfg.Notify),
		Webhooks: summariseWebhooks(&cfg.Webhooks),
	}
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
