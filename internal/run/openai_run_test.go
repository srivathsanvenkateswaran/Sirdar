package run

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider/openai"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// configOpenAI is a workspace that drives runs with Sirdar's own loop
// against an OpenAI-compatible endpoint. baseUrl is filled in with the
// fake server's address.
const configOpenAI = `workspace: test
provider: openai
billing: subscription
openai:
  baseUrl: %s
  apiKey: env:FAKE_MODEL_KEY
  model: qwen/qwen3-coder
  maxContextTokens: 8000
  price:
    inputPerMTok: 0.2
    outputPerMTok: 0.8
notes:
  dir: notes
budget:
  maxTurns: 60
  maxMinutes: 25
  maxUsd: 5
concurrency: 1
permissions:
  bash:
    - "git log*"
playbooks: .sirdar/playbooks
`

// providerFromConfig builds the openai provider the way cmd/sirdar's
// wiring does, so this test exercises the config keys and not just the
// package's own defaults.
func providerFromConfig(t *testing.T, cfg *config.Config, creds config.Resolver) *openai.Provider {
	t.Helper()
	o := cfg.OpenAI
	if o == nil {
		t.Fatal("the workspace has no openai block")
	}
	key, err := creds.Resolve(o.APIKey)
	if err != nil {
		t.Fatalf("resolve %s: %v", o.APIKey, err)
	}
	loop := openai.LoopConfig{
		Chat:             openai.Config{BaseURL: o.BaseURL, APIKey: key, Model: o.Model},
		MaxContextTokens: o.MaxContextTokens,
		MCPWorkspaceOnly: true,
	}
	if o.Price != nil {
		loop.PriceInputPerMTok = o.Price.InputPerMTok
		loop.PriceOutputPerMTok = o.Price.OutputPerMTok
	}
	p, ok := openai.NewProvider(loop).(*openai.Provider)
	if !ok {
		t.Fatal("NewProvider did not return *openai.Provider")
	}
	return p
}

// TestTriageThroughTheOpenAIProvider runs a whole triage on the openai
// provider: config, prompt, the loop's read_file call inside the
// workspace, the submitted note, and the filed markdown.
func TestTriageThroughTheOpenAIProvider(t *testing.T) {
	var authorization string
	var turns int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		turns++
		w.Header().Set("Content-Type", "application/json")
		if turns == 1 {
			// The run's prompt has to reach the model, playbook and all.
			joined := ""
			for _, m := range req.Messages {
				joined += m.Content + "\n"
			}
			if !strings.Contains(joined, "OMNI-1") || !strings.Contains(joined, playbookMarker) {
				t.Errorf("the first request did not carry the run's prompt:\n%s", joined)
			}
			_, _ = w.Write([]byte(chatToolCall("t1", "read_file", `{"path":"README.md"}`)))
			return
		}
		_, _ = w.Write([]byte(chatToolCall("t2", "submit_note", triageDoc)))
	}))
	defer srv.Close()

	body := strings.Replace(configOpenAI, "%s", srv.URL, 1)
	cfg := newWorkspaceWith(t, body)
	if err := os.WriteFile(filepath.Join(cfg.Root, "README.md"), []byte("# the workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	creds := config.Resolver{Env: func(name string) (string, bool) {
		return "model-key-1", name == "FAKE_MODEL_KEY"
	}}
	p := providerFromConfig(t, cfg, creds)

	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.Creds = creds
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if authorization != "Bearer model-key-1" {
		t.Errorf("Authorization = %q, want the resolved key", authorization)
	}
	if turns != 2 {
		t.Errorf("the loop took %d turns, want 2", turns)
	}

	note := readFile(t, filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md"))
	if !strings.Contains(note, `tracker_key: "OMNI-1"`) {
		t.Fatalf("note frontmatter:\n%s", note)
	}
	// Cost came from the workspace's price table, not from a provider CLI.
	if out.State.Usage.CostUSD <= 0 {
		t.Errorf("usage = %+v, want a cost from the price table", out.State.Usage)
	}
	events := readFile(t, filepath.Join(runDir(t, cfg, out), "events.jsonl"))
	if !strings.Contains(events, `"read_file"`) || !strings.Contains(events, `"final"`) {
		t.Errorf("events.jsonl does not record the session:\n%s", events)
	}
}

// TestOpenAIKeyIsKeptFromTheAgentsEnvironment covers the other half of
// resolving the key here: the session's shell commands and MCP servers
// must not be able to read it back out of the environment.
func TestOpenAIKeyIsKeptFromTheAgentsEnvironment(t *testing.T) {
	cfg := newWorkspaceWith(t, strings.Replace(configOpenAI, "%s", "https://api.example/v1", 1))
	d := Deps{Config: cfg, Env: []string{"PATH=/usr/bin", "FAKE_MODEL_KEY=model-key-1"}}
	for _, entry := range d.childEnv() {
		if strings.HasPrefix(entry, "FAKE_MODEL_KEY=") {
			t.Fatalf("the model API key reached the agent's environment: %q", entry)
		}
	}
}

// chatToolCall is one Chat Completions response asking for one tool call.
func chatToolCall(id, name, args string) string {
	b, err := json.Marshal(map[string]any{
		"model": "qwen/qwen3-coder",
		"choices": []map[string]any{{
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []map[string]any{{
					"id":       id,
					"type":     "function",
					"function": map[string]string{"name": name, "arguments": args},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]int{"prompt_tokens": 1200, "completion_tokens": 80},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestOpenAISchemaRetryHappensOnTheSameSession is the interaction the
// loop's end-of-session barrier exists for: the runner sends the retry
// while it is still draining the events of the note that failed
// validation, and the provider has no resume handle to fall back on.
func TestOpenAISchemaRetryHappensOnTheSameSession(t *testing.T) {
	var turns int
	var retryText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		turns++
		w.Header().Set("Content-Type", "application/json")
		if turns == 1 {
			// A note that will not validate: the ticket block is missing.
			_, _ = w.Write([]byte(chatToolCall("t1", "submit_note", `{"title":"half a note"}`)))
			return
		}
		retryText = req.Messages[len(req.Messages)-1].Content
		_, _ = w.Write([]byte(chatToolCall("t2", "submit_note", triageDoc)))
	}))
	defer srv.Close()

	cfg := newWorkspaceWith(t, strings.Replace(configOpenAI, "%s", srv.URL, 1))
	creds := config.Resolver{Env: func(string) (string, bool) { return "model-key-1", true }}
	r := newRunner(cfg, providerFromConfig(t, cfg, creds), stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
	if turns != 2 {
		t.Fatalf("the endpoint saw %d turns, want the first note and one retry", turns)
	}
	if !strings.Contains(retryText, "did not match the schema") {
		t.Errorf("the retry message the model saw = %q", retryText)
	}
	if got := len(outs[0].State.Notes); got == 0 {
		t.Error("the run completed without filing a note")
	}
}
