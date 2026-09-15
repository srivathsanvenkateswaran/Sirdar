package run

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// initEvent is the system event a provider's init line becomes, carrying
// the model the CLI says is answering.
func initEvent(model string) provider.Event {
	return provider.Event{
		Kind:  provider.EvSystem,
		Text:  "init",
		Model: model,
		Raw:   json.RawMessage(`{"type":"system","subtype":"init"}`),
	}
}

// TestReportedModelFillsTheRunsModel: a run whose configuration named no
// model, or named an alias the CLI resolves for itself, has nothing to show
// on a screen or in a register row until the provider says what answered.
// A configured model id is a pin, though, and is never overwritten: a
// record that quietly renamed the model the operator asked for would be
// worse than one that named none.
func TestReportedModelFillsTheRunsModel(t *testing.T) {
	cases := []struct {
		name               string
		configured         string
		reported           string
		wantModel, wantReq string
	}{
		{
			name:      "nothing configured",
			reported:  "claude-sonnet-5-20260514",
			wantModel: "claude-sonnet-5-20260514",
		},
		{
			name:       "an alias the CLI resolves",
			configured: "sonnet",
			reported:   "claude-sonnet-5-20260514",
			wantModel:  "claude-sonnet-5-20260514",
			wantReq:    "sonnet",
		},
		{
			name:       "an alias in another case",
			configured: "Opus",
			reported:   "claude-opus-5-20260514",
			wantModel:  "claude-opus-5-20260514",
			wantReq:    "Opus",
		},
		{
			name:       "a pinned id the provider disagrees with",
			configured: "claude-opus-5-20260514",
			reported:   "claude-sonnet-5-20260514",
			wantModel:  "claude-opus-5-20260514",
		},
		{
			name:       "an agent that reports no model at all",
			configured: "sonnet",
			wantModel:  "sonnet",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := newWorkspace(t)
			p := &stubProvider{script: replay(initEvent(c.reported), finalEvent(triageDoc))}
			r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

			outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{Model: c.configured})
			if err != nil {
				t.Fatal(err)
			}
			out := outs[0]
			if out.State.Status != store.StatusCompleted {
				t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
			}
			if out.State.Model != c.wantModel {
				t.Errorf("state.Model = %q, want %q", out.State.Model, c.wantModel)
			}
			if out.State.ModelRequested != c.wantReq {
				t.Errorf("state.ModelRequested = %q, want %q", out.State.ModelRequested, c.wantReq)
			}
			// The session was asked for what the workspace configured,
			// whatever the provider went on to report.
			if got := p.spec(0).Model; got != c.configured {
				t.Errorf("session asked for model %q, want the configured %q", got, c.configured)
			}

			// state.json is the record every screen and the register row
			// read, so the same answer has to be on disk.
			_, onDisk, err := store.Open(cfg.Root, out.State.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if onDisk.Model != c.wantModel || onDisk.ModelRequested != c.wantReq {
				t.Errorf("state.json model %q requested %q, want %q / %q",
					onDisk.Model, onDisk.ModelRequested, c.wantModel, c.wantReq)
			}
		})
	}
}

// TestReportedModelIsWrittenWhileTheRunIsStillGoing: the desktop reads
// state.json — the watcher turns each write into the run.updated event the
// session topbar listens for — so a model recorded only when the run
// finished would leave the topbar reading "model unknown" for the whole of
// the run, which is precisely the stretch a person is watching it.
func TestReportedModelIsWrittenWhileTheRunIsStillGoing(t *testing.T) {
	cfg := newWorkspace(t)

	const reported = "claude-sonnet-5-20260514"
	midRun := make(chan store.State, 1)
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(initEvent(reported)) {
			return
		}
		midRun <- pollState(spec.RunDir, func(st store.State) bool { return st.Model == reported })
		s.emit(finalEvent(triageDoc))
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{Model: "sonnet"}); err != nil {
		t.Fatal(err)
	}
	seen := <-midRun
	if seen.Model != reported {
		t.Errorf("mid-run state.json model = %q, want %q", seen.Model, reported)
	}
	if seen.ModelRequested != "sonnet" {
		t.Errorf("mid-run state.json requested model = %q, want %q", seen.ModelRequested, "sonnet")
	}
	if seen.Status != store.StatusRunning {
		t.Errorf("mid-run state.json status = %q, want %q", seen.Status, store.StatusRunning)
	}
}

// pollState reads a run's state.json until want is satisfied, and returns
// the last state it read when it never is. It is called from the stub
// provider's goroutine, where a t.Fatal would be lost.
func pollState(dir string, want func(store.State) bool) store.State {
	var last store.State
	deadline := time.Now().Add(2 * time.Second)
	for {
		if st, err := (store.Run{Dir: dir}).ReadState(); err == nil {
			last = st
			if want(st) {
				return st
			}
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
}
