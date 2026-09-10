# `provider: openai`: Sirdar's own agent loop over OpenAI-compatible APIs (design)

Date: 2026-09-10. Branch `providers`. Roadmap: `docs/superpowers/plans/2026-09-10-provider-roadmap.md`, Phase 1.

## Goal

Let any OpenAI-compatible Chat Completions endpoint drive a Sirdar run: aggregators
(OpenRouter, Groq, Together, Fireworks), vendors (DeepSeek, Zhipu, Moonshot, DashScope, xAI) and
self-hosted servers (Ollama, vLLM, LM Studio, llama.cpp). Sirdar runs the loop, owns a
read-only tool set, and connects the workspace's MCP servers itself. Structured output comes
from a `submit_note` tool whose parameters are the note schema, so it works on servers without
`response_format`.

## Packages (all stdlib; no new dependencies)

```
internal/provider/openai/chat.go       Chat Completions client (non-streaming), retries, usage
internal/provider/openai/loop.go       provider.Provider + Session: the loop, budgets, events
internal/provider/openai/prompt.go     system message and tool-nudge texts
internal/agenttools/                   read-only tools confined to cwd + bash allow-list + web_fetch
internal/mcpclient/                    minimal MCP stdio client (initialize, tools/list, tools/call)
internal/config                        `openai:` block; provider name "openai"; env stripping of openai.apiKey
cmd/sirdar/wire.go, internal/app/wire.go (if present)  provider selection
```

## Config

```yaml
provider: openai
openai:
  baseUrl: https://openrouter.ai/api/v1      # or http://localhost:11434/v1
  apiKey: env:OPENROUTER_API_KEY              # optional for local servers; env:/keychain: ref
  model: qwen/qwen3-coder
  maxContextTokens: 128000                    # loop trims old tool results when prompt tokens approach this
  price: { inputPerMTok: 0.2, outputPerMTok: 0.8 }   # optional; cost 0 when absent
  temperature: 0                              # optional
  extraHeaders: { HTTP-Referer: https://github.com/srivathsanvenkateswaran/Sirdar }  # optional
```

Validation: `baseUrl` and `model` required when `provider: openai`; `apiKey` if set must be a
credential ref; `maxContextTokens` default 128000; `price` values ≥ 0.

## chat.go

```go
type Config struct{ BaseURL, APIKey, Model string; Temperature *float64; ExtraHeaders map[string]string }
type Message struct {
    Role       string     `json:"role"`                 // system|user|assistant|tool
    Content    string     `json:"content,omitempty"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
    Name       string     `json:"name,omitempty"`
}
type ToolCall struct{ ID string `json:"id"`; Type string `json:"type"`; Function struct{ Name string `json:"name"`; Arguments string `json:"arguments"` } `json:"function"` }
type ToolSpec struct{ Name, Description string; Parameters json.RawMessage }   // rendered as {"type":"function","function":{...}}
type Usage struct{ PromptTokens, CompletionTokens int }
type Request struct{ Messages []Message; Tools []ToolSpec; ToolChoice string /* "auto"|"required"|"" */; ResponseFormat json.RawMessage }
type Response struct{ Message Message; FinishReason string; Usage Usage; Model string }
func New(cfg Config, hc *http.Client) *Client
func (c *Client) Chat(ctx context.Context, req Request) (Response, error)
func (c *Client) Ping(ctx context.Context) error   // GET {baseUrl}/models
```

Behaviour: `POST {baseUrl}/chat/completions` with `Authorization: Bearer <key>` when a key is
set; `stream: false`; 429 and 5xx retried once after `Retry-After` (≤ 30 s) or 2 s; error body
(≤ 200 bytes, never the key) in the returned error; malformed `arguments` JSON surfaced to the
loop as a tool error message, not a crash. Body read capped at 8 MiB.

## internal/agenttools

```go
type Tool interface {
    Spec() Spec                                          // Name, Description, Parameters (JSON Schema)
    Call(ctx context.Context, args json.RawMessage) (string, error)
}
type Spec struct{ Name, Description string; Parameters json.RawMessage }
type Options struct{ Root string; BashAllow []string; HTTP *http.Client; MaxOutputBytes int /* default 64 KiB */ }
func ReadOnlySet(o Options) []Tool
```

Tools: `read_file{path, offset?, limit?}` (line-numbered like Claude's Read), `list_dir{path}`,
`grep{pattern, path?, glob?, maxResults?}` (uses `rg` when on PATH with `--json` off and `-n`,
else a Go regexp walk skipping `.git`, `node_modules`, binaries), `glob{pattern}`,
`bash{command}` (allowed only when `provider.MatchGlob` matches a `BashAllow` pattern; runs
`sh -c` in Root with a 60 s timeout and a 64 KiB cap; denied → error text starting
"denied:"), `web_fetch{url}` (GET only, http/https, 1 MiB cap, `text/*` or JSON only, 20 s).
Every path argument is resolved with `filepath.Abs` + `filepath.EvalSymlinks` and must stay
under `Root` (after EvalSymlinks of Root too); otherwise error "path escapes workspace".
Outputs beyond the cap are truncated with a trailing `[truncated N bytes]`. Tool names and
descriptions are stable strings the policy can match: `read_file`, `list_dir`, `grep`, `glob`,
`bash`, `web_fetch`.

## internal/mcpclient

```go
type ServerConfig struct{ Name, Command string; Args []string; Env map[string]string }
func LoadWorkspaceServers(root string) ([]ServerConfig, []string /* warnings */, error)
// reads <root>/.mcp.json {"mcpServers":{"name":{"type":"stdio","command":"...","args":[...],"env":{...}}}};
// non-stdio entries (http/sse) are skipped with a warning; ${VAR} in env values expands from the process env.
type Client struct{ ... }
func Start(ctx context.Context, cfg ServerConfig, stderr io.Writer) (*Client, error)  // spawn, initialize, notifications/initialized
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error)                  // ToolInfo{Name, Description string; InputSchema json.RawMessage}
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool /*isError*/, error)
func (c *Client) Close() error                                                        // close stdin, wait 5 s, kill process group
```

Protocol: JSON-RPC 2.0 over stdio, one object per line (also accept Content-Length framing if
the first bytes look like a header). `initialize` params `{"protocolVersion":"2025-06-18",
"capabilities":{},"clientInfo":{"name":"sirdar","version":"0.1.0-dev"}}`; then
`notifications/initialized`. `tools/call` result: concatenate `content[]` items of type `text`
with newlines; for `image`/`resource` items append `[image <mimeType>]` / the resource text;
`isError` propagated. Requests time out after 120 s. Names are exposed to the model as
`mcp__<server>__<tool>` so `provider.PermissionPolicy`'s `mcp__` allow rule applies.

## loop.go

`New() provider.Provider` with `Name() == "openai"`. `Start(ctx, spec)`:

1. Build tools: `agenttools.ReadOnlySet{Root: spec.Cwd, BashAllow: spec.Policy.BashAllow}` +
   every tool from `mcpclient.LoadWorkspaceServers(spec.Cwd)` (each server started lazily on
   first call, kept for the session) + `submit_note` whose `Parameters` is `spec.OutputSchema`.
   MCP load or start failures become `EvSystem` events with the warning text, never fatal.
2. Messages: system = `prompt.System()` (short: you are running inside Sirdar; tools are read
   only; finish by calling `submit_note` exactly once with the JSON note; do not answer in
   prose), user = `spec.Prompt` (+ a line per image path in `spec.Images`; images are not sent
   as data URLs in v1).
3. Loop until final or budget: `Chat` (one call = one turn; `EvUsage` after each with
   cumulative tokens and cost from the price table). If the assistant message has tool calls:
   for each, `spec.Policy.Decide(name, args)` → `EvPermission`; denied → tool message
   "denied: <message>"; allowed → run the tool (MCP or local), `EvToolStarted`/`EvToolFinished`,
   tool message with the output. If `submit_note` is called: validate nothing here (the runner
   validates), set `Final = args`, emit `EvFinal`, stop. If the message has no tool calls: if
   `Content` parses as a JSON object, treat it as the final; else append a user nudge
   ("Call submit_note with the JSON note now.") once, then fail with `EvError` on a second
   prose-only reply.
4. Context trimming: when `Usage.PromptTokens` exceeds 80% of `maxContextTokens`, replace the
   `Content` of the oldest tool messages (keeping the last 6) with `[trimmed]` before the next call.
5. `Send(text)` appends a user message and continues the loop (used by the runner's schema
   retry); `Handle()` returns "" (resume not supported in v1; runner falls back gracefully);
   `Cancel()` cancels the context and closes MCP clients; `Wait()` returns `Result{Final, Text,
   Usage, StderrTail: MCP stderr tail}`; `Events()` closes when the loop ends.
6. `Doctor(ctx, binary)` ignores `binary`; `Ping` on the base URL; reports the model name.

Budget: `spec.Budget.MaxTurns` bounds Chat calls; `MaxUSD` checked from the price table each
turn (0 when no price configured, so the runner's wall-clock and turn budgets still apply).

## Tests

- `chat`: httptest server fixtures for a tool-call response, a final text response, a 429 then
  200, a 500 with body snippet, key never in errors, header presence, `ExtraHeaders`.
- `agenttools`: path confinement (`..`, absolute outside root, symlink pointing outside), read
  with offset/limit and line numbers, grep via Go fallback, glob, bash allow/deny with exact
  policy message, web_fetch content-type and size cap, output truncation marker.
- `mcpclient`: a fake MCP server binary via the re-exec test pattern speaking JSON lines:
  initialize handshake, tools/list, tools/call text + isError, request timeout, `.mcp.json`
  loading with env expansion and non-stdio skip.
- `loop`: fake chat server scripted as a sequence of responses (tool call to `read_file`, tool
  call to `bash rm -rf /` → denied, tool call to an MCP tool via the fake MCP server, then
  `submit_note`); assert the event sequence, cumulative usage/cost, `Final` equals the
  submit_note arguments, `Send` continues the loop, context trimming triggers at the threshold,
  prose-only reply → nudge → error.
- `internal/run`: existing stub tests unchanged; one new test wiring `provider: openai` through
  config to the fake chat server for a completed triage.

## Out of scope for v1

Streaming; image content parts; `response_format: json_schema` (kept for a later flag);
session resume across processes; per-model price catalogue (user supplies numbers).
