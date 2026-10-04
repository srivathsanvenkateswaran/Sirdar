package run

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ghrepo"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// ReportedBundle is a ticket bundle built by the caller from where the
// problem was reported — a Slack thread with no tracker key or helpdesk
// number in it — rather than fetched from a tracker and helpdesk. Bundle
// carries Reported and no Tracker or Helpdesk.
type ReportedBundle struct {
	Bundle ticket.Bundle
	// Files are the files the report listed.
	Files []ReportedFile
	// Download saves one file into the bundle. Nil — a thread read
	// through the Slack MCP, which hands out no file access — names every
	// file as unread, with UnreadReason.
	Download     func(ctx context.Context, url, dest string, max int64) error
	UnreadReason string
}

// ReportedFile is one file a report listed: its name, and the URL a
// Download can fetch it from, when there is one.
type ReportedFile struct {
	Name string
	URL  string
}

// stageReported writes a reported bundle into the run: its files
// downloaded when it can, named as unread when it cannot, and the same size
// and type limits a fetched attachment answers to.
func (r *Runner) stageReported(ctx context.Context, p *prepared, rb *ReportedBundle) (ticket.Bundle, error) {
	dir := p.run.BundleDir()
	attDir := filepath.Join(dir, "attachments")
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		return ticket.Bundle{}, fmt.Errorf("run: create bundle dir: %w", err)
	}
	b := rb.Bundle
	f := &Fetcher{Config: r.Config, Stderr: r.stderr(), Env: r.Env, Now: r.now}
	f.warnings = nil

	var atts []ticket.Attachment
	used := map[string]bool{}
	for i, file := range rb.Files {
		name := strings.TrimSpace(file.Name)
		if name == "" {
			continue
		}
		reason := rb.UnreadReason
		if reason == "" {
			reason = "no way to download it was configured"
		}
		if rb.Download != nil && file.URL != "" {
			local := uniqueName(httpx.SanitizeName(name), used)
			rel := filepath.Join("attachments", local)
			if err := rb.Download(ctx, file.URL, filepath.Join(dir, rel), r.Config.AttachmentMaxBytes()); err == nil {
				atts = append(atts, ticket.Attachment{ID: "slack-file-" + strconv.Itoa(i+1), Name: name, MIME: mime.TypeByExtension(filepath.Ext(name)), Path: rel})
				continue
			} else {
				reason = "could not be downloaded: " + err.Error()
			}
		} else if rb.Download != nil {
			reason = "the message gives no download link for it"
		}
		f.warn(&b, fmt.Sprintf("file %q from the Slack thread is not in the bundle (%s); its contents are unread", name, reason))
		b.SkippedAttachments = append(b.SkippedAttachments, ticket.SkippedAttachment{Name: name, Size: "size unknown", Reason: reason})
	}
	b.Attachments = f.keepReadableAttachments(ctx, &b, dir, atts)
	p.state.Warnings = append(p.state.Warnings, f.warnings...)
	p.state.Warnings = append(p.state.Warnings, "no tracker or helpdesk was read: the ticket was reported in Slack and has none yet")
	return b, ticket.WriteBundle(dir, b)
}

// uniqueName keeps two files of the same name from landing on one path.
func uniqueName(name string, used map[string]bool) string {
	if name == "" {
		name = "file"
	}
	out := name
	ext := filepath.Ext(name)
	for n := 2; used[strings.ToLower(out)]; n++ {
		out = strings.TrimSuffix(name, ext) + "-" + strconv.Itoa(n) + ext
	}
	used[strings.ToLower(out)] = true
	return out
}

// otherRepos is what the ticket and the thread say about repositories:
// the GitHub ones they name that are not the workspace's origin, and the
// origin itself.
func otherRepos(ctx context.Context, root string, b ticket.Bundle, slackMD string) ([]string, string) {
	origin := ghrepo.Origin(ctx, root)
	if origin == "" {
		return nil, ""
	}
	var text strings.Builder
	if t := b.Tracker; t != nil {
		text.WriteString(t.Title + "\n" + t.Description + "\n")
	}
	if h := b.Helpdesk; h != nil {
		text.WriteString(h.Subject + "\n")
	}
	if rp := b.Reported; rp != nil {
		text.WriteString(rp.Description + "\n" + strings.Join(rp.Links, "\n") + "\n")
	}
	for _, m := range b.Thread {
		text.WriteString(m.Text + "\n")
	}
	text.WriteString(slackMD)
	return ghrepo.Foreign(text.String(), origin), origin
}
