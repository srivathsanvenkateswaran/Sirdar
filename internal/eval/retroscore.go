package eval

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Jaccard is the overlap of two sets: how much of everything either side
// named was named by both. It is the right shape for the fix comparison,
// where neither side is the reference — a fix that touches three files the
// pull request never touched is as interesting as one that misses three.
type Jaccard struct {
	Intersection int     `json:"intersection"`
	Union        int     `json:"union"`
	Score        float64 `json:"score"`
}

// LinePair is one measure taken on both sides, the agent's and the pull
// request's. There is no score: a fix half the size of the human's is not
// half as good, and the two numbers side by side are the whole point.
type LinePair struct {
	Agent int `json:"agent"`
	PR    int `json:"pr"`
}

// TriageScore is what a retro triage note is measured on. Classification
// and confidence are copied out rather than judged: there is no ground
// truth for either, and what a reader wants is the two columns beside the
// overlap ones.
type TriageScore struct {
	Classification string `json:"classification"`
	Confidence     string `json:"confidence"`
	// CodeRefsPathOverlap is the fraction of the note's own code
	// references whose file the pull request in fact touched: of what the
	// agent pointed at, how much was the change.
	CodeRefsPathOverlap Fraction `json:"codeRefsPathOverlap"`
	// PRFilesHit is the fraction of the pull request's files the note
	// names anywhere: of what the change was, how much the agent found.
	PRFilesHit Fraction `json:"prFilesHit"`
	// MissedFiles are the pull request's files the note never named,
	// which is the part worth reading.
	MissedFiles []string `json:"missedFiles,omitempty"`
	// StrayRefs are the note's references to files the change never
	// touched.
	StrayRefs []string `json:"strayRefs,omitempty"`
}

// FixScore is what a retro fix is measured on, against the diff a human
// merged.
type FixScore struct {
	FilesJaccard Jaccard  `json:"filesJaccard"`
	HunkOverlap  Fraction `json:"hunkOverlap"`
	LinesAdded   LinePair `json:"linesAdded"`
	LinesRemoved LinePair `json:"linesRemoved"`
	// BuildPassed is what the fix session reported about its own build
	// and tests, nil when it ran none or said nothing legible.
	BuildPassed *bool `json:"buildPassed,omitempty"`
	// DiffPath is the agent's diff, kept so the two can be read side by
	// side; the score is a reason to open them, not a verdict.
	DiffPath string `json:"diffPath,omitempty"`
	// AgentFiles are the paths the agent's diff touched.
	AgentFiles []string `json:"agentFiles,omitempty"`
}

// triageFields are the parts of a triage note the retro score reads.
type triageFields struct {
	Classification string `json:"classification"`
	RootCause      struct {
		Confidence string   `json:"confidence"`
		CodeRefs   []string `json:"codeRefs"`
	} `json:"rootCause"`
}

// ScoreTriage measures one triage note against the files the merged pull
// request touched. doc is the note's JSON; text is the rendered note, which
// is where prose naming a file lives — a note that discusses the right file
// in a sentence found it, even when its codeRefs list points elsewhere.
func ScoreTriage(doc []byte, text string, prFiles []string) TriageScore {
	var s TriageScore
	var t triageFields
	if err := json.Unmarshal(doc, &t); err == nil {
		s.Classification = t.Classification
		s.Confidence = t.RootCause.Confidence
	}

	refs := RefPaths(t.RootCause.CodeRefs)
	matched := 0
	for _, ref := range refs {
		if anySamePath(ref, prFiles) {
			matched++
			continue
		}
		s.StrayRefs = append(s.StrayRefs, ref)
	}
	s.CodeRefsPathOverlap = fraction(matched, len(refs))

	// The note's own references count as naming a file whether or not the
	// rendered text repeats them.
	hay := strings.ToLower(strings.ReplaceAll(text, `\`, "/") + "\n" + strings.Join(refs, "\n"))
	hit := 0
	for _, f := range prFiles {
		if namesPath(hay, f) {
			hit++
			continue
		}
		s.MissedFiles = append(s.MissedFiles, f)
	}
	s.PRFilesHit = fraction(hit, len(prFiles))
	return s
}

// ScoreFix measures the agent's diff against the pull request's.
func ScoreFix(prDiff, agentDiff Diff, prFiles []string, buildPassed *bool) FixScore {
	agentFiles := normalizeAll(agentDiff.Paths())
	s := FixScore{
		FilesJaccard: FilesJaccard(agentFiles, prFiles),
		HunkOverlap:  HunkOverlap(prDiff, agentDiff),
		LinesAdded:   LinePair{Agent: agentDiff.Added, PR: prDiff.Added},
		LinesRemoved: LinePair{Agent: agentDiff.Removed, PR: prDiff.Removed},
		BuildPassed:  buildPassed,
		AgentFiles:   agentFiles,
	}
	return s
}

// FilesJaccard is the overlap of two file sets. Both sides are normalised
// and deduplicated first; an empty pair scores zero rather than one,
// because two fixes that changed nothing are not a match, they are two
// missing diffs.
func FilesJaccard(a, b []string) Jaccard {
	left, right := pathSet(a), pathSet(b)
	var j Jaccard
	union := make(map[string]bool, len(left)+len(right))
	for p := range left {
		union[p] = true
		if right[p] {
			j.Intersection++
		}
	}
	for p := range right {
		union[p] = true
	}
	j.Union = len(union)
	if j.Union > 0 {
		j.Score = float64(j.Intersection) / float64(j.Union)
	}
	return j
}

// HunkOverlap is the fraction of the pull request's hunks that the agent
// also touched: same file, intersecting line window.
//
// The comparison is on the base side of both diffs. Both are taken against
// the same commit, so a base-side line number means the same line in both;
// the new-side numbers would not be comparable at all, since each diff
// renumbers the file its own way.
func HunkOverlap(pr, agent Diff) Fraction {
	byPath := make(map[string][]Hunk, len(agent.Files))
	for _, f := range agent.Files {
		byPath[f.Base()] = append(byPath[f.Base()], f.Hunks...)
	}

	total, matched := 0, 0
	for _, f := range pr.Files {
		for _, h := range f.Hunks {
			total++
			if hunkHit(f.Base(), h, byPath) {
				matched++
			}
		}
	}
	return fraction(matched, total)
}

// hunkHit reports whether any of the agent's hunks on the same file
// intersects this one.
func hunkHit(path string, h Hunk, byPath map[string][]Hunk) bool {
	for agentPath, hunks := range byPath {
		if !samePath(path, agentPath) {
			continue
		}
		lo, hi := baseWindow(h)
		for _, other := range hunks {
			olo, ohi := baseWindow(other)
			if lo <= ohi && olo <= hi {
				return true
			}
		}
	}
	return false
}

// baseWindow is the span of base-side lines a hunk covers. A hunk that only
// adds lines covers none of its own, so its window is the gap it was
// inserted into: an addition after line 40 and another after line 40 are
// the same edit, and must intersect.
func baseWindow(h Hunk) (int, int) {
	if h.OldLines <= 0 {
		return h.OldStart, h.OldStart + 1
	}
	return h.OldStart, h.OldStart + h.OldLines - 1
}

// refSuffix strips the ":412" or ":253-282" a code reference ends with.
var refSuffix = regexp.MustCompile(`:\d+(-\d+)?$`)

// RefPaths turns a note's codeRefs into plain paths: the annotation a
// generated skeleton leaves on ("Export/Csv.cs:253-282 (default branch)")
// and the line numbers both come off, because what is being asked is
// whether the agent pointed at the file the change was in.
func RefPaths(refs []string) []string {
	out := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		p := RefPath(ref)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// RefPath is one code reference reduced to its path.
func RefPath(ref string) string {
	ref = strings.TrimSpace(ref)
	if i := strings.IndexAny(ref, " \t("); i > 0 {
		ref = ref[:i]
	}
	ref = strings.TrimRight(ref, ",;")
	ref = refSuffix.ReplaceAllString(ref, "")
	return NormalizePath(ref)
}

// namesPath reports whether a note names one file. hay is the note already
// lower-cased and slash-normalised.
//
// Matching is by path suffix rather than by equality, because a note cites
// a file the way an engineer would — "Export/Csv.cs", or just "Csv.cs" in a
// sentence — and the pull request lists it from the repository root. A bare
// basename counts: a note that discusses Csv.cs found Csv.cs, and the cost
// of the looseness is a file named Program.cs scoring a hit it did not earn,
// which is why the missed list is what the report prints.
func namesPath(hay, path string) bool {
	path = strings.ToLower(NormalizePath(path))
	if path == "" {
		return false
	}
	if strings.Contains(hay, path) {
		return true
	}
	segs := strings.Split(path, "/")
	for i := len(segs) - 2; i >= 0 && i >= len(segs)-3; i-- {
		if strings.Contains(hay, strings.Join(segs[i:], "/")) {
			return true
		}
	}
	base := segs[len(segs)-1]
	return len(base) > 2 && strings.Contains(hay, base)
}

// samePath compares two paths from the same repository. Equality is the
// rule; a slash-bounded suffix also counts, so a diff taken inside a
// subdirectory still lines up with one taken from the root.
func samePath(a, b string) bool {
	a, b = NormalizePath(a), NormalizePath(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a)
}

func anySamePath(p string, list []string) bool {
	for _, other := range list {
		if samePath(p, other) {
			return true
		}
	}
	return false
}

func normalizeAll(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = NormalizePath(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func pathSet(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		if p = NormalizePath(p); p != "" {
			set[p] = true
		}
	}
	return set
}

// buildFields are the parts of a fix report that say whether it built.
type buildFields struct {
	TestsRun []struct {
		Command string `json:"command"`
		Result  string `json:"result"`
	} `json:"testsRun"`
}

// failWords and passWords are how a free-text test result is read. The
// schema lets the agent write a sentence there, so this is a reading of
// prose and is deliberately conservative: anything that says it failed is a
// failure, anything that says nothing legible is nil, and only a clear pass
// is true.
var failWords = []string{"fail", "error", "broke", "red", "did not pass", "no tests"}
var passWords = []string{"pass", "ok", "green", "success", "clean", "0 errors", "all tests"}

// BuildPassed reads a fix session's own report for whether what it wrote
// builds. nil means the report said nothing that can be read either way —
// which is not the same as a failure, and is printed as a dash.
func BuildPassed(report []byte) *bool {
	var f buildFields
	if err := json.Unmarshal(report, &f); err != nil || len(f.TestsRun) == 0 {
		return nil
	}
	yes, no := false, false
	for _, t := range f.TestsRun {
		result := strings.ToLower(t.Result)
		switch {
		case containsAny(result, failWords):
			no = true
		case containsAny(result, passWords):
			yes = true
		}
	}
	switch {
	case no:
		return boolPtr(false)
	case yes:
		return boolPtr(true)
	default:
		return nil
	}
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func boolPtr(b bool) *bool { return &b }
