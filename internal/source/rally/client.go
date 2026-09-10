// Package rally implements source.Tracker (and, through Client.Helpdesk, a
// source.Helpdesk view over the artifact's discussion and attachments)
// against Broadcom Rally's Web Services API v2.0.
//
// Everything is read-only: artifacts are fetched by FormattedID through the
// per-type query collections, the discussion is read from conversationpost,
// and attachment bytes come from the artifact's Attachment records via the
// base64 Content held on the linked AttachmentContent object.
package rally

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// apiPrefix is the WSAPI v2.0 path every request hangs off.
const apiPrefix = "/slm/webservice/v2.0"

// defaultBaseURL is Rally's North American production host. Subscriptions
// can be provisioned on other hosts, so Config.BaseURL overrides it.
const defaultBaseURL = "https://rally1.rallydev.com"

// maxPageSize is WSAPI's hard per-page ceiling; pagesize is clamped to it.
const maxPageSize = 200

// maxRetryAfter caps how long a 429's Retry-After is honoured before the
// call gives up and reports source.RateLimited to the caller.
const maxRetryAfter = 30 * time.Second

// List bounds. A caller that asks for no limit gets defaultListLimit, and
// nobody gets more than maxListLimit: an unbounded sweep of a Rally
// subscription is a way to hang a triage run, not a useful default.
const (
	defaultListLimit = 100
	maxListLimit     = 200
)

// Response body ceilings. Both are enforced with io.LimitReader and fail
// closed, so a truncated body is never decoded as if it were complete.
const (
	// maxJSONBody bounds an ordinary WSAPI JSON response. Rally caps
	// Description and Notes at 32KB and a page at 200 rows, so 8 MiB is
	// far above anything legitimate.
	maxJSONBody = 8 << 20
	// maxAttachmentBody bounds an AttachmentContent response, which
	// carries the file base64-encoded: Rally's ~50 MB attachment
	// ceiling plus base64's ~33% inflation fits inside 64 MiB.
	maxAttachmentBody = 64 << 20
	// maxAttachmentBytes bounds the decoded file.
	maxAttachmentBytes = 50 << 20
)

// defaultTypes are the artifact types Get and List sweep when Config.Types
// is empty: the two that carry the work Sirdar triages.
var defaultTypes = []string{"Defect", "HierarchicalRequirement"}

// Config holds the settings for one Rally subscription. Every field is a
// plain string: secrets are already resolved by the wiring layer.
type Config struct {
	// BaseURL is the subscription's host, without the WSAPI path
	// (default https://rally1.rallydev.com).
	BaseURL string
	// APIKey is sent as the ZSESSIONID header on every request.
	APIKey string
	// Workspace is a workspace _ref or ObjectID used to scope queries.
	Workspace string
	// Project is a project _ref or ObjectID used to scope List.
	Project string
	// Types are the artifact types Get falls back to and List sweeps
	// (default Defect, HierarchicalRequirement). Values are Rally type
	// names, e.g. "Defect", "HierarchicalRequirement",
	// "PortfolioItem/Feature".
	Types []string
	// HelpdeskField is the custom field (typically c_-prefixed) holding
	// the helpdesk ticket reference. Empty leaves HelpdeskRef unset.
	HelpdeskField string
}

// Client talks to one Rally subscription. It implements source.Tracker and
// source.Warner, and exposes the discussion/attachment side of an artifact
// through Helpdesk.
type Client struct {
	cfg  Config
	http *http.Client

	// mu guards the mutable per-client state below: the per-ticket
	// warnings recorded by recent calls and the current user cached by
	// Ping. One Client serves every ticket in a run, so the warnings are
	// keyed by ticket rather than by "most recent call" — two tickets
	// fetched at once would otherwise swap each other's missing evidence.
	mu       sync.Mutex
	warnings map[string][]string
	userRef  string
	userName string
}

// warnBuf collects the non-fatal problems one public call records, so they
// can be filed under the ticket that call was for. A nil *warnBuf swallows
// everything, which is what the internal helpers want when no ticket is in
// scope.
type warnBuf struct {
	msgs []string
}

func (w *warnBuf) addf(format string, args ...any) {
	if w == nil {
		return
	}
	w.msgs = append(w.msgs, fmt.Sprintf(format, args...))
}

var (
	_ source.Tracker = (*Client)(nil)
	_ source.Warner  = (*Client)(nil)
)

// New validates cfg, applies defaults, and returns a Client. hc may be nil,
// in which case a client with a 30 s timeout is used.
func New(cfg Config, hc *http.Client) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("rally: apiKey is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("rally: invalid baseUrl %q: %w", cfg.BaseURL, err)
	}
	if len(cfg.Types) == 0 {
		cfg.Types = append([]string(nil), defaultTypes...)
	} else {
		types := make([]string, 0, len(cfg.Types))
		for _, t := range cfg.Types {
			if t = strings.TrimSpace(t); t != "" {
				types = append(types, t)
			}
		}
		if len(types) == 0 {
			return nil, fmt.Errorf("rally: types must not be all-empty")
		}
		cfg.Types = types
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{cfg: cfg, http: hc}, nil
}

// WarningsFor implements source.Warner: it returns and consumes the
// non-fatal problems the call for ticket id recorded — an attachment that
// would not download, a WSAPI warning attached to the query — so a partial
// result never quietly looks complete. Keying by id is what a caller
// running several tickets at once needs: it cannot be handed another
// ticket's missing evidence.
func (c *Client) WarningsFor(id string) []string {
	id = strings.TrimSpace(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.warnings[id]
	delete(c.warnings, id)
	if len(w) == 0 {
		return nil
	}
	return append([]string(nil), w...)
}

// putWarnings files the problems one call skipped over under its ticket,
// replacing anything an earlier call for the same ticket left behind.
func (c *Client) putWarnings(id string, warnings []string) {
	id = strings.TrimSpace(id)
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

// --- HTTP plumbing ---

// endpoint builds an absolute WSAPI URL for a type path or resource path
// (e.g. "defect", "conversationpost", "user").
func (c *Client) endpoint(path string) string {
	return c.cfg.BaseURL + apiPrefix + "/" + strings.TrimLeft(path, "/")
}

// refURL turns a _ref returned by the API into an absolute URL on the
// configured base host. Only the ref's path and query are kept, so a
// payload can never redirect a request (carrying the API key) at a host
// the operator did not configure.
func (c *Client) refURL(ref string) (string, error) {
	u, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("parse ref %q: %w", ref, err)
	}
	if u.Path == "" {
		return "", fmt.Errorf("ref %q has no path", ref)
	}
	out := c.cfg.BaseURL + u.Path
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out, nil
}

// get issues an authenticated GET against an absolute URL and decodes a 2xx
// JSON body into out. Non-2xx responses map to a *source.Error; a 429 is
// retried once after honouring a Retry-After of at most maxRetryAfter.
func (c *Client) get(ctx context.Context, rawURL string, out any) error {
	return c.getLimited(ctx, rawURL, maxJSONBody, out)
}

// getLimited is get with an explicit body ceiling, for the one response
// (AttachmentContent) that is legitimately larger than a JSON record.
func (c *Client) getLimited(ctx context.Context, rawURL string, limit int64, out any) error {
	body, err := c.getRaw(ctx, rawURL, limit)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("rally: GET %s: decode: %v", logPath(rawURL), err)}
	}
	return nil
}

func (c *Client) getRaw(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	body, status, retryAfter, err := c.do(ctx, rawURL, limit)
	if err != nil {
		return nil, err
	}
	if status == http.StatusTooManyRequests {
		wait, ok := retryAfter, false
		if wait > 0 && wait <= maxRetryAfter {
			ok = true
		}
		if !ok {
			return nil, statusError(rawURL, status, body)
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("rally: GET %s: %v", logPath(rawURL), err)}
		}
		body, status, _, err = c.do(ctx, rawURL, limit)
		if err != nil {
			return nil, err
		}
	}
	if status < 200 || status >= 300 {
		return nil, statusError(rawURL, status, body)
	}
	return body, nil
}

// do performs one request, returning the body, status and parsed
// Retry-After. Transport-level failures come back as a *source.Error.
func (c *Client) do(ctx context.Context, rawURL string, limit int64) ([]byte, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, 0, &source.Error{Code: source.Internal, Message: fmt.Sprintf("rally: GET %s: %v", logPath(rawURL), err)}
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, 0, &source.Error{Code: source.Internal, Message: fmt.Sprintf("rally: GET %s: %v", logPath(rawURL), err)}
	}
	defer resp.Body.Close()

	body, readErr := readLimited(resp.Body, limit)
	if readErr != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil, 0, 0, &source.Error{Code: source.Internal, Message: fmt.Sprintf("rally: GET %s: read body: %v", logPath(rawURL), readErr)}
	}
	return body, resp.StatusCode, parseRetryAfter(resp.Header.Get("Retry-After")), nil
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

// setHeaders applies the API key. Rally authenticates WSAPI requests with
// the key in the ZSESSIONID header; there is no bearer scheme.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("ZSESSIONID", c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
}

// parseRetryAfter reads a Retry-After header in either of its two forms
// (delta-seconds or an HTTP date), returning 0 when it is absent or
// unparseable.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// logPath reduces a request URL to method-and-path form for error messages,
// dropping the query string so a query never leaks into a log line and
// keeping the credential (which only ever travels in a header) out of it.
func logPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<url>"
	}
	return u.Path
}

// statusError maps a non-2xx HTTP response to a *source.Error, quoting at
// most 200 bytes of the body and never the credential.
func statusError(rawURL string, status int, body []byte) *source.Error {
	path := logPath(rawURL)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("rally: GET %s: %d", path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("rally: GET %s: %d", path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("rally: GET %s: %d", path, status)}
	default:
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("rally: GET %s: %d: %s", path, status, snippet(body))}
	}
}

func snippet(body []byte) []byte {
	if len(body) > 200 {
		return body[:200]
	}
	return body
}

// --- Ping / current user ---

// userResult is the current-user response. Rally has been observed to wrap
// it in either OperationResult or a bare User key depending on the
// subscription's WSAPI build, so both are accepted.
type userResult struct {
	OperationResult struct {
		Errors   []string  `json:"Errors"`
		Warnings []string  `json:"Warnings"`
		User     *userInfo `json:"User"`
	} `json:"OperationResult"`
	User *userInfo `json:"User"`
}

type userInfo struct {
	Ref         string `json:"_ref"`
	UserName    string `json:"UserName"`
	DisplayName string `json:"DisplayName"`
	EmailAddr   string `json:"EmailAddress"`
}

// Ping checks the credential by reading the current user, and caches that
// user's _ref and UserName so List can resolve an "me" assignee filter.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.currentUser(ctx)
	return err
}

// currentUser returns the cached current user, fetching it once on first
// use. The zero UserName is not cached, so a subscription that answers
// without one is retried rather than silently filtering on "".
func (c *Client) currentUser(ctx context.Context) (userInfo, error) {
	c.mu.Lock()
	ref, name := c.userRef, c.userName
	c.mu.Unlock()
	if name != "" {
		return userInfo{Ref: ref, UserName: name}, nil
	}

	var res userResult
	if err := c.get(ctx, c.endpoint("user"), &res); err != nil {
		return userInfo{}, err
	}
	if errs := res.OperationResult.Errors; len(errs) > 0 {
		return userInfo{}, resultError("user", errs)
	}
	u := res.OperationResult.User
	if u == nil {
		u = res.User
	}
	if u == nil || u.UserName == "" {
		return userInfo{}, &source.Error{Code: source.Internal, Message: "rally: GET " + apiPrefix + "/user: response carried no User"}
	}

	c.mu.Lock()
	c.userRef, c.userName = u.Ref, u.UserName
	c.mu.Unlock()
	return *u, nil
}

// resultError maps the Errors array WSAPI returns inside a 200 response to
// a *source.Error: an authorisation complaint becomes source.Auth, anything
// else source.Internal.
func resultError(what string, errs []string) *source.Error {
	msg := strings.Join(errs, "; ")
	if isAuthMessage(msg) {
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("rally: %s: %s", what, msg)}
	}
	return &source.Error{Code: source.Internal, Message: fmt.Sprintf("rally: %s: %s", what, msg)}
}

// isAuthMessage recognises the phrasings Rally uses when the API key is
// missing, wrong, or lacks permission for the queried scope.
func isAuthMessage(msg string) bool {
	l := strings.ToLower(msg)
	for _, needle := range []string{
		"unauthorized", "unauthorised", "not authorized", "not authorised",
		"invalid key", "invalid api key", "credentials", "permission",
	} {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

// --- Helpdesk view ---

// Helpdesk returns the source.Helpdesk view of this tracker: the artifact
// itself as the "ticket", its discussion posts as the thread, and its
// Rally attachments as the files. It is a separate view rather than more
// methods on Client because Client.Get already serves source.Tracker and Go
// will not carry two methods of one name.
func (c *Client) Helpdesk() source.Helpdesk { return helpdeskView{c} }

type helpdeskView struct{ *Client }

var (
	_ source.Helpdesk = helpdeskView{}
	_ source.Warner   = helpdeskView{}
)

// Get returns the artifact as a helpdesk-shaped record so a caller that
// only wants the conversation side still gets the identifying fields.
func (h helpdeskView) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	var w warnBuf
	art, err := h.Client.find(ctx, id, &w)
	h.Client.putWarnings(id, w.msgs)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}
	return ticket.HelpdeskTicket{
		ID:        art.a.FormattedID,
		Subject:   art.a.Name,
		Status:    art.status(),
		Priority:  art.a.Priority,
		Channel:   "rally",
		Contact:   art.a.Owner.name(),
		Customer:  art.a.Project.name(),
		URL:       h.Client.artifactURL(art),
		CreatedAt: parseRallyTime(art.a.CreationDate),
		UpdatedAt: parseRallyTime(art.a.LastUpdateDate),
		Fields:    art.fields(h.Client.cfg.HelpdeskField),
	}, nil
}
