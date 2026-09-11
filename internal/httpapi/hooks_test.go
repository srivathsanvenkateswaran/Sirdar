package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/webhooks"
)

const hookSecret = "s3cret"

// hookServer builds the surface with one generic source enabled.
func hookServer(f *fake) http.Handler {
	rc := webhooks.New(map[string]webhooks.Verifier{
		webhooks.SourceGeneric: webhooks.Generic{Secret: hookSecret},
	}, webhooks.Match{Assignee: "sri@acme.com"})
	return New(f, emptyFS{}, WithHooks(knownWS, rc))
}

// deliver posts one webhook body, with the shared secret unless secret is
// empty.
func deliver(h http.Handler, target, secret, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if secret != "" {
		r.Header.Set(webhooks.SecretHeader, secret)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const assignedBody = `{"key":"OMNI-2510","assignee":"sri@acme.com"}`

func TestHookStartsTriage(t *testing.T) {
	f := newFake()
	w := deliver(hookServer(f), "/hooks/ws1/generic", hookSecret, assignedBody)

	var got struct {
		JobID string   `json:"jobId"`
		Keys  []string `json:"keys"`
	}
	decodeJSON(t, w, http.StatusAccepted, &got)
	if got.JobID != knownJob {
		t.Errorf("jobId %q, want %q", got.JobID, knownJob)
	}
	if len(got.Keys) != 1 || got.Keys[0] != "OMNI-2510" {
		t.Errorf("keys %v", got.Keys)
	}
	if want := []hookCall{{"generic", "OMNI-2510", "started"}}; !equalHooks(f.hooks(), want) {
		t.Errorf("hook events %+v, want %+v", f.hooks(), want)
	}
}

// A delivery for a key that is already running, or that was triaged inside
// the cooldown, is accepted and does nothing. It must not be an error: a
// tracker told its delivery failed will send it again.
func TestHookSkipsOnCooldown(t *testing.T) {
	f := newFake()
	f.idleReason = "OMNI-2510 was triaged 2m ago and the cooldown is 10m"
	w := deliver(hookServer(f), "/hooks/ws1/generic", hookSecret, assignedBody)

	var got struct {
		JobID   string `json:"jobId"`
		Skipped string `json:"skipped"`
	}
	decodeJSON(t, w, http.StatusAccepted, &got)
	if got.JobID != "" {
		t.Errorf("a skipped delivery reported job %q", got.JobID)
	}
	if !strings.Contains(got.Skipped, "cooldown") {
		t.Errorf("skipped %q", got.Skipped)
	}
	if want := []hookCall{{"generic", "OMNI-2510", "skipped"}}; !equalHooks(f.hooks(), want) {
		t.Errorf("hook events %+v, want %+v", f.hooks(), want)
	}
}

// A ticket assigned to somebody else is the case the match filter exists
// for: a tracker fires on every change to every ticket.
func TestHookFiltersOnAssignee(t *testing.T) {
	f := newFake()
	w := deliver(hookServer(f), "/hooks/ws1/generic", hookSecret,
		`{"key":"OMNI-2511","assignee":"someone@acme.com"}`)

	var got struct {
		Skipped string `json:"skipped"`
	}
	decodeJSON(t, w, http.StatusAccepted, &got)
	if !strings.Contains(got.Skipped, "someone@acme.com") {
		t.Errorf("skipped %q", got.Skipped)
	}
	if len(f.gotIdle) != 0 {
		t.Errorf("a filtered delivery still reached the service: %v", f.gotIdle)
	}
	if want := []hookCall{{"generic", "OMNI-2511", "filtered"}}; !equalHooks(f.hooks(), want) {
		t.Errorf("hook events %+v, want %+v", f.hooks(), want)
	}
}

func TestHookRejectsBadSecret(t *testing.T) {
	f := newFake()
	w := deliver(hookServer(f), "/hooks/ws1/generic", "wrong-secret", assignedBody)
	assertError(t, w, http.StatusUnauthorized, "unauthorized")
	if len(f.gotIdle) != 0 {
		t.Error("an unverified delivery reached the service")
	}
	if want := []hookCall{{"generic", "", "rejected"}}; !equalHooks(f.hooks(), want) {
		t.Errorf("hook events %+v", f.hooks())
	}
}

func TestHookRejectsMissingSecret(t *testing.T) {
	w := deliver(hookServer(newFake()), "/hooks/ws1/generic", "", assignedBody)
	assertError(t, w, http.StatusUnauthorized, "unauthorized")
}

// A source that is not configured and a source that does not exist are the
// same 404: answering differently would tell an unauthenticated caller
// which trackers this workspace is wired to.
func TestHookUnknownSourceIs404(t *testing.T) {
	w := deliver(hookServer(newFake()), "/hooks/ws1/linear", hookSecret, assignedBody)
	assertError(t, w, http.StatusNotFound, "not_found")
}

// With webhooks.enabled false the server is built without a receiver, and
// every hook path has to be a 404 rather than falling through to the
// single-page app and answering 200 with a page of HTML.
func TestHooksDisabledIs404(t *testing.T) {
	h := New(newFake(), emptyFS{})
	for _, target := range []string{"/hooks/ws1/generic", "/hooks/", "/hooks/ws1"} {
		w := deliver(h, target, hookSecret, assignedBody)
		assertError(t, w, http.StatusNotFound, "not_found")
	}
}

// A method other than POST is not a delivery.
func TestHookRejectsGet(t *testing.T) {
	r := httptest.NewRequest("GET", "/hooks/ws1/generic", nil)
	w := httptest.NewRecorder()
	hookServer(newFake()).ServeHTTP(w, r)
	assertError(t, w, http.StatusNotFound, "not_found")
}

// The receiver holds one workspace's secrets, and the id in the path is
// checked against the workspace it was built for. Without that check, the
// holder of this workspace's secret could name any other workspace the
// operator has registered and start runs in it — reading that workspace's
// tickets with that workspace's credentials.
func TestHookRefusesAnotherWorkspace(t *testing.T) {
	f := newFake()
	// A second registered workspace: its id is one the service knows, so
	// the 404 can only come from the receiver's own check.
	f.workspaces = append(f.workspaces, Workspace{ID: "ws2", Name: "other", Root: "/repos/other"})
	h := hookServer(f)

	w := deliver(h, "/hooks/ws2/generic", hookSecret, assignedBody)
	assertError(t, w, http.StatusNotFound, "not_found")
	if len(f.gotIdle) != 0 {
		t.Errorf("a delivery naming another workspace reached the service: %v", f.gotIdle)
	}
	if len(f.hooks()) != 0 {
		t.Errorf("hook events %+v, want none", f.hooks())
	}

	// The same delivery to the workspace the receiver was built for is
	// what that 404 has to be distinguished from.
	if got := deliver(h, "/hooks/ws1/generic", hookSecret, assignedBody); got.Code != http.StatusAccepted {
		t.Fatalf("the served workspace answered %d", got.Code)
	}
}

// A workspace id nobody knows is the same 404, and it is refused before
// the body is verified: the endpoint tells an unauthenticated caller
// nothing about which workspace it serves.
func TestHookUnknownWorkspaceIs404(t *testing.T) {
	f := newFake()
	w := deliver(hookServer(f), "/hooks/nope/generic", hookSecret, assignedBody)
	assertError(t, w, http.StatusNotFound, "not_found")
	if len(f.gotIdle) != 0 {
		t.Errorf("a delivery naming an unknown workspace reached the service: %v", f.gotIdle)
	}
}

func TestHookRejectsOversizedBody(t *testing.T) {
	body := `{"key":"OMNI-2510","pad":"` + strings.Repeat("x", webhooks.MaxBodyBytes) + `"}`
	w := deliver(hookServer(newFake()), "/hooks/ws1/generic", hookSecret, body)
	assertError(t, w, http.StatusRequestEntityTooLarge, "too_large")
}

// The body is read before it is verified, so an oversized body is refused
// on its size whatever secret it carries — including none.
func TestHookRejectsOversizedBodyBeforeVerifying(t *testing.T) {
	body := `{"key":"OMNI-2510","pad":"` + strings.Repeat("x", webhooks.MaxBodyBytes) + `"}`
	w := deliver(hookServer(newFake()), "/hooks/ws1/generic", "", body)
	assertError(t, w, http.StatusRequestEntityTooLarge, "too_large")
}

// A run that could not be started is a 500 with nothing in it: a Service
// error names what it was working on — the workspace root, an adapter's
// command line — and the sender of a webhook is not the operator. The
// detail goes to the server's log instead.
func TestHookHidesTheStartFailureFromTheSender(t *testing.T) {
	f := newFake()
	f.idleErr = errors.New("open /repos/oxo-apis/.sirdar/config.yaml: permission denied")

	rc := webhooks.New(map[string]webhooks.Verifier{
		webhooks.SourceGeneric: webhooks.Generic{Secret: hookSecret},
	}, webhooks.Match{})
	h := newServer(f, emptyFS{}, WithHooks(knownWS, rc))
	var logged strings.Builder
	h.logf = func(format string, v ...any) { fmt.Fprintf(&logged, format, v...) }

	w := deliver(h, "/hooks/ws1/generic", hookSecret, assignedBody)
	assertError(t, w, http.StatusInternalServerError, "internal")
	if strings.Contains(w.Body.String(), "/repos/oxo-apis") {
		t.Errorf("the response carried the workspace path: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "permission denied") {
		t.Errorf("the response carried the underlying error: %s", w.Body.String())
	}
	if !strings.Contains(logged.String(), "permission denied") {
		t.Errorf("the detail was not logged: %q", logged.String())
	}
}

func TestHookRejectsUnreadableBody(t *testing.T) {
	w := deliver(hookServer(newFake()), "/hooks/ws1/generic", hookSecret, `{"key":`)
	assertError(t, w, http.StatusBadRequest, "bad_request")
}

// A verified body that names no ticket this receiver acts on is accepted
// and does nothing.
func TestHookAcceptsDeliveryWithNoTrigger(t *testing.T) {
	f := newFake()
	rc := webhooks.New(map[string]webhooks.Verifier{
		webhooks.SourceIntercom: webhooks.Intercom{Secret: hookSecret},
	}, webhooks.Match{})
	h := New(f, emptyFS{}, WithHooks(knownWS, rc))

	body := `{"topic":"conversation.user.replied","data":{"item":{"id":"7712"}}}`
	r := httptest.NewRequest("POST", "/hooks/ws1/intercom", strings.NewReader(body))
	r.Header.Set(webhooks.IntercomSignatureHeader, "sha1="+webhooks.HexHMACSHA1(hookSecret, []byte(body)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var got struct {
		Skipped string `json:"skipped"`
	}
	decodeJSON(t, w, http.StatusAccepted, &got)
	if got.Skipped == "" {
		t.Error("an ignored delivery carried no reason")
	}
	if want := []hookCall{{"intercom", "", "ignored"}}; !equalHooks(f.hooks(), want) {
		t.Errorf("hook events %+v", f.hooks())
	}
}

// One HubSpot delivery may name several tickets, and each gets its own
// job, so the response names them all.
func TestHookStartsOneJobPerTicket(t *testing.T) {
	f := newFake()
	rc := webhooks.New(map[string]webhooks.Verifier{
		webhooks.SourceGeneric: webhooks.Generic{Secret: hookSecret},
		webhooks.SourceHubSpot: webhooks.HubSpot{Secret: hookSecret},
	}, webhooks.Match{})

	body := `[{"subscriptionType":"ticket.creation","objectId":991},{"subscriptionType":"ticket.creation","objectId":992}]`
	// No proxy is configured, so the signed URI is the one the request
	// arrived on — https://hooks.acme.com — and no X-Forwarded-* header
	// takes part in it.
	r := httptest.NewRequest("POST", "https://hooks.acme.com/hooks/ws1/hubspot", strings.NewReader(body))
	ts := hubspotNow()
	r.Header.Set(webhooks.HubSpotTimestampHeader, ts)
	signed := "POST" + "https://hooks.acme.com/hooks/ws1/hubspot" + body + ts
	r.Header.Set(webhooks.HubSpotSignatureHeader, webhooks.Base64HMACSHA256(hookSecret, []byte(signed)))

	w := httptest.NewRecorder()
	New(f, emptyFS{}, WithHooks(knownWS, rc)).ServeHTTP(w, r)

	var got struct {
		JobID  string   `json:"jobId"`
		JobIDs []string `json:"jobIds"`
		Keys   []string `json:"keys"`
	}
	decodeJSON(t, w, http.StatusAccepted, &got)
	if len(got.Keys) != 2 || got.Keys[0] != "991" || got.Keys[1] != "992" {
		t.Fatalf("keys %v", got.Keys)
	}
	if len(got.JobIDs) != 2 {
		t.Errorf("jobIds %v, want one per key", got.JobIDs)
	}
	if got.JobID == "" {
		t.Error("jobId is empty")
	}
}

func equalHooks(got, want []hookCall) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// hubspotNow is the timestamp header a live delivery carries, in the
// milliseconds HubSpot signs.
func hubspotNow() string {
	return strconv.FormatInt(time.Now().UnixMilli(), 10)
}
