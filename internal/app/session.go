package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// plainKey is a reference that is a tracker key and nothing else — the same
// rule the CLI's intake uses (cmd/sirdar/intake.go) — so a session started
// on "OMNI-1" does not pay for a resolve round trip to learn what it
// already knows.
var plainKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]+-[0-9]+$`)

// StartSession starts a session run: an instruction, with or without a
// ticket reference. The run id and the key it will be filed under are both
// known before the job itself runs, so the caller can show them at once.
func (s *Service) StartSession(ctx context.Context, wsID string, o SessionOptions) (SessionStarted, error) {
	instruction := strings.TrimSpace(o.Instruction)
	if instruction == "" {
		return SessionStarted{}, fmt.Errorf("%w: type what you want done", ErrBadSession)
	}

	var access string
	switch o.Access {
	case "":
		access = store.AccessReadOnly
	case store.AccessWorktree:
		// B6 replaces this refusal with the real behaviour: until a
		// worktree session exists, one would run read-only against a
		// prompt that tells the agent it may edit files.
		return SessionStarted{}, fmt.Errorf("%w: worktree sessions are not available yet", ErrBadSession)
	case store.AccessReadOnly:
		access = store.AccessReadOnly
	default:
		return SessionStarted{}, fmt.Errorf("%w: access must be read-only or worktree", ErrBadSession)
	}

	if err := CheckProvider(o.Provider); err != nil {
		return SessionStarted{}, err
	}

	var key, slackLink string
	noBundle := false
	reference := strings.TrimSpace(o.Reference)
	switch {
	case reference == "":
		key = runner.SessionKey(instruction, s.now())
		noBundle = true
	case plainKey.MatchString(reference):
		key = strings.ToUpper(reference)
	default:
		in, err := s.Resolve(ctx, wsID, reference)
		if err != nil {
			return SessionStarted{}, err
		}
		if in.Key == "" {
			return SessionStarted{}, fmt.Errorf("%w: %s", ErrBadSession, in.Reason)
		}
		key = in.Key
		if in.Slack != nil {
			slackLink = in.Slack.URL
		}
	}
	if err := checkID(ErrNoSuchRun, "key", key); err != nil {
		return SessionStarted{}, err
	}

	runID := store.NewRunID(s.now())
	jobID, err := s.start(ctx, wsID, o.Provider, o.Model, func(jctx context.Context, deps runner.Deps) []JobOutcome {
		slackMD, reported, err := s.slackStart(jctx, deps, key, slackLink)
		if err != nil {
			return s.failed([]string{key}, err)
		}
		r := &runner.Runner{Deps: deps}
		out, err := r.Session(jctx, key, runner.Options{
			RunID:       runID,
			NoBundle:    noBundle,
			Instruction: instruction,
			Model:       o.Model,
			Access:      access,
			Slack:       slackMD,
			Reported:    reported,
		})
		if err != nil {
			s.log(err)
			if out.State.RunID == "" {
				return s.failed([]string{key}, nil)
			}
		}
		return outcomesOf([]runner.Outcome{out})
	}, func(err error) []JobOutcome { return s.failed([]string{key}, err) })
	if err != nil {
		return SessionStarted{}, err
	}
	return SessionStarted{JobID: jobID, RunID: runID, Key: key}, nil
}
