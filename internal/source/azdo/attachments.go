package azdo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
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

// orgName reads the Azure DevOps Services organisation out of the base URL,
// from the first path segment of https://dev.azure.com/{org} or from the
// subdomain of the legacy {org}.visualstudio.com form. It returns "" for an
// Azure DevOps Server collection URL, where the base host is the only host
// that can serve attachments.
func orgName(base *url.URL) string {
	host := strings.ToLower(base.Hostname())
	if host == "dev.azure.com" || strings.HasSuffix(host, ".dev.azure.com") {
		if seg := strings.Split(strings.Trim(base.Path, "/"), "/"); len(seg) > 0 {
			return seg[0]
		}
		return ""
	}
	if strings.HasSuffix(host, ".visualstudio.com") {
		return strings.TrimSuffix(host, ".visualstudio.com")
	}
	return ""
}

// untrustedWarning describes a skipped URL without quoting the URL itself,
// which may carry a token in its query.
func untrustedWarning(rawURL string) string {
	host := "(none)"
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return "azure devops: attachment host not trusted: " + host
}

// refs gathers a work item's downloadable files: every AttachedFile
// relation first, then any image embedded in the description or repro steps
// that is not already one of them. Inline images are how a screenshot pasted
// into the description reaches the bundle, and they are served by the same
// attachments route with the same auth.
//
// Both sources are attacker-editable, so both are filtered through the
// client's trust before anything is fetched: every attachment fetch carries
// the PAT in an Authorization header, so following a URL somebody put in a
// work item description would hand the credential to whoever wrote it. A URL that does not pass is dropped and named
// in the returned skips, which become warnings on the call.
func (c *Client) refs(wi workItem) (out []attachmentRef, skipped []string) {
	seen := map[string]bool{}

	for _, rel := range wi.Relations {
		if !strings.EqualFold(rel.Rel, "AttachedFile") || rel.URL == "" {
			continue
		}
		u, err := url.Parse(rel.URL)
		if err != nil || !c.trusted(u) {
			skipped = append(skipped, untrustedWarning(rel.URL))
			continue
		}
		id := attachmentID(rel.URL)
		if seen[id] {
			continue
		}
		seen[id] = true

		name := rel.attributeString("name")
		if name == "" {
			name = u.Query().Get("fileName")
		}
		if name == "" {
			name = id
		}
		out = append(out, attachmentRef{ID: id, Name: name, URL: attachmentURL(rel.URL, name)})
	}

	inline := 0
	for _, field := range []string{fieldDescription, fieldReproSteps} {
		for _, src := range htmltext.InlineImageSrcs(fieldString(wi.Fields, field)) {
			u, err := url.Parse(src)
			if err != nil || !c.trusted(u) {
				// A relative or data: src is not a fetchable attachment and
				// is not worth warning about; a foreign host is.
				if err == nil && u.Host != "" {
					skipped = append(skipped, untrustedWarning(src))
				}
				continue
			}
			id := attachmentID(src)
			if seen[id] {
				continue
			}
			seen[id] = true
			inline++

			name := u.Query().Get("fileName")
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
	return out, skipped
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
	// Whatever an earlier call in the same bundle recorded for this work
	// item stays where it is: warnings accumulate under the id, and reading
	// them is what clears the entry.
	wi, err := c.getWorkItem(ctx, wid)
	if err != nil {
		return nil, err
	}
	refs, failures := c.refs(wi)
	if len(refs) == 0 {
		if len(failures) > 0 {
			c.addWarnings(wid, failures)
		}
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("azure devops: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	for i, r := range refs {
		name := httpx.SanitizeName(r.Name)
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
	c.addWarnings(wid, failures)
	return out, nil
}

// maxRedirects bounds how far a redirect chain that stays on a trusted host
// is followed before the download is abandoned.
const maxRedirects = 3

// maxAttachmentBytes caps one download. Past it the file is refused rather
// than written: an attachment nobody can vouch for should not be able to
// fill the disk the run is using.
const maxAttachmentBytes = 64 << 20

// trusted reports whether u may be fetched with the organisation's PAT.
func (c *Client) trusted(u *url.URL) bool {
	fetch, _, _ := c.trust.Check(u)
	return fetch
}

// download fetches rawURL with the client's auth and writes the body to
// destPath, returning the response's Content-Type.
func (c *Client) download(ctx context.Context, rawURL, destPath string) (string, error) {
	// Belt and braces: refs already dropped every untrusted URL, and this
	// makes it impossible for a future caller to reach one with the PAT
	// attached.
	u, perr := url.Parse(rawURL)
	if perr != nil || !c.trusted(u) {
		return "", fmt.Errorf("refusing to send credentials to an untrusted host")
	}
	req, err := c.newRequest(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "*/*")

	// A sign-in page here would otherwise be written to disk as if it were
	// the file.
	// The redirect policy applies the same trust check the starting URL
	// got. Azure DevOps answers an unauthenticated (or expired) attachment
	// request with a redirect to the Entra sign-in page rather than a 401,
	// and a Location header is a server response like any other: following
	// one off the org's hosts would write a sign-in page to disk under the
	// attachment's name, and put the request on a host that was never
	// checked.
	ct, err := httpx.Download(ctx, httpx.Client(c.hc, c.trust, maxRedirects), req, destPath, httpx.DownloadOptions{
		Max:        maxAttachmentBytes,
		RefuseHTML: true,
	})
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return "", fmt.Errorf("status %d", se.Status)
	}
	if err != nil {
		return "", err
	}
	return ct, nil
}
