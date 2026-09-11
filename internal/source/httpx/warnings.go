package httpx

import "sync"

// Warnings is the per-ticket store behind source.Warner: the non-fatal
// problems a call ran into, keyed by the ticket id it was made for.
//
// Keying by id is what a caller running several tickets at once needs — an
// argument-less store would let two tickets in flight swap each other's
// missing evidence — and the two rulings behind Add are the ones every
// adapter had to make separately:
//
// Add appends. One ticket's bundle is assembled from several calls (Get,
// then Threads, then Attachments, with a single WarningsFor at the end), so
// a warning from an earlier call has to survive a later one. Replacing meant
// a clean Attachments erased the pagination warning Threads had just
// recorded, and the agent read a truncated thread with nothing saying so.
//
// An identical line is dropped. Several calls walk the same comment feed, so
// a feed that stops at the page cap says the same sentence on each pass, and
// two copies in the prompt read as two problems.
//
// Take is what clears an entry: warnings are consumed by the read, not by
// the next call.
//
// The zero value is ready to use, and every method is safe for concurrent
// use.
type Warnings struct {
	mu sync.Mutex
	m  map[string][]string
}

// Add records lines against ticket id, skipping any line already there.
func (w *Warnings) Add(id string, lines ...string) {
	if len(lines) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.m == nil {
		w.m = map[string][]string{}
	}
	seen := make(map[string]bool, len(w.m[id])+len(lines))
	for _, l := range w.m[id] {
		seen[l] = true
	}
	for _, l := range lines {
		if seen[l] {
			continue
		}
		seen[l] = true
		w.m[id] = append(w.m[id], l)
	}
}

// Take returns and removes the lines recorded for ticket id, or nil when
// there are none.
func (w *Warnings) Take(id string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	lines := w.m[id]
	delete(w.m, id)
	if len(lines) == 0 {
		return nil
	}
	return append([]string(nil), lines...)
}
