package run

import (
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
)

// realEvidenceFixAnswer is the exact shape a provider: acp fix-mode session
// answered with in the sandbox evidence
// (sirdar-sandbox/app-acp/.sirdar/runs/SBX-1/20260915T102406Z-77e5/result.raw.txt):
// a complete, otherwise-valid fix report that also carries the fix schema's
// own root "$schema" and "title" keys.
const realEvidenceFixAnswer = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "Sirdar Fix Report",
  "summary": "Unable to apply the approved Return stock fix because this workspace is read-only.",
  "filesChanged": [],
  "testsRun": [],
  "risks": "The Return path remains double-counted, so stock reconciliation is still incorrect for returned quantities.",
  "deviationFromNote": "No changes were made because the workspace explicitly refused write operations. The proposed removal of the direct Return increment and addition of a regression test could not be applied."
}`

func TestStripSchemaEchoRecoversTheRealEvidenceShape(t *testing.T) {
	// The raw answer fails validation exactly the way the live run did.
	if err := note.Validate(note.Fix, []byte(realEvidenceFixAnswer)); err == nil {
		t.Fatal("the raw evidence shape was expected to fail validation")
	} else if !strings.Contains(err.Error(), "'$schema'") || !strings.Contains(err.Error(), "'title'") {
		t.Fatalf("validation error = %v, want it to name $schema and title", err)
	}

	cleaned, schemaItself, ok := stripSchemaEcho(prompt.FixSchema, []byte(realEvidenceFixAnswer))
	if !ok || schemaItself {
		t.Fatalf("stripSchemaEcho: ok=%v schemaItself=%v, want ok=true schemaItself=false", ok, schemaItself)
	}
	if strings.Contains(string(cleaned), "$schema") || strings.Contains(string(cleaned), `"title"`) {
		t.Errorf("cleaned = %s, still carries a schema-metadata key", cleaned)
	}
	if err := note.Validate(note.Fix, cleaned); err != nil {
		t.Errorf("cleaned document still does not validate: %v", err)
	}
	if !strings.Contains(string(cleaned), "double-counted") {
		t.Errorf("cleaned = %s, lost the answer's own content", cleaned)
	}
}

func TestStripSchemaEchoRefusesTheSchemaItself(t *testing.T) {
	// The model answered with the fix schema verbatim rather than a
	// document it describes — the "properties" key is the tell, and there
	// is nothing here to reconstruct an answer from.
	_, schemaItself, ok := stripSchemaEcho(prompt.FixSchema, prompt.FixSchema)
	if ok {
		t.Fatal("stripSchemaEcho should not claim it can fix the schema echoed whole")
	}
	if !schemaItself {
		t.Error("schemaItself = false, want true for a document carrying \"properties\"")
	}
}

func TestStripSchemaEchoKeepsALegitimateTitleField(t *testing.T) {
	// The triage schema's own answer has a root "title" field, so an echo
	// that also carries a genuine title must not lose it — only $schema,
	// which the triage schema never uses as an answer field, is dropped.
	doc := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title": "Export fails for large orders",
		"ticket": {"key":"OMNI-1"},
		"complaint": "The export fails."
	}`
	cleaned, schemaItself, ok := stripSchemaEcho(prompt.TriageSchema, []byte(doc))
	if !ok || schemaItself {
		t.Fatalf("ok=%v schemaItself=%v, want ok=true schemaItself=false", ok, schemaItself)
	}
	if strings.Contains(string(cleaned), "$schema") {
		t.Errorf("cleaned = %s, still carries $schema", cleaned)
	}
	if !strings.Contains(string(cleaned), "Export fails for large orders") {
		t.Errorf("cleaned = %s, dropped the answer's own title", cleaned)
	}
}

func TestStripSchemaEchoLeavesOtherMalformedDocumentsAlone(t *testing.T) {
	// A document whose only problem is a field the schema never heard of —
	// not one of the schema's own metadata keywords — is not this
	// function's business; the ordinary validation error stands.
	doc := `{"summary":"x","filesChanged":[],"testsRun":[],"risks":"none","deviationFromNote":"","notes":"extra"}`
	_, schemaItself, ok := stripSchemaEcho(prompt.FixSchema, []byte(doc))
	if ok || schemaItself {
		t.Fatalf("ok=%v schemaItself=%v, want both false for an unrelated extra key", ok, schemaItself)
	}
}

func TestStripSchemaEchoOnValidDocumentIsANoOp(t *testing.T) {
	doc := `{"summary":"x","filesChanged":[],"testsRun":[],"risks":"none","deviationFromNote":""}`
	_, _, ok := stripSchemaEcho(prompt.FixSchema, []byte(doc))
	if ok {
		t.Error("a document with no schema-metadata keys should report ok=false")
	}
}
