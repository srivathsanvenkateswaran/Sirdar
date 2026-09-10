// Package freshdesk implements source.Helpdesk against the Freshdesk v2 REST
// API: ticket lookup, paginated conversation retrieval, and attachment
// download.
//
// Auth is the simplest of any Sirdar adapter: HTTP Basic with the account
// API key as the username and the literal string "X" as the password — no
// OAuth, no token refresh. The one piece of care this adapter takes that a
// plain REST client would not: attachment URLs are pre-signed links that can
// point off the configured account (Freshdesk's CDN, or — since the URL
// arrives inside an API response body and is therefore attacker-editable in
// a compromised or hostile instance — anywhere else), so the API key is only
// ever sent to the exact configured domain, never to a host merely
// recognised as a first-party Freshdesk one.
package freshdesk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
)

// maxRetryAfter bounds how long a 429's Retry-After is honoured before the
// call gives up and reports source.RateLimited instead of blocking a triage
// run behind a long quota reset.
const maxRetryAfter = 30 * time.Second

// Response body ceilings, enforced with a limited reader that fails closed:
// a truncated body is never decoded as if it were complete.
const (
	// maxJSONBody bounds an ordinary API JSON response (a ticket, a page of
	// conversations, an agent record).
	maxJSONBody = 8 << 20
	// maxAttachmentBytes bounds a downloaded attachment's raw bytes.
	maxAttachmentBytes = 64 << 20
	// maxAgentCacheEntries bounds the agent name cache: past this many
	// distinct agents seen by one Client, the cache is dropped and rebuilt
	// rather than left to grow without limit.
	maxAgentCacheEntries = 1000
)

// conversationsPerPage is the page size used when listing a ticket's
// conversations. It is a var, not a const, so a test can shrink it to
// exercise pagination without a 100-entry fixture.
var conversationsPerPage = 100

// trustedSuffixes are the first-party Freshworks hosts an attachment
// download is allowed to reach even when they are not the configured
// account domain — but never with the API key, since a request to any of
// them but the configured domain itself goes out unauthenticated.
var trustedSuffixes = []string{"freshdesk.com", "freshcloud.io", "freshworksapi.com"}

// Config holds one Freshdesk account's settings. Secrets arrive already
// resolved by the wiring layer, so every field is a plain string.
type Config struct {
	// Domain is the account's Freshdesk host, e.g. "acme.freshdesk.com".
	// A "https://" prefix or trailing slash, if present, is trimmed.
	Domain string
	// APIKey is sent as the HTTP Basic username, with the literal string
	// "X" as the password — Freshdesk's only documented auth mode.
	APIKey string
}

// Client talks to one Freshdesk account. It implements source.Helpdesk and
// source.Warner.
type Client struct {
	cfg     Config
	baseURL string // "https://" + cfg.Domain, no trailing slash
	// host is the one host this client's credential is ever sent to,
	// normalised (lowercased, default port dropped) so a URL taken out of
	// an API response can be compared against it.
	host string
	hc   *http.Client

	// mu guards the caches below. One Client serves every ticket in a run,
	// so two tickets can be inside a call at once.
	mu sync.Mutex
	// warnings holds the non-fatal problems each Attachments call recorded,
	// keyed by the ticket id it was called with. An entry is written when
	// the call ends and removed when it is read, so a stale warning from an
	// earlier call for the same ticket is never handed to the next reader.
	warnings map[string][]string
	// agents caches resolved agent display names by id, since the same
	// agent typically appears on several messages in one ticket's thread.
	agents map[int64]string
}

var (
	_ source.Helpdesk = (*Client)(nil)
	_ source.Warner   = (*Client)(nil)
)

// New validates cfg and returns a Client. hc may be nil, in which case a
// client with a 30 s timeout is used.
func New(cfg Config, hc *http.Client) (*Client, error) {
	domain := strings.TrimSpace(cfg.Domain)
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimSuffix(domain, "/")
	if domain == "" {
		return nil, fmt.Errorf("freshdesk: domain is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("freshdesk: apiKey is required")
	}
	cfg.Domain = domain

	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	c := &Client{
		cfg:     cfg,
		baseURL: "https://" + domain,
		host:    hostKey("https", domain),
	}
	// A shallow copy: the Transport (and any pooled connections) is shared
	// with the caller's client, only the redirect policy is ours. Every
	// request this adapter makes — ticket/conversation/agent lookups as
	// much as attachment downloads — goes through this same *http.Client,
	// so a server response that tries to redirect any of them off the
	// trusted hosts is refused uniformly, not just on the download path.
	dl := *hc
	dl.CheckRedirect = c.checkRedirect
	c.hc = &dl
	return c, nil
}

// maxRedirects bounds how far a same-trust-boundary redirect chain is
// followed before the request is abandoned.
const maxRedirects = 3

// checkRedirect refuses to follow a redirect off the hosts this client
// trusts with its credential or its downloads (see attachmentTrust): a
// redirect Location comes back inside a server response, which makes it
// input, not configuration, and nothing stops a hostile or compromised
// endpoint — including a legitimately trusted one that has been
// compromised, or a CDN host whose pre-signed link expired into a generic
// error/login redirect — from pointing one at a host it controls. Go
// already strips the Authorization header on a cross-host hop, but that
// still lets the request happen and the response get written to disk as if
// it were the real attachment; refusing the hop entirely, with an error
// that names the offending host, is the actual fix.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("freshdesk: stopped after %d redirects", maxRedirects)
	}
	host := hostKey(req.URL.Scheme, req.URL.Host)
	if trusted, _ := c.attachmentTrust(host); !trusted {
		return fmt.Errorf("freshdesk: redirect to untrusted host %s", host)
	}
	return nil
}

// hostKey renders a scheme+host pair comparable: lowercased, with the DNS
// root's trailing dot removed and the scheme's default port dropped, so
// "Acme.Freshdesk.com.:443" and "acme.freshdesk.com" are recognised as one
// host.
func hostKey(scheme, host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		// No port at all, or a bare IPv6 literal.
		return strings.TrimSuffix(host, ".")
	}
	name = strings.TrimSuffix(name, ".")
	scheme = strings.ToLower(scheme)
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return name
	}
	return net.JoinHostPort(name, port)
}

// attachmentTrust reports whether host (already hostKey-normalised) may be
// downloaded from at all, and whether the client's API key may be sent
// there. Only the exact configured domain gets the credential: Freshdesk's
// other first-party hosts (its attachment CDN, principally) are trusted for
// download because their URLs are pre-signed, but they never see the key.
func (c *Client) attachmentTrust(host string) (trusted, sendAuth bool) {
	if host == c.host {
		return true, true
	}
	for _, suf := range trustedSuffixes {
		if host == suf || strings.HasSuffix(host, "."+suf) {
			return true, false
		}
	}
	return false, false
}

// Ping checks the credential by reading the authenticated agent's own
// record, giving doctor a real round trip against the configured account.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.doRaw(ctx, c.baseURL+"/api/v2/agents/me", true, maxJSONBody)
	return err
}

// apiGET issues an authenticated GET against path (relative to baseURL) and
// decodes a 2xx JSON response into out.
func (c *Client) apiGET(ctx context.Context, path string, query url.Values, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	body, err := c.doRaw(ctx, u, true, maxJSONBody)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: decode: %v", logPath(u), err)}
	}
	return nil
}

// doRaw issues one GET against rawURL — with the account's Basic auth
// header when withAuth is true, unauthenticated otherwise — and returns the
// response body for a 2xx result, mapping anything else to a *source.Error.
// A 429 is retried exactly once, after honouring a Retry-After of at most
// maxRetryAfter; a longer or absent one is reported as source.RateLimited
// without waiting.
func (c *Client) doRaw(ctx context.Context, rawURL string, withAuth bool, limit int64) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: %v", logPath(rawURL), err)}
		}
		if withAuth {
			req.SetBasicAuth(c.cfg.APIKey, "X")
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: %v", logPath(rawURL), err)}
		}
		body, readErr := readLimited(resp.Body, limit)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if d, ok := retryAfter(resp.Header); ok {
				if err := sleepCtx(ctx, d); err != nil {
					return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: %v", logPath(rawURL), err)}
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, statusError(logPath(rawURL), resp.StatusCode, body)
		}
		if readErr != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: read body: %v", logPath(rawURL), readErr)}
		}
		return body, nil
	}
}

// readLimited reads at most limit bytes and fails when the reader had more
// to give, rather than returning a body that would decode as a short but
// well-formed result.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return body, err
	}
	if int64(len(body)) > limit {
		return body[:limit], fmt.Errorf("response exceeds the %d byte limit", limit)
	}
	return body, nil
}

// retryAfter reads a Retry-After header in either of its documented forms
// (delta-seconds or an HTTP date) and reports whether the wait is short
// enough to sit through.
func retryAfter(h http.Header) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
		if d < 0 {
			d = 0
		}
	} else {
		return 0, false
	}
	if d > maxRetryAfter {
		return 0, false
	}
	return d, true
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// logPath reduces a request URL to its path, dropping the query string so a
// pre-signed attachment URL's signature (or any other query parameter)
// never lands in a log line or warning. The credential travels only in a
// header and so is never at risk here either way.
func logPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<url>"
	}
	return u.Path
}

// statusError maps a non-2xx HTTP response to a *source.Error, quoting at
// most 200 bytes of the body and never the credential.
func statusError(path string, status int, body []byte) *source.Error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("freshdesk: GET %s: %d", path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("freshdesk: GET %s: %d", path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("freshdesk: GET %s: %d", path, status)}
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: %d: %s", path, status, snippet)}
	}
}

// --- warnings ---

// putWarnings files the problems one Attachments call skipped over under
// its ticket, replacing anything an earlier call for the same ticket left
// behind.
func (c *Client) putWarnings(id string, warnings []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(warnings) == 0 {
		delete(c.warnings, id)
		return
	}
	if c.warnings == nil {
		c.warnings = map[string][]string{}
	}
	c.warnings[id] = append([]string(nil), warnings...)
}

// takeWarnings returns and removes the warnings recorded for ticket id.
func (c *Client) takeWarnings(id string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.warnings[id]
	delete(c.warnings, id)
	if len(w) == 0 {
		return nil
	}
	return append([]string(nil), w...)
}

// WarningsFor implements source.Warner: it returns and consumes the
// per-attachment failures the Attachments call for ticket id recorded, so
// the caller can put them in front of the agent and the operator instead of
// a partial result that quietly looks complete. It is keyed by id, which is
// what a caller running several tickets at once needs: it cannot be handed
// another ticket's missing evidence.
func (c *Client) WarningsFor(id string) []string {
	return c.takeWarnings(id)
}

// --- agent name resolution ---

type fdAgent struct {
	Contact struct {
		Name string `json:"name"`
	} `json:"contact"`
}

// lookupAgent resolves an agent id to its display name, caching the result:
// the same agent typically appears on several messages in one ticket's
// conversation.
func (c *Client) lookupAgent(ctx context.Context, id int64) (string, error) {
	c.mu.Lock()
	if name, ok := c.agents[id]; ok {
		c.mu.Unlock()
		return name, nil
	}
	c.mu.Unlock()

	var ag fdAgent
	if err := c.apiGET(ctx, "/api/v2/agents/"+strconv.FormatInt(id, 10), nil, &ag); err != nil {
		return "", err
	}

	c.mu.Lock()
	if c.agents == nil || len(c.agents) > maxAgentCacheEntries {
		// A triage run's per-ticket agent set is small in practice; a cache
		// that grew past this is more likely a pathological ticket (or a
		// client reused across far more agents than one account plausibly
		// has) than a workload worth holding onto indefinitely. Dropping it
		// wholesale trades a few repeat lookups for a bounded footprint
		// rather than attempting an LRU for what should be a rare case.
		c.agents = map[int64]string{}
	}
	c.agents[id] = ag.Contact.Name
	c.mu.Unlock()
	return ag.Contact.Name, nil
}
