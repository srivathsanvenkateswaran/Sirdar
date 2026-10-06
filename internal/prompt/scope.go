package prompt

import (
	"slices"
	"strings"
	"unicode"
)

// Listed is a playbook on file but not loaded for this run.
type Listed struct {
	Playbook Playbook
	// Matched is the keyword that matched when the playbook was displaced by
	// the byte budget; "" when nothing in the match text named it.
	Matched string
}

// Selection is which of a workspace's playbooks a run loads in full and
// which it only lists, each in filename order.
type Selection struct {
	Loaded []Playbook // filename order
	Listed []Listed   // filename order
}

// SelectPlaybooks decides which playbooks a run of kind loads, given the
// text people wrote about the ticket (matchText) and the byte budget for
// matched playbooks (maxBytes, 0 for none).
//
// A playbook whose kinds exclude this one is dropped outright: it is neither
// loaded nor listed. An always-on playbook (no `when`) loads, whatever the
// budget, because the operator said it always matters. A playbook whose
// `when` names a word in the match text loads while the loaded total stays
// within the budget, and is listed with that word when it does not fit; the
// budget is there so an instruction that names every tool does not pull in
// every topic, as OMNI-3413's did. Anything else is listed.
func SelectPlaybooks(all []Playbook, kind, matchText string, maxBytes int) Selection {
	var sel Selection
	total := 0
	var scoped []Playbook
	for _, p := range all {
		if p.Kinds != nil && !slices.Contains(p.Kinds, kind) {
			continue
		}
		scoped = append(scoped, p)
		if len(p.When) == 0 {
			total += len(p.Body)
		}
	}
	for _, p := range scoped {
		if len(p.When) == 0 {
			sel.Loaded = append(sel.Loaded, p)
			continue
		}
		kw := FirstMatch(matchText, p.When)
		if kw != "" && (maxBytes == 0 || total+len(p.Body) <= maxBytes) {
			sel.Loaded = append(sel.Loaded, p)
			total += len(p.Body)
			continue
		}
		sel.Listed = append(sel.Listed, Listed{Playbook: p, Matched: kw})
	}
	return sel
}

// Summary is the one line a run records about its playbooks:
// "Playbooks: loaded a, b; listed c, d".
func (s Selection) Summary() string {
	loaded := make([]string, 0, len(s.Loaded))
	for _, p := range s.Loaded {
		loaded = append(loaded, p.Name)
	}
	out := "Playbooks: loaded "
	if len(loaded) == 0 {
		out += "none"
	} else {
		out += strings.Join(loaded, ", ")
	}
	if len(s.Listed) > 0 {
		listed := make([]string, 0, len(s.Listed))
		for _, l := range s.Listed {
			listed = append(listed, l.Playbook.Name)
		}
		out += "; listed " + strings.Join(listed, ", ")
	}
	return out
}

// FirstMatch returns the first of keywords that MatchKeyword finds in text,
// or "" when none is there.
func FirstMatch(text string, keywords []string) string {
	norm := normaliseMatchText(text)
	for _, kw := range keywords {
		if matchNormalised(norm, normaliseMatchText(kw)) {
			return kw
		}
	}
	return ""
}

// MatchKeyword reports whether keyword occurs in text. Both are compared
// lower-cased with every run of whitespace collapsed to one space, so a
// phrase matches however the ticket wrapped it. A keyword made only of
// ASCII letters and digits matches as a whole word, so `db` does not fire
// on `feedback`; any other keyword matches as a substring, because Arabic
// attaches articles and prefixes to the word (`الخطأ` contains `خطأ`).
func MatchKeyword(text, keyword string) bool {
	return matchNormalised(normaliseMatchText(text), normaliseMatchText(keyword))
}

func matchNormalised(text, kw string) bool {
	if kw == "" {
		return false
	}
	if !isASCIIWord(kw) {
		return strings.Contains(text, kw)
	}
	for from := 0; from <= len(text)-len(kw); {
		i := strings.Index(text[from:], kw)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(kw)
		if (start == 0 || !isASCIIAlnum(text[start-1])) && (end == len(text) || !isASCIIAlnum(text[end])) {
			return true
		}
		from = start + 1
	}
	return false
}

// normaliseMatchText lower-cases s and collapses every run of Unicode
// whitespace to a single space.
func normaliseMatchText(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func isASCIIWord(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isASCIIAlnum(s[i]) {
			return false
		}
	}
	return s != ""
}

func isASCIIAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
