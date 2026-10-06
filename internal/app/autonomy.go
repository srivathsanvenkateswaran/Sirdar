package app

import "sync"

// ActionKind names one kind of action Sirdar may take without being asked.
type ActionKind string

// ActionShadowTriage is a shadow run, its auto-answer resume, or its retry.
const ActionShadowTriage ActionKind = "shadow-triage"

// AutonomyGate is the single place that decides whether an unattended action may start.
//
// It counts the slots each workspace has taken for each kind of action, so
// every job of one kind in this process shares one limit however it was
// started: by the watcher's tick, by `sirdar shadow run`, or by a retry.
// Later rungs add kinds here rather than counting in their callers.
type AutonomyGate struct {
	mu    sync.Mutex
	slots map[gateSlot]int
}

type gateSlot struct {
	wsID string
	kind ActionKind
}

func newAutonomyGate() *AutonomyGate {
	return &AutonomyGate{slots: map[gateSlot]int{}}
}

// TryAcquire takes one of limit slots for kind in wsID. The release it
// returns gives the slot back and may be called any number of times: a job
// that releases on every way out must not free a slot a later job took.
func (g *AutonomyGate) TryAcquire(wsID string, kind ActionKind, limit int) (release func(), ok bool) {
	k := gateSlot{wsID, kind}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.slots[k] >= limit {
		return nil, false
	}
	g.slots[k]++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.slots[k]--; g.slots[k] <= 0 {
				delete(g.slots, k)
			}
		})
	}, true
}

// InFlight is how many slots of kind wsID holds now.
func (g *AutonomyGate) InFlight(wsID string, kind ActionKind) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.slots[gateSlot{wsID, kind}]
}
