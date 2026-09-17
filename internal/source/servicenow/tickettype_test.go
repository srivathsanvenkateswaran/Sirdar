package servicenow

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestTrackerGetCarriesTheRecordsClassAsType(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(mustRead(t, "incident.json"))
	})
	tr, err := c.Tracker().Get(context.Background(), testSysID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// The fixture names no sys_class_name, so the configured table is what
	// kind of record this is.
	if tr.Type != "incident" {
		t.Errorf("Type = %q, want incident", tr.Type)
	}
}

func TestRecordTypePrefersTheRowsOwnClass(t *testing.T) {
	c, _ := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
	if got := c.recordType(record{"sys_class_name": json.RawMessage(`"Service Request"`)}); got != "service request" {
		t.Errorf("recordType = %q, want service request", got)
	}
	if got := c.recordType(record{}); got != "incident" {
		t.Errorf("recordType with no class = %q, want the configured table", got)
	}
}
