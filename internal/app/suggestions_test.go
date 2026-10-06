package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// suggestionsNow is the clock every suggestions test reads, so the
// fallback's date is known.
var suggestionsNow = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

// suggestionService is a service over one workspace whose playbooks are
// the files given, with a builder that hands the judge a configuration
// and nothing else: every test stubs rememberJudge, so no provider is
// started.
func suggestionService(t *testing.T, files map[string]string) (*Service, string, string) {
	t.Helper()
	root := newWorkspace(t)
	dir := filepath.Join(root, ".sirdar", "playbooks")
	if err := os.Remove(filepath.Join(dir, "00-test.md")); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	build := func(cfg *config.Config, _, _ string, _ io.Writer) (runner.Deps, func(), error) {
		return runner.Deps{Config: cfg}, func() {}, nil
	}
	svc := New(newRegistry(t, root), build, Options{Now: func() time.Time { return suggestionsNow }})
	return svc, WorkspaceID(root), root
}

// stubJudge replaces rememberJudge for one test with a function answering
// raw, or failing with err, and records the prompt it was given.
func stubJudge(t *testing.T, raw string, err error) *string {
	t.Helper()
	var prompt string
	old := rememberJudge
	rememberJudge = func(ctx context.Context, deps runner.Deps, model, p string, schema []byte) (json.RawMessage, float64, error) {
		prompt = p
		if _, ok := ctx.Deadline(); !ok {
			t.Error("the judge was called without a deadline")
		}
		return json.RawMessage(raw), 0.02, err
	}
	t.Cleanup(func() { rememberJudge = old })
	return &prompt
}

// writeRememberRun puts a run on disk with the given instruction and
// events.jsonl lines, and answers with its id.
func writeRememberRun(t *testing.T, root string, st store.State, events ...string) string {
	t.Helper()
	runID := "20261006T080000Z-aaaa"
	rn, err := store.CreateID(root, "OMNI-1", runID)
	if err != nil {
		t.Fatal(err)
	}
	st.RunID, st.Key, st.Kind, st.Status = runID, "OMNI-1", store.KindTriage, store.StatusCompleted
	st.StartedAt, st.UpdatedAt = suggestionsNow, suggestionsNow
	if err := rn.WriteState(st); err != nil {
		t.Fatal(err)
	}
	if len(events) > 0 {
		body := strings.Join(events, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(rn.Dir, "events.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return runID
}

// steerRunEvents is a run whose third event is the operator's steer, after
// an assistant reply streamed in two deltas.
var steerRunEvents = []string{
	`{"t":"2026-10-06T08:00:00Z","kind":"system","payload":{"model":"sonnet"}}`,
	`{"t":"2026-10-06T08:00:01Z","kind":"assistant_text","payload":{"text":"The total is off by ","delta":true}}`,
	`{"t":"2026-10-06T08:00:02Z","kind":"assistant_text","payload":{"text":"one fils.","delta":true}}`,
	`{"t":"2026-10-06T08:00:03Z","kind":"steer","payload":{"text":"Check the tax rounding\nbefore the PSP."}}`,
}

const ordersPlaybook = "# Orders\n\nLede.\n"

func TestRememberDrafts(t *testing.T) {
	svc, ws, root := suggestionService(t, map[string]string{"20-orders.md": ordersPlaybook})
	// The steer is the fourth line: drop the system line so it sits at 3.
	runID := writeRememberRun(t, root, store.State{}, steerRunEvents[1:]...)
	prompt := stubJudge(t, `{"playbook":"20-orders.md","addition":"- Orders: check the tax rounding before the PSP.","reason":"repeated"}`, nil)

	got, err := svc.remember(context.Background(), ws, runID, 3)
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if got.Playbook != "20-orders.md" || got.Addition != "- Orders: check the tax rounding before the PSP." ||
		got.Reason != "repeated" || got.Source != "remember" || got.Key != "OMNI-1" || got.RunID != runID {
		t.Fatalf("suggestion %+v", got)
	}
	if got.ID != store.SuggestionID(got.Playbook, got.Addition) || got.CostUSD != 0.02 {
		t.Errorf("id %q cost %v", got.ID, got.CostUSD)
	}
	for _, want := range []string{"Check the tax rounding", "The total is off by one fils.", "20-orders.md", "Orders"} {
		if !strings.Contains(*prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, *prompt)
		}
	}
	pending, err := svc.suggestions(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != got.ID || pending[0].Source != "remember" {
		t.Fatalf("pending %+v", pending)
	}

	// The same message remembered again is the suggestion already queued.
	again, err := svc.remember(context.Background(), ws, runID, 3)
	if err != nil || again.ID != got.ID {
		t.Fatalf("again %+v %v", again, err)
	}
	lines, _ := store.ReadSuggestions(root)
	if len(lines) != 1 {
		t.Errorf("%d lines, want the one proposal", len(lines))
	}
}

func TestRememberFallback(t *testing.T) {
	svc, ws, root := suggestionService(t, map[string]string{"20-orders.md": ordersPlaybook})
	runID := writeRememberRun(t, root, store.State{}, steerRunEvents...)
	stubJudge(t, "", errors.New("provider is down"))

	got, err := svc.remember(context.Background(), ws, runID, 4)
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if got.Playbook != "90-corrections.md" {
		t.Errorf("playbook %q", got.Playbook)
	}
	if got.Addition != "- 2026-10-06 OMNI-1: Check the tax rounding before the PSP." {
		t.Errorf("addition %q", got.Addition)
	}
	if got.Reason != "the suggestion could not be drafted; kept the message as written" || got.CostUSD != 0.02 {
		t.Errorf("reason %q cost %v", got.Reason, got.CostUSD)
	}
}

func TestRememberBadPlaybook(t *testing.T) {
	svc, ws, root := suggestionService(t, map[string]string{"20-orders.md": ordersPlaybook})
	runID := writeRememberRun(t, root, store.State{}, steerRunEvents...)
	stubJudge(t, `{"playbook":"../x.md","addition":"- Rule.","reason":"r"}`, nil)

	got, err := svc.remember(context.Background(), ws, runID, 4)
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if got.Playbook != "90-corrections.md" || !strings.HasPrefix(got.Addition, "- 2026-10-06 OMNI-1: ") {
		t.Fatalf("suggestion %+v, want the fallback", got)
	}
}

func TestRememberInstruction(t *testing.T) {
	svc, ws, root := suggestionService(t, nil)
	runID := writeRememberRun(t, root, store.State{Instruction: "Look at the PSP logs first."})
	prompt := stubJudge(t, `{"playbook":"40-psp.md","addition":"- Read the PSP logs before the database.","reason":"faster"}`, nil)

	got, err := svc.remember(context.Background(), ws, runID, 0)
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if got.Playbook != "40-psp.md" || got.Addition != "- Read the PSP logs before the database." {
		t.Fatalf("suggestion %+v", got)
	}
	if !strings.Contains(*prompt, "Look at the PSP logs first.") {
		t.Errorf("prompt is missing the instruction:\n%s", *prompt)
	}

	// This run has no events, so there is nothing at index 1.
	if _, err := svc.remember(context.Background(), ws, runID, 1); !errors.Is(err, ErrBadInput) {
		t.Errorf("index past the events: %v, want ErrBadInput", err)
	}
}

// TestRememberRejectsNonOperatorEvents: a run with no instruction has
// nothing at index 0, an assistant line is not the operator's, and an
// index past the end names nothing.
func TestRememberRejectsNonOperatorEvents(t *testing.T) {
	svc, ws, root := suggestionService(t, nil)
	runID := writeRememberRun(t, root, store.State{}, steerRunEvents...)
	stubJudge(t, `{}`, nil)
	for _, index := range []int{0, 2, 9} {
		if _, err := svc.remember(context.Background(), ws, runID, index); !errors.Is(err, ErrBadInput) {
			t.Errorf("index %d: %v, want ErrBadInput", index, err)
		}
	}
}

func TestRememberRefusesShadow(t *testing.T) {
	svc, ws, root := suggestionService(t, nil)
	runID := writeRememberRun(t, root, store.State{Shadow: true, Instruction: "x"}, steerRunEvents...)
	stubJudge(t, `{}`, nil)
	_, err := svc.remember(context.Background(), ws, runID, 4)
	if !errors.Is(err, ErrBadInput) || !strings.Contains(err.Error(), "is a measurement; its messages are not remembered") {
		t.Fatalf("err %v", err)
	}
	if _, statErr := os.Stat(store.SuggestionsPath(root)); !os.IsNotExist(statErr) {
		t.Errorf("suggestions file: %v, want none", statErr)
	}
}

// proposeOne queues one remembered suggestion and answers with it.
func proposeOne(t *testing.T, root, playbook, addition string) store.SuggestionLine {
	t.Helper()
	l := store.SuggestionLine{Ev: "proposed", Source: "remember", Key: "OMNI-1", Playbook: playbook, Addition: addition, Reason: "r"}
	if _, err := store.AppendSuggestion(root, l); err != nil {
		t.Fatal(err)
	}
	l.ID = store.SuggestionID(playbook, addition)
	return l
}

func TestAcceptAppendsToExisting(t *testing.T) {
	svc, ws, root := suggestionService(t, map[string]string{"20-orders.md": ordersPlaybook})
	p := proposeOne(t, root, "20-orders.md", "- Check the tax rounding.")
	// The operator edits the playbook between the proposal and the accept.
	edited := "# Orders\n\nLede, rewritten.\n\n- Older rule.\n\n\n"
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "playbooks", "20-orders.md"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := svc.acceptSuggestion(ws, p.ID, "", "")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if got.ID != p.ID || got.Playbook != "20-orders.md" {
		t.Errorf("accepted %+v", got)
	}
	body, err := svc.Playbook(ws, "20-orders.md")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Orders\n\nLede, rewritten.\n\n- Older rule.\n\n- Check the tax rounding.\n"
	if body != want {
		t.Fatalf("body %q, want %q", body, want)
	}
	lines, _ := store.ReadSuggestions(root)
	last := lines[len(lines)-1]
	if last.Ev != "accepted" || last.ID != p.ID || last.Playbook != "20-orders.md" || last.Edited {
		t.Fatalf("last line %+v, want accepted, not edited", last)
	}
	if pending, _ := svc.suggestions(ws); len(pending) != 0 {
		t.Errorf("pending %+v after accept", pending)
	}
}

func TestAcceptCreatesNew(t *testing.T) {
	svc, ws, root := suggestionService(t, map[string]string{"20-orders.md": ordersPlaybook})
	p := proposeOne(t, root, "20-orders.md", "- Rule.")

	if _, err := svc.acceptSuggestion(ws, p.ID, "95-new-thing.md", "- A new rule."); err != nil {
		t.Fatalf("accept: %v", err)
	}
	body, err := svc.Playbook(ws, "95-new-thing.md")
	if err != nil {
		t.Fatal(err)
	}
	if body != "# New thing\n\n- A new rule.\n" {
		t.Fatalf("body %q", body)
	}
	if orders, _ := svc.Playbook(ws, "20-orders.md"); orders != ordersPlaybook {
		t.Errorf("the proposal's own playbook changed: %q", orders)
	}
	lines, _ := store.ReadSuggestions(root)
	last := lines[len(lines)-1]
	if last.Ev != "accepted" || last.Playbook != "95-new-thing.md" || !last.Edited {
		t.Fatalf("last line %+v, want accepted into the new playbook, edited", last)
	}
}

func TestAcceptInvalidName(t *testing.T) {
	svc, ws, root := suggestionService(t, nil)
	p := proposeOne(t, root, "20-orders.md", "- Rule.")
	for _, name := range []string{"../x.md", "Orders.md", "5-x.md"} {
		if _, err := svc.acceptSuggestion(ws, p.ID, name, ""); !errors.Is(err, ErrBadInput) {
			t.Errorf("%q: %v, want ErrBadInput", name, err)
		}
	}
	if pending, _ := svc.suggestions(ws); len(pending) != 1 {
		t.Errorf("pending %+v, want the proposal still there", pending)
	}
}

func TestAcceptUnknownID(t *testing.T) {
	svc, ws, root := suggestionService(t, nil)
	p := proposeOne(t, root, "20-orders.md", "- Rule.")
	if _, err := svc.acceptSuggestion(ws, "s-000000000000", "", ""); !errors.Is(err, ErrNoSuchSuggestion) {
		t.Errorf("unknown id: %v, want ErrNoSuchSuggestion", err)
	}
	if err := svc.dismissSuggestion(ws, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.acceptSuggestion(ws, p.ID, "", ""); !errors.Is(err, ErrNoSuchSuggestion) {
		t.Errorf("dismissed id: %v, want ErrNoSuchSuggestion", err)
	}
}

func TestDismiss(t *testing.T) {
	svc, ws, root := suggestionService(t, nil)
	a := proposeOne(t, root, "20-orders.md", "- A.")
	b := proposeOne(t, root, "20-orders.md", "- B.")
	if err := svc.dismissSuggestion(ws, a.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := svc.suggestions(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != b.ID {
		t.Fatalf("pending %+v, want only B", pending)
	}
	lines, _ := store.ReadSuggestions(root)
	if last := lines[len(lines)-1]; last.Ev != "dismissed" || last.ID != a.ID {
		t.Fatalf("last line %+v", last)
	}
	if err := svc.dismissSuggestion(ws, a.ID); !errors.Is(err, ErrNoSuchSuggestion) {
		t.Errorf("dismissing twice: %v, want ErrNoSuchSuggestion", err)
	}
}

// TestConcurrentAcceptAppendsOnce: two accepts of the same suggestion at
// once, a double click or two open windows, file the rule once; the
// second finds it no longer pending.
func TestConcurrentAcceptAppendsOnce(t *testing.T) {
	svc, ws, root := suggestionService(t, map[string]string{"20-orders.md": ordersPlaybook})
	for i := 0; i < 20; i++ {
		addition := fmt.Sprintf("- Rule %d.", i)
		p := proposeOne(t, root, "20-orders.md", addition)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for g := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[g] = svc.acceptSuggestion(ws, p.ID, "", "")
			}()
		}
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else if !errors.Is(err, ErrNoSuchSuggestion) {
				t.Fatalf("round %d: %v", i, err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d accepts succeeded, want 1", i, ok)
		}
		body, _ := svc.Playbook(ws, "20-orders.md")
		if n := strings.Count(body, addition+"\n"); n != 1 {
			t.Fatalf("round %d: the rule is in the playbook %d times:\n%s", i, n, body)
		}
		accepted := 0
		lines, _ := store.ReadSuggestions(root)
		for _, l := range lines {
			if l.Ev == "accepted" && l.ID == p.ID {
				accepted++
			}
		}
		if accepted != 1 {
			t.Fatalf("round %d: %d accepted lines, want 1", i, accepted)
		}
	}
}
