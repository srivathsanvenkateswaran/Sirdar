package rally

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// artifactType pairs a Rally type name with the lowercase collection path
// WSAPI addresses it by.
type artifactType struct {
	Name string // canonical Rally type name, e.g. "HierarchicalRequirement"
	Path string // WSAPI collection path, e.g. "hierarchicalrequirement"
}

// prefixTypes maps the FormattedID prefixes Rally assigns by artifact type.
// The convention is widely used but is not part of the documented WSAPI
// schema and a subscription can be configured differently, so it is only a
// first guess: Get falls back to sweeping the configured types when the
// prefix guess misses. Two-character prefixes are listed before the
// one-character one so DE/DS are never shadowed.
var prefixTypes = []struct {
	Prefix string
	Type   artifactType
}{
	{"DE", artifactType{"Defect", "defect"}},
	{"DS", artifactType{"DefectSuite", "defectsuite"}},
	{"US", artifactType{"HierarchicalRequirement", "hierarchicalrequirement"}},
	{"TA", artifactType{"Task", "task"}},
	{"TC", artifactType{"TestCase", "testcase"}},
	{"F", artifactType{"PortfolioItem/Feature", "portfolioitem/feature"}},
}

// typePath turns a configured Rally type name into its WSAPI collection
// path. Rally's collection paths are the lowercased type name, with
// PortfolioItem subtypes addressed as "portfolioitem/<subtype>".
func typePath(name string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(name), "/"))
}

// prefixType returns the type a FormattedID's prefix suggests. The prefix
// must be followed by at least one digit, so a project-specific key like
// "FOO" is not mistaken for a Feature.
func prefixType(key string) (artifactType, bool) {
	k := strings.ToUpper(strings.TrimSpace(key))
	for _, pt := range prefixTypes {
		if !strings.HasPrefix(k, pt.Prefix) {
			continue
		}
		rest := k[len(pt.Prefix):]
		if rest == "" {
			continue
		}
		if rest[0] < '0' || rest[0] > '9' {
			continue
		}
		return pt.Type, true
	}
	return artifactType{}, false
}

// configuredTypes returns the configured artifact types in order.
func (c *Client) configuredTypes() []artifactType {
	out := make([]artifactType, 0, len(c.cfg.Types))
	for _, name := range c.cfg.Types {
		out = append(out, artifactType{Name: name, Path: typePath(name)})
	}
	return out
}

// candidateTypes is the order Get tries collections in for key: the prefix
// heuristic first, then every configured type not already covered.
func (c *Client) candidateTypes(key string) []artifactType {
	var out []artifactType
	seen := map[string]bool{}
	if t, ok := prefixType(key); ok {
		out = append(out, t)
		seen[t.Path] = true
	}
	for _, t := range c.configuredTypes() {
		if seen[t.Path] {
			continue
		}
		seen[t.Path] = true
		out = append(out, t)
	}
	return out
}

// fetchFields is the attribute list requested for an artifact. Rally
// recommends against fetch=true (fetch everything) for performance, so the
// adapter names exactly the fields it maps. HelpdeskField, when configured,
// is appended.
var fetchFields = []string{
	"FormattedID", "ObjectID", "Name", "Description", "Notes",
	"Priority", "Severity", "State", "ScheduleState",
	"Owner", "Project", "Iteration", "Release", "Tags",
	"Parent", "Feature", "CreationDate", "LastUpdateDate",
}

func (c *Client) fetchList() string {
	fields := append([]string(nil), fetchFields...)
	if f := strings.TrimSpace(c.cfg.HelpdeskField); f != "" {
		fields = append(fields, f)
	}
	return strings.Join(fields, ",")
}

// queryResult is the envelope every WSAPI collection query answers with.
// Errors arrive inside a 200 response, so they must be checked explicitly.
type queryResult struct {
	Errors           []string          `json:"Errors"`
	Warnings         []string          `json:"Warnings"`
	TotalResultCount int               `json:"TotalResultCount"`
	StartIndex       int               `json:"StartIndex"`
	PageSize         int               `json:"PageSize"`
	Results          []json.RawMessage `json:"Results"`
}

type queryEnvelope struct {
	QueryResult queryResult `json:"QueryResult"`
}

// queryOpts are the WSAPI query parameters one page request needs.
type queryOpts struct {
	Query    string
	Order    string
	Start    int // 1-based; 0 means 1
	PageSize int
	Project  bool // scope the query to Config.Project when set
}

// queryURL builds the collection query URL for a type.
func (c *Client) queryURL(t artifactType, o queryOpts) string {
	q := url.Values{}
	if o.Query != "" {
		q.Set("query", o.Query)
	}
	q.Set("fetch", c.fetchList())
	if o.Order != "" {
		q.Set("order", o.Order)
	}
	if ws := strings.TrimSpace(c.cfg.Workspace); ws != "" {
		q.Set("workspace", ws)
	}
	if o.Project {
		if p := strings.TrimSpace(c.cfg.Project); p != "" {
			q.Set("project", p)
		}
	}
	start := o.Start
	if start < 1 {
		start = 1
	}
	q.Set("start", strconv.Itoa(start))
	size := o.PageSize
	if size < 1 {
		size = maxPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	q.Set("pagesize", strconv.Itoa(size))
	return c.endpoint(t.Path) + "?" + q.Encode()
}

// runQuery fetches one page of a collection query and returns its envelope,
// mapping an in-body Errors array to a *source.Error and recording in-body
// Warnings on the client.
func (c *Client) runQuery(ctx context.Context, t artifactType, o queryOpts, w *warnBuf) (queryResult, error) {
	var env queryEnvelope
	if err := c.get(ctx, c.queryURL(t, o), &env); err != nil {
		return queryResult{}, err
	}
	if len(env.QueryResult.Errors) > 0 {
		return queryResult{}, resultError("query "+t.Path, env.QueryResult.Errors)
	}
	for _, msg := range env.QueryResult.Warnings {
		w.addf("rally: query %s: %s", t.Path, msg)
	}
	return env.QueryResult, nil
}

// found is a decoded artifact together with the collection it came from and
// its raw JSON, which is kept so a configured custom field can be read
// without the adapter knowing its name at compile time.
type found struct {
	a    artifact
	raw  map[string]json.RawMessage
	typ  artifactType
	body json.RawMessage
}

func decodeArtifact(raw json.RawMessage, t artifactType) (found, error) {
	f := found{typ: t, body: raw}
	if err := json.Unmarshal(raw, &f.a); err != nil {
		return found{}, fmt.Errorf("decode %s: %w", t.Path, err)
	}
	if err := json.Unmarshal(raw, &f.raw); err != nil {
		return found{}, fmt.Errorf("decode %s fields: %w", t.Path, err)
	}
	// The response's own _type is authoritative over the collection we
	// happened to query (they differ for PortfolioItem subtypes).
	if f.a.Type != "" {
		f.typ.Name = f.a.Type
	}
	return f, nil
}

// formattedIDQuery builds the equality filter for a human key.
func formattedIDQuery(key string) string {
	return `(FormattedID = "` + escapeQueryValue(key) + `")`
}

// escapeQueryValue strips the characters that would break out of a quoted
// WSAPI query literal. Rally documents no escape sequence for a double
// quote inside a quoted value, so they are removed rather than encoded.
func escapeQueryValue(v string) string {
	return strings.NewReplacer(`"`, "", `\`, "", "\n", " ", "\r", " ").Replace(v)
}

// find locates one artifact by FormattedID, trying the prefix-suggested
// collection first and then the configured types. The first collection that
// returns a result wins.
func (c *Client) find(ctx context.Context, key string, w *warnBuf) (found, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return found{}, &source.Error{Code: source.NotFound, Message: "rally: empty ticket key"}
	}
	q := formattedIDQuery(key)
	for _, t := range c.candidateTypes(key) {
		res, err := c.runQuery(ctx, t, queryOpts{Query: q, PageSize: 1}, w)
		if err != nil {
			return found{}, err
		}
		if len(res.Results) == 0 {
			continue
		}
		f, derr := decodeArtifact(res.Results[0], t)
		if derr != nil {
			return found{}, &source.Error{Code: source.Internal, Message: "rally: " + derr.Error()}
		}
		return f, nil
	}
	return found{}, &source.Error{Code: source.NotFound, Message: fmt.Sprintf("rally: no artifact with FormattedID %q in %s", key, strings.Join(c.cfg.Types, ", "))}
}

// Get fetches one artifact by its FormattedID (e.g. "DE1234") and maps it
// to a tracker ticket.
func (c *Client) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	var w warnBuf
	f, err := c.find(ctx, key, &w)
	c.putWarnings(key, w.msgs)
	if err != nil {
		return ticket.TrackerTicket{}, err
	}
	return c.toTracker(f), nil
}

// --- List ---

// statusField is the field carrying an artifact's workflow state: Defect
// (and DefectSuite) use State, everything else ScheduleState.
func statusField(t artifactType) string {
	switch strings.ToLower(t.Path) {
	case "defect", "defectsuite":
		return "State"
	default:
		return "ScheduleState"
	}
}

// parentField is the field linking an artifact to the parent named in a
// filter. A story hung off a portfolio Feature is linked by Feature, not
// Parent (which is the parent *story*), so a Feature-shaped parent key
// selects that field.
func parentField(t artifactType, parentKey string) string {
	if pt, ok := prefixType(parentKey); ok && strings.HasPrefix(pt.Path, "portfolioitem") {
		if strings.ToLower(t.Path) == "hierarchicalrequirement" {
			return "Feature"
		}
	}
	return "Parent"
}

// listQuery composes the filter clauses for one type, ANDed together.
// An empty Status means "open-ish": everything not Closed.
func (c *Client) listQuery(t artifactType, f source.ListFilter, userName string) string {
	var clauses []string
	if a := strings.TrimSpace(f.Assignee); a != "" {
		name := a
		if strings.EqualFold(a, "me") {
			name = userName
		}
		if name != "" {
			clauses = append(clauses, `(Owner.UserName = "`+escapeQueryValue(name)+`")`)
		}
	}
	field := statusField(t)
	if s := strings.TrimSpace(f.Status); s != "" {
		clauses = append(clauses, `(`+field+` = "`+escapeQueryValue(s)+`")`)
	} else {
		// Open-ish default. Note that stock ScheduleState has no
		// "Closed" value, so for story-shaped types this excludes
		// nothing unless the subscription defines one; a caller that
		// needs a narrower default sets Status explicitly.
		clauses = append(clauses, `(`+field+` != "Closed")`)
	}
	if p := strings.TrimSpace(f.Parent); p != "" {
		clauses = append(clauses, `(`+parentField(t, p)+`.FormattedID = "`+escapeQueryValue(p)+`")`)
	}
	return andClauses(clauses)
}

// andClauses joins WSAPI clauses with AND. Rally's parser is strictly
// binary, so three clauses nest as ((a AND b) AND c).
func andClauses(clauses []string) string {
	switch len(clauses) {
	case 0:
		return ""
	case 1:
		return clauses[0]
	}
	out := clauses[0]
	for _, cl := range clauses[1:] {
		out = "(" + out + " AND " + cl + ")"
	}
	return out
}

// List returns artifacts matching f across every configured type, newest
// updated first. Results from each type are merged and re-sorted, then
// truncated to Limit.
func (c *Client) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	userName := ""
	if strings.EqualFold(strings.TrimSpace(f.Assignee), "me") {
		u, err := c.currentUser(ctx)
		if err != nil {
			return nil, err
		}
		userName = u.UserName
	}

	var w warnBuf
	var out []ticket.TrackerTicket
	for _, t := range c.configuredTypes() {
		items, err := c.listType(ctx, t, f, userName, &w)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	// A List call has no single ticket to key its warnings by, and the
	// problems it does record (a WSAPI warning on the query, a row that
	// would not decode) qualify every ticket the call returned — so each
	// returned key carries them.
	for _, item := range out {
		c.putWarnings(item.Key, w.msgs)
	}
	return out, nil
}

// listType pages one collection until Limit is reached or the collection is
// exhausted.
func (c *Client) listType(ctx context.Context, t artifactType, f source.ListFilter, userName string, w *warnBuf) ([]ticket.TrackerTicket, error) {
	q := c.listQuery(t, f, userName)
	pageSize := maxPageSize
	if f.Limit > 0 && f.Limit < pageSize {
		pageSize = f.Limit
	}

	var out []ticket.TrackerTicket
	start := 1
	for {
		res, err := c.runQuery(ctx, t, queryOpts{
			Query:    q,
			Order:    "LastUpdateDate DESC",
			Start:    start,
			PageSize: pageSize,
			Project:  true,
		}, w)
		if err != nil {
			return nil, err
		}
		if len(res.Results) == 0 {
			break
		}
		for _, raw := range res.Results {
			item, derr := decodeArtifact(raw, t)
			if derr != nil {
				w.addf("rally: list %s: %v", t.Path, derr)
				continue
			}
			out = append(out, c.toTracker(item))
			if f.Limit > 0 && len(out) >= f.Limit {
				return out, nil
			}
		}
		start += len(res.Results)
		if res.TotalResultCount > 0 && start > res.TotalResultCount {
			break
		}
		if len(res.Results) < pageSize {
			break
		}
	}
	return out, nil
}
