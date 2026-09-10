// Package jira implements source.Tracker — and, for Jira Service Management
// projects, source.Helpdesk — against Jira's REST API, covering both Jira
// Cloud and Jira Data Center/Server with one client.
//
// Everything is read through /rest/api/2 so rich-text fields arrive as wiki
// markup strings rather than Atlassian Document Format JSON: v2 is a
// compatibility layer on Cloud and the native API on Data Center, and it
// spares the adapter an ADF walker. The one place the two deployments
// genuinely differ is search — Cloud removed the classic /search endpoint in
// August 2025 in favour of POST /rest/api/3/search/jql with a
// nextPageToken cursor, while Data Center still serves GET /rest/api/2/search
// with startAt — so the client detects the deployment once (via
// /rest/api/2/serverInfo) and caches it for its lifetime.
package jira

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Deployment values accepted by Config.Deployment.
const (
	DeploymentCloud      = "cloud"
	DeploymentDataCenter = "datacenter"
	DeploymentAuto       = "auto"
)

// maxRetryAfter bounds how long a 429's Retry-After will be honoured before
// the client gives up and reports source.RateLimited instead of blocking the
// triage run behind an hour-long quota reset.
const maxRetryAfter = 30 * time.Second

// Config is the adapter's configuration. Secrets arrive already resolved by
// the wiring layer, so every field is a plain string.
type Config struct {
	// BaseURL is the site URL: https://acme.atlassian.net for Cloud, or the
	// Data Center instance's own base URL.
	BaseURL string
	// Deployment is "cloud", "datacenter", or "auto"/"" to detect it from
	// /rest/api/2/serverInfo on first use.
	Deployment string
	// Email and APIToken are the Cloud credentials, sent as HTTP basic auth.
	Email, APIToken string
	// PAT is the Data Center personal access token, sent as a bearer token.
	PAT string
	// ProjectKey, when set, scopes List to that project.
	ProjectKey string
	// EpicLinkField names the Data Center epic-link custom field, either as
	// an id ("customfield_10014") or as a field name resolved through
	// /rest/api/2/field. Empty means "discover it by the name Epic Link",
	// which is skipped entirely on Cloud where fields.parent carries the
	// epic.
	EpicLinkField string
}

// Client is a Jira REST client implementing source.Tracker. Call Helpdesk
// for the source.Helpdesk view of the same issues.
type Client struct {
	cfg    Config
	base   string // BaseURL, trailing slash trimmed
	scheme string // scheme of BaseURL, lowercased
	// host is the one host this client will ever send its credential to,
	// normalised (lowercased, trailing dot and default port removed) so a
	// URL taken out of an API response can be compared against it.
	host string
	hc   *http.Client

	// mu guards the caches below and the warning map. One Client serves
	// every run in a batch, so two tickets can be inside a call at once.
	mu           sync.Mutex
	deployment   string // resolved deployment; "" until detected
	epicField    string // resolved epic-link custom field id; "" when none
	epicResolved bool
	// warnings holds the non-fatal problems each call recorded, keyed by the
	// ticket key it was called with ("" for List, which is not about one
	// ticket). An entry is written when the call ends and removed when it is
	// read.
	warnings map[string][]string
	// lastID is the ticket whose call finished most recently, which is all
	// the argument-less Warnings can offer.
	lastID string
}

// New validates cfg and returns a Client. hc may be nil, in which case a
// client with a 30 s timeout is used.
func New(cfg Config, hc *http.Client) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("jira: baseUrl is required")
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("jira: baseUrl %q must be an absolute http(s) URL", cfg.BaseURL)
	}

	switch dep := strings.ToLower(strings.TrimSpace(cfg.Deployment)); dep {
	case "", DeploymentAuto:
		cfg.Deployment = DeploymentAuto
	case DeploymentCloud, DeploymentDataCenter:
		cfg.Deployment = dep
	default:
		return nil, fmt.Errorf("jira: deployment %q must be one of cloud, datacenter, auto", cfg.Deployment)
	}

	if !hasBasicCreds(cfg) && cfg.PAT == "" {
		return nil, fmt.Errorf("jira: credentials required: email and apiToken (cloud) or pat (data center)")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}

	scheme := strings.ToLower(u.Scheme)
	c := &Client{cfg: cfg, base: base, scheme: scheme, host: normalizeHost(scheme, u.Host), hc: hc}
	if cfg.Deployment != DeploymentAuto {
		c.deployment = cfg.Deployment
	}
	return c, nil
}

func hasBasicCreds(cfg Config) bool { return cfg.Email != "" && cfg.APIToken != "" }

// browseURL builds the user-facing issue URL. The issue payload's self link
// is the API URL, which is not what an operator wants to click.
func (c *Client) browseURL(key string) string {
	if key == "" {
		return ""
	}
	return c.base + "/browse/" + key
}

// --- deployment detection ---

type serverInfo struct {
	BaseURL        string `json:"baseUrl"`
	Version        string `json:"version"`
	DeploymentType string `json:"deploymentType"`
	ServerTitle    string `json:"serverTitle"`
}

// Ping fetches /rest/api/2/serverInfo, which doubles as the deployment
// probe: doctor gets a real round trip with the configured credentials and
// the client caches whether it is talking to Cloud or Data Center.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.serverInfo(ctx)
	return err
}

func (c *Client) serverInfo(ctx context.Context) (serverInfo, error) {
	var si serverInfo
	if err := c.doJSON(ctx, http.MethodGet, "/rest/api/2/serverInfo", nil, nil, &si); err != nil {
		return si, err
	}
	switch strings.ToLower(strings.ReplaceAll(si.DeploymentType, " ", "")) {
	case "cloud":
		c.setDeployment(DeploymentCloud)
	case "server", "datacenter":
		c.setDeployment(DeploymentDataCenter)
	}
	return si, nil
}

// setDeployment caches a detected deployment. An explicit Config.Deployment
// always wins, so a probe against a proxy that misreports itself cannot
// override the operator.
func (c *Client) setDeployment(d string) {
	if c.cfg.Deployment != DeploymentAuto {
		return
	}
	c.mu.Lock()
	c.deployment = d
	c.mu.Unlock()
}

func (c *Client) cachedDeployment() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deployment
}

// deployment returns the cached deployment, probing serverInfo once if it is
// not known yet and falling back to a hostname guess when the probe fails —
// a failed probe should not turn into a failed List.
func (c *Client) deploymentFor(ctx context.Context) string {
	if d := c.cachedDeployment(); d != "" {
		return d
	}
	if _, err := c.serverInfo(ctx); err == nil {
		if d := c.cachedDeployment(); d != "" {
			return d
		}
	}
	d := c.guessDeployment()
	c.setDeployment(d)
	return d
}

// guessDeployment infers the deployment from the base URL and the shape of
// the credentials, without a network call. It is the pre-detection default
// used to pick an auth header for the very first request.
func (c *Client) guessDeployment() string {
	h := c.host
	if bare, _, err := net.SplitHostPort(h); err == nil {
		h = bare
	}
	if strings.HasSuffix(h, ".atlassian.net") || strings.HasSuffix(h, ".jira.com") {
		return DeploymentCloud
	}
	if c.cfg.PAT != "" {
		return DeploymentDataCenter
	}
	if hasBasicCreds(c.cfg) {
		return DeploymentCloud
	}
	return DeploymentDataCenter
}

// --- transport ---

// setHeaders applies the auth header and the headers every Jira request
// carries. Cloud authenticates with basic email:apiToken; Data Center with a
// bearer PAT. When both are configured the deployment decides, and when the
// deployment is not known yet whichever credential is present wins.
func (c *Client) setHeaders(req *http.Request) {
	dep := c.cachedDeployment()
	if dep == "" {
		dep = c.guessDeployment()
	}
	basic := hasBasicCreds(c.cfg)
	switch {
	case c.cfg.PAT != "" && (dep == DeploymentDataCenter || !basic):
		req.Header.Set("Authorization", "Bearer "+c.cfg.PAT)
	case basic:
		token := base64.StdEncoding.EncodeToString([]byte(c.cfg.Email + ":" + c.cfg.APIToken))
		req.Header.Set("Authorization", "Basic "+token)
	}
	req.Header.Set("Accept", "application/json")
	// Jira's XSRF check rejects some non-GET and attachment requests without
	// this; it is inert on the ones that do not need it.
	req.Header.Set("X-Atlassian-Token", "no-check")
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return &source.Error{Code: source.Internal, Message: fmt.Sprintf("jira: %s %s: encode request: %v", method, path, err)}
		}
		payload = b
	}
	raw, err := c.doRaw(ctx, method, path, query, payload)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("jira: %s %s: decode: %v", method, path, err)}
	}
	return nil
}

// doRaw issues one authenticated request, retrying exactly once when Jira
// answers 429 with a Retry-After the client is willing to wait out.
func (c *Client) doRaw(ctx context.Context, method, path string, query url.Values, payload []byte) ([]byte, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	for attempt := 0; ; attempt++ {
		var rdr io.Reader
		if payload != nil {
			rdr = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rdr)
		if err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("jira: %s %s: %v", method, path, err)}
		}
		c.setHeaders(req)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("jira: %s %s: %v", method, path, err)}
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if d, ok := retryAfter(resp.Header); ok {
				if err := sleepCtx(ctx, d); err != nil {
					return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("jira: %s %s: %v", method, path, err)}
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, statusError(method, path, resp.StatusCode, body)
		}
		if readErr != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("jira: %s %s: read body: %v", method, path, readErr)}
		}
		return body, nil
	}
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

// statusError maps a non-2xx response to a *source.Error. Only the Internal
// case carries any of the response body, capped at 200 bytes; auth failures
// deliberately report nothing but the status so no credential can ride out
// in a message.
func statusError(method, path string, status int, body []byte) *source.Error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("jira: %s %s: %d", method, path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("jira: %s %s: %d", method, path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("jira: %s %s: %d", method, path, status)}
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("jira: %s %s: %d: %s", method, path, status, snippet)}
	}
}

// --- warnings ---

// collector gathers the non-fatal problems one call runs into. It travels on
// that call's context rather than living on the Client, so two tickets in
// flight at the same time cannot pick up each other's warnings on the way
// through shared helpers like the field lookup.
type collector struct {
	mu   sync.Mutex
	msgs []string
}

type collectorKey struct{}

// withCollector attaches a fresh collector to ctx for the duration of one
// public call.
func withCollector(ctx context.Context) (context.Context, *collector) {
	col := &collector{}
	return context.WithValue(ctx, collectorKey{}, col), col
}

// warnCtx records a non-fatal problem against the call ctx belongs to. A
// context with no collector — an internal caller, or a test calling a helper
// directly — silently drops it, which is the right answer for a message
// nobody is going to read.
func warnCtx(ctx context.Context, format string, args ...any) {
	col, ok := ctx.Value(collectorKey{}).(*collector)
	if !ok {
		return
	}
	col.mu.Lock()
	col.msgs = append(col.msgs, fmt.Sprintf(format, args...))
	col.mu.Unlock()
}

func (col *collector) take() []string {
	col.mu.Lock()
	defer col.mu.Unlock()
	msgs := col.msgs
	col.msgs = nil
	return msgs
}

// publish files a finished call's warnings under the ticket it was about, so
// a later WarningsFor(id) finds them. id is "" for List, which is not about
// one ticket.
func (c *Client) publish(id string, col *collector) {
	msgs := col.take()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastID = id
	if len(msgs) == 0 {
		delete(c.warnings, id)
		return
	}
	if c.warnings == nil {
		c.warnings = map[string][]string{}
	}
	c.warnings[id] = msgs
}

// WarningsFor implements source.Warner: it returns and consumes the
// non-fatal problems the call for ticket id recorded — an attachment that
// would not download, an epic-link field that would not resolve, a search cut
// short by Jira's cursor handing back a page it had already served — so a
// partial result never reaches the agent looking complete. It is keyed by id
// so a caller running several tickets at once cannot be handed another
// ticket's missing evidence.
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

// --- issue fetch ---

// baseIssueFields is the field set every issue request asks for. Naming them
// keeps the payload small and the wire shape stable; the epic-link custom
// field is appended when the instance has one.
var baseIssueFields = []string{
	"summary", "description", "priority", "status", "assignee", "reporter",
	"resolution", "issuetype", "project", "labels", "parent", "created",
	"updated", "attachment", "comment",
}

func (c *Client) issueFieldList(ctx context.Context) []string {
	fields := append([]string(nil), baseIssueFields...)
	if ef := c.epicLinkField(ctx); ef != "" {
		fields = append(fields, ef)
	}
	return fields
}

// epicLinkField resolves the custom field carrying epic linkage, once per
// client. Cloud folded Epic Link into the standard parent field, so nothing
// is resolved there unless the operator named a field explicitly. The id is
// instance-specific even on Data Center, so it is looked up by name rather
// than hardcoded to the customfield_10014 that most instances happen to use.
func (c *Client) epicLinkField(ctx context.Context) string {
	c.mu.Lock()
	if c.epicResolved {
		f := c.epicField
		c.mu.Unlock()
		return f
	}
	c.mu.Unlock()

	var id string
	switch named := strings.TrimSpace(c.cfg.EpicLinkField); {
	case strings.HasPrefix(named, "customfield_"):
		id = named
	case named != "":
		id = c.lookupFieldID(ctx, named)
	case c.deploymentFor(ctx) == DeploymentDataCenter:
		id = c.lookupFieldID(ctx, "Epic Link")
	}

	c.mu.Lock()
	c.epicField, c.epicResolved = id, true
	c.mu.Unlock()
	return id
}

type jiraField struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Custom bool   `json:"custom"`
}

// lookupFieldID resolves a field name to its id. A failure here is not fatal:
// the issue is still perfectly readable without its epic link, so the problem
// is recorded as a warning and the epic key comes back empty.
func (c *Client) lookupFieldID(ctx context.Context, name string) string {
	var fields []jiraField
	if err := c.doJSON(ctx, http.MethodGet, "/rest/api/2/field", nil, nil, &fields); err != nil {
		warnCtx(ctx, "jira: resolve field %q: %v", name, err)
		return ""
	}
	for _, f := range fields {
		if strings.EqualFold(strings.TrimSpace(f.Name), name) {
			return f.ID
		}
	}
	warnCtx(ctx, "jira: field %q not found on this instance", name)
	return ""
}

func (c *Client) cachedEpicField() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epicField
}

// fetchIssue reads one issue through the v2 API, so description and comment
// bodies arrive as wiki markup text on both deployments.
func (c *Client) fetchIssue(ctx context.Context, key string) (*jiraIssue, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, &source.Error{Code: source.NotFound, Message: "jira: empty issue key"}
	}
	q := url.Values{}
	q.Set("fields", strings.Join(c.issueFieldList(ctx), ","))

	var iss jiraIssue
	if err := c.doJSON(ctx, http.MethodGet, "/rest/api/2/issue/"+url.PathEscape(key), q, nil, &iss); err != nil {
		return nil, err
	}
	return &iss, nil
}

// Get implements source.Tracker.
func (c *Client) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	ctx, col := withCollector(ctx)
	defer c.publish(key, col)

	iss, err := c.fetchIssue(ctx, key)
	if err != nil {
		return ticket.TrackerTicket{}, err
	}
	return c.mapTracker(iss), nil
}

// --- helpdesk view ---

// Helpdesk returns the source.Helpdesk view of the same client, for issues
// in a Jira Service Management project where the tracker record and the
// customer conversation are the same issue. Client itself cannot satisfy
// both interfaces: their Get methods collide by name with different
// signatures.
func (c *Client) Helpdesk() source.Helpdesk { return helpdeskView{c} }

type helpdeskView struct{ *Client }

func (h helpdeskView) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	return h.Client.getHelpdesk(ctx, id)
}

func (c *Client) getHelpdesk(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	ctx, col := withCollector(ctx)
	defer c.publish(id, col)

	iss, err := c.fetchIssue(ctx, id)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}
	return c.mapHelpdesk(iss), nil
}

var (
	_ source.Tracker  = (*Client)(nil)
	_ source.Helpdesk = helpdeskView{}
	_ source.Warner   = (*Client)(nil)
	_ source.Warner   = helpdeskView{}
)
