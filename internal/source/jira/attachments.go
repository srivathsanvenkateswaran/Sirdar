package jira

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// maxRedirects bounds how far a same-host redirect chain is followed before
// the download is abandoned.
const maxRedirects = 3

// maxAttachmentBytes caps one download. Past it the file is refused rather
// than written: an attachment nobody can vouch for should not be able to
// fill the disk the run is using.
const maxAttachmentBytes = 64 << 20

// Attachments implements source.Helpdesk: it downloads every file attached to
// the issue into dir, named "<1-based index>-<sanitised filename>".
//
// A file that will not download is skipped and reported through Warnings
// rather than failing the call, because a bundle missing one screenshot is
// still worth triaging. Only when every download fails does Attachments
// return an error.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	ctx, col := withCollector(ctx)
	published := false
	// Every path out of here has to file this call's warnings under the
	// ticket, including the paths that return early. publish appends, so
	// what an earlier call in the same bundle (Get, Threads) recorded stays
	// where it is.
	defer func() {
		if !published {
			c.publish(id, col)
		}
	}()

	iss, err := c.fetchIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	atts := iss.Fields.Attachment
	if len(atts) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("jira: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var (
		out      []ticket.Attachment
		failures []error
	)
	for i, a := range atts {
		if a.Content == "" {
			warnCtx(ctx, "jira: attachment %s (%s) has no content URL", a.ID, a.Filename)
			failures = append(failures, fmt.Errorf("attachment %s: no content URL", a.ID))
			continue
		}
		// Checked before the request is built, so an attachment pointed at
		// somebody else's host costs no round trip and, more to the point,
		// never sees the credential. The warning names the host and not the
		// URL: the rest of it is attacker-chosen text headed for a log.
		if fetch, _, _ := c.trust.CheckRaw(a.Content); !fetch {
			host := httpx.HostOf(a.Content)
			warnCtx(ctx, "jira: attachment host not trusted: %s", host)
			failures = append(failures, fmt.Errorf("attachment %s: host not trusted: %s", a.ID, host))
			continue
		}
		name := httpx.SanitizeName(a.Filename)
		if name == httpx.FallbackName && a.ID != "" {
			name = httpx.SanitizeName(a.ID)
		}
		filename := fmt.Sprintf("%d-%s", i+1, name)

		ct, derr := c.download(ctx, a.Content, filepath.Join(dir, filename))
		if derr != nil {
			warnCtx(ctx, "jira: download attachment %s (%s): %v", a.ID, a.Filename, derr)
			failures = append(failures, fmt.Errorf("attachment %s: %w", a.ID, derr))
			continue
		}
		out = append(out, ticket.Attachment{
			ID:   a.ID,
			Name: name,
			MIME: pickMIME(a.MimeType, ct),
			Path: base + "/" + filename,
		})
	}

	if len(out) == 0 {
		// Every download failed, so the caller gets all of them in the
		// error and needs no warning saying the same thing a second time.
		// Dropping this call's messages leaves whatever Get and Threads
		// recorded for the same ticket untouched.
		published = true
		col.take()
		return nil, errors.Join(failures...)
	}
	return out, nil
}

// pickMIME prefers the type Jira recorded for the attachment over the one the
// download response advertised; a proxy or CDN in front of Data Center often
// serves everything as octet-stream.
func pickMIME(declared, served string) string {
	if declared != "" {
		return declared
	}
	return served
}

// download fetches one attachment into destPath and returns the response's
// Content-Type. It refuses anything that looks like a login page: a redirect
// off the Jira host, or an HTML body where a binary was expected.
func (c *Client) download(ctx context.Context, rawURL, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	c.setHeaders(req)
	// Attachment downloads go through Jira's XSRF check, which rejects the
	// request outright without this header.
	req.Header.Set("X-Atlassian-Token", "no-check")
	req.Header.Set("Accept", "*/*")

	// Redirects stop the moment they leave the Jira host. On Data Center
	// the attachment URL is served by the web layer rather than the REST
	// layer, and a bearer PAT is not always accepted there: the request is
	// answered with a redirect to the SSO login page instead of the file,
	// and following that would write an HTML login form to disk under the
	// attachment's name. An untrusted hop stops the chain rather than
	// failing it, so the 3xx comes back here and the warning can name where
	// the request was being sent without quoting the SSO query string.
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
	if err != nil {
		return "", err
	}
	return ct, nil
}

// redirectTarget describes where a download was being sent, by scheme and
// host only: the query string of an SSO redirect carries the original URL
// and sometimes a token, neither of which belongs in a warning.
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
