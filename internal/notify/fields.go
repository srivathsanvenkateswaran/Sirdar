package notify

import (
	"fmt"
	"strings"
)

// field is one label/value pair of the digest, in the order every
// destination renders them.
type field struct{ Label, Value string }

// fields are the run's numbers, minus the ones that have nothing to say: a
// blocked run has no confidence, a local model has no cost.
func fields(ev Event) []field {
	out := make([]field, 0, 6)
	add := func(label, value string) {
		if value != "" {
			out = append(out, field{label, value})
		}
	}
	add("Confidence", ev.Confidence)
	add("Classification", ev.Classification)
	add("Service", ev.Service)
	if ev.CostUSD > 0 {
		add("Cost", fmt.Sprintf("$%.2f", ev.CostUSD))
	}
	if ev.Minutes > 0 {
		add("Duration", fmt.Sprintf("%.1f min", ev.Minutes))
	}
	if ev.Turns > 0 {
		add("Turns", fmt.Sprintf("%d", ev.Turns))
	}
	return out
}

// headline is the first line of every message: which ticket, and how it
// ended.
func headline(ev Event) string {
	key := ev.Key
	if key == "" {
		key = "run"
	}
	status := ev.Status
	if status == "" {
		status = "finished"
	}
	return "[" + key + "] " + status
}

// subtitle names the run under the headline: its kind, its id, and the
// workspace it belongs to, for a channel that hears from several.
func subtitle(ev Event) string {
	parts := make([]string, 0, 3)
	if ev.Kind != "" {
		parts = append(parts, ev.Kind)
	}
	if ev.Workspace != "" {
		parts = append(parts, ev.Workspace)
	}
	if ev.RunID != "" {
		parts = append(parts, "run "+ev.RunID)
	}
	return strings.Join(parts, " · ")
}

// maxReasonLen and maxTitleLen bound the two free-text fields a card
// renders. Slack rejects a section whose text passes 3000 characters, and
// neither field needs anywhere near that to say what it is meant to say;
// the real reason to cap them is that either one carries text Sirdar did
// not write itself — a ticket's subject line, an error a provider returned
// — and neither should be able to blow up the message it appears in.
const (
	maxReasonLen = 200
	maxTitleLen  = 120
)

// truncateReason and truncateTitle cut a field to its limit, marking the
// cut with an ellipsis so a reader knows the field was shortened rather
// than that is all there was.
func truncateReason(s string) string { return truncateRunes(s, maxReasonLen) }
func truncateTitle(s string) string  { return truncateRunes(s, maxTitleLen) }

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
