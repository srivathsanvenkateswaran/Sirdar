package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// SignatureHeader carries the HMAC-SHA256 of the timestamp and the request
// body, as "sha256=" followed by the digest in lowercase hex, when the hook
// configures a shared secret.
const SignatureHeader = "X-Sirdar-Signature"

// TimestampHeader carries the unix time, in seconds, at which the body was
// signed. Without it a signature is valid forever, so anyone who ever saw
// one request — a logging proxy, a receiver's own history — could replay it
// indefinitely; a receiver checks this against its own clock before
// trusting the signature.
const TimestampHeader = "X-Sirdar-Timestamp"

// Generic posts the Event itself, as JSON, to any receiver: a Discord or
// Google Chat relay, an internal service, a queue's HTTP front door.
// Headers are sent as given — a value that was a credential reference has
// already been resolved — and Secret, when set, signs the body.
type Generic struct {
	URL     string
	Headers map[string]string
	Secret  string
	Client  *http.Client

	timeout time.Duration
}

func (g *Generic) Notify(ctx context.Context, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("notify: generic: build payload: %w", err)
	}
	headers := make(map[string]string, len(g.Headers)+2)
	for name, value := range g.Headers {
		headers[name] = value
	}
	if g.Secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		headers[TimestampHeader] = ts
		headers[SignatureHeader] = Sign(g.Secret, ts, body)
	}
	return transport{client: g.Client, timeout: g.timeout}.post(ctx, g.URL, body, headers)
}

// Sign is the value of the signature header for a timestamp and a body:
// HMAC-SHA256 over ts + "." + body, the same construction GitHub and Stripe
// use for their own timestamped signatures, so a receiver can verify it
// with hmac.Equal against its own digest rather than comparing strings.
func Sign(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
