package rally

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// collectionPageSize is the page size used for the discussion and
// attachment collections. WSAPI caps pagesize at 200.
const collectionPageSize = maxPageSize

// collectionURL builds a WSAPI collection query URL with an explicit fetch
// list, used for the collections whose fields differ from an artifact's.
func (c *Client) collectionURL(path, query, fetch, order string, start, pageSize int) string {
	q := url.Values{}
	q.Set("query", query)
	q.Set("fetch", fetch)
	if order != "" {
		q.Set("order", order)
	}
	if ws := strings.TrimSpace(c.cfg.Workspace); ws != "" {
		q.Set("workspace", ws)
	}
	if start < 1 {
		start = 1
	}
	q.Set("start", strconv.Itoa(start))
	if pageSize < 1 || pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	q.Set("pagesize", strconv.Itoa(pageSize))
	return c.endpoint(path) + "?" + q.Encode()
}

// collect pages a WSAPI collection to exhaustion, returning every raw
// result. start is 1-based and advances by the number of rows actually
// returned, which is how WSAPI expects a client to page.
func (c *Client) collect(ctx context.Context, path, query, fetch, order string, w *warnBuf) ([]json.RawMessage, error) {
	var all []json.RawMessage
	start := 1
	for {
		var env queryEnvelope
		if err := c.get(ctx, c.collectionURL(path, query, fetch, order, start, collectionPageSize), &env); err != nil {
			return nil, err
		}
		res := env.QueryResult
		if len(res.Errors) > 0 {
			return nil, resultError("query "+path, res.Errors)
		}
		for _, msg := range res.Warnings {
			w.addf("rally: query %s: %s", path, msg)
		}
		if len(res.Results) == 0 {
			break
		}
		all = append(all, res.Results...)
		start += len(res.Results)
		if res.TotalResultCount > 0 && len(all) >= res.TotalResultCount {
			break
		}
		if len(res.Results) < collectionPageSize {
			break
		}
	}
	return all, nil
}

// artifactClause filters a collection by its owning artifact's _ref. The
// ref comes back from WSAPI rather than from a caller, but it is checked
// on the same terms as any other interpolated value.
func artifactClause(ref string) (string, error) {
	if err := checkQueryValue("artifact ref", ref); err != nil {
		return "", err
	}
	return `(Artifact = "` + ref + `")`, nil
}

// --- Discussion ---

type conversationPost struct {
	Ref          string      `json:"_ref"`
	ObjectID     json.Number `json:"ObjectID"`
	Text         string      `json:"Text"`
	User         *rallyRef   `json:"User"`
	CreationDate string      `json:"CreationDate"`
}

// Threads returns the artifact's discussion as the conversation.
//
// Rally does not distinguish an external customer from an internal user on
// a ConversationPost — every post is written by a licensed Rally user — so
// every message is mapped with RoleAgent rather than guessing.
func (h helpdeskView) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	c := h.Client
	var w warnBuf
	defer func() { c.addWarnings(id, w.msgs) }()

	art, err := c.find(ctx, id, &w)
	if err != nil {
		return nil, err
	}
	clause, err := artifactClause(art.a.Ref)
	if err != nil {
		return nil, err
	}
	raws, err := c.collect(ctx, "conversationpost", clause, "Text,User,CreationDate", "CreationDate ASC", &w)
	if err != nil {
		return nil, err
	}

	msgs := make(ticket.Thread, 0, len(raws))
	for _, raw := range raws {
		var p conversationPost
		if err := json.Unmarshal(raw, &p); err != nil {
			w.addf("rally: decode conversationpost: %v", err)
			continue
		}
		// Post text is HTML; images inside it reference files that
		// live in the artifact's Attachment collection, which
		// Attachments fetches, so the src list is not needed here.
		text, _ := htmltext.ToMarkdown(p.Text)
		msgs = append(msgs, ticket.Message{
			At:     parseRallyTime(p.CreationDate),
			Author: p.User.name(),
			Role:   ticket.RoleAgent,
			Text:   text,
		})
	}

	// The order clause already asks for ascending CreationDate; sorting
	// again keeps the thread coherent if a subscription ignores it or
	// pages come back interleaved.
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].At.Before(msgs[j].At) })
	return msgs, nil
}

// --- Attachments ---

type rallyAttachment struct {
	Ref         string      `json:"_ref"`
	ObjectID    json.Number `json:"ObjectID"`
	Name        string      `json:"Name"`
	ContentType string      `json:"ContentType"`
	Size        json.Number `json:"Size"`
	Content     *rallyRef   `json:"Content"`
}

// attachmentContentResult is the AttachmentContent object holding the blob.
// A single-object GET answers with the type name as the top-level key;
// some builds wrap it in OperationResult, so both are accepted.
type attachmentContentResult struct {
	AttachmentContent *struct {
		Content string `json:"Content"`
	} `json:"AttachmentContent"`
	OperationResult *struct {
		Errors            []string `json:"Errors"`
		AttachmentContent *struct {
			Content string `json:"Content"`
		} `json:"AttachmentContent"`
	} `json:"OperationResult"`
}

// Attachments downloads every file attached to the artifact into dir, named
// "<1-based index>-<sanitised name>".
//
// A failure on one file is recorded as a warning and skipped; only when
// every attachment fails does the call return an error, so one unreadable
// file never costs the caller the rest of the evidence.
func (h helpdeskView) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	c := h.Client
	var w warnBuf
	defer func() { c.addWarnings(id, w.msgs) }()

	art, err := c.find(ctx, id, &w)
	if err != nil {
		return nil, err
	}
	clause, err := artifactClause(art.a.Ref)
	if err != nil {
		return nil, err
	}
	raws, err := c.collect(ctx, "attachment", clause, "Name,ContentType,Size,Content", "", &w)
	if err != nil {
		return nil, err
	}
	if len(raws) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("rally: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	var failures []string
	for i, raw := range raws {
		var a rallyAttachment
		if err := json.Unmarshal(raw, &a); err != nil {
			failures = append(failures, fmt.Sprintf("rally: decode attachment %d: %v", i+1, err))
			continue
		}
		attID := a.ObjectID.String()
		if attID == "" {
			attID = a.Ref
		}
		name := sanitizeName(firstNonEmpty(a.Name, attID))
		filename := fmt.Sprintf("%d-%s", i+1, name)

		blob, err := c.attachmentContent(ctx, a)
		if err != nil {
			failures = append(failures, fmt.Sprintf("rally: download attachment %s: %v", attID, err))
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, filename), blob, 0o644); err != nil {
			failures = append(failures, fmt.Sprintf("rally: write attachment %s: %v", attID, err))
			continue
		}
		out = append(out, ticket.Attachment{
			ID:   attID,
			Name: name,
			MIME: a.ContentType,
			Path: base + "/" + filename,
		})
	}

	if len(out) > 0 {
		// Every failure is already in the error when nothing at all
		// downloaded, and repeating them as warnings would put the
		// same line in front of the agent twice.
		for _, f := range failures {
			w.addf("%s", f)
		}
	}
	if len(out) == 0 && len(failures) > 0 {
		errs := make([]error, len(failures))
		for i, f := range failures {
			errs[i] = errors.New(f)
		}
		return out, errors.Join(errs...)
	}
	return out, nil
}

// attachmentContent follows an Attachment's Content reference to the
// AttachmentContent object and base64-decodes the blob it holds.
func (c *Client) attachmentContent(ctx context.Context, a rallyAttachment) ([]byte, error) {
	if a.Content == nil || a.Content.Ref == "" {
		return nil, errors.New("attachment carries no Content reference")
	}
	target, err := c.refURL(a.Content.Ref)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("fetch", "Content")
	u.RawQuery = q.Encode()

	var res attachmentContentResult
	if err := c.getLimited(ctx, u.String(), maxAttachmentBody, &res); err != nil {
		return nil, err
	}
	content := ""
	if res.AttachmentContent != nil {
		content = res.AttachmentContent.Content
	}
	if content == "" && res.OperationResult != nil {
		if len(res.OperationResult.Errors) > 0 {
			return nil, errors.New(strings.Join(res.OperationResult.Errors, "; "))
		}
		if res.OperationResult.AttachmentContent != nil {
			content = res.OperationResult.AttachmentContent.Content
		}
	}
	if content == "" {
		return nil, errors.New("AttachmentContent carried no Content")
	}
	return decodeBase64(content, maxAttachmentBytes)
}

// decodeBase64 decodes a WSAPI Content field, tolerating the line breaks
// and padding-free variants seen in the wild. It refuses a payload whose
// decoded size would exceed max, so a hostile or corrupt Content field
// cannot turn one attachment into an out-of-memory failure — the check is
// on the encoded length, before any allocation.
func decodeBase64(s string, max int64) ([]byte, error) {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	if int64(base64.StdEncoding.DecodedLen(len(clean))) > max {
		return nil, fmt.Errorf("content decodes to more than the %d byte limit", max)
	}
	if b, err := base64.StdEncoding.DecodeString(clean); err == nil {
		return b, nil
	}
	b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "="))
	if err != nil {
		return nil, fmt.Errorf("base64: %w", err)
	}
	return b, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// sanitizeName turns an attachment name taken from the API into a safe
// filename component: it strips any directory portion (so a name like
// "../../evil.txt" cannot write outside the destination dir), drops path
// separators and control characters, falls back to "attachment" for an
// empty/"."/".." result, and caps the result at 120 bytes while preserving
// the extension.
func sanitizeName(name string) string {
	// Windows-style separators survive filepath.Base on Unix, so strip
	// them before taking the base.
	name = strings.ReplaceAll(name, `\`, "/")
	base := filepath.Base(name)

	var b strings.Builder
	for _, r := range base {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	clean := strings.TrimSpace(b.String())
	if clean == "" || clean == "." || clean == ".." {
		clean = "attachment"
	}
	return capBytes(clean, 120)
}

// capBytes truncates name to at most max bytes, preserving its extension
// where possible and never splitting a multi-byte UTF-8 rune.
func capBytes(name string, max int) string {
	if len(name) <= max {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) >= max {
		return truncateValidUTF8(name, max)
	}
	stem := truncateValidUTF8(name[:len(name)-len(ext)], max-len(ext))
	return stem + ext
}

func truncateValidUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
