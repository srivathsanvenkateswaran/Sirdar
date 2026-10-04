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
	for _, m := range b.Thread {
		parts = append(parts, m.Text)
	}
	parts = append(parts, slackMD)
	return strings.Join(parts, "\n")
}
