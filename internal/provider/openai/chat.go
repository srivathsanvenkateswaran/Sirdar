// Package openai implements an OpenAI-compatible Chat Completions client:
// aggregators (OpenRouter, Groq, Together, Fireworks), vendors (DeepSeek,
// Zhipu, Moonshot, DashScope, xAI) and self-hosted servers (Ollama, vLLM, LM
// Studio, llama.cpp) that speak the same non-streaming request/response
// shape. This file is the HTTP client only; internal/provider/openai/loop.go
// drives the agent loop on top of it.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxBodyBytes caps how much of a response body is read into memory. An
// OpenAI-compatible server that streams back something enormous (or loops)
// should not be able to exhaust process memory.
const maxBodyBytes = 8 << 20 // 8 MiB

// maxRetryAfter caps how long a single retry waits, regardless of what a
// server's Retry-After header asks for.
const maxRetryAfter = 30 * time.Second

// defaultRetryDelay is used when a 429/5xx response carries no usable
// Retry-After header.
const defaultRetryDelay = 2 * time.Second

// Config configures a Client.
type Config struct {
	BaseURL      string
	APIKey       string
	Model        string
	Temperature  *float64
	ExtraHeaders map[string]string
}

// Message is one Chat Completions message, covering every role Sirdar's
// loop sends and receives (system, user, assistant, tool).
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall is one function call an assistant message requested. Arguments
// is kept as the raw string the server sent; parsing (and reporting a
// malformed-JSON tool error back to the model) is the loop's job, not the
// client's.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolSpec describes one tool offered to the model. Parameters is a JSON
// Schema object, rendered on the wire as
// {"type":"function","function":{"name":...,"description":...,"parameters":...}}.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Usage is token accounting for one Chat Completions call.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Request is one Chat Completions call.
type Request struct {
	Messages []Message
	Tools    []ToolSpec
	// ToolChoice is "auto", "required", or "" (omitted, letting the server
	// default).
	ToolChoice string
	// ResponseFormat is passed through verbatim when non-empty.
	ResponseFormat json.RawMessage
}

// Response is the mapped choices[0] of a Chat Completions reply.
type Response struct {
	Message      Message
	FinishReason string
	Usage        Usage
	Model        string
}

// Client is an OpenAI-compatible Chat Completions client.
type Client struct {
	cfg Config
	hc  *http.Client
}

// New returns a Client. When hc is nil, a client with a generous timeout is
// used, since local/self-hosted models can be slow.
func New(cfg Config, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Client{cfg: cfg, hc: hc}
}

// --- wire types ---

type wireRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Tools          []wireTool      `json:"tools,omitempty"`
	ToolChoice     string          `json:"tool_choice,omitempty"`
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
	Temperature    *float64        `json:"temperature,omitempty"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
}

// Chat issues one (non-streaming) Chat Completions call and maps
// choices[0] to a Response. A 429 or 5xx response is retried once, after
// waiting on the first response's Retry-After header (capped at 30s) or 2s
// when absent/unusable.
func (c *Client) Chat(ctx context.Context, req Request) (Response, error) {
	wr := wireRequest{
		Model:       c.cfg.Model,
		Messages:    req.Messages,
		Stream:      false,
		Temperature: c.cfg.Temperature,
		ToolChoice:  req.ToolChoice,
	}
	if len(req.ResponseFormat) > 0 {
		wr.ResponseFormat = req.ResponseFormat
	}
	if len(req.Tools) > 0 {
		wr.Tools = make([]wireTool, len(req.Tools))
		for i, t := range req.Tools {
			wr.Tools[i] = wireTool{
				Type: "function",
				Function: wireFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			}
		}
	}

	body, err := json.Marshal(wr)
	if err != nil {
		return Response{}, fmt.Errorf("openai: POST /chat/completions: encode request: %w", err)
	}

	respBody, err := c.doWithRetry(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return Response{}, err
	}

	var wresp wireResponse
	if err := json.Unmarshal(respBody, &wresp); err != nil {
		return Response{}, fmt.Errorf("openai: POST /chat/completions: decode response: %w", err)
	}
	if len(wresp.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: POST /chat/completions: response had no choices")
	}
	choice := wresp.Choices[0]
	return Response{
		Message:      choice.Message,
		FinishReason: choice.FinishReason,
		Usage: Usage{
			PromptTokens:     wresp.Usage.PromptTokens,
			CompletionTokens: wresp.Usage.CompletionTokens,
		},
		Model: wresp.Model,
	}, nil
}

// Ping issues GET {baseUrl}/models and treats any 2xx response as success.
func (c *Client) Ping(ctx context.Context) error {
	body, status, _, err := c.doOnce(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return c.statusError(http.MethodGet, "/models", status, body)
	}
	return nil
}

// --- HTTP plumbing ---

// url joins the configured base URL and a path, tolerating a base URL with
// or without a trailing slash.
func (c *Client) url(path string) string {
	return strings.TrimSuffix(c.cfg.BaseURL, "/") + path
}

// newRequest builds one HTTP request with the standard headers: an
// Authorization bearer header (only when an API key is configured),
// caller-supplied ExtraHeaders, and a JSON content type when there is a
// body.
func (c *Client) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), r)
	if err != nil {
		return nil, err
	}
	// ExtraHeaders go on first, so the two headers the client owns cannot
	// be overwritten from config: an "Authorization" entry there would
	// otherwise silently replace the configured key, and a "Content-Type"
	// one would make the request unparseable to the server.
	for k, v := range c.cfg.ExtraHeaders {
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "Content-Type":
			continue
		}
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	return req, nil
}

// doOnce issues one request and returns the (capped) response body, status
// code and headers. Errors here never include the API key: they are built
// from the URL path and method, never the Authorization header.
func (c *Client) doOnce(ctx context.Context, method, path string, body []byte) ([]byte, int, http.Header, error) {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("openai: %s %s: %w", method, path, err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("openai: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("openai: %s %s: read body: %w", method, path, err)
	}
	return b, resp.StatusCode, resp.Header, nil
}

// doWithRetry issues a request, retrying once on 429 or 5xx per the
// Retry-After policy described on Chat.
func (c *Client) doWithRetry(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	respBody, status, header, err := c.doOnce(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if status >= 200 && status < 300 {
		return respBody, nil
	}
	if status != http.StatusTooManyRequests && status < 500 {
		return nil, c.statusError(method, path, status, respBody)
	}

	delay := retryDelay(header.Get("Retry-After"))
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(delay):
	}

	respBody, status, _, err = c.doOnce(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if status >= 200 && status < 300 {
		return respBody, nil
	}
	return nil, c.statusError(method, path, status, respBody)
}

// retryDelay parses a Retry-After header value, which may be a number of
// seconds or an HTTP-date. It is capped at 30s; an empty, negative or
// unparseable value falls back to 2s.
func retryDelay(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return defaultRetryDelay
	}
	if secs, err := strconv.Atoi(v); err == nil {
		d := time.Duration(secs) * time.Second
		if d < 0 {
			d = 0
		}
		if d > maxRetryAfter {
			d = maxRetryAfter
		}
		return d
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		if d > maxRetryAfter {
			d = maxRetryAfter
		}
		return d
	}
	return defaultRetryDelay
}

// statusError builds the standard "openai: METHOD PATH: STATUS: snippet"
// error. The snippet is capped at 200 bytes and comes only from the
// response body, never from request headers. Some gateways echo the
// Authorization header they rejected back in the body, so the key is
// scrubbed from the snippet as well: an error goes to the run log and to
// the operator's terminal, and neither is a place for a credential.
func (c *Client) statusError(method, path string, status int, body []byte) error {
	snippet := body
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	return fmt.Errorf("openai: %s %s: %d: %s", method, path, status, c.scrub(string(snippet)))
}

// scrub replaces every occurrence of the configured API key with a marker.
func (c *Client) scrub(s string) string {
	if c.cfg.APIKey == "" {
		return s
	}
	return strings.ReplaceAll(s, c.cfg.APIKey, "[redacted]")
}
