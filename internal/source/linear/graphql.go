package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
)

// --- Query documents ---
//
// The queries are hand-written rather than generated: Sirdar reads a fixed,
// small set of fields, and a generator would pull in a schema dependency for
// no gain. Every operation is named so a test server (and Linear's own
// endpoint-level rate-limit accounting) can tell them apart.

// issueFields is the selection set Sirdar maps onto ticket.TrackerTicket.
const issueFields = `
    id
    identifier
    title
    description
    priority
    priorityLabel
    state { name type }
    assignee { name email }
    team { key name }
    project { name }
    parent { identifier }
    labels { nodes { name } }
    url
    createdAt
    updatedAt
    attachments { nodes { url title subtitle sourceType } }`

// customerNeedsField counts the Customer Requests attached to an issue.
// It is requested optionally: the field is only present on workspaces where
// the Customers feature is enabled, and asking for it elsewhere fails the
// whole query with a GraphQL validation error, so the client retries without
// it (see fieldValidationError).
const customerNeedsField = `
    customerNeeds { nodes { id } }`

func issueQuery(withNeeds bool) string {
	return "query Issue($id: String!) {\n  issue(id: $id) {" + issueFields + needs(withNeeds) + "\n  }\n}"
}

func issuesQuery(withNeeds bool) string {
	return "query Issues($filter: IssueFilter, $first: Int!, $after: String) {\n" +
		"  issues(filter: $filter, first: $first, after: $after) {\n" +
		"    nodes {" + issueFields + needs(withNeeds) + "\n    }\n" +
		"    pageInfo { hasNextPage endCursor }\n  }\n}"
}

func needs(withNeeds bool) string {
	if withNeeds {
		return customerNeedsField
	}
	return ""
}

// issueIDQuery resolves a human identifier ("ENG-123") to the issue's UUID,
// which is what IssueFilter.parent.id compares against.
const issueIDQuery = `query IssueID($id: String!) {
  issue(id: $id) { id identifier }
}`

// commentActorFields name the comment authors that are not workspace users.
// Like customerNeedsField they are requested optionally: neither is confirmed
// against Linear's live schema, and a workspace that does not have them would
// otherwise fail every conversation fetch outright rather than returning a
// thread with plainer authorship (see fieldValidationError).
const commentActorFields = `
        externalUser { name email }
        botActor { name }`

// conversationQuery fetches one page of an issue's comments together with the
// description and attachment list, which the thread and attachment mapping
// both need.
func conversationQuery(withActors bool) string {
	actors := ""
	if withActors {
		actors = commentActorFields
	}
	return `query IssueConversation($id: String!, $first: Int!, $after: String) {
  issue(id: $id) {
    id
    identifier
    description
    attachments { nodes { url title subtitle sourceType } }
    comments(first: $first, after: $after) {
      nodes {
        id
        body
        createdAt
        url
        user { name email }` + actors + `
        parent { id }
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`
}

const pingQuery = `query Ping {
  viewer { id name }
}`

// --- Transport ---

// gqlEnvelope is the standard GraphQL response body. Linear returns HTTP 200
// for most failures, so errors has to be checked on every response.
type gqlEnvelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

type gqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Type                   string `json:"type"`
		Code                   any    `json:"code"`
		UserPresentableMessage string `json:"userPresentableMessage"`
	} `json:"extensions"`
}

// maxErrBody caps how much of a failing response body is quoted back in an
// error message.
const maxErrBody = 200

// maxRetryAfter is the longest Retry-After wait the client will honour before
// giving up and reporting rate limiting to the caller.
const maxRetryAfter = httpx.MaxRetryAfter

// query POSTs a GraphQL operation and decodes the response's data object into
// out. A 429 with a Retry-After of at most maxRetryAfter is waited out and
// retried once; every other failure is mapped to a *source.Error.
func (c *Client) query(ctx context.Context, q string, vars map[string]any, out any) error {
	payload := map[string]any{"query": q}
	if len(vars) > 0 {
		payload["variables"] = vars
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return internalf("linear: encode request: %v", err)
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
		if err != nil {
			return internalf("linear: POST %s: %v", c.Endpoint, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		// Personal API keys go in Authorization raw, with no "Bearer" prefix.
		req.Header.Set("Authorization", c.APIKey)

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return internalf("linear: POST %s: %v", c.Endpoint, err)
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if wait, ok := httpx.RetryAfter(resp.Header, maxRetryAfter); ok {
				if err := httpx.SleepCtx(ctx, wait); err != nil {
					return internalf("linear: POST %s: %v", c.Endpoint, err)
				}
				continue
			}
		}
		return decodeResponse(resp.StatusCode, raw, readErr, out)
	}
}

// decodeResponse turns one HTTP response into either a decoded result or a
// *source.Error. GraphQL errors take precedence over the status code, because
// Linear reports rate limiting as HTTP 400 with a RATELIMITED entry in errors.
func decodeResponse(status int, raw []byte, readErr error, out any) error {
	if readErr != nil {
		return internalf("linear: POST: read body: %v", readErr)
	}

	var env gqlEnvelope
	decErr := json.Unmarshal(raw, &env)
	if decErr == nil && len(env.Errors) > 0 {
		return mapGQLErrors(env.Errors)
	}
	if status < 200 || status >= 300 {
		return statusError(status, raw)
	}
	if decErr != nil {
		return internalf("linear: POST: decode: %v: %s", decErr, snippet(raw))
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return internalf("linear: POST: response carried no data: %s", snippet(raw))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return internalf("linear: POST: decode data: %v", err)
	}
	return nil
}

// statusError maps a non-2xx response with no usable GraphQL errors array.
func statusError(status int, body []byte) *source.Error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("linear: POST: %d", status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("linear: POST: %d", status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("linear: POST: %d", status)}
	default:
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("linear: POST: %d: %s", status, snippet(body))}
	}
}

// errRank orders the codes a single errors array can imply, so the most
// actionable one wins when Linear returns several at once.
func errRank(c source.Code) int {
	switch c {
	case source.RateLimited:
		return 3
	case source.Auth:
		return 2
	case source.NotFound:
		return 1
	default:
		return 0
	}
}

// mapGQLErrors classifies a GraphQL errors array. Linear puts a machine
// readable string in extensions.type (some responses use extensions.code),
// and a human-facing one in extensions.userPresentableMessage.
func mapGQLErrors(errs []gqlError) *source.Error {
	code := source.Internal
	msgs := make([]string, 0, len(errs))

	for _, e := range errs {
		kind := strings.ToUpper(e.Extensions.Type)
		if e.Extensions.Code != nil {
			kind += " " + strings.ToUpper(fmt.Sprint(e.Extensions.Code))
		}
		msg := e.Message
		if msg == "" {
			msg = e.Extensions.UserPresentableMessage
		}
		lower := strings.ToLower(msg)

		var got source.Code
		switch {
		case strings.Contains(kind, "RATELIMIT") || strings.Contains(lower, "rate limit"):
			got = source.RateLimited
		case strings.Contains(kind, "AUTHENTICATION") || strings.Contains(kind, "AUTHORIZATION") || strings.Contains(kind, "FORBIDDEN"):
			got = source.Auth
		case strings.Contains(kind, "NOT_FOUND") || strings.Contains(lower, "entity not found") || strings.Contains(lower, "could not find"):
			got = source.NotFound
		default:
			got = source.Internal
		}
		if errRank(got) > errRank(code) {
			code = got
		}
		if msg != "" {
			msgs = append(msgs, msg)
		}
	}

	joined := strings.Join(msgs, "; ")
	if joined == "" {
		joined = "graphql error"
	}
	return &source.Error{Code: code, Message: "linear: " + truncate(joined, maxErrBody)}
}

// fieldValidationError reports whether err is GraphQL's "you asked for a field
// that does not exist" rejection, which is how a workspace without the
// Customers feature answers customerNeeds.
func fieldValidationError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Cannot query field")
}

func internalf(format string, args ...any) *source.Error {
	return &source.Error{Code: source.Internal, Message: fmt.Sprintf(format, args...)}
}

func snippet(b []byte) string { return truncate(string(b), maxErrBody) }

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
