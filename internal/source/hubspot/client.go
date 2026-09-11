// Package hubspot implements source.Helpdesk against HubSpot Service Hub:
// a CRM ticket object, the Conversations-inbox thread associated with it,
// and attachment download, over plain stdlib net/http.
//
// Auth is a private-app access token sent as a bearer header — the direct
// fit for a single-account integration since HubSpot retired API keys, and
// no refresh loop to run.
//
// Two things this adapter is deliberately defensive about. A ticket does
// not carry its conversation inline: the link is an association, and the
// exact association shape is the least-documented part of HubSpot's
// support model, so a ticket with no readable conversation association
// yields the ticket's own content plus a warning rather than an error.
// And an attachment is fetched through a short-lived signed URL on
// HubSpot's file CDN, which arrives inside a response body: the access
// token goes only to api.hubapi.com, never to the CDN host the signed URL
// names.
package hubspot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
)

// apiHost is HubSpot's single global API host, and the only host this
// client's access token is ever sent to.
const apiHost = "api.hubapi.com"

const defaultBaseURL = "https://" + apiHost

// maxRetryAfter bounds how long a 429's Retry-After is honoured before the
// call gives up and reports source.RateLimited rather than blocking a
// triage run behind a long quota reset.
const maxRetryAfter = httpx.MaxRetryAfter

// maxJSONBody bounds an ordinary API JSON response (a ticket, a page of
// thread messages, a contact).
const maxJSONBody = 8 << 20

// maxRedirects bounds how far a same-trust-boundary redirect chain is
// followed before the request is abandoned.
const maxRedirects = 3

// maxObjectCacheEntries bounds the contact/company name caches: past this
// many distinct records seen by one Client, a cache is dropped and rebuilt
// rather than left to grow without limit.
const maxObjectCacheEntries = 1000

// maxAttachmentBytes bounds a downloaded attachment's raw bytes. It is a
// var so a test can shrink it rather than serve 64 MiB.
var maxAttachmentBytes int64 = 64 << 20

// maxMessagePages bounds how many pages of one thread's message feed are
// followed. A feed still paginating past this many pages stops with a
// warning rather than sweeping without bound. It is a var so a test can
// shrink it instead of needing a 100-page fixture.
var maxMessagePages = 100

// messagesPerPage is the page size asked for when listing a thread's
// messages: HubSpot's own documented cap for the threads/messages
// endpoints. It is a var for the same reason as maxMessagePages.
var messagesPerPage = 500

// Config holds one HubSpot account's settings. Secrets arrive already
// resolved by the wiring layer, so every field is a plain string.
type Config struct {
	// AccessToken is the private-app access token ("pat-na1-…"), sent as a
	// bearer header.
	AccessToken string
}

// Client talks to one HubSpot account. It implements source.Helpdesk and
// source.Warner.
type Client struct {
	cfg     Config
	baseURL string // no trailing slash
	// trust is api.hubapi.com (which gets the credential) plus
	// *.hubspot.com. It does not express the numbered
	// hubspotusercontent-N.net family; urlTrust adds that on top.
	trust *httpx.Trust
	hc    *http.Client

	// warnings holds the non-fatal problems each call recorded, keyed by
	// the ticket id it was called with.
	warnings httpx.Warnings

	// mu guards the caches below. One Client serves every ticket in a run,
	// so two tickets can be inside a call at once.
	mu sync.Mutex
	// contacts and companies cache resolved display names by object id.
	contacts  map[string]crmContact
	companies map[string]string
	// portalID is the account's hub id, read once from account-info and
	// used to build the human-facing ticket URL.
	portalID    string
	portalIDSet bool
}

var (
	_ source.Helpdesk = (*Client)(nil)
	_ source.Warner   = (*Client)(nil)
)

// New validates cfg and returns a Client. hc may be nil, in which case a
// client with a 30 s timeout is used.
func New(cfg Config, hc *http.Client) (*Client, error) {
	cfg.AccessToken = strings.TrimSpace(cfg.AccessToken)
	if cfg.AccessToken == "" {
		return nil, &source.Error{Code: source.Auth, Message: "hubspot: accessToken is required"}
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	trust, terr := httpx.NewTrust(defaultBaseURL, httpx.HostRule{Suffix: ".hubspot.com"})
	if terr != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: %v", terr)}
	}
	c := &Client{cfg: cfg, baseURL: defaultBaseURL, trust: trust}
	// A shallow copy: the Transport is shared with the caller's client,
	// only the redirect policy is ours. Every request this adapter makes
	// goes through it, so a response that tries to redirect one off the
	// trusted hosts is refused uniformly, not just on the download path.
	dl := *hc
	dl.CheckRedirect = c.checkRedirect
	c.hc = &dl
	return c, nil
}

// checkRedirect refuses to follow a redirect off the hosts this client
// trusts, exactly the decision urlTrust makes for any other URL taken out
// of a response body. It returns a *httpx.RedirectRefused carrying the
// refused host alone: Go wraps a CheckRedirect error in a *url.Error that
// includes the full redirect target, and a signed file URL that has expired
// into a generic error page is the ordinary way one points somewhere else,
// sometimes with a token in its query. Go already strips the Authorization
// header on a cross-host hop, but that still lets the request happen and
// the response get written to disk as if it were the real attachment.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("hubspot: stopped after %d redirects", maxRedirects)
	}
	host, trusted, _ := c.urlTrust(req.URL)
	if !trusted {
		return &httpx.RedirectRefused{Host: host, Reason: httpx.ReasonNotTrusted}
	}
	return nil
}

// urlTrust reports whether a URL taken out of an API response may be
// requested at all, and whether this client's access token may be sent
// there. Only api.hubapi.com gets the credential; HubSpot's file-serving
// hosts are trusted to download from because their URLs are signed.
//
// It is httpx.Trust plus the one predicate a Trust's suffix rules cannot
// express: the content CDN's numbered hubspotusercontent-N.net family,
// whose middle part is not fixed.
func (c *Client) urlTrust(u *url.URL) (host string, trusted, sendAuth bool) {
	if u == nil || u.Hostname() == "" {
		return "(no host)", false, false
	}
	host = httpx.NormalizeHost(u.Scheme, u.Host)
	fetch, sendCred, reason := c.trust.Check(u)
	if fetch {
		return host, true, sendCred
	}
	if reason == httpx.ReasonNotTrusted && fetchOnlyUploadHost(host) {
		return host, true, false
	}
	return host, false, false
}

// fetchOnlyUploadHost reports whether host is HubSpot's numbered content
// CDN (cdn2.hubspotusercontent-na1.net, …), matched by requiring the
// registrable label itself to begin with "hubspotusercontent", so
// "hubspotusercontent-na1.net.evil.example" does not match.
func fetchOnlyUploadHost(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) < 2 || labels[len(labels)-1] != "net" {
		return false
	}
	return strings.HasPrefix(labels[len(labels)-2], "hubspotusercontent")
}

// --- HTTP ---

// Ping checks the credential with the cheapest authenticated call the API
// has: the account's own details, which is also where the portal id used
// for ticket URLs comes from.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.doRaw(ctx, c.baseURL+"/account-info/v3/details", true, maxJSONBody)
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
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: decode: %v", logPath(u), err)}
	}
	return nil
}

// doRaw issues one GET against rawURL — with the account's bearer token
// when withAuth is true, unauthenticated otherwise — and returns the body
// of a 2xx response, mapping anything else to a *source.Error. A 429 is
// retried exactly once after honouring a Retry-After of at most
// maxRetryAfter; a longer or absent one is reported as source.RateLimited
// without waiting.
func (c *Client) doRaw(ctx context.Context, rawURL string, withAuth bool, limit int64) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: %v", logPath(rawURL), err)}
		}
		if withAuth {
			req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.hc.Do(req)
		if err != nil {
			if host, ok := httpx.RedirectHost(err); ok {
				return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: redirect to untrusted host %s", logPath(rawURL), host)}
			}
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: %v", logPath(rawURL), err)}
		}
		body, readErr := httpx.ReadLimited(resp.Body, limit)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if d, ok := httpx.RetryAfter(resp.Header, maxRetryAfter); ok {
				if err := httpx.SleepCtx(ctx, d); err != nil {
					return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: %v", logPath(rawURL), err)}
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, statusError(rawURL, resp.StatusCode, body)
		}
		if readErr != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: read body: %v", logPath(rawURL), readErr)}
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
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: %v", logPath(rawURL), err)}
	}
	if withAuth {
		req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	}
	req.Header.Set("Accept", "*/*")

	_, err = httpx.Download(ctx, c.hc, req, destPath, httpx.DownloadOptions{Max: maxAttachmentBytes})
	if host, ok := httpx.RedirectHost(err); ok {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: redirect to untrusted host %s", logPath(rawURL), host)}
	}
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return statusError(rawURL, se.Status, se.Body)
	}
	if errors.Is(err, httpx.ErrTooLarge) {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: attachment exceeds the %d byte limit", logPath(rawURL), maxAttachmentBytes)}
	}
	if err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: %v", logPath(rawURL), err)}
	}
	return nil
}

// logPath reduces a request URL to its path, dropping the query string so
// a signed file URL's signature, or a pagination cursor, never lands in a
// log line or a warning. The credential travels in a header and so is
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
func statusError(rawURL string, status int, body []byte) *source.Error {
	path := logPath(rawURL)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("hubspot: GET %s: %d", path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("hubspot: GET %s: %d", path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("hubspot: GET %s: %d", path, status)}
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: GET %s: %d: %s", path, status, snippet)}
	}
}

// --- warnings ---

// addWarnings files the problems one call skipped over under its ticket
// id, alongside whatever an earlier call for the same id recorded: a
// bundle is assembled from Get, Threads and Attachments, and the caller
// reads WarningsFor once at the end. An identical line is dropped, since
// Threads and Attachments walk the same message feed.
func (c *Client) addWarnings(id string, warnings []string) {
	c.warnings.Add(id, warnings...)
}

// WarningsFor implements source.Warner: it returns and consumes every
// problem recorded across the calls made for ticket id so far.
func (c *Client) WarningsFor(id string) []string {
	return c.warnings.Take(id)
}
