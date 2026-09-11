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
	// ProxyScheme and ProxyHost are the public scheme and host a reverse
	// proxy receives the delivery on, for the one source that signs the
	// URL it called. They come from the operator's configuration: they say
	// what the proxy in front of this process is, which is not something a
	// caller may assert. Empty means there is no proxy, and the request's
	// own scheme and host are used.
	ProxyScheme, ProxyHost string
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

// signedURISources are the sources whose signature covers the URL they
// called, and so the only ones a proxy block means anything to.
var signedURISources = map[string]bool{SourceHubSpot: true}

// SignsURI reports whether a source signs the URL it posted to, which is
// what makes webhooks.sources.<name>.proxy worth setting. Config
// validation asks this rather than repeating the list.
func SignsURI(name string) bool { return signedURISources[name] }

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
		return HubSpot{Secret: a.Secret, ProxyScheme: a.ProxyScheme, ProxyHost: a.ProxyHost, clock: c}, nil
	case SourceGeneric:
		return Generic{Secret: a.Secret}, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownSource, name)
}
