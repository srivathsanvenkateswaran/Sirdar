package shadow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// rcaDoc is the part of an RCA result.json that serves as truth: what the
// operator's own RCA concluded the cause was.
type rcaDoc struct {
	RCA struct {
		Summary        string `json:"summary"`
		Classification string `json:"classification"`
	} `json:"rca"`
}

// runDir is where store keeps a run's files. store.List hands back states,
// not directories, and the layout is fixed by store.CreateID.
func runDir(root, key, runID string) string {
	return filepath.Join(root, ".sirdar", "runs", key, runID)
}

// OperatorOutcomes reads the operator's own non-measurement runs on key and returns the outcome
// lines the ledger does not already hold, plus the StartedAt of the operator's first such run.
func OperatorOutcomes(root, key string, have []Entry, now time.Time) (lines []Entry, firstStart time.Time, err error) {
	states, err := store.List(root, key)
	if err != nil {
		return nil, time.Time{}, err
	}
	seen := outcomeSet(have)
	// store.List is newest first; walking it backwards appends the lines in
	// the order the operator's runs happened.
	for i := len(states) - 1; i >= 0; i-- {
		st := states[i]
		if st.Measurement() {
			continue
		}
		if firstStart.IsZero() || st.StartedAt.Before(firstStart) {
			firstStart = st.StartedAt
		}
		line, ok := operatorLine(root, st)
		if !ok {
			continue
		}
		line.Ev, line.Key, line.At = "outcome", key, now
		if seen[outcomeID(line)] {
			continue
		}
		seen[outcomeID(line)] = true
		lines = append(lines, line)
	}
	return lines, firstStart, nil
}

// operatorLine is what one of the operator's runs says about how the ticket
// ended, if anything. A run still going or one that failed says nothing,
// except a fix run that got as far as opening a pull request.
func operatorLine(root string, st store.State) (Entry, bool) {
	dir := runDir(root, st.Key, st.RunID)
	switch st.Kind {
	case store.KindRCA:
		if st.Status != store.StatusCompleted {
			return Entry{}, false
		}
		e := Entry{Source: "operator-rca", RunID: st.RunID}
		if in := st.RCAInputs; in != nil {
			if in.PRURL != "" {
				e.PRURLs = []string{in.PRURL}
			}
			e.Resolution = in.Resolution
		}
		// An RCA whose result.json cannot be read still carries the PR and
		// resolution the operator gave it, which are the stronger truth.
		if raw, err := os.ReadFile(filepath.Join(dir, "result.json")); err == nil {
			var doc rcaDoc
			if json.Unmarshal(raw, &doc) == nil {
				e.Summary = cutRunes(doc.RCA.Summary, maxSummaryRunes)
				e.Classification = doc.RCA.Classification
			}
		}
		return e, true
	case store.KindFix:
		if st.Fix.PRURL == "" {
			return Entry{}, false
		}
		return Entry{Source: "operator-fix", RunID: st.RunID, PRURLs: []string{st.Fix.PRURL}}, true
	case store.KindTriage:
		if st.Status != store.StatusCompleted {
			return Entry{}, false
		}
		v, ok := VerdictOf(dir)
		if !ok {
			return Entry{}, false
		}
		v.CodeRefs = capList(v.CodeRefs)
		v.OpenQuestions = capList(v.OpenQuestions)
		return Entry{Source: "operator-triage", RunID: st.RunID, Verdict: v}, true
	}
	return Entry{}, false
}

// maxSummaryRunes bounds the operator RCA summary a ledger line carries.
// The schema asks for three to five sentences; this is room for those and
// a guard against a summary that is a pasted log.
const maxSummaryRunes = 2000

// maxListItems and maxItemRunes bound the code references and open
// questions an operator-triage line copies from a note, which sets no
// limit of its own. At these sizes the two lists stay well under the
// ledger's 64 KiB line limit, and the judge still sees what a reader would.
const (
	maxListItems = 20
	maxItemRunes = 300
)

func capList(items []string) []string {
	if len(items) > maxListItems {
		items = items[:maxListItems]
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = cutRunes(it, maxItemRunes)
	}
	return out
}

// MaxManualPRs and MaxManualPRURLLen bound the pull requests one manual
// outcome may name. The outcome is a single ledger line, and a request
// that could carry any number of URLs of any length could push it past the
// line limit and lose it.
const (
	MaxManualPRs      = 10
	MaxManualPRURLLen = 2000
)

// ManualOutcome validates and builds a manual outcome line.
func ManualOutcome(key string, prURLs []string, resolution string, now time.Time) (Entry, error) {
	if len(prURLs) > MaxManualPRs {
		return Entry{}, fmt.Errorf("shadow: an outcome names at most %d pull request URLs; got %d", MaxManualPRs, len(prURLs))
	}
	var urls []string
	for _, u := range prURLs {
		u = strings.TrimSpace(u)
		if n := utf8.RuneCountInString(u); n > MaxManualPRURLLen {
			return Entry{}, fmt.Errorf("shadow: a pull request URL is at most %d characters; got one of %d", MaxManualPRURLLen, n)
		}
		if err := eval.CheckPRURL(u); err != nil {
			return Entry{}, err
		}
		urls = append(urls, u)
	}
	resolution = cutRunes(strings.TrimSpace(resolution), ticket.OutcomeResolutionMax)
	if len(urls) == 0 && resolution == "" {
		return Entry{}, errors.New("shadow: an outcome needs a --pr URL or a resolution")
	}
	return Entry{At: now, Ev: "outcome", Key: key, Source: "manual", PRURLs: urls, Resolution: resolution}, nil
}

// TrackerOutcome asks an Outcomer and returns a new line, or ok=false when it says nothing new.
func TrackerOutcome(ctx context.Context, o source.Outcomer, key string, have []Entry, now time.Time) (line Entry, ok bool, err error) {
	out, err := o.Outcome(ctx, key)
	if err != nil {
		// An adapter that turns out not to support the method is the same
		// as one that never advertised it: no tracker outcome, and the
		// other sources carry on.
		var se *source.Error
		if errors.As(err, &se) && se.Code == source.Unsupported {
			return Entry{}, false, nil
		}
		return Entry{}, false, err
	}
	line = Entry{
		At:         now,
		Ev:         "outcome",
		Key:        key,
		Source:     "tracker",
		Status:     cutRunes(out.Status, MaxFieldRunes),
		Resolution: cutRunes(out.Resolution, ticket.OutcomeResolutionMax),
		PRURLs:     capTrackerPRs(out.PullRequests),
	}
	if !out.ResolvedAt.IsZero() {
		at := out.ResolvedAt
		line.ResolvedAt = &at
	}
	if outcomeSet(have)[outcomeID(line)] {
		return Entry{}, false, nil
	}
	return line, true, nil
}

// capTrackerPRs keeps the first MaxManualPRs of an adapter's PR URLs that
// are no longer than MaxManualPRURLLen, the bounds a manual entry is held
// to. A tracker's list is not checked anywhere else, and an outcome line
// pushed over the ledger's limit would never land. An over-long URL is
// dropped rather than cut, because a cut URL names some other page.
func capTrackerPRs(urls []string) []string {
	var out []string
	for _, u := range urls {
		if len(out) == MaxManualPRs {
			break
		}
		if utf8.RuneCountInString(u) <= MaxManualPRURLLen {
			out = append(out, u)
		}
	}
	return out
}

// outcomeID is what makes two outcome lines the same observation. The PR
// list is sorted because a tracker may list the same pull requests in a
// different order from one read to the next, and that is not news.
func outcomeID(e Entry) string {
	prs := append([]string(nil), e.PRURLs...)
	sort.Strings(prs)
	// The parts are joined with a byte none of them can hold, so two
	// different splits cannot produce the same id.
	return strings.Join([]string{e.Source, e.RunID, strings.Join(prs, "\x01"), e.Resolution, e.Status}, "\x00")
}

func outcomeSet(have []Entry) map[string]bool {
	set := map[string]bool{}
	for _, e := range have {
		if e.Ev == "outcome" {
			set[outcomeID(e)] = true
		}
	}
	return set
}
