package fix

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// Reviewing a fix run's change is the half of the flow no agent is part of.
// The commit is already made; what is left is a person reading it file by
// file and, now and then, deciding that one hunk of it should not have been
// written. Both are answered from the run record and the repository:
// internal/fix has the branch, the base and the commit in state.json, and
// git has the rest.
//
// The two rules the flow keeps are the ones that made the fix safe in the
// first place. Every git command runs in the run's own worktree or in the
// workspace's repository — never anywhere else, and never against a branch
// the run does not own — and nothing here pushes. A change a person has
// already sent to the remote is not something to rewrite behind their back,
// which is why a pushed branch refuses the drop rather than amending under
// it.

// ErrNoDiff means the run has no change to show: it is not a fix run, or
// its worktree is gone and it recorded no commit. The HTTP layer answers
// 404 with the reason.
var ErrNoDiff = errors.New("fix: no diff to review")

// ErrDropRefused means the change is real but must not be edited now: the
// run is still live, the worktree is gone, the branch is pushed, or the
// hunk the caller named is not the one that was served. The HTTP layer
// answers 409 with the reason.
var ErrDropRefused = errors.New("fix: the change cannot be edited")

// patchCap is how much of a unified patch is served. A change big enough to
// pass it is not one anybody reads hunk by hunk in a browser, and shipping
// it whole would put an arbitrary amount of a repository through a JSON
// response.
const patchCap = 2 << 20

// FileChange is one file in a fix run's change, with the counts a file list
// shows beside it.
type FileChange struct {
	Path      string
	Status    string // added, modified, deleted, renamed
	Additions int
	Deletions int
}

// Diff is the review view of one fix run's change: what it is against, where
// it lives, and the patch itself.
type Diff struct {
	Base, Head      string
	Branch          string
	Worktree        string
	WorktreePresent bool
	Pushed          bool
	Files           []FileChange
	Patch           string
	Truncated       bool

	// ETag is a hash of the whole patch, truncation included or not. A
	// drop carries it back so a hunk index computed against one patch
	// cannot be applied to another: the index means nothing once the
	// commit has moved, and silently dropping the wrong hunk is worse
	// than refusing.
	ETag string
}

// ReviewDiff reads one fix run's change. It prefers the run's own worktree,
// where the branch is checked out and where a drop would apply; a run whose
// worktree has been removed is still readable from the repository, because
// the branch and the commit live in the shared git directory either way.
func ReviewDiff(ctx context.Context, root string, state store.State) (Diff, error) {
	g, head, present, err := reviewTree(ctx, root, state)
	if err != nil {
		return Diff{}, err
	}
	base, err := resolveBase(ctx, g, state.Fix.Base, head)
	if err != nil {
		return Diff{}, err
	}

	files, err := changedFiles(ctx, g, base, head)
	if err != nil {
		return Diff{}, err
	}
	patch, err := unifiedPatch(ctx, g, base, head)
	if err != nil {
		return Diff{}, err
	}

	sum := sha256.Sum256([]byte(patch))
	d := Diff{
		Base: base, Head: head,
		Branch:          state.Fix.Branch,
		Worktree:        state.Fix.Worktree,
		WorktreePresent: present,
		Pushed:          state.Fix.Pushed,
		Files:           files,
		Patch:           patch,
		ETag:            hex.EncodeToString(sum[:]),
	}
	if len(d.Patch) > patchCap {
		d.Patch, d.Truncated = capPatch(d.Patch), true
	}
	return d, nil
}

// reviewTree picks the tree the diff is read in and the commit it ends at:
// the run's worktree while it is there, else the repository and the commit
// the run recorded.
func reviewTree(ctx context.Context, root string, state store.State) (git, string, bool, error) {
	if state.Kind != store.KindFix {
		return git{}, "", false, fmt.Errorf("%w: %s is a %s run", ErrNoDiff, state.RunID, state.Kind)
	}
	// A worktree path off the run state is data an operator can hand-edit,
	// so it is bounded to this workspace's own worktrees directory before
	// any git command is pointed at it.
	if path := safeWorktree(root, state.Fix.Worktree, io.Discard, state.Key); path != "" && isWorktree(path) {
		g := git{dir: path}
		head, err := g.head(ctx)
		if err != nil {
			return git{}, "", false, fmt.Errorf("%w: %v", ErrNoDiff, err)
		}
		return g, head, true, nil
	}
	if state.Fix.Commit == "" {
		return git{}, "", false, fmt.Errorf(
			"%w: %s has no worktree and recorded no commit", ErrNoDiff, state.RunID)
	}
	g := git{dir: root}
	head, err := commitSha(ctx, g, state.Fix.Commit)
	if err != nil {
		return git{}, "", false, fmt.Errorf(
			"%w: the commit %s this run recorded is not in this repository", ErrNoDiff, short(state.Fix.Commit))
	}
	return g, head, false, nil
}

// commitSha resolves a revision to the full sha of a commit that exists.
func commitSha(ctx context.Context, g git, rev string) (string, error) {
	sha, err := g.out(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil || sha == "" {
		return "", fmt.Errorf("fix: %q names no commit here", rev)
	}
	return sha, nil
}

// resolveBase turns the base branch the run recorded into the commit the
// change is read against.
//
// It is the fork point rather than the branch tip. The run was cut from
// origin/<base>, and origin/<base> has usually moved on since: diffing
// against the tip would report every commit somebody else landed in the
// meantime as this fix having deleted it.
func resolveBase(ctx context.Context, g git, base, head string) (string, error) {
	candidates := []string{}
	if base != "" {
		candidates = append(candidates, "origin/"+base, base)
	}
	candidates = append(candidates, head+"^")
	for _, rev := range candidates {
		sha, err := commitSha(ctx, g, rev)
		if err != nil {
			continue
		}
		if fork, err := g.out(ctx, "merge-base", sha, head); err == nil && fork != "" {
			return fork, nil
		}
		return sha, nil
	}
	return "", fmt.Errorf("%w: nothing to diff %s against; the run named base %q", ErrNoDiff, short(head), base)
}

// changedFiles lists the files between base and head with their line
// counts. Two commands rather than one because git offers the status and
// the counts separately; both are asked with -z and -M, so they agree on
// rename detection and on the order they answer in.
func changedFiles(ctx context.Context, g git, base, head string) ([]FileChange, error) {
	names, err := g.outRaw(ctx, "diff", "--name-status", "-M", "-z", base+".."+head)
	if err != nil {
		return nil, err
	}
	nums, err := g.outRaw(ctx, "diff", "--numstat", "-M", "-z", base+".."+head)
	if err != nil {
		return nil, err
	}

	counts := parseNumstat(nums)
	files := []FileChange{}
	fields := splitZ(names)
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		if status == "" || i >= len(fields) {
			break
		}
		path := fields[i]
		i++
		// A rename or a copy names both sides; the new path is the one
		// the file list and a drop both mean.
		if c := status[0]; (c == 'R' || c == 'C') && i < len(fields) {
			path = fields[i]
			i++
		}
		add, del := counts[path][0], counts[path][1]
		files = append(files, FileChange{Path: path, Status: fileStatus(status), Additions: add, Deletions: del})
	}
	return files, nil
}

// fileStatus maps git's status letter onto the four the API names. A copy
// reads as an addition (the new path did not exist before) and a type
// change as a modification, which is what each is from a reviewer's side.
func fileStatus(s string) string {
	if s == "" {
		return "modified"
	}
	switch s[0] {
	case 'A', 'C':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	default:
		return "modified"
	}
}

// parseNumstat reads `git diff --numstat -z` into added/removed counts by
// path. A record is "<added>\t<removed>\t<path>", except for a rename,
// where the tab-separated part ends empty and the old and new paths follow
// as two fields of their own. A binary file reports "-" for both counts,
// which is not a number and is left at zero.
func parseNumstat(out string) map[string][2]int {
	counts := map[string][2]int{}
	fields := splitZ(out)
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(fields) {
			path = fields[i+2] // the new name of a rename
			i += 2
		}
		add, _ := strconv.Atoi(parts[0])
		del, _ := strconv.Atoi(parts[1])
		counts[path] = [2]int{add, del}
	}
	return counts
}

// splitZ splits NUL-separated git output, dropping the empty field the
// trailing separator leaves behind.
func splitZ(out string) []string {
	fields := strings.Split(out, "\x00")
	for len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	return fields
}

// unifiedPatch is the whole change as one patch. core.quotePath is turned
// off so a path with a non-ASCII byte in it arrives as itself rather than
// as octal escapes the hunk extractor would have to undo.
func unifiedPatch(ctx context.Context, g git, base, head string) (string, error) {
	return g.outRaw(ctx, "-c", "core.quotePath=false", "diff", "-M", "--no-color", base+".."+head)
}

// capPatch cuts an oversized patch back on a line boundary, so what is
// served still reads as a patch rather than ending mid-hunk.
func capPatch(patch string) string {
	cut := patch[:patchCap]
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		return cut[:i+1]
	}
	return cut
}

// outRaw runs a git command and returns its standard output byte for byte.
// The trimming git.out does is wrong for both things read here: a patch can
// end in a context line that is a single space, and -z output ends in a NUL.
func (g git) outRaw(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, msg)
	}
	return stdout.String(), nil
}

// --- dropping a hunk --------------------------------------------------

// reviewEvent is the run-log line a drop leaves: what was done, to which
// file, and which hunk of it. Hunk is not omitted when zero — the first
// hunk of a file is the one most often dropped, and a line that left it out
// would say a hunk was dropped without saying which.
type reviewEvent struct {
	Action string `json:"action"`
	Path   string `json:"path"`
	Hunk   int    `json:"hunk"`
}

// Drop reverts one hunk of one file out of a fix run's commit and amends
// the commit in place. Nothing is re-run and nothing is pushed: the branch
// keeps its original commit message and gains no second commit, because
// what a reviewer rejected is not a change worth recording as a change.
//
// The returned Diff is the change as it stands afterwards, ready for the
// next decision.
func Drop(ctx context.Context, root string, state store.State, path string, hunk int, etag string) (Diff, error) {
	switch {
	case state.Kind != store.KindFix:
		return Diff{}, fmt.Errorf("%w: %s is a %s run", ErrNoDiff, state.RunID, state.Kind)
	case state.Status == store.StatusPreparing || state.Status == store.StatusRunning:
		return Diff{}, fmt.Errorf("%w: %s is still %s; wait for it to finish", ErrDropRefused, state.RunID, state.Status)
	case state.Fix.Pushed:
		return Diff{}, fmt.Errorf("%w: %s is already pushed, and Sirdar does not rewrite a branch that has left the machine",
			ErrDropRefused, state.Fix.Branch)
	case path == "":
		return Diff{}, fmt.Errorf("%w: no file named", ErrDropRefused)
	case hunk < 0:
		return Diff{}, fmt.Errorf("%w: hunk %d is not an index", ErrDropRefused, hunk)
	}

	wt := safeWorktree(root, state.Fix.Worktree, io.Discard, state.Key)
	if wt == "" || !isWorktree(wt) {
		return Diff{}, fmt.Errorf("%w: the worktree this run committed in is gone, so there is nothing to revert the hunk in",
			ErrDropRefused)
	}
	g := git{dir: wt}
	// The branch is the run's or nothing happens: a worktree somebody has
	// moved onto another branch is not this run's commit to amend.
	if on := g.currentBranch(ctx); on != state.Fix.Branch {
		return Diff{}, fmt.Errorf("%w: the worktree is on %q, not on this run's branch %q",
			ErrDropRefused, fallback(on, "a detached head"), state.Fix.Branch)
	}

	before, err := ReviewDiff(ctx, root, state)
	if err != nil {
		return Diff{}, err
	}
	if before.ETag != etag {
		return Diff{}, fmt.Errorf("%w: the change has moved since that diff was read; read it again", ErrDropRefused)
	}

	single, err := extractHunk(before.Patch, path, hunk)
	if err != nil {
		return Diff{}, err
	}
	// --index so only the reverted hunk is staged: the amend then carries
	// the commit's own tree minus that hunk, and whatever a build left in
	// the worktree afterwards stays out of it.
	if err := applyReverse(ctx, wt, single); err != nil {
		return Diff{}, fmt.Errorf("%w: the hunk no longer applies to this commit: %v", ErrDropRefused, err)
	}
	// --allow-empty because dropping the last hunk of the only file leaves
	// an empty commit, which is a truthful record of a fix a reviewer
	// rejected outright, and better than a half-applied branch.
	if err := g.run(ctx, "commit", "--amend", "--no-edit", "--no-verify", "--allow-empty"); err != nil {
		return Diff{}, err
	}
	head, err := g.head(ctx)
	if err != nil {
		return Diff{}, err
	}

	state.Fix.Commit = head
	recordFixState(root, state.RunID, io.Discard, state.Key, func(s *store.State) { s.Fix.Commit = head })
	appendReview(root, state.RunID, reviewEvent{Action: "drop", Path: path, Hunk: hunk})
	return ReviewDiff(ctx, root, state)
}

// extractHunk pulls one file's one hunk out of a whole-commit patch and
// wraps it back in that file's own header, which is the smallest thing
// `git apply` will take.
func extractHunk(patch, path string, hunk int) (string, error) {
	for _, f := range splitFilePatches(patch) {
		if f.path != path {
			continue
		}
		if hunk >= len(f.hunks) {
			return "", fmt.Errorf("%w: %s has %d hunk(s) in this diff, so there is no hunk %d",
				ErrDropRefused, path, len(f.hunks), hunk)
		}
		return f.header + f.hunks[hunk], nil
	}
	return "", fmt.Errorf("%w: %s is not one of the files in this diff", ErrDropRefused, path)
}

// filePatch is one file's section of a unified patch: everything from its
// "diff --git" line down to its first hunk, and then each hunk whole.
type filePatch struct {
	path   string
	header string
	hunks  []string
}

// splitFilePatches cuts a whole-commit patch into per-file sections and
// each section into hunks. The path is read off the "+++ b/…" line, or off
// "--- a/…" when the file was deleted — the same side `git diff
// --name-status` names, so a caller can use either listing's path.
func splitFilePatches(patch string) []filePatch {
	var files []filePatch
	var cur *filePatch
	var section *string // where the next line goes: the header, or a hunk

	flush := func() {
		if cur != nil {
			files = append(files, *cur)
		}
		cur, section = nil, nil
	}
	for _, line := range splitLines(patch) {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			cur = &filePatch{}
			section = &cur.header
		case cur == nil:
			continue
		case strings.HasPrefix(line, "@@"):
			cur.hunks = append(cur.hunks, "")
			section = &cur.hunks[len(cur.hunks)-1]
		}
		if cur == nil {
			continue
		}
		if len(cur.hunks) == 0 {
			name := strings.TrimRight(line, "\r\n")
			if p, ok := strings.CutPrefix(name, "+++ b/"); ok {
				cur.path = p
			} else if p, ok := strings.CutPrefix(name, "--- a/"); ok && cur.path == "" {
				cur.path = p
			}
		}
		*section += line
	}
	flush()
	return files
}

// splitLines splits text keeping each line's terminator, so a patch rebuilt
// from the pieces is byte for byte what git produced.
func splitLines(s string) []string {
	var lines []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}

// applyReverse reverts one hunk against the index and the working tree of
// dir. --recount lets git re-derive the hunk's line counts rather than
// trusting the header, which matters when the hunk came out of a patch that
// was cut short.
func applyReverse(ctx context.Context, dir, patch string) error {
	cmd := exec.CommandContext(ctx, "git", "apply", "-R", "--index", "--recount", "--whitespace=nowarn", "-")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(patch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// appendReview records the drop in the run's own event log, so the run
// directory says what a person did to the commit as well as what the agent
// did. A log that cannot be written is not worth failing the drop over: the
// commit is already amended by then.
func appendReview(root, runID string, e reviewEvent) {
	rn, _, err := store.Open(root, runID)
	if err != nil {
		return
	}
	log, err := rn.OpenEventLog()
	if err != nil {
		return
	}
	defer log.Close()
	_ = log.Append("review", e)
}
