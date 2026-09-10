package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider/claude"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider/codex"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/zohodesk"
)

// BuildDeps assembles everything a Runner needs: the ticket sources named
// in the configuration, the provider to drive, and the credential resolver
// for this platform. A non-empty providerName or model overrides what the
// configuration says. The returned func releases the adapter subprocesses
// and must be called even when the error is nil.
//
// Both shells wire a runner through here: the CLI and the desktop app.
func BuildDeps(cfg *config.Config, providerName, model string, stderr io.Writer) (runner.Deps, func(), error) {
	stderr = Synced(stderr)
	creds := config.Resolver{Keychain: KeychainFor()}
	adapters := &adapterSet{stderr: stderr}
	cleanup := adapters.close

	if providerName != "" {
		cfg.Provider = config.Provider(providerName)
	}
	if model != "" {
		cfg.Model = model
	}
	p, err := ProviderFor(cfg.Provider)
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
		tracker, err := adapters.tracker(cfg, sc)
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
	}
	return deps, cleanup, nil
}

// ProviderFor returns the adapter for a configured provider name.
func ProviderFor(name config.Provider) (provider.Provider, error) {
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

func (a *adapterSet) tracker(cfg *config.Config, sc *config.SourceConfig) (source.Tracker, error) {
	switch sc.Adapter {
	case "exec":
		return a.client(ExpandCommand(cfg, sc.Command))
	default:
		return nil, fmt.Errorf("adapter %q cannot serve a tracker", sc.Adapter)
	}
}

func (a *adapterSet) helpdesk(cfg *config.Config, sc *config.SourceConfig, creds config.Resolver) (source.Helpdesk, error) {
	switch sc.Adapter {
	case "exec":
		c, err := a.client(ExpandCommand(cfg, sc.Command))
		if err != nil {
			return nil, err
		}
		return c.Helpdesk(), nil
	case "zohodesk":
		ts, err := ZohoTokenSource(sc, creds)
		if err != nil {
			return nil, err
		}
		return zohodesk.New(sc.BaseURL, sc.OrgID, ts), nil
	default:
		return nil, fmt.Errorf("adapter %q cannot serve a helpdesk", sc.Adapter)
	}
}

// ZohoTokenSource builds what a Zoho Desk client authenticates with: either
// the one static access token the config names, or a refreshing source that
// mints access tokens from a Self Client's refresh token. Either way the
// secrets are resolved here and held in memory only — they are never
// written to a run directory and never reach the agent's environment.
func ZohoTokenSource(sc *config.SourceConfig, creds config.Resolver) (zohodesk.TokenSource, error) {
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

// ExpandCommand expands a leading "~/", "./" or "../" in an adapter
// command's program, which is the part that is resolved against the
// workspace root. A "~/" inside the adapter's own arguments is expanded
// too, but against the home directory and by plugin.Start, which is the
// last place that can do it before the argv reaches a process with no
// shell in front of it.
func ExpandCommand(cfg *config.Config, command string) string {
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

// KeychainFor returns the platform's keychain reader, or nil where there is
// none: on those platforms a "keychain:" credential ref is an error and
// only "env:" refs work, as the spec says.
func KeychainFor() config.KeychainReader {
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

// Synced guards a writer that several goroutines share. The runner's
// progress lines and every adapter subprocess's copied stderr all land on
// the same writer at the same time: os.Stderr tolerates that, an in-memory
// writer does not.
func Synced(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	if _, ok := w.(*syncWriter); ok {
		return w
	}
	return &syncWriter{w: w}
}
