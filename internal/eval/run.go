package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// Options are the per-invocation flags of an eval run.
type Options struct {
	// GoldenDir is the golden set to replay. Empty means DefaultDir.
	GoldenDir string
	Model     string
	// Concurrency is how many keys are replayed at once. Zero takes the
	// workspace's configured concurrency.
	Concurrency int
}

// Result is one key's score.
type Result struct {
	Key     string  `json:"key"`
	RunID   string  `json:"runId"`
	State   string  `json:"state"`
	Reason  string  `json:"reason,omitempty"`
	Turns   int     `json:"turns"`
	CostUSD float64 `json:"costUsd"`
	Minutes float64 `json:"minutes"`

	// SchemaValid says whether the session produced a document that
	// validated against the triage schema. A run that ended any other way
	// has nothing to score, and its checks are all failures.
	SchemaValid bool    `json:"schemaValid"`
	Checks      []Check `json:"checks"`
	Passed      int     `json:"passed"`
	Total       int     `json:"total"`

	// Overlap is nil when the golden entry holds no expected.md.
	Overlap *Overlap `json:"overlap,omitempty"`
}

// Report is one eval invocation: what was replayed, against what, and how
// it scored.
type Report struct {
	At        time.Time `json:"at"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	GoldenDir string    `json:"goldenDir"`
	Results   []Result  `json:"results"`
}

// Run replays each key's golden bundle through a real triage run and scores
// the result. Keys are taken in the order given; an empty list means every
// key in the golden set.
//
// A key that cannot be loaded is a failed Result, not an aborted command:
// the point of an eval is the table at the end, and one missing bundle
// should not cost the other six their scores.
func Run(ctx context.Context, deps runner.Deps, keys []string, o Options) (Report, error) {
	if deps.Config == nil {
		return Report{}, fmt.Errorf("eval: no workspace configuration")
	}
	root := ExpandDir(o.GoldenDir)

	if len(keys) == 0 {
		found, err := Keys(root)
		if err != nil {
			return Report{}, err
		}
		if len(found) == 0 {
			return Report{}, fmt.Errorf("eval: %s holds no golden bundles; add one with `sirdar golden add KEY`", root)
		}
		keys = found
	}

	report := Report{
		At:        time.Now(),
		Model:     o.Model,
		GoldenDir: root,
		Results:   make([]Result, len(keys)),
	}
	if report.Model == "" {
		report.Model = deps.Config.Model
	}
	if deps.Provider != nil {
		report.Provider = deps.Provider.Name()
	}

	workers := o.Concurrency
	if workers <= 0 {
		workers = deps.Config.Concurrency
	}
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

	r := &runner.Runner{Deps: deps}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				report.Results[i] = replay(ctx, r, root, keys[i], o)
			}
		}()
	}
	wg.Wait()

	return report, nil
}

// replay runs one key and scores it.
func replay(ctx context.Context, r *runner.Runner, root, key string, o Options) Result {
	res := Result{Key: key, State: string(store.StatusFailed)}

	g, err := Load(root, key)
	if err != nil {
		res.Reason = err.Error()
		return res
	}
	res.Total = len(g.Expected)
	res.Checks = make([]Check, 0, len(g.Expected))

	outs, err := r.Triage(ctx, []string{key}, runner.Options{
		Model:       o.Model,
		Concurrency: 1,
		BundleDir:   g.BundleDir,
	})
	if err != nil || len(outs) == 0 {
		if err != nil {
			res.Reason = err.Error()
		}
		return res
	}
	out := outs[0]
	res.RunID = out.State.RunID
	res.State = string(out.State.Status)
	res.Reason = out.State.Reason
	res.Turns = out.State.Usage.Turns
	res.CostUSD = out.State.Usage.CostUSD
	if !out.State.StartedAt.IsZero() && out.State.UpdatedAt.After(out.State.StartedAt) {
		res.Minutes = out.State.UpdatedAt.Sub(out.State.StartedAt).Minutes()
	}

	runDir := filepath.Join(r.Config.Root, ".sirdar", "runs", key, res.RunID)
	doc, err := os.ReadFile(filepath.Join(runDir, "result.json"))
	if err != nil {
		// No document: every expectation fails, and the reason is
		// already on the result.
		res.Checks = Score(nil, g.Expected)
		return res
	}
	res.SchemaValid = note.Validate(note.Triage, doc) == nil
	res.Checks = Score(doc, g.Expected)
	for _, c := range res.Checks {
		if c.Pass {
			res.Passed++
		}
	}

	if g.ExpectedMD != "" {
		expected, errExp := os.ReadFile(g.ExpectedMD)
		produced, errProd := os.ReadFile(filepath.Join(runDir, "note.md"))
		if errExp == nil && errProd == nil {
			ov := Compare(string(expected), string(produced))
			res.Overlap = &ov
		}
	}
	return res
}

// Write saves the report as <root>/.sirdar/eval/<timestamp>.json and
// returns the path it wrote.
func (rep Report) Write(root string) (string, error) {
	dir := filepath.Join(root, ".sirdar", "eval")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("eval: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, rep.At.UTC().Format("20060102T150405Z")+".json")
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", fmt.Errorf("eval: marshal report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("eval: write %s: %w", path, err)
	}
	return path, nil
}

// Table renders the report as one aligned row per key.
func (rep Report) Table() string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tSTATE\tTURNS\tCOST\tMINS\tVALID\tASSERT\tREFS\tHEADS")
	for _, r := range rep.Results {
		fmt.Fprintf(w, "%s\t%s\t%d\t%.2f\t%.1f\t%s\t%s\t%s\t%s\n",
			r.Key, r.State, r.Turns, r.CostUSD, r.Minutes,
			yesNo(r.SchemaValid),
			fmt.Sprintf("%d/%d", r.Passed, r.Total),
			pct(r.Overlap, func(o *Overlap) Fraction { return o.Refs }),
			pct(r.Overlap, func(o *Overlap) Fraction { return o.Headings }),
		)
	}
	w.Flush()
	out := buf.String()

	var notes []string
	for _, r := range rep.Results {
		for _, c := range r.Checks {
			if !c.Pass {
				notes = append(notes, fmt.Sprintf("%s: %s — %s", r.Key, c.Key, c.Detail))
			}
		}
		if r.Reason != "" && r.State != string(store.StatusCompleted) {
			notes = append(notes, fmt.Sprintf("%s: %s — %s", r.Key, r.State, r.Reason))
		}
	}
	if len(notes) > 0 {
		out = strings.TrimRight(out, "\n") + "\n\n" + strings.Join(notes, "\n") + "\n"
	}
	return out
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// pct renders one overlap fraction as "3/4 75%", or "-" when the golden
// entry carried no human-written note to compare against.
func pct(o *Overlap, pick func(*Overlap) Fraction) string {
	if o == nil {
		return "-"
	}
	f := pick(o)
	if f.Total == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d %.0f%%", f.Matched, f.Total, f.Score*100)
}

// ExitCode reports the process status for a report: 1 when any key failed
// to produce a valid note or failed an assertion, else 0. An eval that
// scores badly is a failing eval; that is what makes it usable in CI.
func ExitCode(rep Report) int {
	for _, r := range rep.Results {
		if !r.SchemaValid || r.Passed != r.Total {
			return 1
		}
	}
	return 0
}
