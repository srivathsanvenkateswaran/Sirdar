package shadow

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// maxHypothesisRunes bounds the hypothesis a ledger line carries. The full
// text stays in the run's result.json; the ledger needs enough for grading
// and the scorecard, and must keep each line small.
const maxHypothesisRunes = 300

// triageDoc is the part of a triage result.json a shadow verdict reads.
type triageDoc struct {
	Classification string `json:"classification"`
	RootCause      *struct {
		Hypothesis string   `json:"hypothesis"`
		Confidence string   `json:"confidence"`
		CodeRefs   []string `json:"codeRefs"`
	} `json:"rootCause"`
	OpenQuestions []string `json:"openQuestions"`
	Reproduction  *struct {
		Outcome string `json:"outcome"`
	} `json:"reproduction"`
}

func readTriageDoc(runDir string) (triageDoc, bool) {
	var doc triageDoc
	raw, err := os.ReadFile(filepath.Join(runDir, "result.json"))
	if err != nil {
		return doc, false
	}
	if json.Unmarshal(raw, &doc) != nil {
		return doc, false
	}
	return doc, true
}

// VerdictOf reads a shadow run's verdict from its result.json. It answers
// false when the file is missing, does not parse, or has no root cause:
// the note turn did not produce a triage note, and grading falls back to
// the reply.
func VerdictOf(runDir string) (*Verdict, bool) {
	doc, ok := readTriageDoc(runDir)
	if !ok || doc.RootCause == nil {
		return nil, false
	}
	refs := doc.RootCause.CodeRefs
	if refs == nil {
		refs = []string{}
	}
	return &Verdict{
		Classification: doc.Classification,
		Confidence:     doc.RootCause.Confidence,
		Hypothesis:     cutRunes(doc.RootCause.Hypothesis, maxHypothesisRunes),
		CodeRefs:       refs,
		OpenQuestions:  doc.OpenQuestions,
	}, true
}

// ReproductionOf reads the triage note's reproduction outcome. Anything
// other than the schema's three outcomes, including a missing note or a
// null reproduction, is "unknown", so the ledger only ever holds four
// values.
func ReproductionOf(runDir string) string {
	doc, ok := readTriageDoc(runDir)
	if !ok || doc.Reproduction == nil {
		return "unknown"
	}
	switch o := doc.Reproduction.Outcome; o {
	case "reproduced", "not_reproduced", "not_attempted":
		return o
	}
	return "unknown"
}

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
