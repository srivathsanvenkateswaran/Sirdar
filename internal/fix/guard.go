package fix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// The guard is the third layer under the permission policy and the tool's
// own path check, and the only one that does not depend on a provider
// honouring anything. Claude Code and Sirdar's own loop are judged by
// provider.PermissionPolicy; Codex is judged by its sandbox, which Sirdar
// configures but does not implement; an ACP agent is judged by whatever it
// implements. So before the session starts, a fix takes a sha256 of every
// file in the two places a session must leave alone — the workspace's own
// .sirdar/ and the directory git runs hooks from — and takes it again the
// moment the session ends, before any git command runs.
//
// A difference stops the run. Nothing is restored: the operator's own copy
// of what changed is better evidence than an automatic revert, and the
// point of the check is that whatever produced the change was not supposed
// to be able to. Nothing is committed and nothing is pushed, which is the
// half that matters — a modified hook only becomes code the machine runs
// at the next `git commit` or `git push`.

// snapshotExcluded are the entries under .sirdar/ that this run legitimately
// writes to while it is running: its own run directory, the register row it
// appends, and the eval store. Everything else under .sirdar/ — the
// configuration whose permission lists judge the session, the playbooks and
// templates that shape it, the filed notes — must come out of the session
// byte for byte as it went in.
var snapshotExcluded = map[string]bool{
	"runs":           true,
	"register.jsonl": true,
	"eval":           true,
}

// snapshot maps a path to a digest of what was there: the sha256 of a
// regular file's contents, or the target of a symlink, so replacing a file
// with a link to one outside the workspace is a difference like any other.
type snapshot map[string]string

// guardedPaths are the directories a fix run watches: the workspace's
// .sirdar/ and the directory git would run this repository's hooks from —
// core.hooksPath when the repository sets one (husky, lefthook, a
// checked-in .githooks/), and .git/hooks when it does not.
func guardedPaths(ctx context.Context, root string) []string {
	paths := []string{filepath.Join(root, provider.SirdarDir)}
	if hooks := provider.HooksDir(ctx, root); hooks != "" {
		paths = append(paths, hooks)
	}
	return paths
}

// takeSnapshot digests every file under the guarded paths. A path that does
// not exist contributes nothing, which is how a hook file created during
// the session shows up as a difference rather than as an error.
func takeSnapshot(ctx context.Context, root string) (snapshot, error) {
	snap := snapshot{}
	for _, base := range guardedPaths(ctx, root) {
		if err := walkGuarded(root, base, snap); err != nil {
			return nil, err
		}
	}
	return snap, nil
}

func walkGuarded(root, base string, snap snapshot) error {
	info, err := os.Lstat(base)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return digestInto(root, base, info, snap)
	}
	return filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if path != base && skipGuarded(base, path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		return digestInto(root, path, info, snap)
	})
}

// skipGuarded reports whether a path under base is one of the .sirdar
// entries this run writes to itself.
func skipGuarded(base, path string) bool {
	if filepath.Base(base) != provider.SirdarDir {
		return false
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	return snapshotExcluded[first]
}

func digestInto(root, path string, info os.FileInfo, snap snapshot) error {
	key := provider.DisplayPath(root, path)
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		snap[key] = "symlink:" + target
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	// The mode goes into the digest: a hook that was already there and is
	// made executable during the session is a change with no new bytes in
	// it.
	fmt.Fprintf(h, "|mode:%04o", info.Mode().Perm())
	snap[key] = hex.EncodeToString(h.Sum(nil))
	return nil
}

// diffSnapshots lists what changed between two snapshots, one line per
// path, sorted so the message is the same twice running.
func diffSnapshots(before, after snapshot) []string {
	var out []string
	for path, digest := range after {
		prior, existed := before[path]
		switch {
		case !existed:
			out = append(out, path+" was created")
		case prior != digest:
			out = append(out, path+" was modified")
		}
	}
	for path := range before {
		if _, still := after[path]; !still {
			out = append(out, path+" was deleted")
		}
	}
	sort.Strings(out)
	return out
}

// tamperError is the refusal a difference produces. It names every changed
// path, and says plainly what was and was not done about it.
func tamperError(changed []string) error {
	return fmt.Errorf("fix: the session changed files it is never allowed to change:\n\n  %s\n\n"+
		"These are Sirdar's own .sirdar/ files and the directory git runs this repository's hooks "+
		"from. Nothing was restored, nothing was committed and nothing was pushed. Inspect the "+
		"changes above, undo them yourself, and look at the session's transcript in the run "+
		"directory before running the fix again",
		strings.Join(changed, "\n  "))
}

// markRunFailed records the guard's refusal in the run's own state.json, so
// `sirdar runs` shows the run as failed rather than as the completed
// session it was until this check ran. A failure to write is reported and
// otherwise ignored: the refusal itself is already on its way to the
// caller.
func markRunFailed(root, runID, reason string, stderr io.Writer, key string) {
	rn, state, err := store.Open(root, runID)
	if err != nil {
		fmt.Fprintf(stderr, "[%s] the run state was not updated: %v\n", key, err)
		return
	}
	state.Status = store.StatusFailed
	state.Reason = reason
	if err := rn.WriteState(state); err != nil {
		fmt.Fprintf(stderr, "[%s] the run state was not updated: %v\n", key, err)
	}
}
