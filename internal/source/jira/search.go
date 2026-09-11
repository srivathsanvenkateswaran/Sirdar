package jira

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

const (
	// maxPageSize is the largest page either search endpoint is asked for.
	maxPageSize = 100
	// defaultListResults is what List returns when the caller asked for no
	// limit at all: a triage run reads a working set, not a backlog export.
	defaultListResults = 100
	// maxListResults caps how many issues one List returns, including when
	// an explicit limit asked for more.
	maxListResults = 200
	// maxSearchPages stops Cloud's cursor pagination from running forever.
	// The nextPageToken cursor has a documented failure mode where a token
	// hands back the first page again, so a repeated token and a page count
	// are both treated as the end of the results.
	maxSearchPages = 20
)

// List implements source.Tracker, translating the filter into JQL and
// paginating whichever search endpoint this deployment serves.
func (c *Client) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	// List is not about one ticket, so its warnings file under the empty
	// key: WarningsFor("") is how a caller reads them back.
	ctx, col := withCollector(ctx)
	defer c.publish("", col)

	limit, capped := httpx.Limit(f.Limit, defaultListResults, maxListResults)
	if capped {
		warnCtx(ctx, "jira: list limit %d capped at %d", f.Limit, maxListResults)
	}

	jql := c.buildJQL(f)
	fields := c.issueFieldList(ctx)

	var (
		issues []jiraIssue
		err    error
	)
	if c.deploymentFor(ctx) == DeploymentCloud {
		issues, err = c.searchCloud(ctx, jql, fields, limit)
	} else {
		issues, err = c.searchDataCenter(ctx, jql, fields, limit)
	}
	if err != nil {
		return nil, err
	}

	out := make([]ticket.TrackerTicket, 0, len(issues))
	for i := range issues {
		out = append(out, c.mapTracker(&issues[i]))
	}
	return out, nil
}

// buildJQL turns a ListFilter into a JQL query. An unset Status means the
// open-ish default, expressed as statusCategory != Done so it holds whatever
// the instance called its workflow steps.
func (c *Client) buildJQL(f source.ListFilter) string {
	var clauses []string
	if pk := strings.TrimSpace(c.cfg.ProjectKey); pk != "" {
		clauses = append(clauses, "project = "+quoteJQL(pk))
	}
	switch a := strings.TrimSpace(f.Assignee); {
	case a == "":
	case strings.EqualFold(a, "me"):
		clauses = append(clauses, "assignee = currentUser()")
	default:
		clauses = append(clauses, "assignee = "+quoteJQL(a))
	}
	if s := strings.TrimSpace(f.Status); s != "" {
		clauses = append(clauses, "status = "+quoteJQL(s))
	} else {
		clauses = append(clauses, "statusCategory != Done")
	}
	if p := strings.TrimSpace(f.Parent); p != "" {
		clauses = append(clauses, "parent = "+quoteJQL(p))
	}

	const order = "ORDER BY updated DESC"
	if len(clauses) == 0 {
		return order
	}
	return strings.Join(clauses, " AND ") + " " + order
}

// quoteJQL renders a value as a JQL string literal, escaping the backslash
// and double quote that would otherwise end it early.
func quoteJQL(v string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		if r == '\\' || r == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// pageSize is the number of issues to ask for next: never more than the
// endpoint's comfortable page, never more than the caller still wants.
func pageSize(limit, have int) int { return httpx.PageSize(limit-have, maxPageSize) }

// --- Cloud ---

type cloudSearchRequest struct {
	JQL           string   `json:"jql"`
	Fields        []string `json:"fields"`
	MaxResults    int      `json:"maxResults"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
}

type cloudSearchResponse struct {
	Issues        []jiraIssue `json:"issues"`
	NextPageToken string      `json:"nextPageToken"`
	IsLast        *bool       `json:"isLast"`
}

// searchCloud pages POST /rest/api/3/search/jql, the endpoint that replaced
// the classic /search Atlassian removed from Cloud in August 2025. It is a
// cursor API: no total, just a nextPageToken to hand back.
func (c *Client) searchCloud(ctx context.Context, jql string, fields []string, limit int) ([]jiraIssue, error) {
	var all []jiraIssue
	seen := map[string]bool{}
	token := ""

	for page := 0; page < maxSearchPages && len(all) < limit; page++ {
		body := cloudSearchRequest{
			JQL:           jql,
			Fields:        fields,
			MaxResults:    pageSize(limit, len(all)),
			NextPageToken: token,
		}
		var resp cloudSearchResponse
		if err := c.doJSON(ctx, http.MethodPost, "/rest/api/3/search/jql", nil, body, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Issues...)

		if resp.NextPageToken == "" || len(resp.Issues) == 0 {
			break
		}
		if resp.IsLast != nil && *resp.IsLast {
			break
		}
		if seen[resp.NextPageToken] {
			warnCtx(ctx, "jira: search returned a repeated nextPageToken after %d issues; stopping pagination", len(all))
			break
		}
		seen[resp.NextPageToken] = true
		token = resp.NextPageToken
	}

	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// --- Data Center ---

type dcSearchResponse struct {
	Issues     []jiraIssue `json:"issues"`
	StartAt    int         `json:"startAt"`
	MaxResults int         `json:"maxResults"`
	Total      int         `json:"total"`
}

// searchDataCenter pages GET /rest/api/2/search, which Data Center still
// serves with classic startAt/total offsets.
func (c *Client) searchDataCenter(ctx context.Context, jql string, fields []string, limit int) ([]jiraIssue, error) {
	var all []jiraIssue
	startAt := 0

	for page := 0; page < maxSearchPages && len(all) < limit; page++ {
		q := url.Values{}
		q.Set("jql", jql)
		q.Set("startAt", strconv.Itoa(startAt))
		q.Set("maxResults", strconv.Itoa(pageSize(limit, len(all))))
		q.Set("fields", strings.Join(fields, ","))

		var resp dcSearchResponse
		if err := c.doJSON(ctx, http.MethodGet, "/rest/api/2/search", q, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Issues...)

		if len(resp.Issues) == 0 {
			break
		}
		startAt += len(resp.Issues)
		if resp.Total > 0 && startAt >= resp.Total {
			break
		}
	}

	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
