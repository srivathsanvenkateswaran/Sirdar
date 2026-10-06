package app

import "testing"

func TestGateSemaphore(t *testing.T) {
	g := newAutonomyGate()

	release, ok := g.TryAcquire("ws-a", ActionShadowTriage, 1)
	if !ok {
		t.Fatal("the first acquire of an empty gate failed")
	}
	if _, ok := g.TryAcquire("ws-a", ActionShadowTriage, 1); ok {
		t.Fatal("a second acquire past the limit succeeded")
	}
	if n := g.InFlight("ws-a", ActionShadowTriage); n != 1 {
		t.Fatalf("in flight %d, want 1", n)
	}

	// Another workspace has slots of its own.
	other, ok := g.TryAcquire("ws-b", ActionShadowTriage, 1)
	if !ok {
		t.Fatal("a second workspace shared the first one's slot")
	}
	other()

	release()
	release() // a second release must not free a slot someone else holds
	if n := g.InFlight("ws-a", ActionShadowTriage); n != 0 {
		t.Fatalf("in flight %d after release, want 0", n)
	}

	again, ok := g.TryAcquire("ws-a", ActionShadowTriage, 1)
	if !ok {
		t.Fatal("acquire after release failed")
	}
	release() // the stale release from before stays spent
	if n := g.InFlight("ws-a", ActionShadowTriage); n != 1 {
		t.Fatalf("a stale release freed the new holder's slot: in flight %d", n)
	}
	again()
}
