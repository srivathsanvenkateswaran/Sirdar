package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// EventLog appends newline-delimited JSON events to a run's events.jsonl.
type EventLog struct {
	f *os.File
}

// OpenEventLog opens (creating if necessary) the run's events.jsonl for
// appending.
func (r Run) OpenEventLog() (*EventLog, error) {
	f, err := os.OpenFile(filepath.Join(r.Dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("store: open event log: %w", err)
	}
	return &EventLog{f: f}, nil
}

type event struct {
	T       string `json:"t"`
	Kind    string `json:"kind"`
	Payload any    `json:"payload"`
}

// Append writes one JSON line: {"t":"<RFC3339Nano UTC>","kind":"<kind>","payload":<payload>}
func (l *EventLog) Append(kind string, payload any) error {
	e := event{
		T:       time.Now().UTC().Format(time.RFC3339Nano),
		Kind:    kind,
		Payload: payload,
	}
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("store: marshal event: %w", err)
	}
	data = append(data, '\n')
	if _, err := l.f.Write(data); err != nil {
		return fmt.Errorf("store: write event: %w", err)
	}
	return nil
}

// Close closes the underlying file.
func (l *EventLog) Close() error {
	return l.f.Close()
}
