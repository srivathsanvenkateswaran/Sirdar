package config

import "strings"

// ChildEnv is the environment a run's agent session and gather steps get:
// base minus every variable a configured credential reference names, plus
// SIRDAR_BILLING. Runs and `sirdar doctor` both build it here, so the
// doctor resolves programs and MCP servers in the same environment a run
// will, rather than in this process's whole one, which still holds the
// helpdesk's token. A nil base is taken as empty, never as os.Environ():
// the caller says which environment it is stripping.
func (c *Config) ChildEnv(base []string) []string {
	secret := c.CredentialEnvNames()
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if name, _, ok := strings.Cut(entry, "="); ok && secret[name] {
			continue
		}
		out = append(out, entry)
	}
	billing := ""
	if c != nil {
		billing = c.Billing
	}
	return append(out, "SIRDAR_BILLING="+billing)
}

// CredentialEnvNames collects the variable names behind every "env:"
// credential reference in the workspace configuration: the tracker's and
// helpdesk's tokens, OAuth client and refresh secrets, the model endpoint's
// API key, and every notify destination's webhook URL, secret and header.
// Each of them is something a session could use to act as Sirdar, so none
// of them is left in a child's environment.
//
// An OAuth grant's client secret and refresh token matter more here, not
// less, than a plain access token: a refresh token read out of the agent's
// environment mints access tokens until somebody revokes it at the Zoho
// console, and Help Scout's clientId/clientSecret pair mints tokens on
// demand, so it outlives every token it has issued. The model's API key
// belongs to Sirdar, not the session: the loop authenticates the chat calls
// itself. An incoming webhook URL is a bearer credential: a session that
// could read one could post to the team's channel as Sirdar.
func (c *Config) CredentialEnvNames() map[string]bool {
	names := make(map[string]bool)
	if c == nil {
		return names
	}
	refs := []string{}
	for _, s := range []*SourceConfig{c.Sources.Tracker, c.Sources.Helpdesk} {
		if s == nil {
			continue
		}
		refs = append(refs, s.Token, s.APIToken, s.PAT, s.APIKey, s.OAuthToken,
			s.ClientID, s.ClientSecret, s.AccessToken, s.Password)
		if s.Auth != nil {
			refs = append(refs, s.Auth.ClientID, s.Auth.ClientSecret, s.Auth.RefreshToken)
		}
	}
	if c.OpenAI != nil {
		refs = append(refs, c.OpenAI.APIKey)
	}
	if n := c.Notify; n != nil {
		for _, w := range []*WebhookConfig{n.Slack, n.Teams} {
			if w != nil {
				refs = append(refs, w.WebhookURL)
			}
		}
		for _, g := range n.Generic {
			refs = append(refs, g.Secret)
			for _, v := range g.Headers {
				refs = append(refs, v)
			}
		}
	}
	for _, ref := range refs {
		if name, ok := strings.CutPrefix(ref, "env:"); ok && name != "" {
			names[name] = true
		}
	}
	return names
}
