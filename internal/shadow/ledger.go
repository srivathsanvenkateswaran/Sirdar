// Package shadow holds the parts of shadow mode that need no service: the
// append-only ledger every shadow event lands in, the per-tick decision of
// which tickets to start, and the scorecard arithmetic. The watcher, the CLI
// and the API all read the ledger through this package, so the rules for
// what a line means live in one place.
package shadow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Version is written on every ledger line so a later format can tell old
// lines from new ones without guessing from the fields present.
const Version = 1

// maxLineBytes bounds one ledger line. The watcher and the manual commands
// append to the same file without a lock, relying on a single small
// O_APPEND write landing whole; keeping lines well short of anything large
// is what keeps that reliance reasonable.
const maxLineBytes = 64 << 10

// MaxFieldRunes bounds a free-text field that reaches a ledger line from
// outside Sirdar, such as a tracker's title or status or a warning built
// from an adapter's PR list. One oversized value would push its line past
// maxLineBytes, and a line that never lands is one the next tick tries to
// write again.
const MaxFieldRunes = 500

// LedgerPath is where a workspace's shadow ledger lives.
func LedgerPath(root string) string {
	return filepath.Join(root, ".sirdar", "shadow.jsonl")
}

// Entry is one ledger line. Ev names the event (armed seen started finished
// outcome graded skipped paused resumed digest); each event uses only the
// fields commented with its name, and the rest stay empty so the line
// carries nothing it does not mean.
type Entry struct {
	V   int       `json:"v"`
	At  time.Time `json:"at"`
	Ev  string    `json:"ev"`
	Key string    `json:"key,omitempty"`

	Since *time.Time `json:"since,omitempty"` // armed
	By    string     `json:"by,omitempty"`    // armed, resumed; paused: operator or limit, none on older lines

	Title     string     `json:"title,omitempty"`     // seen
	Type      string     `json:"type,omitempty"`      // seen
	Status    string     `json:"status,omitempty"`    // seen: ticket; finished: run; outcome: tracker
	CreatedAt *time.Time `json:"createdAt,omitempty"` // seen
	Baseline  bool       `json:"baseline,omitempty"`  // seen

	RunID         string `json:"runId,omitempty"`         // started, finished, outcome
	Attempt       int    `json:"attempt,omitempty"`       // started
	Model         string `json:"model,omitempty"`         // started
	OperatorFirst bool   `json:"operatorFirst,omitempty"` // started
	Backfill      bool   `json:"backfill,omitempty"`      // started
	Resume        bool   `json:"resume,omitempty"`        // started: a retry that resumed the handle
	// PID and Host name the process that started the run, so recovery
	// can tell a run whose process died from one another process is
	// still driving. Lines from before they existed carry neither.
	PID  int        `json:"pid,omitempty"`  // started
	Host string     `json:"host,omitempty"` // started
	AsOf *time.Time `json:"asOf,omitempty"` // started: a backfill's cutoff, which its retry reuses

	Reason       string   `json:"reason,omitempty"` // finished, graded, skipped, paused
	CostUSD      float64  `json:"costUSD,omitempty"`
	ElapsedSec   float64  `json:"elapsedSec,omitempty"`
	Turns        int      `json:"turns,omitempty"`
	Verdict      *Verdict `json:"verdict,omitempty"`      // finished; outcome (operator-triage)
	Reproduction string   `json:"reproduction,omitempty"` // finished
	NoteFiled    bool     `json:"noteFiled,omitempty"`    // finished
	Denied       []Denied `json:"denied,omitempty"`       // finished
	Questions    []string `json:"questions,omitempty"`    // finished

	Source         string     `json:"source,omitempty"` // outcome: operator-rca operator-fix operator-triage manual tracker
	PRURLs         []string   `json:"prUrls,omitempty"`
	Resolution     string     `json:"resolution,omitempty"`
	Summary        string     `json:"summary,omitempty"`        // outcome: operator RCA summary
	Classification string     `json:"classification,omitempty"` // outcome: operator RCA classification
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`

	ShadowRunID         string    `json:"shadowRunId,omitempty"` // graded
	Basis               string    `json:"basis,omitempty"`
	Final               bool      `json:"final,omitempty"`
	Grade               string    `json:"grade,omitempty"` // confirmed partial wrong no-answer ungradable
	PRFilesHit          *Fraction `json:"prFilesHit,omitempty"`
	CodeRefsPathOverlap *Fraction `json:"codeRefsPathOverlap,omitempty"`
	JudgeModel          string    `json:"judgeModel,omitempty"`
	LeadMinutes         *float64  `json:"leadMinutes,omitempty"`
	Warning             string    `json:"warning,omitempty"`

	Until *time.Time `json:"until,omitempty"` // paused
	Day   string     `json:"day,omitempty"`   // digest: local date 2006-01-02
}

// Verdict is what a shadow run concluded, cut down from its result.json to
// what grading and the scorecard read.
type Verdict struct {
	Classification string   `json:"classification"`
	Confidence     string   `json:"confidence"`
	Hypothesis     string   `json:"hypothesis"` // at most 300 runes
	CodeRefs       []string `json:"codeRefs"`
	OpenQuestions  []string `json:"openQuestions,omitempty"`
}

// Denied is a tool call the shadow run's policy refused.
type Denied struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
}

// Fraction is an overlap measure kept with its counts, so a score of 0.5
// can be told apart as 1 of 2 or 50 of 100.
type Fraction struct {
	Matched int     `json:"matched"`
	Total   int     `json:"total"`
	Score   float64 `json:"score"`
}

// Append writes one line to the ledger in a single Write on an O_APPEND
// file, so a concurrent writer's line lands before or after it rather than
// inside it. A line over the limit is refused rather than split.
func Append(root string, e Entry) error {
	e.V = Version
	if e.At.IsZero() {
		e.At = time.Now()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("shadow: encode ledger line: %w", err)
	}
	line = append(line, '\n')
	if len(line) >= maxLineBytes {
		return fmt.Errorf("shadow: ledger line is %d bytes, over the 64 KiB limit", len(line))
	}
	path := LedgerPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("shadow: %w", err)
	}
	// A crash mid-write leaves a last line with no newline. Appending
	// straight after it would glue this line onto the fragment and Read
	// would drop both, so the fragment is closed off first; it stays
	// unparseable on its own and Read skips just it.
	if torn, err := endsTorn(path); err != nil {
		return fmt.Errorf("shadow: %w", err)
	} else if torn {
		line = append([]byte{'\n'}, line...)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("shadow: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return fmt.Errorf("shadow: append ledger: %w", err)
	}
	return f.Close()
}

// endsTorn reports whether the ledger is non-empty and its last byte is not
// a newline. A missing ledger is not torn.
func endsTorn(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// Read returns every ledger line in file order. A workspace that never ran
// shadow has no ledger, which is not an error. A line that does not parse
// is skipped: the only way to get one is a crash partway through a write,
// and the lines either side of it are still good.
func Read(root string) ([]Entry, error) {
	raw, err := os.ReadFile(LedgerPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("shadow: %w", err)
	}
	var out []Entry
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// Attempt is one shadow run on a ticket: its started line, and its finished
// line once the run ended.
type Attempt struct {
	Started  Entry
	Finished *Entry
}

// Ticket is everything the ledger says about one key.
type Ticket struct {
	Key, Title, Type, Status string
	CreatedAt                time.Time
	SeenAt                   time.Time
	Baseline                 bool
	Backfill                 bool
	Attempts                 []Attempt // in ledger order
	Outcomes                 []Entry
	Grade                    *Entry // the newest graded line with a grade whose ShadowRunID is the latest attempt's RunID
	Skipped                  string // reason of the newest skipped line

	// grades is every graded line for the key in ledger order, kept because
	// each one spent a judge call even after a newer grade superseded it.
	// That includes a judge call that failed, whose line carries its cost
	// and no grade.
	grades []Entry
}

// Latest is the newest attempt, or nil for a ticket never started.
func (t *Ticket) Latest() *Attempt {
	if len(t.Attempts) == 0 {
		return nil
	}
	return &t.Attempts[len(t.Attempts)-1]
}

// Fold groups the ledger by key. Lines without a key (armed, paused,
// resumed, digest) belong to the workspace, not a ticket, and are read by
// Armed and PausedUntil instead.
func Fold(entries []Entry) map[string]*Ticket {
	out := map[string]*Ticket{}
	for _, e := range entries {
		if e.Key == "" {
			continue
		}
		t := out[e.Key]
		if t == nil {
			t = &Ticket{Key: e.Key}
			out[e.Key] = t
		}
		switch e.Ev {
		case "seen":
			// The first seen line is the one that counts: SeenAt is what
			// the stale rule measures from.
			if t.SeenAt.IsZero() {
				t.Title, t.Type, t.Status = e.Title, e.Type, e.Status
				if e.CreatedAt != nil {
					t.CreatedAt = *e.CreatedAt
				}
				t.SeenAt = e.At
				t.Baseline = e.Baseline
			}
		case "started":
			t.Attempts = append(t.Attempts, Attempt{Started: e})
			if e.Backfill {
				t.Backfill = true
			}
		case "finished":
			for i := len(t.Attempts) - 1; i >= 0; i-- {
				if t.Attempts[i].Started.RunID == e.RunID {
					f := e
					t.Attempts[i].Finished = &f
					break
				}
			}
		case "outcome":
			t.Outcomes = append(t.Outcomes, e)
		case "graded":
			t.grades = append(t.grades, e)
		case "skipped":
			t.Skipped = e.Reason
		}
	}
	// A grade is about one run, and a ticket's grade is its latest run's:
	// a grade of an earlier attempt says nothing about the retry. Among the
	// latest run's grades the newest wins, which is how a final grade
	// supersedes a provisional one. A line with no grade records a judge
	// call that failed; it says nothing about the run, so it cannot hide
	// the grade before it.
	for _, t := range out {
		gs := t.grades
		latest := t.Latest()
		if latest == nil {
			continue
		}
		for i := len(gs) - 1; i >= 0; i-- {
			if gs[i].ShadowRunID == latest.Started.RunID && gs[i].Grade != "" {
				g := gs[i]
				t.Grade = &g
				break
			}
		}
	}
	return out
}

// Armed reports when shadow was last switched on: the newest armed line's
// since, or its own time when it carries none.
func Armed(entries []Entry) (since time.Time, ok bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Ev != "armed" {
			continue
		}
		if e.Since != nil {
			return *e.Since, true
		}
		return e.At, true
	}
	return time.Time{}, false
}

// PauseByLimit marks a paused line written because a run hit a rate or
// model limit. A paused line without it is the operator's, which is what
// every paused line written before the field existed was taken to be.
const PauseByLimit = "limit"

// PausedUntil reports whether the watcher is paused at now. The operator's
// newest pause sets the pause, replacing their earlier ones, so they can
// shorten it as well as lengthen it. A limit's pause written after it can
// only lengthen it: runs already in flight keep going after shadow pause,
// and one that hits a rate limit must not end the operator's pause early.
// Without an operator pause, the latest limit pause holds. A resumed line
// clears every pause before it, and a pause whose until has passed is over
// without anyone writing a line for it.
func PausedUntil(entries []Entry, now time.Time) (until time.Time, reason string, paused bool) {
	consider := func(e Entry) {
		if e.Until == nil || !e.Until.After(now) {
			return
		}
		if !paused || e.Until.After(until) {
			until, reason, paused = *e.Until, e.Reason, true
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Ev == "resumed" {
			break
		}
		if e.Ev != "paused" {
			continue
		}
		consider(e)
		if e.By != PauseByLimit {
			break
		}
	}
	return until, reason, paused
}
