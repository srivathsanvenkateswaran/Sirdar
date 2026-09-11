package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Teams posts an Adaptive Card to a Microsoft Teams webhook: either a
// channel's Workflows (Power Automate) URL, which is what Microsoft's
// retired Office 365 connectors were replaced by, or a connector URL on a
// tenant that still has them. Both accept the same message envelope.
type Teams struct {
	WebhookURL string
	Client     *http.Client

	timeout time.Duration
}

func (t *Teams) Notify(ctx context.Context, ev Event) error {
	body, err := json.Marshal(teamsPayload(ev))
	if err != nil {
		return fmt.Errorf("notify: teams: build card: %w", err)
	}
	return transport{client: t.Client, timeout: t.timeout}.post(ctx, t.WebhookURL, body, nil)
}

type teamsMessage struct {
	Type        string            `json:"type"`
	Attachments []teamsAttachment `json:"attachments"`
}

type teamsAttachment struct {
	ContentType string    `json:"contentType"`
	ContentURL  *string   `json:"contentUrl"`
	Content     teamsCard `json:"content"`
}

type teamsCard struct {
	Schema  string         `json:"$schema"`
	Type    string         `json:"type"`
	Version string         `json:"version"`
	Body    []teamsElement `json:"body"`
	Actions []teamsAction  `json:"actions,omitempty"`
}

type teamsElement struct {
	Type     string      `json:"type"`
	Text     string      `json:"text,omitempty"`
	Weight   string      `json:"weight,omitempty"`
	Size     string      `json:"size,omitempty"`
	IsSubtle bool        `json:"isSubtle,omitempty"`
	Wrap     bool        `json:"wrap,omitempty"`
	Facts    []teamsFact `json:"facts,omitempty"`
}

type teamsFact struct {
	Title string `json:"title"`
	Value string `json:"value"`
}

type teamsAction struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func teamsPayload(ev Event) teamsMessage {
	card := teamsCard{
		Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
		Type:    "AdaptiveCard",
		Version: "1.4",
	}
	card.Body = append(card.Body, teamsElement{
		Type: "TextBlock", Text: headline(ev), Weight: "Bolder", Size: "Large", Wrap: true,
	})
	if sub := subtitle(ev); sub != "" {
		card.Body = append(card.Body, teamsElement{Type: "TextBlock", Text: sub, IsSubtle: true, Wrap: true})
	}
	if ev.Title != "" {
		card.Body = append(card.Body, teamsElement{Type: "TextBlock", Text: truncateTitle(ev.Title), Wrap: true})
	}
	if fs := fields(ev); len(fs) > 0 {
		facts := make([]teamsFact, 0, len(fs))
		for _, f := range fs {
			facts = append(facts, teamsFact{Title: f.Label, Value: f.Value})
		}
		card.Body = append(card.Body, teamsElement{Type: "FactSet", Facts: facts})
	}
	if ev.Reason != "" {
		card.Body = append(card.Body, teamsElement{Type: "TextBlock", Text: "Reason: " + truncateReason(ev.Reason), Wrap: true})
	}
	if ev.NotePath != "" {
		card.Body = append(card.Body, teamsElement{Type: "TextBlock", Text: "note: " + ev.NotePath, IsSubtle: true, Wrap: true})
	}
	if ev.TrackerURL != "" {
		card.Actions = append(card.Actions, teamsAction{Type: "Action.OpenUrl", Title: "Tracker", URL: ev.TrackerURL})
	}
	if ev.HelpdeskURL != "" {
		card.Actions = append(card.Actions, teamsAction{Type: "Action.OpenUrl", Title: "Helpdesk", URL: ev.HelpdeskURL})
	}

	return teamsMessage{
		Type: "message",
		Attachments: []teamsAttachment{{
			ContentType: "application/vnd.microsoft.card.adaptive",
			Content:     card,
		}},
	}
}
