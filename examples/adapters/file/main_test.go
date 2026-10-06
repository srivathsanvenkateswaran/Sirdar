package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

func TestSafeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"shot.png", "shot.png"},
		{"../../evil.txt", "evil.txt"},
		{"/etc/passwd", "passwd"},
		{"..", "attachment"},
		{"", "attachment"},
		{".", "attachment"},
		{"لقطة.png", "لقطة.png"},
		{"bad\x00name.png", "badname.png"},
	}
	for _, tc := range cases {
		if got := safeName(tc.in); got != tc.want {
			t.Errorf("safeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestWriteAttachmentsStaysUnderDir covers a fixture whose attachment name
// climbs out of the destination directory. The adapter writes wherever the
// name points unless the name is reduced to one component first.
func TestWriteAttachmentsStaysUnderDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bundle", "attachments")

	atts, err := writeAttachments([]attachmentFixture{{
		ID:            "a1",
		Name:          "../../escaped.txt",
		MIME:          "text/plain",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("payload")),
	}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 {
		t.Fatalf("attachments: %+v", atts)
	}
	if atts[0].Path != "attachments/1-escaped.txt" {
		t.Fatalf("attachment path %q", atts[0].Path)
	}
	if _, err := os.Stat(filepath.Join(dir, "1-escaped.txt")); err != nil {
		t.Fatalf("the attachment was not written under the destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("the attachment escaped the destination directory: %v", err)
	}
}

// The fixture's methods are what describe advertises, so a test can stand
// up an adapter that answers tracker.outcome and one that predates it.
func TestDescribeCarriesMethods(t *testing.T) {
	resp := dispatch(fixture{Methods: []string{"tracker.outcome"}}, plugin.Request{ID: 1, Method: "describe"})
	var d plugin.Describe
	if err := json.Unmarshal(resp.Result, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Methods) != 1 || d.Methods[0] != "tracker.outcome" {
		t.Errorf("methods %v", d.Methods)
	}

	resp = dispatch(fixture{}, plugin.Request{ID: 2, Method: "describe"})
	var old plugin.Describe
	if err := json.Unmarshal(resp.Result, &old); err != nil {
		t.Fatal(err)
	}
	if len(old.Methods) != 0 {
		t.Errorf("a fixture with no methods advertises %v", old.Methods)
	}
}

func TestTrackerOutcomeAnswers(t *testing.T) {
	fx := fixture{Outcomes: map[string]ticket.Outcome{
		"OMNI-1": {Key: "OMNI-1", Status: "Done", PullRequests: []string{"https://github.com/o/r/pull/1"}},
	}}
	resp := dispatch(fx, plugin.Request{ID: 1, Method: "tracker.outcome", Params: json.RawMessage(`{"key":"OMNI-1"}`)})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var o ticket.Outcome
	if err := json.Unmarshal(resp.Result, &o); err != nil {
		t.Fatal(err)
	}
	if o.Key != "OMNI-1" || o.Status != "Done" || len(o.PullRequests) != 1 {
		t.Errorf("%+v", o)
	}

	resp = dispatch(fx, plugin.Request{ID: 2, Method: "tracker.outcome", Params: json.RawMessage(`{"key":"OMNI-404"}`)})
	if resp.Error == nil || resp.Error.Code != source.NotFound {
		t.Errorf("an unknown key answers %+v", resp.Error)
	}
}
