package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
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
