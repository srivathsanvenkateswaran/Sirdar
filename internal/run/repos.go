package run

import (
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/repos"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// repositories is what the prompt says about where code may be read: the
// workspace's configured repositories, every repository the ticket, its
// thread, the Slack thread or the operator's words mention, and the ones
// the operator asked the session to look in.
func repositories(cfg *config.Config, bundle ticket.Bundle, slackMD, instruction string) prompt.Repositories {
	list := cfg.Repositories()
	return prompt.Repositories{
		List:     list,
		Mentions: repos.Mentions(bundleText(bundle, slackMD, instruction), list),
		Asks:     repos.Asks(instruction, list),
	}
}

// bundleText is everything a person wrote that came with the ticket, in the
// order the prompt shows it.
func bundleText(b ticket.Bundle, slackMD, instruction string) string {
	parts := []string{instruction}
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
	parts = append(parts, slackMD)
	return strings.Join(parts, "\n")
}

// unconfigured drops from others (owner/name slugs on github.com) the
// repositories configured under repos:, which the prompt's # Repositories
// section already lists as readable; what is left is code the session
// cannot read from here.
func unconfigured(others []string, list []repos.Repo) []string {
	var out []string
	for _, o := range others {
		slug := repos.Slug("https://github.com/" + o)
		known := false
		for _, r := range list {
			if r.Slug() != "" && r.Slug() == slug {
				known = true
				break
			}
		}
		if !known {
			out = append(out, o)
		}
	}
	return out
}
