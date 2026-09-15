package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

// Rubric is the one judged part of a retro score: a model reading both
// diffs and saying whether they are the same change.
//
// Everything else a retro measures is arithmetic on two diffs, which is why
// this is off by default. A rubric verdict is an opinion, it costs a
// provider call per key, and it is the column to distrust first when a
// retro says something surprising.
type Rubric struct {
	SameRootCause bool `json:"sameRootCause"`
	SameFix       bool `json:"sameFix"`
	// Verdict is "equivalent", "partial" or "different".
	Verdict string `json:"verdict"`
	// Reasoning is at most 400 characters, which is what the prompt asks
	// for and what RubricFromJSON enforces.
	Reasoning string `json:"reasoning"`
}

// Verdicts are the three answers the rubric allows.
var Verdicts = []string{"equivalent", "partial", "different"}

// ReasoningMax is the ceiling on the rubric's prose. A judge that writes an
// essay is not adding information to a table column.
const ReasoningMax = 400

// Rubricer judges the agent's diff against the pull request's. It returns
// the verdict and what the call cost.
type Rubricer interface {
	Judge(ctx context.Context, prDiff, agentDiff string) (Rubric, float64, error)
}

// rubricSchema is the answer's shape, handed to the provider as structured
// output so the verdict does not have to be scraped out of prose.
var rubricSchema = []byte(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["sameRootCause", "sameFix", "verdict", "reasoning"],
  "properties": {
    "sameRootCause": {"type": "boolean", "description": "Whether both changes address the same underlying cause."},
    "sameFix": {"type": "boolean", "description": "Whether both changes make substantially the same change."},
    "verdict": {"type": "string", "enum": ["equivalent", "partial", "different"]},
    "reasoning": {"type": "string", "description": "At most 400 characters."}
  }
}`)

// diffChars caps how much of each diff the judge is shown. A pull request
// of half a megabyte is not a rubric question, and sending one would cost
// more than the rest of the retro put together.
const diffChars = 60000

// RubricPrompt is the question the judge is asked. It is fixed: a rubric
// whose wording moves between runs is not a measurement of anything.
func RubricPrompt(prDiff, agentDiff string) string {
	var b strings.Builder
	b.WriteString("You are comparing two fixes for the same bug.\n\n")
	b.WriteString("The first is the change a human engineer merged. The second is the change an agent produced, ")
	b.WriteString("working from the ticket alone, at the same commit, without seeing the first.\n\n")
	b.WriteString("Answer only with the JSON object the schema describes:\n")
	b.WriteString("- sameRootCause: do both changes address the same underlying cause?\n")
	b.WriteString("- sameFix: do both make substantially the same change, allowing for naming and layout?\n")
	b.WriteString("- verdict: \"equivalent\" when the agent's change would have done the job, ")
	b.WriteString("\"partial\" when it gets part of the way or fixes a symptom, \"different\" otherwise.\n")
	b.WriteString("- reasoning: at most 400 characters, naming the difference that decided the verdict.\n\n")
	b.WriteString("Judge the change, not the style. Do not run any tool; read the two diffs and answer.\n\n")
	b.WriteString("## The merged pull request\n\n```diff\n")
	b.WriteString(truncateDiff(prDiff))
	b.WriteString("\n```\n\n## The agent's change\n\n```diff\n")
	b.WriteString(truncateDiff(agentDiff))
	b.WriteString("\n```\n")
	return b.String()
}

func truncateDiff(s string) string {
	if len(s) <= diffChars {
		return s
	}
	return s[:diffChars] + "\n… truncated, the diff is longer than the rubric call carries …"
}

// RubricFromJSON parses and bounds a judge's answer: an unknown verdict
// becomes "different" rather than a column nobody can read, and prose over
// the ceiling is cut.
func RubricFromJSON(data []byte) (Rubric, error) {
	var r Rubric
	if err := json.Unmarshal(data, &r); err != nil {
		return Rubric{}, fmt.Errorf("eval: the rubric answer is not valid JSON: %w", err)
	}
	r.Verdict = strings.ToLower(strings.TrimSpace(r.Verdict))
	known := false
	for _, v := range Verdicts {
		if r.Verdict == v {
			known = true
			break
		}
	}
	if !known {
		return r, fmt.Errorf("eval: the rubric answered %q, which is not one of %s", r.Verdict, strings.Join(Verdicts, ", "))
	}
	r.Reasoning = strings.TrimSpace(r.Reasoning)
	if len(r.Reasoning) > ReasoningMax {
		r.Reasoning = strings.TrimSpace(r.Reasoning[:ReasoningMax])
	}
	return r, nil
}

// providerRubric asks the workspace's own provider. It is one session with
// no tools at all: the judge is given both diffs in the prompt and has
// nothing to read, which keeps the call to a single turn and keeps a judge
// from wandering into the repository and scoring what it finds there
// instead of what it was shown.
type providerRubric struct {
	deps  runner.Deps
	model string
}

// NewRubricer returns the judge for a workspace, or nil when there is no
// provider to ask.
func NewRubricer(deps runner.Deps, model string) Rubricer {
	if deps.Provider == nil || deps.Config == nil {
		return nil
	}
	if model == "" {
		model = deps.Config.Model
	}
	return providerRubric{deps: deps, model: model}
}

// rubricGrace is how long a judge that has answered is given to exit before
// it is cancelled.
const rubricGrace = 10 * time.Second

func (p providerRubric) Judge(ctx context.Context, prDiff, agentDiff string) (Rubric, float64, error) {
	spec := provider.SessionSpec{
		Cwd:          p.deps.Config.Root,
		Prompt:       RubricPrompt(prDiff, agentDiff),
		Model:        p.model,
		OutputSchema: rubricSchema,
		Policy: &provider.PermissionPolicy{
			Root: p.deps.Config.Root,
			Mode: provider.ModeTriage,
		},
		Mode: provider.ModeTriage,
		// One turn, and a ceiling low enough that a judge that starts
		// reading the repository stops before it costs more than the
		// runs it is judging.
		Budget: provider.Budget{MaxTurns: 2, MaxMinutes: 5},
		// No MCP servers: the judge reads two diffs out of its prompt.
		MCPStrict: true,
	}
	sess, err := p.deps.Provider.Start(ctx, spec)
	if err != nil {
		return Rubric{}, 0, fmt.Errorf("eval: start the rubric session: %w", err)
	}
	// Nothing further is coming, and a CLI reading stream-json holds its
	// stdout open until it is told so.
	_ = sess.CloseInput()
	defer time.AfterFunc(rubricGrace, sess.Cancel)

	var final json.RawMessage
	var cost float64
	for ev := range sess.Events() {
		if ev.CostUSD > cost {
			cost = ev.CostUSD
		}
		if ev.Kind == provider.EvFinal && ev.Final != nil {
			final = ev.Final
		}
	}
	res, waitErr := sess.Wait()
	if res.Usage.CostUSD > cost {
		cost = res.Usage.CostUSD
	}
	if final == nil {
		final = res.Final
	}
	if final == nil && strings.TrimSpace(res.Text) != "" {
		final = json.RawMessage(extractJSON(res.Text))
	}
	if final == nil {
		if waitErr != nil {
			return Rubric{}, cost, fmt.Errorf("eval: the rubric session gave no answer: %w", waitErr)
		}
		return Rubric{}, cost, fmt.Errorf("eval: the rubric session gave no answer")
	}
	r, err := RubricFromJSON(final)
	return r, cost, err
}

// extractJSON pulls the object out of a final message that came back as
// prose with a fenced block in it, which is what a provider without
// structured output returns.
func extractJSON(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return text
	}
	return text[start : end+1]
}
