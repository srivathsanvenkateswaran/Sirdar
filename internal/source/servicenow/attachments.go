package servicenow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// attachmentRef is one row of the Attachment API's metadata list.
type attachmentRef struct {
	ID          string
	Name        string
	MIME        string
	DownloadURL string
}

// Attachments implements source.Helpdesk: it downloads every file attached
// to the record into dir, named "<1-based index>-<sanitised filename>".
//
// A file that will not download is skipped and reported through
// WarningsFor rather than failing the call, because a bundle missing one
// screenshot is still worth triaging. Only when every download fails does
// Attachments return an error, and then the failures are not also recorded
// as warnings, since the caller already has every one of them.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	rec, err := c.fetchRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	sysID := rec.str("sys_id")

	var warnings []string
	refs, lerr := c.attachmentRefs(ctx, sysID, &warnings)
	if lerr != nil {
		return nil, lerr
	}
	if len(refs) == 0 {
		c.addWarnings(id, warnings)
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("servicenow: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var (
		out      []ticket.Attachment
		failures []error
	)
	for i, r := range refs {
		// Checked before the request is built, so an attachment pointed at
		// somebody else's host costs no round trip and, more to the point,
		// never sees the credential. The warning names the host and
		// nothing else: the rest of the URL is response-supplied text
		// headed for a log.
		if fetch, _, _ := c.trust.CheckRaw(r.DownloadURL); !fetch {
			host := httpx.HostOf(r.DownloadURL)
			warnings = append(warnings, fmt.Sprintf("servicenow: attachment host not trusted: %s", host))
			failures = append(failures, fmt.Errorf("attachment %s: host not trusted: %s", r.ID, host))
			continue
		}

		name := httpx.SanitizeName(r.Name)
		if name == httpx.FallbackName && r.ID != "" {
			name = httpx.SanitizeName(r.ID)
		}
		filename := fmt.Sprintf("%d-%s", i+1, name)

		ct, derr := c.download(ctx, r.DownloadURL, filepath.Join(dir, filename))
		if derr != nil {
			warnings = append(warnings, fmt.Sprintf("servicenow: download attachment %s (%s): %v", r.ID, name, derr))
			failures = append(failures, fmt.Errorf("attachment %s: %w", r.ID, derr))
			continue
		}
		out = append(out, ticket.Attachment{
			ID:   r.ID,
			Name: name,
			MIME: pickMIME(r.MIME, ct),
			Path: base + "/" + filename,
		})
	}

	if len(out) == 0 && len(failures) > 0 {
		// Every download failed, so the caller gets all of them in the
		// error and needs no warning saying the same thing again. What Get
		// and Threads recorded for the same ticket is left untouched.
		return nil, errors.Join(failures...)
	}
	c.addWarnings(id, warnings)
	return out, nil
}

// attachmentRefs lists the metadata for every file on a record, paging
// with sysparm_offset until a short page ends the list or the page cap
// stops it.
func (c *Client) attachmentRefs(ctx context.Context, sysID string, warnings *[]string) ([]attachmentRef, error) {
	if sysID == "" {
		return nil, nil
	}
	var out []attachmentRef
	offset := 0
	for page := 0; ; page++ {
		if page >= maxAttachmentPages {
			*warnings = append(*warnings, fmt.Sprintf("servicenow: attachment list stopped at the %d-page cap after %d files; some may be missing", maxAttachmentPages, len(out)))
			break
		}
		q := url.Values{}
		q.Set("sysparm_query", "table_name="+encodedValue(c.table)+"^table_sys_id="+encodedValue(sysID)+"^ORDERBYsys_created_on")
		q.Set("sysparm_limit", strconv.Itoa(attachmentPageSize))
		q.Set("sysparm_offset", strconv.Itoa(offset))

		var resp tableResponse
		if err := c.get(ctx, "/api/now/attachment", q, &resp); err != nil {
			if len(out) > 0 {
				*warnings = append(*warnings, fmt.Sprintf("servicenow: attachment list page %d: %v", page+1, err))
				return out, nil
			}
			return nil, err
		}
		for _, r := range resp.Result {
			ref := attachmentRef{
				ID:          r.str("sys_id"),
				Name:        r.str("file_name"),
				MIME:        r.str("content_type"),
				DownloadURL: strings.TrimSpace(r.str("download_link")),
			}
			if ref.DownloadURL == "" && ref.ID != "" {
				// An instance that omits download_link is not a reason to
				// skip the file: the documented path is derivable from the
				// attachment's own sys_id, and it is on the instance host
				// by construction.
				ref.DownloadURL = c.baseURL + "/api/now/attachment/" + url.PathEscape(ref.ID) + "/file"
			}
			if ref.DownloadURL == "" {
				*warnings = append(*warnings, "servicenow: an attachment row carried neither a download link nor a sys_id")
				continue
			}
			out = append(out, ref)
		}
		if len(resp.Result) < attachmentPageSize {
			break
		}
		offset += len(resp.Result)
	}
	return out, nil
}

// pickMIME prefers the type ServiceNow recorded for the attachment over
// the one the download response advertised; a proxy in front of the
// instance often serves everything as octet-stream.
func pickMIME(declared, served string) string {
	if declared != "" {
		return declared
	}
	return served
}

// download fetches one attachment into destPath and returns the response's
// Content-Type. It refuses anything that looks like a login page: a
// redirect off the instance host, or an HTML body where a binary was
// expected — an instance behind SSO answers an unauthenticated attachment
// request with the sign-in form, and writing that to disk under the
// attachment's name is how a login page ends up in an evidence bundle.
func (c *Client) download(ctx context.Context, rawURL, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	c.setHeaders(req)
	req.Header.Set("Accept", "*/*")

	// An untrusted hop stops the chain rather than failing it, so the 3xx
	// comes back here and the message can name where the request was being
	// sent without quoting the SSO query string, which carries the
	// original URL and sometimes a token.
	dl := *c.hc
	dl.CheckRedirect = httpx.RedirectPolicyStop(c.trust, maxRedirects)

	ct, err := httpx.Download(ctx, &dl, req, destPath, httpx.DownloadOptions{
		Max:        maxAttachmentBytes,
		RefuseHTML: true,
	})
	var se *httpx.StatusError
	if errors.As(err, &se) {
		if se.Status >= 300 && se.Status < 400 {
			return "", fmt.Errorf("redirected to %s: the credential was not accepted for the attachment URL (SSO login page?)", redirectTarget(se.Header.Get("Location")))
		}
		return "", fmt.Errorf("status %d", se.Status)
	}
	if errors.Is(err, httpx.ErrTooLarge) {
		return "", fmt.Errorf("attachment exceeds the %d byte limit", maxAttachmentBytes)
	}
	if err != nil {
		return "", err
	}
	return ct, nil
}

// redirectTarget describes where a download was being sent, by scheme and
// host only.
func redirectTarget(location string) string {
	loc, err := url.Parse(location)
	if err != nil || loc == nil || location == "" {
		return "an unnamed location"
	}
	if loc.Host == "" {
		return loc.Path
	}
	return loc.Scheme + "://" + loc.Host
}
