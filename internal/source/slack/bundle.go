package slack

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// KeyPrefix starts every synthetic key a Slack-only run is filed under.
const KeyPrefix = "SLACK-"

// syntheticKey is SLACK-<channel>-<ts seconds>.
var syntheticKey = regexp.MustCompile(`^SLACK-[A-Z0-9]{6,}-[0-9]{9,}$`)

// IsKey reports whether s is a synthetic Slack key.
func IsKey(s string) bool { return syntheticKey.MatchString(s) }

// KeyFor is the synthetic key a thread with no ticket is triaged under:
// SLACK-<channel>-<seconds of the thread's ts>, so the same thread pasted
// twice files its second run beside its first.
func KeyFor(l Link) string {
	ts := l.ThreadTS
	if ts == "" {
		ts = l.TS
	}
	sec, _, _ := strings.Cut(ts, ".")
	return KeyPrefix + strings.ToUpper(l.Channel) + "-" + sec
}

// titleMax is the most characters a Slack-only ticket's title keeps.
const titleMax = 80

var (
	// fieldLine is a "Name: value" line, Slack's *bold* and _italic_
	// markers allowed around either side.
	fieldLine = regexp.MustCompile(`^[\s*_~>•-]*([\p{L}][\p{L}\p{N} _/.()-]{0,39}?)[\s*_~]*:[\s*_~]*(.+?)[\s*_~]*$`)
	linkIn    = regexp.MustCompile(`https?://[^\s<>"|]+`)
	markup    = strings.NewReplacer("*", "", "_", "", "~", "", "`", "")
)

// Bundle is the ticket bundle a thread with no ticket reference is
// triaged from: the first message is the ticket — its first line the
// title, its text the description, its "Name: value" lines the fields —
// every message is the conversation, the URLs the messages carry are its
// links, and the files they list are its attachments, to be downloaded by
// whoever holds a token or named as unread. Tracker and Helpdesk stay nil.
func (t Thread) Bundle(key string) ticket.Bundle {
	var b ticket.Bundle
	r := &ticket.ReportedTicket{Source: "slack", Key: key, URL: t.Link.URL}
	if len(t.Messages) > 0 {
		first := t.Messages[0]
		r.Description = strings.TrimSpace(first.Text)
		r.Author = first.Author
		r.At = first.At
		r.Title = Title(first.Text)
		r.Fields = Fields(first.Text)
	}
	seen := map[string]bool{}
	for _, m := range t.Messages {
		msg := ticket.Message{At: m.At, Author: m.Author, Role: ticket.RoleCustomer, Text: m.Text}
		if m.Author != r.Author {
			msg.Role = ticket.RoleAgent
		}
		for _, raw := range linkIn.FindAllString(m.Text, -1) {
			u := strings.TrimRight(raw, ".,;:)]}'>")
			if !seen[u] {
				seen[u] = true
				r.Links = append(r.Links, u)
			}
		}
		b.Thread = append(b.Thread, msg)
	}
	b.Reported = r
	return b
}

// Files are every file the thread's messages list, in order.
func (t Thread) Files() []File {
	var out []File
	for _, m := range t.Messages {
		out = append(out, m.Files...)
	}
	return out
}

// Title is the first non-blank line of a message with Slack's markup taken
// off, cut to 80 characters on a character boundary. It is not given a
// direction: a viewer lays an Arabic title out right-to-left by itself.
func Title(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(markup.Replace(line)), " ")
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > titleMax {
			r := []rune(line)
			line = strings.TrimSpace(string(r[:titleMax-1])) + "…"
		}
		return line
	}
	return ""
}

// Fields are the "Name: value" lines of a message, in the order written:
// CompanyID, Domain, Expected, Actual and whatever else a reporter labels.
// A line whose value is only a URL keeps the URL as its value; a line
// whose "name" is itself a URL scheme (https:) is not a field.
func Fields(text string) []ticket.Field {
	var out []ticket.Field
	for _, line := range strings.Split(text, "\n") {
		m := fieldLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := strings.TrimSpace(markup.Replace(m[1]))
		value := strings.TrimSpace(m[2])
		lower := strings.ToLower(name)
		if name == "" || value == "" || lower == "http" || lower == "https" || strings.HasPrefix(value, "//") {
			continue
		}
		value = strings.TrimSpace(strings.Trim(value, "<>"))
		if i := strings.Index(value, "|"); i > 0 && strings.HasPrefix(value, "http") {
			value = value[:i]
		}
		out = append(out, ticket.Field{Name: name, Value: value})
	}
	return out
}
