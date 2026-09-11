package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// postTimeout is what one POST gets, request to response body. A run that
// has already written its note is waiting on this, so it is short.
const postTimeout = 10 * time.Second

// maxRetryAfter is the longest Retry-After Sirdar will wait out. A webhook
// that asks for more than half a minute is asking for longer than a
// finished run should be held open; the post is given up on instead.
const maxRetryAfter = 30 * time.Second

// defaultRetryAfter is the pause before the one retry when the response
// named no Retry-After.
const defaultRetryAfter = time.Second

// maxErrorBody is how much of a failed response is quoted back.
const maxErrorBody = 256

// transport is the HTTP behaviour every destination shares: one attempt,
// then at most one retry when the receiver said it was busy.
type transport struct {
	client  *http.Client
	timeout time.Duration
}

func (t transport) httpClient() *http.Client {
	if t.client != nil {
		return t.client
	}
	return http.DefaultClient
}

func (t transport) limit() time.Duration {
	if t.timeout > 0 {
		return t.timeout
	}
	return postTimeout
}

// post sends body and retries once when the receiver answered 429 or 5xx
// with a Retry-After Sirdar is willing to wait out.
func (t transport) post(ctx context.Context, dest string, body []byte, headers map[string]string) error {
	wait, err := t.attempt(ctx, dest, body, headers)
	if err == nil || wait < 0 {
		return err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return err
	case <-timer.C:
	}
	_, retryErr := t.attempt(ctx, dest, body, headers)
	return retryErr
}

// attempt makes one POST. The duration it returns is how long to wait
// before retrying, or -1 when the outcome is final — a success, or a
// failure retrying will not mend.
func (t transport) attempt(ctx context.Context, dest string, body []byte, headers map[string]string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, t.limit())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest, bytes.NewReader(body))
	if err != nil {
		return -1, fmt.Errorf("notify: %s is not a usable webhook URL", redact(dest))
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := t.httpClient().Do(req)
	if err != nil {
		return -1, fmt.Errorf("notify: post to %s: %s", redact(dest), reason(err))
	}
	defer resp.Body.Close()
	snippet := snippetOf(resp.Body)

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return -1, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		failed := fmt.Errorf("notify: %s answered %d%s", redact(dest), resp.StatusCode, snippet)
		wait, ok := retryAfter(resp.Header.Get("Retry-After"))
		if !ok {
			return -1, failed
		}
		return wait, failed
	default:
		return -1, fmt.Errorf("notify: %s answered %d%s", redact(dest), resp.StatusCode, snippet)
	}
}

// reason is what went wrong on the wire, with the URL taken back out:
// net/http wraps every transport failure in a *url.Error that quotes the
// whole URL, and for an incoming webhook that URL is the credential.
func reason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return err.Error()
}

// retryAfter reads a Retry-After header. A missing header means retry after
// the default pause; a delay longer than maxRetryAfter, or one that makes
// no sense, means do not retry at all.
func retryAfter(header string) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return defaultRetryAfter, true
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0, false
		}
		return within(time.Duration(secs) * time.Second)
	}
	if at, err := http.ParseTime(header); err == nil {
		return within(time.Until(at))
	}
	return 0, false
}

func within(d time.Duration) (time.Duration, bool) {
	if d > maxRetryAfter {
		return 0, false
	}
	if d < 0 {
		d = 0
	}
	return d, true
}

// snippetOf quotes the start of a failed response, which is where a
// webhook says what it disliked ("invalid_payload", "no_service").
func snippetOf(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, maxErrorBody))
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return ""
	}
	return ": " + strings.TrimSpace(strings.ReplaceAll(string(data), "\n", " "))
}
