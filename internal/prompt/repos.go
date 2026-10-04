package prompt

import (
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/repos"
)

// RepositoriesHeading names the section that lists the repositories a
// session may read. The Bundle pane reads the mention lines under it.
const RepositoriesHeading = "# Repositories"

// Repositories is what a prompt says about where code may be read: every
// repository the workspace configures (the workspace's own first), the
// repositories the ticket, thread or instruction mention, and the ones the
// operator asked the session to look in.
type Repositories struct {
	List     []repos.Repo
	Mentions []repos.Mention
	Asks     []repos.Ask
}

// empty reports whether there is nothing worth a section: a workspace with
// no companions, a ticket that mentions no other repository and an
// operator who asked for none reads exactly as it did before repos:.
func (r Repositories) empty() bool {
	companions := 0
	for _, repo := range r.List {
		if !repo.Workspace {
			companions++
		}
	}
	mentions := 0
	for _, m := range r.Mentions {
		if m.Status != repos.StatusWorkspace {
			mentions++
		}
	}
	return companions == 0 && mentions == 0 && len(r.Asks) == 0
}

// repositoriesSection tells the session which repositories it may read,
// that a fix happens only in the workspace's own, where to look first, and
// how to name a fix that belongs elsewhere so the fix flow can tell.
func repositoriesSection(r Repositories) string {
	if r.empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString(RepositoriesHeading + "\n\n")
	var companions []repos.Repo
	for _, repo := range r.List {
		if repo.Workspace {
			b.WriteString("You are running in the workspace repository, " + repo.Name + " at " + repo.Path + originNote(repo) +
				". It is the only repository a fix is ever made in.\n")
			continue
		}
		companions = append(companions, repo)
	}
	if len(companions) > 0 {
		b.WriteString("\nYou may also read these companion repositories, by absolute path. Like everything in this run they are read-only:\n")
		for _, repo := range companions {
			b.WriteString("- " + repo.Name + " — " + repo.Path + originNote(repo))
			if repo.About != "" {
				b.WriteString(": " + repo.About)
			}
			b.WriteString("\n")
		}
		b.WriteString("\nWhen the ticket's pull request, a stack trace or a file path points at a companion, look in that " +
			"repository. If the cause is in a companion, say which one in rootCause, and write proposedFix.files as " +
			"`<repository name>/<path>` (for example `" + companions[0].Name + "/src/…`), so the fix is not attempted here.\n")
	}

	var mentioned []repos.Mention
	for _, m := range r.Mentions {
		if m.Status != repos.StatusWorkspace {
			mentioned = append(mentioned, m)
		}
	}
	if len(mentioned) > 0 {
		b.WriteString("\nThe ticket, its thread or the operator mention these repositories:\n")
		for _, m := range mentioned {
			b.WriteString("- " + m.Phrase())
			if m.Ref != "" && m.Ref != m.Name {
				b.WriteString(" — named by " + m.Ref)
			}
			b.WriteString("\n")
		}
		for _, m := range mentioned {
			if m.Status == repos.StatusUnknown {
				b.WriteString("\nThe code a mention points at may live in a repository this workspace has no clone of. " +
					"Say so under open questions, naming it, rather than reasoning about code you have not read.\n")
				break
			}
		}
	}

	for _, a := range r.Asks {
		switch a.Status {
		case repos.StatusWorkspace, repos.StatusCompanion:
			b.WriteString("\nThe operator asked you to look in " + a.Name + ".\n")
		case repos.StatusAmbiguous:
			b.WriteString("\nThe operator asked you to look in " + a.Phrase + ", which matches more than one repository (" +
				strings.Join(a.Candidates, ", ") + "); look in each.\n")
		default:
			b.WriteString("\nThe operator asked you to look in " + a.Phrase + ", which is not a configured repository, so you " +
				"cannot read it here. Say so under open questions.\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func originNote(r repos.Repo) string {
	if r.Origin == "" {
		return ""
	}
	return " (origin " + r.Origin + ")"
}
