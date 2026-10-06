package shadow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
)

func TestBasis(t *testing.T) {
	pr := Entry{Ev: "outcome", Source: "tracker", PRURLs: []string{"https://github.com/o/r/pull/1"}}
	res := Entry{Ev: "outcome", Source: "manual", Resolution: "fixed"}
	cases := []struct {
		name      string
		outcomes  []Entry
		basis     string
		final, ok bool
	}{
		{"pr", []Entry{pr}, "pr", true, true},
		{"pr+resolution", []Entry{res, pr}, "pr+resolution", true, true},
		{"operator-rca alone", []Entry{{Ev: "outcome", Source: "operator-rca"}}, "operator-rca", true, true},
		{"operator-triage", []Entry{{Ev: "outcome", Source: "operator-triage", Verdict: &Verdict{Hypothesis: "h"}}}, "operator-triage", false, true},
		{"tracker status only", []Entry{{Ev: "outcome", Source: "tracker", Status: "Done"}}, "", false, false},
		{"nothing", nil, "", false, false},
	}
	for _, c := range cases {
		basis, final, ok := Basis(c.outcomes)
		if basis != c.basis || final != c.final || ok != c.ok {
			t.Errorf("%s: Basis = %q %v %v, want %q %v %v", c.name, basis, final, ok, c.basis, c.final, c.ok)
		}
	}
}

// shadowRunDir makes a shadow run directory holding the given files, keyed
// by name, with fixture contents read from testdata when the value names
// one.
func shadowRunDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if strings.HasPrefix(content, "testdata/") {
			raw, err := os.ReadFile(content)
			if err != nil {
				t.Fatal(err)
			}
			content = string(raw)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func gradeInput(dir string, outcomes ...Entry) GradeInput {
	finishedAt := t0.Add(30 * time.Minute)
	return GradeInput{
		Run: ShadowRun{
			Dir:      dir,
			Started:  Entry{Ev: "started", Key: "OMNI-1", RunID: "20261006T090000Z-abcd", At: t0},
			Finished: Entry{Ev: "finished", Key: "OMNI-1", RunID: "20261006T090000Z-abcd", At: finishedAt, Status: "completed"},
		},
		Outcomes:   outcomes,
		JudgeModel: "judge-model",
		Now:        t0.Add(5 * time.Hour),
	}
}

type stubJudge struct {
	reply  string
	cost   float64
	err    error
	calls  int
	prompt string
}

func (s *stubJudge) judge(ctx context.Context, prompt string, schema []byte) (json.RawMessage, float64, error) {
	s.calls++
	s.prompt = prompt
	if s.err != nil {
		return nil, 0, s.err
	}
	return json.RawMessage(s.reply), s.cost, nil
}

func stubPRs(t *testing.T, set eval.PullRequestSet, err error) *[]string {
	t.Helper()
	var asked []string
	prev := readPullRequests
	readPullRequests = func(ctx context.Context, urls []string) (eval.PullRequestSet, error) {
		asked = append(asked, urls...)
		return set, err
	}
	t.Cleanup(func() { readPullRequests = prev })
	return &asked
}

var prOutcome = Entry{Ev: "outcome", Key: "OMNI-1", Source: "manual",
	PRURLs: []string{"https://github.com/o/r/pull/912"}, Resolution: "Added the null check in the order mapper."}

func TestGradeNoAnswer(t *testing.T) {
	j := &stubJudge{reply: `{"verdict":"confirmed","sameRootCause":true,"reason":"x"}`}
	in := gradeInput(t.TempDir(), prOutcome)
	in.OperatorStart = t0.Add(2 * time.Hour)
	line, err := Grade(context.Background(), j.judge, in)
	if err != nil {
		t.Fatal(err)
	}
	if j.calls != 0 {
		t.Errorf("judge called %d times", j.calls)
	}
	if line.Ev != "graded" || line.Key != "OMNI-1" || line.ShadowRunID != "20261006T090000Z-abcd" ||
		line.Grade != "no-answer" || !line.Final || line.Basis != "none" ||
		line.Reason != "the run ended without a reply or note" || !line.At.Equal(in.Now) {
		t.Errorf("line = %+v", line)
	}
}

func TestGradeWithPRFiles(t *testing.T) {
	asked := stubPRs(t, eval.PullRequestSet{
		Files: []string{"src/a.cs", "src/b.cs"},
		PRs:   []eval.PRText{{URL: "https://github.com/o/r/pull/912", Title: "Null check in mapper", Body: "Fixes the dropped discount."}},
		Diff:  []byte("diff --git a/src/a.cs b/src/a.cs\n+if (x == null) return;\n"),
	}, nil)
	dir := shadowRunDir(t, map[string]string{"result.json": "testdata/shadow-result.json", "note.md": "The mapper drops it."})
	j := &stubJudge{reply: `{"verdict":"confirmed","sameRootCause":true,"reason":"same null check"}`, cost: 0.03}
	line, err := Grade(context.Background(), j.judge, gradeInput(dir, prOutcome))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*asked, ",") != "https://github.com/o/r/pull/912" {
		t.Errorf("PRs read = %v", *asked)
	}
	if line.Grade != "confirmed" || line.Reason != "same null check" || line.Basis != "pr+resolution" || !line.Final {
		t.Errorf("line = %+v", line)
	}
	if line.PRFilesHit == nil || *line.PRFilesHit != (Fraction{Matched: 1, Total: 2, Score: 0.5}) {
		t.Errorf("PRFilesHit = %+v", line.PRFilesHit)
	}
	if line.CodeRefsPathOverlap == nil || *line.CodeRefsPathOverlap != (Fraction{Matched: 1, Total: 1, Score: 1}) {
		t.Errorf("CodeRefsPathOverlap = %+v", line.CodeRefsPathOverlap)
	}
	if line.CostUSD != 0.03 || line.JudgeModel != "judge-model" || line.Warning != "" || line.LeadMinutes != nil {
		t.Errorf("line = %+v", line)
	}
	for _, want := range []string{"A missing null check in the order mapper", "src/a.cs:12", "Null check in mapper",
		"Fixes the dropped discount.", "src/b.cs", "+if (x == null) return;", "Added the null check in the order mapper."} {
		if !strings.Contains(j.prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func TestGradePRReadFails(t *testing.T) {
	stubPRs(t, eval.PullRequestSet{}, errors.New("gh not on PATH\ninstall it"))
	dir := shadowRunDir(t, map[string]string{"answer.md": "The mapper drops the discount."})
	j := &stubJudge{reply: `{"verdict":"partial","sameRootCause":false,"reason":"right area"}`}
	line, err := Grade(context.Background(), j.judge, gradeInput(dir, prOutcome))
	if err != nil {
		t.Fatal(err)
	}
	if line.Warning != "PR files unread: gh not on PATH" {
		t.Errorf("Warning = %q", line.Warning)
	}
	if line.PRFilesHit != nil || line.CodeRefsPathOverlap != nil {
		t.Errorf("fractions = %+v %+v, want nil", line.PRFilesHit, line.CodeRefsPathOverlap)
	}
	if j.calls != 1 || line.Grade != "partial" {
		t.Errorf("judge calls = %d, grade = %q", j.calls, line.Grade)
	}
	if !strings.Contains(j.prompt, "The mapper drops the discount.") {
		t.Error("prompt lacks answer.md")
	}
}

func TestGradeUnknownVerdict(t *testing.T) {
	stubPRs(t, eval.PullRequestSet{}, nil)
	dir := shadowRunDir(t, map[string]string{"answer.md": "a reply"})
	j := &stubJudge{reply: `{"verdict":"maybe","sameRootCause":false,"reason":"?"}`}
	line, err := Grade(context.Background(), j.judge, gradeInput(dir, prOutcome))
	if err == nil || err.Error() != `shadow: the judge answered "maybe"` {
		t.Errorf("err = %v", err)
	}
	if line.Ev != "" {
		t.Errorf("line = %+v, want none", line)
	}
}

func TestGradeNotEnoughTruth(t *testing.T) {
	dir := shadowRunDir(t, map[string]string{"answer.md": "a reply"})
	j := &stubJudge{}
	_, err := Grade(context.Background(), j.judge, gradeInput(dir, Entry{Ev: "outcome", Source: "tracker", Status: "Done"}))
	if !errors.Is(err, ErrNotEnoughTruth) || j.calls != 0 {
		t.Errorf("err = %v, calls = %d", err, j.calls)
	}
}

func TestGradeLead(t *testing.T) {
	stubPRs(t, eval.PullRequestSet{}, nil)
	dir := shadowRunDir(t, map[string]string{"answer.md": "a reply"})
	in := gradeInput(dir, prOutcome)
	in.OperatorStart = in.Run.Finished.At.Add(47 * time.Minute)
	j := &stubJudge{reply: `{"verdict":"wrong","sameRootCause":false,"reason":"  ` + strings.Repeat("r", 250) + `  "}`}
	line, err := Grade(context.Background(), j.judge, in)
	if err != nil {
		t.Fatal(err)
	}
	if line.LeadMinutes == nil || *line.LeadMinutes != 47 {
		t.Errorf("LeadMinutes = %v", line.LeadMinutes)
	}
	if line.Reason != strings.Repeat("r", 200) {
		t.Errorf("Reason is %d runes, want 200 trimmed", len([]rune(line.Reason)))
	}
}

func TestJudgePromptLabelsOperatorTriage(t *testing.T) {
	truth := TruthOf([]Entry{{Ev: "outcome", Source: "operator-triage",
		Verdict: &Verdict{Classification: "code", Hypothesis: "The calculator sums voided lines."}}}, eval.PullRequestSet{})
	p := JudgePrompt("a reply", nil, truth)
	for _, want := range []string{"Operator's triage (may also be wrong)", "The calculator sums voided lines.", "a reply",
		"confirmed", "partial", "wrong"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func TestJudgePromptCuts(t *testing.T) {
	truth := TruthOf(nil, eval.PullRequestSet{Diff: []byte(strings.Repeat("d", 30000))})
	p := JudgePrompt(strings.Repeat("a", 20000), nil, truth)
	if strings.Contains(p, strings.Repeat("a", 12001)) {
		t.Error("answer.md not cut to 12,000 runes")
	}
	if strings.Contains(p, strings.Repeat("d", 20001)) || !strings.Contains(p, strings.Repeat("d", 20000)) {
		t.Error("diff not cut to 20,000 bytes")
	}
	// A cut that lands inside a multi-byte rune drops the whole rune.
	if got := cutBytes([]byte("aé"), 2); string(got) != "a" {
		t.Errorf("cutBytes split a rune: %q", got)
	}
}

func TestUngradable(t *testing.T) {
	e := Ungradable("OMNI-1", "run-1", t0)
	if e.Ev != "graded" || e.Key != "OMNI-1" || e.ShadowRunID != "run-1" || e.Grade != "ungradable" ||
		!e.Final || e.Basis != "window" || e.Reason != "no outcome within the window" || !e.At.Equal(t0) {
		t.Errorf("entry = %+v", e)
	}
}
