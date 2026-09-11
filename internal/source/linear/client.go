// Package linear implements source.Tracker against Linear's GraphQL API, plus
// a source.Helpdesk view over the same issue for workspaces where the Linear
// issue itself carries the conversation Sirdar reads. Bodies are Markdown on
// both sides of Linear's API, so nothing here converts HTML.
package linear

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// DefaultEndpoint is Linear's single GraphQL endpoint.
const DefaultEndpoint = "https://api.linear.app/graphql"

// Page sizes for the two paginated connections. Linear's own default is 50;
// its hard cap on a single query's complexity makes larger pages a false
// economy, so a caller asking for more gets more pages, not a bigger one.
const (
	defaultPageSize  = 50
	commentsPageSize = 100
)

// Bounds on List's result count: a caller that names no limit gets
// defaultLimit issues, and no caller gets more than maxLimit.
const (
	defaultLimit = 100
	maxLimit     = 200
)

// Config is the adapter's configuration. Secrets arrive already resolved by
// the wiring layer, so APIKey is the key itself, never an env:/keychain: ref.
type Config struct {
	// APIKey is a Linear personal API key. Required.
	APIKey string
	// TeamKey optionally scopes List to one team (e.g. "ENG").
	TeamKey string
}

// Client talks to Linear's GraphQL API. It implements source.Tracker;
// Helpdesk() returns the source.Helpdesk view over the same issue.
type Client struct {
	// Endpoint is the GraphQL URL; New sets it to DefaultEndpoint.
	Endpoint string
	APIKey   string
	TeamKey  string
	HTTP     *http.Client

	// warnings holds the non-fatal problems a call recorded — an
	// attachment that would not download, a field this workspace does not
	// expose — keyed by the ticket id the call was made for, so one
	// ticket's missing evidence is never reported against another's. One
	// Client serves every ticket in a run, so two of them can be inside
	// Attachments at the same time. An entry is removed when it is read.
	warnings httpx.Warnings
}

// New returns a Client for cfg. hc may be nil, in which case a client with a
// 30 second timeout is used.
func New(cfg Config, hc *http.Client) (*Client, error) {
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		return nil, &source.Error{Code: source.Auth, Message: "linear: apiKey is required"}
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		Endpoint: DefaultEndpoint,
		APIKey:   key,
		TeamKey:  strings.TrimSpace(cfg.TeamKey),
		HTTP:     hc,
	}, nil
}

var (
	_ source.Tracker  = (*Client)(nil)
	_ source.Helpdesk = helpdeskView{}
	_ source.Warner   = (*Client)(nil)
)

// --- Warnings ---

// addWarnings appends to ticket id's warnings without disturbing what is
// already there, skipping a line that has already been recorded — a query
// that degrades the same way on every page should say so once.
//
// Appending is what a bundle needs rather than a nicety: internal/run's
// fetchBundle runs Get, then Threads, then Attachments, and reads
// WarningsFor once at the end, so a warning one of those calls records has
// to survive the next two.
func (c *Client) addWarnings(id string, warnings ...string) {
	c.warnings.Add(id, warnings...)
}

// WarningsFor implements source.Warner: it returns and consumes the problems
// recorded for ticket id, so the caller can put them in the prompt and the
// run state instead of silently serving a short list of attachments. It is
// keyed by id, which is what a caller running several tickets at once needs:
// it cannot be handed another ticket's missing evidence.
func (c *Client) WarningsFor(id string) []string { return c.warnings.Take(id) }

// Ping checks the credential by asking Linear who it belongs to. It backs the
// doctor command, so it makes the cheapest authenticated call there is.
func (c *Client) Ping(ctx context.Context) error {
	var resp struct {
		Viewer *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"viewer"`
	}
	if err := c.query(ctx, pingQuery, nil, &resp); err != nil {
		return err
	}
	if resp.Viewer == nil || resp.Viewer.ID == "" {
		return &source.Error{Code: source.Auth, Message: "linear: viewer query returned no user"}
	}
	return nil
}

// --- Tracker ---

// Get fetches one issue. key is the human identifier ("ENG-123"); Linear's
// issue(id:) accepts it directly, so no lookup is needed to resolve it.
func (c *Client) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	iss, err := c.fetchIssue(ctx, key)
	if err != nil {
		return ticket.TrackerTicket{}, err
	}
	return mapIssue(iss), nil
}

// fetchIssue runs the issue query, retrying once without customerNeeds if the
// workspace does not expose that field.
func (c *Client) fetchIssue(ctx context.Context, key string) (*linearIssue, error) {
	var resp struct {
		Issue *linearIssue `json:"issue"`
	}
	err := c.query(ctx, issueQuery(true), map[string]any{"id": key}, &resp)
	if fieldValidationError(err) {
		c.addWarnings(key, noCustomerNeedsWarning)
		resp.Issue = nil
		err = c.query(ctx, issueQuery(false), map[string]any{"id": key}, &resp)
	}
	if err != nil {
		return nil, err
	}
	if resp.Issue == nil {
		return nil, &source.Error{Code: source.NotFound, Message: "linear: issue " + key + " not found"}
	}
	return resp.Issue, nil
}

// List returns the issues matching f, paging through Linear's cursor
// connection until the limit is reached or the results run out. With no
// Status filter it excludes completed and cancelled work, which is the
// open-ish default the adapter contract asks for.
//
// Limit is bounded: 0 (unset) means defaultLimit, and anything above
// maxLimit is capped there. An unbounded walk of a broad filter would spend
// a caller's hourly request budget on results nobody asked to read.
func (c *Client) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	filter, err := c.buildFilter(ctx, f)
	if err != nil {
		return nil, err
	}

	limit, _ := httpx.Limit(f.Limit, defaultLimit, maxLimit)
	pageSize := httpx.PageSize(limit, defaultPageSize)

	var out []ticket.TrackerTicket
	degraded := false
	withNeeds := true
	after := ""
	for {
		vars := map[string]any{"first": pageSize}
		if len(filter) > 0 {
			vars["filter"] = filter
		}
		if after != "" {
			vars["after"] = after
		}

		var resp struct {
			Issues struct {
				Nodes    []linearIssue `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"issues"`
		}
		err := c.query(ctx, issuesQuery(withNeeds), vars, &resp)
		if withNeeds && fieldValidationError(err) {
			withNeeds = false
			degraded = true
			continue
		}
		if err != nil {
			return nil, err
		}

		for i := range resp.Issues.Nodes {
			t := mapIssue(&resp.Issues.Nodes[i])
			if degraded {
				// List has no single ticket id of its own, so a
				// query-wide degradation is recorded against every
				// ticket it hands back.
				c.addWarnings(t.Key, noCustomerNeedsWarning)
			}
			out = append(out, t)
			if len(out) >= limit {
				return out, nil
			}
		}
		if !resp.Issues.PageInfo.HasNextPage || resp.Issues.PageInfo.EndCursor == "" {
			return out, nil
		}
		after = resp.Issues.PageInfo.EndCursor
	}
}

// noCustomerNeedsWarning is recorded when a workspace does not expose the
// Customers feature's fields, so a reader of the ticket knows the customer
// request count is missing rather than zero.
const noCustomerNeedsWarning = "linear: customerNeeds is not available on this workspace; customer request counts are missing"

// noCommentActorsWarning is recorded when a workspace does not expose a
// comment's non-user authors, so a reader knows an unattributed comment is a
// gap in the data rather than an anonymous one.
const noCommentActorsWarning = "linear: comment externalUser/botActor are not available on this workspace; some comment authors and the system role are missing"

// buildFilter turns Sirdar's ListFilter into Linear's IssueFilter input.
func (c *Client) buildFilter(ctx context.Context, f source.ListFilter) (map[string]any, error) {
	filter := map[string]any{}

	if a := strings.TrimSpace(f.Assignee); a != "" {
		switch {
		case strings.EqualFold(a, "me"):
			// isMe resolves against the token's own user, so "me" needs no
			// extra round trip to find out who that is.
			filter["assignee"] = map[string]any{"isMe": map[string]any{"eq": true}}
		case strings.Contains(a, "@"):
			filter["assignee"] = map[string]any{"email": map[string]any{"eq": a}}
		default:
			filter["assignee"] = map[string]any{"name": map[string]any{"eq": a}}
		}
	}

	if s := strings.TrimSpace(f.Status); s != "" {
		// Workflow state names are per-workspace, so an exact name match is
		// the least surprising reading of a status the operator typed.
		filter["state"] = map[string]any{"name": map[string]any{"eq": s}}
	} else {
		filter["state"] = map[string]any{"type": map[string]any{"nin": []string{"completed", "canceled"}}}
	}

	if p := strings.TrimSpace(f.Parent); p != "" {
		id, err := c.resolveIssueID(ctx, p)
		if err != nil {
			return nil, err
		}
		filter["parent"] = map[string]any{"id": map[string]any{"eq": id}}
	}

	if c.TeamKey != "" {
		filter["team"] = map[string]any{"key": map[string]any{"eq": c.TeamKey}}
	}
	return filter, nil
}

// resolveIssueID turns a human identifier into the UUID that IssueFilter
// compares parents against. A value that is already a UUID is passed through.
func (c *Client) resolveIssueID(ctx context.Context, key string) (string, error) {
	if isUUID(key) {
		return key, nil
	}
	var resp struct {
		Issue *linearIssue `json:"issue"`
	}
	if err := c.query(ctx, issueIDQuery, map[string]any{"id": key}, &resp); err != nil {
		return "", err
	}
	if resp.Issue == nil || resp.Issue.ID == "" {
		return "", &source.Error{Code: source.NotFound, Message: "linear: parent issue " + key + " not found"}
	}
	return resp.Issue.ID, nil
}

// --- Helpdesk view ---

// Helpdesk returns a view of the client satisfying source.Helpdesk. Client
// itself satisfies source.Tracker; the two interfaces cannot both be
// satisfied by one type because their Get methods collide by name.
func (c *Client) Helpdesk() source.Helpdesk { return helpdeskView{c} }

type helpdeskView struct{ *Client }

func (h helpdeskView) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	return h.Client.getHelpdesk(ctx, id)
}

// getHelpdesk backs the Helpdesk() view's Get rather than being exported as
// Get on Client, which already carries the tracker's Get.
func (c *Client) getHelpdesk(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	iss, err := c.fetchIssue(ctx, id)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}
	return mapHelpdeskTicket(iss), nil
}

// conversation is an issue's description, attachment list and every comment,
// which is what both Threads and Attachments work from.
type conversation struct {
	Identifier  string
	Description string
	Attachments []linearAttachment
	Comments    []linearComment
}

// fetchConversation reads an issue's comments, following the cursor until the
// connection is exhausted.
func (c *Client) fetchConversation(ctx context.Context, id string) (*conversation, error) {
	conv := &conversation{}
	withActors := true
	after := ""
	for {
		vars := map[string]any{"id": id, "first": commentsPageSize}
		if after != "" {
			vars["after"] = after
		}

		var resp struct {
			Issue *struct {
				Identifier  string         `json:"identifier"`
				Description string         `json:"description"`
				Attachments attachmentConn `json:"attachments"`
				Comments    struct {
					Nodes    []linearComment `json:"nodes"`
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"comments"`
			} `json:"issue"`
		}
		err := c.query(ctx, conversationQuery(withActors), vars, &resp)
		if withActors && fieldValidationError(err) {
			// This workspace does not expose the non-user comment authors.
			// Ask again without them: a thread with plainer authorship is
			// worth far more to the caller than no thread at all.
			withActors = false
			c.addWarnings(id, noCommentActorsWarning)
			continue
		}
		if err != nil {
			return nil, err
		}
		if resp.Issue == nil {
			return nil, &source.Error{Code: source.NotFound, Message: "linear: issue " + id + " not found"}
		}
		if after == "" {
			conv.Identifier = resp.Issue.Identifier
			conv.Description = resp.Issue.Description
			conv.Attachments = resp.Issue.Attachments.Nodes
		}
		conv.Comments = append(conv.Comments, resp.Issue.Comments.Nodes...)

		if !resp.Issue.Comments.PageInfo.HasNextPage || resp.Issue.Comments.PageInfo.EndCursor == "" {
			return conv, nil
		}
		after = resp.Issue.Comments.PageInfo.EndCursor
	}
}

// Threads returns the issue's comments as an ordered conversation.
func (h helpdeskView) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	conv, err := h.Client.fetchConversation(ctx, id)
	if err != nil {
		return nil, err
	}
	return buildThread(conv.Comments), nil
}

// Attachments downloads every Linear-hosted file the issue carries — the
// entries in issue.attachments that point at Linear's own upload storage,
// plus the images embedded in the description and in each comment's Markdown
// — into dir, named "<1-based index>-<sanitised name>".
//
// Attachments pointing at another system (a GitHub PR, a Zendesk ticket) are
// links, not files, and need that system's credential; they are skipped here
// and reach the agent through HelpdeskRef and the tracker record instead.
//
// A download failure for one file does not fail the call: it is skipped and
// reported through WarningsFor(id). Only when every file fails does
// Attachments return an error — and then the failures are not also recorded
// as warnings, since the caller already has every one of them in the error.
func (h helpdeskView) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	c := h.Client
	// Whatever Get and Threads recorded for this ticket stays where it is:
	// they are earlier calls in the same bundle, not stale state, and the
	// caller reads WarningsFor once after all three. Clearing here used to
	// mean a comment field Threads could not read went unreported whenever
	// the attachments downloaded cleanly.
	conv, err := c.fetchConversation(ctx, id)
	if err != nil {
		return nil, err
	}

	refs := uploadAttachments(conv.Attachments)
	refs = append(refs, collectUploads(conv.Description)...)
	for _, cm := range conv.Comments {
		refs = append(refs, collectUploads(cm.Body)...)
	}
	refs = dedupeUploads(refs)
	if len(refs) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, internalf("linear: mkdir %s: %v", dir, err)
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	var failures []string
	for i, r := range refs {
		name := httpx.SanitizeName(r.Name)
		filename := indexedName(i+1, name)

		mime, derr := c.download(ctx, r.URL, filepath.Join(dir, filename))
		if derr != nil {
			failures = append(failures, fmt.Sprintf("linear: download attachment %s: %v", r.ID, derr))
			continue
		}
		out = append(out, ticket.Attachment{ID: r.ID, Name: name, MIME: mime, Path: base + "/" + filename})
	}

	if len(out) == 0 {
		// Every file failed: the caller gets all of them in the error, so
		// repeating them as warnings would put the same line in front of
		// the agent twice.
		errs := make([]error, len(failures))
		for i, f := range failures {
			errs[i] = errors.New(f)
		}
		return out, errors.Join(errs...)
	}
	c.addWarnings(id, failures...)
	return out, nil
}

// maxRedirects bounds how far a redirect chain that stays on the upload
// host is followed before the download is abandoned.
const maxRedirects = 3

// maxAttachmentBytes caps one download. Past it the file is refused rather
// than written: an attachment nobody can vouch for should not be able to
// fill the disk the run is using.
const maxAttachmentBytes = 64 << 20

// download fetches a Linear upload with the same Authorization header the
// GraphQL API uses — Linear's file storage is private and accepts the API key
// directly — and writes it to destPath, returning the response Content-Type.
func (c *Client) download(ctx context.Context, rawURL, destPath string) (string, error) {
	// Belt and braces: every ref that reaches here was already filtered to
	// the upload host, and this makes it impossible for a future caller to
	// reach anywhere else with the API key attached.
	if !isUploadURL(rawURL) {
		return "", fmt.Errorf("refusing to send the API key to an untrusted URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", c.APIKey)

	// The redirect policy applies the same trust check the starting URL
	// got. A Location header arrives inside a server response, so it is
	// input: a redirect off uploads.linear.app would otherwise be followed
	// and whatever the other host served written to disk under the
	// attachment's name. Go strips the Authorization header on a cross-host
	// hop, but stripping the credential is not the same as refusing the
	// request.
	ct, err := httpx.Download(ctx, httpx.Client(c.HTTP, uploadTrust, maxRedirects), req, destPath, httpx.DownloadOptions{Max: maxAttachmentBytes})
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return "", fmt.Errorf("status %d", se.Status)
	}
	if err != nil {
		return "", err
	}
	return ct, nil
}
