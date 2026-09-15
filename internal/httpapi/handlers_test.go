package httpapi

import (
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
)

func TestStartFixPassesEveryFlag(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", "/api/workspaces/"+knownWS+"/fix",
		`{"key":"OMNI-2510","dryRun":false,"noPr":true,"base":"release/9","acceptDeviation":true,"provider":"codex","model":"gpt-5"}`)

	var body struct {
		JobID string `json:"jobId"`
	}
	decodeJSON(t, w, 202, &body)
	if body.JobID != knownJob {
		t.Fatalf("jobId %q", body.JobID)
	}
	if f.gotFixKey != "OMNI-2510" {
		t.Fatalf("key %q", f.gotFixKey)
	}
	want := FixOptions{NoPR: true, Base: "release/9", AcceptDeviation: true, Provider: "codex", Model: "gpt-5"}
	if f.gotFix != want {
		t.Fatalf("options %+v, want %+v", f.gotFix, want)
	}
}

func TestStartFixRejectsBadBodies(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"no key", `{"dryRun":true}`},
		{"unknown provider", `{"key":"OMNI-2510","provider":"gemini"}`},
		{"unknown field", `{"key":"OMNI-2510","accept":true}`},
		{"no body", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertError(t, do(t, newFake(), "POST", "/api/workspaces/"+knownWS+"/fix", tc.body), 400, "bad_request")
		})
	}
}

func TestStartFixUnknownWorkspace(t *testing.T) {
	w := do(t, newFake(), "POST", "/api/workspaces/nope/fix", `{"key":"OMNI-2510"}`)
	assertError(t, w, 404, "not_found")
}

func TestStartEvalWithNoBodyMeansTheWholeSet(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", "/api/workspaces/"+knownWS+"/eval", "")

	decodeJSON(t, w, 202, nil)
	if len(f.gotEvalKeys) != 0 {
		t.Fatalf("keys %v, want none", f.gotEvalKeys)
	}
	if (f.gotEval != EvalOptions{}) {
		t.Fatalf("options %+v", f.gotEval)
	}
}

func TestStartEvalPassesKeysAndOverrides(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", "/api/workspaces/"+knownWS+"/eval",
		`{"keys":["OMNI-1","OMNI-2"],"provider":"openai","model":"qwen3-coder","concurrency":2}`)

	decodeJSON(t, w, 202, nil)
	if strings.Join(f.gotEvalKeys, ",") != "OMNI-1,OMNI-2" {
		t.Fatalf("keys %v", f.gotEvalKeys)
	}
	want := EvalOptions{Provider: "openai", Model: "qwen3-coder", Concurrency: 2}
	if f.gotEval != want {
		t.Fatalf("options %+v, want %+v", f.gotEval, want)
	}
}

func TestStartEvalRejectsBadBodies(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"unknown provider", `{"provider":"gemini"}`},
		{"negative concurrency", `{"concurrency":-1}`},
		{"golden directory is not the caller's", `{"goldenDir":"/tmp/anything"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertError(t, do(t, newFake(), "POST", "/api/workspaces/"+knownWS+"/eval", tc.body), 400, "bad_request")
		})
	}
}

func TestEvalReportsAreCamelCase(t *testing.T) {
	f := newFake()
	f.reports = []EvalReport{{
		Path: "/repos/oxo-apis/.sirdar/eval/20260911T090000Z.json",
		Report: eval.Report{
			At: time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC), Provider: "claude", Model: "sonnet",
			GoldenDir: "/golden",
			Results: []eval.Result{{
				Key: "OMNI-2510", RunID: knownRun, State: "completed", Turns: 6, CostUSD: 0.4,
				SchemaValid: true, Passed: 2, Total: 3,
				Checks: []eval.Check{{Key: "classification", Kind: eval.KindEquals, Pass: false, Detail: "got bug"}},
			}},
		},
	}}
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/eval", "")

	var got []map[string]any
	decodeJSON(t, w, 200, &got)
	if len(got) != 1 {
		t.Fatalf("reports %v", got)
	}
	for _, k := range []string{"path", "at", "provider", "model", "goldenDir", "results"} {
		if _, ok := got[0][k]; !ok {
			t.Errorf("report has no %q: %v", k, got[0])
		}
	}
	results, _ := got[0]["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("results %v", got[0]["results"])
	}
	row, _ := results[0].(map[string]any)
	for _, k := range []string{"key", "runId", "state", "schemaValid", "passed", "total", "checks"} {
		if _, ok := row[k]; !ok {
			t.Errorf("result has no %q: %v", k, row)
		}
	}
}

func TestGoldenList(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/golden", "")

	var got []map[string]any
	decodeJSON(t, w, 200, &got)
	if len(got) != 1 {
		t.Fatalf("entries %v", got)
	}
	for _, k := range []string{"key", "dir", "bundleDir", "assertions", "hasExpectedNote"} {
		if _, ok := got[0][k]; !ok {
			t.Errorf("entry has no %q: %v", k, got[0])
		}
	}
}

func TestAddGoldenFromARunID(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", "/api/workspaces/"+knownWS+"/golden", `{"runId":"`+knownRun+`"}`)

	decodeJSON(t, w, 200, nil)
	if f.gotGolden.RunID != knownRun || f.gotGolden.Key != "" {
		t.Fatalf("asked for %+v", f.gotGolden)
	}
}

func TestAddGoldenNeedsSomethingToCopy(t *testing.T) {
	assertError(t, do(t, newFake(), "POST", "/api/workspaces/"+knownWS+"/golden", `{}`), 400, "bad_request")
}

func TestConfigSummaryRoute(t *testing.T) {
	f := newFake()
	f.summary = ConfigSummary{
		Notify: NotifySummary{
			Enabled: true, On: []string{"completed"},
			Destinations: []NotifyDestination{{Type: "slack", Credential: "env"}},
		},
		Webhooks: WebhooksSummary{
			Enabled: true, Cooldown: "10m0s",
			Sources: []WebhookSourceSummary{{Name: "jira", Auth: "secret", Credential: "keychain"}},
		},
	}
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/config/summary", "")

	var got map[string]any
	decodeJSON(t, w, 200, &got)
	if _, ok := got["notify"]; !ok {
		t.Fatalf("no notify block: %v", got)
	}
	if _, ok := got["webhooks"]; !ok {
		t.Fatalf("no webhooks block: %v", got)
	}
	// Whatever the summary carries, it is the scheme and not the ref.
	if strings.Contains(w.Body.String(), "env:") || strings.Contains(w.Body.String(), "keychain:") {
		t.Fatalf("the route answered with a credential reference: %s", w.Body.String())
	}
}

func TestConfigSummaryUnknownWorkspace(t *testing.T) {
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/nope/config/summary", ""), 404, "not_found")
}

// The one-off provider override reaches triage and RCA too, and a provider
// Sirdar does not drive is refused before a job is started.
func TestTriageAndRCACarryProviderAndModel(t *testing.T) {
	f := newFake()
	decodeJSON(t, do(t, f, "POST", "/api/workspaces/"+knownWS+"/triage",
		`{"keys":["OMNI-1"],"provider":"qwen","model":"qwen3-coder"}`), 202, nil)
	if f.gotTriage.Provider != "qwen" || f.gotTriage.Model != "qwen3-coder" {
		t.Fatalf("triage options %+v", f.gotTriage)
	}

	decodeJSON(t, do(t, f, "POST", "/api/workspaces/"+knownWS+"/rca",
		`{"key":"OMNI-1","provider":"acp","model":"gemini-2.5"}`), 202, nil)
	if f.gotRCA.Provider != "acp" || f.gotRCA.Model != "gemini-2.5" {
		t.Fatalf("rca options %+v", f.gotRCA)
	}

	assertError(t, do(t, newFake(), "POST", "/api/workspaces/"+knownWS+"/triage",
		`{"keys":["OMNI-1"],"provider":"gemini"}`), 400, "bad_request")
	assertError(t, do(t, newFake(), "POST", "/api/workspaces/"+knownWS+"/rca",
		`{"key":"OMNI-1","provider":"gemini"}`), 400, "bad_request")
}
