package webhooks

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// --- Jira -------------------------------------------------------------

// Jira verifies a Jira delivery by a shared secret header.
//
// Jira Cloud's own webhooks are unsigned: the platform offers no HMAC and
// no token of its own, so the documented way to authenticate one is a
// secret in the URL or a header the receiver checks. Jira Automation's
// "send web request" action does have a token header
// (X-Automation-Webhook-Token), but it is configured per rule and is a
// shared secret by another name, so both paths land here: put the secret
// in SecretHeader and give the rule a custom header with the same value.
type Jira struct{ Secret string }

func (j Jira) Verify(r *http.Request, _ []byte) error {
	return verifySharedSecret(r, j.Secret)
}

// jiraIssueEvents are the webhookEvent values worth a run. An assignment
// is not an event of its own in Jira: it arrives as jira:issue_updated
// with an assignee entry in the changelog, so the event filter has to be
// this broad and webhooks.match.assignee does the narrowing.
var jiraIssueEvents = map[string]bool{
	"jira:issue_created": true,
	"jira:issue_updated": true,
}

func (j Jira) Extract(body []byte) ([]Trigger, error) {
	obj, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	event := firstText(obj, []string{"webhookEvent"}, []string{"issue_event_type_name"})
	// An Automation rule sends whatever body the operator wrote, which
	// commonly has no webhookEvent at all. A body that names an event
	// Sirdar does not act on is dropped; one that names none is taken at
	// face value, since the rule itself is the filter.
	if event != "" && !jiraIssueEvents[event] {
		return nil, nil
	}
	key := firstText(obj, []string{"issue", "key"}, []string{"key"}, []string{"issueKey"})
	if key == "" {
		return nil, fmt.Errorf("%w: no issue.key in the Jira payload", ErrBadPayload)
	}
	fields := at(obj, "issue", "fields")
	assignee := firstText(fields,
		[]string{"assignee", "emailAddress"},
		[]string{"assignee", "accountId"},
		[]string{"assignee", "name"},
		[]string{"assignee", "displayName"},
	)
	if assignee == "" {
		// The Automation shape again: a flat body the rule filled in.
		assignee = firstText(obj, []string{"assignee"}, []string{"assigneeEmail"})
	}
	return []Trigger{{Key: key, Event: orDefault(event, "jira.webhook"), Assignee: assignee, Raw: body}}, nil
}

// --- Linear -----------------------------------------------------------

// Linear verifies the Linear-Signature header: a hex-encoded HMAC-SHA256
// of the raw request body under the webhook's signing secret. The raw
// bytes matter — re-encoding the JSON changes key order and whitespace and
// the signature no longer matches.
type Linear struct {
	Secret string
	clock
}

// LinearSignatureHeader is where Linear puts the signature.
const LinearSignatureHeader = "Linear-Signature"

// linearReplayWindow is how far webhookTimestamp may be from now. Linear
// recommends 60 seconds; a delivery whose timestamp is missing is not
// rejected on that ground, since only the data-change payloads carry one.
const linearReplayWindow = 60 * time.Second

func (l Linear) Verify(r *http.Request, body []byte) error {
	if l.Secret == "" {
		return ErrUnauthorized
	}
	if !equalSig(r.Header.Get(LinearSignatureHeader), HexHMACSHA256(l.Secret, body), true) {
		return ErrUnauthorized
	}
	// The signature covers the body, so a body Linear signed last week is
	// still a valid signature; the timestamp inside it is what makes a
	// replay detectable.
	if ms := textAt(jsonObject(body), "webhookTimestamp"); ms != "" {
		if !withinWindow(l.now(), parseEpochMillis(ms), linearReplayWindow) {
			return ErrUnauthorized
		}
	}
	return nil
}

func (l Linear) Extract(body []byte) ([]Trigger, error) {
	obj, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	if textAt(obj, "type") != "Issue" {
		return nil, nil
	}
	action := textAt(obj, "action")
	if action != "create" && action != "update" {
		return nil, nil
	}
	// On an update, updatedFrom holds the previous values of the fields
	// that changed. An update that touched neither the assignee nor the
	// labels is somebody editing a description, which is not a reason to
	// triage anything.
	if action == "update" {
		from, _ := at(obj, "updatedFrom").(map[string]any)
		_, assigneeChanged := from["assigneeId"]
		_, labelsChanged := from["labelIds"]
		if !assigneeChanged && !labelsChanged {
			return nil, nil
		}
	}
	key := textAt(obj, "data", "identifier")
	if key == "" {
		return nil, fmt.Errorf("%w: no data.identifier in the Linear payload", ErrBadPayload)
	}
	data := at(obj, "data")
	assignee := firstText(data,
		[]string{"assignee", "email"},
		[]string{"assignee", "displayName"},
		[]string{"assignee", "name"},
		[]string{"assigneeId"},
	)
	return []Trigger{{Key: key, Event: "Issue." + action, Assignee: assignee, Raw: body}}, nil
}

// --- Azure DevOps -----------------------------------------------------

// AzDO verifies the basic-auth credentials configured on an Azure DevOps
// service hook subscription. The service hooks consumer offers a username
// and password and no signature, so this is the whole of the proof.
type AzDO struct{ Username, Password string }

func (a AzDO) Verify(r *http.Request, _ []byte) error {
	return verifyBasic(r, a.Username, a.Password)
}

var azdoEvents = map[string]bool{
	"workitem.created": true,
	"workitem.updated": true,
}

func (a AzDO) Extract(body []byte) ([]Trigger, error) {
	obj, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	event := textAt(obj, "eventType")
	if event != "" && !azdoEvents[event] {
		return nil, nil
	}
	// workitem.updated names the work item as resource.workItemId and
	// carries its own revision id as resource.id; workitem.created has
	// the work item itself at resource.id. Reading workItemId first is
	// what keeps an update from triaging a revision number.
	key := firstText(obj,
		[]string{"resource", "workItemId"},
		[]string{"resource", "id"},
		[]string{"resource", "revision", "id"},
	)
	if key == "" {
		return nil, fmt.Errorf("%w: no resource.workItemId in the Azure DevOps payload", ErrBadPayload)
	}
	assignee := azdoAssignee(obj)
	return []Trigger{{Key: key, Event: orDefault(event, "workitem.updated"), Assignee: assignee, Raw: body}}, nil
}

// azdoAssignee reads System.AssignedTo out of whichever of the three
// places the subscription's "resource details to send" setting put it. The
// value is a display string like "Sri V <sri@acme.com>" on the classic
// payload and an identity object on the newer one; both reduce to the
// address, which is what a match filter can be written against.
func azdoAssignee(obj map[string]any) string {
	for _, fields := range []any{
		at(obj, "resource", "revision", "fields"),
		at(obj, "resource", "fields"),
	} {
		v := at(fields, "System.AssignedTo")
		if v == nil {
			continue
		}
		// An updated event sends {"oldValue":…,"newValue":…} per field.
		if s := firstText(v,
			[]string{"newValue"},
			[]string{"uniqueName"},
			[]string{"mailAddress"},
			[]string{"displayName"},
		); s != "" {
			return emailOf(s)
		}
		if s := text(v); s != "" {
			return emailOf(s)
		}
	}
	return ""
}

// emailOf pulls the address out of "Display Name <addr@example.com>",
// which is how Azure DevOps renders an identity in a plain field value.
func emailOf(s string) string {
	open := strings.LastIndex(s, "<")
	closing := strings.LastIndex(s, ">")
	if open >= 0 && closing > open {
		if inner := strings.TrimSpace(s[open+1 : closing]); inner != "" {
			return inner
		}
	}
	return strings.TrimSpace(s)
}

// --- Rally ------------------------------------------------------------

// Rally verifies a Rally webhook by a shared secret header. Rally's
// webhooks API authenticates the calls Sirdar would make to it, not the
// deliveries it sends: the delivery carries no signature, so the receiver
// needs a secret of its own.
type Rally struct{ Secret string }

func (ra Rally) Verify(r *http.Request, _ []byte) error {
	return verifySharedSecret(r, ra.Secret)
}

func (ra Rally) Extract(body []byte) ([]Trigger, error) {
	obj, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	// message.state is the post-change snapshot of the object;
	// message.changes holds the attributes that changed, each as an
	// object with the old and new value. A FormattedID does not change,
	// so it is normally in state, and changes is the fallback for a
	// delivery whose format sends only the diff.
	key := firstText(obj,
		[]string{"message", "state", "FormattedID"},
		[]string{"message", "changes", "FormattedID", "new"},
		[]string{"message", "changes", "FormattedID", "value"},
		[]string{"state", "FormattedID"},
		[]string{"FormattedID"},
	)
	if key == "" {
		return nil, fmt.Errorf("%w: no FormattedID in the Rally payload", ErrBadPayload)
	}
	assignee := firstText(obj,
		[]string{"message", "state", "Owner", "EmailAddress"},
		[]string{"message", "state", "Owner", "_refObjectName"},
		[]string{"message", "changes", "Owner", "new", "EmailAddress"},
		[]string{"message", "changes", "Owner", "new", "_refObjectName"},
	)
	event := firstText(obj,
		[]string{"message", "action"},
		[]string{"message", "transaction", "action"},
		[]string{"action"},
	)
	return []Trigger{{Key: key, Event: orDefault(event, "rally.webhook"), Assignee: assignee, Raw: body}}, nil
}

// --- shared -----------------------------------------------------------

// jsonObject decodes a body and returns the object, or nil. It is for the
// places that want one optional field out of an already-verified body and
// have nothing to say about a body that will not parse.
func jsonObject(body []byte) map[string]any {
	obj, err := decodeObject(body)
	if err != nil {
		return nil
	}
	return obj
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
