package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// ErrNotSupported is what a stage returns on a build that cannot run it —
// `--at` and `fix --local` are the retro-b half of this feature, and a
// binary built before they landed says so plainly rather than silently
// scoring a run that stood at the wrong commit.
var ErrNotSupported = errors.New("not supported on this build")

// Stage is one run a retro made: the ordinary run state, plus where the run
// left the two files a score reads.
type Stage struct {
	RunID   string  `json:"runId,omitempty"`
	State   string  `json:"state,omitempty"`
	Reason  string  `json:"reason,omitempty"`
	Turns   int     `json:"turns,omitempty"`
	CostUSD float64 `json:"costUsd,omitempty"`
	Minutes float64 `json:"minutes,omitempty"`
	// DocPath is the run's structured answer (result.json).
	DocPath string `json:"docPath,omitempty"`
	// NotePath is the rendered note, which for an eval-marked run stays
	// in the run directory rather than being filed.
	NotePath string `json:"notePath,omitempty"`
}

// OK reports whether the stage finished with an answer to score.
func (s Stage) OK() bool { return s.State == string(store.StatusCompleted) }

// FixStage is a local fix run: a Stage plus the commit it made in its own
// worktree and the diff of that commit.
type FixStage struct {
	Stage
	// DiffPath is the unified diff the fix flow wrote, <run-dir>/fix.diff.
	DiffPath string `json:"diffPath,omitempty"`
	// Commit is the sha the fix committed in its worktree. Nothing is
	// pushed and no pull request is opened.
	Commit string `json:"commit,omitempty"`
	// BuildPassed is what the session reported about its own build, when
	// the fix flow read it. Left nil, the score reads the run's report.
	BuildPassed *bool `json:"buildPassed,omitempty"`
}

// TriageAt asks for one triage run against a stored bundle with the
// workspace standing at a commit.
type TriageAt struct {
	// Commit is the sha the run's tree is put at. A run with no commit is
	// an ordinary replay against the working tree.
	Commit string
	// BundleDir is the golden bundle replayed instead of fetching the
	// ticket, which for a retro is the as-of capture: the ticket as it
	// stood before anyone wrote down the answer.
	BundleDir string
	Model     string
}

// FixLocalAt asks for one fix run that commits in its own worktree and
// stops: no push, no pull request, and a diff written for the score to
// read.
type FixLocalAt struct {
	Commit string
	Model  string
	// TriageNote is the note this fix implements — the one the retro's own
	// triage stage just produced, not the newest note for the key. An eval
	// run files no note, so nothing else would find it.
	TriageNote string
	// TriageRunID names the run that note came from.
	TriageRunID string
}

// RCAAt asks for one RCA run at a commit. It carries no pull request URL
// on purpose: the point of the retro RCA is what the agent concludes
// without being shown the answer, and an option that could carry it would
// eventually be filled in.
type RCAAt struct {
	Commit      string
	Model       string
	BundleDir   string
	TriageRunID string
	TriageNote  string
}

// RetroRunner is everything a retro replay needs from the run layer.
// internal/run satisfies it (see runner_adapter.go); a test fakes it.
type RetroRunner interface {
	TriageAt(ctx context.Context, key string, o TriageAt) (Stage, error)
	FixLocalAt(ctx context.Context, key string, o FixLocalAt) (FixStage, error)
	RCAAt(ctx context.Context, key string, o RCAAt) (Stage, error)
}

// RetroDeps is what RunRetro is handed: the runs it makes, the judge it
// may ask for a rubric, and the two names that go on the report.
type RetroDeps struct {
	Runner RetroRunner
	// Judge is nil when the workspace has no provider to ask, which makes
	// --rubric a no-op rather than an error.
	Judge    Rubricer
	Provider string
	Model    string
	// Root is the workspace, where the report is written.
	Root string
}

// RetroOptions are the flags of one `sirdar eval --retro`.
type RetroOptions struct {
	GoldenDir   string
	Model       string
	Concurrency int
	// WithRCA adds the blind RCA stage, which costs a third session per
	// key and is off by default.
	WithRCA bool
	// Rubric adds one provider call per key that reads both diffs. Off by
	// default: it is the only part of a retro score that is another
	// model's opinion rather than a measurement.
	Rubric bool
}

// RetroResult is one key's retro: the three runs it made and what they
// scored against the change a human actually merged.
type RetroResult struct {
	Key        string    `json:"key"`
	BaseCommit string    `json:"baseCommit,omitempty"`
	AsOf       time.Time `json:"asOf,omitempty"`
	PRURLs     []string  `json:"prUrls,omitempty"`
	// Reason is why this key has less in it than the others: a bundle
	// that would not load, a stage that failed, a build without `--at`.
	Reason string `json:"reason,omitempty"`

	Triage *Stage    `json:"triage,omitempty"`
	Fix    *FixStage `json:"fix,omitempty"`
	RCA    *Stage    `json:"rca,omitempty"`

	TriageScore *TriageScore `json:"triageScore,omitempty"`
	FixScore    *FixScore    `json:"fixScore,omitempty"`
	Rubric      *Rubric      `json:"rubric,omitempty"`

	// CostUSD is every session this key spent, the rubric call included.
	CostUSD       float64 `json:"costUsd"`
	RubricCostUSD float64 `json:"rubricCostUsd,omitempty"`
}

// RetroReport is one `sirdar eval --retro` invocation.
type RetroReport struct {
	At        time.Time     `json:"at"`
	Provider  string        `json:"provider"`
	Model     string        `json:"model"`
	GoldenDir string        `json:"goldenDir"`
	WithRCA   bool          `json:"withRca"`
	Rubric    bool          `json:"rubric"`
	Results   []RetroResult `json:"results"`
}

// RunRetro replays each key as it stood before the bug was fixed and scores
// what comes back against the change that fixed it.
//
// It is a measurement, not a gate. Nothing here fails the command: a key
// whose bundle will not load, whose triage went nowhere, or whose build
// cannot run a fix at a commit is a row with a reason on it, because the
// table is the output and one broken key should not cost the others theirs.
func RunRetro(ctx context.Context, d RetroDeps, keys []string, o RetroOptions) (RetroReport, error) {
	if d.Runner == nil {
		return RetroReport{}, fmt.Errorf("eval: no runner for a retro replay")
	}
	root := ExpandDir(o.GoldenDir)

	if len(keys) == 0 {
		found, err := RetroKeys(root)
		if err != nil {
			return RetroReport{}, err
		}
		if len(found) == 0 {
			return RetroReport{}, fmt.Errorf("eval: no golden bundle in %s carries a %s; add one with `sirdar golden add KEY --retro --pr URL`", root, RetroFile)
		}
		keys = found
	}

	report := RetroReport{
		At:        time.Now(),
		Provider:  d.Provider,
		Model:     o.Model,
		GoldenDir: root,
		WithRCA:   o.WithRCA,
		Rubric:    o.Rubric,
		Results:   make([]RetroResult, len(keys)),
	}
	if report.Model == "" {
		report.Model = d.Model
	}

	workers := o.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(keys) {
		workers = len(keys)
	}
	queue := make(chan int, len(keys))
	for i := range keys {
		queue <- i
	}
	close(queue)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				report.Results[i] = replayRetro(ctx, d, root, keys[i], o)
			}
		}()
	}
	wg.Wait()
	return report, nil
}

// replayRetro is one key: triage at the base commit, a local fix from that
// triage note, and optionally a blind RCA and a rubric call.
func replayRetro(ctx context.Context, d RetroDeps, root, key string, o RetroOptions) RetroResult {
	res := RetroResult{Key: key}

	g, err := Load(root, key)
	if err != nil {
		res.Reason = err.Error()
		return res
	}
	retro, err := LoadRetro(root, key)
	if err != nil {
		res.Reason = err.Error()
		return res
	}
	res.BaseCommit, res.AsOf, res.PRURLs = retro.BaseCommit, retro.AsOf, retro.PRURLs

	prDiff, err := retro.ReadDiff()
	if err != nil {
		res.Reason = err.Error()
		return res
	}
	prFiles := retro.Files(prDiff)

	triage, err := d.Runner.TriageAt(ctx, key, TriageAt{
		Commit:    retro.BaseCommit,
		BundleDir: g.BundleDir,
		Model:     o.Model,
	})
	res.Triage = &triage
	res.CostUSD += triage.CostUSD
	if err != nil {
		res.Reason = err.Error()
		return res
	}
	doc := readFile(triage.DocPath)
	if len(doc) > 0 {
		score := ScoreTriage(doc, string(readFile(triage.NotePath)), prFiles)
		res.TriageScore = &score
	}
	if !triage.OK() {
		res.Reason = stageReason("triage", triage)
		return res
	}

	fix, err := d.Runner.FixLocalAt(ctx, key, FixLocalAt{
		Commit:      retro.BaseCommit,
		Model:       o.Model,
		TriageNote:  triage.NotePath,
		TriageRunID: triage.RunID,
	})
	res.Fix = &fix
	res.CostUSD += fix.CostUSD
	if err != nil {
		res.Reason = err.Error()
	}

	agentDiffText := string(readFile(fix.DiffPath))
	if fix.DiffPath != "" && agentDiffText == "" && res.Reason == "" {
		res.Reason = "the fix run left no diff at " + fix.DiffPath
	}
	if err == nil || agentDiffText != "" {
		agentDiff := ParseDiff(agentDiffText)
		passed := fix.BuildPassed
		if passed == nil {
			passed = BuildPassed(readFile(fix.DocPath))
		}
		score := ScoreFix(prDiff, agentDiff, prFiles, passed)
		score.DiffPath = fix.DiffPath
		res.FixScore = &score
	}
	if res.Reason == "" && !fix.OK() {
		res.Reason = stageReason("fix", fix.Stage)
	}

	if o.WithRCA {
		rca, err := d.Runner.RCAAt(ctx, key, RCAAt{
			Commit:      retro.BaseCommit,
			Model:       o.Model,
			BundleDir:   g.BundleDir,
			TriageRunID: triage.RunID,
			TriageNote:  triage.NotePath,
		})
		res.RCA = &rca
		res.CostUSD += rca.CostUSD
		if err != nil && res.Reason == "" {
			res.Reason = err.Error()
		}
	}

	if o.Rubric && d.Judge != nil && agentDiffText != "" {
		prText := string(readFile(retro.DiffPath()))
		verdict, cost, err := d.Judge.Judge(ctx, prText, agentDiffText)
		res.RubricCostUSD = cost
		res.CostUSD += cost
		if err != nil {
			if res.Reason == "" {
				res.Reason = "rubric: " + err.Error()
			}
		} else {
			res.Rubric = &verdict
		}
	}
	return res
}

// stageReason names the stage a row stopped at, since a bare "failed" on a
// three-session row says nothing about which session it was.
func stageReason(name string, s Stage) string {
	if s.Reason != "" {
		return name + ": " + s.State + " — " + s.Reason
	}
	return name + ": " + s.State
}

// readFile reads a path a stage reported, returning nothing for a path it
// did not report or a file it did not write. Every caller here treats a
// missing file as an unscored column, not an error.
func readFile(path string) []byte {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// RetroSuffix marks a retro report's file name, which is what keeps it out
// of the triage reports the Eval screen's first table reads.
const RetroSuffix = "-retro.json"

// Write saves the report as <root>/.sirdar/eval/<timestamp>-retro.json.
func (rep RetroReport) Write(root string) (string, error) {
	dir := filepath.Join(root, ".sirdar", "eval")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("eval: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, rep.At.UTC().Format("20060102T150405Z")+RetroSuffix)
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", fmt.Errorf("eval: marshal retro report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("eval: write %s: %w", path, err)
	}
	return path, nil
}

// Table renders the retro report as one aligned row per key, with the
// reasons underneath.
func (rep RetroReport) Table() string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tCLASS\tCONF\tREFS\tPRFILES\tFILES\tHUNKS\tBUILD\tRUBRIC\tCOST")
	for _, r := range rep.Results {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%.2f\n",
			r.Key,
			dash(triagePart(r, func(t TriageScore) string { return t.Classification })),
			dash(triagePart(r, func(t TriageScore) string { return t.Confidence })),
			retroPct(r.TriageScore, func(t TriageScore) Fraction { return t.CodeRefsPathOverlap }),
			retroPct(r.TriageScore, func(t TriageScore) Fraction { return t.PRFilesHit }),
			jaccardText(r.FixScore),
			fixPct(r.FixScore, func(f FixScore) Fraction { return f.HunkOverlap }),
			buildText(r.FixScore),
			rubricText(r.Rubric),
			r.CostUSD,
		)
	}
	w.Flush()
	out := buf.String()

	var notes []string
	for _, r := range rep.Results {
		if r.Reason != "" {
			notes = append(notes, fmt.Sprintf("%s: %s", r.Key, r.Reason))
		}
		if r.TriageScore != nil && len(r.TriageScore.MissedFiles) > 0 {
			notes = append(notes, fmt.Sprintf("%s: the note never named %s",
				r.Key, strings.Join(r.TriageScore.MissedFiles, ", ")))
		}
		if r.Rubric != nil && r.Rubric.Reasoning != "" {
			notes = append(notes, fmt.Sprintf("%s: rubric %s — %s", r.Key, r.Rubric.Verdict, r.Rubric.Reasoning))
		}
	}
	if len(notes) > 0 {
		out = strings.TrimRight(out, "\n") + "\n\n" + strings.Join(notes, "\n") + "\n"
	}
	return out
}

func triagePart(r RetroResult, pick func(TriageScore) string) string {
	if r.TriageScore == nil {
		return ""
	}
	return pick(*r.TriageScore)
}

func retroPct(t *TriageScore, pick func(TriageScore) Fraction) string {
	if t == nil {
		return "-"
	}
	f := pick(*t)
	if f.Total == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d %.0f%%", f.Matched, f.Total, f.Score*100)
}

func fixPct(s *FixScore, pick func(FixScore) Fraction) string {
	if s == nil {
		return "-"
	}
	f := pick(*s)
	if f.Total == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d %.0f%%", f.Matched, f.Total, f.Score*100)
}

func jaccardText(s *FixScore) string {
	if s == nil || s.FilesJaccard.Union == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d %.0f%%", s.FilesJaccard.Intersection, s.FilesJaccard.Union, s.FilesJaccard.Score*100)
}

func buildText(s *FixScore) string {
	if s == nil || s.BuildPassed == nil {
		return "-"
	}
	return yesNo(*s.BuildPassed)
}

func rubricText(r *Rubric) string {
	if r == nil {
		return "-"
	}
	return r.Verdict
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
