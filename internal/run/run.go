// Package run drives a Sirdar run from end to end: it prepares the ticket
// bundle and prompt, executes one provider session against them, validates
// and files the notes the session produced, and records the run's state.
// It is the one place where config, sources, prompt assembly, the provider
// and the note packages meet.
package run

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Deps is everything a Runner needs from the program around it. Tracker and
// Helpdesk may be nil when the workspace configures only one of them; Now,
// Stderr and Env fall back to time.Now, io.Discard and os.Environ.
type Deps struct {
	Config   *config.Config
	Tracker  source.Tracker  // may be nil
	Helpdesk source.Helpdesk // may be nil
	Provider provider.Provider
	Creds    config.Resolver
	Now      func() time.Time
	Stderr   io.Writer // progress lines
	Stdin    io.Reader // for resume answers
	Env      []string  // base child env (os.Environ())
}

// Options are the per-invocation flags shared by triage and rca runs.
type Options struct {
	Model       string
	Concurrency int
	DryRun      bool
}

// RCAOptions adds the two inputs only an rca run takes: the merged pull
// request to read, and the engineer's account of the resolution.
type RCAOptions struct {
	Options
	PRURL      string
	Resolution string
}

// Outcome is one run's result: its final state and the digest line the CLI
// prints for it.
type Outcome struct {
	Key    string
	State  store.State
	Digest note.DigestRow
}

// Runner executes runs against one workspace.
type Runner struct {
	Deps

	// CloseGrace overrides how long a session that has produced its note
	// is given to exit on its own before it is cancelled. Zero means the
	// closeGrace default.
	CloseGrace time.Duration

	// onPause, when set, is called every time a rate-limit pause is
	// recorded, so a test can synchronise on it. Production leaves it nil.
	onPause func(time.Time)
}

// ExitCode reports the process exit status for a set of outcomes: 1 when
// any run failed or ran over budget, else 0. A blocked run is resumable,
// not a failure.
func ExitCode(outs []Outcome) int {
	for _, o := range outs {
		if o.State.Status == store.StatusFailed || o.State.Status == store.StatusOverBudget {
			return 1
		}
	}
	return 0
}

const skippedReason = "skipped: interrupted before this run started"

// skipped is the outcome of a key the interrupt reached before any work
// was done for it. Nothing was written, so there is no run to resume; the
// digest reports it under the reasons block alongside the blocked runs.
func skipped(key string) Outcome {
	return Outcome{
		Key:    key,
		State:  store.State{Key: key, Kind: store.KindTriage, Status: store.StatusBlocked, Reason: skippedReason},
		Digest: note.DigestRow{Key: key, State: string(store.StatusBlocked), Reason: skippedReason},
	}
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) stderr() io.Writer {
	if d.Stderr != nil {
		return d.Stderr
	}
	return io.Discard
}

// childEnv is the environment the agent process runs with: the caller's
// base environment, minus every variable a configured source names as a
// credential, plus the billing mode, which tells a provider adapter whether
// to leave an API key in place.
//
// The agent has no business holding the helpdesk's token: Sirdar reads the
// tickets itself and hands the agent the bundle. Leaving the variable in
// place would put a live support-desk credential inside a session that runs
// shell commands.
func (d Deps) childEnv() []string {
	base := d.Env
	if base == nil {
		base = os.Environ()
	}
	secret := credentialEnvNames(d.Config)
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if name, _, ok := strings.Cut(entry, "="); ok && secret[name] {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "SIRDAR_BILLING="+d.Config.Billing)
}

// credentialEnvNames collects the variable names behind every "env:"
// credential reference in the workspace configuration — the sources' and
// the model endpoint's alike. An OAuth grant's
// client secret and refresh token are longer-lived than the access token a
// static token: ref holds, so they matter here more, not less: a refresh
// token read out of the agent's environment mints access tokens until
// somebody revokes it at the Zoho console. A built-in tracker's or
// helpdesk's apiToken, pat, apiKey or oauthToken is stripped for the same
// reason: the agent reads the tickets Sirdar hands it, never the source.
func credentialEnvNames(cfg *config.Config) map[string]bool {
	names := make(map[string]bool)
	if cfg == nil {
		return names
	}
	refs := []string{}
	for _, s := range []*config.SourceConfig{cfg.Sources.Tracker, cfg.Sources.Helpdesk} {
		if s == nil {
			continue
		}
		refs = append(refs, s.Token, s.APIToken, s.PAT, s.APIKey, s.OAuthToken)
		if s.Auth != nil {
			refs = append(refs, s.Auth.ClientID, s.Auth.ClientSecret, s.Auth.RefreshToken)
		}
	}
	// The model's own API key belongs to Sirdar, not to the session: the
	// loop authenticates the chat calls itself, and a key left in the
	// environment would be readable by every allow-listed shell command
	// and every MCP server the run starts.
	if cfg.OpenAI != nil {
		refs = append(refs, cfg.OpenAI.APIKey)
	}
	for _, ref := range refs {
		if name, ok := strings.CutPrefix(ref, "env:"); ok && name != "" {
			names[name] = true
		}
	}
	return names
}

func (d Deps) providerName() string {
	if d.Provider == nil {
		return ""
	}
	return d.Provider.Name()
}

// Triage runs triage for every key, at most Concurrency at a time. Every
// key yields an Outcome in the order it was given, whether it succeeded or
// not; the error return is reserved for a failure that stops the whole
// command before any run starts.
func (r *Runner) Triage(ctx context.Context, keys []string, o Options) ([]Outcome, error) {
	if r.Config == nil {
		return nil, fmt.Errorf("run: no workspace configuration")
	}
	outs := make([]Outcome, len(keys))
	if len(keys) == 0 {
		return outs, nil
	}

	workers := o.Concurrency
	if workers <= 0 {
		workers = r.Config.Concurrency
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(keys) {
		workers = len(keys)
	}

	queue := make(chan int, len(keys))
	for i := range keys {
		queue <- i
	}
	close(queue)

	p := newPool(r.onPause)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				// An interrupt stops the queue: a key that never
				// started is skipped, not failed. That includes a key
				// still parked in a rate-limit pause when the
				// interrupt lands.
				select {
				case <-ctx.Done():
					outs[i] = skipped(keys[i])
					continue
				default:
				}
				if !p.waitUntilResumed(ctx) {
					outs[i] = skipped(keys[i])
					continue
				}
				outs[i], _ = r.runOne(ctx, keys[i], store.KindTriage, o, nil, p)
			}
		}()
	}
	wg.Wait()
	return outs, nil
}

// RCA produces the RCA note and the resolution draft for one key. It
// requires a completed triage run for that key and returns the preparation
// error, if any, alongside the failed outcome.
func (r *Runner) RCA(ctx context.Context, key string, o RCAOptions) (Outcome, error) {
	if r.Config == nil {
		return Outcome{}, fmt.Errorf("run: no workspace configuration")
	}
	return r.runOne(ctx, key, store.KindRCA, o.Options, &o, newPool(r.onPause))
}

// Resume continues a blocked or interrupted run: it reopens the run, asks
// the operator for an answer when the agent was waiting on a question, and
// starts a new session against the stored provider handle.
func (r *Runner) Resume(ctx context.Context, runID string) (Outcome, error) {
	if r.Config == nil {
		return Outcome{}, fmt.Errorf("run: no workspace configuration")
	}
	rn, state, err := store.Open(r.Config.Root, runID)
	if err != nil {
		return Outcome{}, err
	}
	bundle, err := readBundle(rn.BundleDir())
	if err != nil {
		return Outcome{}, err
	}

	p := &prepared{run: rn, state: state, kind: state.Kind, bundle: bundle}
	if p.kind == store.KindRCA {
		notePath, err := store.LatestNote(r.Config.Root, state.Key, store.KindTriage)
		if err != nil {
			return Outcome{}, fmt.Errorf("no triage note for %s; run triage first", state.Key)
		}
		p.triageNotePath = notePath
		p.triageNoteCopy, p.triageLink = triageNoteCopy(r.Config.Root, notePath)
	}

	// The re-run writes its notes afresh, so start from a clean list
	// rather than duplicating the paths the blocked attempt recorded.
	p.state.Notes = nil

	text, err := r.resumeText(state)
	if err != nil {
		return Outcome{}, err
	}
	p.promptText = text

	out := r.execute(ctx, p, state.Handle, nil)
	return out, nil
}

const resumeContinue = "Continue where you left off and produce the JSON note."

// resumeText is the message the resumed session opens with: the operator's
// answer when the run blocked on a question, else a plain nudge to finish.
func (r *Runner) resumeText(state store.State) (string, error) {
	const askedPrefix = "agent asked: "
	if !strings.HasPrefix(state.Reason, askedPrefix) {
		return resumeContinue, nil
	}
	question := strings.TrimPrefix(state.Reason, askedPrefix)
	fmt.Fprintf(r.stderr(), "[%s] the agent asked: %s\n[%s] answer: ", state.Key, question, state.Key)
	if r.Stdin == nil {
		return "", fmt.Errorf("run: %s is waiting on an answer but no input is available", state.RunID)
	}
	sc := bufio.NewScanner(r.Stdin)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", fmt.Errorf("run: read answer: %w", err)
		}
		return "", fmt.Errorf("run: no answer given for %s", state.RunID)
	}
	answer := strings.TrimSpace(sc.Text())
	if answer == "" {
		return "", fmt.Errorf("run: no answer given for %s", state.RunID)
	}
	return answer, nil
}

// runOne prepares and then executes a single run.
func (r *Runner) runOne(ctx context.Context, key string, kind store.Kind, o Options, rca *RCAOptions, pl *pool) (Outcome, error) {
	p, err := r.prepare(ctx, key, kind, o, rca)
	if err != nil {
		return r.prepareFailed(p, key, kind, err), err
	}
	if o.DryRun {
		return r.finish(p, store.StatusCompleted, "dry-run", note.DigestRow{}), nil
	}
	return r.execute(ctx, p, "", pl), nil
}

// prepareFailed records a run that never reached the agent. When the run
// directory itself could not be created there is nowhere to write state, so
// the outcome carries the reason on its own.
func (r *Runner) prepareFailed(p *prepared, key string, kind store.Kind, err error) Outcome {
	if p == nil {
		state := store.State{Key: key, Kind: kind, Status: store.StatusFailed, Reason: err.Error(), StartedAt: r.now()}
		return Outcome{Key: key, State: state, Digest: note.DigestRow{Key: key, State: string(store.StatusFailed), Reason: err.Error()}}
	}
	return r.finish(p, store.StatusFailed, err.Error(), note.DigestRow{})
}

// finish stamps the run's terminal status, persists it, and builds the
// outcome the CLI reports. row carries the fields only a completed run
// knows (issue, confidence, classification); the rest is filled in here.
func (r *Runner) finish(p *prepared, status store.Status, reason string, row note.DigestRow) Outcome {
	p.state.Status = status
	p.state.Reason = reason
	p.state.UpdatedAt = r.now()
	if err := p.run.WriteState(p.state); err != nil {
		fmt.Fprintf(r.stderr(), "[%s] state: %v\n", p.state.Key, err)
	}

	row.Key = p.state.Key
	row.State = string(status)
	row.RunID = p.state.RunID
	row.Reason = reason
	if row.Priority == "" {
		row.Priority = bundlePriority(p.bundle)
	}
	if row.Issue == "" {
		row.Issue = bundleTitle(p.bundle)
	}
	return Outcome{Key: p.state.Key, State: p.state, Digest: row}
}

// bundlePriority prefers the tracker's priority, which is the one the team
// triages by, and falls back to the helpdesk's.
func bundlePriority(b ticket.Bundle) string {
	if b.Tracker != nil && b.Tracker.Priority != "" {
		return b.Tracker.Priority
	}
	if b.Helpdesk != nil {
		return b.Helpdesk.Priority
	}
	return ""
}

func bundleTitle(b ticket.Bundle) string {
	if b.Tracker != nil && b.Tracker.Title != "" {
		return b.Tracker.Title
	}
	if b.Helpdesk != nil {
		return b.Helpdesk.Subject
	}
	return ""
}

func bundleDateReported(b ticket.Bundle) string {
	if b.Helpdesk != nil && !b.Helpdesk.CreatedAt.IsZero() {
		return b.Helpdesk.CreatedAt.Format(dateLayout)
	}
	if b.Tracker != nil && !b.Tracker.CreatedAt.IsZero() {
		return b.Tracker.CreatedAt.Format(dateLayout)
	}
	return ""
}

const dateLayout = "2006-01-02"

// readBundle reads back the bundle a prepared run wrote, so a resumed run
// can render notes without fetching the ticket again.
func readBundle(dir string) (ticket.Bundle, error) {
	data, err := os.ReadFile(filepath.Join(dir, "ticket.json"))
	if err != nil {
		return ticket.Bundle{}, fmt.Errorf("run: read bundle: %w", err)
	}
	var b ticket.Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return ticket.Bundle{}, fmt.Errorf("run: parse bundle: %w", err)
	}
	return b, nil
}
