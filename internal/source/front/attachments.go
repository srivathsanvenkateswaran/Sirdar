package front

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// attachmentRef is a resolved reference to one downloadable attachment.
type attachmentRef struct {
	ID   string
	Name string
	MIME string
	URL  string
}

// downloadURL is where an attachment's bytes are fetched from: the link the
// API gave, when it gave one, resolved against the API base URL if it came
// back relative; otherwise the documented download path built from the
// attachment id this client already holds. A link taken out of a response
// body is checked against trust.Check before it is used, same as any other.
func (c *Client) downloadURL(a frAttachment, id string) string {
	href := strings.TrimSpace(a.URL)
	switch {
	case href == "":
	case strings.HasPrefix(href, "/"):
		return c.baseURL + href
	default:
		return href
	}
	return c.baseURL + "/download/" + url.PathEscape(id)
}

// downloadTo fetches rawURL and streams the body straight to destPath,
// stopping at maxAttachmentBytes. A file that would exceed the limit is a
// download failure and its partial output is removed, so a truncated
// attachment is never left on disk looking complete.
//
// Front's download endpoint is authenticated like every other call — there
// is no pre-signed tier here — so the bearer token goes with the request,
// which is why trust grants the credential to the hosts it trusts at all.
func (c *Client) downloadTo(ctx context.Context, rawURL, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: %v", logPath(rawURL), err)}
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Accept", "*/*")

	_, err = httpx.Download(ctx, c.hc, req, destPath, httpx.DownloadOptions{Max: maxAttachmentBytes})
	if host, ok := httpx.RedirectHost(err); ok {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: redirect to untrusted host %s", logPath(rawURL), host)}
	}
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return statusError(rawURL, se.Status, se.Body)
	}
	if errors.Is(err, httpx.ErrTooLarge) {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: attachment exceeds the %d byte limit", logPath(rawURL), maxAttachmentBytes)}
	}
	if err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: GET %s: %v", logPath(rawURL), err)}
	}
	return nil
}

// collectRefs gathers every attachment on the merged thread, in thread
// order, so an attachment's position here is the position Threads already
// named in a message's AttachmentIDs.
func (c *Client) collectRefs(items []entry) []attachmentRef {
	var refs []attachmentRef
	for _, e := range items {
		for i, a := range e.atts {
			id := attachmentID(e.id, a, i)
			name := strings.TrimSpace(a.Filename)
			if name == "" {
				name = id
			}
			refs = append(refs, attachmentRef{
				ID:   id,
				Name: name,
				MIME: a.ContentType,
				URL:  c.downloadURL(a, id),
			})
		}
	}
	return refs
}

// Attachments downloads every attachment on a conversation's messages and
// comments into dir, named "<1-based index>-<sanitised name>" in thread
// order.
//
// A download skipped for an untrusted host, or one that otherwise fails, is
// not fatal: it is recorded through WarningsFor and the rest are still
// returned. Only when every attachment fails does Attachments return a
// non-nil error (errors.Join of the individual failures), and then the
// failures are not also recorded as warnings, since the caller already has
// every one of them in the error.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	items, pagingWarnings, err := c.entries(ctx, id)
	if err != nil {
		return nil, err
	}
	refs := c.collectRefs(items)
	if len(refs) == 0 {
		c.addWarnings(id, pagingWarnings)
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("front: mkdir %s: %v", dir, err)}
	}
	base := filepath.Base(dir)

	var out []ticket.Attachment
	warnings := append([]string(nil), pagingWarnings...)
	for i, r := range refs {
		u, perr := url.Parse(r.URL)
		if perr != nil || u.Host == "" {
			warnings = append(warnings, fmt.Sprintf("front: attachment %s: invalid url", r.ID))
			continue
		}
		if fetch, _, _ := c.trust.Check(u); !fetch {
			warnings = append(warnings, fmt.Sprintf("front: attachment host not trusted: %s", httpx.HostOf(r.URL)))
			continue
		}

		name := httpx.SanitizeName(r.Name)
		filename := fmt.Sprintf("%d-%s", i+1, name)

		if derr := c.downloadTo(ctx, r.URL, filepath.Join(dir, filename)); derr != nil {
			warnings = append(warnings, fmt.Sprintf("front: download attachment %s: %v", r.ID, derr))
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
