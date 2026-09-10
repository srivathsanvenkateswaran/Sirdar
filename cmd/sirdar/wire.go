package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider/claude"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider/codex"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/azdo"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/jira"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/linear"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/rally"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/zohodesk"
)

// exitUsage is the status for a command the operator invoked wrongly: bad
// flags, missing arguments, or no workspace to work in.
const exitUsage = 2

// loadWorkspace finds the workspace the working directory belongs to and
// loads its configuration. The bool is false when the caller should give
// up; the message has already been written to stderr.
func loadWorkspace(stderr io.Writer) (*config.Config, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return nil, false
	}
	root, err := config.FindRoot(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: no .sirdar/config.yaml found in %s or its parents; run 'sirdar init'\n", cwd)
		return nil, false
	}
	cfg, err := config.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return nil, false
	}
	return cfg, true
}

// interruptible returns a context that Ctrl-C cancels, and the func that
// releases the signal handler. The runner reacts to the cancellation by
// stopping its sessions and marking the runs blocked, so an interrupted
// batch can be picked up again with `sirdar resume`.
//
// The handler is released as soon as the first signal lands, so a second
// Ctrl-C reaches the default handler and kills the process. Otherwise an
// operator who wants out of a shutdown that is taking too long — an agent
// ignoring SIGINT, an adapter inside its five-second grace — would have
// nothing left to press.
func interruptible() (context.Context, func()) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}

// buildDeps assembles everything a Runner needs: the ticket sources named
// in the configuration, the provider to drive, and the credential resolver
// for this platform. A non-empty providerName or model overrides what the
// configuration says. The returned func releases the adapter subprocesses
// and must be called even when the error is nil.
//
// The stdout writer is accepted so every command wires the runner the same
// way; nothing the runner produces goes there.
func buildDeps(cfg *config.Config, providerName, model string, _, stderr io.Writer) (runner.Deps, func(), error) {
	stderr = synced(stderr)
	creds := config.Resolver{Keychain: keychainFor()}
	adapters := &adapterSet{stderr: stderr}
	cleanup := adapters.close

	if providerName != "" {
		cfg.Provider = config.Provider(providerName)
	}
	if model != "" {
		cfg.Model = model
	}
	p, err := providerFor(cfg.Provider)
	if err != nil {
		return runner.Deps{}, cleanup, err
	}

	deps := runner.Deps{
		Config:   cfg,
		Provider: p,
		Creds:    creds,
		Stderr:   stderr,
		Stdin:    os.Stdin,
		Env:      os.Environ(),
	}

	if sc := cfg.Sources.Tracker; sc != nil {
		tracker, err := adapters.tracker(cfg, sc, creds)
		if err != nil {
			return runner.Deps{}, cleanup, fmt.Errorf("sources.tracker: %w", err)
		}
		deps.Tracker = tracker
	}
	if sc := cfg.Sources.Helpdesk; sc != nil {
		helpdesk, err := adapters.helpdesk(cfg, sc, creds)
		if err != nil {
			return runner.Deps{}, cleanup, fmt.Errorf("sources.helpdesk: %w", err)
		}
		deps.Helpdesk = helpdesk
	} else if adapters.trackerHelpdesk != nil {
		// Trackers like Jira Service Management and Linear carry the
		// customer conversation on the issue itself, so one adapter can
		// serve both roles. A configured sources.helpdesk always wins:
		// an operator who named a separate helpdesk meant the thread to
		// come from there.
		deps.Helpdesk = adapters.trackerHelpdesk
	}
	return deps, cleanup, nil
}

func providerFor(name config.Provider) (provider.Provider, error) {
	switch name {
	case "claude":
		return claude.New(), nil
	case "codex":
		return codex.New(), nil
	default:
		return nil, fmt.Errorf("unknown provider %q: use claude or codex", name)
	}
}

// adapterSet owns the adapter subprocesses a command starts. One process
// serves both roles when the tracker and the helpdesk name the same
// command, which is the usual shape for a single-system adapter.
type adapterSet struct {
	stderr  io.Writer
	clients map[string]*plugin.Client

	// trackerHelpdesk is the conversation view of the built-in tracker
	// that was built, when it has one. It is only used where no separate
	// sources.helpdesk is configured.
	trackerHelpdesk source.Helpdesk
}

func (a *adapterSet) client(command string) (*plugin.Client, error) {
	if c, ok := a.clients[command]; ok {
		return c, nil
	}
	c, err := plugin.Start(context.Background(), command, a.stderr)
	if err != nil {
		return nil, err
	}
	if a.clients == nil {
		a.clients = map[string]*plugin.Client{}
	}
	a.clients[command] = c
	return c, nil
}

func (a *adapterSet) close() {
	for _, c := range a.clients {
		_ = c.Close()
	}
	a.clients = nil
}

func (a *adapterSet) tracker(cfg *config.Config, sc *config.SourceConfig, creds config.Resolver) (source.Tracker, error) {
	switch sc.Adapter {
	case "exec":
		return a.client(expandCommand(cfg, sc.Command))
	case "jira", "linear", "azdo", "rally":
		tracker, helpdesk, err := newBuiltinTracker(sc, creds)
		if err != nil {
			return nil, err
		}
		a.trackerHelpdesk = helpdesk
		return tracker, nil
	default:
		return nil, fmt.Errorf("adapter %q cannot serve a tracker", sc.Adapter)
	}
}

// builtinTimeout is the per-request timeout every built-in tracker adapter
// gets. It is the adapters' own default, named here so the wiring hands
// them a client it controls rather than one they construct.
const builtinTimeout = 30 * time.Second

// pinger is the reachability probe the built-in tracker adapters expose for
// doctor: one authenticated round trip against the cheapest endpoint the
// API has.
type pinger interface {
	Ping(ctx context.Context) error
}

// newBuiltinTracker builds one of the four built-in tracker adapters from
// its configuration, resolving the credential refs on the way in. The
// second return is the adapter's helpdesk view where it has one — the
// conversation on the issue itself — and nil where it does not.
//
// Resolved secrets stay in the returned client: they are never written to a
// run directory and never reach the agent's environment.
func newBuiltinTracker(sc *config.SourceConfig, creds config.Resolver) (source.Tracker, source.Helpdesk, error) {
	apiToken, err := resolveRef(creds, "apiToken", sc.APIToken)
	if err != nil {
		return nil, nil, err
	}
	pat, err := resolveRef(creds, "pat", sc.PAT)
	if err != nil {
		return nil, nil, err
	}
	apiKey, err := resolveRef(creds, "apiKey", sc.APIKey)
	if err != nil {
		return nil, nil, err
	}
	hc := &http.Client{Timeout: builtinTimeout}

	switch sc.Adapter {
	case "jira":
		c, err := jira.New(jira.Config{
			BaseURL:       sc.BaseURL,
			Deployment:    sc.Deployment,
			Email:         sc.Email,
			APIToken:      apiToken,
			PAT:           pat,
			ProjectKey:    sc.ProjectKey,
			EpicLinkField: sc.EpicLinkField,
		}, hc)
		if err != nil {
			return nil, nil, err
		}
		return c, c.Helpdesk(), nil

	case "linear":
		c, err := linear.New(linear.Config{APIKey: apiKey, TeamKey: sc.TeamKey}, hc)
		if err != nil {
			return nil, nil, err
		}
		return c, c.Helpdesk(), nil

	case "azdo":
		c, err := azdo.New(azdo.Config{
			OrgURL:             sc.OrgURL,
			Project:            sc.Project,
			PAT:                pat,
			HelpdeskLinkDomain: sc.HelpdeskLinkDomain,
			HelpdeskField:      sc.HelpdeskField,
		}, hc)
		if err != nil {
			return nil, nil, err
		}
		return c, c.Helpdesk(), nil

	case "rally":
		c, err := rally.New(rally.Config{
			BaseURL:       sc.BaseURL,
			APIKey:        apiKey,
			Workspace:     sc.Workspace,
			Project:       sc.Project,
			Types:         sc.Types,
			HelpdeskField: sc.HelpdeskField,
		}, hc)
		if err != nil {
			return nil, nil, err
		}
		return c, c.Helpdesk(), nil
	}
	return nil, nil, fmt.Errorf("adapter %q cannot serve a tracker", sc.Adapter)
}

// resolveRef resolves one credential reference, naming the key and the ref
// in the error so an operator knows which entry is missing. An empty ref
// resolves to an empty string: whether that is allowed is the config
// validator's business, not this function's.
func resolveRef(creds config.Resolver, key, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	v, err := creds.Resolve(ref)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", key, ref, err)
	}
	return v, nil
}

// builtinEndpoint is what a built-in tracker talks to, for the doctor row.
// It never carries a credential.
func builtinEndpoint(sc *config.SourceConfig) string {
	switch sc.Adapter {
	case "azdo":
		return sc.OrgURL + "/" + sc.Project
	case "linear":
		return linear.DefaultEndpoint
	case "rally":
		if sc.BaseURL == "" {
			return config.RallyDefaultBaseURL
		}
		return sc.BaseURL
	default:
		return sc.BaseURL
	}
}

func (a *adapterSet) helpdesk(cfg *config.Config, sc *config.SourceConfig, creds config.Resolver) (source.Helpdesk, error) {
	switch sc.Adapter {
	case "exec":
		c, err := a.client(expandCommand(cfg, sc.Command))
		if err != nil {
			return nil, err
		}
		return c.Helpdesk(), nil
	case "zohodesk":
		ts, err := zohoTokenSource(sc, creds)
		if err != nil {
			return nil, err
		}
		return zohodesk.New(sc.BaseURL, sc.OrgID, ts), nil
	default:
		return nil, fmt.Errorf("adapter %q cannot serve a helpdesk", sc.Adapter)
	}
}

// zohoTokenSource builds what a Zoho Desk client authenticates with: either
// the one static access token the config names, or a refreshing source that
// mints access tokens from a Self Client's refresh token. Either way the
// secrets are resolved here and held in memory only — they are never
// written to a run directory and never reach the agent's environment.
func zohoTokenSource(sc *config.SourceConfig, creds config.Resolver) (zohodesk.TokenSource, error) {
	if sc.Auth == nil {
		token, err := creds.Resolve(sc.Token)
		if err != nil {
			return nil, fmt.Errorf("token %s: %w", sc.Token, err)
		}
		return zohodesk.StaticToken(token), nil
	}

	resolved := make([]string, 0, 3)
	for _, ref := range []struct{ key, value string }{
		{"auth.clientId", sc.Auth.ClientID},
		{"auth.clientSecret", sc.Auth.ClientSecret},
		{"auth.refreshToken", sc.Auth.RefreshToken},
	} {
		v, err := creds.Resolve(ref.value)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", ref.key, ref.value, err)
		}
		resolved = append(resolved, v)
	}

	// accountsUrl is normally filled in from baseUrl when the config
	// loads; deriving it again covers a Config built by hand.
	accounts := sc.Auth.AccountsURL
	if accounts == "" {
		accounts = config.AccountsURLFor(sc.BaseURL)
	}
	if accounts == "" {
		return nil, fmt.Errorf("auth.accountsUrl: cannot be derived from baseUrl %q; set it explicitly", sc.BaseURL)
	}

	return &zohodesk.RefreshingToken{
		AccountsURL:  accounts,
		ClientID:     resolved[0],
		ClientSecret: resolved[1],
		RefreshToken: resolved[2],
	}, nil
}

// expandCommand expands a leading "~/" or "./" in an adapter command line.
// Only the program is a path; everything after the first space is the
// adapter's own arguments and is left alone.
func expandCommand(cfg *config.Config, command string) string {
	program, args, _ := strings.Cut(command, " ")
	switch {
	case strings.HasPrefix(program, "~/"), strings.HasPrefix(program, "./"), strings.HasPrefix(program, "../"):
		program = cfg.ExpandPath(program)
	default:
		return command
	}
	if args == "" {
		return program
	}
	return program + " " + args
}

// keychainFor returns the platform's keychain reader, or nil where there is
// none: on those platforms a "keychain:" credential ref is an error and
// only "env:" refs work, as the spec says.
func keychainFor() config.KeychainReader {
	if runtime.GOOS == "darwin" {
		return config.MacKeychain{}
	}
	return nil
}

// syncWriter serialises writes to one writer.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// synced guards a writer that several goroutines share. The runner's
// progress lines and every adapter subprocess's copied stderr all land on
// the same writer at the same time: os.Stderr tolerates that, an in-memory
// writer does not.
func synced(w io.Writer) io.Writer {
	if _, ok := w.(*syncWriter); ok {
		return w
	}
	return &syncWriter{w: w}
}
