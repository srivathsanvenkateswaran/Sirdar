package store

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The steer inbox.
//
// A steer typed while a run is working has to reach an executor that may
// be in another process: the desktop app hosting a run the CLI is steering,
// or the other way round. state.json cannot carry it there — the executor
// rewrites the whole file from memory on every usage line, so a second
// writer would either be overwritten or overwrite it. The queue is its own
// append-only file, steers.jsonl, one instruction per line. Anyone may
// append to it; what became of each line is recorded in state.json's
// QueuedSteers, by the executor that owns the run and, once the run has
// settled, by whatever applies the held ones.

const steerInbox = "steers.jsonl"

// QueueSteer appends one instruction to the run's inbox and returns it with
// its id and time filled in. The line goes out in a single append, so two
// writers cannot interleave inside it.
func (r Run) QueueSteer(text, model string, now time.Time) (QueuedSteer, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return QueuedSteer{}, fmt.Errorf("store: steer id: %w", err)
	}
	q := QueuedSteer{
		ID:     now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(buf[:]),
		At:     now.UTC(),
		Text:   text,
		Model:  model,
		Status: SteerQueued,
	}
	data, err := json.Marshal(q)
	if err != nil {
		return QueuedSteer{}, fmt.Errorf("store: marshal steer: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(r.Dir, steerInbox), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return QueuedSteer{}, fmt.Errorf("store: open steer inbox: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return QueuedSteer{}, fmt.Errorf("store: write steer inbox: %w", err)
	}
	return q, nil
}

// SteerInbox reads every instruction ever queued on the run, oldest first,
// each as queued. A run nobody steered while it worked has no inbox, which
// is not an error, and a line that does not parse — a write cut short — is
// skipped.
func (r Run) SteerInbox() ([]QueuedSteer, error) {
	data, err := os.ReadFile(filepath.Join(r.Dir, steerInbox))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read steer inbox: %w", err)
	}
	var out []QueuedSteer
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var q QueuedSteer
		if json.Unmarshal(sc.Bytes(), &q) != nil || q.ID == "" {
			continue
		}
		q.Status = SteerQueued
		out = append(out, q)
	}
	return out, nil
}

// MergeSteerInbox adds every inbox line s does not account for yet to
// s.QueuedSteers, as queued, and returns the ones it added. The state's own
// entries win: they are where what happened to each one is recorded.
func (r Run) MergeSteerInbox(s *State) ([]QueuedSteer, error) {
	inbox, err := r.SteerInbox()
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(s.QueuedSteers))
	for _, q := range s.QueuedSteers {
		known[q.ID] = true
	}
	var added []QueuedSteer
	for _, q := range inbox {
		if known[q.ID] {
			continue
		}
		known[q.ID] = true
		s.QueuedSteers = append(s.QueuedSteers, q)
		added = append(added, q)
	}
	return added, nil
}
