package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/zohodesk"
)

func init() { commands["doctor"] = cmdDoctor }

// doctorTimeout caps each source check, so one unreachable adapter or API
// cannot hang the whole report.
const doctorTimeout = 30 * time.Second

// cmdDoctor checks everything a run depends on and prints one line per
// check. It exits 1 when any check failed, so it can gate a CI job.
func cmdDoctor(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("doctor", stderr, "usage: sirdar doctor")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	stderr = synced(stderr)

	ctx := context.Background()
	checks := []provider.Check{{Name: "config", OK: true, Detail: filepath.Join(cfg.Root, ".sirdar", "config.yaml")}}
	checks = append(checks, providerChecks(ctx, cfg)...)
	checks = append(checks, sourceChecks(ctx, cfg, stderr)...)
	checks = append(checks, notesCheck(cfg), templatesCheck(cfg))

	failed := 0
	for _, c := range checks {
		mark := "[OK]"
		if !c.OK {
			mark = "[!!]"
			failed++
		}
		if c.Detail == "" {
			fmt.Fprintf(stdout, "%s %s\n", mark, c.Name)
			continue
		}
		fmt.Fprintf(stdout, "%s %s — %s\n", mark, c.Name, c.Detail)
	}
	if failed > 0 {
		fmt.Fprintf(stdout, "\n%d of %d checks failed\n", failed, len(checks))
		return 1
	}
	return 0
}

func providerChecks(ctx context.Context, cfg *config.Config) []provider.Check {
	p, err := providerFor(cfg.Provider)
	if err != nil {
		return []provider.Check{{Name: "provider", Detail: err.Error()}}
	}
	binary := cfg.Providers.Claude.Path
	if cfg.Provider == "codex" {
		binary = cfg.Providers.Codex.Path
	}
	if binary != "" {
		binary = cfg.ExpandPath(binary)
	}
	return p.Doctor(ctx, binary)
}

// sourceChecks reaches each configured source the cheapest way that proves
// the credentials and the transport work: describe for an adapter, and a
// lookup of an id that cannot exist for Zoho Desk, where "not found" is the
// answer that proves the token is good.
func sourceChecks(ctx context.Context, cfg *config.Config, stderr io.Writer) []provider.Check {
	var checks []provider.Check
	for _, s := range []struct {
		role string
		sc   *config.SourceConfig
	}{{"tracker", cfg.Sources.Tracker}, {"helpdesk", cfg.Sources.Helpdesk}} {
		if s.sc == nil {
			checks = append(checks, provider.Check{Name: "sources." + s.role, OK: true, Detail: "not configured"})
			continue
		}
		name := fmt.Sprintf("sources.%s (%s)", s.role, s.sc.Adapter)
		checks = append(checks, checkSource(ctx, cfg, name, s.sc, stderr)...)
	}
	return checks
}

func checkSource(ctx context.Context, cfg *config.Config, name string, sc *config.SourceConfig, stderr io.Writer) []provider.Check {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()

	switch sc.Adapter {
	case "exec":
		command := expandCommand(cfg, sc.Command)
		client, err := plugin.Start(ctx, command, stderr)
		if err != nil {
			return []provider.Check{{Name: name, Detail: err.Error()}}
		}
		defer client.Close()
		d, err := client.Describe(ctx)
		if err != nil {
			return []provider.Check{{Name: name, Detail: err.Error()}}
		}
		return []provider.Check{{Name: name, OK: true, Detail: fmt.Sprintf("%s v%s roles=%v", d.Name, d.Version, d.Roles)}}

	case "zohodesk":
		ts, err := zohoTokenSource(sc, config.Resolver{Keychain: keychainFor()})
		if err != nil {
			return []provider.Check{{Name: name, Detail: err.Error()}}
		}

		var checks []provider.Check
		if rt, ok := ts.(*zohodesk.RefreshingToken); ok {
			// The refresh grant gets its own line: it is the part an
			// unattended run depends on, and a Desk call that happens to
			// succeed does not tell the operator the grant is still good
			// for the next one.
			checks = append(checks, oauthCheck(ctx, rt))
		}
		return append(checks, deskProbe(ctx, name, sc, ts))

	case "zendesk", "freshdesk":
		return []provider.Check{builtinHelpdeskProbe(ctx, name, sc)}

	case "jira", "linear", "azdo", "rally":
		return []provider.Check{builtinProbe(ctx, name, sc)}

	default:
		return []provider.Check{{Name: name, Detail: fmt.Sprintf("unknown adapter %q", sc.Adapter)}}
	}
}

// builtinProbe builds a built-in tracker adapter with the credentials the
// config names and makes it call Ping: one authenticated round trip against
// the cheapest endpoint the API has, which is what proves the base URL, the
// credential and the network all work before a run spends an agent session
// finding out otherwise.
func builtinProbe(ctx context.Context, name string, sc *config.SourceConfig) provider.Check {
	tracker, _, err := newBuiltinTracker(sc, config.Resolver{Keychain: keychainFor()})
	if err != nil {
		return provider.Check{Name: name, Detail: err.Error()}
	}
	p, ok := tracker.(pinger)
	if !ok {
		return provider.Check{Name: name, OK: true, Detail: "configured (" + builtinEndpoint(sc) + ")"}
	}
	if err := p.Ping(ctx); err != nil {
		return provider.Check{Name: name, Detail: err.Error()}
	}
	return provider.Check{Name: name, OK: true, Detail: "reachable (" + builtinEndpoint(sc) + ")"}
}

// builtinHelpdeskProbe builds a built-in helpdesk adapter (zendesk,
// freshdesk) with the credentials the config names and calls its Ping: one
// authenticated round trip proving the base URL/domain, the credential and
// the network all work. The detail names who the connection authenticates
// as — an email for Zendesk basic auth, "oauth" for a bearer token, the
// account domain for Freshdesk — never the secret itself.
func builtinHelpdeskProbe(ctx context.Context, name string, sc *config.SourceConfig) provider.Check {
	hd, err := newBuiltinHelpdesk(sc, config.Resolver{Keychain: keychainFor()})
	if err != nil {
		return provider.Check{Name: name, Detail: err.Error()}
	}
	p, ok := hd.(pinger)
	if !ok {
		return provider.Check{Name: name, OK: true, Detail: "configured"}
	}
	if err := p.Ping(ctx); err != nil {
		return provider.Check{Name: name, Detail: err.Error()}
	}
	return provider.Check{Name: name, OK: true, Detail: "reachable as " + helpdeskAuthWho(sc)}
}

// helpdeskAuthWho names who a built-in helpdesk connection authenticates
// as, for the doctor report. It never returns any part of the credential.
func helpdeskAuthWho(sc *config.SourceConfig) string {
	switch sc.Adapter {
	case "zendesk":
		if sc.Email != "" {
			return sc.Email
		}
		return "oauth"
	case "freshdesk":
		return sc.Domain
	default:
		return "configured"
	}
}

// oauthCheck performs one refresh and reports the access token it got and
// how long that token is good for. The token itself is never printed.
func oauthCheck(ctx context.Context, rt *zohodesk.RefreshingToken) provider.Check {
	_, ttl, err := rt.TokenWithExpiry(ctx)
	if err != nil {
		return provider.Check{Name: "zoho oauth", Detail: err.Error()}
	}
	return provider.Check{
		Name: "zoho oauth",
		OK:   true,
		// Rounded: the sub-second drift between minting the token and
		// measuring it is not something to report to three decimals.
		Detail: fmt.Sprintf("access token obtained, expires in %ds", int(ttl.Round(time.Second).Seconds())),
	}
}

// deskProbe looks up a ticket id that cannot exist. "Not found" is the
// answer that proves the transport and the credentials work, since the API
// had to authenticate the request before it could reject the id.
func deskProbe(ctx context.Context, name string, sc *config.SourceConfig, ts zohodesk.TokenSource) provider.Check {
	_, err := zohodesk.New(sc.BaseURL, sc.OrgID, ts).Get(ctx, "0")
	var serr *source.Error
	switch {
	case err == nil:
		return provider.Check{Name: name, OK: true, Detail: sc.BaseURL}
	case errors.As(err, &serr) && serr.Code == source.NotFound:
		return provider.Check{Name: name, OK: true, Detail: sc.BaseURL}
	default:
		return provider.Check{Name: name, Detail: err.Error()}
	}
}

// notesCheck proves the notes directory exists and takes writes, since a
// run that discovers otherwise has already spent an agent session.
func notesCheck(cfg *config.Config) provider.Check {
	dir := cfg.ExpandPath(cfg.Notes.Dir)
	check := provider.Check{Name: "notes.dir", Detail: dir}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		check.Detail = err.Error()
		return check
	}
	f, err := os.CreateTemp(dir, ".sirdar-doctor-*")
	if err != nil {
		check.Detail = err.Error()
		return check
	}
	name := f.Name()
	f.Close()
	if err := os.Remove(name); err != nil {
		check.Detail = err.Error()
		return check
	}
	check.OK = true
	return check
}

// templatesCheck renders every note template against the built-in sample,
// so a broken override is caught here rather than after a run.
func templatesCheck(cfg *config.Config) provider.Check {
	dir := ""
	detail := "embedded defaults"
	if cfg.Notes.Templates != "" {
		dir = cfg.ExpandPath(cfg.Notes.Templates)
		detail = dir
	}
	if err := (note.Renderer{TemplatesDir: dir}).Check(); err != nil {
		return provider.Check{Name: "notes.templates", Detail: err.Error()}
	}
	return provider.Check{Name: "notes.templates", OK: true, Detail: detail}
}
