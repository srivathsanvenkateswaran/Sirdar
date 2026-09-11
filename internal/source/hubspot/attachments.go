package hubspot

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// attachmentIDs returns the ids of a message's attachments, in the order
// they appear on it.
func attachmentIDs(m convMessage) []string {
	if len(m.Attachments) == 0 {
		return nil
	}
	ids := make([]string, 0, len(m.Attachments))
	for i, a := range m.Attachments {
		ids = append(ids, attachmentIDOf(m.ID, i, a))
	}
	return ids
}

// attachmentIDOf names one attachment: its file id where HubSpot gave one
// (the id the file is fetched by, and the most useful thing to see in a
// note), then the attachment's own id, then a synthetic id derived from
// the message it hangs off.
func attachmentIDOf(messageID string, i int, a convAttachment) string {
	if a.FileID != "" {
		return a.FileID
	}
	if a.ID != "" {
		return a.ID
	}
	return fmt.Sprintf("%s-%d", messageID, i+1)
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

// signedFile is what /files/v3/files/{fileId}/signed-url answers with: a
// short-lived URL on HubSpot's file CDN, plus the file's own name and
// extension, which the message's attachment entry does not carry.
type signedFile struct {
	URL       string `json:"url"`
	Name      string `json:"name"`
	Extension string `json:"extension"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
}

// filename renders the signed file's name with its extension, since
// HubSpot stores the two separately and a file written without its
// extension is one an operator has to guess at.
func (s signedFile) filename(fallback string) string {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return fallback
	}
	ext := strings.TrimSpace(s.Extension)
	if ext == "" || strings.EqualFold(filepath.Ext(name), "."+ext) {
		return name
	}
	return name + "." + ext
}

// signedURL resolves one file id to its download URL.
func (c *Client) signedURL(ctx context.Context, fileID string) (signedFile, error) {
	var sf signedFile
	if err := c.apiGET(ctx, "/files/v3/files/"+url.PathEscape(fileID)+"/signed-url", nil, &sf); err != nil {
		return signedFile{}, err
	}
	return sf, nil
}

// Attachments downloads every file attached to the ticket's conversation
// messages into dir, named "<1-based index>-<sanitised name>" in message
// order.
//
// Each file takes two calls: the signed-url lookup on api.hubapi.com with
// the access token, then the download itself from whatever host the signed
// URL names — trusted only when it is one of HubSpot's file hosts, and
// never given the access token, since the URL carries its own signature.
//
// A download skipped for an untrusted host, or one that otherwise fails,
// is not fatal: it is recorded through WarningsFor and the rest are still
// returned. Only when every attachment fails does Attachments return a
// non-nil error (errors.Join of the individual failures), and then the
// failures are not also recorded as warnings, since the caller already has
// every one of them in the error.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	t, err := c.fetchTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	msgs, pagingWarnings, err := c.threadMessages(ctx, t)
	if err != nil {
		return nil, err
	}

	type ref struct {
		id     string
		fileID string
		name   string
	}
	var refs []ref
	for _, m := range msgs {
		for i, a := range m.Attachments {
			if a.FileID == "" {
				continue
			}
			refs = append(refs, ref{id: attachmentIDOf(m.ID, i, a), fileID: a.FileID, name: a.Name})
		}
	}
	if len(refs) == 0 {
		c.addWarnings(id, pagingWarnings)
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("hubspot: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	warnings := append([]string(nil), pagingWarnings...)
	for i, r := range refs {
		sf, serr := c.signedURL(ctx, r.fileID)
		if serr != nil {
			warnings = append(warnings, fmt.Sprintf("hubspot: sign attachment %s: %v", r.id, serr))
			continue
		}
		u, perr := url.Parse(sf.URL)
		if perr != nil || u.Host == "" {
			warnings = append(warnings, fmt.Sprintf("hubspot: attachment %s: invalid url", r.id))
			continue
		}
		host, trusted, sendAuth := c.urlTrust(u)
		if !trusted {
			warnings = append(warnings, fmt.Sprintf("hubspot: attachment host not trusted: %s", host))
			continue
		}

		name := sanitizeName(sf.filename(firstNonEmpty(r.name, r.id)))
		filename := fmt.Sprintf("%d-%s", i+1, name)

		if derr := c.downloadTo(ctx, sf.URL, sendAuth, filepath.Join(dir, filename)); derr != nil {
			warnings = append(warnings, fmt.Sprintf("hubspot: download attachment %s: %v", r.id, derr))
			continue
		}
		out = append(out, ticket.Attachment{
			ID:   r.id,
			Name: name,
			MIME: "",
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
