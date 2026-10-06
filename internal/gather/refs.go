package gather

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/repos"
)

// PRRef is one pull request a ticket names.
type PRRef struct {
	Owner, Repo string
	Number      int
	// URL is the canonical https://github.com/<owner>/<repo>/pull/<n>,
	// with the owner and repository spelt as the ticket spelt them.
	URL string
}

// key is the identity two references to the same pull request share,
// however each was spelt.
func (r PRRef) key() string {
	return strings.ToLower(r.Owner+"/"+r.Repo) + "#" + strconv.Itoa(r.Number)
}

// slug is the repository as repos.Slug spells a remote.
func (r PRRef) slug() string {
	return "github.com/" + strings.ToLower(r.Owner+"/"+r.Repo)
}

// prURL is a pull request's web address.
var prURL = regexp.MustCompile(`(?i)https?://github\.com/([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)/pull/(\d+)`)

// prShort is the owner/repo#n shorthand. On its own it is too loose to
// trust — a helpdesk thread is full of "#29809", which is a Zoho ticket
// number — so a match counts only for a repository the workspace knows.
var prShort = regexp.MustCompile(`\b([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)#(\d+)\b`)

// PullRequests finds the pull requests text names, first seen first: the
// ones to read (take, at most max) and the ones that name a repository
// this workspace does not know (skipped), which anyRepo lets through
// instead. skip is a PR already filed elsewhere — an rca run's resolution
// PR — and is dropped from both lists.
func PullRequests(text string, list []repos.Repo, anyRepo bool, max int, skip string) (take []PRRef, skipped []PRRef) {
	known := make(map[string]bool, len(list))
	for _, r := range list {
		if s := r.Slug(); s != "" {
			known[s] = true
		}
	}

	type hit struct {
		at  int
		ref PRRef
	}
	var hits []hit
	var urlSpans [][]int
	for _, m := range prURL.FindAllStringSubmatchIndex(text, -1) {
		if ref, ok := makeRef(text[m[2]:m[3]], text[m[4]:m[5]], text[m[6]:m[7]]); ok {
			hits = append(hits, hit{m[0], ref})
		}
		urlSpans = append(urlSpans, m[:2])
	}
	for _, m := range prShort.FindAllStringSubmatchIndex(text, -1) {
		inside := false
		for _, s := range urlSpans {
			if m[0] < s[1] && m[1] > s[0] {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		ref, ok := makeRef(text[m[2]:m[3]], text[m[4]:m[5]], text[m[6]:m[7]])
		if !ok || !known[ref.slug()] {
			continue
		}
		hits = append(hits, hit{m[0], ref})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })

	seen := map[string]bool{}
	if m := prURL.FindStringSubmatch(skip); m != nil {
		if ref, ok := makeRef(m[1], m[2], m[3]); ok {
			seen[ref.key()] = true
		}
	}
	for _, h := range hits {
		if seen[h.ref.key()] {
			continue
		}
		seen[h.ref.key()] = true
		if known[h.ref.slug()] || anyRepo {
			if len(take) < max {
				take = append(take, h.ref)
			}
			continue
		}
		if len(skipped) < max {
			skipped = append(skipped, h.ref)
		}
	}
	return take, skipped
}

func makeRef(owner, repo, number string) (PRRef, bool) {
	n, err := strconv.Atoi(number)
	if err != nil || n <= 0 {
		return PRRef{}, false
	}
	return PRRef{
		Owner:  owner,
		Repo:   repo,
		Number: n,
		URL:    "https://github.com/" + owner + "/" + repo + "/pull/" + strconv.Itoa(n),
	}, true
}

// text is everything a person wrote that came with the run, in the order
// the prompt shows it. It mirrors internal/run's bundleText, which this
// package cannot import.
func text(in Input) string {
	b := in.Bundle
	parts := []string{in.Instruction}
	if b.Tracker != nil {
		parts = append(parts, b.Tracker.Title, b.Tracker.Description)
	}
	if b.Helpdesk != nil {
		parts = append(parts, b.Helpdesk.Subject)
	}
	if b.Reported != nil {
		parts = append(parts, b.Reported.Description, strings.Join(b.Reported.Links, "\n"))
	}
	for _, m := range b.Thread {
		parts = append(parts, m.Text)
	}
	parts = append(parts, in.Slack)
	return strings.Join(parts, "\n")
}
