// Package servicenow implements source.Helpdesk — and, through Tracker(),
// source.Tracker — against one ServiceNow instance's REST API, over plain
// stdlib net/http.
//
// ServiceNow is both systems at once in most shops: the incident a
// customer raised is also the record engineering works, so the same client
// serves either role and the workspace decides which by naming it under
// sources.helpdesk or sources.tracker.
//
// Everything is read through two documented APIs: the Table API
// (/api/now/table/{table}) for the incident and for the journal entries
// behind its comments and work notes, and the Attachment API
// (/api/now/attachment) for the files hanging off it. Records are
// requested with sysparm_display_value=true so a reference field arrives
// as the name a human would read ("Abel Tuter") rather than the 32-hex
// sys_id behind it.
//
// The care this adapter takes that a plain REST client would not: an
// instance is a single tenant on a single host, so that host is the only
// one this client's credential is ever sent to, an attachment's
// download_link is checked against it before a request is built, and a
// redirect off it is refused rather than followed.
package servicenow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// instanceSuffix is the host every ServiceNow instance lives on. An
// instance named "acme" is https://acme.service-now.com.
const instanceSuffix = ".service-now.com"

// DefaultTable is the table an unconfigured adapter reads. ITSM's incident
// is the helpdesk analogue; a Customer Service Management shop points this
// at sn_customerservice_case and a fulfilment queue at sc_task.
const DefaultTable = "incident"

// journalTable holds the history of every journal field on every record:
// one row per comment or work note, keyed by the record's sys_id in
// element_id and the field's name in element.
const journalTable = "sys_journal_field"

// maxJSONBody bounds an ordinary API response. An incident with a hundred
// journal entries is measured in kilobytes; a body at this size is a fault
// or a hostile response, and the read fails rather than truncating, so a
// short but well-formed document is never decoded as if it were whole.
const maxJSONBody = 8 << 20

// maxRetryAfter bounds how long a 429's Retry-After is honoured before the
// call gives up and reports source.RateLimited rather than blocking a
// triage run behind a quota reset. ServiceNow's rate limits are set
// per-instance by the customer's own admin, so there is no published
// number to reason about — only the header the instance sends.
const maxRetryAfter = httpx.MaxRetryAfter

// maxSweepRetryWait bounds the total time one paginated sweep (List,
// journalEntries or attachmentRefs) spends waiting out 429s across every
// page it reads — see retryBudget. It is a var so a test can shrink it
// rather than waiting out 90 real seconds.
var maxSweepRetryWait = 90 * time.Second

// maxRedirects bounds how far a same-host redirect chain is followed
// before a request is abandoned.
const maxRedirects = 3

// Page sizes and page caps. Every list endpoint here is offset-paginated
// (sysparm_offset/sysparm_limit), which has no end-of-results signal
// beyond a short page: a cap is what stops a miscounting instance from
// paging forever, and a truncation warning is what stops the agent reading
// a half thread as a whole one.
const (
	journalPageSize    = 100
	maxJournalPages    = 20
	attachmentPageSize = 100
	maxAttachmentPages = 20
	listPageSize       = 100
	maxListPages       = 20
)

// List bounds from the adapter contract: no limit means defaultListResults
// and nothing returns more than maxListResults.
const (
	defaultListResults = 100
	maxListResults     = 200
)

// maxAttachmentBytes caps one download. Past it the file is refused rather
// than written: an attachment nobody has vouched for should not be able to
// fill the disk the run is using. It is a var so a test can shrink it
// rather than serve 64 MiB.
var maxAttachmentBytes int64 = 64 << 20

// Config holds one ServiceNow instance's settings. Secrets arrive already
// resolved by the wiring layer, so every field is a plain string.
type Config struct {
	// Instance is the instance name ("acme") or its full host
	// ("acme.service-now.com"). It builds both the request target and the
	// human-facing record URL.
	Instance string
	// BaseURL overrides the request target (default
	// "https://{Instance}.service-now.com"). Tests use it to point at a
	// local httptest server, and a shop behind a vanity domain uses it for
	// that; production configs normally leave it blank.
	BaseURL string
	// Table is the table records are read from, DefaultTable when empty.
	Table string

	// Username is the integration user's login name, a literal rather than
	// a credential reference: it is the half of basic auth that is not a
	// secret, and naming it in the config is what lets doctor say who the
	// connection authenticates as.
	Username string
	// Password is that user's password, already resolved.
	Password string
	// OAuthToken is an OAuth 2.0 access token, sent as a bearer header.
	// It is the alternative to Username/Password, never a companion to it.
	OAuthToken string

	// DateFormat resolves the one ambiguity a display-value timestamp can
	// carry: a dashed date like "03-04-2024" reads as either the 3rd of
	// April or the 4th of March, and only the operator knows which their
	// instance means. Empty (the default) reads only the unambiguous
	// layouts — ISO's yyyy-first order needs no guess — so a dashed date
	// simply will not parse until this names DateFormatMDY or
	// DateFormatDMY.
	DateFormat string
}

// Client talks to one ServiceNow instance. It implements source.Helpdesk
// and source.Warner; Tracker() returns the source.Tracker view of the same
// records.
type Client struct {
	baseURL string // request target, no trailing slash
	table   string
	// trust is the instance host and nothing else. ServiceNow serves an
	// attachment's bytes from the instance itself rather than from a CDN,
	// so there is no fetch-only tier here: the one trusted host is the one
	// that gets the credential, and any other host a response names is
	// refused.
	trust      *httpx.Trust
	authHeader string
	// authWho names the credential's owner for an error message. It is the
	// username, or "the OAuth token" — never any part of the secret.
	authWho string
	hc      *http.Client

	// warnings holds the non-fatal problems each call recorded, keyed by
	// the id it was called with ("" for List, which is about no one
	// ticket).
	warnings httpx.Warnings

	// dateFormat is DateFormatMDY, DateFormatDMY or "" (unambiguous
	// layouts only) — see Config.DateFormat.
	dateFormat string

	// records caches fetchRecord's result per input id for this client's
	// lifetime — see fetchRecord.
	records recordCache
}

// maxCachedRecords bounds recordCache so a long triage run over many
// distinct tickets cannot grow it without limit; a run over that many
// tickets simply re-fetches the oldest ones, the same cost paid before
// caching existed.
const maxCachedRecords = 256

// recordCache is fetchRecord's per-client cache, keyed by the exact id
// string the caller passed in. Safe for concurrent use, since nothing else
// here promises a client is used from one goroutine at a time.
type recordCache struct {
	mu    sync.Mutex
	order []string
	m     map[string]record
}

func (c *recordCache) get(id string) (record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.m[id]
	return rec, ok
}

func (c *recordCache) put(id string, rec record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]record{}
	}
	if _, exists := c.m[id]; !exists {
		if len(c.order) >= maxCachedRecords {
			evict := c.order[0]
			c.order = c.order[1:]
			delete(c.m, evict)
		}
		c.order = append(c.order, id)
	}
	c.m[id] = rec
}

var (
	_ source.Helpdesk = (*Client)(nil)
	_ source.Warner   = (*Client)(nil)
	_ source.Tracker  = trackerView{}
	_ source.Warner   = trackerView{}
)

// bareInstancePattern is what an instance name given without a host or a
// scheme must match: letters, digits and hyphens only. It is what stops
// "acme/x" from being read as a path and silently glued onto the
// service-now.com suffix as "https://acme/x.service-now.com" — a request
// that would carry the live credential to a host named "acme".
var bareInstancePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// ResolveBaseURL returns the request target for an instance name and an
// optional override, so the wiring layer and doctor can name the endpoint
// without building a client. An instance given as a bare name gets the
// service-now.com host; one given as a host or a full URL is validated as
// one.
//
// Every credentialed request this adapter makes goes to exactly the host
// this returns, so the validation here is what stands between a
// misconfigured instance and a leaked Authorization header: userinfo in
// the resolved URL ("acme.service-now.com@evil.com" resolves to host
// evil.com with "acme.service-now.com" read as a username), a path, a
// query or a fragment, or a scheme other than https, are all rejected
// rather than silently carried into the request target.
func ResolveBaseURL(instance, baseURL string) (string, error) {
	if b := strings.TrimRight(strings.TrimSpace(baseURL), "/"); b != "" {
		return validateBaseURL(b)
	}
	name := strings.TrimRight(strings.TrimSpace(instance), "/")
	if name == "" {
		return "", nil
	}
	if strings.Contains(name, "://") {
		return validateBaseURL(name)
	}
	if strings.Contains(name, ".") {
		return validateBaseURL("https://" + name)
	}
	if !bareInstancePattern.MatchString(name) {
		return "", fmt.Errorf("servicenow: invalid instance %q: must be a bare name (letters, digits, hyphens), a host, or a full https URL", name)
	}
	return "https://" + name + instanceSuffix, nil
}

// validateBaseURL parses raw and rejects everything about it except a bare
// https://host — no userinfo, no path beyond "/", no query, no fragment.
// It returns the URL rebuilt from its own scheme and host, so nothing
// about the input's formatting survives into the request target.
func validateBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("servicenow: invalid base URL %q: %w", raw, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("servicenow: base URL %q has no host", raw)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("servicenow: base URL %q must use https", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("servicenow: base URL %q must not carry a username or password", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("servicenow: base URL %q must not carry a path", raw)
	}
	if u.RawQuery != "" {
		return "", fmt.Errorf("servicenow: base URL %q must not carry a query", raw)
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("servicenow: base URL %q must not carry a fragment", raw)
	}
	return "https://" + u.Host, nil
}

// New validates cfg and returns a Client. hc may be nil, in which case a
// client with a 30 s timeout is used.
func New(cfg Config, hc *http.Client) (*Client, error) {
	basic := cfg.Username != "" || cfg.Password != ""
	bearer := cfg.OAuthToken != ""
	switch {
	case basic && bearer:
		return nil, &source.Error{Code: source.Auth, Message: "servicenow: configure exactly one of username/password or oauthToken, not both"}
	case basic && (cfg.Username == "" || cfg.Password == ""):
		return nil, &source.Error{Code: source.Auth, Message: "servicenow: both username and password are required for basic auth"}
	case !basic && !bearer:
		return nil, &source.Error{Code: source.Auth, Message: "servicenow: no credentials configured: set username and password, or oauthToken"}
	}

	base, rerr := ResolveBaseURL(cfg.Instance, cfg.BaseURL)
	if rerr != nil {
		return nil, &source.Error{Code: source.Internal, Message: rerr.Error()}
	}
	if base == "" {
		return nil, &source.Error{Code: source.Internal, Message: "servicenow: instance is required"}
	}
	trust, terr := httpx.NewTrust(base)
	if terr != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("servicenow: invalid instance %q", firstNonEmpty(cfg.BaseURL, cfg.Instance))}
	}

	dateFormat := strings.ToLower(strings.TrimSpace(cfg.DateFormat))
	switch dateFormat {
	case "", DateFormatMDY, DateFormatDMY:
	default:
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("servicenow: dateFormat must be %q or %q, got %q", DateFormatMDY, DateFormatDMY, cfg.DateFormat)}
	}

	client := hc
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	c := &Client{
		baseURL: base,
		table:   tableOrDefault(cfg.Table),
		trust:   trust,
		// Every request this adapter makes goes through this one client,
		// so a response that tries to redirect any of them — an attachment
		// download as much as a table read carrying the live Authorization
		// header — off the instance host is refused uniformly.
		hc:         httpx.Client(client, trust, maxRedirects),
		dateFormat: dateFormat,
	}
	if bearer {
		c.authHeader = "Bearer " + cfg.OAuthToken
		c.authWho = "the OAuth token"
	} else {
		c.authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Username+":"+cfg.Password))
		c.authWho = cfg.Username
	}
	return c, nil
}

func tableOrDefault(t string) string {
	if t = strings.TrimSpace(t); t != "" {
		return t
	}
	return DefaultTable
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// Table reports which table this client reads, for a caller building a
// message about it.
func (c *Client) Table() string { return c.table }

// --- tracker view ---

// Tracker returns the source.Tracker view of the same client, for a
// workspace that works its incidents in ServiceNow rather than in a
// separate issue tracker. Client itself cannot satisfy both interfaces:
// their Get methods collide by name with different signatures.
func (c *Client) Tracker() source.Tracker { return trackerView{c} }

type trackerView struct{ *Client }

func (t trackerView) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	return t.Client.getTracker(ctx, key)
}

func (t trackerView) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	return t.Client.list(ctx, f)
}

// --- HTTP ---

// Ping checks the credential with the cheapest authenticated call the
// Table API has: one record's sys_id from the configured table. A table
// with no records at all still answers 200 with an empty result, which is
// the proof the credential was accepted.
func (c *Client) Ping(ctx context.Context) error {
	q := url.Values{}
	q.Set("sysparm_limit", "1")
	q.Set("sysparm_fields", "sys_id")
	var out tableResponse
	return c.get(ctx, "/api/now/table/"+url.PathEscape(c.table), q, &out, nil)
}

// tableResponse is the Table API's envelope: everything comes back under
// "result".
type tableResponse struct {
	Result []record `json:"result"`
}

// singleResponse is the same envelope for the single-record form, where
// "result" is an object rather than an array.
type singleResponse struct {
	Result record `json:"result"`
}

// retryBudget bounds how long one paginated sweep — List, journalEntries
// or attachmentRefs — spends waiting out 429s in total, across every page.
// Each individual wait is already capped at maxRetryAfter by doRaw, but
// that is a per-page cap: without a sweep-wide budget too, an instance
// that 429s every page could still hold a 20-page sweep for pages times
// maxRetryAfter — ten minutes, for the page cap this adapter uses.
// nil means no sweep budget, which is doRaw's original behaviour: Ping and
// a single record lookup are not sweeps and keep it.
type retryBudget struct{ remaining time.Duration }

// newRetryBudget starts a budget with d to spend across an entire sweep.
func newRetryBudget(d time.Duration) *retryBudget { return &retryBudget{remaining: d} }

// take reports whether d may be spent from the budget, deducting it when
// it may. A nil budget always allows it, preserving doRaw's behaviour for
// a caller that passed none.
func (b *retryBudget) take(d time.Duration) bool {
	if b == nil {
		return true
	}
	if d > b.remaining {
		return false
	}
	b.remaining -= d
	return true
}

// get issues one authenticated GET and decodes a 2xx JSON body into out.
// budget may be nil for a call that is not part of a paginated sweep.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any, budget *retryBudget) error {
	raw, err := c.doRaw(ctx, path, query, budget)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("servicenow: GET %s: decode: %v", path, err)}
	}
	return nil
}

// doRaw issues one authenticated GET against path, retrying exactly once
// when the instance answers 429 with a Retry-After short enough to wait
// out — and, when budget is non-nil, short enough that the sweep has not
// already spent its total allowance on earlier pages.
func (c *Client) doRaw(ctx context.Context, path string, query url.Values, budget *retryBudget) ([]byte, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("servicenow: GET %s: %v", path, err)}
		}
		c.setHeaders(req)
		req.Header.Set("Accept", "application/json")

		resp, err := c.hc.Do(req)
		if err != nil {
			if host, ok := httpx.RedirectHost(err); ok {
				return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("servicenow: GET %s: redirect to untrusted host %s", path, host)}
			}
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("servicenow: GET %s: %v", path, err)}
		}
		body, readErr := httpx.ReadLimited(resp.Body, maxJSONBody)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if d, ok := httpx.RetryAfter(resp.Header, maxRetryAfter); ok && budget.take(d) {
				if err := httpx.SleepCtx(ctx, d); err != nil {
					return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("servicenow: GET %s: %v", path, err)}
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, statusError(path, resp.StatusCode, body)
		}
		if readErr != nil {
			return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("servicenow: GET %s: read body: %v", path, readErr)}
		}
		return body, nil
	}
}

// setHeaders applies the credential. It is the one place the Authorization
// header is set, so the host check that decides whether a request may be
// made at all is never bypassed by a second code path.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", c.authHeader)
}

// statusError maps a non-2xx response to a *source.Error. Only the
// Internal case carries any of the response body, capped at 200 bytes;
// auth failures report nothing but the status, so no credential can ride
// out in a message.
func statusError(path string, status int, body []byte) *source.Error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("servicenow: GET %s: %d", path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("servicenow: GET %s: %d", path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("servicenow: GET %s: %d", path, status)}
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("servicenow: GET %s: %d: %s", path, status, snippet)}
	}
}

// --- record lookup ---

// recordQuery is the field set and rendering every record request asks
// for. Naming the fields keeps the payload small and the wire shape
// stable, and sysparm_display_value=true is what turns a reference field
// into the name a human reads: caller_id arrives as "Abel Tuter" rather
// than as the sys_id it stores.
//
// The dot-walked caller_id.user_name is what lets a journal entry's
// sys_created_by — a login name — be recognised as the caller's own words.
// An instance that will not serve it simply omits the key, and the role
// falls back to matching the display name.
var recordFields = []string{
	"sys_id", "number", "short_description", "description",
	"state", "priority", "urgency", "impact", "category", "subcategory",
	"contact_type", "assignment_group", "assigned_to", "opened_by",
	"caller_id", "caller_id.user_name", "caller_id.email",
	"company", "company.sys_id",
	"sys_created_on", "sys_updated_on", "opened_at", "closed_at",
	"close_notes", "resolved_at", "active",
	// sys_class_name is the record's own class, which is what the tracker
	// type is read from on a table that has extensions; parent is the
	// record it hangs off.
	"sys_class_name", "parent",
}

func recordParams() url.Values {
	q := url.Values{}
	q.Set("sysparm_display_value", "true")
	q.Set("sysparm_exclude_reference_link", "true")
	q.Set("sysparm_fields", strings.Join(recordFields, ","))
	return q
}

// sysIDPattern reports whether an id is a sys_id — 32 lowercase-or-upper
// hex characters — rather than a record number like INC0010023. The two
// are looked up differently: a sys_id addresses the record directly, a
// number has to be queried for.
func isSysID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// fetchRecord resolves an id — a sys_id or a record number, whichever the
// operator has to hand — to the record itself, caching the result for the
// client's lifetime: Get, Threads and Attachments are called one after
// another for the same ticket, and without this each one would re-fetch
// the identical record from the Table API.
func (c *Client) fetchRecord(ctx context.Context, id string) (record, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, &source.Error{Code: source.NotFound, Message: "servicenow: empty record id"}
	}
	if rec, ok := c.records.get(id); ok {
		return rec, nil
	}
	rec, err := c.fetchRecordUncached(ctx, id)
	if err != nil {
		return nil, err
	}
	c.records.put(id, rec)
	return rec, nil
}

func (c *Client) fetchRecordUncached(ctx context.Context, id string) (record, error) {
	if isSysID(id) {
		var resp singleResponse
		if err := c.get(ctx, "/api/now/table/"+url.PathEscape(c.table)+"/"+url.PathEscape(id), recordParams(), &resp, nil); err != nil {
			return nil, err
		}
		if len(resp.Result) == 0 {
			return nil, &source.Error{Code: source.NotFound, Message: fmt.Sprintf("servicenow: %s %s: no record", c.table, id)}
		}
		return resp.Result, nil
	}

	// A record number is looked up by an exact-match encoded query rather
	// than fetched by path, so it cannot be allowed to carry the query's
	// own control characters: silently stripping "^" or "," the way a list
	// filter's value is stripped would risk matching a record other than
	// the one the caller asked for, under the id they typed. Rejecting is
	// the only answer that cannot return the wrong ticket's data.
	if i := strings.IndexAny(id, "^,"); i >= 0 {
		return nil, &source.Error{Code: source.NotFound, Message: fmt.Sprintf("servicenow: record id contains the invalid character %q", id[i])}
	}

	q := recordParams()
	q.Set("sysparm_query", "number="+id)
	q.Set("sysparm_limit", "1")
	var resp tableResponse
	if err := c.get(ctx, "/api/now/table/"+url.PathEscape(c.table), q, &resp, nil); err != nil {
		return nil, err
	}
	if len(resp.Result) == 0 {
		return nil, &source.Error{Code: source.NotFound, Message: fmt.Sprintf("servicenow: %s %s: no record", c.table, id)}
	}
	return resp.Result[0], nil
}

// encodedValue makes a caller-supplied value safe to put inside an encoded
// query. ServiceNow's encoded-query syntax has no escape sequence, so a
// value carrying the "^" separator would end the condition and start
// another one of the caller's choosing; the separator and the "," that
// splits an IN list are dropped rather than passed through.
func encodedValue(v string) string {
	return strings.NewReplacer("^", "", ",", "").Replace(strings.TrimSpace(v))
}

// recordURL is the page an operator opens to read the record in the
// ServiceNow UI. The record's own API link is not it.
func (c *Client) recordURL(sysID string) string {
	if sysID == "" {
		return ""
	}
	return c.baseURL + "/nav_to.do?uri=" + url.QueryEscape(c.table+".do?sys_id="+sysID)
}

// --- warnings ---

// addWarnings files the problems one call skipped over under the id it was
// called with, alongside whatever an earlier call for the same id
// recorded: a bundle is assembled from Get, Threads and Attachments, and
// the caller reads WarningsFor once at the end.
func (c *Client) addWarnings(id string, warnings []string) {
	c.warnings.Add(id, warnings...)
}

// WarningsFor implements source.Warner: it returns and consumes every
// problem recorded across the calls made for id so far — a journal feed
// that stopped at the page cap, an attachment pointed at somebody else's
// host — so a partial bundle never reaches the agent looking complete.
func (c *Client) WarningsFor(id string) []string {
	return c.warnings.Take(id)
}
