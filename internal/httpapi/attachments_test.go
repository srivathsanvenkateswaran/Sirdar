package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeWithAttachment gives the fake service one bundle attachment, backed
// by a real file, and answers with the fake.
func fakeWithAttachment(t *testing.T) *fake {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, "screenshot.png")
	if err := os.WriteFile(full, []byte("pretend png"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	f.attachments = []Attachment{{
		Name: "screenshot.png", Path: "attachments/screenshot.png",
		MIME: "image/png", Size: 11, Modified: "2026-09-17T09:15:00Z",
	}}
	f.attachmentFiles = map[string]string{"attachments/screenshot.png": full}
	return f
}

func TestAttachmentsList(t *testing.T) {
	f := fakeWithAttachment(t)
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/bundle/attachments", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got []Attachment
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "screenshot.png" || got[0].Size != 11 {
		t.Fatalf("attachments %+v", got)
	}
	// The listing says where the file is inside the bundle and never a URL
	// the pane would have to print beside the name.
	if got[0].Path != "attachments/screenshot.png" {
		t.Fatalf("path %q", got[0].Path)
	}
}

func TestAttachmentsListIsEmptyRatherThanNull(t *testing.T) {
	w := do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/bundle/attachments", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("status %d body %q", w.Code, w.Body.String())
	}
}

func TestAttachmentFileIsServedFromTheBundle(t *testing.T) {
	f := fakeWithAttachment(t)
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/bundle/attachments/screenshot.png", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "pretend png" {
		t.Fatalf("body %q", w.Body.String())
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff header %q", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Fatalf("content security policy %q", got)
	}
}

// Everything the route will not serve answers the same 404, so nothing is
// learned from the difference between a file that is not there and a path
// that was refused.
func TestAttachmentFileRefusesAnythingButAName(t *testing.T) {
	f := fakeWithAttachment(t)
	base := "/api/workspaces/" + knownWS + "/runs/" + knownRun + "/bundle/attachments"
	for _, target := range []string{
		base + "/../../prompt.md",
		base + "/%2e%2e/%2e%2e/prompt.md",
		base + "/..%2f..%2fprompt.md",
		base + "/nothing.png",
		base + "//etc/passwd",
		base + "/sub/dir/file.png",
	} {
		w := do(t, f, "GET", target, "")
		if w.Code == 200 {
			t.Errorf("%s was served: %q", target, w.Body.String())
		}
	}
}

// TestContextRouteCannotReachAttachments and its mirror pin that each
// bundle route passes its own kind down to AttachmentFile: a name that
// would otherwise collapse across the boundary — context/ reached through
// the attachments route's own join, or the reverse — is refused the same
// way anything else outside the bundle is, rather than served out of the
// other directory.
func TestContextRouteCannotReachAttachments(t *testing.T) {
	f := fakeWithAttachment(t)
	full := f.attachmentFiles["attachments/screenshot.png"]
	f.attachmentFiles["context/../attachments/screenshot.png"] = full

	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/bundle/context/..%2Fattachments%2Fscreenshot.png", "")
	if w.Code == 200 {
		t.Fatalf("the context route served an attachment: %q", w.Body.String())
	}
}

func TestAttachmentsRouteCannotReachContext(t *testing.T) {
	f := fakeWithAttachment(t)
	f.attachmentFiles["context/index.json"] = f.attachmentFiles["attachments/screenshot.png"]

	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/bundle/attachments/..%2Fcontext%2Findex.json", "")
	if w.Code == 200 {
		t.Fatalf("the attachments route served a gathered file: %q", w.Body.String())
	}
}

func TestAttachmentRoutesAreScopedToTheRun(t *testing.T) {
	f := fakeWithAttachment(t)
	for _, target := range []string{
		"/api/workspaces/nope/runs/" + knownRun + "/bundle/attachments",
		"/api/workspaces/" + knownWS + "/runs/nope/bundle/attachments",
		"/api/workspaces/" + knownWS + "/runs/nope/bundle/attachments/screenshot.png",
	} {
		if w := do(t, f, "GET", target, ""); w.Code != 404 {
			t.Errorf("%s: status %d, want 404", target, w.Code)
		}
	}
}
