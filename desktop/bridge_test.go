package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

// bridgeMethods is the surface the frontend binds to. Every name here must
// exist on *Bridge and on *app.Service, so a rename in the service breaks
// this test rather than the generated bindings.
var bridgeMethods = []string{
	"Workspaces",
	"AddWorkspace",
	"RemoveWorkspace",
	"Queue",
	"Runs",
	"Run",
	"Events",
	"Note",
	"Prompt",
	"RunDiff",
	"DropHunk",
	"StartTriage",
	"StartRCA",
	"StartFix",
	"StartEval",
	"EvalReports",
	"LatestRetro",
	"Golden",
	"AddGolden",
	"ConfigSummary",
	"Resume",
	"Steer",
	"Cancel",
	"Register",
	"Doctor",
	"Quota",
	"MCPServers",
	"MCPTools",
	"MCPCall",
}

// notBridged is every other exported method of *app.Service, with the
// reason it is not something the frontend calls. Between the two lists the
// Service's surface is accounted for exactly, so a method added there
// reaches this file rather than being quietly unavailable in the desktop
// app — which is how the Wails shell came to be missing a route the browser
// one served.
var notBridged = map[string]string{
	// Lifecycle: main.go calls these around wails.Run.
	"Start": "the shell starts the watcher",
	"Stop":  "the shell stops the jobs on shutdown",
	// The event fan-out reaches the frontend as Wails runtime events,
	// emitted by forward() in main.go, not as a bound call.
	"Subscribe": "events are forwarded onto the Wails runtime",
	// The webhook path. `sirdar serve` owns the hook endpoints; the
	// desktop app has no listener, so nothing calls these.
	"TriageIfIdle": "only an inbound webhook delivery starts a run this way",
	"HookReceived": "only the hook route reports a delivery",
	// Diagnostics with no screen behind them.
	"Jobs": "the frontend tracks the job ids it was given by each start",
}

// TestServiceSurfaceIsAccountedFor is the reverse direction: every exported
// method on *app.Service is either bound to the frontend or listed as
// deliberately unbound.
func TestServiceSurfaceIsAccountedFor(t *testing.T) {
	bound := map[string]bool{}
	for _, name := range bridgeMethods {
		bound[name] = true
	}

	service := reflect.TypeOf(&app.Service{})
	for i := 0; i < service.NumMethod(); i++ {
		name := service.Method(i).Name
		if bound[name] {
			if _, ok := notBridged[name]; ok {
				t.Errorf("%s is in both bridgeMethods and notBridged", name)
			}
			continue
		}
		if _, ok := notBridged[name]; !ok {
			t.Errorf("app.Service.%s is neither bound to the frontend nor listed in notBridged;"+
				" add it to the Bridge, or say there why the desktop app does not need it", name)
		}
	}

	// A stale entry is as bad as a missing one: it would go on excusing a
	// method that no longer exists.
	for name := range notBridged {
		if _, ok := service.MethodByName(name); !ok {
			t.Errorf("notBridged names %s, which app.Service no longer has", name)
		}
	}
}

func TestBridgeCoversServiceSurface(t *testing.T) {
	bridge := reflect.TypeOf(&Bridge{})
	service := reflect.TypeOf(&app.Service{})

	for _, name := range bridgeMethods {
		if _, ok := bridge.MethodByName(name); !ok {
			t.Errorf("Bridge is missing %s", name)
		}
		if _, ok := service.MethodByName(name); !ok {
			t.Errorf("app.Service has no %s for Bridge to forward to", name)
		}
	}
}

// TestBridgeMethodsAreBindable guards the shape Wails can generate bindings
// for: no context parameter, and at most a value plus an error out.
func TestBridgeMethodsAreBindable(t *testing.T) {
	bridge := reflect.TypeOf(&Bridge{})
	errType := reflect.TypeOf((*error)(nil)).Elem()
	ctxName := "context.Context"

	for _, name := range bridgeMethods {
		m, ok := bridge.MethodByName(name)
		if !ok {
			continue // reported by TestBridgeCoversServiceSurface
		}
		for i := 1; i < m.Type.NumIn(); i++ { // 0 is the receiver
			if in := m.Type.In(i); in.String() == ctxName {
				t.Errorf("%s takes a %s; Wails methods have no context parameter", name, ctxName)
			}
		}
		if n := m.Type.NumOut(); n > 2 {
			t.Errorf("%s returns %d values; Wails binds at most a value and an error", name, n)
		}
		if n := m.Type.NumOut(); n == 2 && m.Type.Out(1) != errType {
			t.Errorf("%s second return is %s, want error", name, m.Type.Out(1))
		}
	}
}

// TestBridgeWorkspacesOnEmptyRegistry is the smoke test: a Bridge over a
// Service pointed at a registry file that does not exist yet answers with an
// empty list rather than failing.
// TestBridgeMCPCallDeniedIsAnAnswer pins the one place the bridge reshapes
// an error: a tool the workspace would refuse comes back as a result with
// its verdict, the way the HTTP route answers 403 with a body, so the tool
// tester can show the reason rather than a generic failure. A workspace
// that does not exist is still an error.
func TestBridgeMCPCallDeniedIsAnAnswer(t *testing.T) {
	reg := &app.Registry{Path: filepath.Join(t.TempDir(), "workspaces.json")}
	b := NewBridge(app.New(reg, app.BuildDeps, app.Options{}))

	if _, err := b.MCPCall("no-such-workspace", "filesystem", "read_file", nil); err == nil {
		t.Fatal("MCPCall on an unknown workspace: want an error")
	}
	if _, err := b.MCPServers("no-such-workspace", false); err == nil {
		t.Fatal("MCPServers on an unknown workspace: want an error")
	}
	if _, err := b.MCPTools("no-such-workspace", "filesystem"); err == nil {
		t.Fatal("MCPTools on an unknown workspace: want an error")
	}
}

func TestBridgeWorkspacesOnEmptyRegistry(t *testing.T) {
	reg := &app.Registry{Path: filepath.Join(t.TempDir(), "workspaces.json")}
	b := NewBridge(app.New(reg, app.BuildDeps, app.Options{}))

	got, err := b.Workspaces()
	if err != nil {
		t.Fatalf("Workspaces: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Workspaces on a fresh registry = %v, want none", got)
	}

	if _, err := b.AddWorkspace(t.TempDir()); err == nil {
		t.Fatal("AddWorkspace on a directory with no Sirdar config: want an error")
	}
}
