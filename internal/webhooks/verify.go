package webhooks

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SecretHeader carries the shared secret for the sources that sign
// nothing: Jira Cloud webhooks and Jira Automation, Rally, Freshdesk, and
// the generic endpoint. The operator sets the same value on both ends, and
// the header is the whole of the proof — which is why those sources are
// only safe behind TLS.
const SecretHeader = "X-Sirdar-Secret"

// equalSecret compares two secrets without leaking where they diverge.
func equalSecret(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// verifySharedSecret checks the SecretHeader against the configured value.
func verifySharedSecret(r *http.Request, secret string) error {
	if secret == "" || !equalSecret(r.Header.Get(SecretHeader), secret) {
		return ErrUnauthorized
	}
	return nil
}

// verifyBasic checks HTTP basic auth, which is what an Azure DevOps
// service hook offers instead of a signature.
func verifyBasic(r *http.Request, username, password string) error {
	gotUser, gotPass, ok := r.BasicAuth()
	if !ok || password == "" {
		return ErrUnauthorized
	}
	// Both halves are always compared, so the reply takes the same time
	// whichever of them is wrong.
	user := equalSecret(gotUser, username)
	pass := equalSecret(gotPass, password)
	if !user || !pass {
		return ErrUnauthorized
	}
	return nil
}

// hmacSHA256 and hmacSHA1 sign a message with the source's secret.
func hmacSHA256(secret string, msg []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(msg)
	return m.Sum(nil)
}

func hmacSHA1(secret string, msg []byte) []byte {
	m := hmac.New(sha1.New, []byte(secret))
	m.Write(msg)
	return m.Sum(nil)
}

// HexHMACSHA256 is the signature Linear sends, exported so a test — or an
// operator debugging a delivery — can compute the expected value.
func HexHMACSHA256(secret string, msg []byte) string {
	return hex.EncodeToString(hmacSHA256(secret, msg))
}

// HexHMACSHA1 is the signature Intercom sends, without its "sha1=" prefix.
func HexHMACSHA1(secret string, msg []byte) string {
	return hex.EncodeToString(hmacSHA1(secret, msg))
}

// Base64HMACSHA256 is the signature Zendesk and HubSpot send.
func Base64HMACSHA256(secret string, msg []byte) string {
	return base64.StdEncoding.EncodeToString(hmacSHA256(secret, msg))
}

// equalSig compares a received signature with the expected one. Hex is
// compared case-insensitively because the vendors disagree about case;
// base64 is not, because base64 case is meaningful.
func equalSig(got, want string, hexEncoded bool) bool {
	got = strings.TrimSpace(got)
	if hexEncoded {
		got, want = strings.ToLower(got), strings.ToLower(want)
	}
	return equalSecret(got, want)
}

// withinWindow reports whether t is close enough to now to be a live
// delivery rather than a replayed one. A delivery from the future is
// rejected on the same window, which covers a receiver whose clock has
// drifted behind the sender's.
func withinWindow(now, t time.Time, window time.Duration) bool {
	if t.IsZero() {
		return false
	}
	d := now.Sub(t)
	if d < 0 {
		d = -d
	}
	return d <= window
}

// parseEpochMillis reads the millisecond timestamp HubSpot sends.
func parseEpochMillis(s string) time.Time {
	ms, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// clock is the time source a verifier with a replay window reads. Every
// such verifier carries one so a test can put the window under its own
// control rather than against the wall clock.
type clock struct {
	Now func() time.Time
}

func (c clock) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
