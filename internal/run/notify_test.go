package run

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/notify"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// webhook is a chat endpoint standing in for Slack: it keeps every payload
// it was posted and answers with the scripted status.
type webhook struct {
	srv    *httptest.Server
	status int

	mu    sync.Mutex
	posts [][]byte
}

func newWebhook(t *testing.T, status int) *webhook {
	t.Helper()
	w := &webhook{status: status}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var buf [4096]byte
		n, _ := r.Body.Read(buf[:])
		w.mu.Lock()
		w.posts = append(w.posts, append([]byte(nil), buf[:n]...))
		w.mu.Unlock()
		if w.status >= 500 || w.status == http.StatusTooManyRequests {
			rw.Header().Set("Retry-After", "0")
		}
		rw.WriteHeader(w.status)
	}))
	t.Cleanup(w.srv.Close)
	return w
}

func (w *webhook) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.posts)
}

func (w *webhook) payload(t *testing.T, i int) string {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if i >= len(w.posts) {
		t.Fatalf("the webhook was posted to %d times", len(w.posts))
	}
	return string(w.posts[i])
}

// notifyingRunner is the happy-path runner with the workspace's notify
// block wired to hook.
func notifyingRunner(t *testing.T, hook *webhook, block string) (*config.Config, *Runner) {
	t.Helper()
	cfg := newWorkspaceWith(t, configYAML+block)
	creds := config.Resolver{Env: func(name string) (string, bool) {
		if name == "SLACK_WEBHOOK" {
			return hook.srv.URL + "/services/T000/B000/xoxb-SECRET", true
		}
		return "", false
	}}
	n, err := notify.FromConfig(cfg, creds)
	if err != nil {
		t.Fatal(err)
	}
	p := &stubProvider{script: replay(
		provider.Event{Kind: provider.EvUsage, Turns: 3, InputTok: 100, OutputTok: 20, CostUSD: 0.42},
		finalEvent(triageDoc),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.Notifier = n
	return cfg, r
}

const notifyOnCompleted = `notify:
  on: [completed]
  slack:
    webhookUrl: env:SLACK_WEBHOOK
`

// TestTriageNotifiesOnCompletion: a finished run posts one message, and it
// carries the run's metadata and note path — not a line of the note.
func TestTriageNotifiesOnCompletion(t *testing.T) {
	hook := newWebhook(t, http.StatusOK)
	cfg, r := notifyingRunner(t, hook, notifyOnCompleted)

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
	if hook.count() != 1 {
		t.Fatalf("%d posts, want 1", hook.count())
	}

	body := hook.payload(t, 0)
	for _, want := range []string{"[OMNI-1] completed", "medium", "code", "omni", "$0.42", "OMNI-1 export-fails-for-large-orders.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("the message is missing %q:\n%s", want, body)
		}
	}
	// The note's prose stays on disk, and so does the ticket title while
	// includeTitle is off.
	for _, leak := range []string{"The export fails for large orders", "Export fails", "The export job times out"} {
		if strings.Contains(body, leak) {
			t.Errorf("the message carried %q:\n%s", leak, body)
		}
	}
	if len(outs[0].State.Warnings) != 0 {
		t.Errorf("warnings %v", outs[0].State.Warnings)
	}
	if _, err := (store.Run{Dir: runDir(t, cfg, outs[0])}).ReadState(); err != nil {
		t.Fatal(err)
	}
}

// TestTriageDoesNotNotifyForAStateTheWorkspaceDidNotAskAbout.
func TestTriageDoesNotNotifyForAStateTheWorkspaceDidNotAskAbout(t *testing.T) {
	hook := newWebhook(t, http.StatusOK)
	_, r := notifyingRunner(t, hook, `notify:
  on: [failed, over_budget]
  slack:
    webhookUrl: env:SLACK_WEBHOOK
`)
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q", outs[0].State.Status)
	}
	if hook.count() != 0 {
		t.Fatalf("%d posts, want none:\n%s", hook.count(), hook.payload(t, 0))
	}
}

// TestNoNotifyFlagSilencesTheRun.
func TestNoNotifyFlagSilencesTheRun(t *testing.T) {
	hook := newWebhook(t, http.StatusOK)
	_, r := notifyingRunner(t, hook, notifyOnCompleted)
	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{NoNotify: true}); err != nil {
		t.Fatal(err)
	}
	if hook.count() != 0 {
		t.Fatalf("%d posts, want none", hook.count())
	}
}

// TestDryRunNotifiesNobody: a dry run writes a bundle and a prompt, and
// "completed" in the channel would claim a triage that never happened.
func TestDryRunNotifiesNobody(t *testing.T) {
	hook := newWebhook(t, http.StatusOK)
	_, r := notifyingRunner(t, hook, notifyOnCompleted)
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Reason != "dry-run" {
		t.Fatalf("reason %q", outs[0].State.Reason)
	}
	if hook.count() != 0 {
		t.Fatalf("%d posts, want none", hook.count())
	}
}

// TestIncludeTitleSendsTheTicketTitle: opting in is what puts the subject
// line in the channel.
func TestIncludeTitleSendsTheTicketTitle(t *testing.T) {
	hook := newWebhook(t, http.StatusOK)
	_, r := notifyingRunner(t, hook, `notify:
  on: [completed]
  includeTitle: true
  slack:
    webhookUrl: env:SLACK_WEBHOOK
`)
	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}
	if body := hook.payload(t, 0); !strings.Contains(body, "Export fails") {
		t.Fatalf("the title was not sent:\n%s", body)
	}
}

// TestNotifyFailureIsAWarningNotAFailedRun: the note is already on disk
// when the post goes out, so a webhook that is down cannot change what the
// run was worth. The failure lands in state.json, without the credential.
func TestNotifyFailureIsAWarningNotAFailedRun(t *testing.T) {
	hook := newWebhook(t, http.StatusInternalServerError)
	cfg, r := notifyingRunner(t, hook, notifyOnCompleted)

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("a failed notification failed the run: %q %q", out.State.Status, out.State.Reason)
	}
	if hook.count() != 2 {
		t.Fatalf("%d posts, want 2 (one retry)", hook.count())
	}

	// The warning is on the state that was written, not only on the
	// in-memory copy.
	state, err := (store.Run{Dir: runDir(t, cfg, out)}).ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Warnings) != 1 || !strings.Contains(state.Warnings[0], "notify:") {
		t.Fatalf("warnings %v", state.Warnings)
	}
	if strings.Contains(state.Warnings[0], "xoxb-SECRET") {
		t.Fatalf("the warning leaked the webhook URL: %s", state.Warnings[0])
	}
	if !strings.Contains(state.Warnings[0], "500") {
		t.Fatalf("the warning does not say what happened: %s", state.Warnings[0])
	}
}

// TestNotifyCredentialsAreStrippedFromTheAgent: a session that can run
// shell commands must not be able to read the team's webhook out of its
// environment and post as Sirdar.
func TestNotifyCredentialsAreStrippedFromTheAgent(t *testing.T) {
	cfg := newWorkspaceWith(t, configYAML+`notify:
  slack:
    webhookUrl: env:SLACK_WEBHOOK
  teams:
    webhookUrl: env:TEAMS_WEBHOOK
  generic:
    - url: https://hooks.example.com/sirdar
      headers:
        Authorization: env:HOOK_TOKEN
      secret: env:HOOK_SECRET
`)
	names := credentialEnvNames(cfg)
	for _, want := range []string{"SLACK_WEBHOOK", "TEAMS_WEBHOOK", "HOOK_TOKEN", "HOOK_SECRET"} {
		if !names[want] {
			t.Errorf("%s is left in the agent's environment", want)
		}
	}

	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.Env = []string{"PATH=/usr/bin", "SLACK_WEBHOOK=https://hooks.slack.com/services/x", "HOOK_SECRET=s3cret"}
	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range p.spec(0).Env {
		if strings.HasPrefix(entry, "SLACK_WEBHOOK=") || strings.HasPrefix(entry, "HOOK_SECRET=") {
			t.Errorf("the agent inherited %q", entry)
		}
	}
}

// TestEventCarriesTheRunsFacts pins what the runner puts in the event, so a
// receiver parsing the generic JSON has the fields it was promised.
func TestEventCarriesTheRunsFacts(t *testing.T) {
	// A generic hook's URL is a plain URL rather than a credential ref,
	// so it is wired straight onto the runner.
	hook2 := newWebhook(t, http.StatusOK)
	cfg := newWorkspaceWith(t, configYAML)
	p := &stubProvider{script: replay(
		provider.Event{Kind: provider.EvUsage, Turns: 7, CostUSD: 1.25},
		finalEvent(triageDoc),
	)}
	runner := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	runner.Notifier = &notify.Router{Notifier: &notify.Generic{URL: hook2.srv.URL + "/hook"}}
	outs, err := runner.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	var ev notify.Event
	if err := json.Unmarshal([]byte(hook2.payload(t, 0)), &ev); err != nil {
		t.Fatalf("payload: %v\n%s", err, hook2.payload(t, 0))
	}
	if ev.Kind != "triage" || ev.Key != "OMNI-1" || ev.Status != "completed" {
		t.Errorf("identity %+v", ev)
	}
	if ev.Confidence != "medium" || ev.Classification != "code" || ev.Service != "omni" {
		t.Errorf("verdict %+v", ev)
	}
	if ev.RunID != outs[0].State.RunID || ev.Workspace != "test" {
		t.Errorf("run %+v", ev)
	}
	if ev.TrackerURL != "https://t/OMNI-1" || ev.HelpdeskURL != "https://h/555" {
		t.Errorf("links %+v", ev)
	}
	if ev.Turns != 7 || ev.CostUSD != 1.25 {
		t.Errorf("usage %+v", ev)
	}
	if !strings.HasSuffix(ev.NotePath, "OMNI-1 export-fails-for-large-orders.md") {
		t.Errorf("note path %q", ev.NotePath)
	}
	if ev.Title != "" {
		t.Errorf("the title was sent without includeTitle: %q", ev.Title)
	}
}
