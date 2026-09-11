package azdo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// closedStates are the states a work item is no longer worked in. Process
// templates differ (Agile, Scrum, CMMI, custom), so the default filter
// excludes the closed-ish names all of them use rather than naming the open
// ones.
var closedStates = []string{"Closed", "Done", "Removed", "Resolved"}

// List bounds. A caller that asks for no limit gets defaultListLimit, and
// nobody gets more than maxListLimit: an unbounded WIQL query would size
// $top, and the merged work item list, off whatever a broad filter happens
// to match.
const (
	defaultListLimit = 100
	maxListLimit     = 200
)

// batchFields is the fixed field list requested from workitemsbatch. The
// batch endpoint returns fields only — no relations, no _links — so it asks
// for exactly what mapTracker reads.
func (c *Client) batchFields() []string {
	f := []string{
		fieldTitle,
		fieldDescription,
		fieldReproSteps,
		fieldState,
		fieldReason,
		fieldWorkItemType,
		fieldPriority,
		fieldSeverity,
		fieldAssignedTo,
		fieldTags,
		fieldParent,
		fieldAreaPath,
		fieldIteration,
		fieldCreatedDate,
		fieldChangedDate,
	}
	if c.cfg.HelpdeskField != "" {
		f = append(f, c.cfg.HelpdeskField)
	}
	return f
}

type wiqlRequest struct {
	Query string `json:"query"`
}

type wiqlRef struct {
	ID int `json:"id"`
}

type wiqlResponse struct {
	QueryType string    `json:"queryType"`
	WorkItems []wiqlRef `json:"workItems"`
	// WorkItemRelations is what a tree/oneHop query returns instead of a
	// flat list; it is read defensively so an org whose process rejects the
	// flat parent filter still yields ids.
	WorkItemRelations []struct {
		Target *wiqlRef `json:"target"`
	} `json:"workItemRelations"`
}

type batchRequest struct {
	IDs         []int    `json:"ids"`
	Fields      []string `json:"fields"`
	ErrorPolicy string   `json:"errorPolicy"`
}

type batchResponse struct {
	Count int        `json:"count"`
	Value []workItem `json:"value"`
}

// wiqlQuote renders a WIQL string literal, doubling embedded quotes.
func wiqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// buildWIQL renders the flat query for a list filter. The project clause is
// always present so the query cannot escape the configured project even
// though the endpoint is already project-scoped.
func buildWIQL(f source.ListFilter, project string) (string, error) {
	where := []string{"[System.TeamProject] = @project"}

	if s := strings.TrimSpace(f.Status); s != "" {
		where = append(where, "[System.State] = "+wiqlQuote(s))
	} else {
		quoted := make([]string, len(closedStates))
		for i, s := range closedStates {
			quoted[i] = wiqlQuote(s)
		}
		where = append(where, "[System.State] NOT IN ("+strings.Join(quoted, ",")+")")
	}

	if a := strings.TrimSpace(f.Assignee); a != "" {
		if strings.EqualFold(a, "me") {
			where = append(where, "[System.AssignedTo] = @Me")
		} else {
			where = append(where, "[System.AssignedTo] = "+wiqlQuote(a))
		}
	}

	if p := strings.TrimSpace(f.Parent); p != "" {
		id, err := normalizeKey(p)
		if err != nil {
			return "", &source.Error{Code: source.Unsupported, Message: fmt.Sprintf("azure devops: parent filter %q is not a work item id", f.Parent)}
		}
		where = append(where, "[System.Parent] = "+id)
	}

	return "SELECT [System.Id] FROM WorkItems WHERE " + strings.Join(where, " AND ") +
		" ORDER BY [System.ChangedDate] DESC", nil
}

// List runs a WIQL query for the filter and hydrates the ids it returns.
// WIQL never returns field values, so this is always two steps: the query,
// then workitemsbatch in chunks of at most 200 ids. Limit is bounded by
// effectiveLimit: 0 (unset) means defaultListLimit, and anything above
// maxListLimit is capped there.
func (c *Client) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	query, err := buildWIQL(f, c.cfg.Project)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(wiqlRequest{Query: query})
	if err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("azure devops: encode wiql: %v", err)}
	}

	limit, _ := httpx.Limit(f.Limit, defaultListLimit, maxListLimit)
	u := c.projectURL("/_apis/wit/wiql?api-version=" + apiVersion)
	u += "&$top=" + strconv.Itoa(limit)
	var wr wiqlResponse
	if err := c.doJSON(ctx, http.MethodPost, u, body, &wr); err != nil {
		return nil, err
	}

	ids := make([]int, 0, len(wr.WorkItems))
	seen := map[int]bool{}
	add := func(id int) {
		if id == 0 || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, w := range wr.WorkItems {
		add(w.ID)
	}
	for _, r := range wr.WorkItemRelations {
		if r.Target != nil {
			add(r.Target.ID)
		}
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	if len(ids) == 0 {
		return nil, nil
	}

	size := c.batchSize
	if size <= 0 || size > batchLimit {
		size = batchLimit
	}
	byID := make(map[int]workItem, len(ids))
	for start := 0; start < len(ids); start += size {
		end := start + size
		if end > len(ids) {
			end = len(ids)
		}
		items, err := c.batch(ctx, ids[start:end])
		if err != nil {
			return nil, err
		}
		for _, wi := range items {
			byID[wi.ID] = wi
		}
	}

	// Keep the query's own ordering (ChangedDate DESC): the batch endpoint
	// makes no ordering promise, and errorPolicy Omit means a deleted or
	// unreadable id simply drops out.
	out := make([]ticket.TrackerTicket, 0, len(ids))
	for _, id := range ids {
		wi, ok := byID[id]
		if !ok {
			continue
		}
		out = append(out, c.mapTracker(wi))
	}
	return out, nil
}

// batch hydrates up to 200 ids in one call. errorPolicy Omit keeps one
// deleted or inaccessible id from failing the whole list.
func (c *Client) batch(ctx context.Context, ids []int) ([]workItem, error) {
	body, err := json.Marshal(batchRequest{IDs: ids, Fields: c.batchFields(), ErrorPolicy: "Omit"})
	if err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("azure devops: encode workitemsbatch: %v", err)}
	}
	u := c.projectURL("/_apis/wit/workitemsbatch?api-version=" + apiVersion)
	var br batchResponse
	if err := c.doJSON(ctx, http.MethodPost, u, body, &br); err != nil {
		return nil, err
	}
	return br.Value, nil
}
