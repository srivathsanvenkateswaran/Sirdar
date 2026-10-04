package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/repos"
)

// RepoSummary is one repository as Settings lists it: the name an operator
// types, its one-line purpose, where it lives and comes from, and what a
// read-only look at the clone says.
type RepoSummary struct {
	Name      string      `json:"name"`
	About     string      `json:"about,omitempty"`
	Path      string      `json:"path"`
	Origin    string      `json:"origin,omitempty"`
	Workspace bool        `json:"workspace,omitempty"`
	Facts     repos.Facts `json:"facts"`
	// State is the facts in a few words: "main · 2 behind origin/main ·
	// fetched 3 days ago", "not found", "a plain directory".
	State string `json:"state"`
}

// repoFactsTimeout caps the git queries for one clone, so a repository on
// a stalled network mount cannot hold up Settings or the doctor report.
const repoFactsTimeout = 5 * time.Second

// summariseRepos looks at every repository the workspace may read, the
// clones in parallel. Nothing here fetches.
func summariseRepos(ctx context.Context, cfg *config.Config, now time.Time) []RepoSummary {
	list := cfg.Repositories()
	out := make([]RepoSummary, len(list))
	var wg sync.WaitGroup
	for i, r := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, repoFactsTimeout)
			defer cancel()
			f := repos.ReadFacts(ctx, r.Path)
			out[i] = RepoSummary{
				Name: r.Name, About: r.About, Path: r.Path, Origin: r.Origin, Workspace: r.Workspace,
				Facts: f, State: f.Summary(now),
			}
		}()
	}
	wg.Wait()
	return out
}

// reposCheck is the doctor's repos row: each companion repository by name
// with what its clone says — the branch, how far behind its upstream the
// last fetch left it, and when that fetch was. A companion whose path does
// not exist is a warning, not a failure: a triage still runs, it just
// cannot read that repository. No row when repos: names none.
func reposCheck(ctx context.Context, cfg *config.Config, now time.Time) (Check, bool) {
	if len(cfg.Repos) == 0 {
		return Check{}, false
	}
	var parts, missing []string
	for _, r := range summariseRepos(ctx, cfg, now) {
		if r.Workspace {
			continue
		}
		parts = append(parts, r.Name+": "+r.State)
		if !r.Facts.Exists {
			missing = append(missing, r.Name)
		}
	}
	check := Check{Name: "repos", OK: true, Detail: strings.Join(parts, "; ")}
	if len(missing) > 0 {
		check.Level = string(provider.LevelWarn)
		check.Detail += "; a session cannot read " + strings.Join(missing, ", ") + " until its path exists"
	}
	return check, true
}
