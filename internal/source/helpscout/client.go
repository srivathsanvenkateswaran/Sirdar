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
const maxRetryAfter = 30 * time.Second

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
	host    string // normalised host the credential may be sent to
	hc      *http.Client

	// mu guards warnings. One Client serves every ticket in a run, so two
	// tickets can be inside a call at once.
	mu sync.Mutex
	// warnings holds the non-fatal problems each call recorded, keyed by
	// the conversation id it was called with. Entries are appended by each
	// call in a bundle and removed when read.
	warnings map[string][]string

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
	c := &Client{cfg: cfg, baseURL: defaultBaseURL, host: apiHost}
	// A shallow copy: the Transport (and any pooled connections) is shared
	// with the caller's client, only the redirect policy is ours. Every
	// request this adapter makes goes through it, so a response that tries
	// to redirect any of them off the trusted host is refused uniformly.
	dl := *hc
	dl.CheckRedirect = c.checkRedirect
	c.hc = &dl
	return c, nil
}

// checkRedirect refuses to follow a redirect off the host this client
// trusts. A redirect Location comes back inside a server response, which
// makes it input, not configuration: Go already strips the Authorization
// header on a cross-host hop, but that still lets the request happen and
// the response get written to disk as if it were the real attachment.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("helpscout: stopped after %d redirects", maxRedirects)
	}
	host, trusted, _ := c.urlTrust(req.URL)
	if !trusted {
		return fmt.Errorf("helpscout: redirect to untrusted host %s", host)
	}
	return nil
}

// urlTrust reports whether a URL taken out of an API response may be
// requested at all, and whether this client's bearer token may be sent
// there. Help Scout serves attachment bytes from the API host itself, so
// there is no fetch-only CDN tier here: the one trusted host is also the
// one that gets the credential.
//
// The transport and the userinfo matter as much as the host. A link
// arrives inside a response body, so a hostile or compromised instance can
// put "http://" in front of a legitimate host and watch the token cross
// the network in the clear, or write
// "https://api.helpscout.net@attacker.example/x", which parses with the
// real destination in Host and the decoy in User.
func (c *Client) urlTrust(u *url.URL) (host string, trusted, sendAuth bool) {
	if u == nil || u.Host == "" {
		return "(no host)", false, false
	}
	host = hostKey(u.Scheme, u.Host)
	if u.User != nil {
		return host, false, false
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return host, false, false
	}
	if host == c.host {
		return host, true, true
	}
	return host, false, false
}

// hostKey renders a scheme+host pair comparable: lowercased, with the DNS
// root's trailing dot removed and the scheme's default port dropped, so
// "API.HelpScout.net.:443" and "api.helpscout.net" are one host.
func hostKey(scheme, host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		return strings.TrimSuffix(host, ".")
	}
	name = strings.TrimSuffix(name, ".")
	scheme = strings.ToLower(scheme)
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return name
	}
	return net.JoinHostPort(name, port)
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
		return "", &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: POST %s: %v", logPath(tokenURL), err)}
	}
	body, readErr := readLimited(resp.Body, maxJSONBody)
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
// a thread feed's "next" link, say, already checked against urlTrust.
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
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: GET %s: %v", logPath(rawURL), err)}
		}
		body, readErr := readLimited(resp.Body, limit)
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized && withAuth && !triedRefresh {
			triedRefresh = true
			c.invalidate(tok)
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests && !triedWait {
			if d, ok := retryAfter(resp.Header); ok {
				triedWait = true
				if err := sleepCtx(ctx, d); err != nil {
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
// enough to sit through. Help Scout also sends the same number as
// X-RateLimit-Retry-After, which is read as a fallback.
func retryAfter(h http.Header) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		v = strings.TrimSpace(h.Get("X-RateLimit-Retry-After"))
	}
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
	if len(warnings) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.warnings == nil {
		c.warnings = map[string][]string{}
	}
	seen := make(map[string]bool, len(c.warnings[id])+len(warnings))
	for _, w := range c.warnings[id] {
		seen[w] = true
	}
	for _, w := range warnings {
		if seen[w] {
			continue
		}
		seen[w] = true
		c.warnings[id] = append(c.warnings[id], w)
	}
}

// WarningsFor implements source.Warner: it returns and consumes every
// problem recorded across the calls made for conversation id so far, so
// the caller can surface them instead of a partial result that looks
// complete.
func (c *Client) WarningsFor(id string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.warnings[id]
	delete(c.warnings, id)
	if len(w) == 0 {
		return nil
	}
	return append([]string(nil), w...)
}
