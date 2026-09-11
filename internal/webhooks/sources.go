package webhooks

import (
	"fmt"
	"sort"
	"time"
)

// Source names, one per verifier. They are the last path segment of a hook
// URL and the keys under webhooks.sources in the workspace configuration.
const (
	SourceJira      = "jira"
	SourceLinear    = "linear"
	SourceAzDO      = "azdo"
	SourceRally     = "rally"
	SourceZendesk   = "zendesk"
	SourceFreshdesk = "freshdesk"
	SourceIntercom  = "intercom"
	SourceHubSpot   = "hubspot"
	SourceGeneric   = "generic"
)

// Auth is one source's resolved credentials. Every field arrives already
// resolved from a credential reference: nothing in this package reads an
// environment variable or a keychain.
type Auth struct {
	// Secret is the shared secret or signing secret, for every source but
	// azdo.
	Secret string
	// Username and Password are the basic-auth pair an Azure DevOps
	// service hook is configured with.
	Username, Password string
	// Now is the clock the replay windows read; nil means time.Now.
	Now func() time.Time
}

// basicAuthSources are the sources authenticated by a username and
// password rather than by a secret. Config validation asks this rather
// than repeating the list.
var basicAuthSources = map[string]bool{SourceAzDO: true}

// UsesBasicAuth reports whether a source is configured with a username and
// password instead of a secret.
func UsesBasicAuth(name string) bool { return basicAuthSources[name] }

// Known reports whether name is a source this package can verify.
func Known(name string) bool {
	switch name {
	case SourceJira, SourceLinear, SourceAzDO, SourceRally,
		SourceZendesk, SourceFreshdesk, SourceIntercom, SourceHubSpot, SourceGeneric:
		return true
	}
	return false
}

// KnownSources lists every source name, sorted, for an error message that
// has to say what the valid ones are.
func KnownSources() []string {
	names := []string{
		SourceJira, SourceLinear, SourceAzDO, SourceRally,
		SourceZendesk, SourceFreshdesk, SourceIntercom, SourceHubSpot, SourceGeneric,
	}
	sort.Strings(names)
	return names
}

// Build returns the verifier for one source.
func Build(name string, a Auth) (Verifier, error) {
	c := clock{Now: a.Now}
	switch name {
	case SourceJira:
		return Jira{Secret: a.Secret}, nil
	case SourceLinear:
		return Linear{Secret: a.Secret, clock: c}, nil
	case SourceAzDO:
		return AzDO{Username: a.Username, Password: a.Password}, nil
	case SourceRally:
		return Rally{Secret: a.Secret}, nil
	case SourceZendesk:
		return Zendesk{Secret: a.Secret, clock: c}, nil
	case SourceFreshdesk:
		return Freshdesk{Secret: a.Secret}, nil
	case SourceIntercom:
		return Intercom{Secret: a.Secret}, nil
	case SourceHubSpot:
		return HubSpot{Secret: a.Secret, clock: c}, nil
	case SourceGeneric:
		return Generic{Secret: a.Secret}, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownSource, name)
}
