// Package notify posts a short digest of a finished run to a chat channel
// or a webhook receiver. It is the one place that knows what a Slack
// message, a Teams card and a generic JSON hook look like; the runner hands
// it an Event and never waits on the result of a post to decide what a run
// was worth.
//
// Nothing a note says goes over the wire. An Event carries the run's
// metadata and the paths to its notes, never a line of their body, and the
// ticket title only when the workspace opts in with notify.includeTitle: a
// support ticket's subject routinely names a customer, and a chat channel
// is a wider audience than the note directory.
package notify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// Event is what a finished run says about itself. Kind is the run's
// flavour: "triage", "rca", or "fix" for a future flow that records a
// human's fix.
type Event struct {
	Kind           string  `json:"kind"`
	Key            string  `json:"key"`
	Title          string  `json:"title,omitempty"`
	Status         string  `json:"status"`
	Confidence     string  `json:"confidence,omitempty"`
	Classification string  `json:"classification,omitempty"`
	Service        string  `json:"service,omitempty"`
	RunID          string  `json:"runId,omitempty"`
	NotePath       string  `json:"notePath,omitempty"`
	TrackerURL     string  `json:"trackerUrl,omitempty"`
	HelpdeskURL    string  `json:"helpdeskUrl,omitempty"`
	Turns          int     `json:"turns,omitempty"`
	CostUSD        float64 `json:"costUsd,omitempty"`
	Minutes        float64 `json:"minutes,omitempty"`
	Reason         string  `json:"reason,omitempty"`
	Workspace      string  `json:"workspace,omitempty"`
}

// Notifier posts one event to one destination.
type Notifier interface {
	Notify(ctx context.Context, ev Event) error
}

// DefaultOn are the terminal states a workspace is told about when it names
// none: every state a run can end in.
var DefaultOn = []string{"completed", "failed", "over_budget", "blocked"}

// DisableEnv turns every notification off for one invocation, for an
// operator re-running a batch who does not want the channel to hear about
// it a second time. It is read when the notifier is built, so a run started
// with it set has no destinations at all.
const DisableEnv = "SIRDAR_NO_NOTIFY"

// Disabled reports whether SIRDAR_NO_NOTIFY is set to anything but "" or
// "0".
func Disabled() bool {
	v := strings.TrimSpace(os.Getenv(DisableEnv))
	return v != "" && v != "0"
}

// Router is the notifier the runner holds: the destinations plus the rules
// that decide which runs reach them. A nil Router notifies nothing, so a
// workspace with no notify block needs no check at the call site.
type Router struct {
	Notifier     Notifier
	On           []string
	IncludeTitle bool
}

// Notify drops the event when its status is not one the workspace asked
// about, and strips the ticket title unless the workspace opted in.
func (r *Router) Notify(ctx context.Context, ev Event) error {
	if r == nil || r.Notifier == nil || !r.wants(ev.Status) {
		return nil
	}
	if !r.IncludeTitle {
		ev.Title = ""
	}
	return r.Notifier.Notify(ctx, ev)
}

func (r *Router) wants(status string) bool {
	on := r.On
	if len(on) == 0 {
		on = DefaultOn
	}
	for _, s := range on {
		if s == status {
			return true
		}
	}
	return false
}

// Multi posts to every destination and reports every failure. One webhook
// that is down must not cost the others their message, so the posts run
// side by side and the errors are joined.
type Multi []Notifier

func (m Multi) Notify(ctx context.Context, ev Event) error {
	if len(m) == 0 {
		return nil
	}
	errs := make([]error, len(m))
	var wg sync.WaitGroup
	for i, n := range m {
		wg.Add(1)
		go func(i int, n Notifier) {
			defer wg.Done()
			errs[i] = n.Notify(ctx, ev)
		}(i, n)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// FromConfig builds the workspace's notifier, resolving every credential
// reference the notify block names. It returns nil — not an error — when
// the workspace configures no destination, and nil when SIRDAR_NO_NOTIFY is
// set.
func FromConfig(cfg *config.Config, creds config.Resolver) (Notifier, error) {
	if cfg == nil || cfg.Notify == nil || Disabled() {
		return nil, nil
	}
	n := cfg.Notify
	var dests Multi

	if n.Slack != nil {
		webhook, err := webhookURL("notify.slack.webhookUrl", n.Slack.WebhookURL, creds)
		if err != nil {
			return nil, err
		}
		dests = append(dests, &Slack{WebhookURL: webhook})
	}
	if n.Teams != nil {
		webhook, err := webhookURL("notify.teams.webhookUrl", n.Teams.WebhookURL, creds)
		if err != nil {
			return nil, err
		}
		dests = append(dests, &Teams{WebhookURL: webhook})
	}
	for i, g := range n.Generic {
		key := fmt.Sprintf("notify.generic[%d]", i)
		hook := &Generic{URL: g.URL}
		if len(g.Headers) > 0 {
			hook.Headers = make(map[string]string, len(g.Headers))
		}
		for name, value := range g.Headers {
			resolved, err := maybeResolve(key+".headers."+name, value, creds)
			if err != nil {
				return nil, err
			}
			hook.Headers[name] = resolved
		}
		if g.Secret != "" {
			secret, err := creds.Resolve(g.Secret)
			if err != nil {
				return nil, fmt.Errorf("%s.secret: %w", key, err)
			}
			hook.Secret = secret
		}
		dests = append(dests, hook)
	}

	if len(dests) == 0 {
		return nil, nil
	}
	return &Router{Notifier: dests, On: n.On, IncludeTitle: n.IncludeTitle}, nil
}

// webhookURL resolves an incoming-webhook reference and checks what comes
// back is a URL Sirdar will post a run's metadata to: https, or http on the
// loopback interface for a receiver on this machine.
func webhookURL(key, ref string, creds config.Resolver) (string, error) {
	raw, err := creds.Resolve(ref)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	raw = strings.TrimSpace(raw)
	if err := config.ValidateWebhookURL(key, raw); err != nil {
		// The value came out of a credential store, so it must not be
		// echoed: ValidateWebhookURL quotes what it was given.
		return "", fmt.Errorf("%s: %s resolved to something that is not an https URL (or http on loopback)", key, ref)
	}
	return raw, nil
}

// maybeResolve reads a header value that may be a credential reference. A
// plain value is a plain header — Accept, a routing hint — and is passed
// through as it stands.
func maybeResolve(key, value string, creds config.Resolver) (string, error) {
	if !strings.HasPrefix(value, "env:") && !strings.HasPrefix(value, "keychain:") {
		return value, nil
	}
	v, err := creds.Resolve(value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

// redact reduces a webhook URL to its scheme and host. An incoming-webhook
// URL is a bearer credential in its path, so it must never reach an error
// message, a log line, or a run's warnings.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "the webhook URL"
	}
	return u.Scheme + "://" + u.Host + "/…"
}
