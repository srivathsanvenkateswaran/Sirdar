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
