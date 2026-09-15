package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Retro is the ground truth beside a retrospective golden bundle: the
// instant the bundle was cut at, the commit the repository stood at when
// the engineer started, the pull requests that closed the ticket, and what
// building the bundle had to take out of it.
//
// It is what `sirdar golden add --retro` writes and what
// `sirdar eval --retro` reads. The bundle says what the engineer knew; this
// says what they did about it.
type Retro struct {
	Key string `json:"key"`
	// AsOf is the cutoff the bundle was captured at: the moment work
	// started on the ticket, before any comment naming the fix.
	AsOf time.Time `json:"asOf"`
	// BaseCommit is the commit the pull request branched from. Every
	// retro run stands at it, so the agent sees the repository as the
	// engineer saw it.
	BaseCommit string `json:"baseCommit"`
	// PRURLs are the merged pull requests the change landed in.
	PRURLs []string `json:"prUrls"`
	// PRDiff is the unified diff's file name, relative to the golden
	// entry's directory. Empty means RetroDiffName.
	PRDiff string `json:"prDiff"`
	// PRFiles are the paths those pull requests touched.
	PRFiles  []string `json:"prFiles"`
	Redacted Redacted `json:"redacted"`

	// Dir is the golden entry's directory, filled in by LoadRetro rather
	// than read from the file.
	Dir string `json:"-"`
}

// Redacted counts what the as-of cutoff took out of the bundle beside it,
// so a reader of a retro score can tell how much of the ticket the agent
// was deliberately not shown.
type Redacted struct {
	PRLinks            int `json:"prLinks"`
	CommentsDropped    int `json:"commentsDropped"`
	AttachmentsDropped int `json:"attachmentsDropped"`
}

// RetroFile is the name a golden entry's retro descriptor carries.
const RetroFile = "retro.json"

// RetroDiffName is the file the pull-request diff is written to, inside the
// golden entry and named by Retro.PRDiff.
const RetroDiffName = "pr.diff"

// DiffPath is the absolute path of the pull request's diff.
func (r Retro) DiffPath() string {
	name := r.PRDiff
	if name == "" {
		name = RetroDiffName
	}
	return filepath.Join(r.Dir, name)
}

// ReadDiff parses the pull request's diff.
func (r Retro) ReadDiff() (Diff, error) {
	data, err := os.ReadFile(r.DiffPath())
	if err != nil {
		return Diff{}, fmt.Errorf("eval: read %s: %w", r.DiffPath(), err)
	}
	return ParseDiff(string(data)), nil
}

// Files are the pull request's paths, normalised. retro.json's own list is
// preferred, because it is what the human's tooling recorded; a retro.json
// that carries none falls back to the diff.
func (r Retro) Files(prDiff Diff) []string {
	source := r.PRFiles
	if len(source) == 0 {
		source = prDiff.Paths()
	}
	out := make([]string, 0, len(source))
	for _, p := range source {
		if p = NormalizePath(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// LoadRetro reads one golden key's retro descriptor. A key with no
// retro.json is not an error anywhere it is asked for optionally; callers
// that need one check HasRetro first.
func LoadRetro(root, key string) (Retro, error) {
	dir := filepath.Join(root, key)
	path := filepath.Join(dir, RetroFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Retro{}, fmt.Errorf("eval: read %s: %w", path, err)
	}
	var r Retro
	if err := json.Unmarshal(data, &r); err != nil {
		return Retro{}, fmt.Errorf("eval: parse %s: %w", path, err)
	}
	r.Dir = dir
	if r.Key == "" {
		r.Key = key
	}
	if r.BaseCommit == "" {
		return r, fmt.Errorf("eval: %s names no baseCommit", path)
	}
	if _, err := os.Stat(r.DiffPath()); err != nil {
		return r, fmt.Errorf("eval: %s: %w", key, err)
	}
	return r, nil
}

// HasRetro reports whether a golden key can be replayed as a retro.
func HasRetro(root, key string) bool {
	return fileExists(filepath.Join(root, key, RetroFile))
}

// RetroKeys lists the golden keys that carry a retro.json, sorted. It is
// what `sirdar eval --retro` replays when it is given no keys.
func RetroKeys(root string) ([]string, error) {
	keys, err := Keys(root)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if HasRetro(root, k) {
			out = append(out, k)
		}
	}
	return out, nil
}

// AddRetroOptions are the inputs to AddRetro beyond the ticket key.
type AddRetroOptions struct {
	// GoldenDir is the golden set to write into. Empty means DefaultDir.
	GoldenDir string
	// PRURLs are the merged pull requests that closed the ticket. At
	// least one is required: they are what the cutoff is derived from and
	// what the score is measured against.
	PRURLs []string
	// AsOf overrides the derived cutoff. Zero means derive it.
	AsOf time.Time
	// Force skips the refusal to write a golden set into a git work tree.
	Force bool
}

// AddedRetro reports what `sirdar golden add --retro` wrote.
type AddedRetro struct {
	Key          string
	Dir          string
	BundleDir    string
	RetroPath    string
	DiffPath     string
	ExpectedPath string
	// SkeletonWritten is false when an expected.json was already there and
	// was left alone.
	SkeletonWritten bool
	Retro           Retro
	// AsOfSource says where the cutoff came from, for the line the CLI
	// prints: an operator who disagrees with it needs to know whether it
	// was read off the tracker or off the pull request.
	AsOfSource string
	// Warnings are the fetch's own warnings plus the cutoff's counts.
	Warnings []string
}

// AddRetro builds one retrospective golden entry: the ticket as it looked
// when an engineer picked it up, and the ground truth for scoring what a
// session does with it.
//
//	<golden>/<KEY>/bundle/        the as-of bundle
//	<golden>/<KEY>/expected.json  assertion skeleton, empty for now
//	<golden>/<KEY>/retro.json     the ground truth
//	<golden>/<KEY>/pr.diff        the merged pull request's diff
//
// The ticket is read through the workspace's own configured adapters — the
// same Get/Threads/Attachments calls a triage run makes, and nothing else —
// so nothing is written back to the tracker or the helpdesk. The pull
// request is read through the gh CLI, invoked with exec and never through a
// shell, using only `pr view` and `pr diff`.
func AddRetro(ctx context.Context, f *runner.Fetcher, key string, o AddRetroOptions) (AddedRetro, error) {
	a := AddedRetro{Key: key}
	if !store.ValidKey(key) {
		return a, fmt.Errorf("eval: %q is not a usable ticket key", key)
	}
	if len(o.PRURLs) == 0 {
		return a, fmt.Errorf("eval: --retro needs at least one --pr URL: the merged pull request is what the " +
			"cutoff is derived from and what a retrospective run is scored against")
	}
	root := ExpandDir(o.GoldenDir)
	if err := checkGoldenLocation(root, o.Force); err != nil {
		return a, err
	}

	// The pull requests come first: reading them is cheap, fails loudly,
	// and there is no point calling a helpdesk for a ticket whose ground
	// truth cannot be assembled.
	prs, err := readPullRequests(ctx, o.PRURLs)
	if err != nil {
		return a, err
	}

	asOf, asOfSource, err := retroAsOf(ctx, f.Tracker, key, o.AsOf, prs)
	if err != nil {
		return a, err
	}
	a.AsOfSource = asOfSource

	a.Dir = filepath.Join(root, key)
	a.BundleDir = filepath.Join(a.Dir, "bundle")
	if err := os.MkdirAll(a.BundleDir, 0o755); err != nil {
		return a, fmt.Errorf("eval: create %s: %w", a.BundleDir, err)
	}

	f.AsOf = asOf
	bundle, warnings, err := f.Fetch(ctx, key, a.BundleDir)
	if err != nil {
		return a, err
	}
	a.Warnings = warnings
	if err := ticket.WriteBundle(a.BundleDir, bundle); err != nil {
		return a, fmt.Errorf("eval: write bundle: %w", err)
	}

	a.Retro = Retro{
		Key:        key,
		AsOf:       asOf,
		BaseCommit: prs[0].BaseRefOid,
		PRURLs:     o.PRURLs,
		PRDiff:     RetroDiffName,
		PRFiles:    changedFiles(prs),
	}
	if c := bundle.Cutoff; c != nil {
		a.Retro.Redacted = Redacted{
			PRLinks:            c.PRLinks,
			CommentsDropped:    c.CommentsDropped,
			AttachmentsDropped: c.AttachmentsDropped,
		}
	}

	a.DiffPath = filepath.Join(a.Dir, RetroDiffName)
	diff, err := pullRequestDiff(ctx, o.PRURLs)
	if err != nil {
		return a, err
	}
	if err := os.WriteFile(a.DiffPath, diff, 0o644); err != nil {
		return a, fmt.Errorf("eval: write %s: %w", a.DiffPath, err)
	}

	a.RetroPath = filepath.Join(a.Dir, RetroFile)
	raw, err := json.MarshalIndent(a.Retro, "", "  ")
	if err != nil {
		return a, fmt.Errorf("eval: marshal %s: %w", RetroFile, err)
	}
	if err := os.WriteFile(a.RetroPath, append(raw, '\n'), 0o644); err != nil {
		return a, fmt.Errorf("eval: write %s: %w", a.RetroPath, err)
	}

	// The assertions are empty on purpose. What a retrospective entry is
	// scored on is the pull request beside it, not a skeleton copied out
	// of a note nobody has written yet; an operator who wants ordinary
	// assertions as well writes them into this file by hand.
	a.ExpectedPath = filepath.Join(a.Dir, "expected.json")
	if !fileExists(a.ExpectedPath) {
		if err := os.WriteFile(a.ExpectedPath, []byte("{}\n"), 0o644); err != nil {
			return a, fmt.Errorf("eval: write %s: %w", a.ExpectedPath, err)
		}
		a.SkeletonWritten = true
	}
	return a, nil
}

// retroAsOf decides the instant the bundle is cut at.
//
// An explicit --as-of wins outright. Otherwise it is the earliest of two
// pieces of evidence about when the work started: the ticket's first move
// into an in-progress status, when the tracker adapter implements
// source.Transitioner and reports one, and the earliest pull request's
// created_at. A tracker that exposes no transition history — most of the
// stdio adapters, and every helpdesk-only workspace — falls back to the
// pull request's created_at alone, which is later than the real pickup and
// therefore the conservative choice in the wrong direction: it can leave
// early triage comments in the bundle that the engineer did have, never the
// fix itself, because the fix's own references are redacted whatever the
// cutoff.
func retroAsOf(ctx context.Context, tracker source.Tracker, key string, explicit time.Time, prs []prMeta) (time.Time, string, error) {
	if !explicit.IsZero() {
		return explicit, "--as-of", nil
	}

	asOf, origin := time.Time{}, ""
	for _, pr := range prs {
		if pr.CreatedAt.IsZero() {
			continue
		}
		if asOf.IsZero() || pr.CreatedAt.Before(asOf) {
			asOf, origin = pr.CreatedAt, "the earliest pull request's created_at"
		}
	}
	if at, ok := firstInProgress(ctx, tracker, key); ok && (asOf.IsZero() || at.Before(asOf)) {
		asOf, origin = at, "the ticket's first in_progress transition"
	}
	if asOf.IsZero() {
		return asOf, "", fmt.Errorf("eval: no as-of could be derived for %s: the tracker exposes no status "+
			"history and gh reported no createdAt for any --pr; pass --as-of RFC3339", key)
	}
	return asOf, origin, nil
}

// firstInProgress asks the tracker when the ticket first moved into an
// in-progress status. A tracker that cannot answer — it implements no
// history, or the call failed — simply has no opinion: this is one of two
// pieces of evidence, and the other one is always there.
func firstInProgress(ctx context.Context, tracker source.Tracker, key string) (time.Time, bool) {
	t, ok := tracker.(source.Transitioner)
	if !ok {
		return time.Time{}, false
	}
	transitions, err := t.Transitions(ctx, key)
	if err != nil {
		return time.Time{}, false
	}
	var first time.Time
	for _, tr := range transitions {
		if tr.At.IsZero() || !source.InProgress(tr.To) {
			continue
		}
		if first.IsZero() || tr.At.Before(first) {
			first = tr.At
		}
	}
	return first, !first.IsZero()
}

// prMeta is what `gh pr view` reports about one pull request.
type prMeta struct {
	URL         string
	CreatedAt   time.Time
	BaseRefOid  string
	MergeCommit string
	Files       []string
}

// readPullRequests reads every named pull request through gh, sorted oldest
// first, so the caller can take the first one's base commit as the state of
// the repository when the work started.
func readPullRequests(ctx context.Context, urls []string) ([]prMeta, error) {
	gh, err := ghPath(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]prMeta, 0, len(urls))
	for _, u := range urls {
		if err := checkPRURL(u); err != nil {
			return nil, err
		}
		pr, err := ghView(ctx, gh, u)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// pullRequestDiff returns the diff of every named pull request, in the
// order the operator named them, each introduced by the URL it came from so
// a multi-pull-request fix reads as the sequence it was.
func pullRequestDiff(ctx context.Context, urls []string) ([]byte, error) {
	gh, err := ghPath(ctx)
	if err != nil {
		return nil, err
	}
	var buf []byte
	for _, u := range urls {
		diff, err := ghDiff(ctx, gh, u)
		if err != nil {
			return nil, err
		}
		if len(buf) > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, []byte("pull request: "+u+"\n")...)
		buf = append(buf, diff...)
	}
	return buf, nil
}

// changedFiles is every path any of the pull requests touched, deduplicated
// and sorted, which is what a fix is scored for overlap against.
func changedFiles(prs []prMeta) []string {
	seen := map[string]bool{}
	var out []string
	for _, pr := range prs {
		for _, path := range pr.Files {
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// ghPath finds the GitHub CLI and confirms it is logged in. Both failures
// are the operator's to fix and neither is worth discovering halfway
// through building a golden entry, so they are checked before anything is
// written.
func ghPath(ctx context.Context) (string, error) {
	gh, err := exec.LookPath("gh")
	if err != nil {
		return "", fmt.Errorf("eval: --retro reads the merged pull request through the GitHub CLI, and gh is " +
			"not on PATH; install it from https://cli.github.com and run `gh auth login`")
	}
	if out, err := exec.CommandContext(ctx, gh, "auth", "status").CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("eval: gh is not authenticated, so the pull request cannot be read; run `gh auth login`\n%s", detail)
	}
	return gh, nil
}

// checkPRURL refuses anything that is not an http(s) URL. The value goes
// straight to gh as an argument, and a bare word — or worse, something
// starting with a dash — would be read as a flag or as a pull request in
// whatever repository the working directory happens to be.
func checkPRURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("eval: --pr %q is not a pull request URL; pass the full https:// link", raw)
	}
	return nil
}

// ghView reads one pull request's metadata. gh is executed directly, never
// through a shell: the URL comes off the command line and a shell would
// make its punctuation meaningful.
func ghView(ctx context.Context, gh, prURL string) (prMeta, error) {
	pr := prMeta{URL: prURL}
	out, err := exec.CommandContext(ctx, gh, "pr", "view", prURL,
		"--json", "createdAt,baseRefOid,mergeCommit,files").Output()
	if err != nil {
		return pr, fmt.Errorf("eval: gh pr view %s failed: %w%s", prURL, err, stderrOf(err))
	}
	var raw struct {
		CreatedAt   time.Time `json:"createdAt"`
		BaseRefOid  string    `json:"baseRefOid"`
		MergeCommit struct {
			OID string `json:"oid"`
		} `json:"mergeCommit"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return pr, fmt.Errorf("eval: gh pr view %s returned unreadable JSON: %w", prURL, err)
	}
	pr.CreatedAt = raw.CreatedAt
	pr.BaseRefOid = raw.BaseRefOid
	pr.MergeCommit = raw.MergeCommit.OID
	for _, f := range raw.Files {
		pr.Files = append(pr.Files, f.Path)
	}
	if pr.BaseRefOid == "" {
		return pr, fmt.Errorf("eval: gh reported no baseRefOid for %s, so there is no commit to run a "+
			"retrospective triage at", prURL)
	}
	return pr, nil
}

// ghDiff reads one pull request's unified diff.
func ghDiff(ctx context.Context, gh, prURL string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, gh, "pr", "diff", prURL).Output()
	if err != nil {
		return nil, fmt.Errorf("eval: gh pr diff %s failed: %w%s", prURL, err, stderrOf(err))
	}
	return out, nil
}

// stderrOf renders what gh printed on stderr, which is where it says why it
// refused — a repository the login cannot see, a pull request that does not
// exist. Empty when the error carries none.
func stderrOf(err error) string {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return ""
	}
	detail := strings.TrimSpace(string(ee.Stderr))
	if detail == "" {
		return ""
	}
	return ": " + detail
}
