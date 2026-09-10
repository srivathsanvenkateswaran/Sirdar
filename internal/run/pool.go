package run

import (
	"context"
	"sync"
	"time"
)

// pool coordinates the worker goroutines of one triage invocation. Runs
// never share state; the one thing they do share is a rate limit, which
// pauses every worker that has not started its session yet.
type pool struct {
	mu    sync.Mutex
	until time.Time
}

// pauseObserver is a test seam: when set, it is called every time a pause
// is recorded, so a test can synchronise on it. Production leaves it nil.
var pauseObserver func(time.Time)

func newPool() *pool { return &pool{} }

// pause holds back runs that have not started until the provider says the
// rate limit lifts. A provider that reports no reset time pauses nothing:
// there would be nothing to wait for.
func (p *pool) pause(until time.Time) {
	if p == nil || until.IsZero() {
		return
	}
	p.mu.Lock()
	if until.After(p.until) {
		p.until = until
	}
	p.mu.Unlock()
	if pauseObserver != nil {
		pauseObserver(until)
	}
}

// waitUntilResumed blocks while a rate-limit pause is in force, or until
// the context is cancelled.
func (p *pool) waitUntilResumed(ctx context.Context) {
	if p == nil {
		return
	}
	for {
		p.mu.Lock()
		until := p.until
		p.mu.Unlock()

		wait := time.Until(until)
		if wait <= 0 {
			return
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
