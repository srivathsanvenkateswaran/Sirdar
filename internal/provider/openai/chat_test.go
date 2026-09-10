package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, cfg Config, srv *httptest.Server) *Client {
	t.Helper()
	cfg.BaseURL = srv.URL
	return New(cfg, srv.Client())
}

func decodeWireRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode request body: %v (body=%s)", err, body)
	}
	return m
}

// --- response shape ---

func TestChatToolCallResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "test-model",
			"choices": [{
				"finish_reason": "tool_calls",
				"message": {
					"role": "assistant",
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {"name": "read_file", "arguments": "{\"path\": \"a.go\", \"limit\": 10}"}
					}]
				}
			}],
			"usage": {"prompt_tokens": 42, "completion_tokens": 7}
		}`))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{Model: "test-model"}, srv)
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", resp.FinishReason)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Type != "function" || tc.Function.Name != "read_file" {
		t.Errorf("tool call mismatch: %+v", tc)
	}
	// Arguments must be kept verbatim as the raw string, not re-encoded.
	wantArgs := `{"path": "a.go", "limit": 10}`
	if tc.Function.Arguments != wantArgs {
		t.Errorf("Arguments = %q, want %q", tc.Function.Arguments, wantArgs)
	}
	if resp.Usage.PromptTokens != 42 || resp.Usage.CompletionTokens != 7 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.Model != "test-model" {
		t.Errorf("Model = %q", resp.Model)
	}
}

func TestChatFinalTextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "test-model",
			"choices": [{
				"finish_reason": "stop",
				"message": {"role": "assistant", "content": "final answer"}
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 3}
		}`))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{Model: "test-model"}, srv)
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.FinishReason != "stop" || resp.Message.Content != "final answer" {
		t.Errorf("got %+v", resp)
	}
}

// --- retry behaviour ---

func TestChatRetriesOnceOn429ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{Model: "m"}, srv)
	start := time.Now()
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("retry took too long: %v", elapsed)
	}
	if resp.Message.Content != "ok" {
		t.Errorf("content = %q", resp.Message.Content)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

func TestChat500WithBodySnippetAndNoKeyLeaked(t *testing.T) {
	const secretKey = "sk-super-secret-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secretKey {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error: something broke"))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{Model: "m", APIKey: secretKey}, srv)
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "openai: POST /chat/completions: 500:") {
		t.Errorf("error missing status prefix: %q", msg)
	}
	if !strings.Contains(msg, "internal server error: something broke") {
		t.Errorf("error missing body snippet: %q", msg)
	}
	if strings.Contains(msg, secretKey) {
		t.Errorf("error leaks API key: %q", msg)
	}
}

func TestChat500BodySnippetCappedAt200Bytes(t *testing.T) {
	long := strings.Repeat("x", 500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(long))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{Model: "m"}, srv)
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	// Two retries of 500, each 500 chars; the error must carry only the
	// last response's first 200 bytes, not the full body.
	if strings.Count(err.Error(), "x") != 200 {
		t.Errorf("expected exactly 200 bytes of body snippet, got %d in %q", strings.Count(err.Error(), "x"), err.Error())
	}
}

// --- headers ---

func TestChatHeaders(t *testing.T) {
	var gotAuth, gotExtra, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotExtra = r.Header.Get("HTTP-Referer")
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{
		Model:        "m",
		APIKey:       "sk-abc123",
		ExtraHeaders: map[string]string{"HTTP-Referer": "https://example.com"},
	}, srv)
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotAuth != "Bearer sk-abc123" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotExtra != "https://example.com" {
		t.Errorf("HTTP-Referer = %q", gotExtra)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
}

func TestChatNoAuthorizationHeaderWhenKeyEmpty(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuth = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{Model: "m"}, srv)
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if sawAuth {
		t.Error("Authorization header present with empty APIKey")
	}
}

// --- request body shape: tools, tool_choice, response_format, stream ---

func TestChatRequestBodyShape(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeWireRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{Model: "m"}, srv)
	_, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: []ToolSpec{{
			Name:        "read_file",
			Description: "reads a file",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
		ToolChoice:     "required",
		ResponseFormat: json.RawMessage(`{"type":"json_object"}`),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if streamVal, ok := got["stream"].(bool); !ok || streamVal {
		t.Errorf("stream = %v, want false", got["stream"])
	}
	if got["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v", got["tool_choice"])
	}
	rf, _ := got["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("response_format = %v", got["response_format"])
	}
	tools, ok := got["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", got["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "read_file" || fn["description"] != "reads a file" {
		t.Errorf("function = %v", fn)
	}
	params, _ := fn["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Errorf("parameters = %v", fn["parameters"])
	}
}

func TestChatOmitsToolChoiceAndResponseFormatWhenUnset(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeWireRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{Model: "m"}, srv)
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, present := got["tool_choice"]; present {
		t.Errorf("tool_choice present when unset: %v", got["tool_choice"])
	}
	if _, present := got["response_format"]; present {
		t.Errorf("response_format present when unset: %v", got["response_format"])
	}
	if _, present := got["tools"]; present {
		t.Errorf("tools present when none given: %v", got["tools"])
	}
}

// --- Ping ---

func TestPingSuccess(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodGet {
			t.Errorf("Ping used method %s, want GET", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{Model: "m"}, srv)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if gotPath != "/models" {
		t.Errorf("path = %q, want /models", gotPath)
	}
}

func TestPingFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{Model: "m"}, srv)
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "openai: GET /models: 401:") {
		t.Errorf("error = %q", err)
	}
}

// --- base URL trailing slash ---

func TestBaseURLWithTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	cfg := Config{Model: "m", BaseURL: srv.URL + "/"}
	c := New(cfg, srv.Client())
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions (no double slash)", gotPath)
	}
}

// --- retry delay parsing ---

func TestRetryDelay(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", defaultRetryDelay},
		{"not-a-value", defaultRetryDelay},
		{"5", 5 * time.Second},
		{"-1", 0},
		{"3600", maxRetryAfter},
	}
	for _, c := range cases {
		if got := retryDelay(c.in); got != c.want {
			t.Errorf("retryDelay(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	d := retryDelay(future)
	if d <= 0 || d > 4*time.Second {
		t.Errorf("retryDelay(HTTP-date +3s) = %v, want ~3s", d)
	}
}

// TestExtraHeadersCannotOverrideAuthOrContentType covers a config that
// names one of the two headers the client owns: an Authorization entry
// would silently replace the configured key, and a Content-Type one would
// make the body unreadable to the server.
func TestExtraHeadersCannotOverrideAuthOrContentType(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{
		APIKey: "sk-real",
		Model:  "m",
		ExtraHeaders: map[string]string{
			"Authorization": "Bearer sk-from-headers",
			"content-type":  "text/plain",
			"HTTP-Referer":  "https://example.com",
		},
	}, srv)
	if _, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got.Get("Authorization") != "Bearer sk-real" {
		t.Errorf("Authorization = %q, want the configured key", got.Get("Authorization"))
	}
	if got.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", got.Get("Content-Type"))
	}
	if got.Get("HTTP-Referer") != "https://example.com" {
		t.Errorf("HTTP-Referer = %q, want extra headers to still be applied", got.Get("HTTP-Referer"))
	}
}

// TestErrorSnippetScrubsTheKey covers a gateway that echoes the
// Authorization header it rejected: the error goes to the run log and the
// operator's terminal, and neither is a place for a credential.
func TestErrorSnippetScrubsTheKey(t *testing.T) {
	const secret = "sk-live-abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad key: Bearer `+secret+`"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient(t, Config{APIKey: secret, Model: "m"}, srv)
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("want an error for a 401")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the error carries the api key: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("error = %v, want the key replaced with a marker", err)
	}
}
