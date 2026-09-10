package zohodesk

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
	"regexp"
	"strings"
	"unicode/utf8"

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

// sanitizeName turns an attachment name (or ID) taken from the API response
// into a safe filename component: it strips any directory portion (so a
// name like "../../evil.txt" cannot write outside the destination dir),
// drops path separators and control characters, falls back to "attachment"
// for an empty/"."/".." result, and caps the result at 120 bytes while
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

// resolveURL turns a possibly-relative href/src from a Zoho Desk payload
// into an absolute URL against BaseURL. An absolute href is kept as it is
// and judged by trustedURL before anything is sent to it: these values
// come out of customer-authored HTML, so the host in one is an input, not
// a fact.
func (c *Client) resolveURL(href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if !strings.HasPrefix(href, "/") {
		href = "/" + href
	}
	return c.BaseURL + href
}

// trustedURL reports whether a credentialed request may be sent to raw.
// Every request this client makes carries the org id and a live Desk
// access token, and attachment hrefs and inline <img src> values arrive
// inside ticket HTML a customer wrote. Without this gate, one <img
// src="https://attacker.example/x"> in a ticket is a Zoho access token
// delivered to the attacker — and a 401 from them would have been
// answered with a freshly minted one.
//
// Trusted is: the configured Desk endpoint itself, at the scheme it was
// configured with; or an https host in the same Zoho data centre, meaning
// a subdomain of zoho, zohostatic or zohopublic under the TLD baseUrl
// uses.
func (c *Client) trustedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	base, err := url.Parse(strings.TrimSuffix(c.BaseURL, "/"))
	if err != nil || base.Host == "" {
		return false
	}
	if sameEndpoint(u, base) {
		return true
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	host := normalizeHost(u)
	for _, suffix := range zohoSuffixes(normalizeHost(base)) {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// sameEndpoint reports whether two URLs name the same scheme and host.
func sameEndpoint(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && normalizeHost(a) == normalizeHost(b)
}

// normalizeHost lowercases a URL's host, drops a trailing dot on the name,
// and drops the port when it is the default for the scheme, so
// "DESK.Zoho.in.:443" and "desk.zoho.in" compare equal.
func normalizeHost(u *url.URL) string {
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	port := u.Port()
	switch {
	case port == "",
		port == "443" && strings.EqualFold(u.Scheme, "https"),
		port == "80" && strings.EqualFold(u.Scheme, "http"):
		return host
	}
	return host + ":" + port
}

// zohoSuffixes returns the host suffixes that belong to the same Zoho data
// centre as baseHost — ".zoho.in", ".zohostatic.in", ".zohopublic.in" for
// a desk.zoho.in workspace. A base host that is not a Zoho one (a test
// server) has no siblings: only itself is trusted.
func zohoSuffixes(baseHost string) []string {
	i := strings.Index(baseHost, ".zoho.")
	if i < 0 {
		return nil
	}
	tld := baseHost[i+len(".zoho."):]
	if tld == "" {
		return nil
	}
	return []string{".zoho." + tld, ".zohostatic." + tld, ".zohopublic." + tld}
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
// skipped, reported through Warnings, and the remaining attachments are
// still returned. Only when every attachment fails to download does
// Attachments return a non-nil error (via errors.Join of the individual
// failures) — and in that case the failures are not also recorded as
// warnings, since the caller already has every one of them in the error.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	// Discard anything an earlier call for this ticket left behind before
	// doing anything else: every path out of here from this point on,
	// including the ones that return early, must leave no stale warning
	// for the next caller to pick up as its own.
	c.takeWarnings(id)

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
		name = sanitizeName(name)
		filename := fmt.Sprintf("%d-%s", idx, name)

		if !c.trustedURL(r.URL) {
			// Only the host is reported: the rest of the URL is
			// attacker-authored and has no business in a log line.
			warnings = append(warnings, "zoho desk: attachment host not trusted: "+hostOf(r.URL))
			continue
		}

		mime, derr := c.downloadAttachment(ctx, r.URL, filepath.Join(dir, filename))
		if derr != nil {
			warnings = append(warnings, fmt.Sprintf("zoho desk: download attachment %s: %v", r.ID, derr))
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

// putWarnings records the failures one Attachments call skipped over.
func (c *Client) putWarnings(id string, warnings []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(warnings) == 0 {
		delete(c.warnings, id)
		return
	}
	if c.warnings == nil {
		c.warnings = map[string][]string{}
	}
	c.warnings[id] = warnings
}

// takeWarnings returns and removes the warnings recorded for ticket id.
func (c *Client) takeWarnings(id string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.warnings[id]
	delete(c.warnings, id)
	if len(w) == 0 {
		return nil
	}
	return append([]string(nil), w...)
}

var (
	_ source.Helpdesk = (*Client)(nil)
	_ source.Warner   = (*Client)(nil)
)

// WarningsFor implements source.Warner: it returns and consumes the
// per-attachment failures the Attachments call for ticket id recorded, so
// the caller can put them in the prompt and the run state instead of
// silently serving a short list of attachments. It is keyed by id, which is
// what a caller running several tickets at once needs: it cannot be handed
// another ticket's missing evidence.
func (c *Client) WarningsFor(id string) []string {
	return c.takeWarnings(id)
}

// hostOf returns a URL's host for a log line, or "" when it does not parse.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

const (
	// maxAttachmentBytes caps one download. Past it the file is refused
	// rather than written: an attachment nobody can vouch for should not
	// be able to fill the disk the run is using.
	maxAttachmentBytes = 64 << 20
	// maxRedirects is how many hops a download may take. Each one is
	// re-checked against trustedURL, so a trusted host cannot bounce the
	// credentialed request onto an untrusted one.
	maxRedirects = 3
)

// downloadClient is the HTTP client attachment downloads use: the
// configured one, with a redirect policy that re-applies the trust gate on
// every hop and stops after maxRedirects.
func (c *Client) downloadClient() *http.Client {
	dl := *c.http()
	dl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		if !c.trustedURL(req.URL.String()) {
			return fmt.Errorf("redirected to an untrusted host: %s", req.URL.Host)
		}
		return nil
	}
	return &dl
}

// downloadAttachment fetches url with the client's auth headers and writes
// its body to destPath, returning the response's Content-Type. The caller
// has already checked url against trustedURL; every redirect off it is
// checked again here.
func (c *Client) downloadAttachment(ctx context.Context, rawURL, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.sendWith(ctx, req, c.downloadClient())
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

	written, err := io.Copy(f, io.LimitReader(resp.Body, maxAttachmentBytes+1))
	if err != nil {
		return "", err
	}
	if written > maxAttachmentBytes {
		os.Remove(destPath)
		return "", fmt.Errorf("larger than the %d byte limit", int64(maxAttachmentBytes))
	}
	return resp.Header.Get("Content-Type"), nil
}
