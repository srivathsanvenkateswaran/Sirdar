// Package azdo implements source.Tracker against the Azure DevOps Work Item
// Tracking REST API, and — through Helpdesk() — the conversation side of a
// work item: its comments, and the files attached to it.
//
// Everything is read-only and goes over stdlib HTTP with the organisation's
// personal access token sent as HTTP basic auth with an empty user name,
// which is how Azure DevOps accepts a PAT.
package azdo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

const (
	// apiVersion is pinned to 7.1: it is the GA moniker for every work item
	// endpoint this adapter uses. Comments are only published as a preview
	// route, hence the separate moniker.
	apiVersion         = "7.1"
	commentsAPIVersion = "7.1-preview.4"

	// batchLimit is the documented maximum number of ids accepted by
	// _apis/wit/workitemsbatch in one call.
	batchLimit = 200

	// commentsPageSize is the $top used when walking a work item's comments.
	commentsPageSize = 200

	// maxRetryAfter caps how long a 429's Retry-After is honoured before the
	// call gives up and reports source.RateLimited to the caller.
	maxRetryAfter = httpx.MaxRetryAfter

	// httpTimeout is the per-request timeout used when the caller does not
	// supply its own *http.Client.
	httpTimeout = 30 * time.Second

	// maxJSONBody bounds an ordinary API response (a work item, a page of
	// comments, a batch). The read fails rather than truncating, so a short
	// but well-formed document is never decoded as if it were complete.
	maxJSONBody = 8 << 20
)

// Config is the resolved configuration for one Azure DevOps organisation and
// project. Secrets arrive already resolved by the wiring layer.
type Config struct {
	// OrgURL is https://dev.azure.com/{org} for Azure DevOps Services, or
	// the collection URL (https://{server}/{collection}) for Server.
	OrgURL string
	// Project is the team project the work items live in.
	Project string
	// PAT is the personal access token; it needs the vso.work scope.
	PAT string
	// HelpdeskLinkDomain, when set, is the host suffix that marks a
	// Hyperlink relation as the work item's helpdesk ticket.
	HelpdeskLinkDomain string
	// HelpdeskField, when set, is the reference name of a custom field
	// holding the helpdesk ticket id, used when no Hyperlink relation
	// matches HelpdeskLinkDomain.
	HelpdeskField string
}

// Client is an Azure DevOps work item client. It implements source.Tracker
// directly; Helpdesk() returns the source.Helpdesk view over the same work
// item (its comments and attached files).
type Client struct {
	cfg  Config
	base string // OrgURL, trailing slash trimmed
	hc   *http.Client
	// trust decides which hosts an attachment may be fetched from with the
	// PAT: the organisation's own host, plus — for Azure DevOps Services —
	// the well-known hosts serving the same organisation.
	trust *httpx.Trust

	// batchSize is how many ids go into one workitemsbatch call. It is
	// batchLimit in production and lowered by tests to exercise chunking.
	batchSize int

	// warnings holds the non-fatal problems recorded per ticket id, so a
	// caller running several tickets at once is never handed another
	// ticket's missing evidence.
	warnings httpx.Warnings
}

// New validates cfg and returns a Client. A nil hc gets a default client
// with a 30s timeout.
func New(cfg Config, hc *http.Client) (*Client, error) {
	cfg.OrgURL = strings.TrimRight(strings.TrimSpace(cfg.OrgURL), "/")
	cfg.Project = strings.TrimSpace(cfg.Project)
	cfg.HelpdeskLinkDomain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cfg.HelpdeskLinkDomain)), ".")
	cfg.HelpdeskField = strings.TrimSpace(cfg.HelpdeskField)

	if cfg.OrgURL == "" {
		return nil, fmt.Errorf("azure devops: orgUrl is required")
	}
	u, err := url.Parse(cfg.OrgURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("azure devops: orgUrl %q is not an absolute URL", cfg.OrgURL)
	}
	if cfg.Project == "" {
		return nil, fmt.Errorf("azure devops: project is required")
	}
	if cfg.PAT == "" {
		return nil, fmt.Errorf("azure devops: pat is required")
	}
	if hc == nil {
		hc = &http.Client{Timeout: httpTimeout}
	}
	trust, err := httpx.NewTrust(cfg.OrgURL, servicesRules(u)...)
	if err != nil {
		return nil, fmt.Errorf("azure devops: orgUrl %q is not an absolute URL", cfg.OrgURL)
	}
	return &Client{cfg: cfg, base: cfg.OrgURL, hc: hc, trust: trust, batchSize: batchLimit}, nil
}

// servicesRules are the extra hosts that serve attachments for an Azure
// DevOps Services organisation: dev.azure.com and the legacy
// {org}.visualstudio.com, whichever of the two the base URL is not. Both
// belong to the organisation, so both may see the PAT. An Azure DevOps
// Server collection gets none: its own host is the only one that can serve
// its attachments.
func servicesRules(base *url.URL) []httpx.HostRule {
	org := orgName(base)
	if org == "" {
		return nil
	}
	return []httpx.HostRule{
		{Suffix: "dev.azure.com", SendCredential: true},
		{Suffix: org + ".visualstudio.com", SendCredential: true},
	}
}

var (
	_ source.Tracker  = (*Client)(nil)
	_ source.Helpdesk = helpdeskView{}
	_ source.Warner   = (*Client)(nil)
	_ source.Warner   = helpdeskView{}
)

// Helpdesk returns a view of the client satisfying source.Helpdesk. Client
// itself satisfies source.Tracker; the two cannot be satisfied by one type
// because their Get methods collide by name with different signatures.
func (c *Client) Helpdesk() source.Helpdesk { return helpdeskView{c} }

// helpdeskView adapts Client to source.Helpdesk. Threads and Attachments
// are promoted from Client unchanged; only Get has to be redirected.
type helpdeskView struct{ *Client }

func (h helpdeskView) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	return h.Client.getHelpdesk(ctx, id)
}

// WarningsFor implements source.Warner: it returns and consumes the
// per-attachment failures the Attachments call for work item id recorded,
// so the caller can put them in the prompt and the run state instead of
// silently serving a short list of attachments. The id is normalised the
// same way Attachments normalises it, so "#123", "AB-123" and "123" all
// read back the same entry.
func (c *Client) WarningsFor(id string) []string {
	key, err := normalizeKey(id)
	if err != nil {
		key = id
	}
	return c.warnings.Take(key)
}

// addWarnings records the failures one call skipped over, alongside
// whatever an earlier call for the same work item recorded: one work item's
// bundle is Get, then Threads, then Attachments, with a single WarningsFor
// at the end, and reading is what clears the entry (see httpx.Warnings).
func (c *Client) addWarnings(id string, warnings []string) {
	c.warnings.Add(id, warnings...)
}

// Ping checks that the org URL, project and PAT together reach a project the
// token can read. It backs the doctor command.
func (c *Client) Ping(ctx context.Context) error {
	u := c.base + "/_apis/projects/" + url.PathEscape(c.cfg.Project) + "?api-version=" + apiVersion
	_, err := c.do(ctx, http.MethodGet, u, nil)
	return err
}

// --- URLs ---

// projectURL joins a project-scoped API path (starting with "/") onto the
// organisation base and project.
func (c *Client) projectURL(path string) string {
	return c.base + "/" + url.PathEscape(c.cfg.Project) + path
}

// webURL builds the human-facing work item URL, used for List results where
// the batch response carries fields only and no _links.
func (c *Client) webURL(id string) string {
	return c.base + "/" + url.PathEscape(c.cfg.Project) + "/_workitems/edit/" + url.PathEscape(id)
}

// --- Keys ---

// normalizeKey turns a caller-supplied work item key into the numeric id
// Azure DevOps addresses work items by. It accepts "123", "#123" and
// prefixed forms like "AB-123", taking the last run of digits in the string
// so a prefix that itself contains digits ("AB1-234") still resolves to the
// id.
func normalizeKey(key string) (string, error) {
	s := strings.TrimSpace(key)
	end := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] >= '0' && s[i] <= '9' {
			end = i + 1
			break
		}
	}
	if end < 0 {
		return "", &source.Error{Code: source.NotFound, Message: fmt.Sprintf("azure devops: key %q has no work item id", key)}
	}
	start := end
	for start > 0 && s[start-1] >= '0' && s[start-1] <= '9' {
		start--
	}
	id := strings.TrimLeft(s[start:end], "0")
	if id == "" {
		id = "0"
	}
	return id, nil
}

// --- HTTP ---

// do issues an authenticated request and returns the response body for a 2xx
// JSON response. A 429 is retried once after honouring a Retry-After of at
// most maxRetryAfter; a 2xx that is actually the interactive sign-in page
// (Azure DevOps answers a bad or unscoped PAT with 203 and HTML) is reported
// as an auth failure rather than a decode failure.
func (c *Client) do(ctx context.Context, method, rawURL string, body []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := c.newRequest(ctx, method, rawURL, body)
		if err != nil {
			return nil, err
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("azure devops: %s %s: %v", method, displayPath(rawURL), err)}
		}
		b, readErr := httpx.ReadLimited(resp.Body, maxJSONBody)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if d, ok := httpx.RetryAfter(resp.Header, maxRetryAfter); ok {
				if err := httpx.SleepCtx(ctx, d); err != nil {
					return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("azure devops: %s %s: %v", method, displayPath(rawURL), err)}
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, statusError(method, displayPath(rawURL), resp.StatusCode, b)
		}
		if isSignInPage(resp.StatusCode, resp.Header.Get("Content-Type"), b) {
			return nil, signInError(method, displayPath(rawURL), resp.StatusCode)
		}
		if readErr != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("azure devops: %s %s: read body: %v", method, displayPath(rawURL), readErr)}
		}
		return b, nil
	}
}

// doJSON runs do and decodes the response into out.
func (c *Client) doJSON(ctx context.Context, method, rawURL string, body []byte, out any) error {
	b, err := c.do(ctx, method, rawURL, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("azure devops: %s %s: decode: %v", method, displayPath(rawURL), err)}
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, rawURL string, body []byte) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("azure devops: %s %s: %v", method, displayPath(rawURL), err)}
	}
	// Azure DevOps takes a PAT as the basic-auth password with an empty
	// (ignored) user name.
	req.SetBasicAuth("", c.cfg.PAT)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// displayPath reduces a request URL to its path, so error messages carry the
// endpoint without the query string.
func displayPath(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
		return u.Path
	}
	return rawURL
}

// statusError maps a non-2xx response to a *source.Error, quoting at most
// 200 bytes of the body and never the credential.
func statusError(method, path string, status int, body []byte) *source.Error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("azure devops: %s %s: %d", method, path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("azure devops: %s %s: %d", method, path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("azure devops: %s %s: %d", method, path, status)}
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("azure devops: %s %s: %d: %s", method, path, status, snippet)}
	}
}

func signInError(method, path string, status int) *source.Error {
	return &source.Error{Code: source.Auth, Message: fmt.Sprintf("azure devops: %s %s: %d: sign-in page returned instead of JSON; the PAT is invalid, expired, or missing the vso.work scope", method, path, status)}
}

// isSignInPage reports whether a 2xx response is the interactive sign-in
// page. Azure DevOps answers an unusable PAT with 203 Non-Authoritative
// Information and an HTML login document instead of a 401.
func isSignInPage(status int, contentType string, body []byte) bool {
	if status < 200 || status >= 300 {
		return false
	}
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return true
	}
	if status != http.StatusNonAuthoritativeInfo {
		return false
	}
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '<'
}

// --- Tracker ---

// getWorkItem fetches one work item with fields, relations and links.
func (c *Client) getWorkItem(ctx context.Context, id string) (workItem, error) {
	u := c.projectURL("/_apis/wit/workitems/" + url.PathEscape(id) + "?$expand=all&api-version=" + apiVersion)
	var wi workItem
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &wi); err != nil {
		return workItem{}, err
	}
	return wi, nil
}

// Get fetches a work item by key and maps it to a tracker ticket.
func (c *Client) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	id, err := normalizeKey(key)
	if err != nil {
		return ticket.TrackerTicket{}, err
	}
	wi, err := c.getWorkItem(ctx, id)
	if err != nil {
		return ticket.TrackerTicket{}, err
	}
	return c.mapTracker(wi), nil
}

// getHelpdesk maps the work item onto the helpdesk side of a bundle: it is
// the record the conversation returned by Threads belongs to.
func (c *Client) getHelpdesk(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	wid, err := normalizeKey(id)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}
	wi, err := c.getWorkItem(ctx, wid)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}
	return c.mapHelpdesk(wi), nil
}

// --- Comments ---

// Threads returns the work item's comments, oldest first, as thread
// messages. Comment text is Markdown by default in Azure DevOps; a comment
// authored as HTML is converted.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	wid, err := normalizeKey(id)
	if err != nil {
		return nil, err
	}

	var msgs ticket.Thread
	token := ""
	for page := 0; ; page++ {
		u := c.projectURL("/_apis/wit/workItems/" + url.PathEscape(wid) + "/comments" +
			"?api-version=" + commentsAPIVersion +
			"&$top=" + strconv.Itoa(commentsPageSize) +
			"&order=asc")
		if token != "" {
			u += "&continuationToken=" + url.QueryEscape(token)
		}
		var p commentsPage
		if err := c.doJSON(ctx, http.MethodGet, u, nil, &p); err != nil {
			return nil, err
		}
		for _, cm := range p.Comments {
			msgs = append(msgs, mapComment(cm))
		}
		// Guard against a server that keeps handing back the same token:
		// stop when it does not advance, and when a page came back empty.
		if p.ContinuationToken == "" || p.ContinuationToken == token || len(p.Comments) == 0 {
			break
		}
		token = p.ContinuationToken
	}
	return msgs, nil
}
