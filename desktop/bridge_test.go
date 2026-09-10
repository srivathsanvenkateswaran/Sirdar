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
	"StartTriage",
	"StartRCA",
	"Resume",
	"Cancel",
	"Register",
	"Doctor",
	"Quota",
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
