package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// The workspace, the stub sources and the stub provider below are the
// shape internal/run's own tests use, copied here so this package can run
// a real Runner end to end without depending on another package's test
// fixtures.

const configYAML = `workspace: test
provider: claude
billing: subscription
notes:
  dir: notes
budget:
  maxTurns: 60
  maxMinutes: 25
  maxUsd: 5
concurrency: 1
playbooks: .sirdar/playbooks
`

const triageDoc = `{
  "ticket": {"key":"OMNI-1","title":"Export fails","trackerUrl":"https://t/OMNI-1","helpdeskId":"555","helpdeskUrl":"https://h/555","priority":"high","service":"omni","customer":"شركة","customerId":"4561"},
  "title": "Export fails for large orders",
  "complaint": "The export fails for large orders. It has happened every day this week.",
  "timeline": [{"at":"2026-09-10T08:30:00+03:00","role":"customer","summary":"Reported the export failing."}],
  "reproSteps": ["Request a CSV export for a 600-line order."],
  "rootCause": {"hypothesis":"The export job times out.","confidence":"medium","evidence":[{"source":"logs","query":"service:export level:error","finding":"Timeout after 30s."}],"codeRefs":["internal/export/csv.go:42"]},
  "blastRadius": "Any order above 500 line items.",
  "classification": "code",
  "proposedFix": {"description":"Stream the export.","files":["internal/export/csv.go"],"remediationSql":"","risks":"none"},
  "openQuestions": []
}`

func sampleBundle() ticket.Bundle {
	t0 := time.Date(2026, 9, 10, 8, 30, 0, 0, time.FixedZone("KSA", 3*3600))
	return ticket.Bundle{
		Tracker: &ticket.TrackerTicket{
			Key: "OMNI-1", Title: "Export fails", HelpdeskRef: "555",
			URL: "https://t/OMNI-1", Priority: "high", Status: "open",
			Assignee: "me", CreatedAt: t0, UpdatedAt: t0,
		},
		Helpdesk: &ticket.HelpdeskTicket{
			ID: "555", Subject: "تصدير", Customer: "شركة", CustomerID: "4561",
			URL: "https://h/555", Priority: "high", CreatedAt: t0,
		},
		Thread: ticket.Thread{
			{At: t0, Author: "Customer", Role: ticket.RoleCustomer, Text: "لا يعمل التصدير"},
		},
	}
}

// newWorkspace writes a real .sirdar workspace into a temp dir and returns
// its root, so every default is the one a real run sees.
func newWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sirdar", "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "playbooks", "00-test.md"), []byte("# Exports\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(root); err != nil {
		t.Fatal(err)
	}
	return root
}

// newRegistry returns a registry file in its own temp dir holding root.
func newRegistry(t *testing.T, roots ...string) *Registry {
	t.Helper()
	reg := &Registry{Path: filepath.Join(t.TempDir(), "workspaces.json")}
	for _, root := range roots {
		if _, err := reg.Add(root); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

// --- stub sources -----------------------------------------------------

type stubTracker struct {
	list []ticket.TrackerTicket
	err  error
}

func (s stubTracker) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	tt := *sampleBundle().Tracker
	tt.Key = key
	return tt, nil
}

func (s stubTracker) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.list, nil
}

type stubHelpdesk struct{}

func (stubHelpdesk) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	return *sampleBundle().Helpdesk, nil
}

func (stubHelpdesk) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	return sampleBundle().Thread, nil
}

func (stubHelpdesk) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	return nil, nil
}

// --- stub provider ----------------------------------------------------

type stubSession struct {
	events    chan provider.Event
	cancelled chan struct{}
	handle    string
	result    provider.Result

	cancelOnce sync.Once
	finishOnce sync.Once
}

func (s *stubSession) Events() <-chan provider.Event                   { return s.events }
func (s *stubSession) Send(ctx context.Context, userText string) error { return nil }
func (s *stubSession) Wait() (provider.Result, error)                  { return s.result, nil }
func (s *stubSession) Handle() string                                  { return s.handle }

func (s *stubSession) Cancel() {
	s.cancelOnce.Do(func() { close(s.cancelled) })
}

// emit delivers one event unless the runner has already cancelled the
// session; it reports whether the script should keep going.
func (s *stubSession) emit(ev provider.Event) bool {
	select {
	case s.events <- ev:
		return true
	case <-s.cancelled:
		return false
	}
}

func (s *stubSession) finish() { s.finishOnce.Do(func() { close(s.events) }) }

type stubProvider struct {
	script func(spec provider.SessionSpec, s *stubSession)

	mu       sync.Mutex
	sessions []*stubSession
}

// session returns the i-th session this provider started.
func (p *stubProvider) session(t *testing.T, i int) *stubSession {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= len(p.sessions) {
		t.Fatalf("session %d of %d was never started", i, len(p.sessions))
	}
	return p.sessions[i]
}

func (p *stubProvider) Name() string { return "claude" }

func (p *stubProvider) Doctor(ctx context.Context, binary string) []provider.Check { return nil }

func (p *stubProvider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	s := &stubSession{
		events:    make(chan provider.Event),
		cancelled: make(chan struct{}),
		handle:    "handle-abc",
	}
	s.result.Handle = s.handle
	p.mu.Lock()
	p.sessions = append(p.sessions, s)
	p.mu.Unlock()

	go p.script(spec, s)
	return s, nil
}

// replay emits events in order and then ends the session.
func replay(events ...provider.Event) func(provider.SessionSpec, *stubSession) {
	return func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		for _, ev := range events {
			if !s.emit(ev) {
				return
			}
		}
	}
}

// block starts a session that produces nothing and only ends when the
// runner cancels it. started is signalled once the session is under way.
func block(started chan struct{}) func(provider.SessionSpec, *stubSession) {
	return func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		select {
		case started <- struct{}{}:
		default:
		}
		<-s.cancelled
	}
}

func finalEvent(doc string) provider.Event {
	return provider.Event{Kind: provider.EvFinal, Final: json.RawMessage(doc), Raw: json.RawMessage(`{"type":"result"}`)}
}

// cancelled reports whether the runner has cancelled this session.
func (s *stubSession) wasCancelled() bool {
	select {
	case <-s.cancelled:
		return true
	default:
		return false
	}
}
