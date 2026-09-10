package prompt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// --- LoadPlaybooks ---

func TestLoadPlaybooksSortsByFilenameAndSkipsNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "20-logs.md"), "logs body")
	write(t, filepath.Join(dir, "10-helpdesk.md"), "helpdesk body")
	write(t, filepath.Join(dir, "notes.txt"), "ignore me")

	got, err := LoadPlaybooks(dir)
	if err != nil {
		t.Fatalf("LoadPlaybooks: %v", err)
	}
	want := []Playbook{
		{Name: "10-helpdesk", Body: "helpdesk body"},
		{Name: "20-logs", Body: "logs body"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d playbooks, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("playbook %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestLoadPlaybooksMissingDirReturnsEmptyNil(t *testing.T) {
	got, err := LoadPlaybooks(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("LoadPlaybooks: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d playbooks, want 0: %+v", len(got), got)
	}
}

// --- ScaffoldPlaybooks ---

func TestScaffoldPlaybooksWritesFive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "playbooks")
	if err := ScaffoldPlaybooks(dir); err != nil {
		t.Fatalf("ScaffoldPlaybooks: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"10-helpdesk.md", "20-logs.md", "30-apm.md", "40-database.md", "50-code.md"}
	if len(names) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(names), len(want), names)
	}
	for _, w := range want {
		found := false
		for _, n := range names {
			if n == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing scaffolded file %s, got %v", w, names)
		}
	}
}

func TestScaffoldPlaybooksNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "10-helpdesk.md")
	write(t, custom, "workspace-specific content, do not touch")

	if err := ScaffoldPlaybooks(dir); err != nil {
		t.Fatalf("ScaffoldPlaybooks: %v", err)
	}

	got, err := os.ReadFile(custom)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "workspace-specific content, do not touch" {
		t.Fatalf("ScaffoldPlaybooks overwrote existing file: got %q", got)
	}

	// The other four should still have been written.
	for _, name := range []string{"20-logs.md", "30-apm.md", "40-database.md", "50-code.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to be scaffolded: %v", name, err)
		}
	}
}

// --- Schemas ---

func TestSchemasAreValidJSON(t *testing.T) {
	if !json.Valid(TriageSchema) {
		t.Fatalf("TriageSchema is not valid JSON")
	}
	if !json.Valid(RCASchema) {
		t.Fatalf("RCASchema is not valid JSON")
	}

	var rcaDoc map[string]any
	if err := json.Unmarshal(RCASchema, &rcaDoc); err != nil {
		t.Fatalf("unmarshal RCASchema: %v", err)
	}
	props, ok := rcaDoc["properties"].(map[string]any)
	if !ok {
		t.Fatalf("RCASchema has no top-level \"properties\": %v", rcaDoc)
	}
	if _, ok := props["rca"]; !ok {
		t.Fatalf("RCASchema properties missing \"rca\": %v", props)
	}
	if _, ok := props["resolution"]; !ok {
		t.Fatalf("RCASchema properties missing \"resolution\": %v", props)
	}
}

// --- Triage / RCA golden tests ---

func fixedBundle() ticket.Bundle {
	created := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	return ticket.Bundle{
		Tracker: &ticket.TrackerTicket{
			Key:         "OMNI-2510",
			Title:       "Refund stuck in pending",
			Description: "Customer refund has been pending for 3 days.",
			Priority:    "P2",
			Status:      "Open",
			Assignee:    "srivathsan",
			URL:         "https://tracker.example.com/browse/OMNI-2510",
			HelpdeskRef: "ZD-88213",
			CreatedAt:   created,
			UpdatedAt:   created,
		},
		Helpdesk: &ticket.HelpdeskTicket{
			ID:         "ZD-88213",
			Subject:    "Refund not received",
			Status:     "Open",
			Priority:   "High",
			Channel:    "email",
			Contact:    "jane@example.com",
			Customer:   "Acme Corp",
			CustomerID: "CUST-77",
			URL:        "https://desk.example.com/tickets/88213",
			CreatedAt:  created,
			UpdatedAt:  created,
		},
		Thread: ticket.Thread{
			{At: created, Author: "Jane", Role: ticket.RoleCustomer, Text: "My refund has not arrived."},
			{At: created.Add(time.Hour), Author: "L1 Agent", Role: ticket.RoleAgent, Text: "We are looking into it."},
		},
		Attachments: []ticket.Attachment{
			{ID: "att-1", Name: "screenshot.png", MIME: "image/png", Path: "attachments/att-1-screenshot.png"},
		},
		Warnings: []string{"attachment att-2 failed to download: 404 Not Found"},
	}
}

func fixedPlaybooks() []Playbook {
	return []Playbook{
		{Name: "10-helpdesk", Body: "Read the whole thread first.\nScreenshots are evidence."},
		{Name: "20-logs", Body: "Check the query syntax before trusting zero rows."},
	}
}

const fixedThreadHead = `[2026-09-01T09:00:00Z] Jane (customer): My refund has not arrived.
[2026-09-01T10:00:00Z] L1 Agent (agent): We are looking into it.`

func fixedTriageInput() TriageInput {
	return TriageInput{
		Bundle:     fixedBundle(),
		BundleDir:  "/bundles/OMNI-2510",
		Playbooks:  fixedPlaybooks(),
		ThreadHead: fixedThreadHead,
	}
}

func TestTriageGolden(t *testing.T) {
	got := Triage(fixedTriageInput())
	compareGolden(t, filepath.Join("testdata", "triage.golden.md"), got)

	// Sanity checks independent of the exact golden formatting, so a
	// regression is caught even before eyeballing the golden diff.
	mustContain(t, got, strings.TrimRight(preambleMD, "\n"))
	mustContain(t, got, "## 10-helpdesk")
	mustContain(t, got, "## 20-logs")
	mustContain(t, got, "Key: OMNI-2510")
	mustContain(t, got, "Bundle directory: /bundles/OMNI-2510")
	mustContain(t, got, fixedThreadHead)
	mustContain(t, got, string(TriageSchema))
	mustContain(t, got, "Respond with the JSON object only.")
}

func TestRCAGolden(t *testing.T) {
	in := RCAInput{
		TriageInput: fixedTriageInput(),
		TriageNote:  "# Triage: OMNI-2510\n\nHypothesis: the refund worker silently dropped retryable jobs.",
		Resolution:  "Redeployed the refund worker with the retry fix and reprocessed the stuck queue.",
		PRTitle:     "Fix refund worker dropping retryable jobs",
		PRBody:      "Retries were discarded when the queue backend returned a transient error.",
		PRDiff:      "--- a/worker/refund.go\n+++ b/worker/refund.go\n@@\n-return nil\n+return err\n",
		PRURL:       "https://github.com/example/sirdar/pull/42",
	}

	got := RCA(in)
	compareGolden(t, filepath.Join("testdata", "rca.golden.md"), got)

	mustContain(t, got, strings.TrimRight(preambleMD, "\n"))
	mustContain(t, got, "## 10-helpdesk")
	mustContain(t, got, "## 20-logs")
	mustContain(t, got, "Key: OMNI-2510")
	mustContain(t, got, fixedThreadHead)
	mustContain(t, got, "# Triage note")
	mustContain(t, got, in.TriageNote)
	mustContain(t, got, "# Resolution as reported by the engineer")
	mustContain(t, got, in.Resolution)
	mustContain(t, got, "# Merged pull request")
	mustContain(t, got, in.PRTitle)
	mustContain(t, got, in.PRDiff)
	mustContain(t, got, string(RCASchema))
	mustContain(t, got, "Audit rule:")
	mustContain(t, got, "Respond with the JSON object only.")
}

func TestRCAOmitsPullRequestSectionWhenEmpty(t *testing.T) {
	in := RCAInput{
		TriageInput: fixedTriageInput(),
		TriageNote:  "note",
		Resolution:  "resolution text",
	}
	got := RCA(in)
	if strings.Contains(got, "# Merged pull request") {
		t.Fatalf("expected no pull request section when PR fields are all empty, got:\n%s", got)
	}
}

func TestTriageOmitsWarningsSectionWhenEmpty(t *testing.T) {
	in := fixedTriageInput()
	in.Bundle.Warnings = nil
	got := Triage(in)
	if strings.Contains(got, "## Warnings") {
		t.Fatalf("expected no warnings section when there are no warnings, got:\n%s", got)
	}
}

// --- helpers ---

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustContain(t *testing.T, got, substr string) {
	t.Helper()
	if !strings.Contains(got, substr) {
		t.Fatalf("expected output to contain %q, got:\n%s", substr, got)
	}
}

func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with UPDATE_GOLDEN=1 to create it)", path, err)
	}
	if got != string(want) {
		t.Fatalf("output does not match golden %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}
