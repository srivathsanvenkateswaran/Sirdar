package webhooks

import (
	"errors"
	"testing"
)

// extractOne runs a verifier's extractor over a fixture and insists on
// exactly one trigger.
func extractOne(t *testing.T, v Verifier, name string) Trigger {
	t.Helper()
	got, err := v.Extract(fixture(t, name))
	if err != nil {
		t.Fatalf("extract %s: %v", name, err)
	}
	if len(got) != 1 {
		t.Fatalf("extract %s: got %d triggers, want 1", name, len(got))
	}
	return got[0]
}

func assertTrigger(t *testing.T, got Trigger, key, event, assignee string) {
	t.Helper()
	if got.Key != key {
		t.Errorf("key %q, want %q", got.Key, key)
	}
	if got.Event != event {
		t.Errorf("event %q, want %q", got.Event, event)
	}
	if got.Assignee != assignee {
		t.Errorf("assignee %q, want %q", got.Assignee, assignee)
	}
	if len(got.Raw) == 0 {
		t.Error("raw is empty")
	}
}

func TestExtractFixtures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		v        Verifier
		file     string
		key      string
		event    string
		assignee string
	}{
		{"jira", Jira{}, "jira-issue-updated.json", "OMNI-2510", "jira:issue_updated", "sri@acme.com"},
		{"jira automation", Jira{}, "jira-automation.json", "OMNI-2511", "jira.webhook", "sri@acme.com"},
		{"linear", Linear{}, "linear-issue-update.json", "ENG-431", "Issue.update", "sri@acme.com"},
		{"azdo", AzDO{}, "azdo-workitem-updated.json", "42", "workitem.updated", "sri@acme.com"},
		{"rally", Rally{}, "rally-defect-updated.json", "DE1234", "Updated", "sri@acme.com"},
		{"zendesk", Zendesk{}, "zendesk-trigger.json", "77213", "zendesk.trigger", "sri@acme.com"},
		{"freshdesk", Freshdesk{}, "freshdesk-automation.json", "30142", "ticket_assigned", "sri@acme.com"},
		{"intercom", Intercom{}, "intercom-conversation-assigned.json", "7712", "conversation.admin.assigned", "sri@acme.com"},
		{"generic", Generic{}, "generic.json", "OMNI-2510", "assigned", "sri@acme.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertTrigger(t, extractOne(t, tc.v, tc.file), tc.key, tc.event, tc.assignee)
		})
	}
}

// Azure DevOps sends the work item id as resource.workItemId and the id of
// the update itself as resource.id. Reading them the other way round would
// triage revision 210 rather than bug 42.
func TestAzDOPrefersWorkItemIDOverRevisionID(t *testing.T) {
	got := extractOne(t, AzDO{}, "azdo-workitem-updated.json")
	if got.Key == "210" {
		t.Fatal("extracted the update id instead of the work item id")
	}
}

// HubSpot batches events of every subscribed type down one endpoint, and
// several of them may name the same ticket.
func TestHubSpotExtractsOnlyTicketEvents(t *testing.T) {
	got, err := HubSpot{}.Extract(fixture(t, "hubspot-ticket-events.json"))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d triggers, want 1 (the contact event is not a ticket)", len(got))
	}
	assertTrigger(t, got[0], "8891234567", "ticket.propertyChange", "77341122")
}

func TestHubSpotDeduplicatesOneBatch(t *testing.T) {
	body := []byte(`[
		{"subscriptionType":"ticket.propertyChange","objectId":991,"propertyName":"hubspot_owner_id","propertyValue":"7"},
		{"subscriptionType":"ticket.propertyChange","objectId":991,"propertyName":"hs_pipeline_stage","propertyValue":"2"},
		{"subscriptionType":"ticket.creation","objectId":992}
	]`)
	got, err := HubSpot{}.Extract(body)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d triggers, want 2", len(got))
	}
	if got[0].Key != "991" || got[1].Key != "992" {
		t.Fatalf("keys %q, %q", got[0].Key, got[1].Key)
	}
}

// A large HubSpot object id has to survive the decode: read as a float64
// it would round to a different ticket.
func TestHubSpotKeepsLargeObjectIDsExact(t *testing.T) {
	got, err := HubSpot{}.Extract([]byte(`[{"subscriptionType":"ticket.creation","objectId":9007199254740993}]`))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 1 || got[0].Key != "9007199254740993" {
		t.Fatalf("got %+v", got)
	}
}

// An update that touched neither the assignee nor the labels is somebody
// editing a title, and is not a reason to start an agent session.
func TestLinearIgnoresUnrelatedUpdate(t *testing.T) {
	got, err := Linear{}.Extract(fixture(t, "linear-issue-comment-update.json"))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d triggers, want none", len(got))
	}
}

func TestLinearIgnoresNonIssueEntities(t *testing.T) {
	body := []byte(`{"action":"create","type":"Comment","data":{"identifier":"ENG-9"}}`)
	got, err := Linear{}.Extract(body)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d triggers, err %v", len(got), err)
	}
}

func TestLinearCreateNeedsNoChangeSet(t *testing.T) {
	body := []byte(`{"action":"create","type":"Issue","data":{"identifier":"ENG-9","assignee":{"email":"sri@acme.com"}}}`)
	got, err := Linear{}.Extract(body)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	assertTrigger(t, got[0], "ENG-9", "Issue.create", "sri@acme.com")
}

// Intercom sends every subscribed topic to the same endpoint.
func TestIntercomIgnoresOtherTopics(t *testing.T) {
	body := []byte(`{"topic":"conversation.user.replied","data":{"item":{"id":"7712"}}}`)
	got, err := Intercom{}.Extract(body)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d triggers, err %v", len(got), err)
	}
}

func TestJiraIgnoresEventsItDoesNotActOn(t *testing.T) {
	body := []byte(`{"webhookEvent":"jira:issue_deleted","issue":{"key":"OMNI-1"}}`)
	got, err := Jira{}.Extract(body)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d triggers, err %v", len(got), err)
	}
}

func TestAzDOIgnoresEventsItDoesNotActOn(t *testing.T) {
	body := []byte(`{"eventType":"workitem.deleted","resource":{"workItemId":42}}`)
	got, err := AzDO{}.Extract(body)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d triggers, err %v", len(got), err)
	}
}

// A verified body that names no ticket is the sender's mistake, and the
// receiver says so rather than accepting a delivery it did nothing with.
func TestExtractRejectsPayloadWithNoKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    Verifier
		body string
	}{
		{"jira", Jira{}, `{"webhookEvent":"jira:issue_updated","issue":{"fields":{}}}`},
		{"linear", Linear{}, `{"action":"create","type":"Issue","data":{"title":"x"}}`},
		{"azdo", AzDO{}, `{"eventType":"workitem.updated","resource":{}}`},
		{"rally", Rally{}, `{"message":{"state":{}}}`},
		{"zendesk", Zendesk{}, `{"subject":"x"}`},
		{"freshdesk", Freshdesk{}, `{"ticket":{"subject":"x"}}`},
		{"intercom", Intercom{}, `{"topic":"conversation.admin.assigned","data":{"item":{}}}`},
		{"generic", Generic{}, `{"assignee":"sri@acme.com"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.v.Extract([]byte(tc.body))
			if !errors.Is(err, ErrBadPayload) {
				t.Fatalf("got %v, want ErrBadPayload", err)
			}
		})
	}
}

func TestExtractRejectsUnparsableBody(t *testing.T) {
	for _, v := range []Verifier{Jira{}, Linear{}, AzDO{}, Rally{}, Zendesk{}, Freshdesk{}, Intercom{}, HubSpot{}, Generic{}} {
		if _, err := v.Extract([]byte(`{"key":`)); !errors.Is(err, ErrBadPayload) {
			t.Fatalf("%T: got %v, want ErrBadPayload", v, err)
		}
	}
}

func TestAzDOReadsAssigneeFromTheFieldDiff(t *testing.T) {
	body := []byte(`{"eventType":"workitem.updated","resource":{"workItemId":7,
		"fields":{"System.AssignedTo":{"oldValue":"","newValue":"Sri V <sri@acme.com>"}}}}`)
	got, err := AzDO{}.Extract(body)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	assertTrigger(t, got[0], "7", "workitem.updated", "sri@acme.com")
}

func TestAzDOReadsIdentityObjects(t *testing.T) {
	body := []byte(`{"eventType":"workitem.created","resource":{"id":7,
		"fields":{"System.AssignedTo":{"displayName":"Sri V","uniqueName":"sri@acme.com"}}}}`)
	got, err := AzDO{}.Extract(body)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	assertTrigger(t, got[0], "7", "workitem.created", "sri@acme.com")
}
