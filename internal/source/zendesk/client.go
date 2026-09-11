// Package zendesk implements source.Helpdesk against the Zendesk Support
// API v2: ticket lookup, threaded comment retrieval, and attachment
// download, over plain stdlib net/http.
package zendesk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
)

// maxBodyBytes caps how much of a JSON response body this client will
// read. Zendesk ticket/comment payloads are small; a response past this
// size is treated as an error rather than read into memory whole.
const maxBodyBytes = 8 << 20 // 8 MiB

// maxRetryAfter is the longest Retry-After wait this client will honour
// before giving up and returning source.RateLimited instead. A caller
// blocked longer than this on one ticket is worse than surfacing the limit
// and letting the run schedule a retry of its own.
const maxRetryAfter = httpx.MaxRetryAfter

// maxCommentPages caps how many pages of a ticket's comment feed this
// client will follow. A feed still paginating past this many pages stops
// early with a warning rather than looping without bound.
const maxCommentPages = 100

// Config configures a Client. Exactly one of (Email and APIToken) or
// OAuthToken must be set: basic auth (`{email}/token` as username, the API
// token as password) or a Bearer OAuth token, never both.
type Config struct {
	// Subdomain is the account identifier, e.g. "acme" for
	// acme.zendesk.com. Always required: it also builds the human-facing
	// ticket URL, even when BaseURL overrides where requests are sent.
	Subdomain string
	// BaseURL overrides the request target (default
	// "https://{Subdomain}.zendesk.com"). Tests use this to point at a
	// local httptest server; production configs normally leave it blank.
	BaseURL string

	Email      string
	APIToken   string
	OAuthToken string
}

// Client is a Zendesk Support API client implementing source.Helpdesk.
type Client struct {
	baseURL string // request target, no trailing slash
	// trust is the account's own host (which gets the credential) plus
	// Zendesk's first-party attachment hosts (which are fetched from but
	// never credentialed). An http baseUrl is the only way a URL off https
	// is trusted.
	trust      *httpx.Trust
	subdomain  string
	authHeader string
	hc         *http.Client

	// warnings is keyed by ticket id: one Client can serve several tickets
	// whose Attachments calls overlap.
	warnings httpx.Warnings
}

// New validates cfg and returns a Client. hc may be nil, in which case a
// client with a 30s timeout is used.
func New(cfg Config, hc *http.Client) (*Client, error) {
	basic := cfg.Email != "" || cfg.APIToken != ""
	bearer := cfg.OAuthToken != ""
	switch {
	case basic && bearer:
		return nil, &source.Error{Code: source.Auth, Message: "zendesk: configure exactly one of email/apiToken or oauthToken, not both"}
	case basic && (cfg.Email == "" || cfg.APIToken == ""):
		return nil, &source.Error{Code: source.Auth, Message: "zendesk: both email and apiToken are required for basic auth"}
	case !basic && !bearer:
		return nil, &source.Error{Code: source.Auth, Message: "zendesk: no credentials configured: set email and apiToken, or oauthToken"}
	}

	if cfg.Subdomain == "" {
		return nil, &source.Error{Code: source.Internal, Message: "zendesk: subdomain is required"}
	}

	base := strings.TrimSuffix(cfg.BaseURL, "/")
	if base == "" {
		base = "https://" + cfg.Subdomain + ".zendesk.com"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zendesk: invalid baseUrl %q", cfg.BaseURL)}
	}

	var authHeader string
	if bearer {
		authHeader = "Bearer " + cfg.OAuthToken
	} else {
		authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Email+"/token:"+cfg.APIToken))
	}

	client := hc
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	trust, terr := httpx.NewTrust(base, zendeskHosts...)
	if terr != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zendesk: invalid baseUrl %q", cfg.BaseURL)}
	}

	return &Client{
		baseURL:    base,
		trust:      trust,
		subdomain:  cfg.Subdomain,
		authHeader: authHeader,
		hc:         client,
	}, nil
}

// zendeskHosts are Zendesk's own attachment hosts. An attachment's
// content_url legitimately lives on one of them, and those URLs carry their
// own token, so they are fetched from but never see this client's
// credential.
var zendeskHosts = []httpx.HostRule{
	{Suffix: ".zendesk.com"},
	{Suffix: ".zdusercontent.com"},
}

// Ping implements a health check for `sirdar doctor`: it fetches the
// authenticated user, which succeeds only when the credentials are valid.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.getRaw(ctx, c.baseURL+"/api/v2/users/me.json")
	return err
}

// trustedNextPage validates a comments page's next_page link before it is
// followed: unlike an attachment's content_url (which can legitimately
// live on Zendesk's own CDN hosts), a paginated API response must keep
// coming from this account's own configured host, since the live
// Authorization header goes on every one of these requests — which is
// exactly the hosts hostTrust would send the credential to. host is always
// returned (even when untrusted) so the caller can name it in a warning
// without re-parsing raw.
func (c *Client) trustedNextPage(raw string) (urlStr, host string, trusted bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", false
	}
	host = u.Hostname()
	if _, sendAuth, _ := c.trust.Check(u); !sendAuth {
		return "", host, false
	}
	return raw, host, true
}

// doOnce issues one authenticated GET against urlStr.
func (c *Client) doOnce(ctx context.Context, urlStr string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zendesk: GET %s: %v", logPath(urlStr), err)}
	}
	req.Header.Set("Authorization", c.authHeader)
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zendesk: GET %s: %v", logPath(urlStr), err)}
	}
	return resp, nil
}

// do issues an authenticated GET, retrying once when the response is 429
// and carries a Retry-After no longer than maxRetryAfter.
func (c *Client) do(ctx context.Context, urlStr string) (*http.Response, error) {
	resp, err := c.doOnce(ctx, urlStr)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		return resp, nil
	}

	wait, ok := httpx.RetryAfter(resp.Header, maxRetryAfter)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if !ok {
		return nil, &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("zendesk: GET %s: 429", logPath(urlStr))}
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(wait):
	}
	return c.doOnce(ctx, urlStr)
}

// getRaw issues an authenticated GET against urlStr (absolute) and returns
// the raw response body for a 2xx response, mapping non-2xx responses to a
// *source.Error and never including credentials in that error.
func (c *Client) getRaw(ctx context.Context, urlStr string) ([]byte, error) {
	resp, err := c.do(ctx, urlStr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, readErr := httpx.ReadLimited(resp.Body, maxBodyBytes)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusError(http.MethodGet, urlStr, resp.StatusCode, body)
	}
	if errors.Is(readErr, httpx.ErrTooLarge) {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zendesk: GET %s: response exceeds %d bytes", logPath(urlStr), maxBodyBytes)}
	}
	if readErr != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zendesk: GET %s: read body: %v", logPath(urlStr), readErr)}
	}
	return body, nil
}

// getJSON issues an authenticated GET against BaseURL+path (with query, if
// any) and decodes the 2xx JSON response into out.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return c.getJSONAbsolute(ctx, u, out)
}

// getJSONAbsolute is getJSON for a caller that already has a full URL, such
// as a paginated response's next_page link.
func (c *Client) getJSONAbsolute(ctx context.Context, urlStr string, out any) error {
	body, err := c.getRaw(ctx, urlStr)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("zendesk: GET %s: decode: %v", logPath(urlStr), err)}
	}
	return nil
}

// logPath reduces a request URL to its path for an error message. The query
// string goes: a paginated URL carries cursors, and an attachment URL its
// pre-signed signature, neither of which belongs in a log line an operator
// pastes into a ticket. The credential travels in a header and so is never
// at risk here either way.
func logPath(urlStr string) string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "<url>"
	}
	if u.Path == "" {
		return "/"
	}
	return u.Path
}

// statusError maps a non-2xx HTTP response to a *source.Error, naming the
// request by method and path. body is truncated to at most 200 bytes and
// never contains the request's Authorization header, so a mapped error can
// never repeat the credential.
func statusError(method, urlStr string, status int, body []byte) *source.Error {
	path := logPath(urlStr)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("zendesk: %s %s: %d", method, path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("zendesk: %s %s: %d", method, path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("zendesk: %s %s: %d", method, path, status)}
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("zendesk: %s %s: %d: %s", method, path, status, snippet)}
	}
}

// addWarnings appends the problems one call for ticket id recorded (a
// comment feed that stopped paginating early, a skipped attachment) to
// whatever is already pending for that id. A ticket's bundle is assembled
// from several calls — Get, Threads, Attachments — and the caller
// (internal/run/prepare.go) reads WarningsFor once at the end of all of
// them, so a warning from one call must survive the next call rather than
// being overwritten by it.
// An identical line is dropped rather than appended twice: Get, Threads and
// Attachments each walk the same comment feed, so a feed that stops early or
// an attachment host that is not trusted produces the same sentence on every
// pass, and three copies of one warning in the prompt read as three
// problems.
func (c *Client) addWarnings(id string, warnings []string) {
	c.warnings.Add(id, warnings...)
}

// WarningsFor implements source.Warner: it returns and consumes every
// problem recorded across the calls made for ticket id so far — an
// untrusted attachment host, a failed download, a comment feed whose
// next_page pointed somewhere untrusted or ran past the pagination cap —
// so the caller can surface them instead of silently returning a partial
// result.
func (c *Client) WarningsFor(id string) []string {
	return c.warnings.Take(id)
}

var (
	_ source.Helpdesk = (*Client)(nil)
	_ source.Warner   = (*Client)(nil)
)
