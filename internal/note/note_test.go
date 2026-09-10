package note

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return data
}

// --- Validate ---

func TestValidateTriageAcceptsValidDoc(t *testing.T) {
	doc := readTestdata(t, "triage.json")
	if err := Validate(Triage, doc); err != nil {
		t.Fatalf("Validate(Triage): %v", err)
	}
}

func TestValidateTriageRejectsMissingConfidence(t *testing.T) {
	doc := readTestdata(t, "triage_missing_confidence.json")
	err := Validate(Triage, doc)
	if err == nil {
		t.Fatal("Validate(Triage): got nil error, want a schema violation")
	}
	if !strings.HasPrefix(err.Error(), "note does not match schema:") {
		t.Fatalf("error does not carry the expected prefix: %v", err)
	}
	if !strings.Contains(err.Error(), "rootCause") || !strings.Contains(err.Error(), "confidence") {
		t.Fatalf("error does not name the offending path: %v", err)
	}
}

func TestValidateRCAAcceptsValidCombinedDoc(t *testing.T) {
	doc := readTestdata(t, "rca.json")
	if err := Validate(RCA, doc); err != nil {
		t.Fatalf("Validate(RCA): %v", err)
	}
	if err := Validate(Resolution, doc); err != nil {
		t.Fatalf("Validate(Resolution): %v", err)
	}
}

func TestValidateRCARejectsBadVerdict(t *testing.T) {
	doc := readTestdata(t, "rca_bad_verdict.json")
	err := Validate(RCA, doc)
	if err == nil {
		t.Fatal("Validate(RCA): got nil error, want a schema violation")
	}
	if !strings.Contains(err.Error(), "triageReview.verdict") {
		t.Fatalf("error does not name the offending path: %v", err)
	}
}

func TestValidateUnknownKind(t *testing.T) {
	if err := Validate(Kind("bogus"), []byte(`{}`)); err == nil {
		t.Fatal("Validate(bogus): got nil error, want one for an unknown kind")
	}
}

// --- Render (golden) ---

func triageMeta() Meta {
	return Meta{
		Key: "OMNI-1", TrackerURL: "https://tracker.example/OMNI-1",
		HelpdeskID: "12345", HelpdeskURL: "https://helpdesk.example/12345",
		Customer: "Example Corp", CustomerID: "cust-1",
		Date: "2026-09-10", DateReported: "2026-09-01",
		Priority: "high", Service: "omni",
		RunID: "run-1", Provider: "claude-code",
	}
}

func TestRenderTriageMatchesGolden(t *testing.T) {
	doc := readTestdata(t, "triage.json")
	got, err := (Renderer{}).Render(Triage, doc, triageMeta())
	if err != nil {
		t.Fatalf("Render(Triage): %v", err)
	}
	want := readTestdata(t, "triage.golden.md")
	if got != string(want) {
		t.Fatalf("Render(Triage) mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderRCAMatchesGolden(t *testing.T) {
	doc := readTestdata(t, "rca.json")
	m := triageMeta()
	m.Links.Triage = "OMNI-1 export-fails"
	m.Links.Resolution = "OMNI-1 export-fails-resolution"
	got, err := (Renderer{}).Render(RCA, doc, m)
	if err != nil {
		t.Fatalf("Render(RCA): %v", err)
	}
	want := readTestdata(t, "rca.golden.md")
	if got != string(want) {
		t.Fatalf("Render(RCA) mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderResolutionMatchesGolden(t *testing.T) {
	doc := readTestdata(t, "rca.json")
	m := triageMeta()
	m.Links.Triage = "OMNI-1 export-fails"
	m.Links.RCA = "OMNI-1 export-fails-rca"
	got, err := (Renderer{}).Render(Resolution, doc, m)
	if err != nil {
		t.Fatalf("Render(Resolution): %v", err)
	}
	want := readTestdata(t, "resolution.golden.md")
	if got != string(want) {
		t.Fatalf("Render(Resolution) mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// --- Render (custom template override) ---

func TestRenderUsesCustomTemplateWhenPresent(t *testing.T) {
	dir := t.TempDir()
	custom := "CUSTOM TEMPLATE: {{.doc.title}} / {{.meta.Key}}\n"
	if err := os.WriteFile(filepath.Join(dir, "triage.md.tmpl"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	doc := readTestdata(t, "triage.json")
	r := Renderer{TemplatesDir: dir}
	got, err := r.Render(Triage, doc, triageMeta())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "CUSTOM TEMPLATE: Sample issue for template checks / OMNI-1\n"
	if got != want {
		t.Fatalf("Render with override = %q, want %q", got, want)
	}
}

func TestRenderFallsBackToEmbeddedForOtherKindsWhenOverridingOne(t *testing.T) {
	dir := t.TempDir()
	custom := "CUSTOM TEMPLATE: {{.doc.title}}\n"
	if err := os.WriteFile(filepath.Join(dir, "triage.md.tmpl"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	doc := readTestdata(t, "rca.json")
	m := triageMeta()
	m.Links.Triage = "OMNI-1 export-fails"
	m.Links.Resolution = "OMNI-1 export-fails-resolution"
	r := Renderer{TemplatesDir: dir}
	got, err := r.Render(RCA, doc, m)
	if err != nil {
		t.Fatalf("Render(RCA): %v", err)
	}
	want := readTestdata(t, "rca.golden.md")
	if got != string(want) {
		t.Fatalf("Render(RCA) with an unrelated override in TemplatesDir should still match the embedded golden")
	}
}

// --- Check ---

func TestCheckPassesForEmbeddedTemplates(t *testing.T) {
	if err := (Renderer{}).Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckFailsOnTemplateSyntaxError(t *testing.T) {
	dir := t.TempDir()
	broken := "{{.doc.title"
	if err := os.WriteFile(filepath.Join(dir, "triage.md.tmpl"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	r := Renderer{TemplatesDir: dir}
	if err := r.Check(); err == nil {
		t.Fatal("Check: got nil error, want a template parse failure")
	}
}

// --- Filename / Slug ---

func TestFilename(t *testing.T) {
	got := Filename("{key} {slug}.md", "OMNI-1", "export-fails")
	want := "OMNI-1 export-fails.md"
	if got != want {
		t.Fatalf("Filename() = %q, want %q", got, want)
	}
}

func TestSlug(t *testing.T) {
	cases := []struct{ title, want string }{
		{"Export Fails!! For Large Orders", "export-fails-for-large-orders"},
		{"  Multiple---Dashes__and   spaces ", "multiple-dashes-and-spaces"},
		{"", ""},
		{strings.Repeat("a", 70), strings.Repeat("a", 60)},
	}
	for _, c := range cases {
		got := Slug(c.title)
		if got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

// --- UpdateTriageStatus ---

const triageNoteFixture = `---
status: triaged
tracker_key: OMNI-1
tracker_url: https://tracker.example/OMNI-1
---

# Sample issue for template checks

Register: [[_Issue Register]] · RCA: <fill: RCA> · Resolution: <fill: Resolution>

## Customer Complaint (translated)

Some complaint text.
`

func writeTriageFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "OMNI-1 sample-issue.md")
	if err := os.WriteFile(path, []byte(triageNoteFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpdateTriageStatusSetsStatusAndAddsLinks(t *testing.T) {
	path := writeTriageFixture(t)

	err := UpdateTriageStatus(path, "resolved", map[string]string{
		"rca":        "OMNI-1 export-fails-rca",
		"resolution": "OMNI-1 export-fails-resolution",
	})
	if err != nil {
		t.Fatalf("UpdateTriageStatus: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	want := `---
status: resolved
tracker_key: OMNI-1
tracker_url: https://tracker.example/OMNI-1
rca: OMNI-1 export-fails-rca
resolution: OMNI-1 export-fails-resolution
---

# Sample issue for template checks

Register: [[_Issue Register]] · RCA: <fill: RCA> · Resolution: <fill: Resolution>

## Customer Complaint (translated)

Some complaint text.
`
	if string(got) != want {
		t.Fatalf("UpdateTriageStatus wrote:\n%s\n--- want ---\n%s", got, want)
	}

	// The body must be byte-identical to the original.
	origBody := strings.SplitN(triageNoteFixture, "---\n", 3)[2]
	gotBody := strings.SplitN(string(got), "---\n", 3)[2]
	if origBody != gotBody {
		t.Fatalf("body changed:\n--- got ---\n%s\n--- want ---\n%s", gotBody, origBody)
	}
}

func TestUpdateTriageStatusReplacesExistingLinks(t *testing.T) {
	path := writeTriageFixture(t)

	if err := UpdateTriageStatus(path, "resolved", map[string]string{"rca": "OMNI-1 export-fails-rca"}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := UpdateTriageStatus(path, "resolved", map[string]string{"rca": "OMNI-1 export-fails-rca-v2"}); err != nil {
		t.Fatalf("second update: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	count := strings.Count(string(got), "rca:")
	if count != 1 {
		t.Fatalf("frontmatter has %d 'rca:' lines, want 1:\n%s", count, got)
	}
	if !strings.Contains(string(got), "rca: OMNI-1 export-fails-rca-v2") {
		t.Fatalf("rca link was not replaced:\n%s", got)
	}
}

// --- Digest ---

func TestDigestMatchesGolden(t *testing.T) {
	rows := []DigestRow{
		{Key: "OMNI-1", Priority: "high", Issue: "Export fails for large orders", Confidence: "medium", Classification: "code", State: "done", RunID: "run-1"},
		{Key: "OMNI-2", Priority: "low", Issue: "This export job keeps failing whenever an order has more than five hundred line items", Confidence: "low", Classification: "data", State: "blocked", RunID: "run-2", Reason: "waiting on a data steward to confirm the migration is reversible"},
		{Key: "OMNI-3", Priority: "critical", Issue: "Short one", Confidence: "unknown", Classification: "unknown", State: "failed", RunID: "run-3", Reason: "provider session errored twice"},
		{Key: "OMNI-4", Priority: "medium", Issue: "Budget exceeded case", Confidence: "high", Classification: "config", State: "over_budget", RunID: "run-4", Reason: "exceeded $5 run budget"},
	}
	got := Digest(rows)
	want := readTestdata(t, "digest.golden.txt")
	if got != string(want) {
		t.Fatalf("Digest mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDigestNoReasonBlockWhenNothingBlocked(t *testing.T) {
	rows := []DigestRow{
		{Key: "OMNI-1", Priority: "high", Issue: "Export fails", Confidence: "high", Classification: "code", State: "done", RunID: "run-1"},
	}
	got := Digest(rows)
	if strings.Contains(got, "—") {
		t.Fatalf("Digest included a reason line with nothing blocked/failed/over_budget:\n%s", got)
	}
}
