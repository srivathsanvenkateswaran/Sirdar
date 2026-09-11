package run

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// prepared is a run that has its directory, bundle and prompt on disk and
// is ready for an agent session.
type prepared struct {
	run        store.Run
	state      store.State
	kind       store.Kind
	bundle     ticket.Bundle
	promptText string

	// rca runs only: the triage note under review, the copy of it in the
	// notes directory (may be empty), and the wiki-link stem of that copy.
	triageNotePath string
	triageNoteCopy string
	triageLink     string
}

// threadHeadLines is how much of the conversation the prompt quotes inline;
// the agent reads the rest from the bundle directory.
const threadHeadLines = 40

// keyPattern is what a ticket key may contain. The key comes off the command
// line and goes straight into the run directory path and the note filename,
// so `sirdar triage ../../etc` has to be refused before anything is created
// rather than quietly writing outside the workspace.
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validateKey rejects a ticket key that cannot safely become a path segment.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("run: the ticket key is empty")
	}
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("run: %q is not a usable ticket key: only letters, digits, '.', '_' and '-' are allowed", key)
	}
	if strings.Contains(key, "..") {
		return fmt.Errorf("run: %q is not a usable ticket key: it must not contain \"..\"", key)
	}
	return nil
}

// prepare creates the run directory, fetches the ticket, writes the bundle
// and assembles the prompt. Every error it returns happens before an agent
// process exists, so the caller fails the run in "preparing".
func (r *Runner) prepare(ctx context.Context, key string, kind store.Kind, o Options, rca *RCAOptions) (*prepared, error) {
	cfg := r.Config
	now := r.now()

	if err := validateKey(key); err != nil {
		return nil, err
	}

	rn, err := store.Create(cfg.Root, key, now)
	if err != nil {
		return nil, err
	}

	model := o.Model
	if model == "" {
		model = cfg.Model
	}
	p := &prepared{run: rn, kind: kind}
	p.state = store.State{
		RunID:     filepath.Base(rn.Dir),
		Key:       key,
		Kind:      kind,
		Status:    store.StatusPreparing,
		Provider:  r.providerName(),
		Model:     model,
		StartedAt: now,
		UpdatedAt: now,
	}
	p.state.Budget.MaxTurns = cfg.Budget.MaxTurns
	p.state.Budget.MaxMinutes = cfg.Budget.MaxMinutes
	p.state.Budget.MaxUSD = cfg.Budget.MaxUSD
	if err := rn.WriteState(p.state); err != nil {
		return p, err
	}

	// An rca run reviews a triage note, so refuse before doing any work
	// when there is none.
	if kind == store.KindRCA {
		notePath, err := store.LatestNote(cfg.Root, key, store.KindTriage)
		if err != nil {
			return p, fmt.Errorf("no triage note for %s; run triage first", key)
		}
		p.triageNotePath = notePath
		p.triageNoteCopy, p.triageLink = triageNoteCopy(cfg.Root, notePath)
	}

	bundle, err := r.fetchBundle(ctx, key, p)
	if err != nil {
		return p, err
	}
	p.bundle = bundle
	if err := ticket.WriteBundle(rn.BundleDir(), bundle); err != nil {
		return p, err
	}

	playbooks, err := prompt.LoadPlaybooks(cfg.ExpandPath(cfg.Playbooks))
	if err != nil {
		return p, err
	}
	threadHead, truncated, err := readThreadHead(rn.BundleDir())
	if err != nil {
		return p, err
	}

	in := prompt.TriageInput{
		Bundle:              bundle,
		BundleDir:           rn.BundleDir(),
		Playbooks:           playbooks,
		ThreadHead:          threadHead,
		ThreadHeadTruncated: truncated,
		NotesLanguage:       cfg.NotesLanguage(),
		CustomerLanguage:    cfg.CustomerLanguage(),
	}

	switch kind {
	case store.KindTriage:
		p.promptText = prompt.Triage(in)
	case store.KindRCA:
		rcaIn, err := r.rcaInput(ctx, p, in, rca)
		if err != nil {
			return p, err
		}
		p.promptText = prompt.RCA(rcaIn)
	default:
		return p, fmt.Errorf("run: unknown run kind %q", kind)
	}

	if err := os.WriteFile(filepath.Join(rn.Dir, "prompt.md"), []byte(p.promptText), 0o644); err != nil {
		return p, fmt.Errorf("run: write prompt: %w", err)
	}
	if err := rn.WriteState(p.state); err != nil {
		return p, err
	}
	return p, nil
}

// fetchBundle reads the tracker record, then the helpdesk record, thread
// and attachments that go with it. An attachment failure is a warning the
// prompt carries, not a run failure.
func (r *Runner) fetchBundle(ctx context.Context, key string, p *prepared) (ticket.Bundle, error) {
	var b ticket.Bundle

	if r.Tracker != nil {
		tt, err := r.Tracker.Get(ctx, key)
		if err != nil {
			return b, fmt.Errorf("tracker %s: %w", key, err)
		}
		b.Tracker = &tt
		if b.Tracker.HelpdeskRef == "" {
			r.applyHelpdeskRefFallback(p, &b, b.Tracker)
		}
		// A tracker adapter degrades the same way a helpdesk one does — a
		// comment page it could not read, a field this workspace does not
		// expose, an attachment it skipped — and reports none of it in the
		// error. Draining its warnings here is the only thing that puts
		// them in front of the agent.
		if w, ok := r.Tracker.(source.Warner); ok {
			for _, msg := range w.WarningsFor(key) {
				p.warn(&b, msg)
			}
		}
	}

	helpdeskID := key
	if b.Tracker != nil && b.Tracker.HelpdeskRef != "" {
		helpdeskID = b.Tracker.HelpdeskRef
	}

	if r.Helpdesk != nil {
		ht, err := r.Helpdesk.Get(ctx, helpdeskID)
		if err != nil {
			return b, fmt.Errorf("helpdesk %s: %w", helpdeskID, err)
		}
		b.Helpdesk = &ht

		thread, err := r.Helpdesk.Threads(ctx, helpdeskID)
		if err != nil {
			return b, fmt.Errorf("helpdesk threads %s: %w", helpdeskID, err)
		}
		b.Thread = thread

		atts, err := r.Helpdesk.Attachments(ctx, helpdeskID, filepath.Join(p.run.BundleDir(), "attachments"))
		if err != nil {
			p.warn(&b, fmt.Sprintf("attachments for helpdesk ticket %s could not be downloaded: %v", helpdeskID, err))
		} else {
			b.Attachments = r.keepReadableAttachments(p, &b, atts)
		}
		// A helpdesk that downloaded some attachments and not others
		// returns no error at all, so ask it what it skipped: the agent
		// has to know an attachment is missing before it reasons from
		// the ones that arrived.
		if w, ok := r.Helpdesk.(source.Warner); ok {
			for _, msg := range w.WarningsFor(helpdeskID) {
				p.warn(&b, msg)
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
func (r *Runner) applyHelpdeskRefFallback(p *prepared, b *ticket.Bundle, tt *ticket.TrackerTicket) {
	if r.Config == nil || r.Config.Sources.Tracker == nil {
		return
	}
	h := r.Config.Sources.Tracker.HelpdeskRef
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
			p.warn(b, fmt.Sprintf("helpdeskRef.pattern matched %q in the description but idPattern did not; no helpdesk ticket was read", truncate(ref, 120)))
			return
		}
		ref = im[1]
	}
	tt.HelpdeskRef = ref
}

// truncate caps s at max bytes without splitting a multi-byte rune,
// marking a shortened value with an ellipsis so a reader can tell the
// difference between a short value and a trimmed one.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}

// readableMIME reports whether an agent session can actually open a file
// of this type. Everything else — audio, video, and anything the helpdesk
// labelled with a type nobody can read — is evidence the session cannot
// reach, and is better named in a warning than left in the bundle for it
// to hunt for a transcoder over.
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
// what the helpdesk downloaded. A file that fails either is deleted from
// the bundle — leaving it there means the session can still read 17 MB of
// mp4 into its context — and named, with its size, in a warning the prompt
// and the run state both carry.
func (r *Runner) keepReadableAttachments(p *prepared, b *ticket.Bundle, atts []ticket.Attachment) []ticket.Attachment {
	max := r.Config.AttachmentMaxBytes()
	kept := make([]ticket.Attachment, 0, len(atts))
	for _, a := range atts {
		path := a.Path
		if path != "" && !filepath.IsAbs(path) {
			path = filepath.Join(p.run.BundleDir(), path)
		}
		size := int64(-1)
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
		}

		mimeType := attachmentMIME(a)
		switch {
		case size > max:
			p.warn(b, fmt.Sprintf("attachment %q (%s, %s) is over the %s limit and was not kept; its contents are unread",
				a.Name, mimeType, humanBytes(size), humanBytes(max)))
		case !readableMIME(mimeType):
			p.warn(b, fmt.Sprintf("attachment %q (%s, %s) cannot be opened in this session and was not kept; its contents are unread",
				a.Name, mimeType, humanBytes(size)))
		default:
			kept = append(kept, a)
			continue
		}
		if path != "" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(r.stderr(), "[%s] remove attachment %s: %v\n", p.state.Key, path, err)
			}
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
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

// warn records a warning in both places it has to appear: the prompt the
// agent reads, and the run state a human reads afterwards.
func (p *prepared) warn(b *ticket.Bundle, msg string) {
	b.Warnings = append(b.Warnings, msg)
	p.state.Warnings = append(p.state.Warnings, msg)
}

// rcaInput adds the rca-only material to a triage input: the triage note
// under review, the engineer's resolution, and the merged pull request.
func (r *Runner) rcaInput(ctx context.Context, p *prepared, in prompt.TriageInput, rca *RCAOptions) (prompt.RCAInput, error) {
	out := prompt.RCAInput{TriageInput: in}

	noteBody, err := os.ReadFile(p.triageNotePath)
	if err != nil {
		return out, fmt.Errorf("run: read triage note: %w", err)
	}
	out.TriageNote = string(noteBody)

	if rca == nil {
		return out, nil
	}
	if rca.Resolution != "" {
		out.Resolution = rca.Resolution
		path := filepath.Join(p.run.BundleDir(), "resolution.md")
		if err := os.WriteFile(path, []byte(rca.Resolution+"\n"), 0o644); err != nil {
			return out, fmt.Errorf("run: write resolution: %w", err)
		}
	}
	if rca.PRURL != "" {
		out.PRURL = rca.PRURL
		pr := r.pullRequest(ctx, p, rca.PRURL)
		out.PRTitle, out.PRBody, out.PRDiff = pr.title, pr.body, pr.diff
	}
	// Reading the PR can add warnings, so the prompt renders from the
	// bundle as it stands now rather than the copy taken before.
	out.Bundle = p.bundle
	return out, nil
}

type prMaterial struct{ title, body, diff string }

// pullRequest reads the merged PR through the gh CLI and files it in the
// bundle. Every failure here is a warning: the rca run continues without
// the diff.
func (r *Runner) pullRequest(ctx context.Context, p *prepared, url string) prMaterial {
	var pr prMaterial

	gh, err := exec.LookPath("gh")
	if err != nil {
		p.warn(&p.bundle, fmt.Sprintf("gh is not on PATH, so %s was not read", url))
		return pr
	}

	var meta struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		MergedAt string `json:"mergedAt"`
	}
	view, err := exec.CommandContext(ctx, gh, "pr", "view", url, "--json", "title,body,mergedAt").Output()
	if err != nil {
		p.warn(&p.bundle, fmt.Sprintf("gh pr view %s failed: %v", url, err))
	} else if err := json.Unmarshal(view, &meta); err != nil {
		p.warn(&p.bundle, fmt.Sprintf("gh pr view %s returned unreadable JSON: %v", url, err))
	} else {
		pr.title, pr.body = meta.Title, meta.Body
	}

	diff, err := exec.CommandContext(ctx, gh, "pr", "diff", url).Output()
	if err != nil {
		p.warn(&p.bundle, fmt.Sprintf("gh pr diff %s failed: %v", url, err))
	} else {
		pr.diff = string(diff)
	}

	if pr.title != "" || pr.body != "" {
		md := "# " + pr.title + "\n\n" + url + "\n"
		if meta.MergedAt != "" {
			md += "\nMerged at: " + meta.MergedAt + "\n"
		}
		md += "\n" + pr.body + "\n"
		if err := os.WriteFile(filepath.Join(p.run.BundleDir(), "pr.md"), []byte(md), 0o644); err != nil {
			p.warn(&p.bundle, fmt.Sprintf("write pr.md: %v", err))
		}
	}
	if pr.diff != "" {
		if err := os.WriteFile(filepath.Join(p.run.BundleDir(), "pr.diff"), []byte(pr.diff), 0o644); err != nil {
			p.warn(&p.bundle, fmt.Sprintf("write pr.diff: %v", err))
		}
	}
	return pr
}

// readThreadHead returns the lines of the rendered thread the prompt
// quotes inline, and whether that is only the head of a longer thread —
// which is what decides whether the prompt's heading can honestly say the
// conversation is all there.
func readThreadHead(bundleDir string) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(bundleDir, "thread.md"))
	if err != nil {
		return "", false, fmt.Errorf("run: read thread: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > threadHeadLines {
		return strings.Join(lines[:threadHeadLines], "\n"), true, nil
	}
	return strings.Join(lines, "\n"), false, nil
}

// triageNoteCopy locates the notes-directory copy of a triage note from the
// run that produced it, and returns that path with its wiki-link stem. Both
// are empty when the run recorded no copy.
func triageNoteCopy(root, runNotePath string) (string, string) {
	runID := filepath.Base(filepath.Dir(runNotePath))
	_, state, err := store.Open(root, runID)
	if err != nil {
		return "", ""
	}
	for _, path := range state.Notes {
		if path == runNotePath {
			continue
		}
		return path, strings.TrimSuffix(filepath.Base(path), ".md")
	}
	return "", ""
}
