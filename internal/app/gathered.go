package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/srivathsanvenkateswaran/sirdar/internal/gather"
)

// Gathered lists what a run gathered before it started working, read from
// bundle/context/index.json. A run whose bundle predates the gatherer, or
// that gathered nothing, gets an empty list rather than an error.
func (s *Service) Gathered(wsID, runID string) ([]GatheredInfo, error) {
	rn, _, err := s.openRun(wsID, runID)
	if err != nil {
		return nil, err
	}
	out := gatheredInfosOf(rn.Dir)
	if out == nil {
		out = []GatheredInfo{}
	}
	return out, nil
}

// gatheredInfosOf reads a run directory's gather manifest and converts it
// to the wire shape, dropping every row gather marked denied: a door that
// stayed shut has nothing for a human reading gathered context to open. A
// run with no manifest at all, or one that cannot be read, answers with
// nil rather than an error — a run that predates the gatherer, or whose
// bundle was never written, is not a broken run.
func gatheredInfosOf(runDir string) []GatheredInfo {
	entries, err := gather.ReadIndex(filepath.Join(runDir, "bundle", "context"))
	if err != nil {
		return nil
	}
	var out []GatheredInfo
	for _, e := range entries {
		if e.Status == gather.StatusDenied {
			continue
		}
		out = append(out, GatheredInfo{
			Name:      e.Name,
			File:      e.File,
			Kind:      e.Kind,
			Status:    e.Status,
			Reason:    e.Reason,
			Call:      e.Call,
			Summary:   e.Summary,
			Diff:      e.Diff,
			Bytes:     e.Bytes,
			DiffBytes: e.DiffBytes,
			TookMs:    e.TookMs,
		})
	}
	return out
}

// reproductionDoc is the one key of result.json this reads: the triage
// note's reproduction outcome. A result.json with no reproduction key (an
// rca document, or one from before this field existed) decodes to a nil
// Reproduction, the same as an explicit null.
type reproductionDoc struct {
	Reproduction *struct {
		Outcome string `json:"outcome"`
		Where   string `json:"where"`
	} `json:"reproduction"`
}

// reproductionOf reads a run's result.json for its reproduction outcome. A
// missing file, a file that will not parse, a null reproduction and an rca
// document (no reproduction key) all answer nil: none of them is an error,
// just a run with nothing to report here.
func reproductionOf(runDir string) *ReproductionInfo {
	raw, err := os.ReadFile(filepath.Join(runDir, "result.json"))
	if err != nil {
		return nil
	}
	var doc reproductionDoc
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Reproduction == nil {
		return nil
	}
	return &ReproductionInfo{Outcome: doc.Reproduction.Outcome, Where: doc.Reproduction.Where}
}
