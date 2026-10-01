package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider/claude"
)

// The three places a model in the picker can have come from.
const (
	// ModelFromProbe is a model the provider's CLI said an alias resolves
	// to on this login (claude only; see claude.ProbeModel).
	ModelFromProbe = "probe"
	// ModelFromRun is a model one of the workspace's runs reported using.
	ModelFromRun = "run"
	// ModelFromConfig is one the operator pinned under providers.<p>.models.
	ModelFromConfig = "config"
)

// ModelInfo is one model the picker offers.
type ModelInfo struct {
	// ID is what the start carries as --model.
	ID string `json:"id"`
	// Label is what the chip says: the operator's own words for a pin,
	// otherwise derived from the id (ModelLabel).
	Label string `json:"label"`
	// Source is where it was found: probe, run or config. A model found in
	// more than one place is listed once, under the first of those.
	Source string `json:"source"`
	// SeenAt is when: the probe's time, or the newest run that reported
	// it. Empty for a pin, which is a statement rather than a sighting.
	SeenAt string `json:"seenAt,omitempty"`
	// Alias is the name a probe asked about — opus, sonnet, haiku, or the
	// workspace's configured model — when the source is probe.
	Alias string `json:"alias,omitempty"`
}

// ModelList is what the picker draws for one provider.
type ModelList struct {
	Provider string      `json:"provider"`
	Models   []ModelInfo `json:"models"`
	// CanProbe is true for a provider whose CLI can be asked (claude).
	CanProbe bool `json:"canProbe"`
	// ProbedAt is when the cached probe was taken; empty when never.
	ProbedAt string `json:"probedAt,omitempty"`
	// ProbeDue says the cache is empty or older than ModelsTTL: a picker
	// that opens now asks RefreshModels for a new one. Models itself never
	// probes, so opening the app starts no CLI.
	ProbeDue bool `json:"probeDue"`
	// ProbeErrors names each alias the last probe could not resolve, with
	// the CLI's reason.
	ProbeErrors []string `json:"probeErrors,omitempty"`
}

// ModelsTTL is how long a probe is reused before a picker asks again.
const ModelsTTL = 24 * time.Hour

// refreshDebounce is how recent a probe has to be for a Refresh to answer
// with it rather than start the CLI again: a double click, or two pickers
// opening together on an empty cache, probes once.
const refreshDebounce = 30 * time.Second

// probeAliases are the names asked about on every claude probe. The
// workspace's configured model joins them when it names one.
var probeAliases = []string{"opus", "sonnet", "haiku"}

// ProbeFunc resolves one alias with the CLI at binary. Tests swap it.
type ProbeFunc func(ctx context.Context, binary, alias string, env []string) (string, error)

// modelCache is what is kept under <UserDir>/models/<provider>.json.
type modelCache struct {
	ProbedAt time.Time     `json:"probedAt"`
	Models   []probedModel `json:"models"`
	Errors   []string      `json:"errors,omitempty"`
}

type probedModel struct {
	Alias string `json:"alias"`
	ID    string `json:"id"`
}

// modelProbes serialises refreshes, so two pickers opening together start
// the CLI once.
type modelProbes struct {
	mu sync.Mutex
}

// modelsDir is where the probe cache lives.
func (s *Service) modelsDir() (string, error) {
	if s.opts.ModelsDir != "" {
		return s.opts.ModelsDir, nil
	}
	dir, err := config.UserDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "models"), nil
}

func (s *Service) probeFunc() ProbeFunc {
	if s.opts.ProbeModel != nil {
		return s.opts.ProbeModel
	}
	return claude.ProbeModel
}

// pickerProvider is the provider a models call is about: the one named, or
// the workspace's own.
func pickerProvider(cfg *config.Config, provider string) (string, error) {
	if provider == "" {
		provider = string(cfg.Provider)
	}
	if err := CheckProvider(provider); err != nil {
		return "", err
	}
	return provider, nil
}

// Models lists what the picker offers for a provider: the cached probe,
// every model the workspace's runs reported, newest first, and the
// operator's pins, each id once. It never starts the CLI — see ProbeDue
// and RefreshModels.
func (s *Service) Models(wsID, provider string) (ModelList, error) {
	_, cfg, err := s.load(wsID)
	if err != nil {
		return ModelList{}, err
	}
	provider, err = pickerProvider(cfg, provider)
	if err != nil {
		return ModelList{}, err
	}
	runs, err := s.Runs(wsID, "")
	if err != nil {
		return ModelList{}, err
	}
	var cache *modelCache
	if provider == "claude" {
		cache = s.readModelCache(provider)
	}
	return mergeModels(provider, cache, runs, cfg.PinnedModels(provider), s.now()), nil
}

// RefreshModels probes the provider's CLI again, keeps the answer for
// ModelsTTL, and answers with the list Models would now give. A provider
// that cannot be probed answers with its list unchanged.
func (s *Service) RefreshModels(ctx context.Context, wsID, provider string) (ModelList, error) {
	_, cfg, err := s.load(wsID)
	if err != nil {
		return ModelList{}, err
	}
	provider, err = pickerProvider(cfg, provider)
	if err != nil {
		return ModelList{}, err
	}
	if provider == "claude" {
		if err := s.probeClaude(ctx, cfg); err != nil {
			return ModelList{}, err
		}
	}
	return s.Models(wsID, provider)
}

// probeClaude asks the CLI about each alias, in parallel, and writes the
// cache. A probe already taken in the last refreshDebounce is reused.
func (s *Service) probeClaude(ctx context.Context, cfg *config.Config) error {
	s.probes.mu.Lock()
	defer s.probes.mu.Unlock()
	if c := s.readModelCache("claude"); c != nil && s.now().Sub(c.ProbedAt) < refreshDebounce {
		return nil
	}

	aliases := append([]string(nil), probeAliases...)
	if (cfg.Provider == "" || cfg.Provider == "claude") && strings.TrimSpace(cfg.Model) != "" {
		m := strings.TrimSpace(cfg.Model)
		if !contains(aliases, m) {
			aliases = append(aliases, m)
		}
	}
	env := os.Environ()
	if cfg.Billing == "api" {
		env = append(env, "SIRDAR_BILLING=api")
	}
	binary := cfg.ExpandPath(cfg.Providers.Claude.Path)
	probe := s.probeFunc()

	type answer struct {
		id  string
		err error
	}
	answers := make([]answer, len(aliases))
	var wg sync.WaitGroup
	for i, alias := range aliases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := probe(ctx, binary, alias, env)
			answers[i] = answer{strings.TrimSpace(id), err}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}

	c := modelCache{ProbedAt: s.now()}
	for i, a := range answers {
		switch {
		case a.err != nil:
			c.Errors = append(c.Errors, aliases[i]+": "+a.err.Error())
		case a.id != "":
			c.Models = append(c.Models, probedModel{Alias: aliases[i], ID: a.id})
		}
	}
	return s.writeModelCache("claude", c)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (s *Service) readModelCache(provider string) *modelCache {
	dir, err := s.modelsDir()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, provider+".json"))
	if err != nil {
		return nil
	}
	var c modelCache
	if json.Unmarshal(raw, &c) != nil || c.ProbedAt.IsZero() {
		return nil
	}
	return &c
}

func (s *Service) writeModelCache(provider string, c modelCache) error {
	dir, err := s.modelsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("models cache: %w", err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, provider+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("models cache: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(fmt.Errorf("models cache: %w", err), os.Remove(tmp))
	}
	return nil
}

// mergeModels is the list: probe, then runs (newest first), then pins,
// each id once and under the first source that has it. A pin's label wins
// wherever its id is listed, since it is the operator's own word for it.
func mergeModels(provider string, cache *modelCache, runs []RunSummary, pins []config.ModelPin, now time.Time) ModelList {
	out := ModelList{Provider: provider, Models: []ModelInfo{}, CanProbe: provider == "claude"}
	labels := map[string]string{}
	for _, p := range pins {
		if p.Label != "" {
			labels[p.ID] = p.Label
		}
	}
	seen := map[string]bool{}
	add := func(m ModelInfo) {
		if m.ID == "" || seen[m.ID] {
			return
		}
		seen[m.ID] = true
		if l := labels[m.ID]; l != "" {
			m.Label = l
		} else if m.Label == "" {
			m.Label = ModelLabel(m.ID)
		}
		out.Models = append(out.Models, m)
	}

	if out.CanProbe {
		out.ProbeDue = cache == nil || now.Sub(cache.ProbedAt) >= ModelsTTL
	}
	if cache != nil {
		out.ProbedAt = wireTime(cache.ProbedAt)
		out.ProbeErrors = cache.Errors
		for _, m := range cache.Models {
			add(ModelInfo{ID: m.ID, Source: ModelFromProbe, SeenAt: out.ProbedAt, Alias: m.Alias})
		}
	}
	for _, r := range runs {
		if r.Provider != provider {
			continue
		}
		at := r.UpdatedAt
		if at == "" {
			at = r.StartedAt
		}
		add(ModelInfo{ID: strings.TrimSpace(r.Model), Source: ModelFromRun, SeenAt: at})
	}
	for _, p := range pins {
		add(ModelInfo{ID: p.ID, Label: p.Label, Source: ModelFromConfig})
	}
	return out
}

var (
	dateSuffix = regexp.MustCompile(`^\d{8}$`)
	numeric    = regexp.MustCompile(`^\d+$`)
	contextTag = regexp.MustCompile(`\[(\d+)([km])\]$`)
)

// modelFamilies are the names a bare alias or a versionless id may carry.
var modelFamilies = []string{"opus", "sonnet", "haiku", "fable"}

// ModelLabel is how an id reads on a chip: "claude-opus-4-5-20251101" is
// "Opus 4.5", "claude-3-5-sonnet-20241022" is "Sonnet 3.5",
// "claude-opus-5[1m]" is "Opus 5 (1M)", and the bare aliases are their
// own capitalised names. Anything that is not a Claude id — a Codex or
// OpenAI name — reads as the id itself, which is what the CLI is told.
func ModelLabel(id string) string {
	id = strings.TrimSpace(id)
	suffix := ""
	if m := contextTag.FindStringSubmatch(id); m != nil {
		suffix = " (" + m[1] + strings.ToUpper(m[2]) + ")"
		id = strings.TrimSuffix(id, m[0])
	}
	if contains(modelFamilies, id) {
		return strings.ToUpper(id[:1]) + id[1:] + suffix
	}
	rest, ok := strings.CutPrefix(id, "claude-")
	if !ok || rest == "" {
		return id + suffix
	}
	var family string
	var version []string
	for _, tok := range strings.Split(rest, "-") {
		switch {
		case dateSuffix.MatchString(tok):
			// The snapshot date says nothing a reader picks by.
		case numeric.MatchString(tok):
			version = append(version, tok)
		case family == "" && tok != "":
			family = tok
		default:
			return id + suffix
		}
	}
	// A family with no version is only a name when it is one of the known
	// ones: "claude-next" is somebody's id, not a model called Next.
	if family == "" || (len(version) == 0 && !contains(modelFamilies, family)) {
		return id + suffix
	}
	label := strings.ToUpper(family[:1]) + family[1:]
	if len(version) > 0 {
		label += " " + strings.Join(version, ".")
	}
	return label + suffix
}
