package azdo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// attachmentRef is a resolved, downloadable file belonging to a work item:
// either an AttachedFile relation or an image embedded in the description or
// repro steps.
type attachmentRef struct {
	ID   string
	Name string
	URL  string
}

// sanitizeName turns a file name taken from an API response into a safe
// filename component: it strips any directory portion (so a name like
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

// attachmentURL rebuilds an attachment URL with the file name and API
// version Azure DevOps wants on the download route, preserving whatever
// fileName the relation already carried when no better name is known.
func attachmentURL(rawURL, name string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	if name != "" {
		q.Set("fileName", name)
	}
	q.Set("api-version", apiVersion)
	u.RawQuery = q.Encode()
	return u.String()
}

// attachmentID reads the attachment GUID out of a relation URL: it is the
// last path segment of .../_apis/wit/attachments/{guid}.
func attachmentID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return path.Base(u.Path)
}

// refs gathers a work item's downloadable files: every AttachedFile
// relation first, then any image embedded in the description or repro steps
// that is not already one of them. Inline images are how a screenshot pasted
// into the description reaches the bundle, and they are served by the same
// attachments route with the same auth.
func (c *Client) refs(wi workItem) []attachmentRef {
	var out []attachmentRef
	seen := map[string]bool{}

	for _, rel := range wi.Relations {
		if !strings.EqualFold(rel.Rel, "AttachedFile") || rel.URL == "" {
			continue
		}
		id := attachmentID(rel.URL)
		if seen[id] {
			continue
		}
		seen[id] = true

		name := rel.attributeString("name")
		if name == "" {
			if u, err := url.Parse(rel.URL); err == nil {
				name = u.Query().Get("fileName")
			}
		}
		if name == "" {
			name = id
		}
		out = append(out, attachmentRef{ID: id, Name: name, URL: attachmentURL(rel.URL, name)})
	}

	inline := 0
	for _, field := range []string{fieldDescription, fieldReproSteps} {
		for _, src := range htmltext.InlineImageSrcs(fieldString(wi.Fields, field)) {
			if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
				continue
			}
			id := attachmentID(src)
			if seen[id] {
				continue
			}
			seen[id] = true
			inline++

			name := ""
			if u, err := url.Parse(src); err == nil {
				name = u.Query().Get("fileName")
			}
			if name == "" {
				if strings.Contains(id, ".") {
					name = id
				} else {
					name = fmt.Sprintf("inline-%d.png", inline)
				}
			}
			out = append(out, attachmentRef{ID: id, Name: name, URL: attachmentURL(src, "")})
		}
	}
	return out
}

// Attachments downloads every file attached to a work item into dir, named
// "<1-based index>-<sanitised name>".
//
// A download failure for one file does not fail the call: it is skipped,
// recorded for WarningsFor to return, and the rest are still returned.
// Only when every download fails does Attachments return an error — and
// then nothing is recorded, since the caller already has every failure in
// the error.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	wid, err := normalizeKey(id)
	if err != nil {
		return nil, err
	}
	// Discard anything an earlier call for this work item left behind
	// before doing anything else: every path out of here from this point
	// on, including the ones that return early, must leave no stale
	// warning for the next caller to pick up as its own.
	c.takeWarnings(wid)
	wi, err := c.getWorkItem(ctx, wid)
	if err != nil {
		return nil, err
	}
	refs := c.refs(wi)
	if len(refs) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("azure devops: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	var failures []string
	for i, r := range refs {
		name := sanitizeName(r.Name)
		filename := fmt.Sprintf("%d-%s", i+1, name)

		mime, derr := c.download(ctx, r.URL, filepath.Join(dir, filename))
		if derr != nil {
			failures = append(failures, fmt.Sprintf("azure devops: download attachment %s: %v", r.ID, derr))
			continue
		}
		out = append(out, ticket.Attachment{ID: r.ID, Name: name, MIME: mime, Path: base + "/" + filename})
	}

	if len(out) == 0 {
		errs := make([]error, len(failures))
		for i, f := range failures {
			errs[i] = errors.New(f)
		}
		// No warnings are recorded when everything failed: the caller
		// already has every one of them in the error.
		return out, errors.Join(errs...)
	}
	c.putWarnings(wid, failures)
	return out, nil
}

// download fetches rawURL with the client's auth and writes the body to
// destPath, returning the response's Content-Type.
func (c *Client) download(ctx context.Context, rawURL, destPath string) (string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "*/*")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	// A sign-in page here would otherwise be written to disk as if it were
	// the file.
	if resp.StatusCode == http.StatusNonAuthoritativeInfo || strings.Contains(strings.ToLower(ct), "text/html") {
		return "", fmt.Errorf("sign-in page returned instead of file content")
	}

	f, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return ct, nil
}
