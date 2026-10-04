package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/srivathsanvenkateswaran/sirdar/internal/repos"
)

// RepoConfig is one companion repository under repos:.
//
//	repos:
//	  - name: Acme.Web
//	    path: ~/code/Acme.Web
//	    about: Angular frontend
//	    origin: git@github.com:acme/Acme.Web.git   # optional
//
// Path may start with ~. Origin defaults to what `git remote get-url
// origin` says in the clone, read once per loaded configuration; it is what
// a ticket's "acme/Acme.Web#828" or GitHub link is matched against. A path
// that is not a git clone is still allowed — it is read as a plain
// directory, and only its name can be matched.
type RepoConfig struct {
	Name   string `yaml:"name"`
	Path   string `yaml:"path"`
	Origin string `yaml:"origin,omitempty"`
	About  string `yaml:"about,omitempty"`
}

// validateRepos checks repos:. A name is what an operator types and what a
// mention resolves to, so it has to be there and has to be unique; a path
// widens the read scope, so it follows the readAlso rule — absolute or
// starting with ~ — and may not be the workspace itself. A path that does
// not exist is not an error here: `sirdar doctor` says so, and a clone
// that has not been made yet should not stop every run.
func validateRepos(c *Config) error {
	seen := map[string]int{}
	for i, r := range c.Repos {
		key := fmt.Sprintf("config: repos[%d]", i)
		name := strings.TrimSpace(r.Name)
		switch {
		case name == "":
			return fmt.Errorf("%s.name: is required", key)
		case strings.ContainsAny(name, " \t/\\#"):
			return fmt.Errorf("%s.name: %q may not contain spaces, slashes or #", key, r.Name)
		}
		if j, ok := seen[strings.ToLower(name)]; ok {
			return fmt.Errorf("%s.name: %q is already repos[%d]", key, r.Name, j)
		}
		seen[strings.ToLower(name)] = i
		path := strings.TrimSpace(r.Path)
		switch {
		case path == "":
			return fmt.Errorf("%s.path: is required", key)
		case !strings.HasPrefix(path, "~") && !filepath.IsAbs(path):
			return fmt.Errorf("%s.path: %q must be an absolute path or start with ~", key, r.Path)
		}
		if c.Root != "" && filepath.Clean(c.ExpandPath(path)) == filepath.Clean(c.Root) {
			return fmt.Errorf("%s.path: %q is the workspace itself, which is always readable", key, r.Path)
		}
		if o := strings.TrimSpace(r.Origin); o != "" && repos.Slug(o) == "" {
			return fmt.Errorf("%s.origin: %q is not a git remote URL (git@host:owner/name.git or https://host/owner/name)", key, r.Origin)
		}
	}
	return nil
}

// repoCache holds the resolved repository list of one loaded configuration,
// so the origins are read once however often a run or a resolver asks.
type repoCache struct {
	once sync.Once
	list []repos.Repo
}

// Repositories is every repository a session in this workspace may read:
// the workspace's own first, then each companion from repos:, with ~
// expanded and each origin filled in from the clone when the config gave
// none.
func (c *Config) Repositories() []repos.Repo {
	if c.repos == nil {
		return c.readRepositories()
	}
	c.repos.once.Do(func() { c.repos.list = c.readRepositories() })
	return c.repos.list
}

func (c *Config) readRepositories() []repos.Repo {
	out := make([]repos.Repo, 0, 1+len(c.Repos))
	if c.Root != "" {
		name := strings.TrimSpace(c.Workspace)
		if name == "" {
			name = filepath.Base(c.Root)
		}
		out = append(out, repos.Repo{Name: name, Path: c.Root, Origin: repos.Origin(c.Root), Workspace: true})
	}
	for _, r := range c.Repos {
		path := filepath.Clean(c.ExpandPath(strings.TrimSpace(r.Path)))
		origin := strings.TrimSpace(r.Origin)
		if origin == "" {
			origin = repos.Origin(path)
		}
		out = append(out, repos.Repo{
			Name:   strings.TrimSpace(r.Name),
			Path:   path,
			Origin: origin,
			About:  strings.TrimSpace(r.About),
		})
	}
	return out
}

// ReadAlso is the whole of what widens a session's read scope:
// permissions.readAlso as written, then each companion repository's path,
// expanded. The two lists do the same job; repos: adds a name, an origin and
// a purpose to the path, and readAlso stays for directories that are not
// repositories.
func (c *Config) ReadAlso() []string {
	out := append([]string(nil), c.Permissions.ReadAlso...)
	for _, r := range c.Repos {
		p := strings.TrimSpace(r.Path)
		if p == "" {
			continue
		}
		abs := filepath.Clean(c.ExpandPath(p))
		out = append(out, abs)
		// The scope compares a read against its resolved form too, so a
		// clone reached through a symlink is named both ways.
		if real, err := filepath.EvalSymlinks(abs); err == nil && real != abs {
			out = append(out, real)
		}
	}
	return out
}
