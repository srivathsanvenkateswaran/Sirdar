package webhooks

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func testReceiver(m Match) *Receiver {
	return New(map[string]Verifier{
		SourceGeneric: Generic{Secret: "s3cret"},
		SourceJira:    Jira{Secret: "s3cret"},
	}, m)
}

func TestReceiverAcceptsVerifiedDelivery(t *testing.T) {
	rc := testReceiver(Match{})
	body := fixture(t, "generic.json")
	got, err := rc.Accept(post(body, map[string]string{SecretHeader: "s3cret"}), SourceGeneric)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if len(got) != 1 || got[0].Key != "OMNI-2510" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Source != SourceGeneric {
		t.Errorf("source %q", got[0].Source)
	}
}

func TestReceiverRejectsUnknownSource(t *testing.T) {
	rc := testReceiver(Match{})
	_, err := rc.Accept(post([]byte(`{}`), nil), "gitlab")
	if !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("got %v, want ErrUnknownSource", err)
	}
	if rc.Has("gitlab") {
		t.Error("Has says an unconfigured source is served")
	}
}

func TestReceiverRejectsBadSignature(t *testing.T) {
	rc := testReceiver(Match{})
	_, err := rc.Accept(post(fixture(t, "generic.json"), map[string]string{SecretHeader: "wrong"}), SourceGeneric)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
}

// A key that is not one plain path element would be joined into the run
// tree. Such a trigger is dropped rather than sanitised: a delivery naming
// it is not naming a ticket.
func TestReceiverDropsUnsafeKeys(t *testing.T) {
	rc := testReceiver(Match{})
	body := []byte(`{"key":"../../etc/passwd"}`)
	got, err := rc.Accept(post(body, map[string]string{SecretHeader: "s3cret"}), SourceGeneric)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want the unsafe key dropped", got)
	}
}

func TestValidKey(t *testing.T) {
	for _, k := range []string{"OMNI-2510", "42", "ENG-431", "8891234567"} {
		if !ValidKey(k) {
			t.Errorf("ValidKey(%q) = false", k)
		}
	}
	for _, k := range []string{"", ".", "..", "a/b", `a\b`, "a*b", "a?b", "a[b", "a\nb"} {
		if ValidKey(k) {
			t.Errorf("ValidKey(%q) = true", k)
		}
	}
}

// --- body cap ---------------------------------------------------------

func TestReadBodyRejectsOversizedBody(t *testing.T) {
	huge := strings.Repeat("x", MaxBodyBytes+1)
	r := httptest.NewRequest("POST", "/hooks/ws1/generic", strings.NewReader(huge))
	if _, err := ReadBody(r); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

// A sender that lies about Content-Length must not get past the cap by
// streaming, which is why the read itself is limited too.
func TestReadBodyRejectsOversizedStream(t *testing.T) {
	huge := strings.Repeat("x", MaxBodyBytes+1)
	r := httptest.NewRequest("POST", "/hooks/ws1/generic", strings.NewReader(huge))
	r.ContentLength = -1
	if _, err := ReadBody(r); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestReadBodyAcceptsBodyAtTheCap(t *testing.T) {
	body := strings.Repeat("x", MaxBodyBytes)
	r := httptest.NewRequest("POST", "/hooks/ws1/generic", strings.NewReader(body))
	got, err := ReadBody(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != MaxBodyBytes {
		t.Fatalf("read %d bytes", len(got))
	}
}

// --- match ------------------------------------------------------------

func TestMatchAssignee(t *testing.T) {
	m := Match{Assignee: "sri@acme.com"}
	if ok, why := m.Allows(Trigger{Assignee: "Sri@Acme.com"}); !ok {
		t.Errorf("case-insensitive match rejected: %s", why)
	}
	if ok, _ := m.Allows(Trigger{Assignee: "someone@acme.com"}); ok {
		t.Error("somebody else's ticket was allowed")
	}
	// "Only what is assigned to me" cannot be satisfied by a payload that
	// does not say who it is assigned to.
	if ok, _ := m.Allows(Trigger{}); ok {
		t.Error("a trigger with no assignee was allowed")
	}
	if ok, _ := (Match{}).Allows(Trigger{}); !ok {
		t.Error("an empty filter rejected a trigger")
	}
}

func TestMatchStatusAndLabels(t *testing.T) {
	jira := Trigger{Raw: fixture(t, "jira-issue-updated.json")}
	if ok, why := (Match{Statuses: []string{"open"}}).Allows(jira); !ok {
		t.Errorf("jira status: %s", why)
	}
	if ok, _ := (Match{Statuses: []string{"Closed"}}).Allows(jira); ok {
		t.Error("jira: a status outside the list was allowed")
	}
	if ok, why := (Match{Labels: []string{"payments"}}).Allows(jira); !ok {
		t.Errorf("jira labels: %s", why)
	}
	if ok, _ := (Match{Labels: []string{"security"}}).Allows(jira); ok {
		t.Error("jira: a label outside the list was allowed")
	}
}

// Each source keeps its status and labels somewhere else; the filter finds
// them without being told which source it is reading.
func TestMatchAcrossSources(t *testing.T) {
	for _, tc := range []struct {
		file   string
		status string
		label  string
	}{
		{"azdo-workitem-updated.json", "Active", "payments"},
		{"rally-defect-updated.json", "Defined", "support"},
		{"zendesk-trigger.json", "open", "payments"},
		{"freshdesk-automation.json", "Open", "support"},
		{"intercom-conversation-assigned.json", "open", "support"},
		{"linear-issue-update.json", "", "support"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			trigger := Trigger{Raw: fixture(t, tc.file)}
			if tc.status != "" {
				if ok, why := (Match{Statuses: []string{tc.status}}).Allows(trigger); !ok {
					t.Errorf("status %q: %s", tc.status, why)
				}
				if ok, _ := (Match{Statuses: []string{"nothing-like-it"}}).Allows(trigger); ok {
					t.Error("a status outside the list was allowed")
				}
			}
			if ok, why := (Match{Labels: []string{tc.label}}).Allows(trigger); !ok {
				t.Errorf("label %q: %s", tc.label, why)
			}
		})
	}
}

// Azure DevOps keeps tags in one semicolon-separated string.
func TestMatchSplitsAzureDevOpsTags(t *testing.T) {
	trigger := Trigger{Raw: fixture(t, "azdo-workitem-updated.json")}
	if ok, why := (Match{Labels: []string{"support"}}).Allows(trigger); !ok {
		t.Errorf("first tag of a semicolon-separated list: %s", why)
	}
}

// A payload that carries no status at all passes the status filter rather
// than being dropped: several of these sources can be configured to send a
// body with nothing but an id in it, and dropping those would make the
// filter mean "ignore everything".
func TestMatchPassesWhenTheFieldIsAbsent(t *testing.T) {
	trigger := Trigger{Raw: []byte(`{"key":"OMNI-1"}`)}
	if ok, why := (Match{Statuses: []string{"Open"}, Labels: []string{"support"}}).Allows(trigger); !ok {
		t.Errorf("a payload with neither field was dropped: %s", why)
	}
}

func TestMatchIgnoresUnparsableRaw(t *testing.T) {
	if ok, _ := (Match{Statuses: []string{"Open"}}).Allows(Trigger{Raw: []byte("{")}); !ok {
		t.Error("an unparsable raw body should leave the status filter unapplied")
	}
}

// --- Build ------------------------------------------------------------

func TestBuildKnowsEverySource(t *testing.T) {
	for _, name := range KnownSources() {
		v, err := Build(name, Auth{Secret: "s", Username: "u", Password: "p"})
		if err != nil {
			t.Fatalf("build %s: %v", name, err)
		}
		if v == nil {
			t.Fatalf("build %s: nil verifier", name)
		}
		if !Known(name) {
			t.Errorf("Known(%q) = false", name)
		}
	}
	if _, err := Build("gitlab", Auth{}); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("got %v, want ErrUnknownSource", err)
	}
}

func TestOnlyAzDOUsesBasicAuth(t *testing.T) {
	for _, name := range KnownSources() {
		if got, want := UsesBasicAuth(name), name == SourceAzDO; got != want {
			t.Errorf("UsesBasicAuth(%q) = %v", name, got)
		}
	}
}

// New drops a nil verifier rather than serving a source that would panic
// on the first delivery.
func TestNewIgnoresNilVerifiers(t *testing.T) {
	rc := New(map[string]Verifier{SourceJira: nil}, Match{})
	if rc.Has(SourceJira) {
		t.Fatal("a nil verifier was registered")
	}
}
