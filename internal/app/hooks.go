package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/webhooks"
)

// TriageIfIdle starts a triage of one key unless the workspace is already
// busy with it. It returns either a job id or, with no error, the reason
// nothing was started — which the webhook route reports as an accepted
// delivery that did nothing, because a tracker that is told its delivery
// failed will send it again.
//
// Three things make a key busy. A run of it that is preparing or running
// is the obvious one: a second agent session on the same ticket would
// write over the first one's note. The cooldown is the one that matters in
// practice — a tracker fires on every field change, so an operator
// triaging a ticket by hand generates a delivery every few seconds, each of
// which would otherwise be a fresh run.
//
// The third is a start already under way in this process. Both of the
// others are read off the run directory, and the first status a run writes
// there is written by the job goroutine after this returns: two deliveries
// for one key arriving together would both see an idle key and both start.
// So the key is claimed here, before the directory is read, and released
// when the job ends however it ends.
func (s *Service) TriageIfIdle(ctx context.Context, wsID, key string, o TriageOptions) (JobID, string, error) {
	if err := checkID(ErrNoSuchRun, "key", key); err != nil {
		return "", "", err
	}
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return "", "", err
	}
	if !s.claimKey(ws.ID, key) {
		return "", fmt.Sprintf("a delivery naming %s is already starting a run", key), nil
	}
	started := false
	defer func() {
		if !started {
			s.releaseKey(ws.ID, key)
		}
	}()

	states, err := store.List(ws.Root, key)
	if err != nil {
		return "", "", err
	}
	if reason := busyReason(states, cfg.WebhookCooldown(), s.now()); reason != "" {
		return "", reason, nil
	}
	id, err := s.startTriage(ctx, wsID, []string{key}, o, func() { s.releaseKey(ws.ID, key) })
	if err != nil {
		return "", "", err
	}
	started = true
	return id, "", nil
}

// hookClaim is the key an in-flight start is held under. The NUL keeps two
// workspaces whose ids and keys concatenate to the same string apart.
func hookClaim(wsID, key string) string { return wsID + "\x00" + key }

// claimKey takes the in-flight claim on one workspace's key, reporting
// whether it was free.
func (s *Service) claimKey(wsID, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	claim := hookClaim(wsID, key)
	if _, taken := s.starting[claim]; taken {
		return false
	}
	s.starting[claim] = struct{}{}
	return true
}

// releaseKey gives the claim back. It runs when the job finishes, whether
// it succeeded or failed: a key held by a claim nobody releases could never
// be triaged again without restarting the process.
func (s *Service) releaseKey(wsID, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.starting, hookClaim(wsID, key))
}

// busyReason says why key should be left alone, or returns "".
func busyReason(states []store.State, cooldown time.Duration, now time.Time) string {
	for _, st := range states {
		if st.Status == store.StatusPreparing || st.Status == store.StatusRunning {
			return fmt.Sprintf("a %s run of %s is already %s", st.Kind, st.Key, st.Status)
		}
	}
	if cooldown <= 0 {
		return ""
	}
	for _, st := range states {
		if st.Kind != store.KindTriage {
			continue
		}
		at := st.UpdatedAt
		if at.IsZero() {
			at = st.StartedAt
		}
		if at.IsZero() {
			continue
		}
		if age := now.Sub(at); age >= 0 && age < cooldown {
			return fmt.Sprintf("%s was triaged %s ago and the cooldown is %s",
				st.Key, age.Round(time.Second), cooldown)
		}
	}
	return ""
}

// HookReceived records one inbound webhook delivery on the event stream,
// so the UI's activity pane shows what the tracker has been sending and an
// operator can tell a hook that never arrives from one that arrives and is
// filtered out.
func (s *Service) HookReceived(source, key, outcome string) {
	s.publish(Event{Kind: KindHookReceived, Source: source, Key: key, Outcome: outcome})
}

// BuildReceiver assembles the webhook receiver a workspace configures,
// resolving every secret through the platform's credential resolver. It
// returns nil when the workspace has webhooks turned off, and an error
// when a secret it names cannot be found — a hook endpoint that is up but
// cannot verify anything is worse than one that never started.
func BuildReceiver(cfg *config.Config) (*webhooks.Receiver, error) {
	if !cfg.Webhooks.Enabled {
		return nil, nil
	}
	creds := config.Resolver{Keychain: KeychainFor()}
	sources := make(map[string]webhooks.Verifier, len(cfg.Webhooks.Sources))
	for name, sc := range cfg.Webhooks.Sources {
		auth := webhooks.Auth{Username: sc.Username}
		if sc.Proxy != nil {
			auth.ProxyScheme = strings.TrimSpace(sc.Proxy.Scheme)
			auth.ProxyHost = strings.TrimSpace(sc.Proxy.Host)
		}
		if sc.Secret != "" {
			secret, err := creds.Resolve(sc.Secret)
			if err != nil {
				return nil, fmt.Errorf("webhooks.sources.%s.secret: %w", name, err)
			}
			auth.Secret = secret
		}
		if sc.Password != "" {
			password, err := creds.Resolve(sc.Password)
			if err != nil {
				return nil, fmt.Errorf("webhooks.sources.%s.password: %w", name, err)
			}
			auth.Password = password
		}
		v, err := webhooks.Build(name, auth)
		if err != nil {
			return nil, fmt.Errorf("webhooks.sources.%s: %w", name, err)
		}
		sources[name] = v
	}
	assignee, err := cfg.MatchAssignee()
	if err != nil {
		return nil, err
	}
	return webhooks.New(sources, webhooks.Match{
		Assignee: assignee,
		Statuses: cfg.Webhooks.Match.Statuses,
		Labels:   cfg.Webhooks.Match.Labels,
	}), nil
}
