// Package gather collects context for a run on the host, before the agent
// starts: the pull requests a ticket names and the history of the files
// they touched, and whatever read-only commands and MCP tools the
// workspace configured under gather.steps. Each result is a file under
// <run>/bundle/context/, listed in index.json.
//
// Everything here is read-only by construction and judged before it runs:
// commands are argv lists, never a shell line, and each is checked against
// the same allow-list a session's shell call meets; values taken from the
// ticket pass a fixed character rule before they reach an argument; MCP
// tools get the same verdict `sirdar mcp call` would. Nothing here returns
// an error to the run either. A step that fails, is refused or runs out of
// time is a row saying so, and the run goes on without it.
//
// The package knows nothing about runs or their events; internal/run calls
// Run and reports the rows.
package gather

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Input is everything one gather pass reads.
type Input struct {
	Kind        string         // "triage", "rca" or "session"
	Config      *config.Config // the workspace
	ContextDir  string         // <run>/bundle/context; created only when a file is written
	RunDir      string         // for the read scope of the allow-list check
	BundleDir   string
	Bundle      ticket.Bundle // zero for a no-reference session
	Slack       string
	Instruction string
	SkipPR      string   // an rca run's resolution PR URL, already filed as pr.md
	Env         []string // the run's child env (credentials stripped); PATH resolves gh and git
	GOOS        string   // runtime.GOOS unless a test sets it
	Now         func() time.Time
	Started     func(planned int) // called once with the number of rows before anything runs
	Done        func(Entry)       // called as each row settles, from any goroutine, serialised
}

// Entry is one row of the gather: a file written, or the reason none was.
type Entry struct {
	Name      string `json:"name"`
	File      string `json:"file,omitempty"` // bundle-relative, "context/<name>.md"
	Kind      string `json:"kind"`           // pr|history|command|mcp
	Status    string `json:"status"`         // ok|truncated|failed|denied|skipped
	Reason    string `json:"reason,omitempty"`
	Bytes     int    `json:"bytes"`
	TookMs    int64  `json:"tookMs"`
	Call      string `json:"call"`
	Summary   string `json:"summary,omitempty"`
	Diff      string `json:"diff,omitempty"` // PR rows: "context/pr-<repo>-<n>.diff"
	DiffBytes int    `json:"diffBytes,omitempty"`
}

// Result is a finished gather.
type Result struct {
	Entries []Entry // plan order, not completion order
	Took    time.Duration
}

// The row kinds.
const (
	KindPR      = "pr"
	KindHistory = "history"
	KindCommand = "command"
	KindMCP     = "mcp"
)

// The row statuses.
const (
	StatusOK        = "ok"
	StatusTruncated = "truncated"
	StatusFailed    = "failed"
	StatusDenied    = "denied"
	StatusSkipped   = "skipped"
)

// IndexFile is the manifest's name inside the context directory.
const IndexFile = "index.json"

// parallel is how many rows run at once. Four keeps a ticket naming five
// PRs from taking five times as long, without starting a dozen gh and git
// processes against one laptop. A test that needs the byte cap or the
// deadline to bite in a known order sets it to 1; such a test, and every
// test that calls Run, must stay serial (no t.Parallel), because this is
// shared package state.
var parallel = 4

// job is one unit of work. A PR's job settles two rows, its own and its
// history's, because the history needs what the PR read said.
type job struct {
	rows []int
	run  func(ctx context.Context, g *gatherer)
}

// gatherer is one Run in progress.
type gatherer struct {
	in  Input
	cfg *config.Config
	now func() time.Time

	mu      sync.Mutex
	entries []Entry
	names   map[string]bool // planned row names, lower-cased
	written int

	doneMu sync.Mutex

	invOnce sync.Once
	inv     []mcpclient.Entry
	invErr  error
}

// Run gathers. It never fails: every problem is a row.
func Run(ctx context.Context, in Input) Result {
	g := &gatherer{in: in, cfg: in.Config, now: in.Now}
	if g.now == nil {
		g.now = time.Now
	}
	if g.in.GOOS == "" {
		g.in.GOOS = runtime.GOOS
	}
	start := g.now()
	if g.cfg == nil || !g.cfg.GatherEnabled() {
		return Result{}
	}

	jobs, settled := g.plan()
	if len(g.entries) == 0 {
		return Result{}
	}
	if in.Started != nil {
		in.Started(len(g.entries))
	}
	for _, i := range settled {
		g.done(g.entries[i])
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(g.cfg.Gather.TotalSeconds)*time.Second)
	defer cancel()

	// Jobs are handed a slot in plan order, so the rows that run before
	// the deadline or the byte cap are the first ones planned, not
	// whichever goroutine the scheduler happened to wake.
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for n, j := range jobs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			for _, rest := range jobs[n:] {
				g.skipAll(rest.rows, "gather deadline")
			}
			break
		}
		j := j
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			j.run(ctx, g)
		}()
	}
	wg.Wait()

	g.writeIndex()
	return Result{Entries: g.entries, Took: g.now().Sub(start)}
}

// startRow reports whether row i may start now, settling it as skipped
// when the deadline has passed or the run's bytes are spent.
func (g *gatherer) startRow(ctx context.Context, i int) bool {
	if ctx.Err() != nil {
		g.skip(i, "gather deadline")
		return false
	}
	g.mu.Lock()
	full := g.written >= g.cfg.Gather.TotalBytes
	g.mu.Unlock()
	if full {
		g.skip(i, "context cap")
		return false
	}
	return true
}

func (g *gatherer) skipAll(rows []int, reason string) {
	for _, i := range rows {
		g.skip(i, reason)
	}
}

func (g *gatherer) skip(i int, reason string) {
	g.mu.Lock()
	e := g.entries[i]
	g.mu.Unlock()
	e.Status = StatusSkipped
	e.Reason = joinReasons(reason, e.Reason)
	g.settle(i, e)
}

// settle records row i's outcome and reports it.
func (g *gatherer) settle(i int, e Entry) {
	g.mu.Lock()
	g.entries[i] = e
	g.mu.Unlock()
	g.done(e)
}

func (g *gatherer) done(e Entry) {
	if g.in.Done == nil {
		return
	}
	g.doneMu.Lock()
	defer g.doneMu.Unlock()
	g.in.Done(e)
}

// addRow appends a planned row and returns its index. The row's name is
// made unique first, since it is also its file's name: two values that
// fileSafe to the same text, step x on value y beside a step called x-y,
// or one repository number under two owners would otherwise write over
// each other. Names are compared without case, as macOS and Windows
// compare file names.
func (g *gatherer) addRow(e Entry) int {
	if g.names == nil {
		g.names = map[string]bool{}
	}
	base := e.Name
	for n := 2; g.names[strings.ToLower(e.Name)]; n++ {
		e.Name = base + "-" + strconv.Itoa(n)
	}
	g.names[strings.ToLower(e.Name)] = true
	g.entries = append(g.entries, e)
	return len(g.entries) - 1
}

// writeFile writes one file into the context directory, creating it on
// first use, and counts it against gather.totalBytes. name is the file's
// base name.
func (g *gatherer) writeFile(name string, body []byte) error {
	if err := os.MkdirAll(g.in.ContextDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(g.in.ContextDir, name), body, 0o644); err != nil {
		return err
	}
	g.mu.Lock()
	g.written += len(body)
	g.mu.Unlock()
	return nil
}

// writeIndex files the manifest. A run that has a ticket always gets one,
// so the prompt and the UI can tell "nothing was gathered" from "gather
// never ran"; a session with no reference gets one only when a row wrote
// a file, so its bundle directory is not created to hold a list of
// nothing.
func (g *gatherer) writeIndex() {
	wrote := false
	for _, e := range g.entries {
		if e.File != "" {
			wrote = true
			break
		}
	}
	if !wrote && !hasTicket(g.in.Bundle) {
		return
	}
	body, err := json.MarshalIndent(g.entries, "", "  ")
	if err != nil {
		return
	}
	_ = g.writeFile(IndexFile, append(body, '\n'))
}

// hasTicket reports whether the bundle holds anything at all.
func hasTicket(b ticket.Bundle) bool {
	return b.Tracker != nil || b.Helpdesk != nil || b.Reported != nil || len(b.Thread) > 0
}

// ReadIndex reads the manifest a Run wrote. A directory with no manifest
// is not an error: that run gathered nothing.
func ReadIndex(contextDir string) ([]Entry, error) {
	body, err := os.ReadFile(filepath.Join(contextDir, IndexFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// joinReasons joins the non-empty reasons with "; ".
func joinReasons(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += "; "
		}
		out += p
	}
	return out
}

// tookMs is the milliseconds since start.
func (g *gatherer) tookMs(start time.Time) int64 {
	return g.now().Sub(start).Milliseconds()
}

// plan lays out every row before anything runs: the pull requests first,
// in the order the ticket names them, then the configured steps in config
// order. The rows that are settled by planning alone — a PR in a
// repository nobody configured, a step with no value to run on — come back
// in settled.
func (g *gatherer) plan() (jobs []job, settled []int) {
	if g.cfg.GatherPREnabled() {
		j, s := g.planPRs()
		jobs, settled = append(jobs, j...), append(settled, s...)
	}
	j, s := g.planSteps()
	return append(jobs, j...), append(settled, s...)
}
