package eval

import (
	"strconv"
	"strings"
)

// Hunk is one "@@" window of a unified diff, in both sides' line numbers.
type Hunk struct {
	OldStart int `json:"oldStart"`
	OldLines int `json:"oldLines"`
	NewStart int `json:"newStart"`
	NewLines int `json:"newLines"`
}

// FileDiff is one file's changes within a unified diff.
type FileDiff struct {
	// Path is the file after the change. For a deletion it is the file
	// that was removed, so every FileDiff names something.
	Path string `json:"path"`
	// OldPath is the file before the change, empty for a new file.
	OldPath string `json:"oldPath,omitempty"`
	New     bool   `json:"new,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
	Renamed bool   `json:"renamed,omitempty"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Hunks   []Hunk `json:"hunks,omitempty"`
}

// Base is the path this file had at the commit both diffs were taken
// against, which is what two diffs of the same base can be compared on. A
// rename has moved since, and a new file had no path at all — for that one
// the post-change path is the only name there is.
func (f FileDiff) Base() string {
	if f.OldPath != "" {
		return f.OldPath
	}
	return f.Path
}

// Diff is a parsed unified diff.
type Diff struct {
	Files   []FileDiff `json:"files"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
}

// Paths lists every path the diff touches, in the order the diff named
// them, taking a rename by where it landed.
func (d Diff) Paths() []string {
	out := make([]string, 0, len(d.Files))
	for _, f := range d.Files {
		out = append(out, f.Path)
	}
	return out
}

// ParseDiff reads a unified diff — `git diff`, `git format-patch`, a PR's
// .diff — into the parts a retro score compares: which files moved, how
// far, and which windows of each file they moved in.
//
// The hunk header's own counts drive the body scan rather than the shape of
// each line, because a diff of a diff is a real thing to score: a removed
// line reading "--- a/x" is content, not a header, and only the counts say
// which it is.
func ParseDiff(text string) Diff {
	var d Diff
	var cur *FileDiff
	remOld, remNew := 0, 0

	flush := func() {
		if cur == nil {
			return
		}
		if cur.Path == "" {
			cur.Path = cur.OldPath
		}
		if cur.Path != "" || len(cur.Hunks) > 0 {
			d.Files = append(d.Files, *cur)
		}
		cur = nil
	}
	start := func() *FileDiff {
		if cur == nil {
			cur = &FileDiff{}
		}
		return cur
	}

	for _, line := range strings.Split(text, "\n") {
		if remOld > 0 || remNew > 0 {
			consumed := true
			switch {
			case strings.HasPrefix(line, "\\"):
				// "\ No newline at end of file" belongs to neither side.
			case strings.HasPrefix(line, "+"):
				cur.Added++
				d.Added++
				remNew--
			case strings.HasPrefix(line, "-"):
				cur.Removed++
				d.Removed++
				remOld--
			case line == "" || strings.HasPrefix(line, " "):
				if remOld > 0 {
					remOld--
				}
				if remNew > 0 {
					remNew--
				}
			default:
				// The hunk was cut short: a truncated diff, or a header
				// arriving early. Reread this line as a header.
				consumed = false
				remOld, remNew = 0, 0
			}
			if consumed {
				continue
			}
		}

		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			f := start()
			f.OldPath, f.Path = gitHeaderPaths(strings.TrimPrefix(line, "diff --git "))
		case strings.HasPrefix(line, "new file mode"):
			f := start()
			f.New = true
			f.OldPath = ""
		case strings.HasPrefix(line, "deleted file mode"):
			start().Deleted = true
		case strings.HasPrefix(line, "rename from "):
			f := start()
			f.Renamed = true
			f.OldPath = stripRename(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			f := start()
			f.Renamed = true
			f.Path = stripRename(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "--- "):
			f := start()
			if p := stripSide(strings.TrimPrefix(line, "--- ")); p == devNull {
				f.New = true
				f.OldPath = ""
			} else {
				f.OldPath = p
			}
		case strings.HasPrefix(line, "+++ "):
			f := start()
			if p := stripSide(strings.TrimPrefix(line, "+++ ")); p == devNull {
				f.Deleted = true
			} else {
				f.Path = p
			}
		case strings.HasPrefix(line, "@@"):
			h, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			f := start()
			f.Hunks = append(f.Hunks, h)
			remOld, remNew = h.OldLines, h.NewLines
		}
	}
	flush()
	return d
}

const devNull = "/dev/null"

// gitHeaderPaths splits the two paths off a "diff --git a/x b/y" line. A
// path holding a space makes that line ambiguous on its own, so the split
// prefers the " b/" separator and the --- / +++ lines correct whatever is
// read here.
func gitHeaderPaths(rest string) (string, string) {
	if i := strings.Index(rest, " b/"); i > 0 {
		return stripSide(rest[:i]), stripSide(rest[i+1:])
	}
	fields := strings.Fields(rest)
	if len(fields) == 2 {
		return stripSide(fields[0]), stripSide(fields[1])
	}
	return "", ""
}

// stripSide takes one path out of a diff header: the trailing tab and
// timestamp a plain `diff -u` adds, the quoting git applies to a path with
// unusual bytes in it, and the a/ or b/ prefix.
func stripSide(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	if strings.HasPrefix(s, `"`) {
		if unquoted, err := strconv.Unquote(s); err == nil {
			s = unquoted
		}
	}
	if s == devNull {
		return s
	}
	for _, prefix := range []string{"a/", "b/", "i/", "w/", "c/", "o/"} {
		if rest, ok := strings.CutPrefix(s, prefix); ok {
			return NormalizePath(rest)
		}
	}
	return NormalizePath(s)
}

// stripRename takes one path off a "rename from"/"rename to" line. Git
// writes those without the a/ and b/ prefixes, so unlike stripSide nothing
// is cut from the front: a repository with a top-level "a" directory has
// its renames read correctly.
func stripRename(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) {
		if unquoted, err := strconv.Unquote(s); err == nil {
			s = unquoted
		}
	}
	return NormalizePath(s)
}

// parseHunkHeader reads "@@ -12,7 +19,9 @@ func x()" into its two windows.
// An omitted count means one line, which is what git leaves out.
func parseHunkHeader(line string) (Hunk, bool) {
	rest, ok := strings.CutPrefix(line, "@@")
	if !ok {
		return Hunk{}, false
	}
	end := strings.Index(rest, "@@")
	if end < 0 {
		return Hunk{}, false
	}
	fields := strings.Fields(rest[:end])
	if len(fields) < 2 {
		return Hunk{}, false
	}
	oldStart, oldLines, ok := parseRange(fields[0], "-")
	if !ok {
		return Hunk{}, false
	}
	newStart, newLines, ok := parseRange(fields[1], "+")
	if !ok {
		return Hunk{}, false
	}
	return Hunk{OldStart: oldStart, OldLines: oldLines, NewStart: newStart, NewLines: newLines}, true
}

func parseRange(field, sign string) (int, int, bool) {
	body, ok := strings.CutPrefix(field, sign)
	if !ok {
		return 0, 0, false
	}
	startText, countText, hasCount := strings.Cut(body, ",")
	start, err := strconv.Atoi(startText)
	if err != nil {
		return 0, 0, false
	}
	count := 1
	if hasCount {
		count, err = strconv.Atoi(countText)
		if err != nil {
			return 0, 0, false
		}
	}
	return start, count, true
}

// NormalizePath puts a path into the one form the retro scores compare on:
// forward slashes, no leading "./", no leading or trailing slash.
func NormalizePath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	p = strings.TrimPrefix(p, "./")
	return strings.Trim(p, "/")
}
