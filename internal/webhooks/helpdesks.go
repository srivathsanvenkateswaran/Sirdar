package webhooks

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// --- Zendesk ----------------------------------------------------------

// Zendesk verifies a Zendesk webhook: the signature is the base64 of
// HMAC-SHA256 over the timestamp header concatenated with the raw body,
// under the webhook's signing secret. The timestamp is part of the signed
// message, so checking it against the clock turns a captured delivery into
// something that stops working.
type Zendesk struct {
	Secret string
	clock
}

// The two headers a Zendesk delivery carries.
const (
	ZendeskSignatureHeader = "X-Zendesk-Webhook-Signature"
	ZendeskTimestampHeader = "X-Zendesk-Webhook-Signature-Timestamp"
)

// ReplayWindow is how old a signed timestamp may be. It is the same five
// minutes for Zendesk and HubSpot, which is HubSpot's documented maximum
// and a reasonable bound for a delivery that has crossed the internet once.
const ReplayWindow = 5 * time.Minute

func (z Zendesk) Verify(r *http.Request, body []byte) error {
	if z.Secret == "" {
		return ErrUnauthorized
	}
	ts := r.Header.Get(ZendeskTimestampHeader)
	stamp, err := time.Parse(time.RFC3339, strings.TrimSpace(ts))
	if err != nil || !withinWindow(z.now(), stamp, ReplayWindow) {
		return ErrUnauthorized
	}
	signed := append([]byte(ts), body...)
	if !equalSig(r.Header.Get(ZendeskSignatureHeader), Base64HMACSHA256(z.Secret, signed), false) {
		return ErrUnauthorized
	}
	return nil
}

func (z Zendesk) Extract(body []byte) ([]Trigger, error) {
	obj, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	// A Zendesk trigger posts whatever JSON the operator wrote into the
	// webhook's body template, so the shape is a convention rather than a
	// schema: ticket_id at the top level is the documented placeholder
	// ({{ticket.id}}), and the nested forms are what a template built out
	// of the ticket object produces.
	key := firstText(obj,
		[]string{"ticket_id"},
		[]string{"ticket", "id"},
		[]string{"id"},
	)
	if key == "" {
		return nil, fmt.Errorf("%w: no ticket_id in the Zendesk payload", ErrBadPayload)
	}
	assignee := firstText(obj,
		[]string{"assignee_email"},
		[]string{"assignee"},
		[]string{"ticket", "assignee", "email"},
		[]string{"ticket", "assignee"},
	)
	event := firstText(obj, []string{"event"}, []string{"type"})
	return []Trigger{{Key: key, Event: orDefault(event, "zendesk.trigger"), Assignee: assignee, Raw: body}}, nil
}

// --- Freshdesk --------------------------------------------------------

// Freshdesk verifies a Freshdesk automation's webhook by a shared secret
// header. Freshdesk's "Trigger webhook" action signs nothing; it does let
// the rule add custom headers, which is where the secret goes.
type Freshdesk struct{ Secret string }

func (f Freshdesk) Verify(r *http.Request, _ []byte) error {
	return verifySharedSecret(r, f.Secret)
}

func (f Freshdesk) Extract(body []byte) ([]Trigger, error) {
	obj, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	key := firstText(obj,
		[]string{"ticket", "id"},
		[]string{"freshdesk_webhook", "ticket_id"},
		[]string{"ticket_id"},
		[]string{"id"},
	)
	if key == "" {
		return nil, fmt.Errorf("%w: no ticket.id in the Freshdesk payload", ErrBadPayload)
	}
	assignee := firstText(obj,
		[]string{"ticket", "assignee_email"},
		[]string{"ticket", "responder", "email"},
		[]string{"freshdesk_webhook", "ticket_agent_email"},
		[]string{"assignee_email"},
	)
	event := firstText(obj, []string{"event"}, []string{"ticket", "event"})
	return []Trigger{{Key: key, Event: orDefault(event, "freshdesk.automation"), Assignee: assignee, Raw: body}}, nil
}

// --- Intercom ---------------------------------------------------------

// Intercom verifies X-Hub-Signature, which is "sha1=" followed by the hex
// HMAC-SHA1 of the body under the app's client secret. The scheme is
// Intercom's, inherited from the webhook convention of the same name;
// SHA-1 is what they send, so it is what is checked.
type Intercom struct{ Secret string }

// IntercomSignatureHeader is where Intercom puts the signature.
const IntercomSignatureHeader = "X-Hub-Signature"

func (i Intercom) Verify(r *http.Request, body []byte) error {
	if i.Secret == "" {
		return ErrUnauthorized
	}
	got := strings.TrimSpace(r.Header.Get(IntercomSignatureHeader))
	prefix, hexSig, ok := strings.Cut(got, "=")
	if !ok || !strings.EqualFold(prefix, "sha1") {
		return ErrUnauthorized
	}
	if !equalSig(hexSig, HexHMACSHA1(i.Secret, body), true) {
		return ErrUnauthorized
	}
	return nil
}

// intercomTopics are the notification topics worth a run. Intercom sends
// every topic the app subscribed to down one endpoint, so the receiver
// filters: an assignment is the trigger Sirdar is here for, and a reply or
// a tag change is not.
var intercomTopics = map[string]bool{
	"conversation.admin.assigned": true,
}

func (i Intercom) Extract(body []byte) ([]Trigger, error) {
	obj, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	topic := textAt(obj, "topic")
	if !intercomTopics[topic] {
		return nil, nil
	}
	item := at(obj, "data", "item")
	key := textAt(item, "id")
	if key == "" {
		return nil, fmt.Errorf("%w: no data.item.id in the Intercom payload", ErrBadPayload)
	}
	assignee := firstText(item,
		[]string{"assignee", "email"},
		[]string{"admin_assignee_id"},
		[]string{"assignee", "id"},
	)
	return []Trigger{{Key: key, Event: topic, Assignee: assignee, Raw: body}}, nil
}

// --- HubSpot ----------------------------------------------------------

// HubSpot verifies X-HubSpot-Signature-v3: the base64 of HMAC-SHA256 over
// the request method, the full URI, the raw body and the timestamp header,
// concatenated in that order, under the app's client secret.
//
// The URI is the one HubSpot called, which behind a reverse proxy is not
// the one this process sees. ProxyScheme and ProxyHost, when set, are what
// the signature is computed against; otherwise the request's own
// X-Forwarded-* headers are used, then the connection itself.
type HubSpot struct {
	Secret      string
	ProxyScheme string
	ProxyHost   string
	clock
}

// The two headers a HubSpot v3 delivery carries.
const (
	HubSpotSignatureHeader = "X-HubSpot-Signature-v3"
	HubSpotTimestampHeader = "X-HubSpot-Request-Timestamp"
)

func (h HubSpot) Verify(r *http.Request, body []byte) error {
	if h.Secret == "" {
		return ErrUnauthorized
	}
	ts := r.Header.Get(HubSpotTimestampHeader)
	if !withinWindow(h.now(), parseEpochMillis(ts), ReplayWindow) {
		return ErrUnauthorized
	}
	signed := r.Method + h.uri(r) + string(body) + ts
	if !equalSig(r.Header.Get(HubSpotSignatureHeader), Base64HMACSHA256(h.Secret, []byte(signed)), false) {
		return ErrUnauthorized
	}
	return nil
}

// uri rebuilds the absolute URL HubSpot signed.
func (h HubSpot) uri(r *http.Request) string {
	scheme := h.ProxyScheme
	if scheme == "" {
		scheme = r.Header.Get("X-Forwarded-Proto")
	}
	if scheme == "" {
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	host := h.ProxyHost
	if host == "" {
		host = r.Header.Get("X-Forwarded-Host")
	}
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host + r.URL.RequestURI()
}

// hubspotEvents are the subscription types worth a run.
var hubspotEvents = map[string]bool{
	"ticket.creation":       true,
	"ticket.propertyChange": true,
}

// HubSpot posts a batch: one JSON array holding up to a hundred events,
// which may name several tickets and several properties of the same one.
func (h HubSpot) Extract(body []byte) ([]Trigger, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	events, ok := v.([]any)
	if !ok {
		// A single event is not the documented shape, but reading one is
		// cheaper than refusing a delivery over its brackets.
		if obj, isObj := v.(map[string]any); isObj {
			events = []any{obj}
		} else {
			return nil, fmt.Errorf("%w: the HubSpot body is not a JSON array of events", ErrBadPayload)
		}
	}

	var out []Trigger
	seen := map[string]bool{}
	for _, e := range events {
		event := textAt(e, "subscriptionType")
		if !hubspotEvents[event] {
			continue
		}
		key := firstText(e, []string{"objectId"}, []string{"engagementId"})
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		// A property change carries the one property that changed, so an
		// owner change is the only one that names an assignee.
		assignee := ""
		if textAt(e, "propertyName") == "hubspot_owner_id" {
			assignee = textAt(e, "propertyValue")
		}
		out = append(out, Trigger{Key: key, Event: event, Assignee: assignee, Raw: reencode(e)})
	}
	return out, nil
}

// --- generic ----------------------------------------------------------

// Generic is the endpoint for everything else: a shared secret header and
// a body Sirdar defines, so a tracker with no integration of its own — or
// a shell script — can start a triage with one curl.
type Generic struct{ Secret string }

func (g Generic) Verify(r *http.Request, _ []byte) error {
	return verifySharedSecret(r, g.Secret)
}

func (g Generic) Extract(body []byte) ([]Trigger, error) {
	obj, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	key := textAt(obj, "key")
	if key == "" {
		return nil, fmt.Errorf("%w: no key in the generic payload", ErrBadPayload)
	}
	return []Trigger{{
		Key:      key,
		Event:    orDefault(textAt(obj, "event"), "generic"),
		Assignee: textAt(obj, "assignee"),
		Raw:      body,
	}}, nil
}
