package shadow

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// ShadowReport is the scorecard: how shadow's verdicts compared with what
// the operator did, over the runs started since a point in time.
type ShadowReport struct {
	Since            string     `json:"since"` // RFC 3339
	Headline         Headline   `json:"headline"`
	Rows             []Row      `json:"rows"`
	ByClassification []ClassRow `json:"byClassification"`
}

// Headline is the scorecard's summary numbers. It leaves out backfilled
// runs and runs the operator had already started on, because neither is a
// fair test of an unattended verdict reached first.
type Headline struct {
	Started            int     `json:"started"`
	Graded             int     `json:"graded"`
	Confirmed          float64 `json:"confirmed"`          // 0..1 of graded
	ConfirmedOrPartial float64 `json:"confirmedOrPartial"` // 0..1 of graded
	MedianElapsedSec   float64 `json:"medianElapsedSec"`
	ReadyBefore        int     `json:"readyBefore"` // graded rows with leadMinutes > 0
	ReadyOf            int     `json:"readyOf"`     // graded rows with a leadMinutes value
	CostUSD            float64 `json:"costUSD"`
	MedianCostUSD      float64 `json:"medianCostUSD"`
}

// Row is one ticket on the scorecard.
type Row struct {
	Key                 string      `json:"key"`
	Title               string      `json:"title"`
	RunID               string      `json:"runId"`
	StartedAt           string      `json:"startedAt"`
	Status              string      `json:"status"` // the latest attempt's finished status, "running" before it
	Classification      string      `json:"classification"`
	Hypothesis          string      `json:"hypothesis"` // first sentence, at most 160 runes
	Grade               string      `json:"grade"`      // "" while ungraded
	GradeReason         string      `json:"gradeReason"`
	Basis               string      `json:"basis"`
	Final               bool        `json:"final"`
	CostUSD             float64     `json:"costUSD"` // all attempts' run cost + every graded line's judge cost
	ElapsedSec          float64     `json:"elapsedSec"`
	Tags                []string    `json:"tags"` // provisional, operator first, backfill, no answer, ungradable
	Outcome             *RowOutcome `json:"outcome,omitempty"`
	Reproduction        string      `json:"reproduction"`
	Denied              []Denied    `json:"denied"`
	Questions           []string    `json:"questions"`
	PRFilesHit          *Fraction   `json:"prFilesHit,omitempty"`
	CodeRefsPathOverlap *Fraction   `json:"codeRefsPathOverlap,omitempty"`
	LeadMinutes         *float64    `json:"leadMinutes,omitempty"`
	Warning             string      `json:"warning,omitempty"`
}

// RowOutcome is what the operator's side of a ticket came to, merged across
// every outcome line collected for it.
type RowOutcome struct {
	Sources    []string `json:"sources"`
	PRURLs     []string `json:"prUrls"`
	Resolution string   `json:"resolution"`
	Status     string   `json:"status"`
}

// ClassRow is the grade tally for one shadow classification, the unit a
// later rung can be switched on by.
type ClassRow struct {
	Classification string `json:"classification"`
	Graded         int    `json:"graded"`
	Confirmed      int    `json:"confirmed"`
	Partial        int    `json:"partial"`
	Wrong          int    `json:"wrong"`
}

// maxRowHypothesisRunes keeps a scorecard row to one line of the table.
const maxRowHypothesisRunes = 160

// gradedGrades are the grades that count toward the headline's
// denominator. ungradable is left out: it says the operator's outcome gave
// nothing to compare with, not that shadow was right or wrong.
var gradedGrades = map[string]bool{"confirmed": true, "partial": true, "wrong": true, "no-answer": true}

// Report builds the scorecard over keys whose first shadow run started at
// or after since.
func Report(entries []Entry, since time.Time) ShadowReport {
	out := ShadowReport{Since: since.Format(time.RFC3339), Rows: []Row{}, ByClassification: []ClassRow{}}

	type keyed struct {
		row   Row
		first time.Time
		t     *Ticket
	}
	var rows []keyed
	for _, t := range Fold(entries) {
		if len(t.Attempts) == 0 || t.Attempts[0].Started.At.Before(since) {
			continue
		}
		rows = append(rows, keyed{row: rowOf(t), first: t.Attempts[0].Started.At, t: t})
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].first.Equal(rows[j].first) {
			return rows[i].first.After(rows[j].first)
		}
		return rows[i].row.Key < rows[j].row.Key
	})

	h := &out.Headline
	var elapsed, costs []float64
	confirmed, partial := 0, 0
	classes := map[string]*ClassRow{}
	for _, k := range rows {
		out.Rows = append(out.Rows, k.row)
		r := k.row
		if hasTag(r.Tags, "backfill") || hasTag(r.Tags, "operator first") {
			continue
		}
		h.Started++
		h.CostUSD += r.CostUSD
		costs = append(costs, r.CostUSD)
		if r.Status == "completed" {
			elapsed = append(elapsed, r.ElapsedSec)
		}
		if !r.Final || !gradedGrades[r.Grade] {
			continue
		}
		h.Graded++
		if r.LeadMinutes != nil {
			h.ReadyOf++
			if *r.LeadMinutes > 0 {
				h.ReadyBefore++
			}
		}
		class := r.Classification
		if class == "" {
			class = "unknown"
		}
		c := classes[class]
		if c == nil {
			c = &ClassRow{Classification: class}
			classes[class] = c
		}
		c.Graded++
		switch r.Grade {
		case "confirmed":
			confirmed++
			c.Confirmed++
		case "partial":
			partial++
			c.Partial++
		case "wrong":
			c.Wrong++
		}
	}
	if h.Graded > 0 {
		h.Confirmed = float64(confirmed) / float64(h.Graded)
		h.ConfirmedOrPartial = float64(confirmed+partial) / float64(h.Graded)
	}
	h.MedianElapsedSec = median(elapsed)
	h.MedianCostUSD = median(costs)

	for _, c := range classes {
		out.ByClassification = append(out.ByClassification, *c)
	}
	sort.Slice(out.ByClassification, func(i, j int) bool {
		a, b := out.ByClassification[i], out.ByClassification[j]
		if a.Graded != b.Graded {
			return a.Graded > b.Graded
		}
		return a.Classification < b.Classification
	})
	return out
}

// rowOf describes a ticket by its latest attempt, which is the run any
// grade is about, and charges it for every attempt and every judge call.
func rowOf(t *Ticket) Row {
	latest := t.Latest()
	r := Row{
		Key:       t.Key,
		Title:     t.Title,
		RunID:     latest.Started.RunID,
		StartedAt: t.Attempts[0].Started.At.Format(time.RFC3339),
		Status:    "running",
		Tags:      []string{},
		Denied:    []Denied{},
		Questions: []string{},
	}
	operatorFirst := false
	for _, a := range t.Attempts {
		if a.Started.OperatorFirst {
			operatorFirst = true
		}
		if a.Finished != nil {
			r.CostUSD += a.Finished.CostUSD
		}
	}
	// Every graded line spent a judge call, superseded or not, so all of
	// them are in the cost even though only the newest is the grade.
	for _, g := range t.grades {
		r.CostUSD += g.CostUSD
	}
	if f := latest.Finished; f != nil {
		r.Status = f.Status
		r.ElapsedSec = f.ElapsedSec
		r.Reproduction = f.Reproduction
		if f.Verdict != nil {
			r.Classification = f.Verdict.Classification
			r.Hypothesis = firstSentence(f.Verdict.Hypothesis)
		}
		if f.Denied != nil {
			r.Denied = f.Denied
		}
		if f.Questions != nil {
			r.Questions = f.Questions
		}
	}
	if g := t.Grade; g != nil {
		r.Grade = g.Grade
		r.GradeReason = g.Reason
		r.Basis = g.Basis
		r.Final = g.Final
		r.PRFilesHit = g.PRFilesHit
		r.CodeRefsPathOverlap = g.CodeRefsPathOverlap
		r.LeadMinutes = g.LeadMinutes
		r.Warning = g.Warning
	}
	if r.Grade != "" && !r.Final {
		r.Tags = append(r.Tags, "provisional")
	}
	if operatorFirst {
		r.Tags = append(r.Tags, "operator first")
	}
	if t.Backfill {
		r.Tags = append(r.Tags, "backfill")
	}
	switch r.Grade {
	case "no-answer":
		r.Tags = append(r.Tags, "no answer")
	case "ungradable":
		r.Tags = append(r.Tags, "ungradable")
	}
	if len(t.Outcomes) > 0 {
		r.Outcome = outcomeOf(t.Outcomes)
	}
	return r
}

// outcomeOf merges a ticket's outcome lines: every source and PR seen, and
// the newest non-empty resolution and status.
func outcomeOf(lines []Entry) *RowOutcome {
	o := &RowOutcome{Sources: []string{}, PRURLs: []string{}}
	seen := map[string]bool{}
	for _, e := range lines {
		if e.Source != "" && !seen["s:"+e.Source] {
			seen["s:"+e.Source] = true
			o.Sources = append(o.Sources, e.Source)
		}
		for _, u := range e.PRURLs {
			if !seen["u:"+u] {
				seen["u:"+u] = true
				o.PRURLs = append(o.PRURLs, u)
			}
		}
		if e.Resolution != "" {
			o.Resolution = e.Resolution
		}
		if e.Status != "" {
			o.Status = e.Status
		}
	}
	return o
}

// firstSentence is the hypothesis up to its first sentence end: a full
// stop, question or exclamation mark followed by a space or the end, or a
// line break. A stop inside a number or a path ("2.5", "app.cs") does not
// end it.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	end := len(r)
	for i, c := range r {
		if c == '\n' || c == '\r' {
			end = i
			break
		}
		if (c == '.' || c == '!' || c == '?') && (i+1 == len(r) || unicode.IsSpace(r[i+1])) {
			end = i + 1
			break
		}
	}
	return cutRunes(strings.TrimSpace(string(r[:end])), maxRowHypothesisRunes)
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

// median is the middle value, or the mean of the middle two for an even
// count, and 0 for none.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
