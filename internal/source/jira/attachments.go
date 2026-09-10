package jira

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// maxRedirects bounds how far a same-host redirect chain is followed before
// the download is abandoned.
const maxRedirects = 5

// normalizeHost renders a URL host comparable: lowercased, with the DNS root's
// trailing dot removed and the scheme's default port dropped, so
// "JIRA.Example.com.:443" and "jira.example.com" are recognised as one host.
func normalizeHost(scheme, host string) string {
	host = strings.ToLower(strings.TrimSpace(host))

	name, port, err := net.SplitHostPort(host)
	if err != nil {
		// No port at all, or a bare IPv6 literal.
		return strings.TrimSuffix(host, ".")
	}
	name = strings.TrimSuffix(name, ".")
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return name
	}
	return net.JoinHostPort(name, port)
}

// trustedURL reports whether u may be fetched with this client's credential,
// and returns the host it named so a warning can say where the request would
// have gone.
//
// An attachment's content URL arrives inside an API response body, which makes
// it input rather than configuration: nothing in the protocol stops a hostile
// or compromised instance from pointing one at a host it controls, and sending
// the Authorization header there would hand over the API token or PAT in full.
// So the credential only ever leaves for the host the operator configured, and
// that is checked before the request is built — not only on the redirects that
// follow it.
func (c *Client) trustedURL(u *url.URL) (string, bool) {
	if u == nil || u.Host == "" {
		return "(no host)", false
	}
	// "https://jira.example.com@attacker.example/x" parses with the real
	// destination in Host and the decoy in User. The host comparison below
	// already catches that; userinfo has no business on an attachment URL
	// either way.
	if u.User != nil {
		return u.Host, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != c.scheme {
		return u.Host, false
	}
	if normalizeHost(scheme, u.Host) != c.host {
		return u.Host, false
	}
	return u.Host, true
}

// trustedRawURL is trustedURL for a URL still in string form.
func (c *Client) trustedRawURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "(unparseable)", false
	}
	return c.trustedURL(u)
}

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
	// Every path out of here must leave the right warnings behind for this
	// ticket and no stale ones from an earlier call, including the paths
	// that return early.
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
		if host, ok := c.trustedRawURL(a.Content); !ok {
			warnCtx(ctx, "jira: attachment host not trusted: %s", host)
			failures = append(failures, fmt.Errorf("attachment %s: host not trusted: %s", a.ID, host))
			continue
		}
		name := sanitizeName(a.Filename)
		if name == "attachment" && a.ID != "" {
			name = sanitizeName(a.ID)
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
		// Every download failed, so the caller gets all of them in the error
		// and needs no warning saying the same thing a second time. Filing
		// nothing under id still clears whatever an earlier call left there.
		published = true
		col.take()
		c.publish(id, col)
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

// downloadClient returns a copy of the HTTP client that stops following
// redirects the moment they leave the Jira host. On Data Center the
// attachment URL is served by the web layer rather than the REST layer, and
// a bearer PAT is not always accepted there: the request is answered with a
// redirect to the SSO login page instead of the file. Following that would
// write an HTML login form to disk under the attachment's name.
func (c *Client) downloadClient() *http.Client {
	dl := *c.hc
	dl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Same trust rule as the initial request, so a redirect cannot walk
		// the credential off the configured host either. Go re-sends the
		// Authorization header only on a same-host hop, but the check does
		// not rely on that.
		if _, ok := c.trustedURL(req.URL); !ok {
			return http.ErrUseLastResponse
		}
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		return nil
	}
	return &dl
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

	resp, err := c.downloadClient().Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return "", fmt.Errorf("redirected to %s: the credential was not accepted for the attachment URL (SSO login page?)", redirectTarget(resp))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if isHTML(ct) {
		return "", fmt.Errorf("server answered with %s instead of the file: the request was probably redirected to a login page", ct)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return ct, nil
}

// redirectTarget describes where a download was being sent, by scheme and
// host only: the query string of an SSO redirect carries the original URL
// and sometimes a token, neither of which belongs in a warning.
func redirectTarget(resp *http.Response) string {
	loc, err := resp.Location()
	if err != nil || loc == nil {
		return "an unnamed location"
	}
	if loc.Host == "" {
		return loc.Path
	}
	return loc.Scheme + "://" + loc.Host
}

func isHTML(contentType string) bool {
	if contentType == "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	return mt == "text/html" || mt == "application/xhtml+xml"
}

// sanitizeName turns an attachment filename from the API into a safe filename
// component: it strips any directory portion (so a name like "../../evil.txt"
// cannot write outside the destination dir), drops path separators and
// control characters, falls back to "attachment" for an empty/"."/".."
// result, and caps the result at 120 bytes while preserving the extension.
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
