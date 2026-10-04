package repos

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Ask is one repository the operator asked the session to look in: "look in
// OXO.Systems", "check the POS app", "in the reports repo".
type Ask struct {
	// Phrase is the name as the operator wrote it.
	Phrase string `json:"phrase"`
	// Name is the configured repository it resolved to; empty when it
	// resolved to none.
	Name string `json:"name,omitempty"`
	// Status is workspace or companion when it resolved, unknown when no
	// repository has that name, and ambiguous when several do.
	Status string `json:"status"`
	// Candidates are the repositories an ambiguous phrase matches.
	Candidates []string `json:"candidates,omitempty"`
}

// Resolved reports whether the ask names exactly one configured repository.
func (a Ask) Resolved() bool {
	return a.Status == StatusWorkspace || a.Status == StatusCompanion
}

// Reason is why an ask that did not resolve cannot start a session, in the
// words the composer shows; "" for one that resolved.
func (a Ask) Reason() string {
	switch a.Status {
	case StatusUnknown:
		return a.Phrase + " is not a configured repository"
	case StatusAmbiguous:
		return a.Phrase + " matches more than one repository: " + strings.Join(a.Candidates, ", ")
	}
	return ""
}

var (
	// askVerb finds the verbs that ask for a place to look: "look in",
	// "look into", "look at", "check", "search", "dig into", "go
	// through", with an optional "the" after. The words that follow are
	// read by wordsAfter, not by the pattern, so "look in A and check B"
	// finds both asks.
	askVerb = regexp.MustCompile(`(?i)\b(?:look(?:\s+(?:into|in|at|through))?|check(?:\s+(?:into|in))?|search(?:\s+(?:in|through))?|dig\s+into|go\s+through)\s+(?:the\s+)?`)
	// askIn finds "in the X repo", "in X repository", "in the X codebase".
	askIn = regexp.MustCompile(`(?i)\bin\s+(?:the\s+)?(` + askWord + `)\s+(?:repo|repository|codebase|project)\b`)
	// wordAt is one word at the start of the text: letters, digits and
	// underscores, with dots, slashes and dashes allowed inside, so
	// "OXO.Systems" is one word and a sentence's closing dot is not part
	// of it.
	wordAt = regexp.MustCompile(`^(?:` + askWord + `)`)
)

const askWord = `[A-Za-z0-9_][A-Za-z0-9_./-]*[A-Za-z0-9_]|[A-Za-z0-9_]`

// repoNouns are the words that say the phrase before them is a repository,
// so a name nobody configured is reported rather than passed over.
var repoNouns = map[string]bool{"repo": true, "repository": true, "codebase": true, "project": true, "app": true, "frontend": true, "backend": true}

// Asks finds the repositories the operator asked the session to look in,
// in the order the text asks for them, each once. Each phrase resolves by
// name, case aside: a configured repository's name, the last segment of its
// path, "owner/name" by origin, or the last dot- or dash-separated part of
// its name ("POS" for OXO.Flutter.POS) when only one repository has it. A
// phrase that names no repository is reported only when it looks like one —
// "owner/name", a dotted name such as OXO.Systems, or a word followed by
// "repo", "app" and the like — so "check the logs" asks for nothing.
func Asks(text string, list []Repo) []Ask {
	type hit struct {
		at int
		a  Ask
	}
	var hits []hit
	for _, m := range askVerb.FindAllStringIndex(text, -1) {
		if a, ok := askOf(wordsAfter(text[m[1]:], 3), list); ok {
			hits = append(hits, hit{m[0], a})
		}
	}
	for _, m := range askIn.FindAllStringSubmatchIndex(text, -1) {
		if a, ok := askOf([]string{text[m[2]:m[3]], "repo"}, list); ok {
			hits = append(hits, hit{m[0], a})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	var out []Ask
	seen := map[string]bool{}
	for _, h := range hits {
		k := strings.ToLower(h.a.Phrase)
		if h.a.Name != "" {
			k = strings.ToLower(h.a.Name)
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, h.a)
	}
	return out
}

// wordsAfter reads up to n words from the start of text, stopping at
// punctuation: "KDS app for the timeout" gives KDS, app, for;
// "acme/Web#828 today" gives acme/Web.
func wordsAfter(text string, n int) []string {
	var out []string
	for len(out) < n {
		trimmed := strings.TrimLeft(text, " \t")
		if len(out) > 0 && len(trimmed) == len(text) {
			break
		}
		w := wordAt.FindString(trimmed)
		if w == "" {
			break
		}
		out = append(out, w)
		text = trimmed[len(w):]
	}
	return out
}

// askOf resolves the words after an ask verb: the first word that names a
// repository wins; failing that, a repository-shaped word is reported as
// unknown.
func askOf(words []string, list []Repo) (Ask, bool) {
	for _, w := range words {
		if repoNouns[strings.ToLower(w)] {
			break
		}
		if a, ok := lookup(w, list); ok {
			return a, true
		}
	}
	for i, w := range words {
		if repoNouns[strings.ToLower(w)] {
			if i > 0 {
				return Ask{Phrase: words[i-1], Status: StatusUnknown}, true
			}
			return Ask{}, false
		}
		if repoShaped(w) {
			return Ask{Phrase: w, Status: StatusUnknown}, true
		}
	}
	return Ask{}, false
}

// repoShaped reports whether a word nobody configured still reads as a
// repository's name: "owner/name", or a dotted name whose every part starts
// with a letter and whose last part starts with a capital — OXO.Systems,
// Acme.Web — which a file name (config.yaml) and a version (3.2) are not.
func repoShaped(w string) bool {
	if strings.Count(w, "/") == 1 && !strings.HasPrefix(w, "/") && !strings.HasSuffix(w, "/") {
		return true
	}
	if !strings.Contains(w, ".") || strings.Contains(w, "/") {
		return false
	}
	parts := strings.Split(w, ".")
	for _, p := range parts {
		if p == "" || !isLetter(p[0]) {
			return false
		}
	}
	last := parts[len(parts)-1][0]
	return last >= 'A' && last <= 'Z'
}

func isLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

// lookup resolves one word against the list: "owner/name" by origin, then
// an exact name or path segment, then the short alias, which counts only
// when it is unique.
func lookup(word string, list []Repo) (Ask, bool) {
	if i := strings.Index(word, "/"); i > 0 && strings.Count(word, "/") == 1 {
		if m := resolveHosted("github.com", word[:i], word[i+1:], word, list); m.Status != StatusUnknown {
			return Ask{Phrase: word, Name: m.Name, Status: m.Status}, true
		}
		return Ask{}, false
	}
	for _, r := range list {
		if strings.EqualFold(r.Name, word) || (r.Path != "" && strings.EqualFold(filepath.Base(r.Path), word)) {
			return Ask{Phrase: word, Name: r.Name, Status: statusOf(r)}, true
		}
	}
	var hits []Repo
	for _, r := range list {
		if alias := shortName(r.Name); alias != "" && strings.EqualFold(alias, word) {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 0:
		return Ask{}, false
	case 1:
		return Ask{Phrase: word, Name: hits[0].Name, Status: statusOf(hits[0])}, true
	}
	names := make([]string, len(hits))
	for i, r := range hits {
		names[i] = r.Name
	}
	return Ask{Phrase: word, Status: StatusAmbiguous, Candidates: names}, true
}

// shortName is the last dot-, dash- or underscore-separated part of a name,
// or "" when the name has only one part.
func shortName(name string) string {
	i := strings.LastIndexAny(name, ".-_")
	if i < 0 || i == len(name)-1 {
		return ""
	}
	return name[i+1:]
}
