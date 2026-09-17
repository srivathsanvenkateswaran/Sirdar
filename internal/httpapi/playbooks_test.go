package httpapi

import (
	"fmt"
	"strconv"
	"testing"
)

const playbooksPath = "/api/workspaces/" + knownWS + "/playbooks"

func TestPlaybooksListsTheWorkspacesPlaybooks(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", playbooksPath, "")

	var got []PlaybookSummary
	decodeJSON(t, w, 200, &got)
	if len(got) != 1 {
		t.Fatalf("rows %+v", got)
	}
	if got[0].Name != "10-helpdesk.md" || got[0].Title != "Helpdesk" || got[0].Order != "10" {
		t.Fatalf("row %+v", got[0])
	}
}

func TestPlaybooksOnAnUnknownWorkspaceIs404(t *testing.T) {
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/nope/playbooks", ""), 404, "not_found")
}

func TestPlaybookReturnsMarkdownAndA404ForAnUnknownName(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", playbooksPath+"/10-helpdesk.md", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	if w.Body.String() != f.playbooks["10-helpdesk.md"] {
		t.Fatalf("body %q", w.Body.String())
	}

	assertError(t, do(t, newFake(), "GET", playbooksPath+"/99-nope.md", ""), 404, "not_found")
}

func TestSavePlaybookWritesTheBodyAndAnswersWithTheRow(t *testing.T) {
	f := newFake()
	w := do(t, f, "PUT", playbooksPath+"/10-helpdesk.md", `{"body":"# Helpdesk\n\nNew words.\n"}`)

	var got PlaybookSummary
	decodeJSON(t, w, 200, &got)
	if got.Name != "10-helpdesk.md" {
		t.Fatalf("row %+v", got)
	}
	if f.gotPlaybookSave.Name != "10-helpdesk.md" || f.gotPlaybookSave.Body != "# Helpdesk\n\nNew words.\n" {
		t.Fatalf("service was asked %+v", f.gotPlaybookSave)
	}
}

func TestSavePlaybookOnAnUnknownNameIs404(t *testing.T) {
	assertError(t, do(t, newFake(), "PUT", playbooksPath+"/99-nope.md", `{"body":"x"}`), 404, "not_found")
}

func TestAddPlaybookCreatesOne(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", playbooksPath, `{"name":"40-database.md","body":"# Database\n"}`)

	var got PlaybookSummary
	decodeJSON(t, w, 200, &got)
	if got.Name != "40-database.md" {
		t.Fatalf("row %+v", got)
	}
	if f.gotPlaybookAdd.Name != "40-database.md" {
		t.Fatalf("service was asked %+v", f.gotPlaybookAdd)
	}
}

// TestAddPlaybookRefusesAMalformedNameEarly pins the 400 the route writes
// itself, with the shape in the message, rather than letting a typo reach
// the service as a 404.
func TestAddPlaybookRefusesAMalformedNameEarly(t *testing.T) {
	for _, name := range []string{"", "logs.md", "10-Logs.md", "10-logs", "1-logs.md", "../secret.md"} {
		f := newFake()
		w := do(t, f, "POST", playbooksPath, `{"name":`+strconv.Quote(name)+`,"body":"x"}`)
		assertError(t, w, 400, "bad_request")
		if f.gotPlaybookAdd.Name != "" {
			t.Fatalf("a refused name reached the service: %+v", f.gotPlaybookAdd)
		}
	}
}

func TestAddPlaybookOnANameAlreadyTakenIs409(t *testing.T) {
	assertError(t, do(t, newFake(), "POST", playbooksPath,
		`{"name":"10-helpdesk.md","body":"x"}`), 409, "conflict")
}

func TestDeletePlaybookIs204AndTellsTheService(t *testing.T) {
	f := newFake()
	w := do(t, f, "DELETE", playbooksPath+"/10-helpdesk.md", "")
	if w.Code != 204 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if f.gotPlaybookDelete != "10-helpdesk.md" {
		t.Fatalf("service was asked to delete %q", f.gotPlaybookDelete)
	}

	assertError(t, do(t, newFake(), "DELETE", playbooksPath+"/99-nope.md", ""), 404, "not_found")
}

func TestScaffoldPlaybooksWritesTheStartingSet(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", playbooksPath+"/scaffold", "")

	var got []PlaybookSummary
	decodeJSON(t, w, 200, &got)
	if !f.scaffolded {
		t.Fatal("the scaffold route did not reach the service")
	}
	if len(got) != 1 {
		t.Fatalf("rows %+v, want the list as it stands afterwards", got)
	}
}

// TestScaffoldIsNotReadAsAPlaybookName keeps the two POST routes apart:
// "scaffold" is a literal segment, so it never arrives at the create route
// as a name.
func TestScaffoldIsNotReadAsAPlaybookName(t *testing.T) {
	f := newFake()
	do(t, f, "POST", playbooksPath+"/scaffold", "")
	if f.gotPlaybookAdd.Name != "" {
		t.Fatalf("scaffold reached the create route as %q", f.gotPlaybookAdd.Name)
	}
}

func TestOpenPlaybookIs204AndTellsTheService(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", playbooksPath+"/10-helpdesk.md/open", "")
	if w.Code != 204 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if f.gotPlaybookOpen != "10-helpdesk.md" {
		t.Fatalf("service was asked to open %q", f.gotPlaybookOpen)
	}
}

// TestOpenPlaybookRefusedOnAListenerOthersCanReach is the same rule the fix
// route keeps: this starts a program on the machine running the server, and
// a listener with no authentication must not offer that to whoever can
// reach the port.
func TestOpenPlaybookRefusedOnAListenerOthersCanReach(t *testing.T) {
	f := newFake()
	w := send(t, New(f, nil, LoopbackOnly(false)), "POST", playbooksPath+"/10-helpdesk.md/open", "",
		map[string]string{"Content-Type": "application/json"})
	assertError(t, w, 403, "forbidden")
	if f.gotPlaybookOpen != "" {
		t.Fatalf("a refused open reached the service: %q", f.gotPlaybookOpen)
	}
}

// TestPlaybookWritesRefusedOutsideSirdar is the workspace whose config
// points `playbooks:` somewhere else: the list reads, and every write is
// the 501 the service gives for something a workspace cannot do at all.
func TestPlaybookWritesRefusedOutsideSirdar(t *testing.T) {
	f := newFake()
	f.playbookErr = fmt.Errorf("%w: this workspace keeps its playbooks outside .sirdar", ErrUnsupported)
	assertError(t, do(t, f, "PUT", playbooksPath+"/10-helpdesk.md", `{"body":"x"}`), 501, "unsupported")
	assertError(t, do(t, f, "POST", playbooksPath, `{"name":"40-database.md","body":"x"}`), 501, "unsupported")
	assertError(t, do(t, f, "POST", playbooksPath+"/scaffold", ""), 501, "unsupported")
}
