package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// sampleDiff is what the fake service answers both review routes with.
var sampleDiff = RunDiff{
	Base: "aaaa111", Head: "bbbb222",
	Branch: "fix-omni-2510-export", Worktree: "/w/.sirdar/worktrees/r1",
	WorktreePresent: true,
	Files: []DiffFile{
		{Path: "export/csv.go", Status: "modified", Additions: 4, Deletions: 2},
		{Path: "export/stream.go", Status: "added", Additions: 9},
	},
	Patch: "diff --git a/export/csv.go b/export/csv.go\n",
	ETag:  "b7c3",
}

func TestRunDiffCarriesTheWholeShape(t *testing.T) {
	f := newFake()
	f.diff = sampleDiff

	var got RunDiff
	decodeJSON(t, do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/diff", ""), 200, &got)
	if got.Head != sampleDiff.Head || got.Base != sampleDiff.Base || got.Branch != sampleDiff.Branch {
		t.Fatalf("diff %+v", got)
	}
	if !got.WorktreePresent || got.Pushed || got.Truncated {
		t.Errorf("flags %+v", got)
	}
	if len(got.Files) != 2 || got.Files[1].Path != "export/stream.go" || got.Files[1].Status != "added" {
		t.Fatalf("files %+v", got.Files)
	}
	if got.ETag != "b7c3" || got.Patch != sampleDiff.Patch {
		t.Errorf("patch %q etag %q", got.Patch, got.ETag)
	}
}

// The JSON names are the contract desktop/frontend/src/api/types.ts reads.
func TestRunDiffFieldNames(t *testing.T) {
	f := newFake()
	f.diff = sampleDiff
	f.diff.Truncated = true

	var got map[string]any
	decodeJSON(t, do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/diff", ""), 200, &got)
	for _, name := range []string{"base", "head", "branch", "worktree", "worktreePresent",
		"pushed", "files", "patch", "truncated", "etag"} {
		if _, ok := got[name]; !ok {
			t.Errorf("no %q in %v", name, got)
		}
	}
	files, _ := got["files"].([]any)
	if len(files) == 0 {
		t.Fatalf("files %v", got["files"])
	}
	file, _ := files[0].(map[string]any)
	for _, name := range []string{"path", "status", "additions", "deletions"} {
		if _, ok := file[name]; !ok {
			t.Errorf("no %q in %v", name, file)
		}
	}
}

func TestRunDiffReportsARunWithNothingToShow(t *testing.T) {
	f := newFake()
	f.diffErr = fmt.Errorf("%w: r1 is a triage run", ErrNoDiff)
	assertError(t, do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/diff", ""), 404, "no_diff")
}

func TestRunDiffUnknownIDs(t *testing.T) {
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/nope/runs/"+knownRun+"/diff", ""), 404, "not_found")
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/runs/nope/diff", ""), 404, "not_found")
}

func TestDropPassesThePathHunkAndETag(t *testing.T) {
	f := newFake()
	f.diff = sampleDiff

	var got RunDiff
	decodeJSON(t, do(t, f, "POST", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/diff/drop",
		`{"path":"export/csv.go","hunk":0,"etag":"b7c3"}`), 200, &got)
	want := dropCall{Path: "export/csv.go", Hunk: 0, ETag: "b7c3"}
	if f.gotDrop != want {
		t.Fatalf("drop %+v, want %+v", f.gotDrop, want)
	}
	if got.ETag != "b7c3" {
		t.Errorf("the new diff was not returned: %+v", got)
	}
}

func TestDropRejectsBadBodies(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"no path", `{"hunk":0,"etag":"b7c3"}`},
		{"no etag", `{"path":"export/csv.go","hunk":0}`},
		{"unknown field", `{"path":"export/csv.go","hunk":0,"etag":"b7c3","force":true}`},
		{"no body", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertError(t, do(t, newFake(), "POST",
				"/api/workspaces/"+knownWS+"/runs/"+knownRun+"/diff/drop", tc.body), 400, "bad_request")
		})
	}
}

func TestDropReportsARefusalAsAConflict(t *testing.T) {
	f := newFake()
	f.dropErr = fmt.Errorf("%w: the branch is already pushed", ErrRefused)
	assertError(t, do(t, f, "POST", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/diff/drop",
		`{"path":"export/csv.go","hunk":0,"etag":"b7c3"}`), 409, "conflict")
}

// The drop rewrites a commit in the operator's repository, so it is closed
// on a listener other machines can reach, exactly as the fix route is.
func TestDropIsRefusedOnANonLoopbackListener(t *testing.T) {
	f := newFake()
	w := send(t, New(f, nil, LoopbackOnly(false)), "POST",
		"/api/workspaces/"+knownWS+"/runs/"+knownRun+"/diff/drop",
		`{"path":"export/csv.go","hunk":0,"etag":"b7c3"}`,
		map[string]string{"Content-Type": "application/json"})
	assertError(t, w, http.StatusForbidden, "forbidden")
	if f.gotDrop != (dropCall{}) {
		t.Fatal("the drop reached the service on a remote listener")
	}
	if body := w.Body.String(); !strings.Contains(body, "loopback") {
		t.Errorf("the refusal does not say what would make it work: %s", body)
	}
}

// Reading the diff is a read, so it is not closed by the listener gate.
func TestRunDiffStillWorksOnANonLoopbackListener(t *testing.T) {
	f := newFake()
	f.diff = sampleDiff
	w := send(t, New(f, nil, LoopbackOnly(false)), "GET",
		"/api/workspaces/"+knownWS+"/runs/"+knownRun+"/diff", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}
