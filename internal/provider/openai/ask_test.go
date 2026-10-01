package openai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// TestLoopStopsOnAQuestion: Sirdar's own loop answers the asked call as
// waiting, closes out the rest of the batch, writes the transcript a resume
// reads back, and asks the model nothing more.
func TestLoopStopsOnAQuestion(t *testing.T) {
	root := t.TempDir()
	runDir := t.TempDir()
	batch := reply(map[string]any{
		"role": "assistant",
		"tool_calls": []map[string]any{
			{"id": "c1", "type": "function", "function": map[string]string{"name": "bash", "arguments": `{"command":"rg -n refund src"}`}},
			{"id": "c2", "type": "function", "function": map[string]string{"name": "list_dir", "arguments": `{"path":"."}`}},
		},
	}, "tool_calls", 100, 10)
	cs := newChatServer(t, scripted(batch, toolCallReply("c3", submitNoteTool, noteJSON, 100, 10)))
	sess := newSession(t, cs, LoopConfig{}, provider.SessionSpec{
		Cwd:    root,
		RunDir: runDir,
		Budget: provider.Budget{MaxTurns: 5},
		Policy: &provider.PermissionPolicy{BashAllow: []string{"cat *"}, Root: root, Ask: true},
	})
	events := drain(t, sess)
	if got := summary(events); len(got) < 2 || got[1] != "permission:bash:ask" {
		t.Fatalf("events = %v", got)
	}
	for _, ev := range events {
		if ev.Kind == provider.EvFinal || ev.Kind == provider.EvToolStarted {
			t.Errorf("the loop went on after the question: %v", summary(events))
		}
		if ev.Kind == provider.EvPermission && (ev.Ask == nil || ev.Ask.Summary != "rg -n refund src") {
			t.Errorf("ask = %+v", ev.Ask)
		}
	}
	if n := len(cs.captured()); n != 1 {
		t.Errorf("the model was asked %d times, want 1", n)
	}
	transcript, err := os.ReadFile(filepath.Join(runDir, "transcript.json"))
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	for _, want := range []string{"waiting on the operator", "skipped: the run is waiting on the operator"} {
		if !strings.Contains(string(transcript), want) {
			t.Errorf("transcript lacks %q", want)
		}
	}
}

// TestLoopRunsAGrantedCall: with the operator's grant the same call runs.
func TestLoopRunsAGrantedCall(t *testing.T) {
	root := t.TempDir()
	ask := provider.PermissionAsk{Kind: provider.AskBash, Tool: "bash", Summary: "ls", Patterns: []string{"ls *"}}
	cs := newChatServer(t, scripted(
		toolCallReply("c1", "bash", `{"command":"ls"}`, 100, 10),
		toolCallReply("c2", submitNoteTool, noteJSON, 100, 10),
	))
	sess := newSession(t, cs, LoopConfig{}, provider.SessionSpec{
		Cwd:    root,
		Budget: provider.Budget{MaxTurns: 5},
		Policy: &provider.PermissionPolicy{BashAllow: []string{"cat *"}, Root: root, Ask: true,
			Grants: provider.NewGrants(provider.GrantFor(ask, provider.VerdictAllow, ""))},
	})
	got := summary(drain(t, sess))
	if len(got) < 3 || got[1] != "permission:bash:allow" || got[2] != "tool_started:bash" {
		t.Fatalf("events = %v", got)
	}
}
