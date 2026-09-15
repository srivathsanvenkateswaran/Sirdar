package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

func TestRubricFromJSON(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    Rubric
		wantErr string
	}{
		{
			name: "a well formed answer",
			in:   `{"sameRootCause":true,"sameFix":false,"verdict":"partial","reasoning":"same file, half the change"}`,
			want: Rubric{SameRootCause: true, Verdict: "partial", Reasoning: "same file, half the change"},
		},
		{
			name: "the verdict is folded and trimmed",
			in:   `{"sameRootCause":true,"sameFix":true,"verdict":" Equivalent ","reasoning":"x"}`,
			want: Rubric{SameRootCause: true, SameFix: true, Verdict: "equivalent", Reasoning: "x"},
		},
		{
			name:    "a verdict nobody can read",
			in:      `{"verdict":"maybe","reasoning":"x"}`,
			wantErr: "not one of",
		},
		{
			name:    "not JSON at all",
			in:      `sorry, I can't do that`,
			wantErr: "not valid JSON",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RubricFromJSON([]byte(tc.in))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want an error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRubricReasoningIsCutAtTheCeiling(t *testing.T) {
	long := strings.Repeat("a", ReasoningMax+120)
	got, err := RubricFromJSON([]byte(`{"verdict":"different","reasoning":"` + long + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Reasoning) > ReasoningMax {
		t.Errorf("reasoning is %d characters, want at most %d", len(got.Reasoning), ReasoningMax)
	}
}

func TestRubricPromptCarriesBothDiffsAndTheFixedQuestion(t *testing.T) {
	p := RubricPrompt("PR-DIFF-BODY", "AGENT-DIFF-BODY")
	for _, want := range []string{"PR-DIFF-BODY", "AGENT-DIFF-BODY", "sameRootCause", "sameFix", "verdict", "400 characters"} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt does not carry %q", want)
		}
	}
	if strings.Index(p, "PR-DIFF-BODY") > strings.Index(p, "AGENT-DIFF-BODY") {
		t.Error("the merged change is shown first, so the question reads the same way every time")
	}
}

func TestRubricPromptTruncatesAHugeDiff(t *testing.T) {
	huge := strings.Repeat("+x\n", diffChars)
	p := RubricPrompt(huge, "small")
	if len(p) > 2*diffChars {
		t.Errorf("the prompt is %d characters; a half-megabyte pull request is not a rubric question", len(p))
	}
	if !strings.Contains(p, "truncated") {
		t.Error("a truncated diff says so")
	}
}

// The judge is one session with no tools: it reads two diffs out of its own
// prompt, and a judge that could read the repository would be scoring what
// it found there instead of what it was shown.
func TestProviderRubricAsksOneToollessSession(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{
		doc:   `{"sameRootCause":true,"sameFix":true,"verdict":"equivalent","reasoning":"same change"}`,
		specs: make(chan provider.SessionSpec, 1),
	}
	deps := newDeps(t, cfg, p)

	j := NewRubricer(deps, "sonnet")
	if j == nil {
		t.Fatal("no judge for a workspace that has a provider")
	}
	got, cost, err := j.Judge(context.Background(), "PR", "AGENT")
	if err != nil {
		t.Fatal(err)
	}
	if got.Verdict != "equivalent" || !got.SameFix {
		t.Errorf("got %+v", got)
	}
	if cost != 0.25 {
		t.Errorf("the call's cost is reported: %v", cost)
	}

	spec := <-p.specs
	if spec.Mode.IsFix() {
		t.Error("the judge runs in fix mode")
	}
	if !spec.MCPStrict || spec.MCPConfig != "" {
		t.Error("the judge is given MCP servers to read")
	}
	if spec.Budget.MaxTurns > 2 {
		t.Errorf("the judge's turn ceiling is %d", spec.Budget.MaxTurns)
	}
	if !strings.Contains(spec.Prompt, "PR") || !strings.Contains(spec.Prompt, "AGENT") {
		t.Error("the judge was not given both diffs")
	}
	if len(spec.OutputSchema) == 0 {
		t.Error("the verdict is asked for as structured output, not scraped out of prose")
	}
}

func TestNoJudgeWithoutAProvider(t *testing.T) {
	cfg := newWorkspace(t)
	if NewRubricer(runner.Deps{Config: cfg}, "") != nil {
		t.Error("a workspace with no provider has no judge, and --rubric is a no-op")
	}
	if NewRubricer(runner.Deps{}, "") != nil {
		t.Error("a judge without a workspace")
	}
}
