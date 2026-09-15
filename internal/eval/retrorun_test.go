package eval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// --- fixtures ---------------------------------------------------------

const retroTriageDoc = `{
  "classification": "code",
  "rootCause": {"confidence":"high","codeRefs":["internal/export/csv.go:44"]}
}`

const retroFixReport = `{"summary":"Stream the export.","filesChanged":["internal/export/csv.go"],
  "testsRun":[{"command":"go test ./...","result":"ok"}],"risks":"none","deviationFromNote":""}`

const prDiffText = `diff --git a/internal/export/csv.go b/internal/export/csv.go
--- a/internal/export/csv.go
+++ b/internal/export/csv.go
@@ -40,4 +40,5 @@
-	buf := bytes.NewBuffer(nil)
+	enc := csv.NewWriter(w)
+	defer enc.Flush()
diff --git a/internal/export/pool.go b/internal/export/pool.go
--- a/internal/export/pool.go
+++ b/internal/export/pool.go
@@ -12,2 +12,2 @@
-	size := 100
+	size := 10
`

const agentDiffText = `diff --git a/internal/export/csv.go b/internal/export/csv.go
--- a/internal/export/csv.go
+++ b/internal/export/csv.go
@@ -41,3 +41,4 @@
-	buf := bytes.NewBuffer(nil)
+	enc := csv.NewWriter(w)
`

// newRetroGolden writes a golden entry with a bundle, a retro.json and the
// pull request's diff, and returns the golden root.
func newRetroGolden(t *testing.T, keys ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, key := range keys {
		dir := filepath.Join(root, key)
		if err := ticket.WriteBundle(filepath.Join(dir, "bundle"), sampleBundle()); err != nil {
			t.Fatal(err)
		}
		retro := Retro{
			Key:        key,
			AsOf:       time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
			BaseCommit: "abc123",
			PRUrls:     []string{"https://github.com/acme/repo/pull/7"},
			PRDiff:     "pr.diff",
			PRFiles:    []string{"internal/export/csv.go", "internal/export/pool.go"},
			Redacted:   Redacted{PRLinks: 2, CommentsDropped: 5},
		}
		data, err := json.MarshalIndent(retro, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, RetroFile), string(data))
		write(t, filepath.Join(dir, "pr.diff"), prDiffText)
	}
	return root
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- a fake runner ----------------------------------------------------

// fakeRetroRunner stands in for internal/run: it writes the files a real
// run would leave behind and records what it was asked for.
type fakeRetroRunner struct {
	dir string

	triageDoc  string
	triageNote string
	fixReport  string
	fixDiff    string

	triageState string
	fixState    string

	triageErr error
	fixErr    error
	rcaErr    error

	sawTriage []TriageAt
	sawFix    []FixLocalAt
	sawRCA    []RCAAt
}

func newFakeRunner(t *testing.T) *fakeRetroRunner {
	return &fakeRetroRunner{
		dir:         t.TempDir(),
		triageDoc:   retroTriageDoc,
		triageNote:  "## Root cause\n\nThe export buffers in internal/export/pool.go.\n",
		fixReport:   retroFixReport,
		fixDiff:     agentDiffText,
		triageState: string(store.StatusCompleted),
		fixState:    string(store.StatusCompleted),
	}
}

func (f *fakeRetroRunner) TriageAt(_ context.Context, key string, o TriageAt) (Stage, error) {
	f.sawTriage = append(f.sawTriage, o)
	if f.triageErr != nil {
		return Stage{State: string(store.StatusFailed), Reason: f.triageErr.Error()}, f.triageErr
	}
	doc := filepath.Join(f.dir, key, "triage", "result.json")
	note := filepath.Join(f.dir, key, "triage", "note.md")
	writeIf(f.triageDoc, doc)
	writeIf(f.triageNote, note)
	return Stage{
		RunID: key + "-triage", State: f.triageState,
		Turns: 9, CostUSD: 1.25, Minutes: 3,
		DocPath: doc, NotePath: note,
	}, nil
}

func (f *fakeRetroRunner) FixLocalAt(_ context.Context, key string, o FixLocalAt) (FixStage, error) {
	f.sawFix = append(f.sawFix, o)
	if f.fixErr != nil {
		return FixStage{Stage: Stage{State: string(store.StatusFailed), Reason: f.fixErr.Error()}}, f.fixErr
	}
	doc := filepath.Join(f.dir, key, "fix", "result.json")
	diff := filepath.Join(f.dir, key, "fix", "fix.diff")
	writeIf(f.fixReport, doc)
	writeIf(f.fixDiff, diff)
	return FixStage{
		Stage: Stage{
			RunID: key + "-fix", State: f.fixState,
			Turns: 12, CostUSD: 2.5, Minutes: 6, DocPath: doc,
		},
		DiffPath: diff,
		Commit:   "deadbee",
	}, nil
}

func (f *fakeRetroRunner) RCAAt(_ context.Context, key string, o RCAAt) (Stage, error) {
	f.sawRCA = append(f.sawRCA, o)
	if f.rcaErr != nil {
		return Stage{State: string(store.StatusFailed), Reason: f.rcaErr.Error()}, f.rcaErr
	}
	return Stage{RunID: key + "-rca", State: string(store.StatusCompleted), CostUSD: 0.75}, nil
}

func writeIf(body, path string) {
	if body == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(body), 0o644)
}

// fakeJudge answers the rubric without a provider.
type fakeJudge struct {
	verdict Rubric
	cost    float64
	err     error
	sawPR   string
	sawFix  string
	calls   int
}

func (j *fakeJudge) Judge(_ context.Context, prDiff, agentDiff string) (Rubric, float64, error) {
	j.calls++
	j.sawPR, j.sawFix = prDiff, agentDiff
	return j.verdict, j.cost, j.err
}

// --- tests ------------------------------------------------------------

func TestRunRetroScoresTriageAndFixAgainstTheMergedChange(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	f := newFakeRunner(t)

	rep, err := RunRetro(context.Background(), RetroDeps{Runner: f, Provider: "claude", Model: "sonnet"},
		nil, RetroOptions{GoldenDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 {
		t.Fatalf("results: %d", len(rep.Results))
	}
	r := rep.Results[0]
	if r.Reason != "" {
		t.Fatalf("reason: %s", r.Reason)
	}
	if r.BaseCommit != "abc123" {
		t.Errorf("baseCommit: %q", r.BaseCommit)
	}

	// Both runs stood at the base commit, and the fix started from the
	// note this retro's own triage produced.
	if len(f.sawTriage) != 1 || f.sawTriage[0].Commit != "abc123" {
		t.Fatalf("triage: %+v", f.sawTriage)
	}
	if !strings.HasSuffix(f.sawTriage[0].BundleDir, filepath.Join("OMNI-1", "bundle")) {
		t.Errorf("triage replayed %q, not the as-of bundle", f.sawTriage[0].BundleDir)
	}
	if len(f.sawFix) != 1 || f.sawFix[0].Commit != "abc123" {
		t.Fatalf("fix: %+v", f.sawFix)
	}
	if f.sawFix[0].TriageRunID != "OMNI-1-triage" || !strings.HasSuffix(f.sawFix[0].TriageNote, "note.md") {
		t.Errorf("the fix did not start from this retro's triage note: %+v", f.sawFix[0])
	}

	if r.TriageScore == nil || r.TriageScore.Classification != "code" || r.TriageScore.Confidence != "high" {
		t.Fatalf("triage score: %+v", r.TriageScore)
	}
	if want := (Fraction{Matched: 1, Total: 1, Score: 1}); r.TriageScore.CodeRefsPathOverlap != want {
		t.Errorf("codeRefsPathOverlap: %+v", r.TriageScore.CodeRefsPathOverlap)
	}
	// csv.go through the reference, pool.go through the note's prose.
	if r.TriageScore.PRFilesHit.Matched != 2 {
		t.Errorf("prFilesHit: %+v", r.TriageScore.PRFilesHit)
	}
	if r.FixScore == nil {
		t.Fatal("no fix score")
	}
	if want := (Jaccard{Intersection: 1, Union: 2, Score: 0.5}); r.FixScore.FilesJaccard != want {
		t.Errorf("filesJaccard: %+v", r.FixScore.FilesJaccard)
	}
	if want := (Fraction{Matched: 1, Total: 2, Score: 0.5}); r.FixScore.HunkOverlap != want {
		t.Errorf("hunkOverlap: %+v", r.FixScore.HunkOverlap)
	}
	if r.FixScore.BuildPassed == nil || !*r.FixScore.BuildPassed {
		t.Errorf("buildPassed read from the fix report: %v", r.FixScore.BuildPassed)
	}
	if r.CostUSD != 3.75 {
		t.Errorf("cost: %v, want the triage plus the fix", r.CostUSD)
	}
	// Neither optional stage was asked for.
	if r.RCA != nil || len(f.sawRCA) != 0 {
		t.Errorf("the RCA ran without --with-rca")
	}
	if r.Rubric != nil {
		t.Errorf("the rubric ran without --rubric")
	}
}

func TestRunRetroRunsTheRCAOnlyWhenAsked(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	f := newFakeRunner(t)
	rep, err := RunRetro(context.Background(), RetroDeps{Runner: f}, nil,
		RetroOptions{GoldenDir: root, WithRCA: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.sawRCA) != 1 || f.sawRCA[0].Commit != "abc123" {
		t.Fatalf("rca: %+v", f.sawRCA)
	}
	if rep.Results[0].RCA == nil || rep.Results[0].CostUSD != 4.5 {
		t.Errorf("the RCA's cost is on the row: %+v", rep.Results[0])
	}
}

// The retro RCA is blind: RCAAt has no field for a pull request URL, so
// there is nothing for a later change to fill in by accident.
func TestRCAAtCannotCarryThePullRequest(t *testing.T) {
	var o RCAAt
	if got := reflectFieldNames(o); strings.Contains(strings.ToLower(strings.Join(got, ",")), "pr") {
		t.Errorf("RCAAt names a pull request field: %v", got)
	}
}

func TestRunRetroAsksTheJudgeForBothDiffs(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	f := newFakeRunner(t)
	j := &fakeJudge{verdict: Rubric{SameRootCause: true, SameFix: false, Verdict: "partial", Reasoning: "same file, half the change"}, cost: 0.1}

	rep, err := RunRetro(context.Background(), RetroDeps{Runner: f, Judge: j}, nil,
		RetroOptions{GoldenDir: root, Rubric: true})
	if err != nil {
		t.Fatal(err)
	}
	if j.calls != 1 {
		t.Fatalf("judge calls: %d", j.calls)
	}
	if !strings.Contains(j.sawPR, "pool.go") || !strings.Contains(j.sawFix, "csv.go") {
		t.Errorf("the judge was not given both diffs")
	}
	r := rep.Results[0]
	if r.Rubric == nil || r.Rubric.Verdict != "partial" {
		t.Fatalf("rubric: %+v", r.Rubric)
	}
	if r.RubricCostUSD != 0.1 || r.CostUSD != 3.85 {
		t.Errorf("the rubric call is on the cost: %+v", r)
	}
}

func TestRunRetroKeepsTheRowWhenTheJudgeFails(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	f := newFakeRunner(t)
	j := &fakeJudge{err: errors.New("the model refused")}
	rep, err := RunRetro(context.Background(), RetroDeps{Runner: f, Judge: j}, nil,
		RetroOptions{GoldenDir: root, Rubric: true})
	if err != nil {
		t.Fatal(err)
	}
	r := rep.Results[0]
	if r.Rubric != nil {
		t.Errorf("a failed judge left a verdict: %+v", r.Rubric)
	}
	if r.FixScore == nil || !strings.Contains(r.Reason, "the model refused") {
		t.Errorf("the measured scores survive a failed rubric: %+v", r)
	}
}

func TestRunRetroStopsAtAFailedTriageButKeepsWhatItScored(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	f := newFakeRunner(t)
	f.triageState = string(store.StatusOverBudget)

	rep, err := RunRetro(context.Background(), RetroDeps{Runner: f}, nil, RetroOptions{GoldenDir: root})
	if err != nil {
		t.Fatal(err)
	}
	r := rep.Results[0]
	if len(f.sawFix) != 0 {
		t.Errorf("a fix ran from a triage that did not finish")
	}
	if r.TriageScore == nil {
		t.Errorf("the note that was produced is still scored")
	}
	if !strings.Contains(r.Reason, "triage: over_budget") {
		t.Errorf("reason: %q", r.Reason)
	}
}

func TestRunRetroReportsABuildWithoutTheAtFlag(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	f := newFakeRunner(t)
	f.triageErr = ErrNotSupported

	rep, err := RunRetro(context.Background(), RetroDeps{Runner: f}, nil, RetroOptions{GoldenDir: root})
	if err != nil {
		t.Fatalf("a stage that cannot run is a row, not an aborted command: %v", err)
	}
	if !strings.Contains(rep.Results[0].Reason, "not supported") {
		t.Errorf("reason: %q", rep.Results[0].Reason)
	}
}

func TestRunRetroKeepsGoingPastAKeyWithNoRetroJson(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	// A golden key with a bundle but no retro.json.
	if err := ticket.WriteBundle(filepath.Join(root, "OMNI-2", "bundle"), sampleBundle()); err != nil {
		t.Fatal(err)
	}
	f := newFakeRunner(t)

	rep, err := RunRetro(context.Background(), RetroDeps{Runner: f}, []string{"OMNI-2", "OMNI-1"},
		RetroOptions{GoldenDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Results[0].Reason == "" {
		t.Errorf("OMNI-2 has no retro.json and should say so")
	}
	if rep.Results[1].Reason != "" || rep.Results[1].FixScore == nil {
		t.Errorf("OMNI-1 was still scored: %+v", rep.Results[1])
	}
}

func TestRunRetroReplaysOnlyTheKeysThatCarryARetro(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1", "OMNI-3")
	if err := ticket.WriteBundle(filepath.Join(root, "OMNI-2", "bundle"), sampleBundle()); err != nil {
		t.Fatal(err)
	}
	f := newFakeRunner(t)
	rep, err := RunRetro(context.Background(), RetroDeps{Runner: f}, nil, RetroOptions{GoldenDir: root})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, r := range rep.Results {
		keys = append(keys, r.Key)
	}
	if strings.Join(keys, ",") != "OMNI-1,OMNI-3" {
		t.Errorf("keys: %v", keys)
	}
}

func TestRunRetroWithNoRetroBundles(t *testing.T) {
	root := t.TempDir()
	if err := ticket.WriteBundle(filepath.Join(root, "OMNI-2", "bundle"), sampleBundle()); err != nil {
		t.Fatal(err)
	}
	_, err := RunRetro(context.Background(), RetroDeps{Runner: newFakeRunner(t)}, nil, RetroOptions{GoldenDir: root})
	if err == nil || !strings.Contains(err.Error(), RetroFile) {
		t.Fatalf("want a refusal naming retro.json, got %v", err)
	}
}

func TestRunRetroNeedsARunner(t *testing.T) {
	if _, err := RunRetro(context.Background(), RetroDeps{}, nil, RetroOptions{GoldenDir: t.TempDir()}); err == nil {
		t.Fatal("want an error")
	}
}

func TestRetroReportWriteAndTable(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	f := newFakeRunner(t)
	j := &fakeJudge{verdict: Rubric{Verdict: "partial", Reasoning: "half the change"}, cost: 0.1}
	rep, err := RunRetro(context.Background(), RetroDeps{Runner: f, Judge: j, Provider: "claude"}, nil,
		RetroOptions{GoldenDir: root, Rubric: true})
	if err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	path, err := rep.Write(ws)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, RetroSuffix) {
		t.Errorf("a retro report is named apart from an eval report: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back RetroReport
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Results) != 1 || back.Results[0].Triage.RunID != "OMNI-1-triage" {
		t.Errorf("the run ids are in the report: %+v", back.Results)
	}
	if back.Results[0].FixScore == nil || back.Results[0].Rubric == nil {
		t.Errorf("the scores survive the round trip: %+v", back.Results[0])
	}

	table := rep.Table()
	for _, want := range []string{"KEY", "CLASS", "RUBRIC", "OMNI-1", "code", "high", "partial", "3.85"} {
		if !strings.Contains(table, want) {
			t.Errorf("the table does not carry %q:\n%s", want, table)
		}
	}
	if !strings.Contains(table, "half the change") {
		t.Errorf("the rubric's reasoning is printed under the table:\n%s", table)
	}
}

func TestRetroTableOnARowWithNothingScored(t *testing.T) {
	rep := RetroReport{Results: []RetroResult{{Key: "OMNI-9", Reason: "no bundle"}}}
	table := rep.Table()
	if !strings.Contains(table, "OMNI-9") || !strings.Contains(table, "no bundle") {
		t.Errorf("table:\n%s", table)
	}
	if strings.Count(table, "-") < 5 {
		t.Errorf("an unscored row prints dashes:\n%s", table)
	}
}

func TestLoadRetro(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	r, err := LoadRetro(root, "OMNI-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseCommit != "abc123" || r.Redacted.CommentsDropped != 5 {
		t.Errorf("got %+v", r)
	}
	if !strings.HasSuffix(r.DiffPath(), filepath.Join("OMNI-1", "pr.diff")) {
		t.Errorf("diff path: %s", r.DiffPath())
	}
	d, err := r.ReadDiff()
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Files(d); len(got) != 2 || got[0] != "internal/export/csv.go" {
		t.Errorf("files: %v", got)
	}

	// A retro.json that lists no files falls back to the diff.
	r.PRFiles = nil
	if got := r.Files(d); len(got) != 2 {
		t.Errorf("fallback to the diff's own paths: %v", got)
	}
}

func TestLoadRetroRefusesOneWithNoBaseCommit(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	write(t, filepath.Join(root, "OMNI-1", RetroFile), `{"key":"OMNI-1","prDiff":"pr.diff"}`)
	if _, err := LoadRetro(root, "OMNI-1"); err == nil || !strings.Contains(err.Error(), "baseCommit") {
		t.Fatalf("want a refusal naming baseCommit, got %v", err)
	}
}

func TestLoadRetroRefusesOneWithNoDiff(t *testing.T) {
	root := newRetroGolden(t, "OMNI-1")
	if err := os.Remove(filepath.Join(root, "OMNI-1", "pr.diff")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRetro(root, "OMNI-1"); err == nil {
		t.Fatal("want an error for a retro whose diff is missing")
	}
}

// reflectFieldNames is a small helper for the blind-RCA assertion.
func reflectFieldNames(v any) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		out = append(out, t.Field(i).Name)
	}
	return out
}
