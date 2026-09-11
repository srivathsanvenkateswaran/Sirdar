package httpx

import (
	"fmt"
	"net/http"
)

// DefaultMaxRedirects is how many hops a request may take before it is
// abandoned. Three is enough for the one or two hops a real attachment
// service uses and short enough that a redirect loop costs nothing.
const DefaultMaxRedirects = 3

// RedirectPolicy returns an http.Client.CheckRedirect that applies t to
// every hop and stops the chain after maxHops.
//
// It exists because Go's own handling is not enough: Go strips the
// Authorization header on a cross-host hop but still makes the request and
// still writes the answer to disk, and it strips nothing at all from a
// custom auth header like Rally's ZSESSIONID. A Location header arrives
// inside a server response, which makes it input rather than configuration,
// so refusing the hop outright — with an error that names the host it was
// sent to and nothing else — is the actual fix.
func RedirectPolicy(t *Trust, maxHops int) func(*http.Request, []*http.Request) error {
	if maxHops <= 0 {
		maxHops = DefaultMaxRedirects
	}
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxHops {
			return fmt.Errorf("stopped after %d redirects", maxHops)
		}
		if fetch, _, reason := t.Check(req.URL); !fetch {
			return fmt.Errorf("refusing to follow a redirect to %s: %s", req.URL.Hostname(), reason)
		}
		return nil
	}
}

// Client returns a shallow copy of base carrying RedirectPolicy(t, maxHops).
// The copy shares base's Transport (and its pooled connections) and keeps
// its Timeout; only the redirect policy is replaced. A nil base gets a
// zero-value client, which is http.DefaultClient's behaviour without its
// shared state.
func Client(base *http.Client, t *Trust, maxHops int) *http.Client {
	var c http.Client
	if base != nil {
		c = *base
	}
	c.CheckRedirect = RedirectPolicy(t, maxHops)
	return &c
}

// RedirectPolicyStop is RedirectPolicy for a caller that would rather see
// the redirect response than an error: an untrusted hop stops the chain with
// http.ErrUseLastResponse, leaving the 3xx for the caller to report in its
// own words.
//
// That matters where the message reaches a log or a prompt. Go wraps a
// CheckRedirect error in a *url.Error carrying the full redirect target, and
// an SSO Location carries the original URL and sometimes a token in its
// query; a caller that stops here can name the target by scheme and host and
// drop the rest. The hop cap is still an error, since there is no single
// response to point at.
func RedirectPolicyStop(t *Trust, maxHops int) func(*http.Request, []*http.Request) error {
	if maxHops <= 0 {
		maxHops = DefaultMaxRedirects
	}
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxHops {
			return fmt.Errorf("stopped after %d redirects", maxHops)
		}
		if fetch, _, _ := t.Check(req.URL); !fetch {
			return http.ErrUseLastResponse
		}
		return nil
	}
}
