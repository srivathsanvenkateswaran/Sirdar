package helpscout

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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

// dataURL is where an attachment's bytes are fetched from: the link the
// API gave, when it gave one, resolved against the API base URL if it came
// back relative; otherwise the documented path built from ids this client
// already holds. A link taken out of a response body is checked against
// urlTrust before it is used, same as any other.
func (c *Client) dataURL(convID string, threadID int64, a hsAttachment) string {
	href := strings.TrimSpace(a.Links.Data.Href)
	switch {
	case href == "":
	case strings.HasPrefix(href, "/"):
		return c.baseURL + href
	default:
		return href
	}
	return fmt.Sprintf("%s/v2/conversations/%s/threads/%d/attachments/%d/data",
		c.baseURL, url.PathEscape(convID), threadID, a.ID)
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

// maxAttachmentJSON is how much of an attachment-data response is read.
// The bytes arrive base64-encoded inside JSON, which costs four characters
// per three bytes, plus room for the envelope around them.
func maxAttachmentJSON() int64 { return maxAttachmentBytes/3*4 + 4096 }

// downloadTo fetches one attachment's JSON envelope, decodes the base64
// payload and writes it to destPath. A payload past maxAttachmentBytes is
// a download failure and nothing is written, so a truncated attachment is
// never left on disk looking complete.
func (c *Client) downloadTo(ctx context.Context, rawURL, destPath string) error {
	body, err := c.doRaw(ctx, rawURL, true, maxAttachmentJSON())
	if err != nil {
		return err
	}
	var env struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: GET %s: decode: %v", logPath(rawURL), err)}
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env.Data))
	if err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: GET %s: decode attachment data: %v", logPath(rawURL), err)}
	}
	if int64(len(raw)) > maxAttachmentBytes {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: GET %s: attachment exceeds the %d byte limit", logPath(rawURL), maxAttachmentBytes)}
	}
	if err := os.WriteFile(destPath, raw, 0o644); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: write %s: %v", filepath.Base(destPath), err)}
	}
	return nil
}

// Attachments downloads every attachment on a conversation's threads into
// dir, named "<1-based index>-<sanitised name>" in thread order.
//
// A download skipped for an untrusted host, or one that otherwise fails,
// is not fatal: it is recorded through WarningsFor and the rest are still
// returned. Only when every attachment fails does Attachments return a
// non-nil error (errors.Join of the individual failures), and then the
// failures are not also recorded as warnings, since the caller already has
// every one of them in the error.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	conv, err := c.fetchConversation(ctx, id)
	if err != nil {
		return nil, err
	}
	threads, pagingWarnings, err := c.listThreads(ctx, conv)
	if err != nil {
		return nil, err
	}

	var refs []attachmentRef
	for _, th := range threads {
		for _, a := range th.Embedded.Attachments {
			refs = append(refs, attachmentRef{
				ID:   strconv.FormatInt(a.ID, 10),
				Name: a.Filename,
				MIME: a.MimeType,
				URL:  c.dataURL(id, th.ID, a),
			})
		}
	}
	if len(refs) == 0 {
		c.addWarnings(id, pagingWarnings)
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("helpscout: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	warnings := append([]string(nil), pagingWarnings...)
	for i, r := range refs {
		u, perr := url.Parse(r.URL)
		if perr != nil || u.Host == "" {
			warnings = append(warnings, fmt.Sprintf("helpscout: attachment %s: invalid url", r.ID))
			continue
		}
		host, trusted, _ := c.urlTrust(u)
		if !trusted {
			warnings = append(warnings, fmt.Sprintf("helpscout: attachment host not trusted: %s", host))
			continue
		}

		name := r.Name
		if name == "" {
			name = r.ID
		}
		name = sanitizeName(name)
		filename := fmt.Sprintf("%d-%s", i+1, name)

		if derr := c.downloadTo(ctx, r.URL, filepath.Join(dir, filename)); derr != nil {
			warnings = append(warnings, fmt.Sprintf("helpscout: download attachment %s: %v", r.ID, derr))
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
