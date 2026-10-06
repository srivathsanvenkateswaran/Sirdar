package store

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestSuggestionID: the id is a pure function of the playbook and the
// addition, "s-" and twelve hex digits, and differs when either changes.
func TestSuggestionID(t *testing.T) {
	a := SuggestionID("20-orders.md", "- Check the tax rounding.")
	if a != SuggestionID("20-orders.md", "- Check the tax rounding.") {
		t.Fatalf("id is not stable")
	}
	if !regexp.MustCompile(`^s-[0-9a-f]{12}$`).MatchString(a) {
		t.Fatalf("id %q, want s- and 12 hex digits", a)
	}
	if a == SuggestionID("30-orders.md", "- Check the tax rounding.") || a == SuggestionID("20-orders.md", "- Other.") {
		t.Fatalf("id %q does not change with its inputs", a)
	}
}

// TestAppendSuggestionDedupe: the same proposal twice lands once; once it
// is dismissed, proposing it again is a new pending suggestion.
func TestAppendSuggestionDedupe(t *testing.T) {
	root := t.TempDir()
	p := SuggestionLine{Ev: "proposed", Source: "rca", Key: "OMNI-1", Playbook: "20-orders.md", Addition: "- Rule."}
	ok, err := AppendSuggestion(root, p)
	if err != nil || !ok {
		t.Fatalf("first append: %v %v", ok, err)
	}
	ok, err = AppendSuggestion(root, p)
	if err != nil || ok {
		t.Fatalf("second append: appended %v err %v, want false nil", ok, err)
	}
	lines, err := ReadSuggestions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("%d lines, want 1", len(lines))
	}
	id := lines[0].ID
	if id != SuggestionID(p.Playbook, p.Addition) || lines[0].V != 1 || lines[0].At.IsZero() {
		t.Fatalf("line %+v: want the id filled, v 1 and a time", lines[0])
	}
	if _, err := AppendSuggestion(root, SuggestionLine{Ev: "dismissed", ID: id}); err != nil {
		t.Fatal(err)
	}
	ok, err = AppendSuggestion(root, p)
	if err != nil || !ok {
		t.Fatalf("after dismissal: appended %v err %v, want true nil", ok, err)
	}
	lines, _ = ReadSuggestions(root)
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3", len(lines))
	}
	if pend := PendingSuggestions(lines); len(pend) != 1 || pend[0].ID != id {
		t.Fatalf("pending %+v, want the re-proposal", pend)
	}
	info, err := os.Stat(SuggestionsPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
}

// TestPendingSuggestions: accepted and dismissed proposals leave the
// queue, the rest stay oldest first, and a torn line costs only itself.
func TestPendingSuggestions(t *testing.T) {
	root := t.TempDir()
	for _, add := range []string{"- A.", "- B.", "- C."} {
		if _, err := AppendSuggestion(root, SuggestionLine{Ev: "proposed", Playbook: "20-x.md", Addition: add}); err != nil {
			t.Fatal(err)
		}
	}
	idA, idB := SuggestionID("20-x.md", "- A."), SuggestionID("20-x.md", "- B.")
	if _, err := AppendSuggestion(root, SuggestionLine{Ev: "accepted", ID: idB, Playbook: "20-x.md"}); err != nil {
		t.Fatal(err)
	}
	// A crash partway through a write.
	f, err := os.OpenFile(SuggestionsPath(root), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"v":1,"ev":"dismi`)
	f.Close()
	if _, err := AppendSuggestion(root, SuggestionLine{Ev: "proposed", Playbook: "20-x.md", Addition: "- D."}); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadSuggestions(root)
	if err != nil {
		t.Fatal(err)
	}
	pend := PendingSuggestions(lines)
	var got []string
	for _, p := range pend {
		got = append(got, p.Addition)
	}
	if strings.Join(got, " ") != "- A. - C. - D." {
		t.Fatalf("pending %q, want A, C, D in order", got)
	}
	if pend[0].ID != idA {
		t.Errorf("first pending id %q, want %q", pend[0].ID, idA)
	}
	if len(PendingSuggestions(nil)) != 0 {
		t.Errorf("no lines should be no pending suggestions")
	}
}
