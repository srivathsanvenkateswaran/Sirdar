package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
	"github.com/srivathsanvenkateswaran/sirdar/internal/worktree"
)

// prepared is a run that has its directory, bundle and prompt on disk and
// is ready for an agent session.
type prepared struct {
	run        store.Run
	state      store.State
	kind       store.Kind
	bundle     ticket.Bundle
	promptText string

	// noNotify silences this run's completion notification.
	noNotify bool

	// root is the tree the session runs in and is confined to, when it is
	// not the workspace root: a fix run's linked worktree, or the
	// historical checkout an `--at` run made. Everything else — the run
	// directory, the configuration, the playbooks — still comes from the
	// workspace root.
	root string

	// ownWorktree is set when this run made root itself and is therefore
	// the one to take it away again. A fix run's worktree is
	// internal/fix's to manage, so it is left alone here.
	ownWorktree bool

	// keepWorktree leaves an own worktree on disk after the run.
	keepWorktree bool

	// service and notePath are what the first register row recorded: the
	// service the note filed under and the path a human opens. The
	// notification needs both, and only the note-writing path knows them.
	service  string
	notePath string

	// noteRefused says the notes directory already held this key's triage
	// note with a status a person had moved on, so nothing was filed and
	// the run's note is readable only in the run directory. It is said
	// again when the run ends, where an operator will see it.
	noteRefused string

	// rca runs only: the triage note under review, the copy of it in the
	// notes directory (may be empty), and the wiki-link stem of that copy.
	triageNotePath string
	triageNoteCopy string
	triageLink     string
}

// sessionRoot is the directory this run's session stands in: its own root
// when it has one, else the workspace root.
func (p *prepared) sessionRoot(workspace string) string {
	if p.root != "" {
		return p.root
	}
	return workspace
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

	// The run id is minted here rather than inside store.Create because an
	// --at run names its worktree after it, and that directory has to
	// exist before the session that stands in it.
	runID := store.NewRunID(now)
	rn, err := store.CreateID(cfg.Root, key, runID)
	if err != nil {
		return nil, err
	}

	model := o.Model
	if model == "" {
		model = cfg.Model
	}
	// A dry run notifies nobody: it writes a bundle and a prompt, and
	// "completed" on the channel would claim a triage that never ran.
	p := &prepared{run: rn, kind: kind, noNotify: o.NoNotify || o.DryRun, keepWorktree: o.KeepWorktree}
	p.state = store.State{
		RunID:     filepath.Base(rn.Dir),
		Key:       key,
		Kind:      kind,
		Status:    store.StatusPreparing,
		Provider:  r.providerName(),
		Model:     model,
		StartedAt: now,
		UpdatedAt: now,
		Eval:      o.Eval,
	}
	// The historical checkout, before anything else the run does: a
	// commit that does not exist, or a repository that refuses the
	// worktree, should stop the run before a ticket is fetched.
	if o.At != "" {
		if err := r.checkoutAt(ctx, p, o.At); err != nil {
			return p, err
		}
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
		notePath, err := rcaTriageNote(cfg.Root, key, rca)
		if err != nil {
			return p, err
		}
		p.triageNotePath = notePath
		p.triageNoteCopy, p.triageLink = triageNoteCopy(cfg.Root, notePath)
	}

	bundle, err := r.stageBundle(ctx, key, p, o)
	if err != nil {
		return p, err
	}
	p.bundle = bundle

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

// rcaTriageNote settles which triage note an rca run reviews: the one
// RCAOptions.TriageNote names when it names one, else the newest completed
// triage note for the key. A named note is checked here, before a ticket is
// fetched, so a caller that named the wrong file hears it as that rather
// than as an unreadable path halfway through preparation.
func rcaTriageNote(root, key string, rca *RCAOptions) (string, error) {
	if rca != nil {
		if path := strings.TrimSpace(rca.TriageNote); path != "" {
			if _, err := os.Stat(path); err != nil {
				return "", fmt.Errorf("run: the triage note named for %s is not readable: %w", key, err)
			}
			return path, nil
		}
	}
	path, err := store.LatestNote(root, key, store.KindTriage)
	if err != nil {
		return "", fmt.Errorf("no triage note for %s; run triage first", key)
	}
	return path, nil
}

// checkoutAt puts this run in a linked worktree of the workspace checked
// out at commit, detached, and points the session's root at it. It is the
// same machinery `sirdar fix` stands its session in, with one difference:
// there is no branch. A retrospective run reads the repository as it stood
// and writes nothing to it, so there is nothing a branch name would mean.
//
// The commit is resolved before the directory is made, so `--at` on a
// commit this repository does not have fails with that as the reason rather
// than as a git error out of `worktree add`.
func (r *Runner) checkoutAt(ctx context.Context, p *prepared, commit string) error {
	root := r.Config.Root
	g := worktree.Git{Dir: root}
	if err := g.Run(ctx, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("run: --at %s: the workspace is not a git repository", commit)
	}
	sha, err := worktree.ResolveCommit(ctx, g, commit)
	if err != nil {
		return fmt.Errorf("run: --at %s: %w", commit, err)
	}
	path := worktree.Path(root, p.state.RunID)
	if err := worktree.AddDetached(ctx, g, path, sha); err != nil {
		return fmt.Errorf("run: --at %s: %w", commit, err)
	}
	p.root, p.ownWorktree = path, true
	p.state.At = sha
	p.state.Warnings = append(p.state.Warnings,
		fmt.Sprintf("the session ran against %s in %s, not against the working tree", sha, worktree.RelToRoot(root, path)))
	fmt.Fprintf(r.stderr(), "[%s] at %s in %s\n", p.state.Key, sha, worktree.RelToRoot(root, path))
	return nil
}

// releaseWorktree takes away the worktree an --at run made, once that run
// has ended. A blocked run keeps it: it can be resumed, and a resumed
// session has to stand where the first one stood. So does a run the
// operator asked to keep with --keep-worktree.
func (r *Runner) releaseWorktree(ctx context.Context, p *prepared, status store.Status) {
	if p == nil || !p.ownWorktree || p.root == "" {
		return
	}
	if p.keepWorktree || status == store.StatusBlocked {
		return
	}
	// The removal is run from the main tree, never from inside the
	// directory being removed, and a context that has already been
	// cancelled — an interrupt — would refuse the git command outright.
	if ctx.Err() != nil {
		ctx = context.WithoutCancel(ctx)
	}
	worktree.Remove(ctx, worktree.Git{Dir: r.Config.Root}, p.root, r.stderr(), p.state.Key)
	p.root, p.ownWorktree = "", false
}

// stageBundle puts the ticket bundle in the run directory: normally by
// fetching it from the configured sources, and, when the caller named a
// BundleDir, by copying that directory in instead. A replayed bundle is
// taken as it stands — its warnings, its attachments and its thread are
// whatever the run that produced it recorded — so an eval run reasons over
// exactly the evidence the original session saw.
func (r *Runner) stageBundle(ctx context.Context, key string, p *prepared, o Options) (ticket.Bundle, error) {
	if o.BundleDir == "" {
		f := &Fetcher{
			Config:   r.Config,
			Tracker:  r.Tracker,
			Helpdesk: r.Helpdesk,
			Stderr:   r.stderr(),
			AsOf:     o.AsOf,
			Env:      r.Env,
			Now:      r.now,
		}
		bundle, warnings, err := f.Fetch(ctx, key, p.run.BundleDir())
		p.state.Warnings = append(p.state.Warnings, warnings...)
		if err != nil {
			return bundle, err
		}
		return bundle, ticket.WriteBundle(p.run.BundleDir(), bundle)
	}
	if err := copyTree(o.BundleDir, p.run.BundleDir()); err != nil {
		return ticket.Bundle{}, fmt.Errorf("run: copy bundle %s: %w", o.BundleDir, err)
	}
	bundle, err := readBundle(p.run.BundleDir())
	if err != nil {
		return bundle, err
	}
	p.state.Warnings = append(p.state.Warnings, "bundle replayed from "+o.BundleDir+"; no ticket source was called")
	return bundle, nil
}

// copyTree copies src over dst recursively, creating directories as it
// goes. Symlinks are skipped rather than followed: a golden bundle is data,
// and a link in one has nothing to point at on another machine.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
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

// readableMIME, attachmentMIME, baseMIME, keepReadableAttachments and
// humanBytes live in fetch.go: the bundle-fetch extraction gave the
// Fetcher its own copies of this logic, so prepare.go does not keep a
// second set.

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
