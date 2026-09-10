package zendesk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// maxAttachmentBytes caps a single attachment download. A file that would
// exceed it is treated as a download failure, recorded as a warning like
// any other.
const maxAttachmentBytes = 64 << 20 // 64 MiB

// attachmentRef is a resolved reference to a downloadable attachment,
// gathered from either a comment's attachments[] list or an inline <img>
// src in its html_body.
type attachmentRef struct {
	ID   string
	Name string
	URL  string
}

// collectAttachmentRefs gathers every comment attachment and inline image
// reference across comments, in comment order. inlineCounter numbers inline
// images sequentially across the whole ticket, so names stay unique and
// stable regardless of which comment they came from.
func collectAttachmentRefs(comments []zendeskComment) []attachmentRef {
	var refs []attachmentRef
	inlineCounter := 0
	for _, cm := range comments {
		for _, a := range cm.Attachments {
			if a.ContentURL == "" {
				continue
			}
			name := a.FileName
			if name == "" {
				name = strconv.FormatInt(a.ID, 10)
			}
			refs = append(refs, attachmentRef{ID: strconv.FormatInt(a.ID, 10), Name: name, URL: a.ContentURL})
		}
		for _, src := range htmltext.InlineImageSrcs(cm.HTMLBody) {
			inlineCounter++
			name := inlineName(src, inlineCounter)
			refs = append(refs, attachmentRef{ID: fmt.Sprintf("inline-%d", inlineCounter), Name: name, URL: src})
		}
	}
	return refs
}

// inlineName derives a filename for an inline image src: the URL's base
// name when it looks like a filename, else a numbered fallback.
func inlineName(src string, n int) string {
	clean := src
	if i := strings.IndexByte(clean, '?'); i >= 0 {
		clean = clean[:i]
	}
	base := filepath.Base(clean)
	if base != "" && base != "." && base != "/" && strings.Contains(base, ".") {
		return base
	}
	return fmt.Sprintf("inline-%d.png", n)
}

// Attachments downloads every attachment referenced by a ticket's comment
// thread (direct attachments[] entries plus inline images found in
// html_body) into dir, named "<1-based index>-<sanitised name>" in
// collection order.
//
// A download that fails — an untrusted host, a network error, a file over
// maxAttachmentBytes — does not fail the whole call: it is skipped and
// recorded via WarningsFor, and the remaining attachments are still
// returned. Only when every attachment fails does Attachments return a
// non-nil error (errors.Join of the individual failures), in which case
// those failures are not also recorded as warnings, since the caller
// already has all of them in the error.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	// Discard anything an earlier call for this ticket left behind: every
	// path out of here, including the early ones, must leave no stale
	// warning for the next caller to mistake for its own.
	c.takeWarnings(id)

	comments, _, err := c.fetchComments(ctx, id)
	if err != nil {
		return nil, err
	}

	refs := collectAttachmentRefs(comments)
	if len(refs) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zendesk: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	var warnings []string
	for i, r := range refs {
		idx := i + 1

		u, perr := url.Parse(r.URL)
		if perr != nil {
			warnings = append(warnings, fmt.Sprintf("zendesk: attachment %s: invalid url", r.ID))
			continue
		}
		trusted, sendAuth := c.hostTrust(u.Hostname())
		if !trusted {
			warnings = append(warnings, fmt.Sprintf("zendesk: attachment host not trusted: %s", u.Hostname()))
			continue
		}

		name := sanitizeName(r.Name)
		filename := fmt.Sprintf("%d-%s", idx, name)

		mime, derr := c.downloadAttachment(ctx, r.URL, sendAuth, filepath.Join(dir, filename))
		if derr != nil {
			warnings = append(warnings, fmt.Sprintf("zendesk: download attachment %s: %v", r.ID, derr))
			continue
		}
		out = append(out, ticket.Attachment{ID: r.ID, Name: name, MIME: mime, Path: base + "/" + filename})
	}

	if len(out) == 0 {
		errs := make([]error, len(warnings))
		for i, w := range warnings {
			errs[i] = errors.New(w)
		}
		return out, errors.Join(errs...)
	}
	c.putWarnings(id, warnings)
	return out, nil
}

// downloadAttachment fetches rawURL and writes its body to destPath,
// returning the response's Content-Type. It sends this client's
// Authorization header only when sendAuth is true, per the trust decision
// hostTrust already made for rawURL's host.
func (c *Client) downloadAttachment(ctx context.Context, rawURL string, sendAuth bool, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	if sendAuth {
		req.Header.Set("Authorization", c.authHeader)
	}

	resp, err := c.hc.Do(req)
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

	limited := io.LimitReader(resp.Body, maxAttachmentBytes+1)
	n, err := io.Copy(f, limited)
	if err != nil {
		return "", err
	}
	if n > maxAttachmentBytes {
		return "", fmt.Errorf("attachment exceeds %d bytes", maxAttachmentBytes)
	}
	return resp.Header.Get("Content-Type"), nil
}

// sanitizeName turns an attachment name (or id) from the API response into
// a safe filename component: it strips any directory portion (so a name
// like "../../evil.txt" cannot write outside the destination dir), drops
// path separators and control characters, falls back to "attachment" for
// an empty/"."/".." result, and caps the result at 120 bytes while
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
	clean := b.String()
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
