package intercom

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// attachmentRef is a resolved reference to one downloadable attachment.
type attachmentRef struct {
	ID   string
	Name string
	MIME string
	URL  string
}

// sanitizeName turns an attachment name taken from the API response into a
// safe filename component: it strips any directory portion (so a name like
// "../../evil.txt" cannot write outside the destination dir), drops path
// separators and control characters, falls back to "attachment" for an
// empty/"."/".." result, and caps the result at 120 bytes while preserving
// the extension.
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

// nameFromURL derives a filename from an attachment URL whose own name
// field was empty, falling back to the synthetic id.
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

// collectRefs gathers every attachment on a conversation, in thread order:
// the source message's first, then each part's.
func collectRefs(conv icConversation) []attachmentRef {
	var refs []attachmentRef
	add := func(partID, kind string, atts []icAttachment) {
		for i, a := range atts {
			if strings.TrimSpace(a.URL) == "" {
				continue
			}
			id := attachmentID(partID, kind, i)
			name := a.Name
			if name == "" {
				name = nameFromURL(a.URL, id)
			}
			refs = append(refs, attachmentRef{ID: id, Name: name, MIME: a.ContentType, URL: a.URL})
		}
	}
	add(conv.Source.ID, "source", conv.Source.Attachments)
	for _, p := range conv.ConversationParts.Parts {
		add(p.ID, "part", p.Attachments)
	}
	return refs
}

// Attachments downloads every attachment on a conversation into dir, named
// "<1-based index>-<sanitised name>" in thread order.
//
// A download skipped for an untrusted host, or one that otherwise fails,
// is not fatal: it is recorded through WarningsFor and the rest are still
// returned. Only when every attachment fails does Attachments return a
// non-nil error (errors.Join of the individual failures), and then the
// failures are not also recorded as warnings, since the caller already has
// every one of them in the error.
//
// Intercom's attachment URLs are pre-signed and short-lived (roughly half
// an hour), so a conversation fetched long before its attachments are
// downloaded can hand back links that have already expired; the failure is
// reported per file rather than failing the bundle.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	conv, err := c.fetchConversation(ctx, id)
	if err != nil {
		return nil, err
	}
	refs := collectRefs(conv)
	if len(refs) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("intercom: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	var warnings []string
	for i, r := range refs {
		u, perr := url.Parse(r.URL)
		if perr != nil || u.Host == "" {
			warnings = append(warnings, fmt.Sprintf("intercom: attachment %s: invalid url", r.ID))
			continue
		}
		host, trusted, sendAuth := c.urlTrust(u)
		if !trusted {
			warnings = append(warnings, fmt.Sprintf("intercom: attachment host not trusted: %s", host))
			continue
		}

		name := sanitizeName(r.Name)
		filename := fmt.Sprintf("%d-%s", i+1, name)

		if derr := c.downloadTo(ctx, r.URL, sendAuth, filepath.Join(dir, filename)); derr != nil {
			warnings = append(warnings, fmt.Sprintf("intercom: download attachment %s: %v", r.ID, derr))
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
