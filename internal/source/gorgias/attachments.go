package gorgias

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// attachmentRef is a resolved reference to one downloadable attachment.
type attachmentRef struct {
	ID   string
	Name string
	MIME string
	URL  string
}

// attachmentID is the synthetic id Threads and Attachments agree on for
// the same file. A Gorgias File object carries no id of its own — the URL
// is its identifier, and a URL is neither stable enough nor safe enough to
// put in a note — so the id is derived from the message it hangs off and
// its position, which is stable for as long as the message is.
func attachmentID(messageID int64, i int) string {
	return fmt.Sprintf("%s-%d", strconv.FormatInt(messageID, 10), i+1)
}

// attachmentIDs builds the ids for one message's attachments, skipping a
// file with no URL: Attachments cannot fetch one, so Threads must not
// promise it.
func attachmentIDs(m gMessage) []string {
	var ids []string
	for i, a := range m.Attachments {
		if strings.TrimSpace(a.URL) == "" {
			continue
		}
		ids = append(ids, attachmentID(m.ID, i))
	}
	return ids
}

// collectRefs gathers every downloadable attachment on a ticket, in thread
// order.
func collectRefs(msgs []gMessage) []attachmentRef {
	var refs []attachmentRef
	for _, m := range msgs {
		for i, a := range m.Attachments {
			if strings.TrimSpace(a.URL) == "" {
				continue
			}
			id := attachmentID(m.ID, i)
			name := strings.TrimSpace(a.Name)
			if name == "" {
				name = nameFromURL(a.URL, id)
			}
			refs = append(refs, attachmentRef{ID: id, Name: name, MIME: a.ContentType, URL: a.URL})
		}
	}
	return refs
}

// nameFromURL derives a filename from an attachment URL whose own name
// field was empty, falling back to the synthetic id. The query string is
// dropped first: a Gorgias download URL's query is a signature, not part
// of the name.
func nameFromURL(rawURL, id string) string {
	clean := rawURL
	if i := strings.IndexByte(clean, '?'); i >= 0 {
		clean = clean[:i]
	}
	if u, err := url.Parse(clean); err == nil && u.Path != "" {
		base := path.Base(u.Path)
		if base != "" && base != "." && base != "/" && strings.Contains(base, ".") {
			return base
		}
	}
	return id
}

// Attachments downloads every attachment on a ticket's messages into dir,
// named "<1-based index>-<sanitised name>" in thread order.
//
// A download skipped for an untrusted host, or one that otherwise fails,
// is not fatal: it is recorded through WarningsFor and the rest are still
// returned. Only when every attachment fails does Attachments return a
// non-nil error (errors.Join of the individual failures), and then the
// failures are not also recorded as warnings, since the caller already has
// every one of them in the error.
//
// An attachment URL arrives inside an API response body, so it is checked
// before it is fetched: the API key goes only to the configured account
// host, Gorgias's other hosts are fetched from without it, and anything
// else is skipped with the host named and nothing else — the rest of an
// attacker-chosen URL has no business in a warning an agent reads.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	msgs, pagingWarnings, err := c.listMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	refs := collectRefs(msgs)
	if len(refs) == 0 {
		c.addWarnings(id, pagingWarnings)
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("gorgias: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	warnings := append([]string(nil), pagingWarnings...)
	for i, r := range refs {
		u, perr := url.Parse(r.URL)
		if perr != nil || u.Host == "" {
			warnings = append(warnings, fmt.Sprintf("gorgias: attachment %s: invalid url", r.ID))
			continue
		}
		trusted, sendAuth, _ := c.trust.Check(u)
		if !trusted {
			warnings = append(warnings, fmt.Sprintf("gorgias: attachment host not trusted: %s", httpx.NormalizeHost(u.Scheme, u.Host)))
			continue
		}

		name := httpx.SanitizeName(r.Name)
		filename := fmt.Sprintf("%d-%s", i+1, name)

		if derr := c.downloadTo(ctx, r.URL, sendAuth, filepath.Join(dir, filename)); derr != nil {
			warnings = append(warnings, fmt.Sprintf("gorgias: download attachment %s: %v", r.ID, derr))
			continue
		}
		out = append(out, ticket.Attachment{
			ID:   r.ID,
			Name: name,
			MIME: r.MIME,
			Path: base + "/" + filename,
		})
	}

	if len(out) == 0 && len(warnings) > 0 {
		errs := make([]error, len(warnings))
		for i, w := range warnings {
			errs[i] = errors.New(w)
		}
		return out, errors.Join(errs...)
	}
	c.addWarnings(id, warnings)
	return out, nil
}
