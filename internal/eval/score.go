package eval

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Check is the outcome of one expectation against one produced document.
type Check struct {
	Key    string `json:"key"`
	Kind   Kind   `json:"kind"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// Score evaluates every expectation against a produced triage document and
// returns one Check per expectation, in the order the expectations were
// given. A document that will not parse fails every check rather than
// silently passing none of them.
func Score(doc []byte, expected []Expectation) []Check {
	var decoded any
	parseErr := json.Unmarshal(doc, &decoded)

	checks := make([]Check, 0, len(expected))
	for _, e := range expected {
		c := Check{Key: e.Key, Kind: e.Kind}
		if parseErr != nil {
			c.Detail = "the note is not valid JSON: " + parseErr.Error()
			checks = append(checks, c)
			continue
		}
		got, ok := lookup(decoded, e.Path)
		if !ok {
			c.Detail = "no value at " + e.Path
			checks = append(checks, c)
			continue
		}
		c.Pass, c.Detail = compare(e, got)
		checks = append(checks, c)
	}
	return checks
}

// compare applies one expectation's comparison to the value found at its
// path, returning whether it held and, when it did not, what was there
// instead.
func compare(e Expectation, got any) (bool, string) {
	switch e.Kind {
	case KindContains:
		return containsAll(e.Want, got)
	case KindMin:
		return atLeast(e.Want, got)
	default:
		var want any
		if err := json.Unmarshal(e.Want, &want); err != nil {
			return false, "the expected value is not valid JSON: " + err.Error()
		}
		if reflect.DeepEqual(want, got) {
			return true, ""
		}
		return false, fmt.Sprintf("want %s, got %s", render(want), render(got))
	}
}

// containsAll requires each wanted string to appear as a substring of at
// least one element of the array at the path. It is deliberately loose:
// a human writing "Domain/Inventory.API/" into expected.json is naming the
// area the note should have cited, not the exact line it should have cited.
func containsAll(want json.RawMessage, got any) (bool, string) {
	wants, err := stringList(want)
	if err != nil {
		return false, err.Error()
	}
	list, ok := got.([]any)
	if !ok {
		return false, "the value at this path is not an array, so _contains cannot be applied"
	}
	var elems []string
	for _, v := range list {
		elems = append(elems, fmt.Sprint(v))
	}
	var missing []string
	for _, w := range wants {
		found := false
		for _, e := range elems {
			if strings.Contains(e, w) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, w)
		}
	}
	if len(missing) == 0 {
		return true, ""
	}
	return false, "no element contains " + strings.Join(quoteAll(missing), ", ") +
		"; the array holds " + strings.Join(quoteAll(elems), ", ")
}

// atLeast requires the array at the path to hold at least n elements.
func atLeast(want json.RawMessage, got any) (bool, string) {
	var n float64
	if err := json.Unmarshal(want, &n); err != nil {
		return false, "the expected value is not a number: " + err.Error()
	}
	list, ok := got.([]any)
	if !ok {
		return false, "the value at this path is not an array, so _min cannot be applied"
	}
	if float64(len(list)) >= n {
		return true, ""
	}
	return false, fmt.Sprintf("want at least %d, got %d", int(n), len(list))
}

func stringList(raw json.RawMessage) ([]string, error) {
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	return nil, fmt.Errorf("the expected value must be a string or an array of strings")
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = `"` + s + `"`
	}
	return out
}

func render(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

// lookup walks a dot path through a decoded JSON document. Each segment is
// an object key; a segment that is an integer indexes an array, so
// "timeline.0.role" reads.
func lookup(doc any, path string) (any, bool) {
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := index(seg)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func index(seg string) (int, error) {
	var i int
	_, err := fmt.Sscanf(seg, "%d", &i)
	return i, err
}

// Fraction is one overlap measure: how many of the things the human's note
// held were also in the produced one.
type Fraction struct {
	Matched int     `json:"matched"`
	Total   int     `json:"total"`
	Score   float64 `json:"score"`
}

func fraction(matched, total int) Fraction {
	f := Fraction{Matched: matched, Total: total}
	if total > 0 {
		f.Score = float64(matched) / float64(total)
	}
	return f
}

// Overlap is the coarse comparison against a human-written note: how much
// of what they cited and how much of what they structured came back.
//
// It is coarse on purpose. Two notes about the same bug will not share
// prose, and scoring prose similarity would measure the wrong thing. What
// is worth measuring is whether the run looked where the human looked
// (the file:line references) and covered what the human covered (the
// section headings). Neither is a grade; both are a reason to open the two
// notes side by side.
type Overlap struct {
	Refs     Fraction `json:"refs"`
	Headings Fraction `json:"headings"`
	// MissingRefs and MissingHeadings name what the produced note left
	// out, which is the part worth reading.
	MissingRefs     []string `json:"missingRefs,omitempty"`
	MissingHeadings []string `json:"missingHeadings,omitempty"`
}

// refPattern matches a "path/to/file.ext:123" reference anywhere in a note.
var refPattern = regexp.MustCompile(`[\w./\\-]+\.[A-Za-z0-9]+:\d+`)

// headingPattern matches a level-2 markdown heading.
var headingPattern = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)

// Compare measures how much of a human-written note the produced note
// covers.
func Compare(expected, produced string) Overlap {
	var o Overlap

	wantRefs := uniqueMatches(refPattern.FindAllString(expected, -1), nil)
	gotRefs := uniqueMatches(refPattern.FindAllString(produced, -1), nil)
	matched, missing := intersect(wantRefs, gotRefs)
	o.Refs = fraction(matched, len(wantRefs))
	o.MissingRefs = missing

	wantHeads := uniqueMatches(nil, headingPattern.FindAllStringSubmatch(expected, -1))
	gotHeads := uniqueMatches(nil, headingPattern.FindAllStringSubmatch(produced, -1))
	matched, missing = intersect(wantHeads, gotHeads)
	o.Headings = fraction(matched, len(wantHeads))
	o.MissingHeadings = missing

	return o
}

// uniqueMatches folds either a list of whole matches or a list of
// single-capture submatches into a sorted, deduplicated, lower-cased set.
// Case is dropped because "Root Cause Hypothesis" and "Root cause
// hypothesis" are the same section.
func uniqueMatches(whole []string, groups [][]string) []string {
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			seen[s] = true
		}
	}
	for _, s := range whole {
		add(s)
	}
	for _, g := range groups {
		if len(g) > 1 {
			add(g[1])
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// intersect counts how many of want appear in got, and returns the ones
// that do not.
func intersect(want, got []string) (int, []string) {
	have := make(map[string]bool, len(got))
	for _, g := range got {
		have[g] = true
	}
	matched := 0
	var missing []string
	for _, w := range want {
		if have[w] {
			matched++
			continue
		}
		missing = append(missing, w)
	}
	return matched, missing
}
