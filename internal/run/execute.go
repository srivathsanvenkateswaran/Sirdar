package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// maxMalformed is how many malformed provider lines in a row end the run.
const maxMalformed = 10

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
	schemaError string
	malformed   int

	// retrySession is set when the schema retry could not be sent on the
	// running session and a fresh one was started to carry it; consume
	// switches to it and keeps going. live is the session being read from
	// right now, which the wall-clock timer cancels from its own goroutine.
	retrySession provider.Session
	live         liveSession

	question    string
	rateLimited bool
	resetsAt    time.Time

	overBudget  string
	interrupted bool
	failure     string
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

	sessions := r.consume(ctx, p, sess, log, pl, ex)

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
	spec := provider.SessionSpec{
		Cwd:          cfg.Root,
		Prompt:       p.promptText,
		Model:        p.state.Model,
		OutputSchema: schemaFor(p.kind),
		Policy: &provider.PermissionPolicy{
			BashAllow: cfg.Permissions.Bash,
			MCPAllow:  cfg.Permissions.MCP,
			Root:      cfg.Root,
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
		spec.Policy = provider.FixPolicy(cfg.Root, cfg.Permissions.FixBash, cfg.Permissions.MCP,
			extraReserved(cfg.Root))
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
			sess.Cancel()
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
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
			sess.Cancel()
		case b.MaxTurns > 0 && u.Turns > b.MaxTurns:
			ex.overBudget = fmt.Sprintf("%d turns exceeded the %d turn budget", u.Turns, b.MaxTurns)
			sess.Cancel()
		}

	case provider.EvRateLimited:
		ex.rateLimited = true
		ex.resetsAt = ev.ResetsAt
		pl.pause(ev.ResetsAt)

	case provider.EvQuestion:
		ex.question = ev.Text

	case provider.EvError:
		ex.malformed++
		if ex.malformed >= maxMalformed {
			ex.failure = fmt.Sprintf("%d malformed provider lines in a row: %s", ex.malformed, firstLine(ev.Text))
			sess.Cancel()
		}

	case provider.EvFinal:
		r.handleFinal(ctx, p, sess, ex, ev)
	}
}

// handleFinal validates the session's JSON output. The first failure buys
// one retry turn quoting the validation errors; the second ends the run.
func (r *Runner) handleFinal(ctx context.Context, p *prepared, sess provider.Session, ex *execution, ev provider.Event) {
	// A session that has already produced a valid note is done. A provider
	// that emits a second final line — a resumed session replaying its
	// result, a CLI that repeats itself on the way out — must not file the
	// note twice and hand the register a duplicate row.
	if len(ex.final) > 0 {
		return
	}

	doc := []byte(ev.Final)
	if len(doc) == 0 {
		doc = []byte(strings.TrimSpace(ev.Text))
	}

	err := note.Validate(noteKind(p.kind), doc)
	if err == nil {
		ex.final = append([]byte(nil), doc...)
		ex.rawFinal = ""
		// Write everything the run exists to produce before waiting on
		// anything else, then end the session on purpose instead of
		// hoping it ends by itself.
		ex.row, ex.completeErr = r.complete(p, ex.final)
		p.state.UpdatedAt = r.now()
		if werr := p.run.WriteState(p.state); werr != nil {
			fmt.Fprintf(r.stderr(), "[%s] state: %v\n", p.state.Key, werr)
		}
		r.endSession(p, sess)
		return
	}

	ex.rawFinal = string(doc)
	if ex.retried {
		ex.schemaError = firstProblem(err)
		ex.failure = "schema validation failed twice: " + ex.schemaError
		sess.Cancel()
		return
	}

	ex.retried = true
	ex.schemaError = firstProblem(err)
	msg := "Your previous answer did not match the schema: " +
		strings.ReplaceAll(strings.TrimSpace(err.Error()), "\n", "; ") +
		". Reply again with the corrected JSON object only."
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
		Service    string `json:"service"`
		Customer   string `json:"customer"`
		CustomerID string `json:"customerId"`
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
	})

	return note.DigestRow{
		Issue:          firstSentence(f.Complaint),
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

	// The triage note is now resolved and links to both new notes.
	links := map[string]string{
		"rca":        wikiLink(meta.Links.RCA),
		"resolution": wikiLink(meta.Links.Resolution),
	}
	for _, path := range []string{p.triageNotePath, p.triageNoteCopy} {
		if path == "" {
			continue
		}
		if err := note.UpdateTriageStatus(path, "resolved", links); err != nil {
			p.state.Warnings = append(p.state.Warnings, fmt.Sprintf("triage note %s was not updated: %v", path, err))
		}
	}

	r.appendRegister(p, store.RegisterRow{
		Kind:           string(note.RCA),
		Date:           meta.Date,
		Service:        meta.Service,
		Classification: f.RCA.Classification,
		Confidence:     f.RCA.Confidence,
		Severity:       f.RCA.Severity,
		TriageVerdict:  f.RCA.TriageReview.Verdict,
		NotePath:       rcaPath,
	})
	r.appendRegister(p, store.RegisterRow{
		Kind:           string(note.Resolution),
		Date:           meta.Date,
		Service:        meta.Service,
		Classification: f.Resolution.ResolutionType,
		NotePath:       resPath,
	})

	return note.DigestRow{
		Issue:          firstSentence(f.RCA.Summary),
		Confidence:     f.RCA.Confidence,
		Classification: f.RCA.Classification,
	}, nil
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
// and the two can be compared later.
func (r *Runner) applyDocumentCustomer(p *prepared, m *note.Meta, customer, customerID string) {
	for _, f := range []struct {
		field   string
		fromDoc string
		into    *string // holds the bundle's value on the way in
	}{
		{"customer", strings.TrimSpace(customer), &m.Customer},
		{"customer_id", strings.TrimSpace(customerID), &m.CustomerID},
	} {
		if f.fromDoc == "" {
			continue
		}
		bundle := *f.into
		if bundle != "" && bundle != f.fromDoc {
			p.state.Warnings = append(p.state.Warnings, fmt.Sprintf(
				"frontmatter %s: the note says %q, the helpdesk bundle says %q; the note's value was used",
				f.field, f.fromDoc, bundle))
		}
		*f.into = f.fromDoc
	}
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

// grace is how long endSession waits before cancelling, taking the
// Runner's override when it has one.
func (r *Runner) grace() time.Duration {
	if r.CloseGrace > 0 {
		return r.CloseGrace
	}
	return closeGrace
}
