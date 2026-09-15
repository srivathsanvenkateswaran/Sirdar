package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loopback builds the handler `sirdar serve` builds on its default
// address. The bind decision is a separate gate from the cross-site one,
// and the cases below are about the latter.
func loopback(f *fake) http.Handler { return New(f, nil, LoopbackOnly(true)) }

// send issues one request with the headers a browser would attach, so a
// case reads as the situation it stands for rather than as plumbing.
func send(t *testing.T, s http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// The routes a cross-site page would want. Each takes a body this API would
// have accepted, so nothing but the guard can be what refuses them.
var mutatingRoutes = map[string]struct {
	method, target, body string
}{
	"fix":       {"POST", "/api/workspaces/" + knownWS + "/fix", `{"key":"OMNI-2510"}`},
	"triage":    {"POST", "/api/workspaces/" + knownWS + "/triage", `{"keys":["OMNI-2510"]}`},
	"rca":       {"POST", "/api/workspaces/" + knownWS + "/rca", `{"key":"OMNI-2510"}`},
	"eval":      {"POST", "/api/workspaces/" + knownWS + "/eval", `{"keys":["OMNI-2510"]}`},
	"golden":    {"POST", "/api/workspaces/" + knownWS + "/golden", `{"key":"OMNI-2510"}`},
	"workspace": {"POST", "/api/workspaces", `{"root":"/repos/other"}`},
	"resume":    {"POST", "/api/workspaces/" + knownWS + "/runs/" + knownRun + "/resume", `{"answer":"yes"}`},
}

// A form is the whole attack: a page the operator has open posts to the
// loopback listener, the browser sends the request, and without this the
// fix route would start an agent session that writes code.
func TestCrossSiteFormPostIsRefused(t *testing.T) {
	for name, route := range mutatingRoutes {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			for _, ct := range []string{
				"text/plain;charset=UTF-8",
				"application/x-www-form-urlencoded",
				"multipart/form-data; boundary=x",
			} {
				w := send(t, loopback(f), route.method, route.target, route.body,
					map[string]string{"Content-Type": ct})
				assertError(t, w, http.StatusUnsupportedMediaType, "unsupported_media_type")
			}
			if f.gotFixKey != "" || len(f.gotKeys) != 0 || f.gotRCAKey != "" || f.gotRoot != "" {
				t.Fatalf("a refused request still reached the service: %+v", f)
			}
		})
	}
}

// The same request as JSON, from a page on another origin. Sending JSON at
// all needs fetch or XHR, so this is the case where a misconfigured CORS
// setup or a browser bug would otherwise let the body through.
func TestCrossOriginJSONPostIsRefused(t *testing.T) {
	for name, route := range mutatingRoutes {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			w := send(t, loopback(f), route.method, route.target, route.body, map[string]string{
				"Content-Type": "application/json",
				"Origin":       "https://evil.example",
			})
			assertError(t, w, http.StatusForbidden, "forbidden")
			if f.gotFixKey != "" || len(f.gotKeys) != 0 || f.gotRCAKey != "" || f.gotRoot != "" {
				t.Fatalf("a refused request still reached the service: %+v", f)
			}
		})
	}
}

// Sec-Fetch-Site is the browser's own account of where the request came
// from, and it is refused on its own: a request that carried no Origin but
// says it came from another site is the same attack.
func TestCrossSiteFetchMetadataIsRefused(t *testing.T) {
	for _, site := range []string{"cross-site", "same-site"} {
		f := newFake()
		w := send(t, loopback(f), "POST", "/api/workspaces/"+knownWS+"/fix", `{"key":"OMNI-2510"}`,
			map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": site})
		assertError(t, w, http.StatusForbidden, "forbidden")
		if f.gotFixKey != "" {
			t.Fatalf("Sec-Fetch-Site %q still started a fix", site)
		}
	}
}

// The UI's own requests: same origin, JSON, and the fetch metadata a
// same-origin request carries.
func TestSameOriginJSONPostIsAccepted(t *testing.T) {
	for name, route := range mutatingRoutes {
		t.Run(name, func(t *testing.T) {
			// httptest.NewRequest sets Host to example.com, which stands
			// for whatever the listener was reached on.
			w := send(t, loopback(newFake()), route.method, route.target, route.body, map[string]string{
				"Content-Type":   "application/json",
				"Origin":         "http://example.com",
				"Sec-Fetch-Site": "same-origin",
			})
			if w.Code >= 400 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// The Wails shell talks to the Service in process, so it never reaches
// this handler today. Its origins are allowed explicitly all the same, so
// that a shell falling back to the HTTP transport is not refused by a 403
// nobody could place.
func TestWailsOriginsAreAccepted(t *testing.T) {
	for _, origin := range []string{"wails://wails", "http://wails.localhost", "https://wails.localhost"} {
		f := newFake()
		w := send(t, loopback(f), "POST", "/api/workspaces/"+knownWS+"/fix", `{"key":"OMNI-2510"}`,
			map[string]string{"Content-Type": "application/json", "Origin": origin})
		if w.Code != http.StatusAccepted {
			t.Fatalf("%s: status %d: %s", origin, w.Code, w.Body.String())
		}
		if f.gotFixKey != "OMNI-2510" {
			t.Fatalf("%s: the fix did not reach the service", origin)
		}
	}
}

// A sandboxed iframe and a data: URL both send Origin: null.
func TestNullOriginIsRefused(t *testing.T) {
	w := send(t, loopback(newFake()), "POST", "/api/workspaces/"+knownWS+"/fix", `{"key":"OMNI-2510"}`,
		map[string]string{"Content-Type": "application/json", "Origin": "null"})
	assertError(t, w, http.StatusForbidden, "forbidden")
}

// curl, a script, the CLI's own tests: no browser in the way, so no Origin
// and no fetch metadata. The guard is a cross-site defence and not
// authentication, and it must not turn into a broken API for them.
func TestRequestWithNoOriginIsAccepted(t *testing.T) {
	w := send(t, loopback(newFake()), "POST", "/api/workspaces/"+knownWS+"/fix", `{"key":"OMNI-2510"}`,
		map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

// Cancel, the workspace delete and a resume that answers no question send
// no body and declare no type; requiring one would break them.
func TestBodilessMutationsAreAccepted(t *testing.T) {
	for _, tc := range []struct{ method, target string }{
		{"POST", "/api/jobs/" + knownJob + "/cancel"},
		{"DELETE", "/api/workspaces/" + knownWS},
		{"POST", "/api/workspaces/" + knownWS + "/runs/" + knownRun + "/resume"},
		{"POST", "/api/workspaces/" + knownWS + "/eval"},
	} {
		w := send(t, loopback(newFake()), tc.method, tc.target, "", nil)
		if w.Code >= 400 {
			t.Fatalf("%s %s: status %d: %s", tc.method, tc.target, w.Code, w.Body.String())
		}
	}
}

// Reading is not guarded: a GET changes nothing, and the responses are
// unreadable cross-origin anyway since this server sends no CORS headers.
func TestReadsAreNotGuarded(t *testing.T) {
	w := send(t, loopback(newFake()), "GET", "/api/workspaces", "", map[string]string{
		"Origin":         "https://evil.example",
		"Sec-Fetch-Site": "cross-site",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

// A tracker's delivery is not a browser's request: it carries the
// tracker's own content type and whatever Origin that host has, and it
// authenticates by signature. The guard must leave it alone.
func TestHookRoutesAreExemptFromTheGuard(t *testing.T) {
	f := newFake()
	w := send(t, loopback(f), "POST", "/hooks/"+knownWS+"/generic", `{"key":"OMNI-2510"}`, map[string]string{
		"Content-Type":   "application/x-www-form-urlencoded",
		"Origin":         "https://tracker.example",
		"Sec-Fetch-Site": "cross-site",
	})
	// No receiver was configured, so this is the hooks' own 404 rather
	// than the guard's 403 or 415.
	assertError(t, w, http.StatusNotFound, "not_found")
}

// --- the loopback gate ------------------------------------------------

// Every other route reads, or starts a session that writes a note. This one
// writes code to the operator's repository and opens a pull request under
// their GitHub login, so a listener other machines can reach does not get it.
func TestFixIsRefusedOnANonLoopbackListener(t *testing.T) {
	f := newFake()
	w := send(t, New(f, nil, LoopbackOnly(false)), "POST", "/api/workspaces/"+knownWS+"/fix",
		`{"key":"OMNI-2510"}`, map[string]string{"Content-Type": "application/json"})
	assertError(t, w, http.StatusForbidden, "forbidden")
	if f.gotFixKey != "" {
		t.Fatal("the fix reached the service on a remote listener")
	}
	if body := w.Body.String(); !strings.Contains(body, "loopback") {
		t.Errorf("the refusal does not say what would make it work: %s", body)
	}
}

// The gate is the fix route's alone: reading the queue or starting a triage
// on a remote listener is the operator's call, already warned about at
// startup, and closing those would be a different change.
func TestTheOtherRoutesStillWorkOnANonLoopbackListener(t *testing.T) {
	f := newFake()
	s := New(f, nil, LoopbackOnly(false))
	w := send(t, s, "POST", "/api/workspaces/"+knownWS+"/triage", `{"keys":["OMNI-2510"]}`,
		map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("triage status %d: %s", w.Code, w.Body.String())
	}
	if w := send(t, s, "GET", "/api/workspaces", "", nil); w.Code != http.StatusOK {
		t.Fatalf("workspaces status %d", w.Code)
	}
}

// A caller that says nothing about its listener gets the closed door: a
// shell that never thought about the question is exactly the case this
// gate is for, so the option has to be asked for rather than opted out of.
func TestFixIsRefusedWithoutTheBindDecision(t *testing.T) {
	f := newFake()
	w := send(t, New(f, nil), "POST", "/api/workspaces/"+knownWS+"/fix", `{"key":"OMNI-2510"}`,
		map[string]string{"Content-Type": "application/json"})
	assertError(t, w, http.StatusForbidden, "forbidden")
	if f.gotFixKey != "" {
		t.Fatal("the fix reached the service from a handler that was never told its listener")
	}
}

// With the decision passed, the route is the one `sirdar fix` runs.
func TestFixIsAllowedOnALoopbackListener(t *testing.T) {
	f := newFake()
	w := send(t, loopback(f), "POST", "/api/workspaces/"+knownWS+"/fix", `{"key":"OMNI-2510"}`,
		map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if f.gotFixKey != "OMNI-2510" {
		t.Fatal("the fix did not reach the service")
	}
}
