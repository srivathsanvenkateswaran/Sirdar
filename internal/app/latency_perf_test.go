package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// coalesceWindow is the window the browser transport batches arriving
// frames in (desktop/frontend/src/api/coalesce.ts), so lines that land
// inside one of them are one change of the transcript.
const coalesceWindow = 16 * time.Millisecond

// TestEventLatency times how long a line written to a run's events.jsonl
// takes to reach a subscriber, on both paths: the in-process sink a run
// this shell is hosting uses, and the watcher's poll, which is all a run
// another process started has.
//
// It is the measurement behind finding #1 of docs/research/14-session-perf.md
// — a line every 50 ms for six seconds — and it is skipped unless asked
// for, because it takes that long twice:
//
//	SIRDAR_PERF=1 go test ./internal/app -run TestEventLatency -v
func TestEventLatency(t *testing.T) {
	if os.Getenv("SIRDAR_PERF") == "" {
		t.Skip("set SIRDAR_PERF=1 to time the event path")
	}

	const (
		lines = 120
		every = 50 * time.Millisecond
	)

	for i, path := range []string{"watcher poll", "in-process sink"} {
		t.Run(path, func(t *testing.T) {
			root := newWorkspace(t)
			svc, events := liveService(t, root, DefaultInterval)

			key := "SBX-1"
			runID := fmt.Sprintf("20260917T10000%dZ-perf", i)
			writeState(t, root, key, runID, store.StatusRunning)
			dir := filepath.Join(root, ".sirdar", "runs", key, runID)

			log, err := (store.Run{Dir: dir}).OpenEventLog()
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()

			// Let the watcher adopt the run before the writing starts, so
			// the first line is not waiting on the initial sweep.
			time.Sleep(2 * DefaultInterval)

			var mu sync.Mutex
			written := make([]time.Time, 0, lines)
			arrived := make([]time.Time, 0, lines)
			done := make(chan struct{})
			go func() {
				defer close(done)
				for e := range events {
					if e.Kind != KindRunEvent || e.RunID != runID {
						continue
					}
					mu.Lock()
					arrived = append(arrived, time.Now())
					full := len(arrived) == lines
					mu.Unlock()
					if full {
						return
					}
				}
			}()

			write := func() {
				mu.Lock()
				written = append(written, time.Now())
				mu.Unlock()
				if path == "in-process sink" {
					if err := svc.Append(dir, func() ([]byte, error) {
						return log.AppendLine("assistant_text", map[string]string{"text": "a line of the answer"})
					}); err != nil {
						t.Error(err)
					}
					return
				}
				// What the CLI does: append, and let whoever is watching
				// find it.
				if err := log.Append("assistant_text", map[string]string{"text": "a line of the answer"}); err != nil {
					t.Error(err)
				}
			}

			tick := time.NewTicker(every)
			defer tick.Stop()
			for n := 0; n < lines; n++ {
				write()
				<-tick.C
			}

			select {
			case <-done:
			case <-time.After(3 * time.Second): // the last poll interval, with room
			}

			mu.Lock()
			defer mu.Unlock()
			if len(arrived) < lines {
				t.Fatalf("only %d of %d lines arrived", len(arrived), lines)
			}

			// How long each line took to reach the reader.
			latency := make([]float64, lines)
			for n := range latency {
				latency[n] = ms(arrived[n].Sub(written[n]))
			}
			sort.Float64s(latency)

			// And how often the transcript moved: lines landing inside one
			// coalescing window are one change.
			changes := []time.Time{arrived[0]}
			for _, at := range arrived[1:] {
				if at.Sub(changes[len(changes)-1]) > coalesceWindow {
					changes = append(changes, at)
				}
			}
			gaps := make([]float64, 0, len(changes))
			for n := 1; n < len(changes); n++ {
				gaps = append(gaps, ms(changes[n].Sub(changes[n-1])))
			}
			sort.Float64s(gaps)

			t.Logf("%s: %d lines in %s", path, lines, time.Duration(lines)*every)
			t.Logf("  per line: median %.0fms  p95 %.0fms  max %.0fms",
				quantile(latency, 0.5), quantile(latency, 0.95), latency[lines-1])
			t.Logf("  transcript changed %d times: gap median %.0fms  p95 %.0fms  max %.0fms",
				len(changes), quantile(gaps, 0.5), quantile(gaps, 0.95), gaps[len(gaps)-1])
		})
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// quantile is the value at a quantile of a sorted slice.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[int(q*float64(len(sorted)-1))]
}
