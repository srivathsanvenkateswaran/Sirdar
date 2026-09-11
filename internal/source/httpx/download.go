package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ErrTooLarge is returned when a body is bigger than the ceiling it was
// read under. It is an error rather than a truncation on purpose: a short
// but well-formed JSON document decodes without complaint, and a truncated
// attachment on disk looks like the file.
var ErrTooLarge = errors.New("body too large")

// ErrHTMLBody is returned when a text/html body arrives for a request that
// asked for a file. That is the SSO sign-in page an expired or unaccepted
// credential gets instead of the attachment, and writing it to disk under
// the attachment's name is how a login form ends up in an evidence bundle.
var ErrHTMLBody = errors.New("html body where a file was expected")

// StatusError reports a non-2xx response. The body snippet is capped, and
// callers map Status onto their own error codes. Header is the response's,
// so a caller can name a redirect's target (by scheme and host, never its
// query) without the response itself.
type StatusError struct {
	Status int
	Body   []byte
	Header http.Header
}

func (e *StatusError) Error() string { return fmt.Sprintf("status %d", e.Status) }

// maxStatusBody is how much of a failed response is kept for the caller's
// error message.
const maxStatusBody = 8 << 10

// ReadLimited reads at most max bytes and fails with ErrTooLarge when the
// reader had more to give.
func ReadLimited(r io.Reader, max int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return body, err
	}
	if int64(len(body)) > max {
		return body[:max], fmt.Errorf("%w: response exceeds the %d byte limit", ErrTooLarge, max)
	}
	return body, nil
}

// DownloadOptions bounds one download.
type DownloadOptions struct {
	// Max is the byte ceiling. A response past it fails with ErrTooLarge
	// and leaves nothing on disk. Zero or negative means no ceiling.
	Max int64
	// RefuseHTML fails the download with ErrHTMLBody when the response is
	// text/html, for a caller that expected a binary.
	RefuseHTML bool
}

// Download issues req through hc and streams the body to dest, returning
// the served Content-Type.
//
// The body goes to a temporary file in dest's directory and is renamed into
// place only once it has arrived whole, so a failure — a transport error, a
// body past the ceiling, a sign-in page — never leaves a partial file behind
// under the name of the real thing.
func Download(ctx context.Context, hc *http.Client, req *http.Request, dest string, opts DownloadOptions) (contentType string, err error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	if req == nil {
		return "", errors.New("httpx: nil request")
	}
	req = req.WithContext(ctx)

	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	return Save(resp, dest, opts)
}

// Save is Download's second half, for a caller that has to issue the request
// itself — an adapter whose auth needs the response in hand, to mint a new
// token and replay a 401, say. It consumes and closes resp.
func Save(resp *http.Response, dest string, opts DownloadOptions) (contentType string, err error) {
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxStatusBody))
		resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := ReadLimited(resp.Body, maxStatusBody)
		return "", &StatusError{Status: resp.StatusCode, Body: body, Header: resp.Header}
	}
	ct := resp.Header.Get("Content-Type")
	if opts.RefuseHTML && IsHTML(ct) {
		return ct, fmt.Errorf("%w: server answered with %s instead of the file: the request was probably redirected to a login page", ErrHTMLBody, ct)
	}

	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dest)+".part*")
	if err != nil {
		return ct, err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	var body io.Reader = resp.Body
	if opts.Max > 0 {
		body = io.LimitReader(resp.Body, opts.Max+1)
	}
	n, copyErr := io.Copy(tmp, body)
	closeErr := tmp.Close()
	switch {
	case copyErr != nil:
		return ct, copyErr
	case closeErr != nil:
		return ct, closeErr
	case opts.Max > 0 && n > opts.Max:
		return ct, fmt.Errorf("%w: exceeds the %d byte limit", ErrTooLarge, opts.Max)
	}
	if err = os.Rename(tmpName, dest); err != nil {
		return ct, err
	}
	return ct, nil
}

// IsHTML reports whether a Content-Type names an HTML document.
func IsHTML(contentType string) bool {
	if contentType == "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	return mt == "text/html" || mt == "application/xhtml+xml"
}
