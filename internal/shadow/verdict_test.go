package shadow

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func writeResult(t *testing.T, doc string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReproductionOf(t *testing.T) {
	for _, outcome := range []string{"reproduced", "not_reproduced", "not_attempted"} {
		dir := writeResult(t, `{"classification":"code","reproduction":{"attempted":true,"outcome":"`+outcome+`","where":null,"steps":[],"evidence":[]}}`)
		if got := ReproductionOf(dir); got != outcome {
			t.Errorf("ReproductionOf(%s) = %q", outcome, got)
		}
	}
	cases := map[string]string{
		"null":          `{"classification":"code","reproduction":null}`,
		"missing key":   `{"classification":"code"}`,
		"other outcome": `{"reproduction":{"outcome":"maybe"}}`,
		"unparseable":   `{"reproduction":`,
	}
	for name, doc := range cases {
		if got := ReproductionOf(writeResult(t, doc)); got != "unknown" {
			t.Errorf("%s: ReproductionOf = %q, want unknown", name, got)
		}
	}
	if got := ReproductionOf(t.TempDir()); got != "unknown" {
		t.Errorf("no result.json: ReproductionOf = %q, want unknown", got)
	}
}

func TestVerdictOf(t *testing.T) {
	long := strings.Repeat("é", 500)
	dir := writeResult(t, `{"classification":"code","rootCause":{"hypothesis":"`+long+`","confidence":"medium","evidence":[],"codeRefs":["src/a.cs:12"]},"openQuestions":["which tenant?"]}`)
	v, ok := VerdictOf(dir)
	if !ok {
		t.Fatal("VerdictOf found no verdict")
	}
	if n := utf8.RuneCountInString(v.Hypothesis); n != 300 {
		t.Errorf("hypothesis is %d runes, want 300", n)
	}
	if v.Classification != "code" || v.Confidence != "medium" {
		t.Errorf("verdict = %+v", v)
	}
	if !reflect.DeepEqual(v.CodeRefs, []string{"src/a.cs:12"}) || !reflect.DeepEqual(v.OpenQuestions, []string{"which tenant?"}) {
		t.Errorf("verdict refs/questions = %+v", v)
	}
}

func TestVerdictOfAbsent(t *testing.T) {
	if _, ok := VerdictOf(t.TempDir()); ok {
		t.Error("a verdict from a run with no result.json")
	}
	if _, ok := VerdictOf(writeResult(t, `{"classification":`)); ok {
		t.Error("a verdict from an unparseable result.json")
	}
	v, ok := VerdictOf(writeResult(t, `{"classification":"data","rootCause":{"hypothesis":"h","confidence":"low","evidence":[],"codeRefs":[]}}`))
	if !ok || v.CodeRefs == nil {
		t.Errorf("VerdictOf = %+v, %v; want codeRefs as an empty list", v, ok)
	}
}
