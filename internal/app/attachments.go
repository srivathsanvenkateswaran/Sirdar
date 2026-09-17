package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Attachment is one file the bundle downloaded, as the inspector draws it:
// the name a reader sees, the bundle-relative path the file is served
// under, what it is, how big it is, and when it was written. The path is
// the identity — it is what AttachmentFile takes back — and never a URL.
type Attachment struct {
	Name string `json:"name"`
	// Path is relative to the bundle directory, always with forward
	// slashes: "attachments/voice-note.m4a".
	Path string `json:"path"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
	// Modified is RFC3339, or empty when the file could not be stat'd.
	Modified string `json:"modified,omitempty"`
	// Transcript is the bundle-relative path of an audio attachment's
	// transcription, when the run made one.
	Transcript string `json:"transcript,omitempty"`
}

// maxAttachmentName is the longest path the attachment routes accept. A
// name longer than this is refused before the filesystem is touched.
const maxAttachmentName = 512

// Attachments lists the run bundle's attachments with their sizes and
// types, read from `bundle/ticket.json` — the bundle's own record of what
// was fetched — and stat'd on disk. A run whose bundle has no ticket.json,
// or none that lists attachments, gets an empty list rather than an error:
// a bundle with no files is an ordinary bundle.
func (s *Service) Attachments(wsID, runID string) ([]Attachment, error) {
	rn, _, err := s.openRun(wsID, runID)
	if err != nil {
		return nil, err
	}
	bundleDir := filepath.Join(rn.Dir, "bundle")
	raw, err := os.ReadFile(filepath.Join(bundleDir, "ticket.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Attachment{}, nil
		}
		return nil, fmt.Errorf("app: read ticket.json: %w", err)
	}
	var bundle ticket.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return nil, fmt.Errorf("app: parse ticket.json: %w", err)
	}
	out := make([]Attachment, 0, len(bundle.Attachments))
	for _, a := range bundle.Attachments {
		rel := filepath.ToSlash(a.Path)
		if rel == "" {
			rel = path.Join("attachments", a.Name)
		}
		// A ticket.json naming a file outside the bundle is a bundle the
		// inspector will not offer: the same rule the file route applies.
		full, err := resolveUnder(bundleDir, rel)
		if err != nil {
			continue
		}
		item := Attachment{
			Name:       a.Name,
			Path:       rel,
			MIME:       a.MIME,
			Transcript: filepath.ToSlash(a.Transcript),
		}
		if item.Name == "" {
			item.Name = path.Base(rel)
		}
		if info, err := os.Stat(full); err == nil {
			item.Size = info.Size()
			item.Modified = wireTime(info.ModTime())
		}
		if item.MIME == "" {
			item.MIME = mimeOf(full)
		}
		out = append(out, item)
	}
	return out, nil
}

// AttachmentFile resolves one bundle-relative attachment path to the file
// on disk and the content type to serve it as.
//
// The path is the whole guard. It must stay under the run's own bundle
// directory after the symlinks on both sides are resolved, so "..", an
// absolute path, a Windows-style path, a NUL and a symlink pointing out of
// the bundle are all refused with ErrNoSuchRun — which the HTTP layer
// answers 404, telling a prober nothing about what is on the disk.
func (s *Service) AttachmentFile(wsID, runID, name string) (string, string, error) {
	rn, _, err := s.openRun(wsID, runID)
	if err != nil {
		return "", "", err
	}
	full, err := resolveUnder(filepath.Join(rn.Dir, "bundle"), name)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() {
		// A directory is not a listing here and a missing file is not a
		// different answer from a refused one.
		return "", "", fmt.Errorf("%w: attachment %q", ErrNoSuchRun, name)
	}
	return full, mimeOf(full), nil
}

// resolveUnder turns a bundle-relative attachment path into an absolute one
// and proves it is inside dir, symlinks and all.
func resolveUnder(dir, name string) (string, error) {
	refused := fmt.Errorf("%w: attachment %q", ErrNoSuchRun, name)
	if name == "" || len(name) > maxAttachmentName {
		return "", refused
	}
	if strings.ContainsRune(name, 0) {
		return "", refused
	}
	// Both separators, because the check must not depend on the platform
	// the path was typed on: "attachments\..\..\etc" is one segment to
	// path.Clean on Linux and three to the Windows filesystem.
	clean := path.Clean("/" + strings.ReplaceAll(name, `\`, "/"))
	if clean == "/" {
		return "", refused
	}
	// Only files under the bundle's attachments directory are served: not
	// ticket.json, not the thread, not the prompt beside them, and not the
	// directory itself — the route has no listing.
	if !strings.HasPrefix(clean, "/attachments/") {
		return "", refused
	}
	full := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(clean, "/")))

	// EvalSymlinks on the bundle root as well, so a run directory reached
	// through a symlinked workspace is not itself read as an escape.
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", refused
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", refused
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", refused
	}
	return real, nil
}

// mimeOf is the type an attachment is served as: the extension's registered
// type, else the first bytes sniffed, else octet-stream. A type the browser
// would execute is never guessed — an .html or .svg attachment from a
// helpdesk is served as text and as an image respectively, never as a
// document that could script.
func mimeOf(full string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(full))); t != "" {
		return safeMIME(t)
	}
	f, err := os.Open(full)
	if err != nil {
		return "application/octet-stream"
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, _ := f.Read(head)
	return safeMIME(http.DetectContentType(head[:n]))
}

// safeMIME downgrades the types a webview would run as a document in the
// app's own origin.
func safeMIME(t string) string {
	base, _, err := mime.ParseMediaType(t)
	if err != nil {
		base = t
	}
	switch base {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "application/xml", "text/xml":
		return "text/plain; charset=utf-8"
	case "application/javascript", "text/javascript":
		return "text/plain; charset=utf-8"
	}
	return t
}
