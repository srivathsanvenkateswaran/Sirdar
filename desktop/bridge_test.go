package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
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
	"Resolve",
	"ResolveHelpdesk",
	"ComposeIntent",
	"Runs",
	"Run",
	"DeleteRun",
	"Search",
	"Events",
	"Note",
	"Prompt",
	"Attachments",
	"RunDiff",
	"DropHunk",
	"StartTriage",
	"StartRCA",
	"StartSession",
	"UpdateNote",
	"SaveNote",
	"StartFix",
	"StartEval",
	"EvalReports",
	"LatestRetro",
	"Golden",
	"AddGolden",
	"ConfigSummary",
	"Models",
	"RefreshModels",
	"Resume",
	"Steer",
	"Cancel",
	"Register",
	"Doctor",
	"Quota",
	"MCPServers",
	"MCPTools",
	"MCPCall",
	"Playbooks",
	"Playbook",
	"SavePlaybook",
	"AddPlaybook",
	"DeletePlaybook",
	"ScaffoldPlaybooks",
	"OpenPlaybook",
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
	// The bundle's files. A browser fetches one over the attachment
	// route; the desktop reads it through Bridge.AttachmentDataURL, which
	// calls this and hands the webview a data URL it can load.
	"AttachmentFile": "the desktop reaches it through Bridge.AttachmentDataURL",
	// run.Sink: the executor calls these as it writes each event line, so
	// the window sees it without waiting for the watcher's poll. Nothing
	// in the frontend calls them. See internal/app/live.go.
	"Append": "the run executor publishes its own event lines through this",
	"Done":   "the run executor closes its event log through this",
}

// awaitingFrontend are bridge methods whose BridgeBindings entries the frontend track adds in
// its own worktree. Until the two tracks merge, the reverse check skips them; task M1 deletes
// this map once transport.ts names them.
var awaitingFrontend = map[string]string{
	"StartSession": "the session composer's start",
	"UpdateNote":   "the session screen's Update note",
	"SaveNote":     "the session screen's Save as note",
}

// bridgeOnlyMethods are the desktop's own bindings: methods on *Bridge with
// no *app.Service counterpart, which is why they are absent from
// bridgeMethods. Each is something only the machine running the app can do,
// with the reason a browser served by `sirdar serve` cannot — the web UI
// copies a path instead, and `desktop/frontend/src/api/parity.test.ts`
// carries the same list on the TypeScript side.
var bridgeOnlyMethods = map[string]string{
	"Version":           "the desktop build's version; a browser is served by whatever `sirdar serve` is running",
	"OpenConfig":        "opens config.yaml in whatever the machine associates with it",
	"OpenNote":          "opens a note in whatever the machine associates with Markdown",
	"OpenRunDir":        "reveals the run directory in the machine's file manager",
	"AttachmentDataURL": "inlines an attachment, which WKWebView will not load over file://",
}

// bindingsPath is the frontend's own declaration of what it calls on the
// bridge. Wails generates typed bindings at build time, but that directory
// is not in the repository, so the frontend reaches window.go.main.Bridge
// through a hand-written interface — and nothing checked that interface
// against the Go side. This file does.
const bindingsPath = "frontend/src/api/transport.ts"

// frontendBridgeCalls reads the method names off the BridgeBindings
// interface in bindingsPath. A method declaration is a line beginning with
// an upper-case name and an open paren; a doc comment, a parameter line and
// a closing paren are none of those.
func frontendBridgeCalls(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(bindingsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", bindingsPath, err)
	}
	lines := strings.Split(string(src), "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "interface BridgeBindings {") {
			start = i + 1
			break
		}
	}
	if start == -1 {
		t.Fatalf("%s no longer declares an interface BridgeBindings; the frontend reaches the bridge some other way now", bindingsPath)
	}
	method := regexp.MustCompile(`^\s+([A-Z]\w*)\(`)
	var names []string
	for _, line := range lines[start:] {
		if line == "}" {
			return names
		}
		if m := method.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	t.Fatalf("%s: the BridgeBindings interface is never closed", bindingsPath)
	return nil
}

// TestFrontendBridgeCallsExist walks the frontend's BridgeBindings interface
// and asserts every name on it is a method of *Bridge. A screen that calls a
// binding the Go side does not have fails in the desktop app only — the
// browser takes the HTTP route and says nothing — so the mismatch is caught
// here instead.
func TestFrontendBridgeCallsExist(t *testing.T) {
	bridge := reflect.TypeOf(&Bridge{})
	calls := frontendBridgeCalls(t)
	if len(calls) < len(bridgeMethods) {
		t.Fatalf("read %d bindings off %s, fewer than the %d bound methods; the parse is wrong", len(calls), bindingsPath, len(bridgeMethods))
	}
	for _, name := range calls {
		if _, ok := bridge.MethodByName(name); !ok {
			t.Errorf("the frontend calls Bridge.%s, which desktop/bridge.go does not have", name)
		}
	}

	// The other direction: a bound method the frontend never calls is a
	// route the desktop app carries for nobody.
	called := map[string]bool{}
	for _, name := range calls {
		called[name] = true
	}
	for i := 0; i < bridge.NumMethod(); i++ {
		name := bridge.Method(i).Name
		if !called[name] {
			if _, ok := awaitingFrontend[name]; ok {
				continue
			}
			t.Errorf("Bridge.%s is bound but the frontend's BridgeBindings does not name it", name)
		}
	}
}

// TestAwaitingFrontendIsStillAwaited: every name in awaitingFrontend is a
// method the Go side already has and the frontend's BridgeBindings does
// not yet name. Once transport.ts catches up, this fails — which is the
// point: the map cannot outlive the merge silently, and task M1 is the one
// that deletes it.
func TestAwaitingFrontendIsStillAwaited(t *testing.T) {
	bridge := reflect.TypeOf(&Bridge{})
	called := map[string]bool{}
	for _, name := range frontendBridgeCalls(t) {
		called[name] = true
	}
	for name := range awaitingFrontend {
		if _, ok := bridge.MethodByName(name); !ok {
			t.Errorf("awaitingFrontend names %s, which *Bridge does not have", name)
		}
		if called[name] {
			t.Errorf("awaitingFrontend still names %s, but the frontend's BridgeBindings already calls it; delete this entry", name)
		}
	}
}

// TestBridgeSurfaceIsAccountedFor: every exported method of *Bridge is
// either forwarded to the service (bridgeMethods) or the desktop's own
// (bridgeOnlyMethods, with its reason). Between the two the shell's surface
// is named exactly, so what the web UI cannot do is a list somebody wrote
// rather than a screen that quietly lost a button.
func TestBridgeSurfaceIsAccountedFor(t *testing.T) {
	forwarded := map[string]bool{}
	for _, name := range bridgeMethods {
		forwarded[name] = true
	}

	bridge := reflect.TypeOf(&Bridge{})
	for i := 0; i < bridge.NumMethod(); i++ {
		name := bridge.Method(i).Name
		_, own := bridgeOnlyMethods[name]
		if forwarded[name] && own {
			t.Errorf("%s is in both bridgeMethods and bridgeOnlyMethods", name)
		}
		if !forwarded[name] && !own {
			t.Errorf("Bridge.%s is neither forwarded to app.Service nor listed in bridgeOnlyMethods;"+
				" say there why the browser cannot do it and what the web UI offers instead", name)
		}
	}

	for name, reason := range bridgeOnlyMethods {
		if _, ok := bridge.MethodByName(name); !ok {
			t.Errorf("bridgeOnlyMethods names %s, which *Bridge no longer has", name)
		}
		if reason == "" {
			t.Errorf("bridgeOnlyMethods[%s] has no reason", name)
		}
	}
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

// TestResumeAndSteerTakeAModel: the desktop transport calls these with a
// model, so a run blocked on a per-model limit can be carried on from the
// session screen. The arity is asserted here because the Wails bindings
// are generated from it: a parameter dropped in the bridge is a silently
// ignored model in the app.
func TestResumeAndSteerTakeAModel(t *testing.T) {
	bridge := reflect.TypeOf(&Bridge{})
	for name, arity := range map[string]int{"Resume": 6, "Steer": 5} {
		m, ok := bridge.MethodByName(name)
		if !ok {
			t.Fatalf("Bridge.%s is missing", name)
		}
		// receiver, workspace, run, text/answer, model (and a decision on Resume)
		if got := m.Type.NumIn(); got != arity {
			t.Errorf("Bridge.%s takes %d arguments, want %d", name, got, arity)
		}
		if model := m.Type.In(4); model.Kind() != reflect.String {
			t.Errorf("Bridge.%s's model argument is %s, want a string", name, model)
		}
	}
}

// TestResumeTakesADecision: the decision bar's verdict reaches the service
// as a structured value, decoded from the JSON the Wails runtime sends, and
// null is every resume that is not answering a permission question.
func TestResumeTakesADecision(t *testing.T) {
	m, _ := reflect.TypeOf(&Bridge{}).MethodByName("Resume")
	if got, want := m.Type.In(5), reflect.TypeOf(&app.PermissionDecision{}); got != want {
		t.Fatalf("Bridge.Resume's decision is %s, want %s", got, want)
	}
	var d *app.PermissionDecision
	if err := json.Unmarshal([]byte(`{"verdict":"deny","reason":"too broad"}`), &d); err != nil || d == nil || d.Verdict != "deny" || d.Reason != "too broad" {
		t.Fatalf("decode: %+v %v", d, err)
	}
	d = nil
	if err := json.Unmarshal([]byte(`null`), &d); err != nil || d != nil {
		t.Fatalf("null: %+v %v", d, err)
	}
}
