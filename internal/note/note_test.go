package note

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return data
}

// assertGolden compares got against testdata/name. Set UPDATE_GOLDEN=1 to
// overwrite the golden file with got instead of failing, then re-review the
// diff by eye before committing it.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden testdata/%s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	if got != string(want) {
		t.Fatalf("testdata/%s mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
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
	assertGolden(t, "triage.golden.md", got)
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
	assertGolden(t, "rca.golden.md", got)
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
	assertGolden(t, "resolution.golden.md", got)
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

// --- Frontmatter YAML safety ---
//
// Frontmatter scalars go through the "yq" template func, which renders them
// as YAML double-quoted scalars, so values containing ": ", "#", quotes, or
// non-Latin text can't corrupt the frontmatter block. These tests render
// with such a value and confirm the result parses as YAML and round-trips.

const specialCustomer = `شركة: "كود" #1`

// withTitle returns a copy of doc (decoded from JSON) with title (a
// top-level field for Triage, or nested under key for the combined RCA
// document) replaced by a value containing "a: b", to also exercise a body
// heading with a colon in it alongside the frontmatter customer field.
func withTitle(t *testing.T, doc []byte, key string) []byte {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(doc, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	const title = "Export fails: a: b"
	if key == "" {
		decoded["title"] = title
	} else {
		obj, ok := decoded[key].(map[string]any)
		if !ok {
			t.Fatalf("decoded[%q] is not an object", key)
		}
		obj["title"] = title
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

// assertFrontmatterRoundTrips extracts rendered's frontmatter block, parses
// it as YAML, and asserts it parses cleanly and that "customer" comes back
// exactly as wantCustomer.
func assertFrontmatterRoundTrips(t *testing.T, rendered, wantCustomer string) {
	t.Helper()
	fm, err := extractFrontmatter(rendered)
	if err != nil {
		t.Fatalf("extractFrontmatter: %v\n%s", err, rendered)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(fm), &parsed); err != nil {
		t.Fatalf("frontmatter is not valid YAML: %v\n--- frontmatter ---\n%s", err, fm)
	}
	got, _ := parsed["customer"].(string)
	if got != wantCustomer {
		t.Fatalf("customer round-tripped as %q, want %q\n--- frontmatter ---\n%s", got, wantCustomer, fm)
	}
}

func TestRenderTriageFrontmatterRoundTripsSpecialCharacters(t *testing.T) {
	doc := withTitle(t, readTestdata(t, "triage.json"), "")
	m := triageMeta()
	m.Customer = specialCustomer

	got, err := (Renderer{}).Render(Triage, doc, m)
	if err != nil {
		t.Fatalf("Render(Triage): %v", err)
	}
	assertFrontmatterRoundTrips(t, got, specialCustomer)
}

func TestRenderRCAFrontmatterRoundTripsSpecialCharacters(t *testing.T) {
	doc := withTitle(t, readTestdata(t, "rca.json"), "rca")
	m := triageMeta()
	m.Customer = specialCustomer
	m.Links.Triage = "OMNI-1 export-fails"
	m.Links.Resolution = "OMNI-1 export-fails-resolution"

	got, err := (Renderer{}).Render(RCA, doc, m)
	if err != nil {
		t.Fatalf("Render(RCA): %v", err)
	}
	assertFrontmatterRoundTrips(t, got, specialCustomer)
}

func TestRenderResolutionFrontmatterRoundTripsSpecialCharacters(t *testing.T) {
	doc := withTitle(t, readTestdata(t, "rca.json"), "resolution")
	m := triageMeta()
	m.Customer = specialCustomer
	m.Links.Triage = "OMNI-1 export-fails"
	m.Links.RCA = "OMNI-1 export-fails-rca"

	got, err := (Renderer{}).Render(Resolution, doc, m)
	if err != nil {
		t.Fatalf("Render(Resolution): %v", err)
	}
	assertFrontmatterRoundTrips(t, got, specialCustomer)
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

func TestCheckFailsOnInvalidFrontmatterYAML(t *testing.T) {
	dir := t.TempDir()
	// Parses and renders fine as a template, but the frontmatter it
	// produces is not valid YAML: an unterminated double-quoted scalar.
	broken := "---\nfoo: \"unterminated\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "triage.md.tmpl"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	r := Renderer{TemplatesDir: dir}
	if err := r.Check(); err == nil {
		t.Fatal("Check: got nil error, want the invalid frontmatter YAML rejected")
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

func TestUpdateTriageStatusErrorsOnMissingFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-frontmatter.md")
	original := "# Just a note\n\nNo frontmatter here.\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	err := UpdateTriageStatus(path, "resolved", nil)
	if err == nil {
		t.Fatal("UpdateTriageStatus: got nil error, want one for a file with no frontmatter")
	}
	if !strings.Contains(err.Error(), "has no frontmatter") {
		t.Fatalf("error = %v, want it to name the missing-frontmatter condition", err)
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != original {
		t.Fatalf("file was modified despite the error:\n%s", got)
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
	assertGolden(t, "digest.golden.txt", got)
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

func TestDigestTruncatesIssueByRuneNotByte(t *testing.T) {
	// Each "ع" is two bytes in UTF-8; a byte-based truncation at 60 bytes
	// would split the 30th rune in half and leave a broken multi-byte
	// sequence (or a replacement character) in the output.
	issue := strings.Repeat("ع", 70)
	rows := []DigestRow{
		{Key: "OMNI-9", Priority: "low", Issue: issue, Confidence: "low", Classification: "data", State: "done", RunID: "run-9"},
	}
	got := Digest(rows)

	if strings.ContainsRune(got, '�') {
		t.Fatalf("Digest output contains a UTF-8 replacement character (a rune was split):\n%s", got)
	}
	want := strings.Repeat("ع", 60)
	if !strings.Contains(got, want) {
		t.Fatalf("Digest did not contain the 60-rune truncated issue:\n%s", got)
	}
	if strings.Contains(got, want+"ع") {
		t.Fatalf("Digest issue was not truncated to 60 runes:\n%s", got)
	}
}
