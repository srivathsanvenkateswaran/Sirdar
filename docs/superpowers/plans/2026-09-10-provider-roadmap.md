# Provider roadmap: models beyond Claude Code and Codex (plan)

Date: 2026-09-10. Status: plan only, no implementation started today.
Research: `docs/research/providers/multi-model-runtimes.md`, `docs/research/providers/acp-protocol.md`,
`docs/research/03-licensing-byo-subscription.md`.

## What the research settled

- **Codex with a custom `model_provider` is not a route.** Codex removed the Chat Completions
  wire API in early 2026 and speaks only the Responses API, so Ollama, vLLM, Groq, DeepSeek,
  Moonshot, Zhipu, DashScope and xAI backends fail at startup.
- **Anthropic-compatible endpoints are widespread.** Ollama and llama.cpp both serve
  `POST /v1/messages`; DeepSeek, Z.ai (GLM), Moonshot (Kimi), MiniMax, Alibaba DashScope and
  OpenRouter's Anthropic surface do too. Sirdar's existing Claude adapter can therefore drive
  those models unmodified via `ANTHROPIC_BASE_URL`, subject to two unknowns: whether the
  `--json-schema` structured-output path survives a non-Anthropic backend, and the exact
  Anthropic terms clause about running Claude Code against third-party backends (the
  "no explicit prohibition" reading rests on secondary commentary today).
- **ACP (Agent Client Protocol) is broad but thin.** About forty agents implement it, its
  permission request maps cleanly onto `PermissionPolicy.Decide`, but `PromptResponse`
  carries only a stop reason: no schema-constrained output, no cost, no rate-limit signal,
  and context-window usage that is still buggy. There is no official Go SDK;
  `coder/acp-go-sdk` (Apache-2.0, pre-1.0) exists, and a minimal hand-rolled client is
  600 to 900 lines.
- **Qwen Code** (OpenAI-compatible, despite the name) is the one third-party runtime that
  meets the whole contract natively: `--output-format stream-json`, `--json-schema`,
  `--resume`, MCP, and a permission mediator with a host-decides policy.
- **OpenAI-compatible Chat Completions is the universal surface** for aggregators
  (OpenRouter, Groq, Together, Fireworks, DeepInfra) and self-hosting (Ollama, vLLM,
  LM Studio, llama.cpp). llama.cpp is the only server offering hard schema guarantees
  (GBNF / `json_schema` constrained decoding).
- Open models with reliable agentic tool calling in 2026, per vendor claims and benchmarks
  cited in the report: Qwen3-Coder, DeepSeek V3.x, GLM-4.x/5, Kimi K2.x, gpt-oss; Llama 4
  weaker on long tool loops.

## Phases

**Phase 0, spikes (half a day, no product code).**
1. `ANTHROPIC_BASE_URL` against Ollama (`qwen3-coder`) and llama.cpp with the current Claude
   adapter: does `system/init` appear, do tool calls flow, does `--json-schema` produce
   `structured_output`? Record in `docs/research/providers/spike-anthropic-compatible.md`.
2. Pull the primary text of Anthropic's terms on third-party backends for Claude Code and
   quote it in `03-licensing-byo-subscription.md`. If it forbids the proxy path, the
   `ANTHROPIC_BASE_URL` route is documented as "possible, not supported by Sirdar".
3. Qwen Code: capture its stream-json and control-request shapes the way
   `06-wire-formats.md` did for Claude and Codex.

**Phase 1, `provider: openai` (Sirdar's own loop).** Sirdar runs the agent loop itself against
any OpenAI-compatible endpoint with its own read-only tool set: `read_file`, `list_dir`,
`grep`, `bash` (allow-list from `permissions.bash`), `web_fetch`, plus every tool from the
workspace's MCP servers via an MCP client. Structured output: `response_format: json_schema`
where the server supports it, else a final `submit_note` tool whose arguments are validated
against the note schema (one retry). Usage and cost from the response `usage` block and a
per-model price table in config. Config:

```yaml
provider: openai
openai:
  baseUrl: https://openrouter.ai/api/v1      # or http://localhost:11434/v1
  apiKey: env:OPENROUTER_API_KEY              # optional for local servers
  model: qwen/qwen3-coder
  maxContextTokens: 128000
  price: { inputPerMTok: 0.2, outputPerMTok: 0.8 }
```

Dependencies: `github.com/openai/openai-go` (or hand-written HTTP; decide in the plan) and
the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`). Read-only becomes a
property of the tool set, not a policy applied to someone else's tools. Estimated size:
the largest of the three, roughly the Claude and Codex adapters combined plus the tools.

**Phase 2, `provider: acp` ("everything else").** One ACP client covering Gemini CLI, Goose,
OpenCode, Kimi CLI, Crush, Junie and the rest: `initialize` → `session/new` (cwd, MCP servers)
→ `session/prompt` → `session/update` mapping → `session/request_permission` answered from
`PermissionPolicy` (reject options; ACP carries no message) → stop reason. Structured output
via a Sirdar-owned in-process MCP tool `submit_result` the prompt instructs the agent to call,
with the free-text final as fallback. No cost signal: budgets by turns and minutes only.

**Phase 3, `provider: qwen` native.** A thin variant of the Claude adapter (same stream-json
family) once Phase 0.3 has the shapes; likely under 300 lines of differences.

**Not planned:** Codex custom providers (dead end above); a Gemini-specific adapter (Gemini
CLI is reachable through ACP; its native `--output-format stream-json` can be a Phase 3
sibling if demand appears).

## Documentation stance

README gains a "Models" section: Claude Code and Codex with your own login today;
Anthropic-compatible endpoints via the Claude adapter after Phase 0 clears; any
OpenAI-compatible endpoint after Phase 1; forty-plus agents via ACP after Phase 2. Each
row states what is lost (cost signal, schema guarantee) so users choose knowingly.
