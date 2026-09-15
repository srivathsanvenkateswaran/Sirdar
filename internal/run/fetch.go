// Package run's bundle assembly: the Fetcher reads a ticket out of the
// configured tracker and helpdesk, applies the size and type limits to what
// it downloaded, and — when the caller named an instant — cuts the result
// back to how the ticket looked then.
//
// It is a type of its own rather than a Runner method because two callers
// need it. A run fetches a bundle on its way to a session; `sirdar golden
// add --retro` fetches one with no run and no session at all, to build a
// golden entry out of a ticket whose fix has already shipped.
package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
	"github.com/srivathsanvenkateswaran/sirdar/internal/transcribe"
)

// Fetcher assembles one ticket bundle from the sources a workspace
// configures. Tracker and Helpdesk may each be nil when the workspace names
// only the other; a Fetcher with neither fails every call.
type Fetcher struct {
	Config   *config.Config
	Tracker  source.Tracker  // may be nil
	Helpdesk source.Helpdesk // may be nil
	Stderr   io.Writer       // where a failed cleanup is reported; nil discards

	// AsOf, when non-zero, assembles the bundle as the ticket stood at
	// that instant instead of as it stands now: later messages and the
	// attachments only they pointed at are dropped, and every
	// pull-request reference is redacted. See ticket.ApplyAsOf.
	AsOf time.Time

	// Env is the base environment a transcription command's child process
	// is filtered down from; os.Environ() when nil. It has no effect
	// unless the workspace configured attachments.transcribe.
	Env []string

	// Now is the clock a transcription batch's budget is measured
	// against; time.Now when nil.
	Now func() time.Time

	// warnings is what this fetch has to tell the operator. It is a
	// superset of the bundle's own warnings: the cutoff's counts go here
	// and not into the bundle, because the prompt quotes the bundle's
	// warnings to the agent, and "four comments were dropped" tells a
	// session being measured on a retrospective ticket that there is a
	// later conversation it is not being shown.
	warnings []string
}

// Fetch reads the ticket into a bundle, downloading its attachments under
// bundleDir, and returns the bundle together with the warnings the
// operator should see. The bundle is not written to disk: the caller
// decides where it goes.
func (f *Fetcher) Fetch(ctx context.Context, key, bundleDir string) (ticket.Bundle, []string, error) {
	f.warnings = nil
	if err := os.MkdirAll(filepath.Join(bundleDir, "attachments"), 0o755); err != nil {
		return ticket.Bundle{}, nil, fmt.Errorf("run: create bundle dir: %w", err)
	}
	b, err := f.fetchBundle(ctx, key, bundleDir)
	if err != nil {
		return b, f.warnings, err
	}
	if !f.AsOf.IsZero() {
		f.applyCutoff(&b, bundleDir)
	}
	return b, f.warnings, nil
}

// applyCutoff cuts the bundle back to the as-of instant and deletes the
// files the dropped attachments named, so nothing the thread no longer
// mentions is left in the directory for a session to open anyway.
func (f *Fetcher) applyCutoff(b *ticket.Bundle, bundleDir string) {
	c, dropped := ticket.ApplyAsOf(b, f.AsOf)
	for _, a := range dropped {
		path := a.Path
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(bundleDir, path)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(f.stderr(), "remove attachment %s: %v\n", path, err)
		}
	}
	f.note(fmt.Sprintf("bundle assembled as of %s: %d later message(s) and %d attachment(s) dropped, %d pull-request reference(s) redacted",
		f.AsOf.Format(time.RFC3339), c.CommentsDropped, c.AttachmentsDropped, c.PRLinks))
}

func (f *Fetcher) stderr() io.Writer {
	if f.Stderr != nil {
		return f.Stderr
	}
	return io.Discard
}

// warn records something the agent has to know as well as the operator: it
// goes into the bundle, which the prompt quotes, and into this fetch's
// warnings, which the run state records.
func (f *Fetcher) warn(b *ticket.Bundle, msg string) {
	b.Warnings = append(b.Warnings, msg)
	f.warnings = append(f.warnings, msg)
}

// note records something only the operator has to know.
func (f *Fetcher) note(msg string) { f.warnings = append(f.warnings, msg) }

// fetchBundle reads the tracker record, then the helpdesk record, thread
// and attachments that go with it. An attachment failure is a warning the
// prompt carries, not a run failure.
func (f *Fetcher) fetchBundle(ctx context.Context, key, bundleDir string) (ticket.Bundle, error) {
	var b ticket.Bundle

	if f.Tracker != nil {
		tt, err := f.Tracker.Get(ctx, key)
		if err != nil {
			return b, fmt.Errorf("tracker %s: %w", key, err)
		}
		b.Tracker = &tt
		if b.Tracker.HelpdeskRef == "" {
			f.applyHelpdeskRefFallback(&b, b.Tracker)
		}
		// A tracker adapter degrades the same way a helpdesk one does — a
		// comment page it could not read, a field this workspace does not
		// expose, an attachment it skipped — and reports none of it in the
		// error. Draining its warnings here is the only thing that puts
		// them in front of the agent.
		if w, ok := f.Tracker.(source.Warner); ok {
			for _, msg := range w.WarningsFor(key) {
				f.warn(&b, msg)
			}
		}
	}

	helpdeskID := key
	if b.Tracker != nil && b.Tracker.HelpdeskRef != "" {
		helpdeskID = b.Tracker.HelpdeskRef
	}

	if f.Helpdesk != nil {
		ht, err := f.Helpdesk.Get(ctx, helpdeskID)
		if err != nil {
			return b, fmt.Errorf("helpdesk %s: %w", helpdeskID, err)
		}
		b.Helpdesk = &ht

		thread, err := f.Helpdesk.Threads(ctx, helpdeskID)
		if err != nil {
			return b, fmt.Errorf("helpdesk threads %s: %w", helpdeskID, err)
		}
		b.Thread = thread

		atts, err := f.Helpdesk.Attachments(ctx, helpdeskID, filepath.Join(bundleDir, "attachments"))
		if err != nil {
			f.warn(&b, fmt.Sprintf("attachments for helpdesk ticket %s could not be downloaded: %v", helpdeskID, err))
		} else {
			b.Attachments = f.keepReadableAttachments(ctx, &b, bundleDir, atts)
		}
		// A helpdesk that downloaded some attachments and not others
		// returns no error at all, so ask it what it skipped: the agent
		// has to know an attachment is missing before it reasons from
		// the ones that arrived.
		if w, ok := f.Helpdesk.(source.Warner); ok {
			for _, msg := range w.WarningsFor(helpdeskID) {
				f.warn(&b, msg)
			}
		}
	}

	if b.Tracker == nil && b.Helpdesk == nil {
		return b, fmt.Errorf("no ticket source configured; set sources.tracker or sources.helpdesk")
	}
	return b, nil
}

// applyHelpdeskRefFallback fills in a tracker ticket's HelpdeskRef from its
// description, using the regex rule the workspace configured under
// sources.tracker.helpdeskRef. It is only reached when the adapter reported
// no reference of its own, so a tracker with native linkage — a Jira
// Service Management request, a Linear customer request, an Azure DevOps
// hyperlink — is never overridden by a guess made from prose.
//
// Both patterns were compiled once at config load, so a compile failure
// here cannot happen for a config that loaded; it is treated as no match
// rather than as a run failure. A pattern that matched while idPattern did
// not is worth a warning: the description does name a helpdesk ticket and
// the rule could not turn it into an id, which is a rule to fix rather than
// a ticket without a link.
func (f *Fetcher) applyHelpdeskRefFallback(b *ticket.Bundle, tt *ticket.TrackerTicket) {
	if f.Config == nil || f.Config.Sources.Tracker == nil {
		return
	}
	h := f.Config.Sources.Tracker.HelpdeskRef
	if h == nil || h.Pattern == "" {
		return
	}
	re, err := regexp.Compile(h.Pattern)
	if err != nil {
		return
	}
	m := re.FindStringSubmatch(tt.Description)
	if len(m) < 2 || m[1] == "" {
		return
	}
	ref := m[1]

	if h.IDPattern != "" {
		idRe, err := regexp.Compile(h.IDPattern)
		if err != nil {
			return
		}
		im := idRe.FindStringSubmatch(ref)
		if len(im) < 2 || im[1] == "" {
			// The captured value came out of a ticket description, so its
			// length is whoever wrote that description's choice, not a
			// bounded field. A warning line goes into the prompt and the
			// run state; a paragraph of prose does not belong in either.
			f.warn(b, fmt.Sprintf("helpdeskRef.pattern matched %q in the description but idPattern did not; no helpdesk ticket was read", truncate(ref, 120)))
			return
		}
		ref = im[1]
	}
	tt.HelpdeskRef = ref
}

// readableMIME reports whether an agent session can actually open a file
// of this type. Everything else — audio, video, and anything the helpdesk
// labelled with a type nobody can read — is evidence the session cannot
// reach, and is better named in a warning than left in the bundle for it
// to hunt for a transcoder over.
//
// Audio is the one type with a second chance: a workspace that configured
// attachments.transcribe has the file turned into text before this
// judgement is final (see keepReadableAttachments).
func readableMIME(mime string) bool {
	switch {
	case mime == "":
		return false
	case strings.HasPrefix(mime, "image/"), strings.HasPrefix(mime, "text/"):
		return true
	}
	switch mime {
	case "application/pdf", "application/json", "application/csv", "application/xml",
		"application/zip", "application/x-zip-compressed":
		return true
	}
	return false
}

// attachmentMIME is the type an attachment should be judged by: what its
// filename extension says, falling back to what the helpdesk's download
// response claimed. The extension leads because the claim is unreliable —
// Zoho served this workspace's 16 MB .mp4 as text/html — and because a
// wrong claim in that direction is the one that matters: it would put a
// file the session cannot open back into the bundle.
func attachmentMIME(a ticket.Attachment) string {
	if byExt := baseMIME(mime.TypeByExtension(strings.ToLower(filepath.Ext(a.Name)))); byExt != "" {
		return byExt
	}
	return baseMIME(a.MIME)
}

// baseMIME strips any ";charset=..." parameters from a media type.
func baseMIME(t string) string {
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

// keepReadableAttachments enforces the size cap and the type allow-list on
// what the helpdesk downloaded, and, for audio, gives the workspace's
// transcription command a chance to turn a file the session cannot open
// into one it can. A file that fails is deleted from the bundle — leaving
// it there means the session can still read 17 MB of mp4 into its context
// — and named, with its size and a reason, in a warning the prompt and the
// run state both carry, and in the bundle's SkippedAttachments list the
// note's "Attachments not reviewed" section renders from.
//
// A transcribed voice note stays in the bundle beside its transcript. It
// is still unreadable, but it is now the source of a quotation in the
// note, and an engineer who wants to check that quotation has to be able
// to listen to it.
func (f *Fetcher) keepReadableAttachments(ctx context.Context, b *ticket.Bundle, bundleDir string, atts []ticket.Attachment) []ticket.Attachment {
	max := f.Config.AttachmentMaxBytes()
	tx := f.transcriber(b)
	cappedReported := false

	kept := make([]ticket.Attachment, 0, len(atts))
	for _, a := range atts {
		path := a.Path
		if path != "" && !filepath.IsAbs(path) {
			path = filepath.Join(bundleDir, path)
		}
		size := int64(-1)
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
		}

		mimeType := attachmentMIME(a)
		var reason string
		switch {
		case size > max:
			reason = fmt.Sprintf("over the %s limit", humanBytes(max))
			f.warn(b, fmt.Sprintf("attachment %q (%s, %s) is %s and was not kept; its contents are unread",
				a.Name, mimeType, humanBytes(size), reason))
		case readableMIME(mimeType):
			kept = append(kept, a)
			continue
		case tx != nil && a.Path != "" && tx.Handles(a.Name, mimeType):
			res, err := tx.Run(ctx, path)
			if err == nil {
				if err := f.writeTranscript(bundleDir, &a, res); err != nil {
					reason = "transcribed, but the transcript could not be written"
					f.warn(b, fmt.Sprintf("attachment %q was transcribed but the transcript could not be written: %v; its contents are unread", a.Name, err))
					break
				}
				kept = append(kept, a)
				continue
			}
			if errors.Is(err, transcribe.ErrOverCap) {
				// One line for the cap, however many files follow it:
				// the per-file warnings below already name each one.
				if !cappedReported {
					f.warn(b, fmt.Sprintf("transcription stopped: %v; the audio after this point is unread", err))
					cappedReported = true
				}
				reason = "not transcribed before the transcription budget ran out"
				f.warn(b, fmt.Sprintf("attachment %q (%s, %s) was not transcribed and was not kept; its contents are unread",
					a.Name, mimeType, humanBytes(size)))
				break
			}
			reason = fmt.Sprintf("could not be transcribed: %v", err)
			f.warn(b, fmt.Sprintf("attachment %q (%s, %s) could not be transcribed and was not kept; its contents are unread: %v",
				a.Name, mimeType, humanBytes(size), err))
		default:
			reason = "cannot be opened in this session"
			f.warn(b, fmt.Sprintf("attachment %q (%s, %s) %s and was not kept; its contents are unread",
				a.Name, mimeType, humanBytes(size), reason))
		}
		b.SkippedAttachments = append(b.SkippedAttachments, ticket.SkippedAttachment{
			Name: a.Name, Type: mimeType, Size: humanBytes(size), Reason: reason,
		})
		if path != "" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(f.stderr(), "remove attachment %s: %v\n", path, err)
			}
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// transcriber builds this fetch's audio transcriber, or nil when the
// workspace configured none. A command that cannot be turned into an argv
// is a warning and no transcription: config validation already refuses
// one, so this is the hand-built Fetcher a test or an embedder assembles.
func (f *Fetcher) transcriber(b *ticket.Bundle) *transcribe.Transcriber {
	opts, ok := f.Config.TranscribeOptions()
	if !ok {
		return nil
	}
	opts.Env = f.Env
	opts.Now = f.Now
	tx, err := transcribe.New(opts)
	if err != nil {
		f.warn(b, fmt.Sprintf("attachments.transcribe.command is unusable, so no audio was transcribed: %v", err))
		return nil
	}
	return tx
}

// writeTranscript files one transcript beside its audio in the bundle and
// records it on the attachment, so the manifest, the prompt and the
// rendered conversation all point at the same file. The transcript is
// capped at the workspace's attachments.maxBytes, the same limit every
// other kept attachment answers to: transcribe.Transcriber already bounds
// what it holds in memory to 4 MiB, but a workspace that configured a
// smaller attachments.maxBytes should not get a bundle file bigger than
// what everything else in it is held to.
func (f *Fetcher) writeTranscript(bundleDir string, a *ticket.Attachment, res transcribe.Result) error {
	rel := a.Path + ".transcript.txt"
	body := transcribe.Header(a.Name, res) + "\n\n" + res.Text + "\n"
	if max := f.Config.AttachmentMaxBytes(); max > 0 && int64(len(body)) > max {
		body = truncate(body, int(max)) + "\n"
	}
	if err := os.WriteFile(filepath.Join(bundleDir, rel), []byte(body), 0o644); err != nil {
		return err
	}
	a.Transcript = rel
	a.TranscriptLanguage = res.Language
	return nil
}

// humanBytes renders a byte count the way a warning should read.
func humanBytes(n int64) string {
	switch {
	case n < 0:
		return "size unknown"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
}
