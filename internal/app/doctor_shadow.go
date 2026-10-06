package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
)

// shadowDoctorSources builds the tracker the shadow row lists with.
var shadowDoctorSources = BuildSources // replaced in tests

// shadowDoctorLimit is how many tickets the shadow row asks the tracker
// for: enough to show the filter matches something, one cheap page.
const shadowDoctorLimit = 20

// shadowLedgerFiles are the files shadow mode writes under .sirdar/ that
// hold ticket titles and hypotheses, and so must stay out of the
// workspace's git history.
var shadowLedgerFiles = []string{".sirdar/shadow.jsonl", ".sirdar/playbook-suggestions.jsonl"}

// describer is the exec adapter's describe call, which says whether the
// adapter answers tracker.outcome.
type describer interface {
	Describe(ctx context.Context) (plugin.Describe, error)
}

// shadowPart is one clause of the shadow row and how bad it is.
type shadowPart struct {
	level provider.Level
	text  string
}

// shadowCheck is the shadow row: whether the watcher, once on, would find
// the operator's tickets, keep its ledger out of git, and have a way to
// learn how tickets ended. It reports false when no tracker is configured,
// since shadow has nothing to watch then.
//
// A workspace with shadow switched off still gets every part, so the
// operator can check it all before switching it on, but the row leads
// with "off" and goes no worse than a warning: what would stop the
// watcher is no fault while there is no watcher.
func shadowCheck(ctx context.Context, cfg *config.Config) (Check, bool) {
	if cfg.Sources.Tracker == nil {
		return Check{}, false
	}
	var parts []shadowPart
	if !cfg.Shadow.Enabled {
		parts = append(parts, shadowPart{provider.LevelOK, "off"})
	}

	assignee := queueAssignee(cfg, cfg.Shadow.Assignee)
	if source.IsSelf(cfg.Shadow.Assignee) && !source.ResolvesSelf(cfg.Sources.Tracker.Adapter) && SelfOf(cfg) == "" {
		parts = append(parts, shadowPart{provider.LevelFail, "assignee me resolves to nothing; set me: in config.yaml"})
	} else {
		parts = append(parts, shadowPart{provider.LevelOK, "assignee " + assignee})
	}

	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	tracker, _, cleanup, err := shadowDoctorSources(cfg, io.Discard)
	if cleanup != nil {
		defer cleanup()
	}
	switch {
	case err != nil:
		parts = append(parts, shadowPart{provider.LevelFail, firstLineOf(err.Error())})
	case tracker == nil:
		parts = append(parts, shadowPart{provider.LevelFail, "sources.tracker did not build"})
	default:
		parts = append(parts, shadowListPart(ctx, cfg, tracker, assignee))
		if p, ok := shadowOutcomePart(ctx, cfg, tracker); ok {
			parts = append(parts, p)
		}
	}

	parts = append(parts, shadowIgnoredParts(cfg.Root)...)
	if p, ok := shadowDigestPart(cfg); ok {
		parts = append(parts, p)
	}

	level := provider.LevelOK
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		texts = append(texts, p.text)
		if levelRank(p.level) > levelRank(level) {
			level = p.level
		}
	}
	if !cfg.Shadow.Enabled && level == provider.LevelFail {
		level = provider.LevelWarn
	}
	return Check{Name: "shadow", OK: level != provider.LevelFail, Level: string(level), Detail: strings.Join(texts, "; ")}, true
}

func levelRank(l provider.Level) int {
	switch l {
	case provider.LevelFail:
		return 2
	case provider.LevelWarn:
		return 1
	}
	return 0
}

// shadowListPart lists what the watcher would list, cut to the shadow type
// filter, so the row says whether the filter finds anything.
func shadowListPart(ctx context.Context, cfg *config.Config, tracker source.Tracker, assignee string) shadowPart {
	rows, err := tracker.List(ctx, source.ListFilter{Assignee: assignee, Order: source.OrderNewest, Limit: shadowDoctorLimit})
	if err != nil {
		return shadowPart{provider.LevelFail, firstLineOf(err.Error())}
	}
	types := cfg.ShadowTypes()
	n := 0
	for _, r := range rows {
		if config.MatchQueueType(types, r.Type) {
			n++
		}
	}
	if config.QueueTypesAll(types) {
		return shadowPart{provider.LevelOK, fmt.Sprintf("lists %d open tickets", n)}
	}
	return shadowPart{provider.LevelOK, fmt.Sprintf("lists %d open %s tickets", n, strings.Join(types, ", "))}
}

// shadowOutcomePart says where outcomes come from for an exec adapter,
// which may or may not answer tracker.outcome. The built-in adapters do
// not, and the row says nothing about them.
func shadowOutcomePart(ctx context.Context, cfg *config.Config, tracker source.Tracker) (shadowPart, bool) {
	d, ok := tracker.(describer)
	if cfg.Sources.Tracker.Adapter != "exec" || !ok {
		return shadowPart{}, false
	}
	desc, err := d.Describe(ctx)
	if err != nil {
		return shadowPart{provider.LevelFail, firstLineOf(err.Error())}, true
	}
	for _, m := range desc.Methods {
		if m == "tracker.outcome" {
			return shadowPart{provider.LevelOK, "tracker.outcome supported"}, true
		}
	}
	return shadowPart{provider.LevelOK, "tracker.outcome unsupported; outcomes come from your runs and manual entries"}, true
}

// shadowIgnoredParts warns for each shadow file git would track. A
// workspace that is not a repository has no history to leak into, and
// says nothing.
func shadowIgnoredParts(root string) []shadowPart {
	var parts []shadowPart
	for _, file := range shadowLedgerFiles {
		err := exec.Command("git", "-C", root, "check-ignore", "-q", file).Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			parts = append(parts, shadowPart{provider.LevelWarn, file + " is not git-ignored"})
		}
	}
	return parts
}

// shadowDigestPart checks the digest's webhook the way the send does, so
// a host the send would refuse shows here before the first morning it
// fails.
func shadowDigestPart(cfg *config.Config) (shadowPart, bool) {
	if !cfg.Shadow.Digest.Enabled {
		return shadowPart{}, false
	}
	hook, err := config.Resolver{Keychain: KeychainFor()}.Resolve(cfg.Shadow.Digest.WebhookURL)
	if err != nil {
		return shadowPart{provider.LevelFail, "shadow.digest.webhookUrl: " + firstLineOf(err.Error())}, true
	}
	if msg := digestURLProblem(hook); msg != "" {
		return shadowPart{provider.LevelFail, msg}, true
	}
	return shadowPart{}, false
}
