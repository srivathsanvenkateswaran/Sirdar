package httpx

import (
	"context"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// MaxRetryAfter is the longest 429 wait an adapter sits out inline. A
// longer one is reported to the caller, which knows about the run's budget
// and the adapter does not.
const MaxRetryAfter = 30 * time.Second

// RetryAfter reads a Retry-After header in either of its documented forms
// (delta-seconds or an HTTP date) and reports whether the wait is one worth
// sitting through: an absent, negative or unparseable value is refused, and
// so is a wait longer than max. A date already in the past is zero, not a
// refusal — the server is saying "now".
func RetryAfter(h http.Header, max time.Duration) (time.Duration, bool) {
	v := textproto.TrimString(h.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		if secs < 0 {
			return 0, false
		}
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
		if d < 0 {
			d = 0
		}
	} else {
		return 0, false
	}
	if d > max {
		return 0, false
	}
	return d, true
}

// SleepCtx waits for d, or until ctx is done. A non-positive d reports
// ctx's own state rather than sleeping, so a caller cannot spin on a
// cancelled context.
func SleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
