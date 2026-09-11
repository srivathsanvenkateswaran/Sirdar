package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Slack posts a Block Kit message to an incoming webhook.
//
// An incoming webhook is bound to the channel it was created for, so there
// is no channel override: Slack has ignored the legacy `channel` field on
// app-created webhooks since 2018, and sending it changes nothing. To post
// to a different channel, create a second webhook for it and add it to the
// configuration.
type Slack struct {
	WebhookURL string
	Client     *http.Client

	// timeout overrides the 10 s per-post limit; tests set it.
	timeout time.Duration
}

func (s *Slack) Notify(ctx context.Context, ev Event) error {
	body, err := json.Marshal(slackPayload(ev))
	if err != nil {
		return fmt.Errorf("notify: slack: build message: %w", err)
	}
	return transport{client: s.Client, timeout: s.timeout}.post(ctx, s.WebhookURL, body, nil)
}

type slackBlock struct {
	Type     string      `json:"type"`
	Text     *slackText  `json:"text,omitempty"`
	Fields   []slackText `json:"fields,omitempty"`
	Elements []slackText `json:"elements,omitempty"`
}

type slackText struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emoji *bool  `json:"emoji,omitempty"`
}

type slackMessage struct {
	Text   string       `json:"text"`
	Blocks []slackBlock `json:"blocks"`
}

func slackPayload(ev Event) slackMessage {
	msg := slackMessage{Text: headline(ev)}
	msg.Blocks = append(msg.Blocks, slackBlock{
		Type: "header",
		Text: &slackText{Type: "plain_text", Text: headline(ev)},
	})
	if sub := subtitle(ev); sub != "" {
		msg.Blocks = append(msg.Blocks, slackBlock{
			Type:     "context",
			Elements: []slackText{{Type: "mrkdwn", Text: slackEscape(sub)}},
		})
	}
	if ev.Title != "" {
		msg.Blocks = append(msg.Blocks, slackBlock{
			Type: "section",
			Text: &slackText{Type: "mrkdwn", Text: slackEscape(ev.Title)},
		})
	}
	if fs := fields(ev); len(fs) > 0 {
		texts := make([]slackText, 0, len(fs))
		for _, f := range fs {
			texts = append(texts, slackText{Type: "mrkdwn", Text: "*" + f.Label + "*\n" + slackEscape(f.Value)})
		}
		msg.Blocks = append(msg.Blocks, slackBlock{Type: "section", Fields: texts})
	}
	if ev.Reason != "" {
		msg.Blocks = append(msg.Blocks, slackBlock{
			Type: "section",
			Text: &slackText{Type: "mrkdwn", Text: "*Reason*\n" + slackEscape(ev.Reason)},
		})
	}
	if links := slackLinks(ev); links != "" {
		msg.Blocks = append(msg.Blocks, slackBlock{
			Type: "section",
			Text: &slackText{Type: "mrkdwn", Text: links},
		})
	}
	if ev.NotePath != "" {
		msg.Blocks = append(msg.Blocks, slackBlock{
			Type:     "context",
			Elements: []slackText{{Type: "mrkdwn", Text: "note: `" + slackEscape(ev.NotePath) + "`"}},
		})
	}
	return msg
}

func slackLinks(ev Event) string {
	var links []string
	if ev.TrackerURL != "" {
		links = append(links, slackLink(ev.TrackerURL, "Tracker"))
	}
	if ev.HelpdeskURL != "" {
		links = append(links, slackLink(ev.HelpdeskURL, "Helpdesk"))
	}
	return strings.Join(links, " · ")
}

// slackLink builds a <url|label> link. A URL carrying the characters that
// close the link would break the block, so such a URL is dropped from the
// message rather than rendered wrong.
func slackLink(url, label string) string {
	if strings.ContainsAny(url, "<>|") {
		return label + " (link omitted)"
	}
	return "<" + url + "|" + label + ">"
}

// slackEscape escapes the three characters Slack's mrkdwn treats as
// control characters.
var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func slackEscape(s string) string { return slackEscaper.Replace(s) }
