package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/slack"
)

// TestSlackMCPPromptReadsTheThreadFirst: slack_read_thread is the first
// call, with the permalink's ts dotted and a DM's D… id passed as it is;
// slack_read_channel is the fallback, with a window one microsecond either
// side of the ts because its bounds are exclusive.
func TestSlackMCPPromptReadsTheThreadFirst(t *testing.T) {
	l, ok := slack.FindLink("https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059")
	if !ok {
		t.Fatal("not a link")
	}
	p := SlackMCPPrompt(l)
	thread := strings.Index(p, `mcp__slack__slack_read_thread with channel_id "D0FAKEDM01" and message_ts "1791100254.656059"`)
	channel := strings.Index(p, `mcp__slack__slack_read_channel with channel_id "D0FAKEDM01", oldest "1791100254.656058", latest "1791100254.656060" and limit 1`)
	if thread < 0 || channel < 0 || thread > channel {
		t.Fatalf("read_thread first, then read_channel with the widened window:\n%s", p)
	}
	if !strings.Contains(p, "Only if that call returns an error") || !strings.Contains(p, "direct-message id starting with D") {
		t.Errorf("the fallback is conditional and a DM id is valid:\n%s", p)
	}

	// A link into a thread reads the thread by its parent's ts.
	l, _ = slack.FindLink("https://acme.slack.com/archives/C0FAKE0001/p1791100299000100?thread_ts=1791100254.656059&cid=C0FAKE0001")
	if p := SlackMCPPrompt(l); !strings.Contains(p, `message_ts "1791100254.656059"`) || !strings.Contains(p, `oldest "1791100299.000099", latest "1791100299.000101"`) {
		t.Errorf("thread link:\n%s", p)
	}
	if slackMCPMaxTurns != 6 {
		t.Errorf("max turns %d, want 6", slackMCPMaxTurns)
	}
}

func TestMCPAuthor(t *testing.T) {
	for in, want := range map[string]string{
		"Sam Example <sam@example.com> (U0FAKE001)": "Sam Example",
		"سارة <s@example.com>":                      "سارة",
		"sam":                                       "sam",
		"  Sam (U0FAKE001) ":                        "Sam",
	} {
		if got := mcpAuthor(in); got != want {
			t.Errorf("mcpAuthor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTSWindow(t *testing.T) {
	for _, c := range []struct{ ts, oldest, latest string }{
		{"1791100254.656059", "1791100254.656058", "1791100254.656060"},
		{"1791100254.000000", "1791100253.999999", "1791100254.000001"},
		{"1791100254.999999", "1791100254.999998", "1791100255.000000"},
		{"nonsense", "nonsense", "nonsense"},
	} {
		if o, l := tsWindow(c.ts); o != c.oldest || l != c.latest {
			t.Errorf("tsWindow(%s) = %s, %s", c.ts, o, l)
		}
	}
}

// TestSlackMCPEmptyReadingSaysWhy: a reading that answers with no messages
// is an error, and the error carries what the model said (cut to 200
// characters) instead of "found no message".
func TestSlackMCPEmptyReadingSaysWhy(t *testing.T) {
	slackCLI(t)
	long := "The slack_read_thread call failed with channel_not_found for this id. " + strings.Repeat("x", 300)
	prov := &stubProvider{script: replay(
		provider.Event{Kind: provider.EvAssistantText, Text: long},
		finalEvent(`{"messages":[],"refs":[]}`),
	)}
	sr, _ := SlackReaderFor(slackMCPConfig(t), prov)
	r := newTestResolver(t, nil, nil)
	r.slack = sr
	in := resolveOK(t, r, "https://acme.slack.com/archives/D0FAKEDM02/p1712345678901234")
	if in.Key != "" || !strings.Contains(in.Reason, "returned no messages: The slack_read_thread call failed with channel_not_found") {
		t.Fatalf("reason %q", in.Reason)
	}
	if strings.Contains(in.Reason, "found no message") || !strings.HasSuffix(in.Reason, "x…") || strings.Count(in.Reason, "x") > 205 {
		t.Errorf("the reason carries the first 200 characters of what the model said: %q", in.Reason)
	}

	// With nothing said, the last tool result is the reason.
	prov = &stubProvider{script: replay(
		provider.Event{Kind: provider.EvToolFinished, Text: "Tool permission request failed: AbortError: Stream closed"},
		finalEvent(`{"messages":[],"refs":[]}`),
	)}
	sr, _ = SlackReaderFor(slackMCPConfig(t), prov)
	r.slack = sr
	in = resolveOK(t, r, "https://acme.slack.com/archives/D0FAKEDM03/p1712345678901234")
	if !strings.Contains(in.Reason, "the last tool result: Tool permission request failed") {
		t.Fatalf("reason %q", in.Reason)
	}
}

// inputWatch is a session that records whether its input was closed while
// a tool call was still in flight: the CLI answers permission prompts over
// stdin, so closing it early fails every Slack call.
type inputWatch struct {
	*stubSession
	mu     sync.Mutex
	closed bool
}

func (s *inputWatch) CloseInput() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *inputWatch) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

type inputWatchProvider struct {
	stubProvider
	session      *inputWatch
	closedAtTool bool
}

func (p *inputWatchProvider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	inner := &stubSession{events: make(chan provider.Event), cancelled: make(chan struct{})}
	p.session = &inputWatch{stubSession: inner}
	go func() {
		defer inner.finish()
		inner.emit(provider.Event{Kind: provider.EvToolStarted, Tool: "mcp__slack__slack_read_thread"})
		p.closedAtTool = p.session.isClosed()
		inner.emit(finalEvent(`{"messages":[{"author":"sam","time":"","text":"hello"}],"refs":[]}`))
	}()
	return p.session, nil
}

func TestSlackMCPKeepsInputOpenUntilTheAnswer(t *testing.T) {
	slackCLI(t)
	prov := &inputWatchProvider{}
	sr, err := SlackReaderFor(slackMCPConfig(t), prov)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := slack.FindLink("https://acme.slack.com/archives/D0FAKEDM04/p1712345678901234")
	if _, err := sr.Read(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if prov.closedAtTool {
		t.Error("input was closed before the tool call: its permission prompt would fail")
	}
	if !prov.session.isClosed() {
		t.Error("input was never closed after the answer")
	}
}

// TestSlackMCPWritesATranscript: every reading leaves a JSON-lines record
// of the request, the events and the outcome, and a week-old one is swept.
func TestSlackMCPWritesATranscript(t *testing.T) {
	slackCLI(t)
	dir := t.TempDir()
	old := filepath.Join(dir, "20200101T000000.000000000Z.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	prev := intakeLogDir
	intakeLogDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { intakeLogDir = prev })

	prov, _ := recordingSlackProvider(`{"messages":[{"author":"sam","time":"","text":"hello"}],"refs":[]}`)
	sr, _ := SlackReaderFor(slackMCPConfig(t), prov)
	l, _ := slack.FindLink("https://acme.slack.com/archives/D0FAKEDM05/p1712345678901234")
	if _, err := sr.Read(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("a transcript older than a week was kept")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("transcripts %v", files)
	}
	data, _ := os.ReadFile(files[0])
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 3 || !strings.Contains(lines[0], `"record":"request"`) || !strings.Contains(lines[0], "D0FAKEDM05") ||
		!strings.Contains(lines[len(lines)-1], `"record":"outcome"`) || !strings.Contains(string(data), `"kind":"final"`) {
		t.Fatalf("transcript:\n%s", data)
	}
}
