package main

import (
	"context"
	"fmt"
	"io"
	"regexp"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/slack"
)

// plainKey is an argument that is a tracker key and nothing else. It is
// passed through as typed, without a lookup, the way every command took a
// key before it took anything else.
var plainKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]+-[0-9]+$`)

// resolvedArgs is what a command's ticket arguments came to: one key per
// argument, and the Slack thread when exactly one argument was a Slack link.
type resolvedArgs struct {
	keys  []string
	slack string
	// reported is the bundle a Slack thread with no ticket in it is
	// triaged from; its key is the one key.
	reported *runner.ReportedBundle
}

// resolveArgs turns each ticket argument — a key, a tracker URL, a helpdesk
// number such as #28310, a helpdesk link, a Slack link — into the tracker
// key it points at, printing how each one that was not already a key was
// resolved. An argument that resolves to no key is an error naming the
// reason; the command then runs nothing.
func resolveArgs(ctx context.Context, cfg *config.Config, deps runner.Deps, args []string, stderr io.Writer) (resolvedArgs, bool) {
	var out resolvedArgs
	slackThreads := 0 // how many arguments carried a Slack thread
	for _, arg := range args {
		if plainKey.MatchString(arg) || slack.IsKey(arg) {
			out.keys = append(out.keys, arg)
			continue
		}
		in, err := app.ResolveIntake(ctx, cfg, deps.Provider, deps.Tracker, deps.Helpdesk, arg, nil)
		if err != nil {
			fmt.Fprintf(stderr, "sirdar: %s: %v\n", arg, err)
			return out, false
		}
		if in.Key == "" {
			fmt.Fprintf(stderr, "sirdar: %s: %s\n", arg, in.Reason)
			return out, false
		}
		fmt.Fprintf(stderr, "sirdar: %s\n", in.Summary)
		out.keys = append(out.keys, in.Key)
		if md := in.SlackMarkdown(); md != "" {
			slackThreads++
			out.slack = md
		}
		if rb := in.Reported(); rb != nil {
			if len(args) > 1 {
				fmt.Fprintf(stderr, "sirdar: %s: a Slack thread with no ticket is triaged on its own; give it as the one ticket\n", arg)
				return out, false
			}
			out.reported = rb
		}
	}
	if slackThreads > 0 && len(args) > 1 {
		// The thread is carried into every bundle of the batch, so with
		// more than one ticket it would land beside tickets it is not
		// about. One ticket per command carries it.
		fmt.Fprintln(stderr, "sirdar: several tickets given; the Slack thread is carried only when the Slack link is the one ticket")
		out.slack = ""
	}
	return out, true
}
