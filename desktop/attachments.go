package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

// maxAttachmentDataURL is the largest file the desktop shell will hand the
// webview inline. A data URL is copied twice — once as base64 in the
// bridge's JSON reply, once as the decoded bytes in the webview — so a
// 200 MB video would cost half a gigabyte to show a player. Above the cap
// the pane offers the file to the desktop instead.
const maxAttachmentDataURL = 24 << 20

// Attachments lists the run bundle's attachments for the Bundle pane.
func (b *Bridge) Attachments(ws, runId string) ([]app.Attachment, error) {
	return b.svc.Attachments(ws, runId)
}

// AttachmentDataURL answers with one attachment as a `data:` URL the
// webview can put in an <img>, an <object> or a media element.
//
// A data URL rather than a `file://` one: WKWebView, which is the system
// webview the Wails shell runs on macOS, will not load a file:// subresource
// from a page served on a custom scheme, and relaxing that is a setting
// that applies to the whole webview rather than to one image. The bytes are
// already on this machine and already the operator's, so the cost is a copy
// rather than a permission.
//
// The path is bundle-relative and the service refuses anything that does
// not resolve inside the run's own bundle directory.
func (b *Bridge) AttachmentDataURL(ws, runId, name string) (string, error) {
	full, ctype, err := b.svc.AttachmentFile(ws, runId, name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if info.Size() > maxAttachmentDataURL {
		return "", fmt.Errorf("attachment is %d bytes, over the %d the app can show inline", info.Size(), maxAttachmentDataURL)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	var sb strings.Builder
	sb.WriteString("data:")
	sb.WriteString(ctype)
	sb.WriteString(";base64,")
	sb.WriteString(base64.StdEncoding.EncodeToString(raw))
	return sb.String(), nil
}
