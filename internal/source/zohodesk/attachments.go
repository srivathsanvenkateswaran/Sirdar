package zohodesk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// attachmentRef is a resolved reference to a downloadable attachment,
// gathered from either a thread/comment's attachments[] list or an inline
// <img> tag in its HTML content.
type attachmentRef struct {
	ID   string
	Name string
	URL  string
}

// inlineImgRe matches an <img> src attribute pointing at an inline
// attachment, e.g. src="/supportapi/x/inlineattachments/i9".
var inlineImgRe = regexp.MustCompile(`src="([^"]*inlineattachments[^"]*)"`)

// resolveURL turns a possibly-relative href/src from a Zoho Desk payload
// into an absolute URL against BaseURL.
func (c *Client) resolveURL(href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if !strings.HasPrefix(href, "/") {
		href = "/" + href
	}
	return c.BaseURL + href
}

// collectEntryAttachments gathers attachment references from a thread or
// comment's direct attachments[] list and from any inline images in its
// HTML content. inlineCounter is shared across a whole Threads/Attachments
// call so inline attachment names (inline-<n>.png) number sequentially
// across the ticket.
func (c *Client) collectEntryAttachments(direct []zohoAttachmentRef, htmlContent string, inlineCounter *int) []attachmentRef {
	var refs []attachmentRef
	for _, d := range direct {
		if d.Href == "" {
			continue
		}
		refs = append(refs, attachmentRef{ID: d.ID, Name: d.Name, URL: c.resolveURL(d.Href)})
	}

	for _, m := range inlineImgRe.FindAllStringSubmatch(htmlContent, -1) {
		*inlineCounter++
		raw := m[1]
		clean := raw
		if i := strings.IndexByte(clean, '?'); i >= 0 {
			clean = clean[:i]
		}
		base := path.Base(clean)
		name := fmt.Sprintf("inline-%d.png", *inlineCounter)
		if strings.Contains(base, ".") {
			name = base
		}
		refs = append(refs, attachmentRef{ID: base, Name: name, URL: c.resolveURL(raw)})
	}
	return refs
}

// Attachments downloads every attachment referenced by a ticket's
// conversation (thread attachments[], inline images in HTML content, and
// IM-session attachments referenced the same way) into dir, named
// "<1-based index>-<name>" in collection order.
//
// A download failure for one attachment does not fail the call: it is
// skipped, recorded in LastWarnings, and the remaining attachments are
// still returned. Only when every attachment fails to download does
// Attachments return a non-nil error (via errors.Join of the individual
// failures).
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	c.LastWarnings = nil

	entries, err := c.listConversations(ctx, id)
	if err != nil {
		return nil, err
	}

	var refs []attachmentRef
	inlineCounter := 0
	for _, e := range entries {
		switch e.Type {
		case "thread":
			td, err := c.getThread(ctx, id, e.ID)
			if err != nil {
				return nil, err
			}
			refs = append(refs, c.collectEntryAttachments(td.Attachments, td.Content, &inlineCounter)...)
		case "comment":
			refs = append(refs, c.collectEntryAttachments(e.Attachments, e.Content, &inlineCounter)...)
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zoho desk: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	var warnings []string
	for i, r := range refs {
		idx := i + 1
		name := r.Name
		if name == "" {
			name = r.ID
		}
		filename := fmt.Sprintf("%d-%s", idx, name)

		mime, derr := c.downloadAttachment(ctx, r.URL, filepath.Join(dir, filename))
		if derr != nil {
			warnings = append(warnings, fmt.Sprintf("zoho desk: download attachment %s: %v", r.ID, derr))
			continue
		}
		out = append(out, ticket.Attachment{ID: r.ID, Name: name, MIME: mime, Path: base + "/" + filename})
	}
	c.LastWarnings = warnings

	if len(out) == 0 {
		errs := make([]error, len(warnings))
		for i, w := range warnings {
			errs[i] = errors.New(w)
		}
		return out, errors.Join(errs...)
	}
	return out, nil
}

// downloadAttachment fetches url with the client's auth headers and writes
// its body to destPath, returning the response's Content-Type.
func (c *Client) downloadAttachment(ctx context.Context, url, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	c.setHeaders(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return resp.Header.Get("Content-Type"), nil
}
