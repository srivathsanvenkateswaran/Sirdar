// Package gorgias implements source.Helpdesk against the Gorgias REST API:
// ticket lookup, the cursor-paginated message feed that makes up the
// thread, and attachment download, over plain stdlib net/http.
//
// Gorgias gives every account its own host — https://{account}.gorgias.com
// — and a private integration authenticates with HTTP Basic: the account's
// login email as the username, the API key as the password. There is an
// OAuth2 flow as well, but it is for public apps distributed through
// Gorgias's app store, its access tokens expire in 24 hours, and a
// single-tenant read-only integration has no use for either half of that.
//
// The care this adapter takes that a plain REST client would not: an
// attachment's URL arrives inside an API response body, which makes it
// input rather than configuration, so it is host-checked before it is
// fetched. The API key goes only to the configured account host; every
// other Gorgias host is fetched from without it, and anything else is
// refused with the host named and nothing more.
package gorgias

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
)

// hostSuffix is the domain every Gorgias account lives under. An account
// identifier ("acme") plus this suffix is the account's API host.
const hostSuffix = ".gorgias.com"

// maxRetryAfter bounds how long a 429's Retry-After is honoured before the
// call gives up and reports source.RateLimited rather than blocking a
// triage run behind a long quota reset. Gorgias's leaky bucket refills
// fast — 40 requests per 20 seconds for an API-key integration — so a
// Retry-After longer than this is a quota problem, not a burst.
const maxRetryAfter = httpx.MaxRetryAfter

// maxJSONBody bounds an ordinary API JSON response (a ticket, a page of
// messages, the account record). It fails closed: a truncated body is
// never decoded as if it were complete.
const maxJSONBody = 8 << 20

// maxRedirects bounds how far a redirect chain that stays inside the trust
// boundary is followed before the request is abandoned.
const maxRedirects = 3

// messagesPerPage is the page size used when listing a ticket's messages.
// 100 is Gorgias's documented maximum (the default is 30). It is a var,
// not a const, so a test can shrink it to exercise pagination without a
// 100-entry fixture.
var messagesPerPage = 100

// maxMessagePages bounds how many pages listMessages will fetch. Every
// page URL is built by this client from the configured host and the cursor
// the previous page returned — never followed as a server-supplied link —
// so the cap exists only to stop an unbounded sweep against a ticket with
// a pathological number of messages from hanging a triage run. Reaching it
// is warned about, not treated as fatal. It is a var so a test can shrink
// it without a 100-page fixture.
var maxMessagePages = 100

// maxAttachmentBytes bounds a downloaded attachment's raw bytes. It is a
// var so a test can shrink it rather than serve 64 MiB.
var maxAttachmentBytes int64 = 64 << 20

// gorgiasHosts are the Gorgias-operated hosts an attachment may be
// downloaded from but which never see the API key. The configured account
// host is trusted with the credential by the Trust's base rule; every
// other *.gorgias.com host (the file service a download redirects to, an
// account that hosts a shared asset) is fetched from unauthenticated,
// because those URLs carry their own signature and an API key handed to
// them would be a credential spent for nothing.
var gorgiasHosts = []httpx.HostRule{
	{Suffix: hostSuffix},
}

// Config holds one Gorgias account's settings. Secrets arrive already
// resolved by the wiring layer, so every field is a plain string.
type Config struct {
	// Account is the account identifier — "acme" for acme.gorgias.com. A
	// scheme, a trailing slash or a trailing ".gorgias.com" is trimmed.
	// Ignored when BaseURL is set.
	Account string
	// BaseURL overrides the host derived from Account, for an account
	// reached through a proxy. No trailing slash is kept.
	BaseURL string
	// Email is the Gorgias login email, sent as the HTTP Basic username.
	// It is an identifier, not a secret.
	Email string
	// APIKey is sent as the HTTP Basic password.
	APIKey string
}

// Client talks to one Gorgias account. It implements source.Helpdesk and
// source.Warner.
type Client struct {
	cfg     Config
	baseURL string // no trailing slash
	// trust is the configured account host — the one host this client's
	// API key is ever sent to — plus Gorgias's other hosts, which are
	// fetched from unauthenticated.
	trust *httpx.Trust
	hc    *http.Client

	// warnings holds the non-fatal problems each call recorded, keyed by
	// the ticket id it was called with; reading is what clears an entry.
	warnings httpx.Warnings
}

var (
	_ source.Helpdesk = (*Client)(nil)
	_ source.Warner   = (*Client)(nil)
)

// New validates cfg and returns a Client. hc may be nil, in which case a
// client with a 30 s timeout is used.
func New(cfg Config, hc *http.Client) (*Client, error) {
	cfg.Email = strings.TrimSpace(cfg.Email)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)

	base, err := baseURLFor(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Email == "" {
		return nil, fmt.Errorf("gorgias: email is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gorgias: apiKey is required")
	}
	cfg.BaseURL = base

	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	trust, terr := httpx.NewTrust(base, gorgiasHosts...)
	if terr != nil {
		return nil, fmt.Errorf("gorgias: invalid base URL %q: %w", base, terr)
	}
	// A shallow copy: the Transport (and any pooled connections) is shared
	// with the caller's client, only the redirect policy is ours. Every
	// request this adapter makes goes through it, so a response that tries
	// to redirect one off the trusted hosts is refused uniformly, not just
	// on the download path.
	//
	// This matters more here than in most adapters: Gorgias answers an
	// attachment download with a 307 to a signed URL, and that Location
	// arrives inside a server response. Go strips the Authorization header
	// on a cross-host hop, but that still lets the request happen and the
	// answer get written to disk under the attachment's name.
	return &Client{
		cfg:     cfg,
		baseURL: base,
		trust:   trust,
		hc:      httpx.Client(hc, trust, maxRedirects),
	}, nil
}

// baseURLFor resolves the account host: the explicit baseUrl when there is
// one, otherwise https://{account}.gorgias.com. An account identifier that
// was pasted as a full host ("acme.gorgias.com") or a URL is trimmed back
// to its label rather than refused, since that is the shape an operator
// copies out of their browser.
func baseURLFor(cfg Config) (string, error) {
	if raw := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"); raw != "" {
		return raw, nil
	}
	account := strings.TrimSpace(cfg.Account)
	account = strings.TrimPrefix(account, "https://")
	account = strings.TrimPrefix(account, "http://")
	account = strings.TrimRight(account, "/")
	account = strings.TrimSuffix(account, hostSuffix)
	if account == "" {
		return "", fmt.Errorf("gorgias: one of account or baseUrl is required")
	}
	if strings.ContainsAny(account, "./ ") {
		return "", fmt.Errorf("gorgias: account %q must be the account identifier alone, e.g. acme for acme.gorgias.com", cfg.Account)
	}
	return "https://" + account + hostSuffix, nil
}

// Ping checks the credential with the cheapest authenticated call the API
// has: the account the key belongs to. It gives doctor a real round trip
// against the configured host.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.doRaw(ctx, c.baseURL+"/api/account", true, maxJSONBody)
	return err
}

// apiGET issues an authenticated GET against path (relative to baseURL)
// and decodes a 2xx JSON response into out.
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
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: decode: %v", logPath(u), err)}
	}
	return nil
}

// doRaw issues one GET against rawURL — with the account's Basic auth
// header when withAuth is true, unauthenticated otherwise — and returns
// the body of a 2xx response, mapping anything else to a *source.Error. A
// 429 is retried exactly once after honouring a Retry-After of at most
// maxRetryAfter; a longer or absent one is reported as source.RateLimited
// without waiting.
func (c *Client) doRaw(ctx context.Context, rawURL string, withAuth bool, limit int64) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: %v", logPath(rawURL), err)}
		}
		if withAuth {
			req.SetBasicAuth(c.cfg.Email, c.cfg.APIKey)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.hc.Do(req)
		if err != nil {
			if host, ok := httpx.RedirectHost(err); ok {
				return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: redirect to untrusted host %s", logPath(rawURL), host)}
			}
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: %v", logPath(rawURL), err)}
		}
		body, readErr := httpx.ReadLimited(resp.Body, limit)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if d, ok := httpx.RetryAfter(resp.Header, maxRetryAfter); ok {
				if err := httpx.SleepCtx(ctx, d); err != nil {
					return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: %v", logPath(rawURL), err)}
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, statusError(logPath(rawURL), resp.StatusCode, body)
		}
		if readErr != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: read body: %v", logPath(rawURL), readErr)}
		}
		return body, nil
	}
}

// downloadTo fetches rawURL and streams the body straight to destPath,
// stopping at maxAttachmentBytes. A file that would exceed the limit is a
// download failure and its partial output is removed, so a truncated
// attachment is never left on disk looking complete.
func (c *Client) downloadTo(ctx context.Context, rawURL string, withAuth bool, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: %v", logPath(rawURL), err)}
	}
	if withAuth {
		req.SetBasicAuth(c.cfg.Email, c.cfg.APIKey)
	}
	req.Header.Set("Accept", "*/*")

	_, err = httpx.Download(ctx, c.hc, req, destPath, httpx.DownloadOptions{Max: maxAttachmentBytes})
	var se *httpx.StatusError
	host, refused := httpx.RedirectHost(err)
	switch {
	case err == nil:
		return nil
	case refused:
		// Only the host: Go's *url.Error carries the refused target's
		// path and query, a Gorgias download redirects to a signed URL
		// whose query is the signature, and this error becomes a
		// per-ticket warning an operator and an agent both read.
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: redirect to untrusted host %s", logPath(rawURL), host)}
	case errors.As(err, &se):
		return statusError(logPath(rawURL), se.Status, se.Body)
	case errors.Is(err, httpx.ErrTooLarge):
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: attachment exceeds the %d byte limit", logPath(rawURL), maxAttachmentBytes)}
	default:
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: %v", logPath(rawURL), err)}
	}
}

// logPath reduces a request URL to its path, dropping the query string so
// a pagination cursor or a signed attachment URL's signature never lands
// in a log line or a warning. The credential travels in a header and so is
// never at risk here either way.
func logPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<url>"
	}
	if u.Path == "" {
		return "/"
	}
	return u.Path
}

// statusError maps a non-2xx HTTP response to a *source.Error, quoting at
// most 200 bytes of the body and never the credential.
func statusError(path string, status int, body []byte) *source.Error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("gorgias: GET %s: %d", path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("gorgias: GET %s: %d", path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("gorgias: GET %s: %d", path, status)}
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: GET %s: %d: %s", path, status, snippet)}
	}
}

// --- warnings ---

// addWarnings files the problems one call skipped over under its ticket,
// alongside whatever an earlier call for the same ticket recorded: a
// bundle is assembled from Get, Threads and Attachments, and the caller
// reads WarningsFor once at the end. An identical line is dropped, since
// Threads and Attachments page the same message feed.
func (c *Client) addWarnings(id string, warnings []string) {
	c.warnings.Add(id, warnings...)
}

// WarningsFor implements source.Warner: it returns and consumes every
// problem recorded across the calls made for ticket id so far, so the
// caller can put them in front of the agent and the operator instead of a
// partial result that quietly looks complete.
func (c *Client) WarningsFor(id string) []string {
	return c.warnings.Take(id)
}
