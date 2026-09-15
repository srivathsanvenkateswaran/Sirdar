package eval

import (
	"reflect"
	"testing"
)

func TestFilesJaccard(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want Jaccard
	}{
		{
			name: "the same two files",
			a:    []string{"a.go", "b.go"},
			b:    []string{"b.go", "a.go"},
			want: Jaccard{Intersection: 2, Union: 2, Score: 1},
		},
		{
			name: "one of three in common",
			a:    []string{"a.go", "b.go"},
			b:    []string{"b.go", "c.go"},
			want: Jaccard{Intersection: 1, Union: 3, Score: 1.0 / 3.0},
		},
		{
			name: "nothing in common",
			a:    []string{"a.go"},
			b:    []string{"b.go"},
			want: Jaccard{Intersection: 0, Union: 2, Score: 0},
		},
		{
			name: "separators and a leading ./ do not make two files",
			a:    []string{`internal\export\csv.go`},
			b:    []string{"./internal/export/csv.go"},
			want: Jaccard{Intersection: 1, Union: 1, Score: 1},
		},
		{
			name: "a repeated path counts once",
			a:    []string{"a.go", "a.go"},
			b:    []string{"a.go"},
			want: Jaccard{Intersection: 1, Union: 1, Score: 1},
		},
		{
			// Two empty diffs are two missing diffs, not a perfect match.
			name: "two empty sets score zero, not one",
			a:    nil,
			b:    nil,
			want: Jaccard{},
		},
		{
			name: "an agent that changed nothing",
			a:    nil,
			b:    []string{"a.go", "b.go"},
			want: Jaccard{Intersection: 0, Union: 2, Score: 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FilesJaccard(tc.a, tc.b)
			if got.Intersection != tc.want.Intersection || got.Union != tc.want.Union {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if diff := got.Score - tc.want.Score; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("score %v, want %v", got.Score, tc.want.Score)
			}
		})
	}
}

func TestHunkOverlap(t *testing.T) {
	prDiff := func(body string) Diff { return ParseDiff(body) }

	cases := []struct {
		name      string
		pr, agent string
		want      Fraction
	}{
		{
			name: "the same window in the same file",
			pr: `--- a/x.go
+++ b/x.go
@@ -40,7 +40,9 @@
-a
+b
+c
`,
			agent: `--- a/x.go
+++ b/x.go
@@ -42,3 +42,4 @@
-a
+b
`,
			want: Fraction{Matched: 1, Total: 1, Score: 1},
		},
		{
			name: "the same file, windows that do not meet",
			pr: `--- a/x.go
+++ b/x.go
@@ -10,3 +10,3 @@
-a
+b
`,
			agent: `--- a/x.go
+++ b/x.go
@@ -400,3 +400,3 @@
-a
+b
`,
			want: Fraction{Matched: 0, Total: 1, Score: 0},
		},
		{
			name: "the same window in a different file counts for nothing",
			pr: `--- a/x.go
+++ b/x.go
@@ -10,3 +10,3 @@
-a
+b
`,
			agent: `--- a/y.go
+++ b/y.go
@@ -10,3 +10,3 @@
-a
+b
`,
			want: Fraction{Matched: 0, Total: 1, Score: 0},
		},
		{
			name: "one of two hunks met",
			pr: `--- a/x.go
+++ b/x.go
@@ -10,3 +10,3 @@
-a
+b
@@ -80,3 +80,3 @@
-c
+d
`,
			agent: `--- a/x.go
+++ b/x.go
@@ -11,2 +11,3 @@
-a
+b
+e
`,
			want: Fraction{Matched: 1, Total: 2, Score: 0.5},
		},
		{
			// Both sides only add lines, so neither covers a base line
			// of its own; the insertion point is the window.
			name: "two insertions at the same point meet",
			pr: `--- a/x.go
+++ b/x.go
@@ -40,0 +41,2 @@
+a
+b
`,
			agent: `--- a/x.go
+++ b/x.go
@@ -40,0 +41,1 @@
+a
`,
			want: Fraction{Matched: 1, Total: 1, Score: 1},
		},
		{
			// The pull request renamed the file; both diffs are against
			// the same base, so the old name is what lines them up.
			name: "a rename is compared on the name at the base commit",
			pr: `diff --git a/old/x.go b/new/x.go
rename from old/x.go
rename to new/x.go
--- a/old/x.go
+++ b/new/x.go
@@ -10,3 +10,3 @@
-a
+b
`,
			agent: `--- a/old/x.go
+++ b/old/x.go
@@ -10,3 +10,3 @@
-a
+c
`,
			want: Fraction{Matched: 1, Total: 1, Score: 1},
		},
		{
			name: "a pull request with no hunks scores nothing either way",
			pr: `diff --git a/old/x.go b/new/x.go
rename from old/x.go
rename to new/x.go
`,
			agent: `--- a/old/x.go
+++ b/old/x.go
@@ -10,3 +10,3 @@
-a
+c
`,
			want: Fraction{},
		},
		{
			name: "an agent that changed nothing meets none of them",
			pr: `--- a/x.go
+++ b/x.go
@@ -10,3 +10,3 @@
-a
+b
`,
			agent: ``,
			want:  Fraction{Matched: 0, Total: 1, Score: 0},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HunkOverlap(prDiff(tc.pr), ParseDiff(tc.agent))
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRefPath(t *testing.T) {
	cases := map[string]string{
		"internal/export/csv.go:42":                            "internal/export/csv.go",
		"Domain/Inventory.API/Export/Csv.cs:253-282":           "Domain/Inventory.API/Export/Csv.cs",
		"Domain/Inventory.API/Export/Csv.cs:253-282 (default)": "Domain/Inventory.API/Export/Csv.cs",
		"  internal/export/csv.go  ":                           "internal/export/csv.go",
		`internal\export\csv.go:9`:                             "internal/export/csv.go",
		"internal/export/csv.go":                               "internal/export/csv.go",
		"":                                                     "",
	}
	for in, want := range cases {
		if got := RefPath(in); got != want {
			t.Errorf("RefPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScoreTriage(t *testing.T) {
	doc := []byte(`{
      "classification": "code",
      "rootCause": {
        "confidence": "medium",
        "codeRefs": ["internal/export/csv.go:42", "internal/export/csv.go:88", "docs/notes.md:3"]
      }
    }`)

	got := ScoreTriage(doc, "## Root cause\n\nThe buffer in internal/export/writer.go grows without bound.\n",
		[]string{"internal/export/csv.go", "internal/export/writer.go", "internal/export/pool.go"})

	if got.Classification != "code" || got.Confidence != "medium" {
		t.Errorf("classification/confidence: got %q/%q", got.Classification, got.Confidence)
	}
	// Two distinct reference paths; csv.go is in the change, notes.md is not.
	if want := (Fraction{Matched: 1, Total: 2, Score: 0.5}); got.CodeRefsPathOverlap != want {
		t.Errorf("codeRefsPathOverlap: got %+v, want %+v", got.CodeRefsPathOverlap, want)
	}
	if !reflect.DeepEqual(got.StrayRefs, []string{"docs/notes.md"}) {
		t.Errorf("strayRefs: got %v", got.StrayRefs)
	}
	// csv.go through the references, writer.go through the prose, pool.go
	// nowhere.
	if want := (Fraction{Matched: 2, Total: 3, Score: 2.0 / 3.0}); got.PRFilesHit.Matched != want.Matched || got.PRFilesHit.Total != want.Total {
		t.Errorf("prFilesHit: got %+v, want %+v", got.PRFilesHit, want)
	}
	if !reflect.DeepEqual(got.MissedFiles, []string{"internal/export/pool.go"}) {
		t.Errorf("missedFiles: got %v", got.MissedFiles)
	}
}

func TestScoreTriageNamesAFileCitedByItsTail(t *testing.T) {
	doc := []byte(`{"classification":"config","rootCause":{"confidence":"low","codeRefs":[]}}`)
	got := ScoreTriage(doc, "The bug is in Export/Csv.cs, which buffers the whole file.",
		[]string{"src/Domain/Inventory.API/Export/Csv.cs"})
	if got.PRFilesHit.Matched != 1 {
		t.Errorf("a note citing the tail of the path found it: got %+v", got.PRFilesHit)
	}
}

func TestScoreTriageOnANoteThatWillNotParse(t *testing.T) {
	got := ScoreTriage([]byte("not json"), "", []string{"a.go"})
	if got.Classification != "" || got.CodeRefsPathOverlap.Total != 0 {
		t.Errorf("got %+v", got)
	}
	if got.PRFilesHit != (Fraction{Matched: 0, Total: 1}) {
		t.Errorf("the change's files are still counted as missed: %+v", got.PRFilesHit)
	}
}

func TestScoreFix(t *testing.T) {
	pr := ParseDiff(`--- a/internal/export/csv.go
+++ b/internal/export/csv.go
@@ -40,4 +40,6 @@
-a
-b
+c
+d
+e
diff --git a/internal/export/pool.go b/internal/export/pool.go
--- a/internal/export/pool.go
+++ b/internal/export/pool.go
@@ -1,2 +1,2 @@
-x
+y
`)
	agent := ParseDiff(`--- a/internal/export/csv.go
+++ b/internal/export/csv.go
@@ -41,2 +41,3 @@
-a
+c
+d
`)
	passed := true
	got := ScoreFix(pr, agent, []string{"internal/export/csv.go", "internal/export/pool.go"}, &passed)

	if want := (Jaccard{Intersection: 1, Union: 2, Score: 0.5}); got.FilesJaccard != want {
		t.Errorf("filesJaccard: got %+v, want %+v", got.FilesJaccard, want)
	}
	if want := (Fraction{Matched: 1, Total: 2, Score: 0.5}); got.HunkOverlap != want {
		t.Errorf("hunkOverlap: got %+v, want %+v", got.HunkOverlap, want)
	}
	if got.LinesAdded != (LinePair{Agent: 2, PR: 4}) {
		t.Errorf("linesAdded: got %+v", got.LinesAdded)
	}
	if got.LinesRemoved != (LinePair{Agent: 1, PR: 3}) {
		t.Errorf("linesRemoved: got %+v", got.LinesRemoved)
	}
	if got.BuildPassed == nil || !*got.BuildPassed {
		t.Errorf("buildPassed: got %v", got.BuildPassed)
	}
	if !reflect.DeepEqual(got.AgentFiles, []string{"internal/export/csv.go"}) {
		t.Errorf("agentFiles: got %v", got.AgentFiles)
	}
}

func TestBuildPassed(t *testing.T) {
	cases := []struct {
		name   string
		report string
		want   *bool
	}{
		{
			name:   "every command passed",
			report: `{"testsRun":[{"command":"go build ./...","result":"ok"},{"command":"go test ./...","result":"all tests passed"}]}`,
			want:   boolPtr(true),
		},
		{
			name:   "one command failed",
			report: `{"testsRun":[{"command":"go build ./...","result":"ok"},{"command":"go test ./...","result":"FAIL: 2 tests"}]}`,
			want:   boolPtr(false),
		},
		{
			name:   "the session ran nothing",
			report: `{"testsRun":[]}`,
			want:   nil,
		},
		{
			name:   "a result nobody can read either way",
			report: `{"testsRun":[{"command":"make","result":"see the log"}]}`,
			want:   nil,
		},
		{
			name:   "not a report at all",
			report: `nonsense`,
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildPassed([]byte(tc.report))
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("got %v, want nil", *got)
			case tc.want != nil && got == nil:
				t.Errorf("got nil, want %v", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Errorf("got %v, want %v", *got, *tc.want)
			}
		})
	}
}

func TestSamePath(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"internal/export/csv.go", "internal/export/csv.go", true},
		{"internal/export/csv.go", "export/csv.go", true},
		{"export/csv.go", "internal/export/csv.go", true},
		{"internal/export/csv.go", "internal/export/pool.go", false},
		// A suffix that does not fall on a separator is a different file.
		{"internal/export/mycsv.go", "csv.go", false},
		{"", "csv.go", false},
	}
	for _, tc := range cases {
		if got := samePath(tc.a, tc.b); got != tc.want {
			t.Errorf("samePath(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
