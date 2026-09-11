package zohodesk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
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

// trust reports which hosts a credentialed request may be sent to. Every
// request this client makes carries the org id and a live Desk access
// token, and attachment hrefs and inline <img src> values arrive inside
// ticket HTML a customer wrote. Without this gate, one <img
// src="https://attacker.example/x"> in a ticket is a Zoho access token
// delivered to the attacker — and a 401 from them would have been answered
// with a freshly minted one.
//
// Trusted is: the configured Desk endpoint itself, at the scheme it was
// configured with; or an https host in the same Zoho data centre, meaning a
// subdomain of zoho, zohostatic or zohopublic under the TLD baseUrl uses.
//
// It is cached against the BaseURL it was built from, since BaseURL is an
// exported field a caller can still change.
func (c *Client) trust() *httpx.Trust {
	base := strings.TrimSuffix(c.BaseURL, "/")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hostTrust != nil && c.trustBase == base {
		return c.hostTrust
	}
	// A base URL that will not parse trusts nothing, which is what a nil
	// Trust answers.
	t, err := httpx.NewTrust(base, zohoRules(base)...)
	if err != nil {
		t = nil
	}
	c.hostTrust, c.trustBase = t, base
	return t
}

// zohoRules turns a base URL into the sibling host rules for its data
// centre. They carry the credential: they are the same workspace.
func zohoRules(base string) []httpx.HostRule {
	u, err := url.Parse(base)
	if err != nil {
		return nil
	}
	var rules []httpx.HostRule
	for _, suffix := range zohoSuffixes(httpx.NormalizeHost(u.Scheme, u.Host)) {
		rules = append(rules, httpx.HostRule{Suffix: suffix, SendCredential: true})
	}
	return rules
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
	// Whatever an earlier call in the same bundle (Get, Threads) recorded
	// for this ticket stays where it is: the caller reads WarningsFor once
	// after all three, and reading is what clears the entry.
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
		name = httpx.SanitizeName(name)
		filename := fmt.Sprintf("%d-%s", idx, name)

		if fetch, _, _ := c.trust().CheckRaw(r.URL); !fetch {
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

// putWarnings records the failures one Attachments call skipped over,
// alongside whatever an earlier call for the same ticket recorded: one
// ticket's bundle is Get, then Threads, then Attachments, with a single
// WarningsFor at the end, and reading is what clears the entry (see
// httpx.Warnings).
func (c *Client) putWarnings(id string, warnings []string) {
	c.warnings.Add(id, warnings...)
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
	return c.warnings.Take(id)
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

// downloadAttachment fetches url with the client's auth headers and writes
// its body to destPath, returning the response's Content-Type. The caller
// has already checked url against the client's trust; every redirect off it
// is checked again by the redirect policy, so a trusted host cannot bounce
// the credentialed request onto an untrusted one.
func (c *Client) downloadAttachment(ctx context.Context, rawURL, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}

	// sendWith, not httpx.Download: a 401 from the Desk endpoint buys one
	// fresh token and a replay, which needs the response in hand.
	resp, err := c.sendWith(ctx, req, httpx.Client(c.http(), c.trust(), maxRedirects))
	if err != nil {
		// Only the host: Go's *url.Error carries the refused target's path
		// and query, and this error becomes a per-ticket warning.
		if host, ok := httpx.RedirectHost(err); ok {
			return "", fmt.Errorf("redirected to an untrusted host: %s", host)
		}
		return "", err
	}
	ct, err := httpx.Save(resp, destPath, httpx.DownloadOptions{Max: maxAttachmentBytes})
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return "", fmt.Errorf("status %d", se.Status)
	}
	if errors.Is(err, httpx.ErrTooLarge) {
		return "", fmt.Errorf("larger than the %d byte limit", int64(maxAttachmentBytes))
	}
	if err != nil {
		return "", err
	}
	return ct, nil
}
