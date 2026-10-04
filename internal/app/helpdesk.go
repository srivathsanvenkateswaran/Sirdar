package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/slack"
)

// HelpdeskLink is what one helpdesk number resolves to: the tracker key the
// helpdesk ticket belongs to, or nothing with the reason it could not be
// found. It is the older, narrower answer of GET …/helpdesk/{number}, kept
// as an alias of Resolve for clients that still ask it.
type HelpdeskLink struct {
	Number string `json:"number"`
	Key    string `json:"key"`
	// Subject is the helpdesk record's own subject when it was read, so a
	// status line can show what was found even with no key on it.
	Subject string `json:"subject,omitempty"`
	// Reason says why Key is empty, in words a person reads. Empty when
	// there is a key.
	Reason string `json:"reason,omitempty"`
}

// helpdeskNumber is what the alias route accepts: digits, as a helpdesk
// writes a ticket number.
var helpdeskNumber = regexp.MustCompile(`^[0-9]{1,32}$`)

// trackerKeyIn finds a tracker key in arbitrary text: two or more
// upper-case letters or digits starting with a letter, a dash, and digits.
// It is deliberately the same shape the composer's own parser uses, so a
// key the person could have typed is a key the resolver can find.
var trackerKeyIn = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]+\b`)

// SlackFor builds the Slack reader a workspace configures, or nil when it
// configures none. The token is resolved here and held only by the client.
func SlackFor(cfg *config.Config) (SlackReader, error) {
	if cfg.Sources.Slack == nil {
		return nil, nil
	}
	token, err := resolveRef(config.Resolver{Keychain: KeychainFor()}, "sources.slack.token", cfg.Sources.Slack.Token)
	if err != nil {
		return nil, err
	}
	return slack.New(token), nil
}

// ResolveIntake resolves text against the sources a caller already built —
// the CLI's path, which builds its own and runs no service. fallback reads
// text with no reference in it; nil answers such text with a reason.
//
// prov is the provider a Slack link is read through when the workspace
// reads Slack by its own MCP server rather than a token; nil reads by token
// only.
func ResolveIntake(ctx context.Context, cfg *config.Config, prov provider.Provider, tracker source.Tracker, helpdesk source.Helpdesk, text string, fallback func(context.Context, string) (ComposedIntent, error)) (Intake, error) {
	sr, serr := SlackReaderFor(cfg, prov)
	r := newResolver(cfg, tracker, helpdesk, sr, serr, fallback)
	return r.resolve(ctx, text)
}

func newResolver(cfg *config.Config, tracker source.Tracker, helpdesk source.Helpdesk, sr SlackReader, serr error, fallback func(context.Context, string) (ComposedIntent, error)) *intakeResolver {
	r := &intakeResolver{
		tracker:      tracker,
		helpdesk:     helpdesk,
		helpdeskName: helpdeskDisplayName(cfg.Sources.Helpdesk),
		slack:        sr,
		slackErr:     serr,
		cache:        newIntakeCache(cfg.Root, nil),
		repos:        cfg.Repositories(),
		fallback:     fallback,
	}
	if cfg.Sources.Helpdesk != nil {
		r.trackerField = cfg.Sources.Helpdesk.TrackerField
	}
	return r
}

// Resolve answers what a piece of pasted text points at: a tracker key, a
// tracker URL, a helpdesk number or link, or a Slack link whose thread names
// one of those. It is read-only — it reads the tracker, the helpdesk and
// Slack, starts no run and writes nothing but its own day-long cache of
// helpdesk→tracker pairs. Text with none of those in it is read by the
// model, one short call, the same reading ComposeIntent makes.
func (s *Service) Resolve(ctx context.Context, wsID, text string) (Intake, error) {
	if strings.TrimSpace(text) == "" {
		return Intake{}, fmt.Errorf("%w: there is nothing to resolve", ErrInvalidArgument)
	}
	_, cfg, err := s.load(wsID)
	if err != nil {
		return Intake{}, err
	}
	deps, cleanup, err := s.build(cfg, "", "", s.stderr())
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return Intake{}, err
	}
	sr, serr := s.slackFor(cfg, deps.Provider)
	fallback := func(ctx context.Context, text string) (ComposedIntent, error) {
		return s.ComposeIntent(ctx, wsID, text)
	}
	r := newResolver(cfg, deps.Tracker, deps.Helpdesk, sr, serr, fallback)
	r.cache = newIntakeCache(cfg.Root, s.opts.Now)
	return r.resolve(ctx, text)
}

// checkSlackLink refuses a start whose Slack field is not a Slack link,
// before any job exists.
func checkSlackLink(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if _, ok := slack.FindLink(raw); !ok {
		return fmt.Errorf("%w: %q is not a Slack message link", ErrInvalidArgument, raw)
	}
	return nil
}

// slackMarkdown reads the Slack link a start carries and renders the thread
// for the bundle's slack.md. Empty when the start carries none.
// slackThread reads the Slack link a start carries: the thread, and the
// reader that read it. Nil with no error when the start carries none.
func (s *Service) slackThread(ctx context.Context, cfg *config.Config, prov provider.Provider, raw string) (*slack.Thread, SlackReader, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil, nil
	}
	l, ok := slack.FindLink(raw)
	if !ok {
		return nil, nil, fmt.Errorf("%w: %q is not a Slack message link", ErrInvalidArgument, raw)
	}
	sr, err := s.slackFor(cfg, prov)
	if err != nil {
		return nil, nil, fmt.Errorf("the Slack reader could not be set up: %w", err)
	}
	if sr == nil {
		return nil, nil, fmt.Errorf("%s", SlackNotConfigured)
	}
	th, err := sr.Read(ctx, l)
	if err != nil {
		return nil, nil, fmt.Errorf("the Slack link could not be read: %w", err)
	}
	return &th, sr, nil
}

// checkSlackOnly refuses, before any job exists, a start on a Slack-only
// key that is not the one key of the start or does not carry the link the
// key was made from: the thread is the ticket, and there is nothing else to
// build the bundle from.
func checkSlackOnly(keys []string, link string) error {
	for _, key := range keys {
		if !slack.IsKey(key) {
			continue
		}
		if len(keys) != 1 {
			return fmt.Errorf("%w: %s is triaged from its Slack thread alone, not beside other keys", ErrInvalidArgument, key)
		}
		l, ok := slack.FindLink(link)
		if !ok {
			return fmt.Errorf("%w: %s needs the Slack link it was made from", ErrInvalidArgument, key)
		}
		if got := slack.KeyFor(l); got != key {
			return fmt.Errorf("%w: the Slack link is for %s, not %s", ErrInvalidArgument, got, key)
		}
	}
	return nil
}

// slackStart reads a start's Slack link into what the runner takes: the
// thread as slack.md beside a ticket, or, for a Slack-only key, the bundle
// the thread is triaged from.
func (s *Service) slackStart(ctx context.Context, deps runner.Deps, key, link string) (string, *runner.ReportedBundle, error) {
	th, sr, err := s.slackThread(ctx, deps.Config, deps.Provider, link)
	if err != nil || th == nil {
		return "", nil, err
	}
	if slack.IsKey(key) {
		if len(th.Messages) == 0 {
			return "", nil, fmt.Errorf("the Slack thread for %s has no messages", key)
		}
		return "", reportedFor(*th, key, sr), nil
	}
	return slack.Markdown(*th), nil, nil
}

func (s *Service) slackFor(cfg *config.Config, prov provider.Provider) (SlackReader, error) {
	if s.opts.Slack != nil {
		return s.opts.Slack(cfg)
	}
	return SlackReaderFor(cfg, prov)
}

// ResolveHelpdesk answers which tracker issue a helpdesk number belongs to.
// It is Resolve for "#<number>", answered in the older HelpdeskLink shape.
func (s *Service) ResolveHelpdesk(ctx context.Context, wsID, number string) (HelpdeskLink, error) {
	number = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(number), "#"))
	if !helpdeskNumber.MatchString(number) {
		return HelpdeskLink{}, fmt.Errorf("%w: %q is not a helpdesk number", ErrInvalidArgument, number)
	}
	_, cfg, err := s.load(wsID)
	if err != nil {
		return HelpdeskLink{}, err
	}
	deps, cleanup, err := s.build(cfg, "", "", s.stderr())
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return HelpdeskLink{}, err
	}
	r := newResolver(cfg, deps.Tracker, deps.Helpdesk, nil, nil, nil)
	r.cache = newIntakeCache(cfg.Root, s.opts.Now)
	in := r.fromHelpdesk(ctx, InputHelpdeskNumber, number, "")
	return HelpdeskLink{Number: number, Key: in.Key, Subject: in.Subject, Reason: in.Reason}, nil
}
