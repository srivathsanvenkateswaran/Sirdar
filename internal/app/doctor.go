package app

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

// doctorTimeout caps each source check, so one unreachable adapter or API
// cannot hang the whole report.
const doctorTimeout = 30 * time.Second

// RunDoctor checks everything a run in this workspace depends on: the
// configuration, the agent binary, every configured source, the notes
// directory and the note templates. Both the `sirdar doctor` command and
// the desktop Settings screen read the same list.
func RunDoctor(ctx context.Context, cfg *config.Config) []Check {
	checks := []Check{{Name: "config", OK: true, Detail: filepath.Join(cfg.Root, ".sirdar", "config.yaml")}}
	checks = append(checks, providerChecks(ctx, cfg)...)
	checks = append(checks, sourceChecks(ctx, cfg)...)
	checks = append(checks, notesCheck(cfg), templatesCheck(cfg))
	return checks
}

// checkOf converts a provider diagnostic into the wire shape.
func checkOf(c provider.Check) Check {
	return Check{Name: c.Name, OK: c.OK, Detail: c.Detail}
}

func providerChecks(ctx context.Context, cfg *config.Config) []Check {
	p, err := ProviderFor(cfg.Provider)
	if err != nil {
		return []Check{{Name: "provider", Detail: err.Error()}}
	}
	binary := cfg.Providers.Claude.Path
	if cfg.Provider == "codex" {
		binary = cfg.Providers.Codex.Path
	}
	if binary != "" {
		binary = cfg.ExpandPath(binary)
	}
	raw := p.Doctor(ctx, binary)
	out := make([]Check, 0, len(raw))
	for _, c := range raw {
		out = append(out, checkOf(c))
	}
	return out
}

// sourceChecks reaches each configured source the cheapest way that proves
// the credentials and the transport work: describe for an adapter, and a
// lookup of an id that cannot exist for Zoho Desk, where "not found" is the
// answer that proves the token is good.
func sourceChecks(ctx context.Context, cfg *config.Config) []Check {
	var checks []Check
	for _, s := range []struct {
		role string
		sc   *config.SourceConfig
	}{{"tracker", cfg.Sources.Tracker}, {"helpdesk", cfg.Sources.Helpdesk}} {
		if s.sc == nil {
			checks = append(checks, Check{Name: "sources." + s.role, OK: true, Detail: "not configured"})
			continue
		}
		name := fmt.Sprintf("sources.%s (%s)", s.role, s.sc.Adapter)
		checks = append(checks, checkSource(ctx, cfg, name, s.sc))
	}
	return checks
}

func checkSource(ctx context.Context, cfg *config.Config, name string, sc *config.SourceConfig) Check {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()

	switch sc.Adapter {
	case "exec":
		command := ExpandCommand(cfg, sc.Command)
		client, err := plugin.Start(ctx, command, io.Discard)
		if err != nil {
			return Check{Name: name, Detail: err.Error()}
		}
		defer client.Close()
		d, err := client.Describe(ctx)
		if err != nil {
			return Check{Name: name, Detail: err.Error()}
		}
		return Check{Name: name, OK: true, Detail: fmt.Sprintf("%s v%s roles=%v", d.Name, d.Version, d.Roles)}

	case "zohodesk":
		token, err := (config.Resolver{Keychain: KeychainFor()}).Resolve(sc.Token)
		if err != nil {
			return Check{Name: name, Detail: fmt.Sprintf("token %s: %v", sc.Token, err)}
		}
		_, err = zohodesk.New(sc.BaseURL, sc.OrgID, token).Get(ctx, "0")
		var serr *source.Error
		switch {
		case err == nil:
			return Check{Name: name, OK: true, Detail: sc.BaseURL}
		case errors.As(err, &serr) && serr.Code == source.NotFound:
			// The API answered and rejected the id, not the token.
			return Check{Name: name, OK: true, Detail: sc.BaseURL}
		default:
			return Check{Name: name, Detail: err.Error()}
		}

	default:
		return Check{Name: name, Detail: fmt.Sprintf("unknown adapter %q", sc.Adapter)}
	}
}

// notesCheck proves the notes directory exists and takes writes, since a
// run that discovers otherwise has already spent an agent session.
func notesCheck(cfg *config.Config) Check {
	dir := cfg.ExpandPath(cfg.Notes.Dir)
	check := Check{Name: "notes.dir", Detail: dir}
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
func templatesCheck(cfg *config.Config) Check {
	dir := ""
	detail := "embedded defaults"
	if cfg.Notes.Templates != "" {
		dir = cfg.ExpandPath(cfg.Notes.Templates)
		detail = dir
	}
	if err := (note.Renderer{TemplatesDir: dir}).Check(); err != nil {
		return Check{Name: "notes.templates", Detail: err.Error()}
	}
	return Check{Name: "notes.templates", OK: true, Detail: detail}
}
