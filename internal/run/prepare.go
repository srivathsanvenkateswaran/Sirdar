package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
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

// prepare creates the run directory, fetches the ticket, writes the bundle
// and assembles the prompt. Every error it returns happens before an agent
// process exists, so the caller fails the run in "preparing".
func (r *Runner) prepare(ctx context.Context, key string, kind store.Kind, o Options, rca *RCAOptions) (*prepared, error) {
	cfg := r.Config
	now := r.now()

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
	threadHead, err := readThreadHead(rn.BundleDir())
	if err != nil {
		return p, err
	}

	in := prompt.TriageInput{
		Bundle:     bundle,
		BundleDir:  rn.BundleDir(),
		Playbooks:  playbooks,
		ThreadHead: threadHead,
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
			b.Attachments = atts
		}
	}

	if b.Tracker == nil && b.Helpdesk == nil {
		return b, fmt.Errorf("no ticket source configured; set sources.tracker or sources.helpdesk")
	}
	return b, nil
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

// readThreadHead returns the first lines of the rendered thread, which the
// prompt quotes inline.
func readThreadHead(bundleDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(bundleDir, "thread.md"))
	if err != nil {
		return "", fmt.Errorf("run: read thread: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > threadHeadLines {
		lines = lines[:threadHeadLines]
	}
	return strings.Join(lines, "\n"), nil
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
