// Package helpscout implements source.Helpdesk against the Help Scout
// Mailbox API 2.0: conversation lookup, the thread list embedded on it,
// and attachment download, over plain stdlib net/http.
//
// Help Scout has no API-key mode: every call carries an OAuth2 bearer
// token, and the server-to-server flow Sirdar uses (client credentials)
// hands back a token with no refresh token beside it. This client
// therefore mints its own: it caches the token with its expiry, re-mints
// when the cached one is spent, and re-mints once more when a call comes
// back 401 anyway — a token can be revoked at the Help Scout console long
// before the expiry it was issued with.
//
// Everything this adapter talks to lives on api.helpscout.net, attachment
// downloads included: an attachment's bytes arrive base64-encoded inside a
// JSON response from the API itself rather than from a CDN. The host check
// is still made against every URL taken out of a response body, because a
// link that arrives inside a response is input, not configuration.
package helpscout

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
)

// apiHost is the single host every Help Scout Mailbox API call goes to —
// the token endpoint, the conversation, and the attachment bytes alike.
const apiHost = "api.helpscout.net"

// defaultBaseURL is apiHost as a request target. It is a const, not a
// config knob: Help Scout runs one API host, and an operator-supplied
// override would only widen where the client credentials can be sent.
const defaultBaseURL = "https://" + apiHost

// maxRetryAfter bounds how long a 429's Retry-After is honoured before the
// call gives up and reports source.RateLimited instead of blocking a
// triage run behind a long quota reset.
const maxRetryAfter = httpx.MaxRetryAfter

// maxJSONBody bounds an ordinary API JSON response (a conversation, a page
// of threads, a mailbox list).
const maxJSONBody = 8 << 20

// maxRedirects bounds how far a same-trust-boundary redirect chain is
// followed before the request is abandoned.
const maxRedirects = 3

// tokenSkew is how long before its stated expiry a cached access token is
// treated as spent, so a call does not set off with a token that expires
// mid-flight.
const tokenSkew = 60 * time.Second

// maxAttachmentBytes bounds a downloaded attachment's decoded bytes. It is
// a var so a test can shrink it rather than serve 64 MiB.
var maxAttachmentBytes int64 = 64 << 20

// maxThreadPages bounds how many pages of a conversation's thread feed are
// read in total, the page embedded on the conversation included. A feed
// still paginating past this many pages stops with a warning rather than
// sweeping without bound. It is a var so a test can shrink it instead of
// needing a 100-page fixture.
var maxThreadPages = 100

// threadsPageSize is Help Scout's page size for the thread feed (per the
// vendor docs: most list endpoints default to 50/page), and so also the
// size of the page embedded on a conversation via `?embed=threads`. A
// shorter embedded page is the whole feed; a full one is the signal to
// fetch the rest from the dedicated thread-list endpoint. It is a var so a
// test can shrink it instead of needing a 50-thread fixture.
var threadsPageSize = 50

// Config holds one Help Scout account's settings. Secrets arrive already
// resolved by the wiring layer, so every field is a plain string.
type Config struct {
	// ClientID and ClientSecret are the Help Scout app's client-credentials
	// pair. The app must be tied to an active invited user on the account.
	ClientID     string
	ClientSecret string
}

// Client talks to one Help Scout account. It implements source.Helpdesk
// and source.Warner.
type Client struct {
	cfg     Config
	baseURL string // no trailing slash
	// trust is api.helpscout.net alone: Help Scout serves attachment bytes
	// from the API host itself, so there is no fetch-only CDN tier here —
	// the one trusted host is also the one that gets the credential.
	trust *httpx.Trust
	hc    *http.Client

	// warnings holds the non-fatal problems each call recorded, keyed by
	// the conversation id it was called with. Entries are appended by each
	// call in a bundle and removed when read.
	warnings httpx.Warnings

	// tokMu serialises minting: it is held across the token HTTP request
	// so a burst of concurrent calls mints one token rather than one each.
	tokMu    sync.Mutex
	token    string
	tokenExp time.Time
}

var (
	_ source.Helpdesk = (*Client)(nil)
	_ source.Warner   = (*Client)(nil)
)

// New validates cfg and returns a Client. hc may be nil, in which case a
// client with a 30 s timeout is used.
func New(cfg Config, hc *http.Client) (*Client, error) {
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	if cfg.ClientID == "" {
		return nil, &source.Error{Code: source.Auth, Message: "helpscout: clientId is required"}
	}
	if cfg.ClientSecret == "" {
		return nil, &source.Error{Code: source.Auth, Message: "helpscout: clientSecret is required"}
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	trust, terr := httpx.NewTrust(defaultBaseURL)
	if terr != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: %v", terr)}
	}
	c := &Client{cfg: cfg, baseURL: defaultBaseURL, trust: trust}
	// A shallow copy: the Transport (and any pooled connections) is shared
	// with the caller's client, only the redirect policy is ours. Every
	// request this adapter makes goes through it, so a response that tries
	// to redirect any of them off the trusted host is refused uniformly.
	c.hc = httpx.Client(hc, trust, maxRedirects)
	return c, nil
}

// --- auth ---

type hsToken struct {
	TokenType   string `json:"token_type"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// accessToken returns a live access token, minting one when the cache is
// empty or spent. The mint is serialised: a burst of concurrent calls
// against a cold cache mints once.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.tokMu.Lock()
	defer c.tokMu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp.Add(-tokenSkew)) {
		return c.token, nil
	}
	return c.mintLocked(ctx)
}

// invalidate drops the cached token when it is still the one that just
// failed, so a concurrent call that already minted a fresh one keeps it.
func (c *Client) invalidate(spent string) {
	c.tokMu.Lock()
	defer c.tokMu.Unlock()
	if c.token == spent {
		c.token = ""
		c.tokenExp = time.Time{}
	}
}

// mintLocked performs the client-credentials grant. It must be called with
// tokMu held.
func (c *Client) mintLocked(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)

	tokenURL := c.baseURL + "/v2/oauth2/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: POST %s: %v", logPath(tokenURL), err)}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		if host, ok := httpx.RedirectHost(err); ok {
			return "", &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: POST %s: redirect to untrusted host %s", logPath(tokenURL), host)}
		}
		return "", &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: POST %s: %v", logPath(tokenURL), err)}
	}
	body, readErr := httpx.ReadLimited(resp.Body, maxJSONBody)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// A token endpoint that answers 400 is answering about the
		// credentials, so it is an auth failure whatever the status code
		// says.
		if resp.StatusCode == http.StatusBadRequest {
			return "", &source.Error{Code: source.Auth, Message: fmt.Sprintf("helpscout: POST %s: 400: client credentials rejected", logPath(tokenURL))}
		}
		return "", statusError(http.MethodPost, tokenURL, resp.StatusCode, body)
	}
	if readErr != nil {
		return "", &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: POST %s: read body: %v", logPath(tokenURL), readErr)}
	}
	var tok hsToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: POST %s: decode: %v", logPath(tokenURL), err)}
	}
	if tok.AccessToken == "" {
		return "", &source.Error{Code: source.Auth, Message: fmt.Sprintf("helpscout: POST %s: no access_token in response", logPath(tokenURL))}
	}
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 2 * tokenSkew
	}
	c.token = tok.AccessToken
	c.tokenExp = time.Now().Add(ttl)
	return c.token, nil
}

// --- HTTP ---

// Ping checks the credential with the cheapest authenticated call the
// Mailbox API has: one page of one mailbox.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.doRaw(ctx, c.baseURL+"/v2/mailboxes?page=1&size=1", true, maxJSONBody)
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

// apiGETAbsolute is apiGET for a caller that already holds a full URL —
// a thread feed's "next" link, say, already checked against trust.Check.
func (c *Client) apiGETAbsolute(ctx context.Context, rawURL string, out any) error {
	body, err := c.doRaw(ctx, rawURL, true, maxJSONBody)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: GET %s: decode: %v", logPath(rawURL), err)}
	}
	return nil
}

// doRaw issues one GET against rawURL and returns the body of a 2xx
// response, mapping anything else to a *source.Error. A 401 re-mints the
// access token and retries exactly once — a token can be revoked long
// before the expiry it was issued with. A 429 is retried exactly once
// after honouring a Retry-After of at most maxRetryAfter; a longer or
// absent one is reported as source.RateLimited without waiting.
func (c *Client) doRaw(ctx context.Context, rawURL string, withAuth bool, limit int64) ([]byte, error) {
	triedRefresh, triedWait := false, false
	for {
		var tok string
		if withAuth {
			var err error
			if tok, err = c.accessToken(ctx); err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: GET %s: %v", logPath(rawURL), err)}
		}
		if withAuth {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.hc.Do(req)
		if err != nil {
			if host, ok := httpx.RedirectHost(err); ok {
				return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: GET %s: redirect to untrusted host %s", logPath(rawURL), host)}
			}
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: GET %s: %v", logPath(rawURL), err)}
		}
		body, readErr := httpx.ReadLimited(resp.Body, limit)
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized && withAuth && !triedRefresh {
			triedRefresh = true
			c.invalidate(tok)
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests && !triedWait {
			if d, ok := retryAfter(resp.Header, maxRetryAfter); ok {
				triedWait = true
				if err := httpx.SleepCtx(ctx, d); err != nil {
					return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: GET %s: %v", logPath(rawURL), err)}
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, statusError(http.MethodGet, rawURL, resp.StatusCode, body)
		}
		if readErr != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: GET %s: read body: %v", logPath(rawURL), readErr)}
		}
		return body, nil
	}
}

// retryAfter reads a Retry-After header, or — when Help Scout omits it —
// its own X-RateLimit-Retry-After fallback, in either documented form, and
// reports whether the wait is inside max.
func retryAfter(h http.Header, max time.Duration) (time.Duration, bool) {
	if h.Get("Retry-After") == "" {
		if v := strings.TrimSpace(h.Get("X-RateLimit-Retry-After")); v != "" {
			h = h.Clone()
			h.Set("Retry-After", v)
		}
	}
	return httpx.RetryAfter(h, max)
}

// logPath reduces a request URL to its path, dropping the query string so
// a paginated cursor never lands in a log line or a warning. The
// credential travels in a header (and, for the token grant, in a form
// body) and so is never at risk here either way.
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
func statusError(method, rawURL string, status int, body []byte) *source.Error {
	path := logPath(rawURL)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("helpscout: %s %s: %d", method, path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("helpscout: %s %s: %d", method, path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("helpscout: %s %s: %d", method, path, status)}
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: %s %s: %d: %s", method, path, status, snippet)}
	}
}

// --- warnings ---

// addWarnings files the problems one call skipped over under its
// conversation id, alongside whatever an earlier call for the same id
// recorded: a bundle is assembled from Get, Threads and Attachments, and
// the caller reads WarningsFor once at the end. An identical line is
// dropped, since Threads and Attachments walk the same thread feed.
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
