// Package repos knows the repositories a workspace's triage may read: the
// workspace's own repository, where a fix happens, and the companion
// repositories config.yaml names under repos:, which a session may read and
// never write.
//
// It answers three questions, all from text and the configured list alone:
// which repositories a ticket, a thread or an instruction mentions
// (Mentions), which repository the operator asked the session to look in
// (Asks), and whether a triage note's proposed fix belongs in a companion
// rather than in the workspace (FixTarget). Facts reads what `sirdar doctor`
// says about a clone, with read-only git queries; nothing here fetches.
package repos

import (
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Repo is one repository a session may read.
type Repo struct {
	// Name is what the operator calls it: the config's name:, or the
	// workspace's name for the workspace repository.
	Name string `json:"name"`
	// Path is the clone's absolute path, with ~ expanded.
	Path string `json:"path"`
	// Origin is the remote the clone came from — the config's origin:, or
	// what `git remote get-url origin` said — or empty when neither said.
	Origin string `json:"origin,omitempty"`
	// About is the config's one-line purpose, for the prompt.
	About string `json:"about,omitempty"`
	// Workspace marks the workspace's own repository: the one a fix runs in.
	Workspace bool `json:"workspace,omitempty"`
}

// Slug is the repository's origin as "host/owner/name", lower-cased, or ""
// when the origin is not a hosted remote.
func (r Repo) Slug() string { return Slug(r.Origin) }

// Status words a mention or an ask carries.
const (
	// StatusWorkspace is the workspace's own repository.
	StatusWorkspace = "workspace"
	// StatusCompanion is a repository configured under repos:.
	StatusCompanion = "companion"
	// StatusUnknown is a repository no configuration names.
	StatusUnknown = "unknown"
	// StatusAmbiguous is a name that matches more than one repository.
	StatusAmbiguous = "ambiguous"
)

// Slug normalises a git remote to "host/owner/name", lower-cased and
// without ".git": git@github.com:Owner/Name.git, ssh://git@github.com/o/n,
// https://github.com/o/n and https://user@github.com/o/n.git all become
// "github.com/owner/name". A local path or anything else that names no host
// and repository is "".
func Slug(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	var host, path string
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil || u.Host == "" {
			return ""
		}
		host, path = u.Hostname(), u.Path
	} else if at := strings.Index(remote, ":"); at > 1 && !strings.Contains(remote[:at], "/") {
		// scp-like: [user@]host:owner/name. A one-letter "host" is a
		// Windows drive, C:/repos/web, which is a local path.
		host = remote[:at]
		if i := strings.LastIndex(host, "@"); i >= 0 {
			host = host[i+1:]
		}
		path = remote[at+1:]
	} else {
		return ""
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if host == "" || len(parts) < 2 || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
		return ""
	}
	owner, name := parts[len(parts)-2], parts[len(parts)-1]
	return strings.ToLower(host + "/" + owner + "/" + name)
}

// Mention is one repository a piece of text names.
type Mention struct {
	// Name is the configured name when the repository is configured, else
	// the repository's own name as the text wrote it ("OXO.Systems" out of
	// "acme/OXO.Systems#828").
	Name string `json:"name"`
	// Slug is "owner/name" when the text named a hosted repository; empty
	// for a bare name.
	Slug string `json:"slug,omitempty"`
	// Ref is the text that named it: a URL, "owner/name#828", or the name.
	Ref string `json:"ref"`
	// Status is workspace, companion or unknown.
	Status string `json:"status"`
}

// Phrase is the chip's words for the mention, or "" for the workspace's own
// repository, which is not worth a word.
func (m Mention) Phrase() string {
	switch m.Status {
	case StatusCompanion:
		return "mentions " + m.Name + " (companion repo)"
	case StatusUnknown:
		return "mentions " + m.Name + " (not configured — add it under repos:)"
	}
	return ""
}

var (
	urlIn = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)
	// ownerNameRef is GitHub's short reference: owner/name#123. The owner
	// is GitHub's user-name shape; the name is a repository name, which
	// may carry dots, underscores and dashes.
	ownerNameRef = regexp.MustCompile(`(?:^|[^A-Za-z0-9._/-])([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9._-]{1,100})#([0-9]+)\b`)
)

// Mentions finds every repository the text names, in the order the text
// names them, each once: a GitHub URL (a pull request, an issue, a file,
// the repository itself), an "owner/name#N" reference, and the bare name of
// a configured repository. A hosted reference is matched to a configured
// repository by its origin; one no origin matches is reported as unknown.
func Mentions(text string, list []Repo) []Mention {
	type hit struct {
		at int
		m  Mention
	}
	var hits []hit
	rest := []byte(text)
	blank := func(from, to int) {
		for i := from; i < to && i < len(rest); i++ {
			rest[i] = ' '
		}
	}

	for _, span := range urlIn.FindAllStringIndex(text, -1) {
		raw := strings.TrimRight(text[span[0]:span[1]], ".,;:)]}>")
		blank(span[0], span[1])
		owner, name, ok := githubRepoInURL(raw)
		if !ok {
			continue
		}
		hits = append(hits, hit{span[0], resolveHosted("github.com", owner, name, raw, list)})
	}
	for _, m := range ownerNameRef.FindAllStringSubmatchIndex(string(rest), -1) {
		owner, name := string(rest[m[2]:m[3]]), string(rest[m[4]:m[5]])
		ref := string(rest[m[2]:m[7]])
		hits = append(hits, hit{m[2], resolveHosted("github.com", owner, name, ref, list)})
		blank(m[2], m[7])
	}
	plain := string(rest)
	for _, r := range list {
		if at := wordIndex(plain, r.Name); at >= 0 {
			hits = append(hits, hit{at, Mention{Name: r.Name, Ref: plain[at : at+len(r.Name)], Status: statusOf(r)}})
		}
	}

	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	var out []Mention
	seen := map[string]bool{}
	for _, h := range hits {
		k := strings.ToLower(h.m.Name)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, h.m)
	}
	return out
}

// githubRepoInURL reads owner and name out of a github.com URL.
func githubRepoInURL(raw string) (owner, name string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), true
}

// resolveHosted matches host/owner/name against the configured origins,
// then — for a repository whose origin could not be read — against its
// name.
func resolveHosted(host, owner, name, ref string, list []Repo) Mention {
	slug := strings.ToLower(host + "/" + owner + "/" + name)
	for _, r := range list {
		if r.Slug() != "" && r.Slug() == slug {
			return Mention{Name: r.Name, Slug: owner + "/" + name, Ref: ref, Status: statusOf(r)}
		}
	}
	for _, r := range list {
		if r.Slug() == "" && (strings.EqualFold(r.Name, name) || strings.EqualFold(filepath.Base(r.Path), name)) {
			return Mention{Name: r.Name, Slug: owner + "/" + name, Ref: ref, Status: statusOf(r)}
		}
	}
	return Mention{Name: name, Slug: owner + "/" + name, Ref: ref, Status: StatusUnknown}
}

func statusOf(r Repo) string {
	if r.Workspace {
		return StatusWorkspace
	}
	return StatusCompanion
}

// wordIndex is where name first appears in text as a word of its own, case
// aside, or -1. A name is a word when what is on either side of it is not a
// letter, a digit, an underscore or a dash, and not a dot followed by more
// of the name's own kind of characters: "OXO.Systems." ends a sentence,
// "OXO.Systems.Web" is a different repository.
func wordIndex(text, name string) int {
	if strings.TrimSpace(name) == "" {
		return -1
	}
	lower, needle := strings.ToLower(text), strings.ToLower(name)
	from := 0
	for {
		i := strings.Index(lower[from:], needle)
		if i < 0 {
			return -1
		}
		at := from + i
		end := at + len(needle)
		before := at == 0 || !nameByte(lower[at-1]) && lower[at-1] != '.' && lower[at-1] != '/'
		after := end == len(lower) || !nameByte(lower[end]) && !(lower[end] == '.' && end+1 < len(lower) && nameByte(lower[end+1]))
		if before && after {
			return at
		}
		from = at + 1
	}
}

func nameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}
