package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// intakeLogTTL is how long a Slack MCP reading's transcript is kept. A week
// covers "it said no message on Tuesday" read on the following Monday.
const intakeLogTTL = 7 * 24 * time.Hour

// intakeLogDir is where the transcripts go: <user dir>/intake. A variable so
// this package's tests keep them out of the operator's home.
var intakeLogDir = func() (string, error) {
	dir, err := config.UserDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "intake"), nil
}

// intakeLog is one reading's transcript: a JSON line for the request, one
// per provider event, and one for the outcome. It holds what the Slack MCP
// returned — message text the operator can already read — and never a
// credential: the session's environment and MCP config are not recorded.
// A transcript that cannot be written is skipped silently; the reading is
// what matters, the record of it is a convenience.
type intakeLog struct {
	f *os.File
}

// openIntakeLog starts a transcript and sweeps the ones older than a week.
func openIntakeLog(now time.Time) *intakeLog {
	dir, err := intakeLogDir()
	if err != nil || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	sweepIntakeLogs(dir, now)
	name := now.UTC().Format("20060102T150405.000000000Z") + ".jsonl"
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return nil
	}
	return &intakeLog{f: f}
}

func sweepIntakeLogs(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err == nil && now.Sub(info.ModTime()) > intakeLogTTL {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Path is the transcript's file, or "" for a nil log.
func (l *intakeLog) Path() string {
	if l == nil {
		return ""
	}
	return l.f.Name()
}

func (l *intakeLog) write(v any) {
	if l == nil {
		return
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = l.f.Write(append(raw, '\n'))
}

// event records one provider event. Streamed text fragments are left out:
// the whole block arrives after them, and a transcript of every token is
// unreadable.
func (l *intakeLog) event(ev provider.Event) {
	if l == nil || (ev.Kind == provider.EvAssistantText && ev.Delta) {
		return
	}
	rec := map[string]any{"record": "event", "kind": ev.Kind, "at": ev.At}
	if ev.Text != "" {
		rec["text"] = ev.Text
	}
	if ev.Tool != "" {
		rec["tool"] = ev.Tool
	}
	if len(ev.Input) > 0 && json.Valid(ev.Input) {
		rec["input"] = ev.Input
	}
	if ev.Decision != "" {
		rec["decision"] = ev.Decision
	}
	if ev.Turns > 0 {
		rec["turns"] = ev.Turns
	}
	if len(ev.Final) > 0 && json.Valid(ev.Final) {
		rec["final"] = ev.Final
	}
	l.write(rec)
}

func (l *intakeLog) close() {
	if l != nil {
		_ = l.f.Close()
	}
}
