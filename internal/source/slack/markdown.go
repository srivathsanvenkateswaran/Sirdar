package slack

import (
	"fmt"
	"strings"
	"time"
)

// Markdown renders a thread as the bundle's slack.md: a heading, the link,
// and one `## time · slack · author` section per message, the same shape
// thread.md uses so a reader of one reads the other. The text is written as
// Slack sent it, with no direction marks added: a viewer that sets
// dir="auto" on each message lays an Arabic one out right-to-left and an
// English one left-to-right without the file choosing for it.
func Markdown(t Thread) string {
	var b strings.Builder
	b.WriteString("# Slack thread\n\n")
	if t.Link.URL != "" {
		fmt.Fprintf(&b, "Link: %s\n", t.Link.URL)
	}
	n := len(t.Messages)
	switch {
	case t.Truncated:
		fmt.Fprintf(&b, "Messages: %d (the first %d of a longer thread)\n\n", n, n)
	default:
		fmt.Fprintf(&b, "Messages: %d\n\n", n)
	}
	for _, m := range t.Messages {
		at := ""
		if !m.At.IsZero() {
			at = m.At.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(&b, "## %s · slack · %s\n\n", at, oneLine(m.Author))
		if text := strings.TrimSpace(m.Text); text != "" {
			b.WriteString(text)
			b.WriteString("\n\n")
		}
		if len(m.Titles) > 0 {
			fmt.Fprintf(&b, "Attachments: %s\n\n", strings.Join(m.Titles, ", "))
		}
	}
	return b.String()
}

// oneLine keeps an author name on its heading line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
