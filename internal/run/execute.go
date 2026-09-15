package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// maxMalformed is how many malformed provider lines in a row end the run.
const maxMalformed = 10

// noWireSchemaEnforcement names the providers that cannot hold the model
// to SessionSpec.OutputSchema, so a failed note is likely to have failed by
// quoting the schema's own header back, or by answering with the schema
// itself, rather than by getting a field wrong. Those get the sharpened
// retry wording.
//
// Claude passes the schema as a CLI flag its own process enforces, Codex as
// a protocol param its agent enforces, and the openai loop as a tool-call
// parameter schema. ACP has none of those: the schema reaches the agent
// only as prompt text (acp.promptText). Cursor has none of them either —
// there is no --json-schema flag and no structured_output field, so the
// schema is appended to the prompt and the answer is read back out of the
// result line's prose (cursor.extractJSON, which refuses an object whose
// whole top level is $schema and title).
//
// Qwen is here on the strength of the first live run rather than the flag.
// --json-schema is a real flag and Qwen Code does act on it, but only after
// the fact: it registers a synthetic structured_output tool and fails the
// run when the model answers in prose instead of calling it, which is
// exactly what qwen-plus-character did on both the first turn and the
// resumed retry. The schema constrains nothing the model writes, so the
// retry is talking to a model that is answering in free text and has to be
// told so in the same words ACP's is.
var noWireSchemaEnforcement = map[string]bool{
	"acp":    true,
	"cursor": true,
	"qwen":   true,
}

// maxEmptyTurns is how many turns may end with no answer before the run is
// given up on. A turn that says nothing is not a wrong answer but a session
// that stopped — an ACP agent ends its turn on the spot when a tool call is
// refused — so it is asked to carry on rather than spending the schema
// retry, and the wall-clock and turn budgets are what stop this for good.
const maxEmptyTurns = 2

// closeGrace is how long a session gets to end on its own after its input
// has been closed, before it is cancelled outright. The note is already on
// disk by then; this only decides how the process is reaped. Runner.CloseGrace
// overrides it, which is how a test asserts the reaping without waiting
// ten seconds for it.
const closeGrace = 10 * time.Second

// execution is the state the event loop accumulates for one session.
type execution struct {
	final    []byte // the validated JSON note
	rawFinal string // the last candidate, kept when validation failed

	// row and completeErr are the outcome of filing the note, which
	// happens the moment the note validates rather than after the
	// session has ended: a run that has produced its answer must not be
	// able to lose it to a timeout, an interrupt, or a provider that
	// will not exit.
	row         note.DigestRow
	completeErr error

	retried     bool
	emptyTurns  int // turns that ended with no answer at all
	schemaError string
	malformed   int

	// finalNarration is the last thing a terminal provider line said in
	// prose rather than in JSON: a CLI's account of why it failed the run,
	// or an agent's closing sentence. It is not an answer — answerDoc
	// keeps it out of the validator — so it is held here and becomes the
	// reason a run that never produced a document ended.
	finalNarration string

	// retrySession is set when the schema retry could not be sent on the
	// running session and a fresh one was started to carry it; consume
	// switches to it and keeps going. live is the session being read from
	// right now, which the wall-clock timer cancels from its own goroutine.
	retrySession provider.Session
	live         liveSession

	// stall watches for a provider that has gone silent. It is nil when
	// the workspace turned the check off.
	stall *stallGuard

	question    string
	rateLimited bool
	resetsAt    time.Time

	overBudget  string
	interrupted bool
	failure     string

	// breach is set when the provider reported that a read-only session
	// did something a read-only session cannot do. It is kept apart from
	// failure because it outranks everything, the note included: a run
	// that saw one files nothing and ends failed, however far along it
	// was. See provider.EvBreach.
	breach string
}

// liveSession holds the session the run is currently reading from. The
// wall-clock timer fires on its own goroutine and has to stop whichever
// session is live, which is not necessarily the one the run started with:
// a schema retry can replace it partway through.
type liveSession struct {
	mu   sync.Mutex
	sess provider.Session
}

func (l *liveSession) set(s provider.Session) {
	l.mu.Lock()
	l.sess = s
	l.mu.Unlock()
}

func (l *liveSession) cancel() {
	l.mu.Lock()
	s := l.sess
	l.mu.Unlock()
	if s != nil {
		s.Cancel()
	}
}

// stallGuard cancels a session whose provider has stopped saying anything.
//
// A live agent session is never quiet for long: a tool starts, a tool
// finishes, a line of assistant text arrives, a usage line lands. Silence
// means the process died without closing its stream, or is waiting on
// something that will never arrive. The wall-clock budget catches that
// eventually, but "eventually" is 25 minutes of a run that has been dead
// since its first one.
//
// The countdown restarts on every event, so a tool call that takes longer
// than the timeout is not a stall. It is suspended outright once the run
// is waiting on a person rather than on the agent — the agent asked a
// question nobody is there to answer, or a rate limit parked the run —
// because that is a blocked run, not a hung one, and cancelling it would
// throw away a handle the operator can resume from.
//
// Every method is safe on a nil guard, which is what a workspace that set
// budget.stallMinutes to 0 gets.
type stallGuard struct {
	d time.Duration

	mu    sync.Mutex
	timer *time.Timer
	held  bool // the run is waiting on a person; do not count the silence
	done  bool // fired, or the session is over

	stalled atomic.Bool
}

// newStallGuard starts the countdown. onStall runs on the timer's own
// goroutine when the silence runs out, once at most; d <= 0 returns nil,
// which every method below tolerates.
func newStallGuard(d time.Duration, onStall func()) *stallGuard {
	if d <= 0 {
		return nil
	}
	g := &stallGuard{d: d}
	g.timer = time.AfterFunc(d, func() {
		g.mu.Lock()
		if g.done || g.held {
			g.mu.Unlock()
			return
		}
		g.done = true
		g.mu.Unlock()
		g.stalled.Store(true)
		onStall()
	})
	return g
}

// reset restarts the countdown. Every event the run reads does this.
func (g *stallGuard) reset() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done || g.held {
		return
	}
	g.timer.Reset(g.d)
}

// hold suspends the countdown: what is being waited on right now is not the
// model. A caller that means this for the rest of the run — the session
// asked a question, or a rate limit parked it — simply never calls rearm;
// one that means it for one specific wait, such as the time a fresh
// process takes to start, calls rearm once that wait is over.
func (g *stallGuard) hold() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return
	}
	g.held = true
	g.timer.Stop()
}

// rearm resumes the countdown after a hold that was only ever meant to
// cover one wait — Provider.Start for a resumed session, say — as opposed
// to a hold that means the run is now blocked on a person or a clock for
// good. reset would not do this: it leaves a held guard held, on purpose,
// so that the ordinary per-event reset during that same wait cannot
// accidentally undo a hold meant to last the rest of the run.
func (g *stallGuard) rearm() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return
	}
	g.held = false
	g.timer.Reset(g.d)
}

// stop ends the watch, for a session that has produced its answer or is
// being reaped.
func (g *stallGuard) stop() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.done = true
	g.mu.Unlock()
	g.timer.Stop()
}

// fired reports whether this run was cancelled for going quiet.
func (g *stallGuard) fired() bool { return g != nil && g.stalled.Load() }

// stallReason is the run's Reason and the text of the error event that
// records the cancellation. It is Sirdar's own words, short enough to pass
// notifyReason's cap whole.
func stallReason(d time.Duration) string {
	return "stalled: no activity for " + stallWindow(d)
}

// stallWindow renders the timeout the way the setting names it — whole
// minutes, because budget.stallMinutes is in minutes — and falls back to
// the duration's own form for the sub-minute values a test uses.
func stallWindow(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return d.String()
}

// execute starts one agent session for a prepared run, streams its events
// to the log and to the operator, enforces the budgets, and turns whatever
// the session ended with into a terminal state.
func (r *Runner) execute(ctx context.Context, p *prepared, resume string, pl *pool) Outcome {
	if r.Provider == nil {
		return r.finish(ctx, p, store.StatusFailed, "no provider configured", note.DigestRow{})
	}

	// A rate limit reported by another run pauses this one until it lifts.
	// An interrupt during that wait means no session should start at all.
	if !pl.waitUntilResumed(ctx) {
		return r.finish(ctx, p, store.StatusBlocked, "interrupted", note.DigestRow{})
	}

	sess, err := r.Provider.Start(ctx, r.sessionSpec(p, resume))
	if err != nil {
		return r.finish(ctx, p, store.StatusFailed, fmt.Sprintf("provider: %v", err), note.DigestRow{})
	}

	p.state.Status = store.StatusRunning
	p.state.UpdatedAt = r.now()
	if err := p.run.WriteState(p.state); err != nil {
		fmt.Fprintf(r.stderr(), "[%s] state: %v\n", p.state.Key, err)
	}

	log, err := p.run.OpenEventLog()
	if err != nil {
		sess.Cancel()
		res, _ := sess.Wait() // reap the child before giving up on the run
		p.state.StderrTail = res.StderrTail
		return r.finish(ctx, p, store.StatusFailed, err.Error(), note.DigestRow{})
	}
	defer log.Close()

	ex := &execution{}
	ex.live.set(sess)

	var timedOut atomic.Bool
	if mins := r.Config.Budget.MaxMinutes; mins > 0 {
		timer := time.AfterFunc(time.Duration(mins)*time.Minute, func() {
			timedOut.Store(true)
			ex.live.cancel()
		})
		defer timer.Stop()
	}

	// The stall watch is the shorter of the two clocks: it measures
	// silence rather than elapsed time, so it catches a provider that
	// died in its first minute instead of waiting out the wall-clock
	// budget on a session that stopped existing.
	stallFor := r.stallTimeout()
	ex.stall = newStallGuard(stallFor, ex.live.cancel)
	defer ex.stall.stop()

	sessions := r.consume(ctx, p, sess, log, pl, ex)

	// The cancellation happened on the timer's goroutine, so the event
	// log is written here, once the stream has drained and nothing else
	// is appending to it.
	if ex.stall.fired() {
		stalled := provider.Event{Kind: provider.EvError, At: r.now(), Text: stallReason(stallFor)}
		r.record(p, log, stalled)
		r.progress(p, stalled)
	}

	// Every session started for this run is reaped, and the last one's
	// result is the run's: a schema retry that had to open a fresh session
	// carries the answer.
	var res provider.Result
	for _, s := range sessions {
		got, _ := s.Wait()
		res = got
		if got.Handle != "" {
			p.state.Handle = got.Handle
		} else if h := s.Handle(); h != "" {
			p.state.Handle = h
		}
		if got.Usage.Turns > p.state.Usage.Turns {
			p.state.Usage.Turns = got.Usage.Turns
			p.state.Usage.InputTokens = got.Usage.InputTok
			p.state.Usage.OutputTokens = got.Usage.OutputTok
		}
		if got.Usage.CostUSD > p.state.Usage.CostUSD {
			p.state.Usage.CostUSD = got.Usage.CostUSD
		}
		if len(got.StderrTail) > 0 {
			p.state.StderrTail = got.StderrTail
		}
	}

	if len(ex.final) == 0 && ex.rawFinal != "" {
		if err := os.WriteFile(filepath.Join(p.run.Dir, "result.raw.txt"), []byte(ex.rawFinal), 0o644); err != nil {
			fmt.Fprintf(r.stderr(), "[%s] write result.raw.txt: %v\n", p.state.Key, err)
		}
	}

	switch {
	case ex.breach != "":
		// Ahead of the note, which is the whole point. A triage run's
		// product is a note asserting that nothing was written; a run
		// that watched a write complete cannot file one, and the answer
		// it was about to give is worth less than the fact that the
		// guarantee failed. handleFinal refuses to file after a breach,
		// so on the ordinary path there is no note to disown here.
		return r.finish(ctx, p, store.StatusFailed, ex.breach, note.DigestRow{})
	case len(ex.final) > 0:
		// The note validated and was filed the moment it arrived. What
		// happened to the session afterwards — a bad exit, an interrupt,
		// a budget that expired while the process was being reaped — is
		// a warning on a completed run, not a verdict that throws the
		// answer away.
		if ex.completeErr != nil {
			return r.finish(ctx, p, store.StatusFailed, ex.completeErr.Error(), note.DigestRow{})
		}
		if res.ExitErr != nil {
			p.state.Warnings = append(p.state.Warnings, fmt.Sprintf("provider exited: %v", res.ExitErr))
		}
		if ex.interrupted {
			p.state.Warnings = append(p.state.Warnings, "interrupted after the note was produced")
		}
		if ex.failure != "" {
			// Whatever went wrong after the note landed is still worth
			// reading — it is the only account of a provider that died
			// mid-sentence — but it does not make the run a failure.
			p.state.Warnings = append(p.state.Warnings, ex.failure+", after the note was written")
		}
		if timedOut.Load() {
			p.state.Warnings = append(p.state.Warnings,
				fmt.Sprintf("the session was still running when the %d minute budget expired, after the note was written", r.Config.Budget.MaxMinutes))
		}
		if ex.overBudget != "" {
			p.state.Warnings = append(p.state.Warnings, ex.overBudget+", after the note was written")
		}
		if ex.stall.fired() {
			p.state.Warnings = append(p.state.Warnings, stallReason(stallFor)+", after the note was written")
		}
		return r.finish(ctx, p, store.StatusCompleted, "", ex.row)
	case timedOut.Load():
		return r.finish(ctx, p, store.StatusOverBudget,
			fmt.Sprintf("wall-clock budget of %d minutes exceeded", r.Config.Budget.MaxMinutes), note.DigestRow{})
	case ex.overBudget != "":
		return r.finish(ctx, p, store.StatusOverBudget, ex.overBudget, note.DigestRow{})
	case ex.interrupted:
		return r.finish(ctx, p, store.StatusBlocked, "interrupted", note.DigestRow{})
	case ex.failure != "":
		return r.finish(ctx, p, store.StatusFailed, ex.failure, note.DigestRow{})
	case ex.question != "":
		return r.finish(ctx, p, store.StatusBlocked, "agent asked: "+ex.question, note.DigestRow{})
	case ex.rateLimited:
		reason := "rate limited"
		if !ex.resetsAt.IsZero() {
			reason += ", resets at " + ex.resetsAt.Format(time.RFC3339)
		}
		return r.finish(ctx, p, store.StatusBlocked, reason, note.DigestRow{})
	case ex.stall.fired():
		// Last of the verdicts, because every other one names something
		// that actually happened: an interrupt, a budget, a malformed
		// stream. Silence is what is left when none of them did.
		return r.finish(ctx, p, store.StatusFailed, stallReason(stallFor), note.DigestRow{})
	default:
		reason := "the session ended without a JSON note"
		if res.ExitErr != nil {
			reason = fmt.Sprintf("provider exited: %v", res.ExitErr)
		}
		return r.finish(ctx, p, store.StatusFailed, reason, note.DigestRow{})
	}
}

// sessionSpec turns the workspace configuration and the prepared run into
// the session the provider should start.
func (r *Runner) sessionSpec(p *prepared, resume string) provider.SessionSpec {
	cfg := r.Config
	// Where the session stands, and the root every write is confined to.
	// It is the workspace root for everything but a fix run in a linked
	// worktree, whose tree is the worktree and whose .sirdar/ is still
	// the workspace's.
	root := p.sessionRoot(cfg.Root)
	spec := provider.SessionSpec{
		Cwd:          root,
		Prompt:       p.promptText,
		Model:        p.state.Model,
		OutputSchema: schemaFor(p.kind),
		Policy: &provider.PermissionPolicy{
			BashAllow:  cfg.Permissions.Bash,
			MCPAllow:   cfg.Permissions.MCP,
			FetchAllow: cfg.Permissions.Fetch,
			Root:       root,
			// Where a read may look: the tree the session stands in,
			// plus this run's own directory and the bundle of ticket
			// text and attachments staged inside it. A fix session's
			// root is its worktree, which contains neither.
			ReadRoots: []string{p.run.Dir, p.run.BundleDir()},
			ReadAlso:  cfg.Permissions.ReadAlso,
		},
		Mode: provider.ModeTriage,
		// mcp.workspaceOnly travels as these two fields for every
		// provider that spawns a CLI: Claude Code turns them into
		// --strict-mcp-config --mcp-config, and Codex into a generated
		// CODEX_HOME whose config.toml declares the workspace's servers
		// and nothing else.
		MCPConfig: cfg.MCPConfigPath(),
		MCPStrict: cfg.WorkspaceOnlyMCP(),
		Budget: provider.Budget{
			MaxTurns:   cfg.Budget.MaxTurns,
			MaxMinutes: cfg.Budget.MaxMinutes,
			MaxUSD:     cfg.Budget.MaxUSD,
		},
		Resume: resume,
		RunDir: p.run.Dir,
		Env:    r.childEnv(),
		Binary: r.binary(),
	}
	// A fix session is the one run that may change the workspace, so it
	// gets the write-enabled policy and its own shell allow-list, and the
	// providers are told which mode they are starting in: it is what
	// decides Claude's --disallowedTools, Codex's sandbox, and whether
	// Sirdar's own loop offers write_file and edit_file at all.
	if p.kind == store.KindFix {
		spec.Mode = provider.ModeFix
		// A repository that sets core.hooksPath (husky, lefthook, a
		// checked-in .githooks/) keeps the code git runs on commit and
		// push in an ordinary source directory, which the .git rule does
		// not cover. It is read once, here, and reserved for this session.
		spec.Policy = provider.FixPolicy(root, cfg.Permissions.FixBash, cfg.Permissions.MCP,
			extraReserved(root))
		// Where a fix may fetch from, and where it may read, are the same
		// lists a triage gets: neither question changes because the
		// session is allowed to edit files.
		spec.Policy.FetchAllow = cfg.Permissions.Fetch
		spec.Policy.ReadRoots = []string{p.run.Dir, p.run.BundleDir()}
		spec.Policy.ReadAlso = cfg.Permissions.ReadAlso
	}

	// Claude Code reads image files from the bundle directory itself.
	// Codex has to be handed them on the command line, the openai loop
	// names them in its first user message, and an ACP agent takes them as
	// inline base64 content blocks, so all three need the list.
	switch r.providerName() {
	case "codex", "openai", "acp":
		spec.Images = imageAttachments(p)
	}
	return spec
}

// extraReserved is the per-run reserved list a fix session is judged
// against on top of .git and .sirdar: the repository's core.hooksPath when
// it sets one.
func extraReserved(root string) []string {
	// A bounded context: this is one local `git config` read, and a fix
	// session must not hang behind a git that does not answer.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if hooks := provider.HooksPath(ctx, root); hooks != "" {
		return []string{hooks}
	}
	return nil
}

// binary is the configured path override for the provider in use, empty
// when the provider should be looked up on PATH — or, for the openai
// provider, because there is no binary at all: that loop runs in this
// process.
func (r *Runner) binary() string {
	var path string
	switch r.providerName() {
	case "claude":
		path = r.Config.Providers.Claude.Path
	case "codex":
		path = r.Config.Providers.Codex.Path
	}
	if path == "" {
		return ""
	}
	return r.Config.ExpandPath(path)
}

// imageAttachments returns the absolute path of every downloaded image in
// the bundle.
func imageAttachments(p *prepared) []string {
	var out []string
	for _, a := range p.bundle.Attachments {
		if !strings.HasPrefix(a.MIME, "image/") || a.Path == "" {
			continue
		}
		path := a.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(p.run.BundleDir(), path)
		}
		out = append(out, path)
	}
	return out
}

func schemaFor(kind store.Kind) []byte {
	switch kind {
	case store.KindRCA:
		return prompt.RCASchema
	case store.KindFix:
		return prompt.FixSchema
	default:
		return prompt.TriageSchema
	}
}

func noteKind(kind store.Kind) note.Kind {
	switch kind {
	case store.KindRCA:
		return note.RCA
	case store.KindFix:
		return note.Fix
	default:
		return note.Triage
	}
}

// consume reads the run's events until they end, and returns every session
// it read from, in the order they were started. There is normally one; a
// schema retry the running session could not take adds the fresh session
// that carried it, whose events are consumed the same way and into the same
// execution state.
func (r *Runner) consume(ctx context.Context, p *prepared, sess provider.Session, log *store.EventLog, pl *pool, ex *execution) []provider.Session {
	sessions := []provider.Session{sess}
	for {
		r.consumeSession(ctx, p, sess, log, pl, ex)
		if ex.retrySession == nil {
			return sessions
		}
		sess, ex.retrySession = ex.retrySession, nil
		ex.live.set(sess)
		// The retry session is live: the hold handleFinal put on for
		// Provider.Start is over, and the countdown resumes rather than
		// staying suspended. reset would not do this — the guard is
		// still held at this point — so this is rearm, not reset.
		ex.stall.rearm()
		sessions = append(sessions, sess)
	}
}

// consumeSession reads one session's events until it ends, handling an
// interrupt by cancelling the session and letting the stream drain.
func (r *Runner) consumeSession(ctx context.Context, p *prepared, sess provider.Session, log *store.EventLog, pl *pool, ex *execution) {
	done := ctx.Done()
	events := sess.Events()
	for events != nil {
		select {
		case <-done:
			done = nil // an interrupt is handled once; the stream still drains
			ex.interrupted = true
			// Stopped before the cancel, not after: an interrupt is why
			// this session is ending, and the stall guard's own timer
			// racing to the same conclusion a moment later must not also
			// mark the run stalled and put a misleading error next to the
			// real one in events.jsonl.
			ex.stall.stop()
			sess.Cancel()
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			// Every event is proof the provider is alive, whatever it
			// says, so the stall countdown starts again here rather
			// than in the handler for any particular kind.
			ex.stall.reset()
			r.record(p, log, ev)
			r.progress(p, ev)
			r.handleEvent(ctx, p, sess, pl, ex, ev)
		}
	}
}

// eventPayload is the shape of one events.jsonl payload: enough of the
// event to reconstruct what happened, plus the provider's original line.
type eventPayload struct {
	Tool     string          `json:"tool,omitempty"`
	Decision string          `json:"decision,omitempty"`
	Text     string          `json:"text,omitempty"`
	Turns    int             `json:"turns,omitempty"`
	CostUSD  float64         `json:"costUsd,omitempty"`
	Raw      json.RawMessage `json:"raw,omitempty"`
}

func (r *Runner) record(p *prepared, log *store.EventLog, ev provider.Event) {
	payload := eventPayload{
		Tool:     ev.Tool,
		Decision: ev.Decision,
		Text:     ev.Text,
		Turns:    ev.Turns,
		CostUSD:  ev.CostUSD,
	}
	if ev.Raw != nil {
		payload.Raw = ev.Raw
	}
	if err := log.Append(string(ev.Kind), payload); err != nil {
		fmt.Fprintf(r.stderr(), "[%s] event log: %v\n", p.state.Key, err)
	}
}

// progress prints the one-line-per-event view: what the agent is doing,
// what it was refused, and how it ended. Informational and assistant-text
// events are left out; they would drown the rest.
func (r *Runner) progress(p *prepared, ev provider.Event) {
	w := r.stderr()
	key := p.state.Key
	switch ev.Kind {
	case provider.EvToolStarted:
		fmt.Fprintf(w, "[%s] tool %s%s\n", key, ev.Tool, toolDetail(ev.Input))
	case provider.EvToolFinished:
		// How a tool call ended, where the provider said something worth
		// repeating. On cursor that is the only place a refusal shows up
		// — there is no permission event to print, so without this line
		// an operator watching a run cannot tell a tool that was refused
		// from one that ran. The summary is cut to one short line
		// because other adapters put the tool's whole output in Text.
		if summary := toolSummary(ev.Text); summary != "" {
			fmt.Fprintf(w, "[%s] tool %s %s\n", key, ev.Tool, summary)
		}
	case provider.EvPermission:
		verb := "allow"
		if ev.Decision == "deny" {
			verb = "deny"
		}
		fmt.Fprintf(w, "[%s] %s %s\n", key, verb, ev.Tool)
	case provider.EvFinal:
		fmt.Fprintf(w, "[%s] final\n", key)
	case provider.EvError:
		fmt.Fprintf(w, "[%s] error %s\n", key, firstLine(ev.Text))
	case provider.EvBreach:
		fmt.Fprintf(w, "[%s] failed %s\n", key, firstLine(ev.Text))
	case provider.EvQuestion:
		fmt.Fprintf(w, "[%s] blocked agent asked: %s\n", key, firstLine(ev.Text))
	case provider.EvRateLimited:
		fmt.Fprintf(w, "[%s] blocked rate limited\n", key)
	case provider.EvSystem:
		// A window's utilization is worth a line on a run that costs
		// real money. It is not a block, and must not read like one.
		if strings.HasPrefix(ev.Text, "rate limit") {
			fmt.Fprintf(w, "[%s] %s\n", key, ev.Text)
		}
	}
}

// toolSummary is a finished tool call's outcome, in a form a progress line
// can carry: one line, cut short, and nothing at all for the ordinary
// success every read tool reports.
func toolSummary(text string) string {
	s := firstLine(text)
	if s == "" || s == "ok" {
		return ""
	}
	if len(s) > toolDetailMax {
		s = s[:toolDetailMax] + "…"
	}
	return s
}

const toolDetailMax = 60

// toolDetail renders a tool's input as a short one-line tail for the
// progress view.
func toolDetail(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, input); err != nil {
		return ""
	}
	s := strings.TrimSpace(compact.String())
	if s == "" || s == "{}" {
		return ""
	}
	if len(s) > toolDetailMax {
		s = s[:toolDetailMax] + "…"
	}
	return " " + s
}

func (r *Runner) handleEvent(ctx context.Context, p *prepared, sess provider.Session, pl *pool, ex *execution, ev provider.Event) {
	if ev.Kind != provider.EvError {
		ex.malformed = 0
	}

	switch ev.Kind {
	case provider.EvUsage:
		// The highest figure each session reported, not the last one:
		// a schema retry runs in a fresh session whose counters start at
		// zero, and assigning those would hand the run back a turn and
		// cost budget it has already spent.
		//
		// The provider's running turn count is an estimate of the same
		// unit the CLI reports and never runs ahead of it, and the
		// provider reconciles its meter to the result line's num_turns,
		// so taking the maximum lands on the provider's own total.
		u := &p.state.Usage
		u.Turns = max(u.Turns, ev.Turns)
		u.InputTokens = max(u.InputTokens, ev.InputTok)
		u.OutputTokens = max(u.OutputTokens, ev.OutputTok)
		u.CostUSD = max(u.CostUSD, ev.CostUSD)
		p.state.UpdatedAt = r.now()
		if err := p.run.WriteState(p.state); err != nil {
			fmt.Fprintf(r.stderr(), "[%s] state: %v\n", p.state.Key, err)
		}
		b := r.Config.Budget
		switch {
		case b.MaxUSD > 0 && u.CostUSD > b.MaxUSD:
			ex.overBudget = fmt.Sprintf("cost $%.2f exceeded the $%.2f budget", u.CostUSD, b.MaxUSD)
			// Stopped beside the cancel it decided, for the same reason
			// as the interrupt branch: the budget is why this session is
			// ending, and the stall guard's own timer must not also fire
			// and misname the reason in events.jsonl.
			ex.stall.stop()
			sess.Cancel()
		case b.MaxTurns > 0 && u.Turns > b.MaxTurns:
			ex.overBudget = fmt.Sprintf("%d turns exceeded the %d turn budget", u.Turns, b.MaxTurns)
			ex.stall.stop()
			sess.Cancel()
		}

	case provider.EvRateLimited:
		ex.rateLimited = true
		ex.resetsAt = ev.ResetsAt
		pl.pause(ev.ResetsAt)
		// A run parked behind a rate limit is waiting on a clock the
		// provider owns. The silence after this is the block, not a
		// stall, and the run keeps the handle it can resume from.
		ex.stall.hold()

	case provider.EvQuestion:
		ex.question = ev.Text
		// The agent is waiting on an operator who is not here — the
		// blocked state `sirdar resume` exists for. Counting that
		// silence as a stall would turn every question into a failure.
		ex.stall.hold()

	case provider.EvError:
		ex.malformed++
		if ex.malformed >= maxMalformed {
			ex.failure = fmt.Sprintf("%d malformed provider lines in a row: %s", ex.malformed, firstLine(ev.Text))
			sess.Cancel()
		}

	case provider.EvBreach:
		// There is no counter here and no threshold: one is enough. The
		// session is stopped on the spot — Cancel kills the whole process
		// group, so the agent's own children go with it — and nothing
		// this run produces is filed. The first breach is the one
		// reported, because it is the one the later ones followed from.
		if ex.breach == "" {
			ex.breach = firstLine(ev.Text)
		}
		// Stopped beside the cancel it decided, as the budget branches
		// do: the breach is why this session is ending, and the stall
		// guard must not fire behind it and misname the reason.
		ex.stall.stop()
		sess.Cancel()

	case provider.EvFinal:
		r.handleFinal(ctx, p, sess, ex, ev)
	}
}

// handleFinal validates the session's JSON output. The first failure buys
// one retry turn quoting the validation errors; the second ends the run.
//
// A turn that ended with no answer at all is counted separately (see
// maxEmptyTurns): nothing was validated, so it is not the schema retry
// being spent, and an agent that stops mid-work — the ACP agents do it on
// a refused tool call — is asked again.
func (r *Runner) handleFinal(ctx context.Context, p *prepared, sess provider.Session, ex *execution, ev provider.Event) {
	// A session that has already produced a valid note is done. A provider
	// that emits a second final line — a resumed session replaying its
	// result, a CLI that repeats itself on the way out — must not file the
	// note twice and hand the register a duplicate row.
	if len(ex.final) > 0 {
		return
	}

	// A budget that went over before this line arrived has already
	// cancelled the session, and the answer can still be on its way: the
	// CLI's one `result` line becomes the usage event that breaks the
	// budget and then the final note, in that order, so the note is
	// already in flight when the cancel lands. Whether it gets read
	// before the stream drains is a race, and letting it through would
	// file the note and call the run completed — the verdict the budget
	// refused a moment earlier. The budget decided first, so it stands;
	// a budget that goes over after the note was filed is still only a
	// warning on a completed run, which is the ordering above.
	if ex.overBudget != "" {
		return
	}

	// A breach decided the same way, and harder. The session was
	// cancelled the moment it was seen, but the answer can already be in
	// flight — and filing it would put a note on disk and a row in the
	// register asserting a read-only run, which is exactly what did not
	// happen. complete() is never reached.
	if ex.breach != "" {
		return
	}

	doc, narration := answerDoc(ev)
	if narration != "" {
		// Kept for the failure reason below. It is the provider's own
		// account of how the session ended, and it is the only account
		// there is once the document turns out not to exist.
		ex.finalNarration = narration
	}

	// An empty document is a turn that ended with nothing in it — all tool
	// calls and thinking, no answer — and note.Validate would report that
	// as "parse document: EOF", which reads like malformed JSON and tells
	// an operator nothing about what actually happened.
	var err error
	if len(bytes.TrimSpace(doc)) == 0 {
		err = errEmptyAnswer
	} else {
		err = note.Validate(noteKind(p.kind), doc)
	}
	if err != nil && !errors.Is(err, errEmptyAnswer) {
		// A provider with no wire-level schema enforcement (acp today) is
		// only ever shown the schema as prompt text, and sometimes echoes
		// its own header keys ($schema, title, ...) back alongside a real
		// answer, or — rarer — answers with the schema itself. Recover the
		// former when the rest of the document goes on to validate; the
		// latter gets a clearer reason than a bare additionalProperties
		// error naming every schema keyword in turn.
		schema := schemaFor(p.kind)
		if cleaned, schemaItself, ok := stripSchemaEcho(schema, doc); ok {
			if cerr := note.Validate(noteKind(p.kind), cleaned); cerr == nil {
				doc, err = cleaned, nil
			}
		} else if schemaItself {
			err = fmt.Errorf("the agent's answer is the JSON Schema itself (a root \"properties\" object), not a document shaped by it")
		}
		if err != nil {
			// The other thing a model writing free-text JSON gets wrong
			// about a string field: null for "I have nothing to put
			// here", which is what the schema's own optional fields
			// spell that way. Same information as "", so it is read as
			// "" and the fields are named in a warning.
			if coerced, nulled, ok := coerceNullStrings(schema, doc); ok {
				if cerr := note.Validate(noteKind(p.kind), coerced); cerr == nil {
					doc, err = coerced, nil
					p.state.Warnings = append(p.state.Warnings,
						"the agent wrote null where the schema wants a string, read as empty: "+strings.Join(nulled, ", "))
				}
			}
		}
	}
	if err == nil {
		ex.final = append([]byte(nil), doc...)
		ex.rawFinal = ""
		// Write everything the run exists to produce before waiting on
		// anything else, then end the session on purpose instead of
		// hoping it ends by itself.
		// The answer is in; what happens to the process now is
		// endSession's business, and a provider that takes its time
		// exiting is not a stalled run.
		ex.stall.stop()
		ex.row, ex.completeErr = r.complete(p, ex.final)
		p.state.UpdatedAt = r.now()
		if werr := p.run.WriteState(p.state); werr != nil {
			fmt.Fprintf(r.stderr(), "[%s] state: %v\n", p.state.Key, werr)
		}
		r.endSession(p, sess)
		return
	}

	ex.rawFinal = string(doc)
	empty := errors.Is(err, errEmptyAnswer)
	switch {
	case empty && ex.emptyTurns >= maxEmptyTurns:
		// Nothing was ever validated, so calling this a schema failure
		// would name the wrong problem. The provider's own last words go
		// in the reason when it had any: "the agent ended the turn
		// without an answer" is true of a CLI that failed the run for
		// answering in prose, and says nothing an operator can act on.
		ex.schemaError = firstProblem(err)
		ex.failure = errEmptyAnswer.Error()
		if ex.finalNarration != "" {
			ex.failure += ": " + firstLine(ex.finalNarration)
		}
		sess.Cancel()
		return
	case !empty && ex.retried:
		ex.schemaError = firstProblem(err)
		ex.failure = "schema validation failed twice: " + ex.schemaError
		sess.Cancel()
		return
	}

	if empty {
		ex.emptyTurns++
	} else {
		ex.retried = true
	}
	ex.schemaError = firstProblem(err)
	msg := "Your previous answer did not match the schema: " +
		strings.ReplaceAll(strings.TrimSpace(err.Error()), "\n", "; ") +
		". Reply again with the corrected JSON object only."
	if empty {
		msg = "Your previous turn ended without an answer. A refused tool call does not end the " +
			"task: carry on from what you have already done, and finish by replying with the JSON " +
			"object only."
	}
	if p.kind == store.KindFix {
		// The session has been editing files and running commands for
		// several turns by now, and the report shape was last stated in
		// the opening prompt. Restating it is cheaper than a second
		// failed answer.
		msg += " The fix report is one JSON object matching this schema: " +
			string(compactJSON(schemaFor(p.kind)))
	}
	if noWireSchemaEnforcement[r.providerName()] {
		// This provider has nothing enforcing OutputSchema on the wire, so
		// the retry has to spell out the exact mistake it is likely to
		// repeat: quoting the schema's own header back instead of the
		// answer it describes.
		msg += " Reply with the JSON object only: no `$schema`, no `title`, no surrounding text or code fence."
	}
	sendErr := sess.Send(ctx, msg)
	if sendErr == nil {
		return
	}

	// A Codex session ends its event stream on the completed turn, and a
	// `claude -p` session that has been cancelled or has already closed
	// its input is equally past taking another message, so by the time a
	// note fails validation there is often no session left to answer on.
	// Carry the retry into a fresh session against the same provider
	// handle instead of throwing the run away over it.
	//
	// The guard is held across Provider.Start: starting a fresh process is
	// not silence from a live session, and a start slow enough to cross
	// the stall window must not fail a run that is about to carry on
	// normally. consume rearms it once the retry session is live; on
	// failure here the run is ending anyway; held is where it stays.
	ex.stall.hold()
	next, startErr := r.resumeForRetry(ctx, p, sess, msg)
	if startErr != nil {
		ex.failure = fmt.Sprintf("the schema retry could not be sent: %v; resuming for it failed: %v", sendErr, startErr)
		sess.Cancel()
		return
	}
	fmt.Fprintf(r.stderr(), "[%s] schema retry in a resumed session\n", p.state.Key)
	ex.retrySession = next
}

// endSession brings a session that has given its answer to a close. It
// tells the provider no further message is coming — a CLI reading
// stream-json on stdin holds its stdout open until it knows that — and
// cancels the process outright if it is still running closeGrace later.
// The timer is left to fire on its own goroutine: Cancel on an exited
// session is harmless, and the run must not sit here waiting for it.
func (r *Runner) endSession(p *prepared, sess provider.Session) {
	if err := sess.CloseInput(); err != nil {
		fmt.Fprintf(r.stderr(), "[%s] close session input: %v\n", p.state.Key, err)
	}
	time.AfterFunc(r.grace(), sess.Cancel)
}

// resumeForRetry starts a new session that continues the finished one,
// opening with the retry message. It reports an error when the provider has
// no handle to resume from, because a fresh session without one would start
// the whole triage again on a budget meant for a single answer.
func (r *Runner) resumeForRetry(ctx context.Context, p *prepared, sess provider.Session, msg string) (provider.Session, error) {
	handle := sess.Handle()
	if handle == "" {
		return nil, fmt.Errorf("the session reported no handle to resume")
	}
	spec := r.sessionSpec(p, handle)
	spec.Prompt = msg
	return r.Provider.Start(ctx, spec)
}

// firstProblem summarises a validation error in one line: its header plus
// the first violation under it.
func firstProblem(err error) string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	head := strings.TrimSpace(lines[0])
	if len(lines) > 1 && strings.HasSuffix(head, ":") {
		return head + " " + strings.TrimSpace(lines[1])
	}
	return head
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// --- completion -------------------------------------------------------

// triageFields are the parts of a validated triage document the run itself
// reads: the rest is the template's business.
type triageFields struct {
	Title          string `json:"title"`
	Complaint      string `json:"complaint"`
	Classification string `json:"classification"`
	Ticket         struct {
		Service     string   `json:"service"`
		Customer    string   `json:"customer"`
		CustomerID  string   `json:"customerId"`
		CustomerIDs []string `json:"customerIds"`
	} `json:"ticket"`
	RootCause struct {
		Confidence string `json:"confidence"`
	} `json:"rootCause"`
}

// rcaFields are the parts of a validated rca document the run itself reads.
type rcaFields struct {
	RCA struct {
		Title          string `json:"title"`
		Summary        string `json:"summary"`
		Classification string `json:"classification"`
		Severity       string `json:"severity"`
		Confidence     string `json:"confidence"`
		TriageReview   struct {
			Verdict string `json:"verdict"`
		} `json:"triageReview"`
		PlaybookSuggestions []struct {
			Playbook string `json:"playbook"`
			Addition string `json:"addition"`
			Reason   string `json:"reason"`
		} `json:"playbookSuggestions"`
	} `json:"rca"`
	Resolution struct {
		Title          string `json:"title"`
		ResolutionType string `json:"resolutionType"`
	} `json:"resolution"`
}

// complete writes the validated document and the notes it renders into.
func (r *Runner) complete(p *prepared, doc []byte) (note.DigestRow, error) {
	if err := os.WriteFile(filepath.Join(p.run.Dir, "result.json"), doc, 0o644); err != nil {
		return note.DigestRow{}, fmt.Errorf("run: write result.json: %w", err)
	}
	switch p.kind {
	case store.KindRCA:
		return r.completeRCA(p, doc)
	case store.KindFix:
		return r.completeFix(p, doc)
	default:
		return r.completeTriage(p, doc)
	}
}

func (r *Runner) completeTriage(p *prepared, doc []byte) (note.DigestRow, error) {
	cfg := r.Config
	var f triageFields
	if err := json.Unmarshal(doc, &f); err != nil {
		return note.DigestRow{}, fmt.Errorf("run: parse triage note: %w", err)
	}

	key := p.state.Key
	slug := note.Slug(f.Title)
	filename := note.Filename(cfg.Notes.Filenames.Triage, key, slug)

	meta := r.meta(p, f.Ticket.Service)
	r.applyDocumentCustomer(p, &meta, f.Ticket.Customer, f.Ticket.CustomerID)
	meta.CustomerIDs = strings.Join(f.Ticket.CustomerIDs, ", ")
	meta.Links.Triage = stem(filename)
	meta.Links.RCA = stem(note.Filename(cfg.Notes.Filenames.RCA, key, slug))
	meta.Links.Resolution = stem(note.Filename(cfg.Notes.Filenames.Resolution, key, slug))

	body, err := r.renderer().Render(note.Triage, doc, meta)
	if err != nil {
		return note.DigestRow{}, err
	}
	notePath, err := r.writeNote(p, note.Triage, filename, body)
	if err != nil {
		return note.DigestRow{}, err
	}

	r.appendRegister(p, store.RegisterRow{
		Kind:           string(note.Triage),
		Date:           meta.Date,
		Service:        f.Ticket.Service,
		Classification: f.Classification,
		Confidence:     f.RootCause.Confidence,
		NotePath:       notePath,
		Title:          strings.TrimSpace(f.Title),
		Company:        registerCompany(body, meta.Customer),
	})

	return note.DigestRow{
		Issue:          issueLine(f.Title, f.Complaint),
		Confidence:     f.RootCause.Confidence,
		Classification: f.Classification,
	}, nil
}

func (r *Runner) completeRCA(p *prepared, doc []byte) (note.DigestRow, error) {
	cfg := r.Config
	var f rcaFields
	if err := json.Unmarshal(doc, &f); err != nil {
		return note.DigestRow{}, fmt.Errorf("run: parse rca note: %w", err)
	}

	key := p.state.Key
	rcaName := note.Filename(cfg.Notes.Filenames.RCA, key, note.Slug(f.RCA.Title))
	resName := note.Filename(cfg.Notes.Filenames.Resolution, key, note.Slug(f.Resolution.Title))

	// The rca document carries no ticket block, so the service comes from
	// the triage note this run reviews.
	meta := r.meta(p, r.triageService(p))
	meta.Links.RCA = stem(rcaName)
	meta.Links.Resolution = stem(resName)
	meta.Links.Triage = p.triageLink
	if meta.Links.Triage == "" {
		meta.Links.Triage = stem(note.Filename(cfg.Notes.Filenames.Triage, key, note.Slug(f.RCA.Title)))
	}

	rend := r.renderer()
	rcaBody, err := rend.Render(note.RCA, doc, meta)
	if err != nil {
		return note.DigestRow{}, err
	}
	resBody, err := rend.Render(note.Resolution, doc, meta)
	if err != nil {
		return note.DigestRow{}, err
	}

	rcaPath, err := r.writeNote(p, note.RCA, rcaName, rcaBody)
	if err != nil {
		return note.DigestRow{}, err
	}
	resPath, err := r.writeNote(p, note.Resolution, resName, resBody)
	if err != nil {
		return note.DigestRow{}, err
	}

	if err := r.writePlaybookSuggestions(p, f); err != nil {
		return note.DigestRow{}, err
	}

	// The triage note is now resolved and links to both new notes — in
	// its frontmatter and in its body, which until now kept the names the
	// triage run predicted from its own title. An rca that retitles the
	// issue files under a different slug, and the body's links then
	// pointed at notes nobody wrote while the frontmatter was right.
	links := map[string]string{
		"rca":        wikiLink(meta.Links.RCA),
		"resolution": wikiLink(meta.Links.Resolution),
	}
	relinks := []note.Relink{
		{Pattern: cfg.Notes.Filenames.RCA, Key: key, Stem: meta.Links.RCA},
		{Pattern: cfg.Notes.Filenames.Resolution, Key: key, Stem: meta.Links.Resolution},
	}
	for _, path := range []string{p.triageNotePath, p.triageNoteCopy} {
		if path == "" {
			continue
		}
		if err := note.UpdateTriageStatus(path, "resolved", links, relinks...); err != nil {
			p.state.Warnings = append(p.state.Warnings, fmt.Sprintf("triage note %s was not updated: %v", path, err))
		}
	}

	company := registerCompany(rcaBody, meta.Customer)
	r.appendRegister(p, store.RegisterRow{
		Kind:           string(note.RCA),
		Date:           meta.Date,
		Service:        meta.Service,
		Classification: f.RCA.Classification,
		Confidence:     f.RCA.Confidence,
		Severity:       f.RCA.Severity,
		TriageVerdict:  f.RCA.TriageReview.Verdict,
		NotePath:       rcaPath,
		Title:          strings.TrimSpace(f.RCA.Title),
		Company:        company,
	})
	r.appendRegister(p, store.RegisterRow{
		Kind:           string(note.Resolution),
		Date:           meta.Date,
		Service:        meta.Service,
		Classification: f.Resolution.ResolutionType,
		NotePath:       resPath,
		// No Title: the resolution note is titled after the fix, and the
		// register's title is the issue's — which is the triage note's,
		// refined by the rca's.
		Company: company,
	})

	return note.DigestRow{
		Issue:          firstSentence(f.RCA.Summary),
		Confidence:     f.RCA.Confidence,
		Classification: f.RCA.Classification,
	}, nil
}

// registerCompany is the company the register records for a note: the
// note's own `company` frontmatter key when a workspace template writes
// one, then `customer`, which the built-in templates do write, and finally
// whatever the run resolved the customer to be. It is read off the
// rendered note rather than off the document, so a workspace whose
// template names the company somewhere Sirdar does not model still gets it
// into the export.
func registerCompany(body, fallback string) string {
	for _, key := range []string{"company", "customer"} {
		if v := strings.TrimSpace(note.Frontmatter(body, key)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(fallback)
}

// writeNote writes the rendered note into the run directory and, unless the
// notes directory refuses it, files a copy there. It returns the path a
// human should open.
func (r *Runner) writeNote(p *prepared, kind note.Kind, filename, body string) (string, error) {
	name := "note.md"
	if kind == note.Resolution {
		name = "note-resolution.md"
	}
	runPath := filepath.Join(p.run.Dir, name)
	if err := os.WriteFile(runPath, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("run: write %s: %w", name, err)
	}
	p.state.Notes = append(p.state.Notes, runPath)

	// An eval replay's note is a measurement of the agent, not a record of
	// a ticket: filing it would overwrite the note a human wrote for the
	// same key and reads in their vault.
	if p.state.Eval {
		return runPath, nil
	}

	filed := r.fileNote(p, kind, filename, body)
	if filed == "" {
		return runPath, nil
	}
	p.state.Notes = append(p.state.Notes, filed)
	return filed, nil
}

// fileNote copies a note into the workspace's notes directory, creating
// filename's parent directory when the configured pattern files it into a
// subdirectory (e.g. "Triage/{key} {slug}.md"). A key's triage note is
// looked up by key rather than by filename, because a re-triage often
// retitles the issue: the existing note is overwritten in place while its
// status is still "triaged", and left alone with a warning once a human has
// moved it on.
func (r *Runner) fileNote(p *prepared, kind note.Kind, filename, body string) string {
	dir := r.Config.ExpandPath(r.Config.Notes.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		p.state.Warnings = append(p.state.Warnings, fmt.Sprintf("notes directory %s: %v", dir, err))
		return ""
	}
	path := filepath.Join(dir, filename)

	if kind == note.Triage {
		if existing := r.existingTriageNote(p, dir); existing != "" {
			status := ""
			if data, err := os.ReadFile(existing); err == nil {
				status = frontmatterValue(string(data), "status")
			}
			if status != "triaged" {
				p.state.Warnings = append(p.state.Warnings,
					fmt.Sprintf("%s has status %q and was left unchanged", existing, status))
				return ""
			}
			// Keep the filename the vault already links to.
			path = existing
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.state.Warnings = append(p.state.Warnings, fmt.Sprintf("notes directory %s: %v", filepath.Dir(path), err))
		return ""
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		p.state.Warnings = append(p.state.Warnings, fmt.Sprintf("write %s: %v", path, err))
		return ""
	}
	return path
}

// existingTriageNote finds this key's triage note in the notes directory,
// whatever it is called or however deep the configured filename pattern
// files it. It prefers the path the last completed triage run recorded, and
// falls back to walking dir recursively — skipping dot-directories such as
// .obsidian — for a "<key> *.md" file whose frontmatter tags it as triage,
// which is how a note written by hand, filed before the run state existed,
// or filed into a pattern subdirectory such as Triage/ is still found.
func (r *Runner) existingTriageNote(p *prepared, dir string) string {
	states, err := store.List(r.Config.Root, p.state.Key)
	if err == nil {
		for _, s := range states {
			if s.Kind != store.KindTriage || s.Status != store.StatusCompleted || s.RunID == p.state.RunID || s.Eval {
				continue
			}
			for _, path := range s.Notes {
				if !underDir(path, dir) {
					continue
				}
				if _, err := os.Stat(path); err == nil {
					return path
				}
			}
		}
	}

	prefix := p.state.Key + " "
	var found string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable entry just isn't a candidate
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(d.Name(), prefix) || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(frontmatterValue(string(data), "tags"), string(note.Triage)) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// underDir reports whether path is dir itself or lies somewhere beneath it.
func underDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// writePlaybookSuggestions leaves the agent's playbook additions in the run
// directory as a ready-to-paste block. Sirdar never edits a playbook.
func (r *Runner) writePlaybookSuggestions(p *prepared, f rcaFields) error {
	var b strings.Builder
	b.WriteString("# Playbook suggestions\n")
	if len(f.RCA.PlaybookSuggestions) == 0 {
		b.WriteString("\n(none)\n")
	}
	for _, s := range f.RCA.PlaybookSuggestions {
		b.WriteString("\n## " + s.Playbook + "\n\n" + strings.TrimRight(s.Addition, "\n") + "\n")
		if s.Reason != "" {
			b.WriteString("\nWhy: " + s.Reason + "\n")
		}
	}
	path := filepath.Join(p.run.Dir, "playbook-suggestions.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("run: write playbook suggestions: %w", err)
	}
	return nil
}

// appendRegister fills in the fields every register row shares and appends
// it. A register that cannot be written is a warning, not a failed run:
// the notes are already on disk.
func (r *Runner) appendRegister(p *prepared, row store.RegisterRow) {
	// The register is the audit index of tickets that were worked. An
	// eval replayed a stored bundle to score a prompt change, so it has
	// nothing to add to it; its own report is .sirdar/eval/<stamp>.json.
	if p.state.Eval {
		return
	}
	row.Key = p.state.Key
	row.RunID = p.state.RunID
	row.Provider = p.state.Provider
	row.Model = p.state.Model
	row.Turns = p.state.Usage.Turns
	row.CostUSD = p.state.Usage.CostUSD
	if p.service == "" {
		p.service = row.Service
	}
	if p.notePath == "" {
		p.notePath = row.NotePath
	}
	if err := store.AppendRegister(r.Config.Root, row); err != nil {
		p.state.Warnings = append(p.state.Warnings, fmt.Sprintf("register: %v", err))
	}
}

// meta carries the identifiers, dates and run details a note's frontmatter
// needs but the validated document does not hold.
func (r *Runner) meta(p *prepared, service string) note.Meta {
	b := p.bundle
	m := note.Meta{
		Key:          p.state.Key,
		Date:         r.now().Format(dateLayout),
		DateReported: bundleDateReported(b),
		Priority:     bundlePriority(b),
		Service:      service,
		RunID:        p.state.RunID,
		Provider:     p.state.Provider,
		At:           p.state.At,
	}
	if b.Tracker != nil {
		m.TrackerURL = b.Tracker.URL
	}
	if b.Helpdesk != nil {
		m.HelpdeskID = b.Helpdesk.ID
		m.HelpdeskURL = b.Helpdesk.URL
		m.Customer = b.Helpdesk.Customer
		m.CustomerID = b.Helpdesk.CustomerID
	}
	for _, a := range b.SkippedAttachments {
		m.SkippedAttachments = append(m.SkippedAttachments, note.SkippedAttachment{
			Name: a.Name, Type: a.Type, Size: a.Size, Reason: a.Reason,
		})
	}
	return m
}

// applyDocumentCustomer lets the note say who the customer is. The
// helpdesk's account fields are only where the ticket was filed: an
// agent that has read the thread, the attachments and the logs routinely
// resolves a different company, or the id the helpdesk never held, and the
// frontmatter used to carry the bundle's answer regardless. The
// document's value wins when it has one; the bundle's is the fallback.
//
// A disagreement is not silently resolved: the bundle's value goes into
// the run's warnings, so state.json still says what the helpdesk claimed
// and the two can be compared later. The prompt now tells the agent to
// copy ticket.customer verbatim (see prompt.triageFieldGuidance and
// customerName in internal/prompt), but an older note, or a provider that
// does not follow it, may still carry an appended domain, CompanyID or
// company code — "Acme Corp (3521)" against a bundle of "Acme Corp - 3521".
// customer, unlike customer_id, is compared after stripping that kind of
// suffix, so a name that only disagrees with the bundle by such an
// identifier is not warned about.
func (r *Runner) applyDocumentCustomer(p *prepared, m *note.Meta, customer, customerID string) {
	for _, f := range []struct {
		field     string
		fromDoc   string
		into      *string // holds the bundle's value on the way in
		normalize bool
	}{
		{"customer", strings.TrimSpace(customer), &m.Customer, true},
		{"customer_id", strings.TrimSpace(customerID), &m.CustomerID, false},
	} {
		if f.fromDoc == "" {
			continue
		}
		bundle := *f.into
		disagrees := bundle != "" && bundle != f.fromDoc
		if disagrees && f.normalize && normalizeCustomerName(bundle) == normalizeCustomerName(f.fromDoc) {
			disagrees = false
		}
		if disagrees {
			p.state.Warnings = append(p.state.Warnings, fmt.Sprintf(
				"frontmatter %s: the note says %q, the helpdesk bundle says %q; the note's value was used",
				f.field, f.fromDoc, bundle))
		}
		*f.into = f.fromDoc
	}
}

// normalizeCustomerName strips an identifier a source appended to a
// customer name — " (3521)", " (domain 3521)", " - 3521" — so two spellings
// of the same base name compare equal. It cuts at the first " (" or " - "
// and trims what remains, case-insensitively.
func normalizeCustomerName(s string) string {
	s = strings.TrimSpace(s)
	cut := len(s)
	if i := strings.Index(s, " ("); i >= 0 && i < cut {
		cut = i
	}
	if i := strings.Index(s, " - "); i >= 0 && i < cut {
		cut = i
	}
	return strings.ToLower(strings.TrimSpace(s[:cut]))
}

func (r *Runner) renderer() note.Renderer {
	dir := ""
	if r.Config.Notes.Templates != "" {
		dir = r.Config.ExpandPath(r.Config.Notes.Templates)
	}
	rtl := r.Config.RTLMarkup()
	return note.Renderer{TemplatesDir: dir, RTLMarkup: &rtl}
}

// triageService reads the service out of the triage note this rca run
// reviews, so both notes file under the same service.
func (r *Runner) triageService(p *prepared) string {
	if p.triageNotePath == "" {
		return ""
	}
	data, err := os.ReadFile(p.triageNotePath)
	if err != nil {
		return ""
	}
	return frontmatterValue(string(data), "service")
}

// frontmatterValue returns the value of key in the note's leading YAML
// frontmatter block, or "" when there is no such block or key.
func frontmatterValue(text, key string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	prefix := key + ":"
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return ""
		}
		if strings.HasPrefix(line, prefix) {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), `"'`)
		}
	}
	return ""
}

func stem(filename string) string { return strings.TrimSuffix(filename, ".md") }

func wikiLink(stem string) string {
	if stem == "" {
		return ""
	}
	return `"[[` + stem + `]]"`
}

// firstSentence is the digest's one-line summary of a complaint: up to and
// including the first full stop. Fitting it to the table is note.Digest's
// job, not this package's.
func firstSentence(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return strings.TrimSpace(s[:i+1])
	}
	return s
}

// issueLine is the digest's ISSUE column for a triage row: the note's own
// title when the agent wrote one, and otherwise the first sentence of the
// complaint that says something about the issue.
//
// The title comes first because it is the one line written to describe the
// problem. The complaint is the customer's own words, and a support ticket
// opens with a greeting: a live digest's ISSUE column read "Peace be upon
// you." for every row of an Arabic thread, which is the sentence
// firstSentence lands on and tells the reader nothing.
func issueLine(title, complaint string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	return firstIssueSentence(complaint)
}

// greetingOpeners are the salutations a ticket opens with before it says
// anything, in the two languages this workspace reads. A sentence counts
// as a greeting only when it starts with one of them, is shorter than
// greetingMaxWords, and ends inside the complaint's first line — all
// three, because "Hello, the export has failed every night since Tuesday"
// is the complaint and "Peace be upon you." is not.
var greetingOpeners = []string{
	"السلام عليكم", "سلام عليكم", "وعليكم السلام", "عليكم السلام",
	"صباح الخير", "مساء الخير", "تحية طيبة", "أهلا", "اهلا", "أهلاً",
	"مرحبا", "مرحباً", "السلام",
	"peace be upon you", "peace upon you", "assalamu alaikum", "as-salamu alaykum",
	"salam alaikum", "salaam", "salam", "hello", "hi", "hey", "dear", "greetings",
	"good morning", "good afternoon", "good evening", "good day",
}

// greetingMaxWords is the length a greeting stays under. A sentence that
// opens with "Hello" and runs to six words is carrying content.
const greetingMaxWords = 6

// firstIssueSentence is the first sentence of a complaint that is not the
// opening greeting. When every sentence looks like one — a complaint that
// is nothing but a salutation — it falls back to firstSentence, so the
// column says what the customer wrote rather than going empty.
func firstIssueSentence(s string) string {
	text := strings.TrimSpace(s)
	if text == "" {
		return ""
	}
	firstLineEnd := len(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		firstLineEnd = i
	}
	for _, sen := range splitSentences(text) {
		if sen.end <= firstLineEnd+1 && isGreeting(sen.text) {
			continue
		}
		if trimmed := strings.TrimSpace(sen.text); trimmed != "" {
			return firstSentence(trimmed)
		}
	}
	return firstSentence(text)
}

// sentence is one sentence of a complaint and the offset just past its
// terminator, which is what says whether it ended on the first line.
type sentence struct {
	text string
	end  int
}

// splitSentences cuts text at the marks a sentence ends on, keeping the
// terminator with the sentence it closes. A newline counts: a ticket
// written as a list of lines has no full stops at all.
func splitSentences(text string) []sentence {
	var out []sentence
	start := 0
	for i, r := range text {
		if !isSentenceEnd(r) {
			continue
		}
		end := i + utf8.RuneLen(r)
		out = append(out, sentence{text: text[start:end], end: end})
		start = end
	}
	if start < len(text) {
		out = append(out, sentence{text: text[start:], end: len(text)})
	}
	return out
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', '\n', '؟', '۔':
		return true
	}
	return false
}

// isGreeting reports whether a sentence is a salutation and nothing else.
func isGreeting(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return true
	}
	if len(strings.Fields(trimmed)) >= greetingMaxWords {
		return false
	}
	lower := strings.ToLower(strings.TrimLeftFunc(trimmed, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}))
	for _, opener := range greetingOpeners {
		if strings.HasPrefix(lower, opener) {
			return true
		}
	}
	return false
}

// stallTimeout is how long this run tolerates silence from the provider:
// the Runner's override when it has one, else budget.stallMinutes. Zero,
// from either, turns the check off.
func (r *Runner) stallTimeout() time.Duration {
	if r.StallTimeout != 0 {
		return r.StallTimeout
	}
	if r.Config == nil {
		return 0
	}
	return time.Duration(r.Config.StallMinutes()) * time.Minute
}

// grace is how long endSession waits before cancelling, taking the
// Runner's override when it has one.
func (r *Runner) grace() time.Duration {
	if r.CloseGrace > 0 {
		return r.CloseGrace
	}
	return closeGrace
}

// errEmptyAnswer is the failure of a turn that ended with no answer in it:
// the provider reported a final with neither JSON nor text, which is what a
// session spent entirely on tool calls and thinking looks like from here.
var errEmptyAnswer = errors.New("the agent ended the turn without an answer")

// compactJSON strips the whitespace out of a schema before it goes into a
// retry message, and hands back what it was given when that fails — the
// point is to send the schema, not to validate it here.
func compactJSON(raw []byte) []byte {
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return raw
	}
	return out.Bytes()
}
