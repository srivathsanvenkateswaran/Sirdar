package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bundleWithAttachment lays out a run bundle with one attachment in it and
// one file outside it, and answers with the bundle directory and the
// outside file's path.
func bundleWithAttachment(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bundle := filepath.Join(dir, "run", "bundle")
	if err := os.MkdirAll(filepath.Join(bundle, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "attachments", "screenshot.png"), []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "ticket.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("not yours"), 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle, secret
}

func TestResolveUnderServesAnAttachment(t *testing.T) {
	bundle, _ := bundleWithAttachment(t)
	full, err := resolveUnder(bundle, "attachments/screenshot.png")
	if err != nil {
		t.Fatalf("resolveUnder: %v", err)
	}
	if !strings.HasSuffix(full, filepath.Join("attachments", "screenshot.png")) {
		t.Fatalf("resolved to %q", full)
	}
}

func TestResolveUnderRefusesEverythingOutsideTheBundle(t *testing.T) {
	bundle, secret := bundleWithAttachment(t)
	cases := map[string]string{
		"parent traversal":         "attachments/../../secret.txt",
		"bare traversal":           "../secret.txt",
		"absolute path":            secret,
		"absolute unix path":       "/etc/passwd",
		"windows separators":       `attachments\..\..\secret.txt`,
		"the bundle's own files":   "ticket.json",
		"the thread":               "thread.md",
		"the directory itself":     "attachments",
		"empty":                    "",
		"a NUL":                    "attachments/scr\x00eenshot.png",
		"a name that is only dots": "attachments/../..",
		"over the length cap":      "attachments/" + strings.Repeat("a", maxAttachmentName),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := resolveUnder(bundle, path); err == nil {
				t.Fatalf("resolveUnder(%q) was allowed", path)
			}
		})
	}
}

func TestResolveUnderRefusesASymlinkOutOfTheBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows")
	}
	bundle, secret := bundleWithAttachment(t)
	link := filepath.Join(bundle, "attachments", "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, err := resolveUnder(bundle, "attachments/leak.txt"); err == nil {
		t.Fatal("a symlink pointing out of the bundle was served")
	}

	// A symlink to a directory outside the bundle is the same refusal, so
	// a whole tree cannot be grafted in.
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(bundle, "attachments", "out")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, err := resolveUnder(bundle, "attachments/out/note.txt"); err == nil {
		t.Fatal("a file under a symlinked directory was served")
	}
}

func TestResolveUnderAllowsASymlinkedBundleRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows")
	}
	bundle, _ := bundleWithAttachment(t)
	// The workspace itself reached through a symlink — a common macOS
	// layout — must not read as an escape.
	alias := filepath.Join(t.TempDir(), "bundle")
	if err := os.Symlink(bundle, alias); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, err := resolveUnder(alias, "attachments/screenshot.png"); err != nil {
		t.Fatalf("a symlinked bundle root was refused: %v", err)
	}
}

func TestMIMEIsNeverSomethingTheWebviewWouldRun(t *testing.T) {
	dir := t.TempDir()
	for name, want := range map[string]string{
		"page.html":  "text/plain",
		"vector.svg": "text/plain",
		"app.js":     "text/plain",
		"photo.png":  "image/png",
		"notes.txt":  "text/plain",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("hello"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := mimeOf(filepath.Join(dir, name))
		if !strings.HasPrefix(got, want) {
			t.Errorf("%s is served as %q, wanted %q", name, got, want)
		}
	}
}
