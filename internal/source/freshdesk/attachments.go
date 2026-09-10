package freshdesk

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
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// attachmentRef is a resolved reference to a downloadable attachment,
// gathered from either a ticket/conversation's attachments[] list or an
// inline <img> tag in its HTML body.
type attachmentRef struct {
	ID          string
	Name        string
	URL         string
	ContentType string
}

// collectRefs gathers attachment references from a ticket or conversation's
// direct attachments[] list and from any inline images in its HTML body.
// inlineCounter is shared across a whole Threads/Attachments call so inline
// attachment names number sequentially across the ticket.
func collectRefs(direct []fdAttachment, htmlBody string, inlineCounter *int) []attachmentRef {
	var refs []attachmentRef
	for _, d := range direct {
		if d.AttachmentURL == "" {
			continue
		}
		refs = append(refs, attachmentRef{
			ID:          strconv.FormatInt(d.ID, 10),
			Name:        d.Name,
			URL:         d.AttachmentURL,
			ContentType: d.ContentType,
		})
	}

	for _, src := range htmltext.InlineImageSrcs(htmlBody) {
		if src == "" {
			continue
		}
		*inlineCounter++
		refs = append(refs, attachmentRef{
			ID:   fmt.Sprintf("inline-%d", *inlineCounter),
			Name: inlineName(src, *inlineCounter),
			URL:  src,
		})
	}
	return refs
}

// inlineName derives a filename for an inline image from its src, falling
// back to "inline-<n>.png" when the URL carries no usable basename (e.g. a
// query-parameterised CDN URL with no extension in its path).
func inlineName(src string, n int) string {
	clean := src
	if i := strings.IndexByte(clean, '?'); i >= 0 {
		clean = clean[:i]
	}
	base := path.Base(clean)
	if base != "" && base != "." && base != "/" && strings.Contains(base, ".") {
		return base
	}
	return fmt.Sprintf("inline-%d.png", n)
}

// refIDs returns the ids of refs, or nil for an empty slice.
func refIDs(refs []attachmentRef) []string {
	if len(refs) == 0 {
		return nil
	}
	ids := make([]string, len(refs))
	for i, r := range refs {
		ids[i] = r.ID
	}
	return ids
}

// sanitizeName turns an attachment name (or id) taken from the API response
// into a safe filename component: it strips any directory portion (so a
// name like "../../evil.txt" cannot write outside the destination dir),
// drops path separators and control characters, falls back to "attachment"
// for an empty/"."/".." result, and caps the result at 120 bytes while
// preserving the extension.
func sanitizeName(name string) string {
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

// Attachments downloads every attachment referenced by a ticket's
// description and its conversation thread — direct attachments[] entries
// plus inline images in HTML bodies — into dir, named "<1-based
// index>-<sanitised name>" in collection order.
//
// A download skipped for an untrusted host, or one that otherwise fails, is
// not fatal: it is recorded through WarningsFor and the rest of the
// attachments are still returned. Only when every attachment fails does
// Attachments return a non-nil error (via errors.Join of the individual
// failures), and in that case the failures are not also recorded as
// warnings, since the caller already has every one of them in the error.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	// Whatever Get and Threads recorded for this ticket stays where it is:
	// they are earlier calls in the same bundle, not stale state, and the
	// caller reads WarningsFor once after all three.
	ft, err := c.fetchTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	convs, pagingWarnings, err := c.listConversations(ctx, id)
	if err != nil {
		return nil, err
	}

	inline := 0
	refs := collectRefs(ft.Attachments, ft.Description, &inline)
	for _, cv := range convs {
		refs = append(refs, collectRefs(cv.Attachments, cv.Body, &inline)...)
	}
	if len(refs) == 0 {
		c.addWarnings(id, pagingWarnings)
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("freshdesk: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	warnings := append([]string(nil), pagingWarnings...)
	for i, r := range refs {
		idx := i + 1

		u, perr := url.Parse(r.URL)
		if perr != nil || u.Host == "" {
			warnings = append(warnings, fmt.Sprintf("freshdesk: attachment %s: invalid url", r.ID))
			continue
		}
		host, trusted, sendAuth := c.urlTrust(u)
		if !trusted {
			warnings = append(warnings, fmt.Sprintf("freshdesk: attachment host not trusted: %s", host))
			continue
		}

		name := r.Name
		if name == "" {
			name = r.ID
		}
		name = sanitizeName(name)
		filename := fmt.Sprintf("%d-%s", idx, name)

		if derr := c.downloadTo(ctx, r.URL, sendAuth, filepath.Join(dir, filename)); derr != nil {
			warnings = append(warnings, fmt.Sprintf("freshdesk: download attachment %s: %v", r.ID, derr))
			continue
		}

		out = append(out, ticket.Attachment{
			ID:   r.ID,
			Name: name,
			MIME: r.ContentType,
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
