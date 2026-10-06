package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeWithGathered gives the fake service one gathered file, backed by a
// real file, and answers with the fake.
func fakeWithGathered(t *testing.T) *fake {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, "a.md")
	if err := os.WriteFile(full, []byte("gathered body"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	f.gathered = []GatheredInfo{{
		Name: "pr-x-1", File: "context/a.md", Kind: "pr", Status: "ok",
		Call: "gh pr view 1", Bytes: 13, TookMs: 42,
	}}
	f.attachmentFiles = map[string]string{"context/a.md": full}
	return f
}

func TestGatheredList(t *testing.T) {
	f := fakeWithGathered(t)
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/bundle/context", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got []GatheredInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "pr-x-1" || got[0].Kind != "pr" {
		t.Fatalf("gathered %+v", got)
	}
}

func TestGatheredFileIsServedFromTheBundle(t *testing.T) {
	f := fakeWithGathered(t)
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/bundle/context/a.md", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "gathered body" {
		t.Fatalf("body %q", w.Body.String())
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff header %q", got)
	}
}

// A traversal under the context route answers the same 404 an attachment
// traversal does, so a prober learns nothing from the difference.
func TestGatheredFileRefusesATraversal(t *testing.T) {
	f := fakeWithGathered(t)
	base := "/api/workspaces/" + knownWS + "/runs/" + knownRun + "/bundle/context"
	w := do(t, f, "GET", base+"/..%2Fticket.json", "")
	if w.Code != 404 {
		t.Errorf("%s: status %d, want 404", base+"/..%2Fticket.json", w.Code)
	}
}
