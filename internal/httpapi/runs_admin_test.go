package httpapi

import (
	"fmt"
	"testing"
)

func TestDeleteRunRoute(t *testing.T) {
	f := newFake()
	w := do(t, f, "DELETE", "/api/workspaces/"+knownWS+"/runs/"+knownRun, "")
	if w.Code != 204 {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	if f.gotDeleted != knownRun {
		t.Fatalf("deleted %q", f.gotDeleted)
	}
}

func TestDeleteRunRefusesALiveRunWith409(t *testing.T) {
	f := newFake()
	f.deleteErr = fmt.Errorf("%w: %s is running", ErrRunLive, knownRun)
	assertError(t, do(t, f, "DELETE", "/api/workspaces/"+knownWS+"/runs/"+knownRun, ""), 409, "conflict")
	if f.gotDeleted != "" {
		t.Fatalf("deleted %q, want nothing", f.gotDeleted)
	}
}

func TestDeleteRunUnknownIDs(t *testing.T) {
	assertError(t, do(t, newFake(), "DELETE", "/api/workspaces/"+knownWS+"/runs/nope", ""), 404, "not_found")
	assertError(t, do(t, newFake(), "DELETE", "/api/workspaces/nope/runs/"+knownRun, ""), 404, "not_found")
}

func TestSearchRoute(t *testing.T) {
	f := newFake()
	f.hits = []SearchHit{{
		RunID: knownRun, Key: "OMNI-2510", Kind: "triage", Status: "completed",
		Source: "note", Path: "/notes/OMNI-2510-triage.md", Excerpt: "…the adapter Times Out on page two…",
	}}
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/search?q=times+out", "")

	var got []map[string]any
	decodeJSON(t, w, 200, &got)
	if f.gotSearch != "times out" {
		t.Fatalf("query %q", f.gotSearch)
	}
	if len(got) != 1 {
		t.Fatalf("hits %+v", got)
	}
	for _, key := range []string{"runId", "key", "kind", "status", "source", "path", "excerpt"} {
		if _, ok := got[0][key]; !ok {
			t.Fatalf("hit is missing %q: %+v", key, got[0])
		}
	}
	if got[0]["source"] != "note" || got[0]["runId"] != knownRun {
		t.Fatalf("hit %+v", got[0])
	}
}

func TestSearchAnswersAnEmptyListNotNull(t *testing.T) {
	w := do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/search?q=zzz", "")
	decodeJSON(t, w, 200, nil)
	if body := w.Body.String(); body != "[]\n" {
		t.Fatalf("body %q", body)
	}
}

func TestSearchNeedsAQuery(t *testing.T) {
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/search", ""), 400, "bad_request")
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/search?q=%20", ""), 400, "bad_request")
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/nope/search?q=x", ""), 404, "not_found")
}
