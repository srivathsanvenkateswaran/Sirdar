package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPServersRoute(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/mcp", "")

	var got MCPInventory
	decodeJSON(t, w, 200, &got)
	if len(got.Servers) != 1 || got.Servers[0].Name != knownMCPServer {
		t.Fatalf("got %+v", got.Servers)
	}
	if f.gotConnect {
		t.Error("the plain list route must not connect")
	}
	if got.Servers[0].Connected || got.Servers[0].Tools != 0 {
		t.Errorf("a list without --connect has no connected half: %+v", got.Servers[0])
	}

	f = newFake()
	w = do(t, f, "GET", "/api/workspaces/"+knownWS+"/mcp?connect=1", "")
	decodeJSON(t, w, 200, &got)
	if !f.gotConnect {
		t.Error("connect=1 should reach the service")
	}
	if !got.Servers[0].Connected || got.Servers[0].Tools != 2 {
		t.Errorf("want the connected half: %+v", got.Servers[0])
	}

	if w := do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/mcp?connect=maybe", ""); w.Code != 400 {
		t.Errorf("a nonsense connect value should be a 400, got %d", w.Code)
	}
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/nope/mcp", ""), 404, "not_found")
}

func TestMCPToolsRoute(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/mcp/"+knownMCPServer+"/tools", "")

	var got MCPToolList
	decodeJSON(t, w, 200, &got)
	if len(got.Tools) != 2 {
		t.Fatalf("got %+v", got.Tools)
	}
	if got.Tools[0].Verdict != "allowed" || got.Tools[1].Verdict != "denied" {
		t.Errorf("verdicts = %q, %q", got.Tools[0].Verdict, got.Tools[1].Verdict)
	}
	if got.Tools[1].Reason == "" {
		t.Error("a denied tool must say why")
	}

	assertError(t, do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/mcp/nope/tools", ""), 404, "not_found")
}

func TestMCPCallRoute(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", "/api/workspaces/"+knownWS+"/mcp/"+knownMCPServer+"/call",
		`{"tool":"list_rows","args":{"table":"orders"}}`)

	var got MCPCallResult
	decodeJSON(t, w, 200, &got)
	if got.Verdict != "allowed" || got.Result != "rows of orders" {
		t.Fatalf("got %+v", got)
	}
	if got.TookMs == 0 {
		t.Error("the result should carry its timing")
	}
	if f.gotCall.Tool != "list_rows" || !strings.Contains(f.gotCall.Args, "orders") {
		t.Errorf("the service was asked %+v", f.gotCall)
	}
}

// A tool the workspace's permissions refuse is 403, and the body still
// carries the verdict and the reason rather than a bare error envelope.
func TestMCPCallRouteRefusesADeniedTool(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", "/api/workspaces/"+knownWS+"/mcp/"+knownMCPServer+"/call",
		`{"tool":"delete_rows"}`)

	var got MCPCallResult
	decodeJSON(t, w, 403, &got)
	if got.Verdict != "denied" || !strings.Contains(got.Reason, "write word") {
		t.Fatalf("got %+v, want the denial and its reason", got)
	}
	if got.Result != "" {
		t.Error("a refused call has no result")
	}
}

func TestMCPCallRouteWantsATool(t *testing.T) {
	assertError(t, do(t, newFake(), "POST", "/api/workspaces/"+knownWS+"/mcp/"+knownMCPServer+"/call", `{}`),
		400, "bad_request")
}

// The call route writes, so it is behind the same cross-site guard as
// every other mutating route: a form post from another page is refused
// before the service is reached.
func TestMCPCallRouteIsGuardedLikeTheOthers(t *testing.T) {
	f := newFake()
	r := httptest.NewRequest("POST", "/api/workspaces/"+knownWS+"/mcp/"+knownMCPServer+"/call",
		strings.NewReader(`{"tool":"list_rows"}`))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	New(f, emptyFS{}, LoopbackOnly(true)).ServeHTTP(w, r)

	assertError(t, w, 403, "forbidden")
	if f.gotCall.Tool != "" {
		t.Error("the guard let a cross-site call through")
	}

	// A form content type is the other half of the same guard.
	r = httptest.NewRequest("POST", "/api/workspaces/"+knownWS+"/mcp/"+knownMCPServer+"/call",
		strings.NewReader("tool=list_rows"))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	New(newFake(), emptyFS{}, LoopbackOnly(true)).ServeHTTP(w, r)
	if w.Code != 415 {
		t.Errorf("a form post should be 415, got %d", w.Code)
	}
}
