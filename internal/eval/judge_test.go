package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// judgeSession plays back a fixed set of events and a fixed result, which
// is all JudgeOnce reads from a session.
type judgeSession struct {
	events chan provider.Event
	result provider.Result
}

func (s *judgeSession) Events() <-chan provider.Event               { return s.events }
func (s *judgeSession) Send(ctx context.Context, text string) error { return nil }
func (s *judgeSession) CloseInput() error                           { return nil }
func (s *judgeSession) Handle() string                              { return "j1" }
func (s *judgeSession) Cancel()                                     {}
func (s *judgeSession) Wait() (provider.Result, error)              { return s.result, nil }

// judgeProvider records the spec it was started with and answers with
// whatever events and result the test gave it.
type judgeProvider struct {
	events []provider.Event
	result provider.Result
	specs  chan provider.SessionSpec
}

func (p *judgeProvider) Name() string                                               { return "claude" }
func (p *judgeProvider) Doctor(ctx context.Context, binary string) []provider.Check { return nil }

func (p *judgeProvider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	select {
	case p.specs <- spec:
	default:
	}
	s := &judgeSession{events: make(chan provider.Event, len(p.events)), result: p.result}
	for _, ev := range p.events {
		s.events <- ev
	}
	close(s.events)
	return s, nil
}

func TestJudgeOnceReturnsFinal(t *testing.T) {
	cfg := newWorkspace(t)
	p := &judgeProvider{
		events: []provider.Event{
			{Kind: provider.EvUsage, CostUSD: 0.03},
			{Kind: provider.EvFinal, Final: json.RawMessage(`{"verdict":"confirmed"}`)},
		},
		specs: make(chan provider.SessionSpec, 1),
	}
	schema := []byte(`{"type":"object"}`)
	raw, cost, err := JudgeOnce(context.Background(), newDeps(t, cfg, p), "", "PROMPT", schema)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"verdict":"confirmed"}` {
		t.Errorf("raw answer %s", raw)
	}
	if cost != 0.03 {
		t.Errorf("cost %v", cost)
	}
	spec := <-p.specs
	if !spec.MCPStrict {
		t.Error("the judge is given MCP servers to read")
	}
	if spec.Budget.MaxTurns != 2 {
		t.Errorf("the judge's turn ceiling is %d", spec.Budget.MaxTurns)
	}
	if !bytes.Equal(spec.OutputSchema, schema) {
		t.Errorf("output schema %s", spec.OutputSchema)
	}
	if spec.Model != cfg.Model {
		t.Errorf("an empty model is the workspace's: got %q, want %q", spec.Model, cfg.Model)
	}
	if spec.Prompt != "PROMPT" {
		t.Errorf("prompt %q", spec.Prompt)
	}
}

func TestJudgeOnceExtractsFromProse(t *testing.T) {
	cfg := newWorkspace(t)
	p := &judgeProvider{
		result: provider.Result{Text: `here {"a":1} done`},
		specs:  make(chan provider.SessionSpec, 1),
	}
	raw, _, err := JudgeOnce(context.Background(), newDeps(t, cfg, p), "sonnet", "PROMPT", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"a":1}` {
		t.Errorf("raw answer %s", raw)
	}
	if spec := <-p.specs; spec.Model != "sonnet" {
		t.Errorf("model %q", spec.Model)
	}
}

func TestJudgeOnceNoAnswer(t *testing.T) {
	cfg := newWorkspace(t)
	p := &judgeProvider{specs: make(chan provider.SessionSpec, 1)}
	_, _, err := JudgeOnce(context.Background(), newDeps(t, cfg, p), "", "PROMPT", nil)
	if err == nil || !strings.Contains(err.Error(), "gave no answer") {
		t.Fatalf("got %v", err)
	}
}

func TestJudgeOnceWithoutAProvider(t *testing.T) {
	cfg := newWorkspace(t)
	_, _, err := JudgeOnce(context.Background(), newDeps(t, cfg, nil), "", "PROMPT", nil)
	if err == nil || err.Error() != "eval: no provider to judge with" {
		t.Fatalf("got %v", err)
	}
}

func TestCheckPRURL(t *testing.T) {
	if err := CheckPRURL("https://github.com/o/r/pull/1"); err != nil {
		t.Errorf("a pull request URL is refused: %v", err)
	}
	for _, bad := range []string{"-x", "github.com/o/r/pull/1", "ftp://h/x"} {
		if err := CheckPRURL(bad); err == nil {
			t.Errorf("%q is accepted", bad)
		}
	}
}

// No URLs is nothing to read, and must not need gh: a key with no pull
// request is asked about on machines that have never installed it.
func TestPullRequestFilesWithNoURLsNeedsNoGh(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, urls := range [][]string{nil, {}} {
		set, err := PullRequestFiles(context.Background(), urls)
		if err != nil {
			t.Fatalf("%v: %v", urls, err)
		}
		if len(set.Files) != 0 || len(set.PRs) != 0 || len(set.Diff) != 0 {
			t.Errorf("%v read %+v", urls, set)
		}
	}
}

func TestCheckPRURLNamesNoFlag(t *testing.T) {
	err := CheckPRURL("-x")
	if err == nil || err.Error() != `eval: "-x" is not a pull request URL; it must be a full https:// link` {
		t.Fatalf("got %v", err)
	}
}
