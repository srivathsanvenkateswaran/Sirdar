package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// GoldenEntry is one key in the golden set, as the Eval screen lists it.
// The bundle's contents are a real customer's ticket, so nothing of it
// crosses to the UI but the path and how much a human has written about it.
type GoldenEntry struct {
	Key        string `json:"key"`
	Dir        string `json:"dir"`
	BundleDir  string `json:"bundleDir"`
	Assertions int    `json:"assertions"`
	// HasExpectedNote says whether a human wrote the note they would have
	// written, which is what the overlap columns of a report compare
	// against.
	HasExpectedNote bool `json:"hasExpectedNote"`
	// HasRetro says whether the entry carries a retro.json, so `sirdar eval
	// --retro` can put the agent back at the commit the human fix branched
	// from. The Eval screen draws it as the entry's kind and decides from it
	// whether a suite is a retro or a plain replay.
	HasRetro bool `json:"hasRetro"`
}

// EvalReport is one recorded eval run: the report internal/eval wrote,
// plus the path it was read from.
type EvalReport struct {
	Path string `json:"path"`
	eval.Report
}

// maxEvalReports is how many of a workspace's recorded reports are read
// back. The screen shows the newest and offers the ones before it for
// comparison; a workspace that has evaluated nightly for a year does not
// need all of them parsed to draw one table.
const maxEvalReports = 20

// goldenDir is the golden set this service reads, with a leading "~/"
// expanded. It is the service's, never a caller's: see Options.GoldenDir.
func (s *Service) goldenDir() string { return eval.ExpandDir(s.opts.GoldenDir) }

// Golden lists the keys in the golden set. A golden set that does not
// exist yet is an empty list rather than an error: nothing has been added
// to it.
func (s *Service) Golden(wsID string) ([]GoldenEntry, error) {
	if _, err := s.root(wsID); err != nil {
		return nil, err
	}
	root := s.goldenDir()
	keys, err := eval.Keys(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []GoldenEntry{}, nil
		}
		return nil, err
	}
	out := make([]GoldenEntry, 0, len(keys))
	for _, key := range keys {
		g, err := eval.Load(root, key)
		if err != nil {
			// A key whose bundle will not load is reported as itself with
			// nothing in it, rather than costing the list its other rows.
			out = append(out, GoldenEntry{Key: key, Dir: filepath.Join(root, key)})
			continue
		}
		out = append(out, GoldenEntry{
			Key:             g.Key,
			Dir:             g.Dir,
			BundleDir:       g.BundleDir,
			Assertions:      len(g.Expected),
			HasExpectedNote: g.ExpectedMD != "",
			HasRetro:        eval.HasRetro(root, key),
		})
	}
	return out, nil
}

// AddGolden copies a completed run's bundle into the golden set and writes
// the expected.json skeleton beside it, which is what `sirdar golden add`
// does. An empty key is read off the run it names, so a UI can offer the
// button on a run without asking for the key again.
//
// The refusal to write a golden set into a git work tree is not overridden
// here: a bundle holds a real customer's conversation, and a UI is not the
// place to be talked past that.
func (s *Service) AddGolden(wsID, key, runID string) (GoldenEntry, error) {
	root, err := s.root(wsID)
	if err != nil {
		return GoldenEntry{}, err
	}
	if runID != "" {
		if err := checkID(ErrNoSuchRun, "run", runID); err != nil {
			return GoldenEntry{}, err
		}
	}
	if key == "" {
		if runID == "" {
			return GoldenEntry{}, fmt.Errorf("%w: name a key or a run id", ErrInvalidArgument)
		}
		_, state, err := store.Open(root, runID)
		if err != nil {
			return GoldenEntry{}, fmt.Errorf("%w: %s", ErrNoSuchRun, runID)
		}
		key = state.Key
	}
	if err := checkID(ErrNoSuchRun, "key", key); err != nil {
		return GoldenEntry{}, err
	}
	if _, err := eval.Add(root, s.opts.GoldenDir, key, runID, false); err != nil {
		return GoldenEntry{}, err
	}
	g, err := eval.Load(s.goldenDir(), key)
	if err != nil {
		return GoldenEntry{}, err
	}
	return GoldenEntry{
		Key:             g.Key,
		Dir:             g.Dir,
		BundleDir:       g.BundleDir,
		Assertions:      len(g.Expected),
		HasExpectedNote: g.ExpectedMD != "",
		HasRetro:        eval.HasRetro(s.goldenDir(), key),
	}, nil
}

// EvalReports returns the workspace's recorded eval reports, newest first.
func (s *Service) EvalReports(wsID string) ([]EvalReport, error) {
	root, err := s.root(wsID)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, ".sirdar", "eval")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []EvalReport{}, nil
		}
		return nil, fmt.Errorf("app: read %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		// A retro report is a different table with different columns; it
		// is read back by LatestRetro and would decode here as an eval
		// with no results in it.
		if strings.HasSuffix(e.Name(), eval.RetroSuffix) {
			continue
		}
		names = append(names, e.Name())
	}
	// The file names are UTC timestamps, so newest first is reverse order.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > maxEvalReports {
		names = names[:maxEvalReports]
	}

	out := make([]EvalReport, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rep eval.Report
		if json.Unmarshal(data, &rep) != nil {
			// A half-written report is skipped rather than failing the
			// list: the reports before it are still readable.
			continue
		}
		if rep.Results == nil {
			rep.Results = []eval.Result{}
		}
		out = append(out, EvalReport{Path: path, Report: rep})
	}
	return out, nil
}

// StartEval replays the golden set through real triage runs, scores them,
// and writes the report the Eval screen reads. An empty keys list means
// every key in the golden set.
//
// It is a job like every other start: the runs it makes are ordinary runs
// marked eval, so they appear in the watcher's stream and are cancelled
// through the same job id.
func (s *Service) StartEval(ctx context.Context, wsID string, keys []string, o EvalOptions) (JobID, error) {
	for _, key := range keys {
		if err := checkID(ErrNoSuchRun, "key", key); err != nil {
			return "", err
		}
	}
	root, err := s.root(wsID)
	if err != nil {
		return "", err
	}
	if o.Retro {
		return s.startRetro(ctx, wsID, root, keys, o)
	}
	return s.start(ctx, wsID, o.Provider, o.Model, func(jctx context.Context, deps runner.Deps) []JobOutcome {
		report, err := eval.Run(jctx, deps, keys, eval.Options{
			GoldenDir:   s.opts.GoldenDir,
			Model:       o.Model,
			Concurrency: o.Concurrency,
		})
		if err != nil {
			s.log(err)
			return s.failed(keys, nil)
		}
		if path, err := report.Write(root); err != nil {
			s.log(err)
		} else {
			s.logText("eval: wrote " + path)
		}
		out := make([]JobOutcome, 0, len(report.Results))
		for _, r := range report.Results {
			out = append(out, JobOutcome{Key: r.Key, Status: r.State, RunID: r.RunID})
		}
		return out
	}, func(err error) []JobOutcome { return s.failed(keys, err) })
}

// startRetro is the retro half of StartEval: each key replayed at the
// commit its fix branched from, scored against the pull request that fixed
// it. It writes its report beside the ordinary ones, under a name that
// keeps the two tables apart.
func (s *Service) startRetro(ctx context.Context, wsID, root string, keys []string, o EvalOptions) (JobID, error) {
	return s.start(ctx, wsID, o.Provider, o.Model, func(jctx context.Context, deps runner.Deps) []JobOutcome {
		rd := eval.NewRetroDeps(deps, o.Model)
		rd.Root = root
		report, err := eval.RunRetro(jctx, rd, keys, eval.RetroOptions{
			GoldenDir:   s.opts.GoldenDir,
			Model:       o.Model,
			Concurrency: o.Concurrency,
			WithRCA:     o.WithRCA,
			Rubric:      o.Rubric,
		})
		if err != nil {
			s.log(err)
			return s.failed(keys, nil)
		}
		if path, err := report.Write(root); err != nil {
			s.log(err)
		} else {
			s.logText("eval: wrote " + path)
		}
		out := make([]JobOutcome, 0, len(report.Results))
		for _, r := range report.Results {
			row := JobOutcome{Key: r.Key, Status: string(store.StatusFailed)}
			if r.Triage != nil {
				row.Status, row.RunID = r.Triage.State, r.Triage.RunID
			}
			out = append(out, row)
		}
		return out
	}, func(err error) []JobOutcome { return s.failed(keys, err) })
}

// RetroReport is one recorded retro report, with the path it was read from.
type RetroReport struct {
	Path string `json:"path"`
	eval.RetroReport
}

// LatestRetro returns the newest retro report recorded for a workspace, or
// nil when none has been run. It is one report rather than a list: the
// screen shows the last retro's table, and a retro is expensive enough that
// a workspace has a handful of them, not a nightly series.
func (s *Service) LatestRetro(wsID string) (*RetroReport, error) {
	root, err := s.root(wsID)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, ".sirdar", "eval")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("app: read %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), eval.RetroSuffix) {
			names = append(names, e.Name())
		}
	}
	// The file names are UTC timestamps, so newest first is reverse order.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rep eval.RetroReport
		if json.Unmarshal(data, &rep) != nil {
			// A half-written report is skipped, and the one before it
			// answers instead.
			continue
		}
		if rep.Results == nil {
			rep.Results = []eval.RetroResult{}
		}
		return &RetroReport{Path: path, RetroReport: rep}, nil
	}
	return nil, nil
}
