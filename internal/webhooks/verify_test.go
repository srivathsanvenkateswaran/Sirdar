package webhooks

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fixture reads one recorded payload. The bodies are the bytes the
// signatures below are computed over, which is the point: a verifier that
// re-encoded the JSON would stop matching them.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

// at2026 is the instant every replay-window test is anchored to, so the
// windows are exercised against a clock the test owns.
var at2026 = time.Date(2026, 9, 11, 9, 15, 0, 0, time.UTC)

func fixedClock() clock { return clock{Now: func() time.Time { return at2026 }} }

// post builds a request the way a delivery arrives: body already read by
// the receiver, headers as the vendor sends them.
func post(body []byte, headers map[string]string) *http.Request {
	r := httptest.NewRequest("POST", "https://sirdar.acme.com/hooks/ws1/x", strings.NewReader(string(body)))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func assertVerified(t *testing.T, v Verifier, r *http.Request, body []byte) {
	t.Helper()
	if err := v.Verify(r, body); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func assertRejected(t *testing.T, v Verifier, r *http.Request, body []byte) {
	t.Helper()
	err := v.Verify(r, body)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("verify: got %v, want ErrUnauthorized", err)
	}
}

// --- shared secret ----------------------------------------------------

func TestSharedSecretSourcesVerify(t *testing.T) {
	body := []byte(`{"key":"OMNI-1"}`)
	for _, tc := range []struct {
		name string
		v    Verifier
	}{
		{"jira", Jira{Secret: "s3cret"}},
		{"rally", Rally{Secret: "s3cret"}},
		{"freshdesk", Freshdesk{Secret: "s3cret"}},
		{"generic", Generic{Secret: "s3cret"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertVerified(t, tc.v, post(body, map[string]string{SecretHeader: "s3cret"}), body)
			assertRejected(t, tc.v, post(body, map[string]string{SecretHeader: "wrong"}), body)
			assertRejected(t, tc.v, post(body, nil), body)
		})
	}
}

// An empty configured secret must never let an empty header through: that
// is the failure mode where a workspace with a missing credential accepts
// every anonymous POST.
func TestSharedSecretRejectsEmptyConfiguredSecret(t *testing.T) {
	body := []byte(`{"key":"OMNI-1"}`)
	assertRejected(t, Generic{Secret: ""}, post(body, map[string]string{SecretHeader: ""}), body)
}

// --- Linear -----------------------------------------------------------

func linearRequest(t *testing.T, secret string, body []byte) *http.Request {
	t.Helper()
	return post(body, map[string]string{LinearSignatureHeader: HexHMACSHA256(secret, body)})
}

func TestLinearVerifiesHexHMAC(t *testing.T) {
	body := fixture(t, "linear-issue-update.json")
	v := Linear{Secret: "lin_wh_secret", clock: fixedClock()}
	assertVerified(t, v, linearRequest(t, "lin_wh_secret", body), body)
	assertRejected(t, v, linearRequest(t, "another-secret", body), body)
	assertRejected(t, v, post(body, map[string]string{LinearSignatureHeader: "not-hex"}), body)
}

// The signature covers the body, so a body signed last week still carries
// a valid signature; webhookTimestamp is what makes the replay detectable.
func TestLinearRejectsStaleTimestamp(t *testing.T) {
	body := fixture(t, "linear-issue-update.json")
	late := clock{Now: func() time.Time { return at2026.Add(10 * time.Minute) }}
	assertRejected(t, Linear{Secret: "lin_wh_secret", clock: late}, linearRequest(t, "lin_wh_secret", body), body)
}

// A body changed after signing must fail even by one byte.
func TestLinearRejectsTamperedBody(t *testing.T) {
	body := fixture(t, "linear-issue-update.json")
	r := linearRequest(t, "lin_wh_secret", body)
	tampered := append(append([]byte{}, body...), ' ')
	assertRejected(t, Linear{Secret: "lin_wh_secret", clock: fixedClock()}, r, tampered)
}

// --- Azure DevOps -----------------------------------------------------

func TestAzDOVerifiesBasicAuth(t *testing.T) {
	body := fixture(t, "azdo-workitem-updated.json")
	v := AzDO{Username: "sirdar", Password: "hook-password"}

	ok := post(body, nil)
	ok.SetBasicAuth("sirdar", "hook-password")
	assertVerified(t, v, ok, body)

	badPass := post(body, nil)
	badPass.SetBasicAuth("sirdar", "nope")
	assertRejected(t, v, badPass, body)

	badUser := post(body, nil)
	badUser.SetBasicAuth("someone", "hook-password")
	assertRejected(t, v, badUser, body)

	assertRejected(t, v, post(body, nil), body)
}

// --- Zendesk ----------------------------------------------------------

func zendeskRequest(secret string, stamp time.Time, body []byte) *http.Request {
	ts := stamp.UTC().Format(time.RFC3339)
	return post(body, map[string]string{
		ZendeskTimestampHeader: ts,
		ZendeskSignatureHeader: Base64HMACSHA256(secret, append([]byte(ts), body...)),
	})
}

func TestZendeskVerifiesTimestampedSignature(t *testing.T) {
	body := fixture(t, "zendesk-trigger.json")
	v := Zendesk{Secret: "zd-signing-secret", clock: fixedClock()}
	assertVerified(t, v, zendeskRequest("zd-signing-secret", at2026, body), body)
	assertRejected(t, v, zendeskRequest("other-secret", at2026, body), body)
}

// The timestamp is inside the signed message, so a captured delivery
// cannot be re-dated: outside the window it is refused whatever its
// signature says.
func TestZendeskRejectsReplay(t *testing.T) {
	body := fixture(t, "zendesk-trigger.json")
	v := Zendesk{Secret: "zd-signing-secret", clock: fixedClock()}
	old := at2026.Add(-ReplayWindow - time.Second)
	assertRejected(t, v, zendeskRequest("zd-signing-secret", old, body), body)

	// A clock behind the sender's is the same failure in the other
	// direction, and is rejected on the same window.
	future := at2026.Add(ReplayWindow + time.Second)
	assertRejected(t, v, zendeskRequest("zd-signing-secret", future, body), body)
}

func TestZendeskRejectsMissingTimestamp(t *testing.T) {
	body := fixture(t, "zendesk-trigger.json")
	v := Zendesk{Secret: "zd-signing-secret", clock: fixedClock()}
	assertRejected(t, v, post(body, map[string]string{
		ZendeskSignatureHeader: Base64HMACSHA256("zd-signing-secret", body),
	}), body)
}

// --- Intercom ---------------------------------------------------------

func TestIntercomVerifiesHubSignature(t *testing.T) {
	body := fixture(t, "intercom-conversation-assigned.json")
	v := Intercom{Secret: "ic-client-secret"}
	assertVerified(t, v, post(body, map[string]string{
		IntercomSignatureHeader: "sha1=" + HexHMACSHA1("ic-client-secret", body),
	}), body)
	assertRejected(t, v, post(body, map[string]string{
		IntercomSignatureHeader: "sha1=" + HexHMACSHA1("wrong", body),
	}), body)
	// The prefix is part of the scheme: a bare hex digest is not one.
	assertRejected(t, v, post(body, map[string]string{
		IntercomSignatureHeader: HexHMACSHA1("ic-client-secret", body),
	}), body)
	// Nor is the same digest under a different algorithm name.
	assertRejected(t, v, post(body, map[string]string{
		IntercomSignatureHeader: "sha256=" + HexHMACSHA1("ic-client-secret", body),
	}), body)
}

// --- HubSpot ----------------------------------------------------------

const hubspotURL = "https://sirdar.acme.com/hooks/ws1/hubspot"

func hubspotRequest(secret string, stamp time.Time, body []byte) *http.Request {
	ts := strconv.FormatInt(stamp.UnixMilli(), 10)
	r := httptest.NewRequest("POST", hubspotURL, strings.NewReader(string(body)))
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set(HubSpotTimestampHeader, ts)
	signed := "POST" + hubspotURL + string(body) + ts
	r.Header.Set(HubSpotSignatureHeader, Base64HMACSHA256(secret, []byte(signed)))
	return r
}

func TestHubSpotVerifiesV3Signature(t *testing.T) {
	body := fixture(t, "hubspot-ticket-events.json")
	v := HubSpot{Secret: "hs-client-secret", clock: fixedClock()}
	assertVerified(t, v, hubspotRequest("hs-client-secret", at2026, body), body)
	assertRejected(t, v, hubspotRequest("hs-other-secret", at2026, body), body)
}

// The URI is in the signed message, so a delivery replayed against a
// different path on the same host does not verify.
func TestHubSpotSignatureCoversTheURI(t *testing.T) {
	body := fixture(t, "hubspot-ticket-events.json")
	r := hubspotRequest("hs-client-secret", at2026, body)
	r.URL.Path = "/hooks/ws1/generic"
	assertRejected(t, HubSpot{Secret: "hs-client-secret", clock: fixedClock()}, r, body)
}

func TestHubSpotRejectsReplay(t *testing.T) {
	body := fixture(t, "hubspot-ticket-events.json")
	v := HubSpot{Secret: "hs-client-secret", clock: fixedClock()}
	assertRejected(t, v, hubspotRequest("hs-client-secret", at2026.Add(-ReplayWindow-time.Second), body), body)
	assertRejected(t, v, hubspotRequest("hs-client-secret", at2026.Add(ReplayWindow+time.Second), body), body)
}

func TestHubSpotRejectsUnparsableTimestamp(t *testing.T) {
	body := fixture(t, "hubspot-ticket-events.json")
	r := hubspotRequest("hs-client-secret", at2026, body)
	r.Header.Set(HubSpotTimestampHeader, "not-a-number")
	assertRejected(t, HubSpot{Secret: "hs-client-secret", clock: fixedClock()}, r, body)
}

// The proxy fields override what the connection says, for a receiver
// behind something that rewrites neither Host nor X-Forwarded-Host.
func TestHubSpotProxyOverride(t *testing.T) {
	body := []byte(`[]`)
	ts := strconv.FormatInt(at2026.UnixMilli(), 10)
	signed := "POST" + "https://hooks.acme.com/hooks/ws1/hubspot" + string(body) + ts
	r := httptest.NewRequest("POST", "http://127.0.0.1:7777/hooks/ws1/hubspot", strings.NewReader(string(body)))
	r.Header.Set(HubSpotTimestampHeader, ts)
	r.Header.Set(HubSpotSignatureHeader, Base64HMACSHA256("hs", []byte(signed)))

	v := HubSpot{Secret: "hs", ProxyScheme: "https", ProxyHost: "hooks.acme.com", clock: fixedClock()}
	assertVerified(t, v, r, body)
}

// A signature that is valid base64 of the wrong length must not be
// accepted by any shortcut in the comparison.
func TestBase64SignatureLengthIsNotAShortcut(t *testing.T) {
	body := fixture(t, "zendesk-trigger.json")
	r := zendeskRequest("zd-signing-secret", at2026, body)
	r.Header.Set(ZendeskSignatureHeader, base64.StdEncoding.EncodeToString([]byte("short")))
	assertRejected(t, Zendesk{Secret: "zd-signing-secret", clock: fixedClock()}, r, body)
}
