package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

func init() { commands["shadow"] = cmdShadow }

const shadowUsage = `usage: sirdar shadow watch [--once]
       sirdar shadow run KEY... [--backfill]
       sirdar shadow status
       sirdar shadow report [--since 7d|2026-10-01] [--json]
       sirdar shadow outcome KEY [--pr URL]... [--resolution TEXT|@file]
       sirdar shadow grade [KEY...]
       sirdar shadow pause [--for 4h]
       sirdar shadow resume`

// shadowJobPoll is how often a command waiting on jobs looks at the job
// list even with no event: a job.finished dropped from a full subscriber
// buffer must not leave the command waiting for ever.
const shadowJobPoll = time.Second

// cmdShadow is shadow mode at the terminal: the headless watcher, shadow
// runs asked for by hand, manual outcomes and grading, and the scorecard.
func cmdShadow(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, shadowUsage)
		return exitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "watch":
		return cmdShadowWatch(rest, stdout, stderr)
	case "run":
		return cmdShadowRun(rest, stdout, stderr)
	case "status":
		return cmdShadowStatus(rest, stdout, stderr)
	case "report":
		return cmdShadowReport(rest, stdout, stderr)
	case "outcome":
		return cmdShadowOutcome(rest, stdout, stderr)
	case "grade":
		return cmdShadowGrade(rest, stdout, stderr)
	case "pause":
		return cmdShadowPause(rest, stdout, stderr)
	case "resume":
		return cmdShadowResume(rest, stdout, stderr)
	}
	fmt.Fprintf(stderr, "sirdar shadow: unknown subcommand %q\n", sub)
	fmt.Fprintln(stderr, shadowUsage)
	return exitUsage
}

// openWorkspaceService resolves the workspace the way serve does — the one
// the working directory is in, registered if it is not yet — and builds
// the service over the registry. The bool is false when the caller should
// give up; the message is already on stderr.
func openWorkspaceService(opts app.Options, stderr io.Writer) (*app.Service, string, bool) {
	root, ok := serveRoot("", stderr)
	if !ok {
		return nil, "", false
	}
	registryPath, err := app.DefaultRegistryPath()
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return nil, "", false
	}
	reg := &app.Registry{Path: registryPath}
	wsID, err := ensureRegistered(reg, root)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return nil, "", false
	}
	opts.Stderr = app.Synced(stderr)
	return app.New(reg, app.BuildDeps, opts), wsID, true
}

func cmdShadowWatch(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("shadow watch", stderr, "usage: sirdar shadow watch [--once]")
	once := fs.Bool("once", false, "run one tick, wait for the runs it started, print the status and exit")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}
	svc, wsID, ok := openWorkspaceService(app.Options{Shadow: true, ShadowBy: "watch"}, stderr)
	if !ok {
		return exitUsage
	}

	ctx, stop := interruptible()
	defer stop()
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	if *once {
		defer svc.Stop()
		_, err := svc.ShadowPoll(ctx, wsID)
		if errors.Is(err, app.ErrShadowLocked) {
			fmt.Fprintln(stderr, lockedMessage(err))
			return 1
		}
		if err != nil {
			fmt.Fprintf(stderr, "sirdar shadow watch: %v\n", err)
			return 1
		}
		if !waitForJobs(ctx, svc, events, nil) {
			fmt.Fprintln(stderr, "sirdar shadow watch: interrupted; the runs still going were stopped and can be resumed")
			return 1
		}
		// Stop waits for whatever the tick and its runs left going — a
		// collection pass, a run's closing republish — so the status
		// printed next is the settled one. The deferred Stop is then a
		// no-op.
		svc.Stop()
		st, err := svc.ShadowStatus(wsID)
		if err != nil {
			fmt.Fprintf(stderr, "sirdar shadow watch: %v\n", err)
			return 1
		}
		printShadowStatus(stdout, st)
		return 0
	}

	svc.Start(ctx)
	defer svc.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0
		case ev, open := <-events:
			if !open {
				return 0
			}
			if ev.Kind != app.KindShadowStatus || ev.WorkspaceID != wsID || ev.Shadow == nil {
				continue
			}
			st := ev.Shadow
			// Another process is this workspace's watcher; two would poll
			// the tracker twice and start every ticket twice.
			if st.State == "locked-elsewhere" {
				fmt.Fprintln(stderr, st.Reason)
				return 1
			}
			if st.Reason == "" {
				fmt.Fprintf(stdout, "shadow: %s\n", st.State)
			} else {
				fmt.Fprintf(stdout, "shadow: %s: %s\n", st.State, st.Reason)
			}
		}
	}
}

// lockedMessage is a refused poll as the operator reads it: who holds the
// lock, without the sentinel's own words in front.
func lockedMessage(err error) string {
	msg := strings.TrimPrefix(err.Error(), app.ErrShadowLocked.Error()+": ")
	if !strings.HasPrefix(msg, "shadow: ") {
		msg = "shadow: " + msg
	}
	return msg
}

// waitForJobs blocks until this process runs no job, or ctx ends. Every
// job.finished goes to onFinished when it is set. It reports whether the
// jobs ended rather than the wait.
func waitForJobs(ctx context.Context, svc *app.Service, events <-chan app.Event, onFinished func(app.Event)) bool {
	tick := time.NewTicker(shadowJobPoll)
	defer tick.Stop()
	for {
		if len(svc.Jobs()) == 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		case ev, open := <-events:
			if !open {
				events = nil
				continue
			}
			if ev.Kind == app.KindJobFinished && onFinished != nil {
				onFinished(ev)
			}
		}
	}
}

func cmdShadowRun(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("shadow run", stderr, "usage: sirdar shadow run KEY... [--backfill]")
	backfill := fs.Bool("backfill", false, "triage each ticket as it stood when it was created")
	keys, ok := parseFlags(fs, args, 1, -1, stderr)
	if !ok {
		return exitUsage
	}
	svc, wsID, ok := openWorkspaceService(app.Options{}, stderr)
	if !ok {
		return exitUsage
	}
	ctx, stop := interruptible()
	defer stop()
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()
	defer svc.Stop()

	started, err := svc.ShadowRun(ctx, wsID, keys, *backfill)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar shadow run: %v\n", err)
	}
	// The outcome each job reported, by run id. A run whose job.finished
	// was missed is read back from its state instead.
	status := map[string]string{}
	interrupted := !waitForJobs(ctx, svc, events, func(ev app.Event) {
		for _, o := range ev.Outcomes {
			status[o.RunID] = o.Status
		}
	})

	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	startedAny := false
	for _, s := range started {
		if s.RunID == "" {
			fmt.Fprintf(w, "%s\tskipped: %s\n", s.Key, s.Skipped)
			continue
		}
		startedAny = true
		st := status[s.RunID]
		if st == "" {
			if d, err := svc.Run(wsID, s.RunID); err == nil {
				st = d.Status
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Key, s.RunID, st)
	}
	if code := flush(w, stderr); code != 0 {
		return code
	}
	if interrupted {
		fmt.Fprintln(stderr, "sirdar shadow run: interrupted; the runs still going were stopped and can be resumed")
		return 1
	}
	if !startedAny || err != nil {
		return 1
	}
	return 0
}

func cmdShadowStatus(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("shadow status", stderr, "usage: sirdar shadow status")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}
	svc, wsID, ok := openWorkspaceService(app.Options{}, stderr)
	if !ok {
		return exitUsage
	}
	st, err := svc.ShadowStatus(wsID)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar shadow status: %v\n", err)
		return 1
	}
	printShadowStatus(stdout, st)
	return 0
}

// printShadowStatus writes the status as key: value lines.
func printShadowStatus(w io.Writer, st app.ShadowStatus) {
	fmt.Fprintf(w, "state: %s\n", st.State)
	fmt.Fprintf(w, "reason: %s\n", st.Reason)
	fmt.Fprintf(w, "today: %d of %d runs, $%.2f of $%.2f\n", st.Today.Runs, st.Today.MaxPerDay, st.Today.USD, st.Today.MaxUSDPerDay)
	fmt.Fprintf(w, "next poll: %s\n", orNone(localTime(st.NextPoll)))
	fmt.Fprintf(w, "in flight: %s\n", orNone(strings.Join(st.InFlight, ", ")))
	if st.LastError != "" {
		fmt.Fprintf(w, "last error: %s\n", st.LastError)
	}
}

// localTime renders a wire time in the operator's zone.
func localTime(wire string) string {
	if wire == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, wire)
	if err != nil {
		return wire
	}
	return formatTime(t)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func cmdShadowReport(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("shadow report", stderr, "usage: sirdar shadow report [--since 7d|2026-10-01] [--json]")
	since := fs.String("since", "", "how far back: a duration like 7d, or a date like 2026-10-01 (default 30 days)")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}
	svc, wsID, ok := openWorkspaceService(app.Options{}, stderr)
	if !ok {
		return exitUsage
	}
	rep, err := svc.ShadowReport(wsID, *since)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar shadow report: %v\n", err)
		return 1
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n", err)
			return 1
		}
		return 0
	}

	h := rep.Headline
	fmt.Fprintf(stdout, "Graded %d of %d started\n", h.Graded, h.Started)
	fmt.Fprintf(stdout, "Confirmed %.0f%% (%.0f%% confirmed or partial)\n", h.Confirmed*100, h.ConfirmedOrPartial*100)
	fmt.Fprintf(stdout, "Median time %s · ready before you started: %d of %d\n", seconds(h.MedianElapsedSec), h.ReadyBefore, h.ReadyOf)
	fmt.Fprintf(stdout, "Cost $%.2f · median $%.2f per ticket\n", h.CostUSD, h.MedianCostUSD)
	if len(rep.Rows) == 0 {
		return 0
	}
	fmt.Fprintln(stdout)
	return printShadowRows(stdout, stderr, rep.Rows)
}

// seconds renders a count of seconds as a Go duration, to the second.
func seconds(s float64) string {
	return time.Duration(s * float64(time.Second)).Round(time.Second).String()
}

// printShadowRows writes the scorecard table: the grade carries its tags in
// brackets, and the hypothesis is kept to one line.
func printShadowRows(stdout, stderr io.Writer, rows []app.ShadowRow) int {
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tGRADE\tCLASSIFICATION\tCOST\tHYPOTHESIS")
	for _, r := range rows {
		grade := orDash(r.Grade)
		if len(r.Tags) > 0 {
			grade += " [" + strings.Join(r.Tags, ", ") + "]"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t$%.2f\t%s\n", r.Key, grade, orDash(r.Classification), r.CostUSD,
			strings.Join(strings.Fields(r.Hypothesis), " "))
	}
	return flush(w, stderr)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdShadowOutcome(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("shadow outcome", stderr, "usage: sirdar shadow outcome KEY [--pr URL]... [--resolution TEXT|@file]")
	var prs repeatedFlag
	fs.Var(&prs, "pr", "a pull request that fixed the ticket (repeatable)")
	resolution := fs.String("resolution", "", "what was done, as text or @path to a file")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
		return exitUsage
	}
	text, err := resolutionText(*resolution)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar shadow outcome: %v\n", err)
		return 1
	}
	svc, wsID, ok := openWorkspaceService(app.Options{}, stderr)
	if !ok {
		return exitUsage
	}
	ctx, stop := interruptible()
	defer stop()
	defer svc.Stop()
	row, err := svc.ShadowOutcome(ctx, wsID, app.ShadowOutcomeInput{Key: positional[0], PRURLs: prs, Resolution: text})
	if err != nil {
		fmt.Fprintf(stderr, "sirdar shadow outcome: %v\n", err)
		return 1
	}
	return printShadowRows(stdout, stderr, []app.ShadowRow{row})
}

func cmdShadowGrade(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("shadow grade", stderr, "usage: sirdar shadow grade [KEY...]")
	keys, ok := parseFlags(fs, args, 0, -1, stderr)
	if !ok {
		return exitUsage
	}
	svc, wsID, ok := openWorkspaceService(app.Options{}, stderr)
	if !ok {
		return exitUsage
	}
	ctx, stop := interruptible()
	defer stop()
	defer svc.Stop()
	rows, err := svc.ShadowRegrade(ctx, wsID, keys)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar shadow grade: %v\n", err)
		return 1
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tGRADE\tBASIS")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.Key, orDash(r.Grade), orDash(r.Basis))
	}
	return flush(w, stderr)
}

func cmdShadowPause(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("shadow pause", stderr, "usage: sirdar shadow pause [--for 4h]")
	forDuration := fs.String("for", "", "how long to pause new shadow runs (default 24h)")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}
	svc, wsID, ok := openWorkspaceService(app.Options{}, stderr)
	if !ok {
		return exitUsage
	}
	st, err := svc.ShadowPause(wsID, *forDuration)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar shadow pause: %v\n", err)
		return 1
	}
	printShadowStatus(stdout, st)
	return 0
}

func cmdShadowResume(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("shadow resume", stderr, "usage: sirdar shadow resume")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}
	svc, wsID, ok := openWorkspaceService(app.Options{}, stderr)
	if !ok {
		return exitUsage
	}
	st, err := svc.ShadowResume(wsID)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar shadow resume: %v\n", err)
		return 1
	}
	printShadowStatus(stdout, st)
	return 0
}
