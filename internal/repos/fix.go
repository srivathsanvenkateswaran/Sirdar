package repos

import (
	"os"
	"path/filepath"
	"strings"
)

// FixTarget is the companion repository a triage note's proposed fix
// belongs in, read from the files it names, or false when the fix is the
// workspace's own.
//
// A file names a companion when it is an absolute path inside the
// companion's clone, or when it is written "<name>/<path>" — the spelling
// the triage prompt asks for when a fix belongs in a companion — and the
// workspace has no top-level entry of that name of its own. The fix's
// description is not read: a note routinely cites a companion's pull
// request as evidence for a fix that is the workspace's.
func FixTarget(files []string, list []Repo, root string) (Repo, bool) {
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		for _, r := range list {
			if r.Workspace {
				continue
			}
			if filepath.IsAbs(f) && r.Path != "" && within(r.Path, f) {
				return r, true
			}
			slash := filepath.ToSlash(f)
			first, _, ok := strings.Cut(slash, "/")
			if !ok || first == "" {
				continue
			}
			if !strings.EqualFold(first, r.Name) && !(r.Path != "" && strings.EqualFold(first, filepath.Base(r.Path))) {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, first)); err == nil {
				continue
			}
			return r, true
		}
	}
	return Repo{}, false
}

func within(dir, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(target))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
