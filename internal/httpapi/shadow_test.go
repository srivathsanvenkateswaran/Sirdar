package httpapi

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

const (
	shadowPath      = "/api/workspaces/" + knownWS + "/shadow"
	suggestionsPath = "/api/workspaces/" + knownWS + "/suggestions"
)

// TestShadowRoutesReachTheirMethods drives every shadow and suggestion
// route once and checks the service was asked with the decoded arguments.
func TestShadowRoutesReachTheirMethods(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
		status                     int
		want                       shadowCall
	}{
		{"status", "GET", shadowPath + "/status", "", 200,
			shadowCall{"ShadowStatus", []any{knownWS}}},
		{"report", "GET", shadowPath + "/report?since=7d", "", 200,
			shadowCall{"ShadowReport", []any{knownWS, "7d"}}},
		{"report without since", "GET", shadowPath + "/report", "", 200,
			shadowCall{"ShadowReport", []any{knownWS, ""}}},
		{"poll", "POST", shadowPath + "/poll", "", 200,
			shadowCall{"ShadowPoll", []any{knownWS}}},
		{"pause", "POST", shadowPath + "/pause", `{"for":"4h"}`, 200,
			shadowCall{"ShadowPause", []any{knownWS, "4h"}}},
		{"pause without a body", "POST", shadowPath + "/pause", "", 200,
			shadowCall{"ShadowPause", []any{knownWS, ""}}},
		{"resume", "POST", shadowPath + "/resume", "", 200,
			shadowCall{"ShadowResume", []any{knownWS}}},
		{"outcomes", "POST", shadowPath + "/outcomes",
			`{"key":"OMNI-2510","prUrls":["https://github.com/o/r/pull/1"],"resolution":"fixed the cache"}`, 200,
			shadowCall{"ShadowOutcome", []any{knownWS, app.ShadowOutcomeInput{
				Key: "OMNI-2510", PRURLs: []string{"https://github.com/o/r/pull/1"}, Resolution: "fixed the cache"}}}},
		{"regrade", "POST", shadowPath + "/regrade", `{"key":"OMNI-2510"}`, 200,
			shadowCall{"ShadowRegrade", []any{knownWS, []string{"OMNI-2510"}}}},
		{"remember", "POST", "/api/workspaces/" + knownWS + "/runs/" + knownRun + "/remember", `{"index":2}`, 200,
			shadowCall{"Remember", []any{knownWS, knownRun, 2}}},
		{"suggestions", "GET", suggestionsPath, "", 200,
			shadowCall{"Suggestions", []any{knownWS}}},
		{"accept", "POST", suggestionsPath + "/s-0123456789ab/accept", `{"playbook":"20-logs.md","addition":"- read the log"}`, 200,
			shadowCall{"AcceptSuggestion", []any{knownWS, "s-0123456789ab", app.AcceptSuggestionInput{Playbook: "20-logs.md", Addition: "- read the log"}}}},
		{"accept as proposed", "POST", suggestionsPath + "/s-0123456789ab/accept", "", 200,
			shadowCall{"AcceptSuggestion", []any{knownWS, "s-0123456789ab", app.AcceptSuggestionInput{}}}},
		{"dismiss", "POST", suggestionsPath + "/s-0123456789ab/dismiss", "", 204,
			shadowCall{"DismissSuggestion", []any{knownWS, "s-0123456789ab"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			w := do(t, f, tc.method, tc.target, tc.body)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if got := f.lastShadowCall(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("service was asked %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestShadowRegradeAnswersWithTheKeysRow pins the single-key shape: the
// route answers with the row itself, not a list of one.
func TestShadowRegradeAnswersWithTheKeysRow(t *testing.T) {
	var row app.ShadowRow
	decodeJSON(t, do(t, newFake(), "POST", shadowPath+"/regrade", `{"key":"OMNI-2510"}`), 200, &row)
	if row.Key != "OMNI-2510" || row.Grade != "wrong" {
		t.Fatalf("row %+v", row)
	}
}

func TestShadowRegradeWithNoRowIs404(t *testing.T) {
	f := newFake()
	f.noRegradeRow = true
	assertError(t, do(t, f, "POST", shadowPath+"/regrade", `{"key":"OMNI-9"}`), 404, "not_found")
}

func TestShadowRegradeNeedsAKey(t *testing.T) {
	f := newFake()
	assertError(t, do(t, f, "POST", shadowPath+"/regrade", `{}`), 400, "bad_request")
	if len(f.shadowCalls) != 0 {
		t.Fatalf("a request with no key reached the service: %+v", f.shadowCalls)
	}
}

// TestShadowPollLockedElsewhereIs403: another process watching the
// workspace refuses a poll, and the message says who.
func TestShadowPollLockedElsewhereIs403(t *testing.T) {
	f := newFake()
	f.shadowErr = fmt.Errorf("%w: shadow: watched by pid 42 (serve)", app.ErrShadowLocked)
	w := do(t, f, "POST", shadowPath+"/poll", "")
	assertError(t, w, http.StatusForbidden, "forbidden")
	if !strings.Contains(w.Body.String(), "watched by pid 42 (serve)") {
		t.Fatalf("the 403 does not name the holder: %s", w.Body.String())
	}
}

func TestShadowOutcomeBadInputIs400(t *testing.T) {
	f := newFake()
	f.shadowErr = fmt.Errorf("%w: \"notaurl\" is not a pull request URL", app.ErrBadInput)
	assertError(t, do(t, f, "POST", shadowPath+"/outcomes", `{"key":"OMNI-2510","prUrls":["notaurl"]}`), 400, "bad_request")
}

func TestShadowOutcomeRefusesAnUnknownField(t *testing.T) {
	f := newFake()
	assertError(t, do(t, f, "POST", shadowPath+"/outcomes", `{"key":"OMNI-2510","prs":["x"]}`), 400, "bad_request")
	if len(f.shadowCalls) != 0 {
		t.Fatalf("a malformed body reached the service: %+v", f.shadowCalls)
	}
}

func TestDismissUnknownSuggestionIs404(t *testing.T) {
	f := newFake()
	f.shadowErr = fmt.Errorf("%w: %q", app.ErrNoSuchSuggestion, "s-000000000000")
	assertError(t, do(t, f, "POST", suggestionsPath+"/s-000000000000/dismiss", ""), 404, "not_found")
}

func TestSuggestionsWithNoneIsAnEmptyList(t *testing.T) {
	w := do(t, newFake(), "GET", suggestionsPath, "")
	decodeJSON(t, w, 200, nil)
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("body %q, want []", got)
	}
}

func TestShadowRoutesOnAnUnknownWorkspaceAre404(t *testing.T) {
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/nope/shadow/status", ""), 404, "not_found")
}

// TestShadowRoutesAreGuarded: a cross-site POST to a shadow route is
// refused by the guard before the service hears of it.
func TestShadowRoutesAreGuarded(t *testing.T) {
	f := newFake()
	w := send(t, loopback(f), "POST", shadowPath+"/pause", `{"for":"4h"}`, map[string]string{
		"Content-Type": "application/json",
		"Origin":       "https://evil.example",
	})
	assertError(t, w, http.StatusForbidden, "forbidden")
	if len(f.shadowCalls) != 0 {
		t.Fatalf("a refused request still reached the service: %+v", f.shadowCalls)
	}
}
