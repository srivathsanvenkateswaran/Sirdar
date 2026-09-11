// Package webhooks receives inbound triggers from a tracker or a helpdesk
// and turns them into the ticket keys Sirdar should triage.
//
// Each vendor gets its own Verifier: one half checks that the request
// really came from that vendor, the other pulls the ticket key out of the
// body. The two halves are separate because the vendors disagree about
// both — Linear signs the raw body with HMAC-SHA256, Azure DevOps sends
// basic auth, Jira Cloud signs nothing at all — while the thing Sirdar
// wants out of every one of them is the same: a key, an event name, and
// whoever the ticket is now assigned to.
//
// Nothing here fetches the ticket. A webhook body is a notification, not a
// source of truth: the key it carries is handed to the normal triage path,
// which reads the ticket through the configured adapter.
package webhooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxBodyBytes is the largest webhook body that will be read. Every vendor
// here sends a few kilobytes; a megabyte is room for an Azure DevOps
// "resource details: All" payload with a long description and nothing more.
const MaxBodyBytes = 1 << 20

// The failures a receiver reports, each mapped to one HTTP status by the
// caller: 401, 404, 413, 400.
var (
	// ErrUnauthorized means the request did not prove it came from the
	// source it claims. Every verifier returns this and nothing more
	// specific: telling a caller which half of a signature was wrong
	// helps only a caller who is guessing.
	ErrUnauthorized = errors.New("webhooks: verification failed")
	// ErrUnknownSource names a source this receiver does not serve.
	ErrUnknownSource = errors.New("webhooks: unknown source")
	// ErrTooLarge means the body exceeded MaxBodyBytes.
	ErrTooLarge = errors.New("webhooks: body too large")
	// ErrBadPayload means the body verified but could not be read as the
	// shape the source documents.
	ErrBadPayload = errors.New("webhooks: payload not understood")
)

// Trigger is one ticket a delivery asks Sirdar to look at.
type Trigger struct {
	// Source is the configured source name the delivery arrived under.
	Source string
	// Key is the ticket key as the tracker or helpdesk names it.
	Key string
	// Event is the vendor's own name for what happened, kept for the
	// hook.received event and for the operator reading the log.
	Event string
	// Assignee is whoever the payload says the ticket now belongs to: an
	// email where the vendor sends one, otherwise an account id or a
	// display name. Empty when the payload carries no assignee.
	Assignee string
	// Raw is the JSON this trigger was read out of — the whole body for
	// most sources, one element for a source that batches events.
	Raw json.RawMessage
}

// Verifier is one source's half of the receiver: authenticate the request,
// then read the triggers out of its body.
//
// Verify is given the request with its body already read, because two of
// the schemes here sign the exact bytes that arrived and a re-encoded body
// would not match. It must not read r.Body.
type Verifier interface {
	Verify(r *http.Request, body []byte) error
	Extract(body []byte) ([]Trigger, error)
}

// Receiver holds the verifiers for the sources a workspace enabled and the
// filter that decides which triggers are worth a run.
type Receiver struct {
	sources map[string]Verifier
	match   Match
}

// New returns a Receiver over the named sources.
func New(sources map[string]Verifier, match Match) *Receiver {
	m := make(map[string]Verifier, len(sources))
	for name, v := range sources {
		if v != nil {
			m[name] = v
		}
	}
	return &Receiver{sources: m, match: normalise(match)}
}

// normalise trims a Match so the comparisons in Allows need not.
func normalise(m Match) Match {
	m.Assignee = strings.TrimSpace(m.Assignee)
	m.Statuses = trimAll(m.Statuses)
	m.Labels = trimAll(m.Labels)
	return m
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Sources lists the source names this receiver serves.
func (rc *Receiver) Sources() []string {
	out := make([]string, 0, len(rc.sources))
	for name := range rc.sources {
		out = append(out, name)
	}
	return out
}

// Has reports whether source is one this receiver serves.
func (rc *Receiver) Has(source string) bool {
	_, ok := rc.sources[source]
	return ok
}

// Accept reads the request body, verifies it against the named source, and
// returns every trigger the body carries — including the ones the match
// filter will reject, so the caller can say why a delivery did nothing.
//
// The body is read here rather than by the caller because the signature
// schemes cover the bytes as they arrived.
func (rc *Receiver) Accept(r *http.Request, source string) ([]Trigger, error) {
	v, ok := rc.sources[source]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSource, source)
	}
	body, err := ReadBody(r)
	if err != nil {
		return nil, err
	}
	if err := v.Verify(r, body); err != nil {
		return nil, err
	}
	triggers, err := v.Extract(body)
	if err != nil {
		return nil, err
	}
	out := triggers[:0]
	for _, t := range triggers {
		t.Source = source
		// A key that is not one plain path element would be joined into
		// the run tree, so it is dropped here rather than sanitised: a
		// delivery naming such a key is not naming a ticket.
		if !ValidKey(t.Key) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// Allows reports whether a trigger passes the workspace's match filter,
// and when it does not, why.
func (rc *Receiver) Allows(t Trigger) (bool, string) { return rc.match.Allows(t) }

// ReadBody reads at most MaxBodyBytes from the request.
func ReadBody(r *http.Request) ([]byte, error) {
	if r.ContentLength > MaxBodyBytes {
		return nil, ErrTooLarge
	}
	// One byte past the cap, so a body exactly at the cap still reads and
	// anything over it is caught without reading the rest.
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("webhooks: read body: %w", err)
	}
	if len(body) > MaxBodyBytes {
		return nil, ErrTooLarge
	}
	return body, nil
}

// keyRejects are the characters that stop a key being one plain path
// element: separators, and the glob metacharacters the run store would act
// on. It matches what internal/app refuses for a key arriving over HTTP.
const keyRejects = `/\*?[`

// ValidKey reports whether a key read out of a payload is safe to hand to
// the run store.
func ValidKey(key string) bool {
	switch {
	case key == "", key == ".", key == "..":
		return false
	case strings.ContainsAny(key, keyRejects):
		return false
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
