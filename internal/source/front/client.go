// Package front implements source.Helpdesk against Front's Core API:
// conversation lookup, the two feeds that together make up the thread
// (messages and comments), and attachment download, over plain stdlib
// net/http.
//
// Auth is a static API token, minted once under Settings → Developers and
// sent as a bearer header — the intended mechanism for a single-workspace
// read-only integration, with no refresh loop to run. Front's OAuth flow
// exists for public multi-tenant apps and buys Sirdar nothing.
//
// Everything this adapter talks to lives on Front's own API host,
// attachment bytes included: `GET /download/{id}` is an ordinary
// bearer-authenticated call returning the file, not a pre-signed CDN link.
// That is the one way Front differs from the other fixed-host helpdesks —
// there is no fetch-only tier here, so the host that may be fetched from is
// also the host that may see the token, and the rule is correspondingly
// narrow: api2.frontapp.com and the `*.api.frontapp.com` per-company form
// Front's own examples show in `_links`, and nothing else. A URL taken out
// of a response body is still checked before it is used, because a link
// that arrives inside a response is input rather than configuration.
package front

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
)

// apiHost is Front's Core API host, and the host this client's token is
// sent to.
const apiHost = "api2.frontapp.com"

// defaultBaseURL is apiHost as a request target. It is a const, not a
// config knob: Front runs one API host, and an operator-supplied override
// would only widen where the token can be sent.
const defaultBaseURL = "https://" + apiHost

// companyHostSuffix is the per-company API host form Front's own reference
// examples show inside `_links.self` and an attachment's `url`
// ("yourCompany.api.frontapp.com"). It is trusted with the credential
// because a download from it is an authenticated call like any other, and
// the whole `*.api.frontapp.com` space is Front-operated. The rule is
// dot-prefixed, so "api.frontapp.com.evil.example" does not match it.
const companyHostSuffix = ".api.frontapp.com"

// appURLPrefix is the Front web app's deep link for a conversation. The id
// appended to it is the one this client was asked for, never one taken out
// of a response body: a "view in Front" link is a link an operator clicks.
const appURLPrefix = "https://app.frontapp.com/open/"

// maxRetryAfter bounds how long a 429's retry-after is honoured before the
// call gives up and reports source.RateLimited rather than blocking a
// triage run behind a long quota reset. Front's per-minute plan limits are
// low enough (50/min on Starter) that a run can meet one.
const maxRetryAfter = httpx.MaxRetryAfter

// maxJSONBody bounds an ordinary API JSON response (a conversation, a page
// of messages, a page of comments).
const maxJSONBody = 8 << 20

// maxRedirects bounds how far a same-trust-boundary redirect chain is
// followed before the request is abandoned.
const maxRedirects = 3

// pageSize is how many entries a feed page is asked for. Front's documented
// maximum is 100, with a default of 50.
const pageSize = 100

// maxAttachmentBytes bounds a downloaded attachment's raw bytes. It is a
// var so a test can shrink it rather than serve 64 MiB.
var maxAttachmentBytes int64 = 64 << 20

// maxFeedPages bounds how many pages of one feed (messages, or comments)
// are read before pagination stops with a warning rather than sweeping
// without bound. Front's pagination contract is explicit that a short page
// does not mean the end of the feed — only a null `_pagination.next` does —
// so there is no natural stopping point other than this cap and the link
// itself. It is a var so a test can shrink it instead of needing a
// 100-page fixture.
var maxFeedPages = 100

// Config holds one Front workspace's settings. Secrets arrive already
// resolved by the wiring layer, so every field is a plain string.
type Config struct {
	// Token is the Front API token, sent as a bearer header.
	Token string
}

// Client talks to one Front workspace. It implements source.Helpdesk and
// source.Warner.
type Client struct {
	cfg     Config
	baseURL string // no trailing slash
	// trust is the Front API host plus the per-company form of it. Both
	// get the credential: Front serves attachment bytes from the API
	// itself, so there is no fetch-only tier to withhold the token from.
	trust *httpx.Trust
	hc    *http.Client

	// warnings holds the non-fatal problems each call recorded, keyed by
	// the conversation id it was called with. Entries are appended by each
	// call in a bundle and removed when read.
	warnings httpx.Warnings
}

var (
	_ source.Helpdesk = (*Client)(nil)
	_ source.Warner   = (*Client)(nil)
)

// New validates cfg and returns a Client. hc may be nil, in which case a
// client with a 30 s timeout is used.
func New(cfg Config, hc *http.Client) (*Client, error) {
	cfg.Token = strings.TrimSpace(cfg.Token)
	if cfg.Token == "" {
		return nil, &source.Error{Code: source.Auth, Message: "front: token is required"}
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	trust, terr := httpx.NewTrust(defaultBaseURL, httpx.HostRule{Suffix: companyHostSuffix, SendCredential: true})
	if terr != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: %v", terr)}
	}
	c := &Client{cfg: cfg, baseURL: defaultBaseURL, trust: trust}
	// A shallow copy: the Transport (and any pooled connections) is shared
	// with the caller's client, only the redirect policy is ours. Every
	// request this adapter makes goes through it, so a response that tries
	// to redirect any of them off the trusted hosts is refused uniformly,
	// not just on the download path.
	c.hc = httpx.Client(hc, trust, maxRedirects)
	return c, nil
}

// --- HTTP ---

// Ping checks the credential with a cheap authenticated call: one page of
// one teammate. Front documents no identity endpoint, so this is the
// smallest read the token has to be good for.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.doRaw(ctx, c.baseURL+"/teammates?limit=1", maxJSONBody)
	return err
}

// apiGET issues an authenticated GET against path (relative to baseURL)
// and decodes a 2xx JSON response into out.
func (c *Client) apiGET(ctx context.Context, path string, query url.Values, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return c.apiGETAbsolute(ctx, u, out)
}

// apiGETAbsolute is apiGET for a caller that already holds a full URL — a
// feed's `_pagination.next` link, say, already checked against trust.Check.
func (c *Client) apiGETAbsolute(ctx context.Context, rawURL string, out any) error {
	body, err := c.doRaw(ctx, rawURL, maxJSONBody)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: decode: %v", logPath(rawURL), err)}
	}
	return nil
}

// doRaw issues one authenticated GET against rawURL and returns the body of
// a 2xx response, mapping anything else to a *source.Error. A 429 is
// retried exactly once after honouring a retry-after of at most
// maxRetryAfter; a longer or absent one is reported as source.RateLimited
// without waiting.
func (c *Client) doRaw(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: %v", logPath(rawURL), err)}
		}
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
		req.Header.Set("Accept", "application/json")

		resp, err := c.hc.Do(req)
		if err != nil {
			if host, ok := httpx.RedirectHost(err); ok {
				return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: redirect to untrusted host %s", logPath(rawURL), host)}
			}
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: %v", logPath(rawURL), err)}
		}
		body, readErr := httpx.ReadLimited(resp.Body, limit)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if d, ok := httpx.RetryAfter(resp.Header, maxRetryAfter); ok {
				if err := httpx.SleepCtx(ctx, d); err != nil {
					return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: %v", logPath(rawURL), err)}
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, statusError(rawURL, resp.StatusCode, body)
		}
		if readErr != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: read body: %v", logPath(rawURL), readErr)}
		}
		return body, nil
	}
}

// logPath reduces a request URL to its path, dropping the query string so
// an opaque page token never lands in a log line or a warning. The
// credential travels in a header and so is never at risk here either way.
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
func statusError(rawURL string, status int, body []byte) *source.Error {
	path := logPath(rawURL)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("front: GET %s: %d", path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("front: GET %s: %d", path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("front: GET %s: %d", path, status)}
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: %d: %s", path, status, snippet)}
	}
}

// --- warnings ---

// addWarnings files the problems one call skipped over under its
// conversation id, alongside whatever an earlier call for the same id
// recorded: a bundle is assembled from Get, Threads and Attachments, and
// the caller reads WarningsFor once at the end. An identical line is
// dropped, since Threads and Attachments walk the same two feeds.
func (c *Client) addWarnings(id string, warnings []string) {
	c.warnings.Add(id, warnings...)
}

// WarningsFor implements source.Warner: it returns and consumes every
// problem recorded across the calls made for conversation id so far, so
// the caller can surface them instead of a partial result that looks
// complete.
func (c *Client) WarningsFor(id string) []string {
	return c.warnings.Take(id)
}
