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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
)

// maxRetryAfter bounds how long a 429's Retry-After is honoured before the
// call gives up and reports source.RateLimited instead of blocking a triage
// run behind a long quota reset.
const maxRetryAfter = httpx.MaxRetryAfter

// Response body ceilings, enforced with a limited reader that fails closed:
// a truncated body is never decoded as if it were complete.
const (
	// maxJSONBody bounds an ordinary API JSON response (a ticket, a page of
	// conversations, an agent record).
	maxJSONBody = 8 << 20
	// maxAgentCacheEntries bounds the agent name cache: past this many
	// distinct agents seen by one Client, the cache is dropped and rebuilt
	// rather than left to grow without limit.
	maxAgentCacheEntries = 1000
)

// conversationsPerPage is the page size used when listing a ticket's
// conversations. It is a var, not a const, so a test can shrink it to
// exercise pagination without a 100-entry fixture.
var conversationsPerPage = 100

// maxAttachmentBytes bounds a downloaded attachment's raw bytes. It is a
// var for the same reason: a test can shrink it rather than serve 64 MiB.
var maxAttachmentBytes int64 = 64 << 20

// trustedHosts are the first-party Freshworks hosts an attachment download
// is allowed to reach even when they are not the configured account domain
// — but never with the API key, since a request to any of them but the
// configured domain itself goes out unauthenticated. Their URLs are
// pre-signed, so they need no credential of ours.
var trustedHosts = []httpx.HostRule{
	{Suffix: ".freshdesk.com"},
	{Suffix: ".freshcloud.io"},
	{Suffix: ".freshworksapi.com"},
}

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
	// trust is the configured account domain — the one host this client's
	// credential is ever sent to — plus Freshworks' own attachment hosts,
	// which are fetched from unauthenticated.
	trust *httpx.Trust
	hc    *http.Client

	// warnings holds the non-fatal problems each call recorded, keyed by
	// the ticket id it was called with; reading is what clears an entry.
	warnings httpx.Warnings

	// mu guards the agent cache. One Client serves every ticket in a run,
	// so two tickets can be inside a call at once.
	mu sync.Mutex
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
	trust, err := httpx.NewTrust("https://"+domain, trustedHosts...)
	if err != nil {
		return nil, fmt.Errorf("freshdesk: invalid domain %q: %w", cfg.Domain, err)
	}
	// A shallow copy: the Transport (and any pooled connections) is shared
	// with the caller's client, only the redirect policy is ours. Every
	// request this adapter makes — ticket/conversation/agent lookups as
	// much as attachment downloads — goes through this same *http.Client,
	// so a server response that tries to redirect any of them off the
	// trusted hosts is refused uniformly, not just on the download path.
	//
	// A redirect Location comes back inside a server response, which makes
	// it input, not configuration, and nothing stops a hostile or
	// compromised endpoint — including a CDN host whose pre-signed link
	// expired into a generic error/login redirect — from pointing one at a
	// host it controls. Go already strips the Authorization header on a
	// cross-host hop, but that still lets the request happen and the
	// response get written to disk as if it were the real attachment.
	return &Client{
		cfg:     cfg,
		baseURL: "https://" + domain,
		trust:   trust,
		hc:      httpx.Client(hc, trust, maxRedirects),
	}, nil
}

// maxRedirects bounds how far a same-trust-boundary redirect chain is
// followed before the request is abandoned.
const maxRedirects = 3

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
			if host, ok := httpx.RedirectHost(err); ok {
				return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: redirect to untrusted host %s", logPath(rawURL), host)}
			}
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: %v", logPath(rawURL), err)}
		}
		body, readErr := httpx.ReadLimited(resp.Body, limit)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if d, ok := httpx.RetryAfter(resp.Header, maxRetryAfter); ok {
				if err := httpx.SleepCtx(ctx, d); err != nil {
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

// downloadTo fetches rawURL and streams the body straight to destPath,
// stopping at maxAttachmentBytes. Attachments are the one response this
// adapter never needs whole in memory, and a 64 MiB buffer per file — times
// however many files a ticket carries, times the tickets a run has in
// flight — is memory spent for nothing. A file that would exceed the limit
// is a download failure and its partial output is removed, so a truncated
// attachment is never left on disk looking complete.
func (c *Client) downloadTo(ctx context.Context, rawURL string, withAuth bool, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: %v", logPath(rawURL), err)}
	}
	if withAuth {
		req.SetBasicAuth(c.cfg.APIKey, "X")
	}
	req.Header.Set("Accept", "*/*")

	_, err = httpx.Download(ctx, c.hc, req, destPath, httpx.DownloadOptions{Max: maxAttachmentBytes})
	var se *httpx.StatusError
	host, refused := httpx.RedirectHost(err)
	switch {
	case err == nil:
		return nil
	case refused:
		// Only the host: Go's *url.Error carries the refused target's path
		// and query, and this error becomes a per-ticket warning.
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: redirect to untrusted host %s", logPath(rawURL), host)}
	case errors.As(err, &se):
		return statusError(logPath(rawURL), se.Status, se.Body)
	case errors.Is(err, httpx.ErrTooLarge):
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: attachment exceeds the %d byte limit", logPath(rawURL), maxAttachmentBytes)}
	default:
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: GET %s: %v", logPath(rawURL), err)}
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

// addWarnings files the problems one call skipped over under its ticket,
// alongside whatever an earlier call for the same ticket recorded.
//
// It appends rather than replaces because one ticket's bundle is assembled
// from several calls — internal/run's fetchBundle runs Get, then Threads,
// then Attachments, and reads WarningsFor once at the end. Reading is what
// clears the entry.
//
// An identical line is dropped: Threads and Attachments both page the same
// conversation feed, so a feed that stops at the page cap says so once
// rather than twice.
func (c *Client) addWarnings(id string, warnings []string) {
	c.warnings.Add(id, warnings...)
}

// WarningsFor implements source.Warner: it returns and consumes the
// per-attachment failures the Attachments call for ticket id recorded, so
// the caller can put them in front of the agent and the operator instead of
// a partial result that quietly looks complete. It is keyed by id, which is
// what a caller running several tickets at once needs: it cannot be handed
// another ticket's missing evidence.
func (c *Client) WarningsFor(id string) []string {
	return c.warnings.Take(id)
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
