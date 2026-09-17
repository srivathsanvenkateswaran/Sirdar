package config

import (
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// QueueTypeWildcard is the entry that turns the type filter off. It is
// spelled "*" rather than "all" or "any" because those are type names a
// tracker could plausibly use and this must never collide with one.
const QueueTypeWildcard = "*"

// defaultQueueTypes is what the queue shows when a workspace configures no
// list: bug tickets and nothing else. The board is a support queue, and the
// rest of a project's work — sub-tasks, chores, spikes — is assigned to the
// same person without being anything Sirdar should offer to triage.
var defaultQueueTypes = []string{"bug"}

// QueueTypes is the tracker queue's effective type filter: the configured
// list folded onto the canonical vocabulary, or the default when the
// workspace names none. An operator writing their own tracker's word for it
// ("Defect") gets the same filter as one writing "bug".
//
// An empty (but present) list and one holding "*" both come back as the
// wildcard, which QueueTypesAll reports on.
func (c *Config) QueueTypes() []string {
	if c == nil || c.Sources.Tracker == nil || c.Sources.Tracker.Queue == nil || c.Sources.Tracker.Queue.Types == nil {
		return append([]string(nil), defaultQueueTypes...)
	}
	return NormalizeQueueTypes(*c.Sources.Tracker.Queue.Types)
}

// NormalizeQueueTypes folds a written type list onto the canonical
// vocabulary, dropping blanks and repeats and keeping the wildcard as it
// is. A list that held nothing but blanks comes back empty, which means the
// same as "*": the operator asked for no narrowing.
func NormalizeQueueTypes(types []string) []string {
	out := make([]string, 0, len(types))
	seen := make(map[string]bool, len(types))
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t != QueueTypeWildcard {
			t = ticket.CanonicalType(t)
		}
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// QueueTypesAll reports whether a normalized list means "every type":
// empty, or naming the wildcard. It is also the only shape of the filter
// that keeps a ticket whose tracker reports no type at all.
func QueueTypesAll(types []string) bool {
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		if t == QueueTypeWildcard {
			return true
		}
	}
	return false
}

// QueueTypeIsDefault reports whether a normalized list is the shipped
// default, which is what lets a screen say "no bug tickets" rather than
// listing the types the operator chose back at them.
func QueueTypeIsDefault(types []string) bool {
	if len(types) != len(defaultQueueTypes) {
		return false
	}
	for i, t := range types {
		if t != defaultQueueTypes[i] {
			return false
		}
	}
	return true
}

// MatchQueueType reports whether a ticket of this type belongs in a queue
// filtered by types. types must already be normalized.
//
// A ticket whose tracker reports no type passes only under the wildcard.
// That asymmetry is deliberate in both directions: an adapter that cannot
// say what its tickets are must not have every one of them silently pass a
// bug filter, and an operator who turned the filter off must not lose them
// either. `sirdar doctor` is what tells the operator which case they are in.
func MatchQueueType(types []string, ticketType string) bool {
	if QueueTypesAll(types) {
		return true
	}
	t := ticket.CanonicalType(ticketType)
	if t == "" {
		return false
	}
	for _, want := range types {
		if want == t {
			return true
		}
	}
	return false
}
