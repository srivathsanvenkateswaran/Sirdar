package gather

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// prFields is what `gh pr view --json` is asked for.
const prFields = "number,title,state,url,author,baseRefName,headRefName,headRefOid,mergedAt,mergeCommit,files,body"

// prBodyMax caps a PR's description in its file. The description is the
// author's account of the change; the diff beside it is the change.
const prBodyMax = 8192

// prViewMax bounds what `gh pr view` may answer with. Its JSON cannot be
// cut and still parsed, so an answer beyond this is a failure, not a
// truncation.
const prViewMax = 8 << 20

// historyFilesMax is how many of a PR's paths the history is read for.
const historyFilesMax = 30

// builtinAllow is the fixed list the built-in gatherers' own argv are
// judged by. It goes through the same matching as a session's shell call,
// gitDenial included, so a flag that moves where git reads its config or
// runs code from cannot pass even here.
var builtinAllow = []string{
	"gh pr view *",
	"gh pr diff *",
	"git cat-file -e *",
	"git tag --contains *",
	"git describe --tags *",
	"git log *",
}

// builtinPolicy is builtinAllow with the workspace's read scope, so
// `git -C <companion>` is judged as the git command it runs there.
func builtinPolicy(cfg *config.Config) *provider.PermissionPolicy {
	return &provider.PermissionPolicy{
		BashAllow: builtinAllow,
		Root:      cfg.Root,
		ReadAlso:  cfg.ReadAlso(),
	}
}

// prView is the part of `gh pr view --json` the files are written from.
type prView struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	URL    string `json:"url"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	MergedAt    string `json:"mergedAt"`
	MergeCommit *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
	Files []struct {
		Path      string `json:"path"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	} `json:"files"`
	Body string `json:"body"`
}

// sha is the commit the change landed as, or its head when it has not.
func (v prView) sha() string {
	if v.MergeCommit != nil && v.MergeCommit.Oid != "" {
		return v.MergeCommit.Oid
	}
	return v.HeadRefOid
}

func (v prView) merged() bool { return v.MergedAt != "" }

// planPRs lays out a row for each pull request the ticket names, and a
// history row beside each one whose repository has a clone here.
func (g *gatherer) planPRs() (jobs []job, settled []int) {
	list := g.cfg.Repositories()
	take, skipped := PullRequests(text(g.in), list, g.cfg.Gather.PR.AnyRepo, g.cfg.Gather.PR.Max, g.in.SkipPR)
	for _, ref := range take {
		ref := ref
		base := fileSafe(ref.Repo) + "-" + strconv.Itoa(ref.Number)
		prRow := g.addRow(Entry{
			Name: "pr-" + base,
			Kind: KindPR,
			Call: DisplayLine(prViewArgv(ref.URL)),
		})
		rows := []int{prRow}
		histRow, clone := -1, ""
		if g.cfg.GatherPRHistory() {
			for _, r := range list {
				if r.Slug() != "" && r.Slug() == ref.slug() {
					clone = r.Path
					break
				}
			}
		}
		if clone != "" {
			histRow = g.addRow(Entry{Name: "history-" + base, Kind: KindHistory})
			rows = append(rows, histRow)
		}
		jobs = append(jobs, job{rows: rows, run: func(ctx context.Context, g *gatherer) {
			g.runPR(ctx, ref, prRow, histRow, clone)
		}})
	}
	for _, ref := range skipped {
		i := g.addRow(Entry{
			Name:   "pr-" + fileSafe(ref.Repo) + "-" + strconv.Itoa(ref.Number),
			Kind:   KindPR,
			Status: StatusSkipped,
			Reason: "not a configured repository",
			Call:   DisplayLine(prViewArgv(ref.URL)),
		})
		settled = append(settled, i)
	}
	return jobs, settled
}

func prViewArgv(url string) []string {
	return []string{"gh", "pr", "view", url, "--json", prFields}
}

// runPR reads one pull request and its diff, then its history when it has
// a history row.
func (g *gatherer) runPR(ctx context.Context, ref PRRef, prRow, histRow int, clone string) {
	view, ok := g.readPR(ctx, ref, prRow)
	if histRow < 0 {
		return
	}
	if !ok {
		g.mu.Lock()
		pr := g.entries[prRow]
		g.mu.Unlock()
		reason := "the pull request could not be read"
		if pr.Status == StatusSkipped {
			reason = pr.Reason
		}
		g.skip(histRow, reason)
		return
	}
	g.runHistory(ctx, histRow, clone, view)
}

// readPR settles the PR's own row and returns what gh said about it.
func (g *gatherer) readPR(ctx context.Context, ref PRRef, i int) (prView, bool) {
	var view prView
	if !g.startRow(ctx, i) {
		return view, false
	}
	start := g.now()
	g.mu.Lock()
	e := g.entries[i]
	g.mu.Unlock()
	fail := func(status, reason string) (prView, bool) {
		e.Status, e.Reason, e.TookMs = status, reason, g.tookMs(start)
		g.settle(i, e)
		return view, false
	}

	gh, err := lookPath("gh", g.in.Env, g.in.GOOS, g.cfg.Root)
	if err != nil {
		return fail(StatusFailed, "gh is not on PATH")
	}
	policy := builtinPolicy(g.cfg)
	timeout := time.Duration(g.cfg.Gather.TimeoutSeconds) * time.Second

	viewArgv := prViewArgv(ref.URL)
	if d := decideLine(policy, DisplayLine(viewArgv)); !d.Allow {
		return fail(StatusDenied, d.Message)
	}
	res := g.exec(ctx, timeout, gh, viewArgv[1:], prViewMax)
	if status, reason := res.verdict(g.cfg.Gather.TimeoutSeconds); status != StatusOK {
		return fail(status, reason)
	}
	if res.out.total > prViewMax {
		return fail(StatusFailed, fmt.Sprintf("gh pr view answered with more than %d bytes", prViewMax))
	}
	if err := json.Unmarshal(res.out.buf, &view); err != nil {
		return fail(StatusFailed, "gh pr view answered with unreadable JSON: "+firstLine(err.Error(), 200))
	}

	limit := g.cfg.Gather.MaxBytes
	status, reason := StatusOK, ""

	diffArgv := []string{"gh", "pr", "diff", ref.URL}
	if d := decideLine(policy, DisplayLine(diffArgv)); !d.Allow {
		reason = "the diff was not read: " + d.Message
	} else {
		dres := g.exec(ctx, timeout, gh, diffArgv[1:], limit)
		if st, why := dres.verdict(g.cfg.Gather.TimeoutSeconds); st != StatusOK {
			reason = "the diff was not read: " + why
		} else {
			diff := dres.out.kept()
			body := diff
			if dres.out.total > len(diff) {
				status = StatusTruncated
				if !strings.HasSuffix(body, "\n") {
					body += "\n"
				}
				body += truncationMarker(len(diff), dres.out.total) + "\n"
			}
			name := e.Name + ".diff"
			if err := g.writeFile(name, []byte(body)); err != nil {
				reason = "write context/" + name + ": " + err.Error()
			} else {
				e.Diff, e.DiffBytes = "context/"+name, len(diff)
			}
		}
	}

	md := prMarkdown(ref, view)
	out := clipBytes(md, limit, false)
	if len(out) < len(md) {
		status = StatusTruncated
	}
	e.TookMs = g.tookMs(start)
	e.Summary = prSummary(ref, view)
	g.writeRow(&e, fileHeader{
		Name:   e.Name,
		Call:   e.Call,
		TookMs: e.TookMs,
		Status: status,
		Reason: reason,
	}, out, len(md))
	e.Reason = joinReasons(e.Reason, reason)
	g.settle(i, e)
	return view, true
}

// prMarkdown is a pull request's facts, files and description.
func prMarkdown(ref PRRef, v prView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PR %s/%s#%d: %s\n\n", ref.Owner, ref.Repo, ref.Number, v.Title)
	url := v.URL
	if url == "" {
		url = ref.URL
	}
	b.WriteString("- URL: " + url + "\n")
	b.WriteString("- State: " + v.State + "\n")
	b.WriteString("- Author: " + v.Author.Login + "\n")
	fmt.Fprintf(&b, "- Branches: %s ← %s (%s)\n", v.BaseRefName, v.HeadRefName, v.HeadRefOid)
	if v.MergedAt != "" {
		b.WriteString("- Merged at: " + v.MergedAt + "\n")
	}
	if v.MergeCommit != nil && v.MergeCommit.Oid != "" {
		b.WriteString("- Merge commit: " + v.MergeCommit.Oid + "\n")
	}
	fmt.Fprintf(&b, "\n## Files (%d)\n\n", len(v.Files))
	for _, f := range v.Files {
		fmt.Fprintf(&b, "- %s (+%d −%d)\n", f.Path, f.Additions, f.Deletions)
	}
	b.WriteString("\n## Body\n\n")
	body := clipBytes(v.Body, prBodyMax, false)
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	if len(body) < len(v.Body) {
		b.WriteString(truncationMarker(len(body), len(v.Body)) + "\n")
	}
	return b.String()
}

// prSummary is the one line the UI and the prompt show for a PR row.
func prSummary(ref PRRef, v prView) string {
	s := fmt.Sprintf("PR %s/%s#%d %q", ref.Owner, ref.Repo, ref.Number, clipRunes(v.Title, 80))
	switch {
	case v.merged():
		s += " · merged " + dateOf(v.MergedAt)
	case strings.EqualFold(v.State, "closed"):
		s += " · closed"
	default:
		s += " · open"
	}
	return s + fmt.Sprintf(" · %d files", len(v.Files))
}

// dateOf is the YYYY-MM-DD of an ISO timestamp.
func dateOf(iso string) string {
	if len(iso) >= 10 {
		return iso[:10]
	}
	return iso
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// runHistory settles a PR's history row.
func (g *gatherer) runHistory(ctx context.Context, i int, clone string, view prView) {
	if !g.startRow(ctx, i) {
		return
	}
	start := g.now()
	g.mu.Lock()
	e := g.entries[i]
	g.mu.Unlock()

	var paths []string
	for _, f := range view.Files {
		if len(paths) == historyFilesMax {
			break
		}
		paths = append(paths, f.Path)
	}
	h := g.history(ctx, clone, view.sha(), view.merged(), paths)
	e.Call, e.TookMs = h.call, g.tookMs(start)
	if h.status != StatusOK {
		e.Status, e.Reason = h.status, h.reason
		g.settle(i, e)
		return
	}
	e.Summary = h.summary
	out := clipBytes(h.output, g.cfg.Gather.MaxBytes, false)
	status := StatusOK
	if len(out) < len(h.output) {
		status = StatusTruncated
	}
	g.writeRow(&e, fileHeader{Name: e.Name, Call: e.Call, TookMs: e.TookMs, Status: status}, out, len(h.output))
	g.settle(i, e)
}

// historyResult is what reading a change's history found.
type historyResult struct {
	status, reason string
	call           string
	output         string
	summary        string
}

// history reads, from the clone alone, which release first shipped sha,
// which release came before it, and who touched the change's files in
// between and before: the good and bad versions a bisect would start
// from. Nothing here fetches; a clone behind origin says so and stops.
func (g *gatherer) history(ctx context.Context, clone, sha string, merged bool, paths []string) historyResult {
	// The commit id and the paths come from gh's JSON, which is the
	// pull request author's text as much as GitHub's, and both go into
	// git's argv. The id must be an object name and nothing else (spec
	// §9); a path must carry no shell syntax, so the line the allow-list
	// judges cannot read differently from the argv that runs.
	if !commitID.MatchString(sha) {
		return historyResult{status: StatusFailed, reason: "the pull request names no valid commit"}
	}
	for _, p := range paths {
		if !safePath(p) {
			return historyResult{status: StatusFailed, reason: fmt.Sprintf("the pull request's file %q carries shell syntax, so its history was not read", p)}
		}
	}
	git, err := lookPath("git", g.in.Env, g.in.GOOS, g.cfg.Root)
	if err != nil {
		return historyResult{status: StatusFailed, reason: "git is not on PATH"}
	}
	if info, err := os.Stat(clone); err != nil || !info.IsDir() {
		return historyResult{status: StatusFailed, reason: "the clone at " + clone + " does not exist"}
	}
	policy := builtinPolicy(g.cfg)
	timeout := time.Duration(g.cfg.Gather.TimeoutSeconds) * time.Second
	var pathspec []string
	if len(paths) > 0 {
		pathspec = append(pathspec, "--")
		for _, p := range paths {
			pathspec = append(pathspec, ":(literal)"+p)
		}
	}

	// run judges and runs one git query. A refusal or a failure that
	// stops the row comes back as a non-nil result.
	run := func(args ...string) (string, execResult, *historyResult) {
		argv := append([]string{"git", "-C", clone}, args...)
		if d := decideLine(policy, DisplayLine(argv)); !d.Allow {
			return "", execResult{}, &historyResult{status: StatusDenied, reason: d.Message, call: DisplayLine(argv)}
		}
		res := g.exec(ctx, timeout, git, argv[1:], g.cfg.Gather.MaxBytes)
		if res.deadline || res.timedOut {
			st, why := res.verdict(g.cfg.Gather.TimeoutSeconds)
			return "", res, &historyResult{status: st, reason: why, call: DisplayLine(argv)}
		}
		return strings.TrimSpace(res.out.kept()), res, nil
	}

	_, res, stop := run("cat-file", "-e", sha+"^{commit}")
	if stop != nil {
		return *stop
	}
	if res.err != nil {
		return historyResult{status: StatusFailed, reason: "the clone does not have " + sha + "; it is behind origin"}
	}

	tags, res, stop := run("tag", "--contains", sha, "--sort=creatordate")
	if stop != nil {
		return *stop
	}
	first := ""
	if res.err == nil {
		first = firstLine(tags, 200)
	}
	if strings.HasPrefix(first, "-") {
		return dashTag(first)
	}
	firstDate := ""
	if first != "" {
		d, res, stop := run("log", "-1", "--format=%cI", first)
		if stop != nil {
			return *stop
		}
		if res.err == nil {
			firstDate = dateOf(d)
		}
	}
	prev, res, stop := run("describe", "--tags", "--abbrev=0", sha+"^")
	if stop != nil {
		return *stop
	}
	if res.err != nil {
		prev = ""
	}
	if strings.HasPrefix(prev, "-") {
		return dashTag(prev)
	}
	head, res, stop := run("log", "-1", "--format=%cI", "HEAD")
	if stop != nil {
		return *stop
	}
	if res.err != nil {
		head = ""
	}

	const format = "--format=%h %ad %an %s"
	var between []string
	if prev != "" {
		upper := first
		if upper == "" {
			upper = "HEAD"
		}
		between = append([]string{"log", "--date=iso-strict", format, "-n", "50", prev + ".." + upper}, pathspec...)
	} else {
		between = append([]string{"log", "--date=iso-strict", format, "-n", "30", sha + "^..HEAD"}, pathspec...)
	}
	call := DisplayLine(append([]string{"git", "-C", clone}, between...))
	betweenOut, res, stop := run(between...)
	if stop != nil {
		return *stop
	}
	betweenFailed := res.err != nil
	if betweenFailed {
		betweenOut = "(git log failed: " + firstLine(string(res.stderr.buf), 200) + ")"
	}
	before := append([]string{"log", "--date=iso-strict", format, "-n", "10", sha + "^"}, pathspec...)
	beforeOut, res, stop := run(before...)
	if stop != nil {
		return *stop
	}
	if res.err != nil {
		beforeOut = "(git log failed: " + firstLine(string(res.stderr.buf), 200) + ")"
	}

	verb := "Merged as"
	if !merged {
		verb = "Not merged; head at"
	}
	released := "not yet in a release"
	if first != "" {
		released = "first released in " + first + " (" + firstDate + ")"
	}
	previous := "no earlier release"
	if prev != "" {
		previous = "previous release " + prev
	}
	commits := 0
	if !betweenFailed && betweenOut != "" {
		commits = len(strings.Split(betweenOut, "\n"))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s; %s; %s\n", verb, short(sha), released, previous)
	if head != "" {
		b.WriteString("Clone HEAD at " + head + "\n")
	}
	b.WriteString("\n## Commits on these files between releases\n\n")
	b.WriteString(orNone(betweenOut) + "\n")
	b.WriteString("\n## Before the change\n\n")
	b.WriteString(orNone(beforeOut) + "\n")

	summaryPrev := "no earlier release"
	if prev != "" {
		summaryPrev = "previous " + prev
	}
	summary := fmt.Sprintf("%s %s; %s; %s · %d commits on these files",
		strings.ToLower(verb[:1])+verb[1:], short(sha), released, summaryPrev, commits)

	return historyResult{status: StatusOK, call: call, output: b.String(), summary: summary}
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "None."
	}
	return s
}

// commitID is a git object name as gh reports one: hex, at least an
// abbreviation's length, at most a SHA-256 id's.
var commitID = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// safePath reports whether a PR's file path is free of the characters the
// value rule refuses, bar whitespace, which real paths carry.
func safePath(p string) bool {
	for _, r := range p {
		if unicode.IsControl(r) || strings.ContainsRune("'\"`$;|&<>\\", r) {
			return false
		}
	}
	return p != ""
}

// dashTag refuses a tag name read back from the clone that git would
// take for a flag in the next query.
func dashTag(tag string) historyResult {
	return historyResult{status: StatusFailed, reason: fmt.Sprintf("the clone has a tag named %q, which would be read as a flag", tag)}
}
