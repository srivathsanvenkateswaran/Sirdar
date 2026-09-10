# Running Sirdar against other people's models

Research date: **2026-09-10**. Everything below was checked against live docs, GitHub APIs and
vendor pages on that date. Claims that could not be confirmed from a primary source are marked
**UNVERIFIED** inline; do not build on those without re-checking.

## What Sirdar actually needs

Read off `internal/provider/provider.go` and the two shipped adapters, so the rest of this
document can be scored against something concrete rather than a wish list.

| Requirement | Where it lives | How Claude adapter gets it | How Codex adapter gets it |
|---|---|---|---|
| Streaming events (`assistant_text`, `tool_started`, `tool_finished`, `usage`, …) | `provider.Event` | `--output-format stream-json --verbose --include-partial-messages` | `codex app-server` JSON-RPC notifications |
| Host-side permission decision | `provider.PermissionPolicy.Decide` | `--permission-prompt-tool stdio`, answers `control_request` / `can_use_tool` (`claude.go:339`) | **Does not.** `sandbox:"read-only"`, `approvalPolicy:"never"`, and every `requestApproval` is declined (`codex.go:717`) |
| Schema-conforming final answer | `SessionSpec.OutputSchema` | `--json-schema <schema>` (`claude.go:59`) | `outputSchema` on the turn (`codex.go:206`) |
| Resume handle | `Session.Handle()` / `SessionSpec.Resume` | `--resume <id>` | `thread/resume` |
| Follow-up turn for schema retry | `Session.Send` | stdin `stream-json` user line | `sendUserTurn` |
| Usage and cost | `Event.InputTok/OutputTok/CostUSD` | `usage` blocks + `rate_limit_event` | app-server usage notifications |
| MCP | not in the provider contract — inherited from the CLI's own config | | |

Two things follow from this table and they drive every recommendation later.

**A runtime does not need a host-side permission callback to be usable.** The Codex adapter
already proves the alternative: lean on the runtime's own read-only sandbox and decline anything
that still asks. Any runtime with a credible read-only or plan mode can be adopted the same way.

**A runtime without schema-constrained output is a real problem.** Sirdar's whole output is a
validated note document; `Session.Send` exists purely to re-prompt after a schema failure. A
runtime that can only emit free text pushes Sirdar back to prompt-and-parse, and the weaker the
model, the worse that gets — which is precisely the model class this exercise is about.

---

## 1. Agent runtimes with a headless, machine-readable protocol

Two projects changed GitHub org since early 2026. `sst/opencode` now redirects to
**`anomalyco/opencode`** ([Anomaly org](https://github.com/anomalyco)); `block/goose` redirects
to an Agentic AI Foundation org after Block donated Goose to the Linux Foundation in April 2026
([LF press release](https://www.linuxfoundation.org/press/linux-foundation-announces-the-formation-of-the-agentic-ai-foundation),
[Goose blog](https://goose-docs.ai/blog/2026/04/07/goose-moves-to-aaif/)). The exact new Goose
path was reported as `aaif-goose/goose` by one pass and still as `block/goose` by the ACP registry
pull — **UNVERIFIED which is canonical**; old URLs redirect either way. Docs moved to
`goose-docs.ai`.

### OpenCode — `anomalyco/opencode`

MIT. 206,320 stars, `v1.18.30` released 2026-09-09 (GitHub API, 2026-09-10).

`opencode run --format json` streams JSON events on stdout; observed types include `step_start`
and `tool_use` (emitted when a call reaches `status == "completed"`)
([CLI docs](https://opencode.ai/docs/cli/)). The richer surface is the server:
`opencode serve` (default `127.0.0.1:4096`) exposes `GET /event` as SSE and, critically,
**`POST /session/:id/permissions/:permissionID`** with body `{ response, remember? }`
([server docs](https://opencode.ai/docs/server/)). Per-tool policy is `allow` / `ask` / `deny`
across `edit`, `bash`, `webfetch`, `read`, `glob`, `grep`, `task`, `skill`, `lsp`, `question`,
`websearch`, `external_directory`, `doom_loop` ([permissions docs](https://opencode.ai/docs/permissions/)).
The exact wire shape of the permission-request event on the SSE stream is not spelled out in
those docs — check the OpenAPI spec at `/doc` before implementing. **UNVERIFIED.**

Resume: `--continue/-c`, `--session/-s <id>`, `--fork`, `--attach`. MCP: native, local
(`command` array) and remote, under `mcp` in `opencode.json`
([MCP docs](https://opencode.ai/docs/mcp-servers/)). ACP: `opencode acp` over JSON-RPC/stdio,
with permissions carried through ACP ([ACP docs](https://opencode.ai/docs/acp/)).

Bring-your-own-model is OpenCode's strongest suit: built on the AI SDK plus
[models.dev](https://models.dev), documented as 75+ providers, with local runtimes wired through
`@ai-sdk/openai-compatible` and `provider.<id>.options.baseURL`.

**Schema-constrained output: not found.** No `--json-schema` equivalent, no `response_format`
config surfaced. Treat as absent until proven otherwise.

### Goose — Agentic AI Foundation (née `block/goose`)

Apache-2.0. 54,081 stars, `v1.50.0` released 2026-09-08.

`goose run --output-format text|json|stream-json`; `stream-json` emits JSONL during execution
([CLI commands](https://goose-docs.ai/docs/guides/goose-cli-commands/)). Event type names for
`stream-json` are not enumerated in the docs — **UNVERIFIED detail, confirmed capability**.

Permissions come in two flavours. The CLI has four coarse modes — `auto`/`chat`, `approve`
(confirm every call), `smart_approve` (an LLM `PermissionJudge` gates only sensitive calls) —
selected by `GOOSE_MODE` or `/mode`
([modes reference](https://deepwiki.com/block/goose/6.2-permission-modes-and-tool-approval)).
Whether a host process can answer individual `approve`-mode prompts over `stream-json` rather
than interactive stdin is **UNVERIFIED and load-bearing**. The solid path is ACP: Goose is a
native ACP agent over `POST /acp` with documented permission flows and session resumption
([ACP providers](https://goose-docs.ai/docs/guides/acp-providers/),
[AAIF blog](https://aaif.io/blog/goose-doubles-down-on-open-in-latest-two-releases)).

Structured output exists, but through **recipes**: a recipe declares a `response_schema` and
"Goose will validate the agent's output against the schema and retry if invalid"
([recipes guide](https://goose-docs.ai/docs/guides/recipes/session-recipes/)). That is Sirdar's
schema-retry loop implemented inside Goose, which is either convenient or a duplicated
responsibility depending on taste.

Resume: `--resume`, `--name <name>`. MCP: native by design — `--with-extension` (stdio),
`--with-streamable-http-extension` (remote), `--with-builtin`. Providers: 15+ including Groq,
Google, OpenRouter, Mistral, and local Ollama / Ramalama / Docker Model Runner
([provider config](https://goose-docs.ai/docs/getting-started/providers/)). vLLM, LM Studio and
llama.cpp are not named explicitly — presumably reachable via a generic OpenAI-compatible entry,
**UNVERIFIED**.

### Gemini CLI — `google-gemini/gemini-cli`

Apache-2.0. 106,892 stars, `v0.59.0` released 2026-09-08.

Best-documented headless surface of the six. `--output-format json` returns one object with
`response`, `stats` and optional `error`; `--output-format stream-json` returns JSONL with event
types **`init`**, **`message`**, **`tool_use`**, **`tool_result`**, **`error`**, **`result`**
(final aggregated stats with per-model token breakdown)
([headless docs](https://geminicli.com/docs/cli/headless/)).

Permissions are coarse: `--approval-mode` takes `default`, `auto_edit`, `yolo`, `plan`
(read-only). The finer mechanism is ACP mode — the flag is now `--acp`, with
`--experimental-acp` still referenced in an open stdout-contamination bug
([#22647](https://github.com/google-gemini/gemini-cli/issues/22647)) — which exposes
`setSessionMode` ([ACP mode docs](https://geminicli.com/docs/cli/acp-mode/)). A per-call
host-arbitrated hook beyond `setSessionMode` is **UNVERIFIED**.

Resume is unusually rich: `--resume`/`-r` (most recent), `--resume 5` (by index),
`--resume <uuid>`, plus in-session `/chat save|resume` and a `/resume` browser
([session management](https://geminicli.com/docs/cli/session-management/)). MCP via `mcpServers`
in `settings.json`, stdio and remote
([MCP docs](https://google-gemini.github.io/gemini-cli/docs/tools/mcp-server.html)).

Schema-constrained output: **not found** as a CLI flag. (Gemini the *API* has structured output
at [ai.google.dev](https://ai.google.dev/gemini-api/docs/structured-output); that is a different
surface.)

**Bring-your-own-model: no.** Gemini/Vertex only. The OpenAI-compatible-endpoint feature request
is open and unmerged
([#23385](https://github.com/google-gemini/gemini-cli/issues/23385)): "Gemini CLI currently only
works with Google's Gemini API… cannot currently [use] OpenAI GPT, Anthropic Claude, local models
via Ollama/vLLM." The ecosystem's answer to that is to fork it, which is what Qwen Code is.

### Qwen Code — `QwenLM/qwen-code`

Apache-2.0. 27,744 stars, `v0.23.2` released 2026-09-09. (The GitHub "latest release" API
surfaces sub-package tags like `sdk-typescript-v0.1.11`; filter for plain `vX.Y.Z`.)

**This is the closest match to Sirdar's contract of anything researched, and it is not a
near-miss — it hits every box.** It began as a Gemini CLI fork but has diverged: it has its own
daemon and features upstream lacks.

- Events: `--output-format text|json|stream-json` plus `--include-partial-messages`
  ([headless docs](https://qwenlm.github.io/qwen-code-docs/en/users/features/headless/)).
- Permissions: an ACP bridge with a real **multi-client permission mediator** —
  `BridgeClient.requestPermission` → `MultiClientPermissionMediator.request`, under one of four
  policies: `first-responder`, `designated`, `consensus`, **`local-only`**, with
  `permissionResponseTimeoutMs`
  ([ACP bridge docs](https://qwenlm.github.io/qwen-code-docs/en/developers/daemon/03-acp-bridge/)).
  `local-only` is exactly "the host decides". Started via `qwen --acp` (stdio subprocess) or
  `qwen serve`. Plain-CLI modes also exist: `--approval-mode plan|default|auto-edit|auto|yolo`.
- Schema output: **`--json-schema`**, inline literal or `@./path/to/schema.json`, validated JSON
  on stdout
  ([structured output docs](https://qwenlm.github.io/qwen-code-docs/en/users/features/structured-output/)).
- Resume: `--continue`, `--resume [sessionId]`, with conversation writer locks and crash recovery.
- MCP: yes.
- BYO model: `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL` (aliased `QWEN_MODEL`), set by
  shell export, `.qwen/.env`, or `~/.qwen/settings.json`
  ([auth docs](https://qwenlm.github.io/qwen-code-docs/en/users/configuration/auth/)). Any
  `/v1/chat/completions` endpoint — so OpenRouter, Groq, DeepSeek, Ollama, vLLM, LM Studio,
  llama.cpp.

The name is misleading. Nothing about it is Qwen-only.

### Kimi CLI — `MoonshotAI/kimi-cli`

Apache-2.0 per GitHub metadata; the ACP registry lists it as MIT — **conflicting, UNVERIFIED**.
11,337 stars, `1.50.0` released 2026-09-01.

`--output-format text|stream-json` with a symmetric `--input-format`, JSONL, documented for
"programmatic integration"; `--final-message-only` trims to the final assistant message
([kimi-command.md](https://github.com/MoonshotAI/kimi-cli/blob/main/docs/en/reference/kimi-command.md)).
Permissions: `--yolo`/`--yes`/`--auto-approve`, `--afk` (auto-approve tool calls *and* dismiss
`AskUserQuestion`), and **`--plan`** which restricts to read-only tools — directly usable for
Sirdar's enforcement model. Per-call arbitration goes through `kimi acp`. Resume: `--continue/-C`,
`--session`/`--resume`/`-S`/`-r`. MCP: `kimi mcp add` with `--transport stdio|http`, plus
`--mcp-config-file`
([kimi-mcp.md](https://github.com/MoonshotAI/kimi-cli/blob/main/docs/en/reference/kimi-mcp.md)).
Schema output: **not found**.

**Caveat that should decide this one:** the repo's own docs state Kimi CLI "is evolving into Kimi
Code CLI, with gradual wind-down planned". A separate `MoonshotAI/kimi-code` repo exists. Do not
build a native adapter against a runtime whose maintainers have announced its retirement.

### Crush — `charmbracelet/crush`

**FSL-1.1-MIT**, which is *not* OSI-approved — the Functional Source License restricts competing
commercial use for two years per version before converting to MIT. GitHub's licence API returns
`NOASSERTION`. 27,992 stars, `v0.93.1` released 2026-09-09.

Reading `internal/cmd/run.go` directly: `crush run` has `-q/--quiet`, `-v/--verbose`,
`-m/--model`, `--small-model`, `-s/--session`, `-C/--continue` — **no `--output-format` or
`--json`**. There is a typed internal event stream (`proto.Message`, `proto.RunComplete`,
`proto.AgentEvent`) but it drives stdout text reconciliation, not a caller-facing mode. The
machine-readable surface is `crush server` (`internal/cmd/server.go`), whose
`internal/server/events.go` defines SSE-wrapped **`PermissionRequest`** (`ToolCallID`, `ToolName`,
`Description`, `Action`, `Path`, `Params`) and **`PermissionNotification`**
(`ToolCallID`, `Granted`/`Denied`) — a genuine host-arbitrated channel. The REST path for posting
the notification back is **UNVERIFIED**; read `internal/server/proto.go` at a pinned version.

BYO model via [Catwalk](https://github.com/charmbracelet/catwalk), with provider types
`openai-compat`, `anthropic`, `ollama`, `llamacpp`, `lmstudio`, `litellm`, `omlx` — the broadest
explicit local-runtime list of the six. Schema output: not found. ACP: not found; treat as no.

### aider — `Aider-AI/aider`

Apache-2.0, 48,878 stars. `--message`/`-m` runs one instruction and exits, `--message-file`/`-f`
reads from a file, `--yes` auto-confirms
([scripting docs](https://aider.chat/docs/scripting.html)). A Python API exists
(`Coder.create(...)`, `coder.run(...)`, `io=InputOutput(yes=True)`) which the docs themselves say
"is not officially supported or documented, and could change".

No JSON event stream (`--stream` controls terminal text only). No granular permission hook. No
schema output. No formal resume flag. **No MCP** — issues #2525, #3314, #4506 remain open;
users bridge it externally. Provider coverage is excellent via LiteLLM, and that is the only box
it ticks.

Maintenance is the deciding factor: the latest GitHub *Release* object is `v0.86.0` (2025-08-09),
the most recent tag is `v0.86.3.dev` (2026-02-12), and `pushed_at` is 2026-05-22. Formal releases
appear stalled. **Not confirmed as actively released in 2026.** Skip.

### Cline CLI / Roo Code / Kilo Code

**Cline** (`cline/cline`, Apache-2.0, 67,776 stars, last push 2026-09-10) ships a real headless
CLI. `--json` produces NDJSON "events for piping into other tools"; headless triggers on `--json`,
piped stdin, or redirected output. `--auto-approve false` forces TTY prompts, `--yolo` skips them,
and a desktop-IPC approval mode exists (`CLINE_TOOL_APPROVAL_MODE=desktop`) — but a documented
*programmatic* host-answered approval channel is **UNVERIFIED**; what is documented looks like
TTY/desktop IPC, not a clean subprocess hook. Native MCP (`cline mcp`), broad providers including
"any OpenAI-compatible endpoint". Session history via `cline history`; the resume flag is
**UNVERIFIED**. Cline is also a native ACP agent (`cline --acp`), which is the cleaner way in.

**Roo Code is dead.** `RooCodeInc/Roo-Code` is archived (`"archived": true`), last push
2026-05-15, 24,303 stars. Do not build on it.

**Kilo Code** (`Kilo-Org/kilocode`, MIT, 27,243 stars, last push 2026-09-10) is built on OpenCode.
`kilo serve` gives HTTP+SSE, **`kilo acp`** gives an ACP server, `kilo attach` connects a terminal
to a running agent, `kilo export [sessionID]` dumps session JSON, `--continue/-c` resumes,
`kilo mcp` manages MCP servers. 500+ models, `--model`, `KILO_PROVIDER`. Permission-hook shape and
arbitrary-schema output are **UNVERIFIED**. Reachable via ACP, so a native adapter is hard to
justify.

### Amp — Sourcegraph

**Closed source.** `sourcegraph/amp` 404s. The ToS states "Amp owns all right, title, and interest
in the Services", granting a "limited, revocable, non-exclusive, non-transferable,
non-sublicensable license" ([ampcode.com/terms](https://ampcode.com/terms)).

Technically it is a strong fit. `-x`/`--execute` plus `--stream-json` gives one JSON object per
line — `init` system message, user/assistant messages, final `result` with duration and stats;
`--stream-json-input` reads JSON from stdin for multi-turn steering including a `steer` boolean
to interrupt at the next checkpoint
([streaming docs](https://ampcode.com/docs/cli/streaming-json)). Amp notes that
`--stream-json-thinking` "extends the schema and makes it incompatible with Claude Code['s]"
shape — worth knowing if you hoped one parser would cover both. Permissions are rule-based
(`amp.mcpPermissions`, allow/block patterns, `permission_denials` in the result message); the
specific `--dangerously-allow-all` flag name is **UNVERIFIED**. Resume via
`amp threads continue <threadId>`. MCP via `amp.mcpServers` / `amp mcp add`.

BYO model is the blocker. The Unconstrained/enterprise tier offers "bring your own keys for
inference" and users can link ChatGPT or Grok subscriptions, but Amp curates which frontier models
it supports. Whether BYOK extends to an arbitrary OpenAI-compatible base URL or a local Ollama is
**UNVERIFIED and probably no**. Also: "the Services utilize third-party LLM providers to process
User Content" and those providers "may retain or access User Content" — relevant for a
support-ticket harness.

### OpenHands CLI — `OpenHands/OpenHands`

MIT core, 87,208 stars, `v1.17.0` released 2026-09-09. Actively maintained.

`openhands --headless -t "task"`, with `--json` producing structured JSONL — one object per agent
event (action/observation), i.e. its EventStream exposed
([headless docs](https://docs.openhands.dev/openhands/usage/cli/headless)). Resume:
`--resume`, `--resume <id>`, `--resume --last`. MCP over SSE / Streamable HTTP / stdio via
`config.toml`'s `[mcp]` or `openhands mcp add`, with a documented caveat that OAuth MCP servers
need interactive auth and are unsuitable for headless. BYO model via LiteLLM,
`provider/model_name` convention, 100+ providers including Ollama.

**The disqualifier is the permission model.** The docs state that headless mode "always runs in
`always-approve` mode. The agent will execute all actions without any confirmation", and that
`--llm-approve` (the security-analyzer gate) is unavailable in headless. Sirdar would be relying
entirely on the sandbox with no host veto and no read-only mode. Verify this directly before
dismissing it — it is one sentence doing a lot of work — but as written it rules OpenHands out
for a read-only triage harness.

### Pi — `earendil-works/pi`

Mario Zechner's terminal agent toolkit, moved from `@mariozechner` to Earendil Works in May 2026;
`v0.84.2` (2026-08-14). Described as "an agent harness rather than a finished, opinionated coding
environment", and explicitly: "Pi does not provide a built-in security sandbox or the kind of
permission system developers may expect after using Claude Code or similar tools." That settles
it for Sirdar. Its JSON output, MCP, resume and BYO-model support were **not verified**. No other
"Pi coding agent" was found; treat Parallel/pi-labs interpretations as not found.

### Codex CLI with a custom `model_provider`

Apache-2.0, 123,031 stars, `rust-v0.154.0` released 2026-09-09. Config shape:

```toml
[model_providers.custom_provider]
name = "Custom Provider"
base_url = "https://.../v1"
env_key = "MY_API_KEY"
wire_api = "responses"
```

**`wire_api = "chat"` is gone.** It was deprecated in
[openai/codex#7782](https://github.com/openai/codex/discussions/7782) (2025-12-09) and hard-removed
in PR #10157 around February 2026. Codex now speaks only the Responses API wire protocol, so any
provider that implements `/v1/chat/completions` and nothing else **fails at startup**. Confirming
reports: `janhq/jan#7413`, `farion1231/cc-switch#2806`, `farion1231/cc-switch#2553` (a DeepSeek
provider 404ing for exactly this reason). The docs and the config schema currently disagree about
the default ([openai/codex#13628](https://github.com/openai/codex/issues/13628)) — trust the
schema.

This one change guts the "point Codex at anything" plan. Ollama, vLLM, LM Studio, llama.cpp,
Groq, Together, Fireworks, DeepInfra, DeepSeek, Moonshot, Zhipu, DashScope and xAI all expose
Chat Completions; a Responses-API surface is the exception, not the rule. OpenRouter and
translating proxies are the way through, and third-party guides confirm people do run Codex
against OpenRouter/DeepSeek/Ollama this way — fragile and provider-dependent.

Other friction: `model_reasoning_effort` reportedly ignored at session start for some providers
([#28113](https://github.com/openai/codex/issues/28113)); custom providers "unusable with existing
chats and the model picker" in the Desktop app ([#29156](https://github.com/openai/codex/issues/29156));
`apply_patch` behaviour under non-OpenAI models **UNVERIFIED**. `codex app-server` does carry a
`modelProvider` field on `ThreadStartParams`, but `TurnStartParams` can override `model` without
overriding `modelProvider` — test that. Provider IDs `openai`, `ollama`, `lmstudio` are reserved
and cannot be redefined.

No ToS problem: `model_providers` is a documented first-class feature of an Apache-2.0 binary, and
in that mode OpenAI's service is not being used at all. Whether OpenAI's *subscription* terms say
anything about using ChatGPT auth alongside third-party providers was **not checked**.

### Claude Code against Anthropic-compatible endpoints

The mechanism is `ANTHROPIC_BASE_URL` plus `ANTHROPIC_AUTH_TOKEN` (or `ANTHROPIC_API_KEY`), or
their `settings.json` equivalents. Claude Code appends `/v1/messages` itself, so the base URL is
given **without** a `/v1` suffix ([settings docs](https://code.claude.com/docs/en/settings)).

Confirmed endpoints:

| Backend | `ANTHROPIC_BASE_URL` | Source |
|---|---|---|
| DeepSeek | `https://api.deepseek.com/anthropic` | [api-docs.deepseek.com](https://api-docs.deepseek.com/guides/agent_integrations/claude_code) |
| Moonshot / Kimi | `https://api.moonshot.ai/anthropic` (`POST /anthropic/v1/messages`) | [platform.kimi.ai](https://platform.kimi.ai/docs/guide/claude-code-kimi) |
| Z.ai / Zhipu GLM | `https://api.z.ai/api/anthropic` (also `https://open.bigmodel.cn/api/anthropic` for mainland) | [docs.z.ai](https://docs.z.ai/scenario-example/develop-tools/claude) |
| MiniMax | `https://api.minimax.io/anthropic` | [platform.minimax.io](https://platform.minimax.io/docs/api-reference/text-anthropic-api) |
| Alibaba Model Studio | `https://dashscope.aliyuncs.com/apps/anthropic`, `https://coding-intl.dashscope.aliyuncs.com/apps/anthropic`, plus workspace-scoped regional variants | [alibabacloud.com](https://www.alibabacloud.com/help/en/model-studio/claude-code) |
| OpenRouter ("Anthropic Skin") | `https://openrouter.ai/api` | [openrouter.ai](https://openrouter.ai/docs/cookbook/coding-agents/claude-code-integration) |
| **Ollama (local)** | `http://localhost:11434` | [docs.ollama.com](https://docs.ollama.com/api/anthropic-compatibility) |
| **llama.cpp (local)** | `http://localhost:8080` | [HF blog](https://huggingface.co/blog/ggml-org/anthropic-messages-api-in-llamacpp) |
| claude-code-router | local proxy, `ccr code` | [musistudio/claude-code-router](https://github.com/musistudio/claude-code-router), MIT |

OpenRouter's own setup requires `ANTHROPIC_API_KEY=""` explicitly blanked alongside
`ANTHROPIC_AUTH_TOKEN`. Z.ai's guide sets `API_TIMEOUT_MS=3000000` and maps
`ANTHROPIC_DEFAULT_OPUS_MODEL` / `SONNET` / `HAIKU` onto GLM tiers. MiniMax has an open bug
([MiniMax-M2.7#46](https://github.com/MiniMax-AI/MiniMax-M2.7/issues/46)) where `/anthropic`
misreports the context window as 200K against an actual 1M, causing Claude Code to compact early.
LiteLLM proxy's `/v1/messages` passthrough is widely assumed to exist but was **not confirmed
from LiteLLM's own docs this pass — UNVERIFIED**.

That Ollama and llama.cpp both now implement `/v1/messages` natively is the single most
consequential finding for Sirdar. Ollama's implementation supports messages, streaming, system
prompts, multi-turn, base64 vision, tools, tool results and extended thinking; it does **not**
support `tool_choice`, prompt caching, or `/v1/messages/count_tokens`. llama.cpp's converts
Anthropic wire format to OpenAI internally and reuses its existing tool-calling pipeline,
including Anthropic-style SSE and `count_tokens`.

**Licence and terms.** Claude Code is **not open source**: GitHub reports `"license": null`, and
`LICENSE.md` reads "© Anthropic PBC. All rights reserved. Use is subject to Anthropic's Commercial
Terms of Service." 144,632 stars on a proprietary repo mostly measures issue-watching.

On whether pointing it at a third-party model is permitted: **no explicit prohibition was found**,
but the primary text was not retrieved this pass — this rests on secondary legal commentary and is
**PARTIALLY VERIFIED**. What *is* firmly established, and already recorded in
`docs/research/03-licensing-byo-subscription.md`, is a different restriction: Anthropic bans using
Claude Free/Pro/Max **subscription OAuth credentials** outside Anthropic's own applications, while
explicitly carving out "an end user signing in to the unmodified Claude Code binary with their own
Claude subscription". Setting `ANTHROPIC_BASE_URL` neither modifies the binary nor touches
Anthropic's service, so the OAuth ban does not obviously reach it — but that reading is synthesis,
not a quoted clause. Before Sirdar documents this path, read
`anthropic.com/legal/aup` and `anthropic.com/legal/commercial-terms` directly.

A second unknown matters just as much and is purely technical: **does `--json-schema` still
constrain output when the backend is not Anthropic?** If it is implemented client-side (injected
tool or prompt), it survives. If it relies on an Anthropic-side feature, it does not, and the
entire path loses Sirdar's most important guarantee. This is the first thing to test.

---

## 2. The Agent Client Protocol

ACP is JSON-RPC 2.0 over stdio between an Agent subprocess and a Client (the editor, or Sirdar).
Ground truth below comes from the schema itself
(`agentclientprotocol/agent-client-protocol`, `schema/v1/meta.json` and `schema/v1/schema.json`),
not from prose docs, which lag.

**Wire `protocolVersion` is `1`.** The schema *package* is separately versioned at **1.21.0**
(2026-08-20, [v1 CHANGELOG](https://github.com/agentclientprotocol/agent-client-protocol/blob/main/schema/v1/CHANGELOG.md)).
A `2.0.0-alpha.3` draft exists that renames `authenticate` → `auth/login`; not production-current.
Everything is Apache-2.0.

Agent methods: `initialize`, `authenticate`, `session/new`, `session/load`, `session/set_mode`,
`session/set_config_option`, `session/prompt`, `session/cancel`, `session/list`, `session/delete`,
`session/resume`, `session/close`, `logout`.

Client methods: `session/request_permission`, `session/update` (notification), `fs/read_text_file`,
`fs/write_text_file`, `terminal/create`, `terminal/output`, `terminal/release`,
`terminal/wait_for_exit`, `terminal/kill`, `elicitation/create`, `elicitation/complete`.

`session/update` has **11** variants, more than the docs list
([drift tracked in #1694](https://github.com/agentclientprotocol/agent-client-protocol/issues/1694)):
`user_message_chunk`, `agent_message_chunk`, `agent_thought_chunk`, `tool_call`,
`tool_call_update`, `plan`, `available_commands_update`, `current_mode_update`,
`config_option_update`, `session_info_update`, `usage_update`.

`ToolCall` carries `toolCallId`, `title`, `kind`, `status`, `content[]`, `locations[]`, `rawInput`,
`rawOutput`. `kind` ∈ `read | edit | delete | move | search | execute | think | fetch |
switch_mode | other`. `status` ∈ `pending | in_progress | completed | failed` (v2 adds `cancelled`).

Permissions: `session/request_permission` takes `{sessionId, toolCall, options: PermissionOption[]}`
and returns `{outcome}`. `PermissionOption = {optionId, name, kind}` with `kind` ∈
`allow_once | allow_always | reject_once | reject_always`. This is a genuine host-arbitrated
callback — it maps cleanly onto `PermissionPolicy.Decide`.

`StopReason` ∈ `end_turn | max_tokens | max_turn_requests | refusal | cancelled`.

### What ACP does not give Sirdar

**Structured output: confirmed absent.** `PromptResponse` has exactly one field besides `_meta`:
`stopReason`. Grepping `schema.json` finds no `response_format`, no `outputSchema`, nothing
equivalent. The only JSON-schema-shaped construct is `elicitation/create`'s `requestedSchema`,
which is the client asking the *user* for structured input mid-turn — the opposite direction. All
model output travels as free-text `agent_message_chunk`. A Go host gets zero protocol help
extracting a validated document.

**Usage: present but shaky.** `usage_update` → `UsageUpdate {used, size, cost?}`,
`Cost {amount, currency}`. That is context-window occupancy, not a prompt/completion/cache split.
It landed in [PR #316](https://github.com/agentclientprotocol/agent-client-protocol/pull/316)
(Jan 2026) and [#1860](https://github.com/agentclientprotocol/agent-client-protocol/issues/1860)
is an open bug that the spec's own wording is self-contradictory and implementations already
diverge. Sirdar's `budget.maxUsd` would be running on that.

**Model selection: no dedicated method.** No `session/set_model`. Model choice goes through
`session/set_config_option` against a `ConfigOption` the agent advertises with `category: "model"`
(stabilised in schema 1.16.0). The same mechanism supersedes `session/set_mode`, which the docs say
"will be removed in a future version"
([session config options](https://agentclientprotocol.com/protocol/session-config-options)).
So model selection is an untyped, per-agent-defined string, with no shared notion of context
limit, pricing or capability.

**Resume: present.** `session/load` gated on `AgentCapabilities.loadSession`, plus `session/resume`,
`session/list`, `session/delete`, `session/close`.

**MCP: present.** `NewSessionRequest.mcpServers` is a required field;
`AgentCapabilities.mcpCapabilities = {http, sse}` with stdio as the baseline.

### Who implements it

The live registry (`https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json`,
[repo](https://github.com/agentclientprotocol/registry), Apache-2.0) lists **40 agents**.
Selected:

| Agent | Native / adapter | Package | Licence |
|---|---|---|---|
| Gemini CLI | native | `@google/gemini-cli --acp` (no longer `--experimental-acp`) | Apache-2.0 |
| Claude Code | adapter | `@agentclientprotocol/claude-agent-acp` (`@zed-industries/claude-code-acp` deprecated) | proprietary |
| Codex | adapter | `@agentclientprotocol/codex-acp` (Rust binary, renamed from `@zed-industries/codex-acp`) | Apache-2.0 |
| Goose | native | `goose acp` | Apache-2.0 |
| OpenCode | native | `opencode acp` | MIT |
| Qwen Code | native | `qwen --acp` | Apache-2.0 |
| Kimi CLI | native | `kimi acp` | MIT (conflicts with repo metadata) |
| Cline | native | `cline --acp` | Apache-2.0 |
| Amp | adapter | `amp-acp` binary | Apache-2.0 |
| GitHub Copilot CLI | native | npx | proprietary |
| Cursor | native | binary | proprietary |

**Not in the registry:** OpenHands (treat "OpenHands speaks ACP" as false for Sept 2026), and
Crush (no ACP mention found).

Clients: Zed (reference, [external agents docs](https://zed.dev/docs/ai/external-agents)),
JetBrains across IntelliJ/PyCharm/GoLand/WebStorm, Neovim via `olimorris/codecompanion.nvim`,
`brianhuster/acp.nvim`, `hador/emeth.nvim`, `carlos-algms/agentic.nvim`, and Emacs via
[`acp.el`](https://xenodium.com/introducing-acpel).

### Go SDK

**No official Go SDK.** The org ships Rust, TypeScript (`@agentclientprotocol/sdk`), Python
([python-sdk](https://github.com/agentclientprotocol/python-sdk)), Kotlin and Java. Go is absent.

Community options:

- **[`coder/acp-go-sdk`](https://github.com/coder/acp-go-sdk)** — the serious one. Maintained by
  Coder, Apache-2.0, `go get github.com/coder/acp-go-sdk`, examples for both Agent and Client
  roles, integration examples against Claude Code and Gemini CLI. Pre-1.0; version reports were
  inconsistent across fetches, so treat the API as unstable.
- `ironpark/acp-go` — unofficial, stdio + HTTP/SSE transports.
- `joshgarnett/agent-client-protocol-go`.

### Would one ACP client cover most third-party agents?

Yes for reach, no for fidelity.

One client reaches all 40 registry agents — Google, Anthropic and OpenAI via adapters, plus
Alibaba, Moonshot, the Goose/OpenCode/Cline/Kilo open-source tier, Cursor and Copilot. For a host
that mainly needs "stream what the agent did and let me veto tool calls", it is an excellent deal:
the permission callback and the tool-call metadata are exactly the right shape, and resume is
first-class.

What Sirdar specifically loses:

- **Schema-conforming final answer — the whole point of a Sirdar run.** Falls back to
  prompt-and-parse plus `Session.Send` retries, with no protocol guarantee, against models chosen
  for being cheap.
- **Cost accounting** for `budget.maxUsd`, which becomes best-effort on a field with an open
  correctness bug.
- **Token split** for `InputTok`/`OutputTok` — `usage_update` gives occupancy, not a breakdown.
- **Model configuration** becomes an untyped per-agent string, so `SessionSpec.Model` cannot be
  passed through uniformly.
- **Tool semantics** flatten into a 10-value `kind` enum, so `PermissionPolicy`'s bash-glob
  matching has to work off `rawInput` shapes that differ per agent anyway.

---

## 3. Aggregators, vendor APIs and self-hosting

### Aggregators

| Provider | OpenAI base URL | Anthropic `/v1/messages` | Auth env var |
|---|---|---|---|
| OpenRouter | `https://openrouter.ai/api/v1` | **Yes** — "Anthropic Skin" at `https://openrouter.ai/api` | `OPENROUTER_API_KEY` |
| Groq | `https://api.groq.com/openai/v1` | No | `GROQ_API_KEY` |
| Together AI | `https://api.together.ai/v1` | No | `TOGETHER_API_KEY` |
| Fireworks AI | `https://api.fireworks.ai/inference/v1` | No | `FIREWORKS_API_KEY` |
| DeepInfra | `https://api.deepinfra.com/v1/openai` | No | `DEEPINFRA_TOKEN` |
| Novita AI | `https://api.novita.ai/openai` | No | UNVERIFIED var name |

**OpenRouter** is the only aggregator with both surfaces, which makes it the single highest-value
integration target. It exposes per-model capability flags: `supported_parameters` on the Models
API, so `GET https://openrouter.ai/api/v1/models?supported_parameters=tools` enumerates
tool-capable models, and requests carrying `tools` are routed only to providers that support them
([models API](https://openrouter.ai/docs/api/api-reference/parameters/get-parameters)). BYOK is
supported at a **5% surcharge on upstream cost**, keys encrypted at rest, zero-data-retention
policies respected ([BYOK guide](https://openrouter.ai/docs/guides/overview/auth/byok)).

Its own caveat on the Anthropic skin deserves quoting: reliable tool use "is only guaranteed on
Anthropic's own models when routing to alternative backends". So the skin is not a free pass to
run GLM through Claude Code and expect Claude-grade tool calling.

**Groq's roster has narrowed.** Its live models doc lists Llama 3.1/3.3, gpt-oss-120b and
gpt-oss-20b, Qwen 3.6-27B/3.8-27B (preview) and MiniMax M2.7 (enterprise preview) —
**no DeepSeek, no GLM**, and `moonshotai/kimi-k2-instruct` explicitly **retired** in favour of
`openai/gpt-oss-120b` ([deprecations](https://console.groq.com/docs/deprecations)). Any 2025-era
"Kimi K2 on Groq" guidance is stale. Groq's OpenAI compatibility also omits `logprobs`,
`logit_bias`, `top_logprobs`, `messages[].name`, and requires `n = 1`
([compat doc](https://console.groq.com/docs/openai)).

Together, Fireworks, DeepInfra and Novita are all plain OpenAI-compatible with per-model tool
support; none expose capability flags as cleanly as OpenRouter, and BYOK was **not confirmed** for
any of them this pass. Their exact model rosters churn weekly — check the live catalogue, not a
snapshot.

### Vendor APIs

| Vendor | OpenAI base URL | Anthropic base URL | Notes |
|---|---|---|---|
| DeepSeek | `https://api.deepseek.com` | `https://api.deepseek.com/anthropic` | Model ids `deepseek-flash`, `deepseek-v4-pro` per the fetched doc — **UNVERIFIED, post-cutoff naming, confirm before hardcoding** |
| Z.ai / Zhipu | `https://api.z.ai/api/paas/v4` (**UNVERIFIED**) | `https://api.z.ai/api/anthropic` | `API_TIMEOUT_MS=3000000`; GLM tier mapping reported as GLM-4.7 / GLM-4.5-Air — **UNVERIFIED** |
| Moonshot | `https://api.moonshot.ai/v1` | `https://api.moonshot.ai/anthropic` | Model ids came back garbled (`kimi-k3[1m]`, `kimi-k2.7-code`, `kimi-k2.6`) — **UNVERIFIED** |
| Alibaba DashScope | `dashscope[-intl].aliyuncs.com/compatible-mode/v1` **or** workspace-scoped `*.maas.aliyuncs.com/compatible-mode/v1` — the two schemes need reconciling | via Model Studio `/apps/anthropic` (see §1) | `DASHSCOPE_API_KEY`; region keys not interchangeable |
| xAI | `https://api.x.ai/v1` | not found | `XAI_API_KEY`, [function-calling guide](https://docs.x.ai/developers/tools/function-calling) |
| Mistral | `https://api.mistral.ai/v1` | not found | `MISTRAL_API_KEY`; current Devstral/Codestral/Magistral ids **UNVERIFIED** |
| Google Gemini | `https://generativelanguage.googleapis.com/v1beta/openai/` | not found | `GEMINI_API_KEY`, [compat doc](https://ai.google.dev/gemini-api/docs/openai) |
| Meta Llama | first-party hosted API in 2026 **UNVERIFIED** | — | In practice Llama reaches agents through aggregators |

The pattern is clean and worth stating plainly: **every vendor speaks OpenAI; only the Chinese
labs plus OpenRouter speak Anthropic.** DeepSeek, Zhipu, Moonshot, MiniMax and Alibaba all built
`/anthropic` surfaces specifically to capture Claude Code users. xAI, Mistral, Google and the
Western aggregators did not.

### Self-hosting

**Ollama.** OpenAI-compatible at `http://localhost:11434/v1/` covering `/chat/completions`,
`/completions`, `/models`, `/embeddings` and, since v0.13.3, `/responses` (non-stateful only).
`tools` is supported; **`tool_choice` is not**
([compat doc](https://docs.ollama.com/api/openai-compatibility)). It also now serves
`POST /v1/messages` natively — messages, streaming, system prompts, multi-turn, base64 vision,
tools, tool results, extended thinking; no `tool_choice`, no prompt caching, no `count_tokens`
([Anthropic compat doc](https://docs.ollama.com/api/anthropic-compatibility)).

The context-length trap is worth a loud line in Sirdar's docs: **the OpenAI-compatible API has no
per-request way to set context size.** You either set `OLLAMA_CONTEXT_LENGTH` on the server or
bake `PARAMETER num_ctx` into a Modelfile and `ollama create` a variant. A silently-default context
window truncating tool schemas and history is a classic agentic failure mode. No evidence was
found for an `ollama launch` command — **UNVERIFIED / probably does not exist**. The docs index
does list a web-search capability and a cloud page, and integrations with VS Code, JetBrains,
Xcode, Zed, Cline, Goose, Roo Code and Copilot CLI.

**vLLM.** `vllm serve`, tool calling via `--enable-auto-tool-choice --tool-call-parser <parser>`.
Parsers confirmed live ([tool calling docs](https://docs.vllm.ai/en/latest/features/tool_calling.html)):
`hermes`, `mistral`, `llama3_json`, `llama4_pythonic`, `granite`/`granite4`/`granite-20b-fc`,
`internlm`, `jamba`, `xlam`, `qwen3_xml` (Qwen3-Coder), `deepseek_v3`/`deepseek_v31`, `openai`
(gpt-oss), `kimi_k2`, `hunyuan_a13b`, `cohere_command3`, `longcat`, `glm45`/`glm47`,
`functiongemma`, `olmo3`, `gigachat3`, `apertus`, plus a generic `pythonic` fallback. Note the
naming has moved: `glm4_moe` is now `glm45`/`glm47`, and **no `minimax` parser appeared** in this
fetch. Parser names churn per release — read `vllm/entrypoints/openai/tool_parsers/` at your
pinned version. A separate `--reasoning-parser` handles `<think>` extraction (**UNVERIFIED** list).

**LM Studio.** Local OpenAI-compatible server (conventionally `http://localhost:1234/v1`), tool
calling for compatible models, `lms` CLI. The docs URL tried this pass 404'd — **this entire entry
is UNVERIFIED against 2026 docs.**

**llama.cpp server.** Native tool-call format recognition for Llama 3.1/3.3, Functionary,
Hermes 2/3, Qwen 2.5 and Mistral Nemo, with a generic (token-hungrier) fallback for unrecognised
templates; check a running server with `GET /props` (`chat_template`, `chat_template_tool_use`) or
override with `--chat-template-file` / `--chat-template`. It also serves `POST /v1/messages`
natively with no extra flag, including `tool_use`/`tool_result` blocks, Anthropic-style SSE,
vision, extended thinking and `count_tokens`.

Its differentiator for Sirdar is **grammar-constrained decoding**:
`response_format: {"type": "json_schema", "json_schema": {...}}` compiled to GBNF, plus direct
`--grammar`/`--grammar-file`. That is a hard guarantee of schema conformance, not a request the
model may ignore — the only place in this entire survey where a weak local model can be *forced*
to emit a valid Sirdar note. (Exact parameter names **UNVERIFIED** this pass; the server README
moved from `docs/server.md` to `tools/server/README.md`.)

### Which open models actually do agentic tool calling

Caveat first: BFCL, Terminal-Bench and tau²-bench leaderboards are JS-rendered SPAs that could not
be scraped this pass, and the Aider polyglot page's visible data appears to stop around **October
2025**. What follows is model-card self-reports (fetched live from Hugging Face) plus that stale
leaderboard. Treat vendor numbers as vendor numbers.

Aider polyglot, as displayed ([leaderboard](https://aider.chat/docs/leaderboards/)):
DeepSeek-V3.2-Exp Reasoner 74.2% (2025-10-03), V3.2-Exp Chat 70.2%, Qwen3 235B A22B 59.6%,
Kimi K2 59.1% (2025-07-17), DeepSeek V3 (0324) 55.1%, gpt-oss-120b (high) 41.8% (2025-08-06),
Qwen3 32B 40.0%, **Llama 4 Maverick 15.6%** (2025-04-06), Codestral 25.01 11.1%.

Model cards:

- **Qwen3-Coder-480B-A35B-Instruct** — Apache-2.0, 480B/35B active MoE, 256K native context
  (1M via YaRN). SWE-bench Pro 38.7, Terminal-Bench 2.0 23.9. Marketed explicitly for agentic
  tool-calling ([card](https://huggingface.co/Qwen/Qwen3-Coder-480B-A35B-Instruct)).
- **Kimi K2-Instruct** — 1T/32B active MoE, 128K. SWE-bench Verified 65.8% single-attempt /
  71.6% multi-attempt, SWE-bench Multilingual 47.3%, Terminal-Bench 25–30% harness-dependent
  ([card](https://huggingface.co/moonshotai/Kimi-K2-Instruct)).
- **DeepSeek-V3.2** — SWE-bench Verified 70, SWE-bench Pro 15.56. Its chat template adds a
  `developer` role scoped to search-agent scenarios; check that it doesn't perturb generic tool
  flows ([card](https://huggingface.co/deepseek-ai/DeepSeek-V3.2)).
- **GLM-4.6** — 357B, 200K context. SWE-bench Pro 9.67, Terminal-Bench 2.0 24.5. Zhipu claims
  parity with DeepSeek-V3.1-Terminus and Claude Sonnet 4; the SWE-bench Pro gap against DeepSeek
  suggests selective benchmarking ([card](https://huggingface.co/zai-org/GLM-4.6)).
- **Llama 4 Maverick** — 400B/17B active, 1M context. LiveCodeBench 43.4 pass@1, MMLU Pro 80.5,
  GPQA Diamond 69.8, but 15.6% on Aider polyglot and no explicit tool-calling statement on the
  card ([card](https://huggingface.co/meta-llama/Llama-4-Maverick-17B-128E-Instruct)).
- **gpt-oss-120b** — 41.8% Aider polyglot at high reasoning effort; OpenAI's own announcement page
  403'd. It has a dedicated vLLM parser (`openai`), and Groq treats it as the recommended
  replacement for several retired open models — a decent vendor-neutral signal.
- **Mistral** — Codestral 25.01 at 11.1% is weak, but Codestral is FIM/completion-oriented, not
  agentic. Devstral, the line actually built for agent harnesses, was not captured. **UNVERIFIED.**

The practical read: **Qwen3-Coder and Kimi K2 have the strongest first-party agentic framing and
first-class tool-parser support in both vLLM (`qwen3_xml`, `kimi_k2`) and llama.cpp; DeepSeek V3.2
posts the best confirmed SWE-bench Verified number; GLM-4.6 is credible but self-graded; Llama 4 is
the weakest of the cohort for agentic coding despite strong general benchmarks.** Having a
dedicated tool-call parser upstream is a better predictor of "works in an agent loop" than any
headline benchmark, because it means someone maintains the format.

Newer names surfaced only by content-farm pages — GLM-5.x, DeepSeek V4, Kimi K3, Qwen3.8-Max —
were **excluded as unverified**. Some are echoed by vendor docs fetched through a summarising
layer, which is not good enough to hardcode.

---

## 4. Recommendation for Sirdar

### Comparison

| Runtime | Models | Event stream | Permission hook | Schema output | Resume | MCP | Licence | ACP? |
|---|---|---|---|---|---|---|---|---|
| **Claude Code + `ANTHROPIC_BASE_URL`** | DeepSeek, GLM, Kimi, MiniMax, DashScope, OpenRouter (→ all), Ollama, llama.cpp | yes (already wired) | yes (already wired) | `--json-schema`, **untested off-Anthropic** | yes | yes | proprietary | via adapter |
| **Sirdar's own loop (OpenAI-compatible)** | every vendor + every aggregator + Ollama/vLLM/LM Studio/llama.cpp | Sirdar defines it | Sirdar owns the tools | native `response_format` / GBNF | Sirdar owns it | Go SDK v1.7.0 | n/a | n/a |
| **Qwen Code** | any OpenAI-compatible | `stream-json` | ACP `local-only` mediator | **`--json-schema`** | `--resume` | yes | Apache-2.0 | native |
| **ACP client (`coder/acp-go-sdk`)** | whatever the 40 agents support | `session/update`, 11 variants | `session/request_permission` | **none** | `session/load`/`resume` | `mcpServers` | Apache-2.0 | — |
| **OpenCode** | 75+ via models.dev | `--format json`, server SSE | `POST /session/:id/permissions/:id` | not found | `-c`, `-s` | yes | MIT | native |
| **Goose** | 15+ incl. Ollama | `--output-format stream-json` | coarse via `GOOSE_MODE`, real via ACP | recipe `response_schema` | `--resume` | native | Apache-2.0 | native |
| **Crush** | Catwalk: `openai-compat`, `ollama`, `llamacpp`, `lmstudio`, `litellm` | server SSE only | server SSE, path unverified | not found | `-s`, `-C` | yes | **FSL-1.1-MIT** | no |
| **Gemini CLI** | **Gemini/Vertex only** | `stream-json`, 6 event types | coarse `--approval-mode` | not found | rich | yes | Apache-2.0 | native |
| **Kimi CLI** | Moonshot + unclear | `stream-json` | `--plan` read-only, ACP | not found | `-C`, `-r` | yes | Apache-2.0 / MIT (conflict) | native |
| **Cline** | any OpenAI-compatible | `--json` NDJSON | TTY/desktop IPC | not found | history, flag unverified | native | Apache-2.0 | native |
| **Kilo Code** | 500+ | `kilo serve` SSE | unverified | session JSON only | `-c` | yes | MIT | native |
| **Amp** | curated frontier only | `--stream-json` | rule-based config | `result` message | `threads continue` | yes | proprietary | adapter |
| **OpenHands** | 100+ via LiteLLM | `--json` JSONL | **none in headless** | not found | `--resume` | yes | MIT | no |
| **Codex + custom provider** | Responses-API providers only | app-server JSON-RPC | sandbox only | `outputSchema` | `thread/resume` | yes | Apache-2.0 | adapter |
| **aider** | LiteLLM | none | `--yes` only | none | none | **none** | Apache-2.0 | no |

### Ranking against the maintainer's criteria

**1. Sirdar runs its own tool loop against any OpenAI-compatible API.** Highest coverage of
anything here — one adapter reaches DeepSeek, Zhipu, Moonshot, DashScope, xAI, Mistral, Gemini,
OpenRouter, Groq, Together, Fireworks, DeepInfra, Novita, Ollama, vLLM, LM Studio and llama.cpp,
because they all speak `/v1/chat/completions`. Perfect fidelity, because Sirdar defines every
piece it needs: the event stream is its own `provider.Event` with nothing to translate; read-only
enforcement stops being a policy applied to someone else's tools and becomes a property of the
tool set — Sirdar only implements Read/Glob/Grep/allow-listed-Bash/WebFetch, so `Write` cannot be
called because it does not exist; structured output uses native `response_format: json_schema`,
with llama.cpp's GBNF as a hard floor for local models; resume is Sirdar's own transcript; MCP
comes from the official [Go SDK v1.7.0](https://github.com/modelcontextprotocol/go-sdk),
maintained with Google, protocol 2026-07-28 with backward compatibility. Zero licence or ToS
exposure — no third-party binary in the loop. Largest code footprint of the options, and Sirdar
inherits responsibility for prompt quality, context management and loop termination.
Dependencies are small: [`openai-go` v3](https://github.com/openai/openai-go) (`option.WithBaseURL`,
tool calling, streaming) plus the MCP SDK.

**2. Claude Code pointed at an Anthropic-compatible endpoint.** Least code by a wide margin —
config keys and a doctor check, no new adapter. Immediately covers DeepSeek, GLM, Kimi, MiniMax,
DashScope, OpenRouter (and through it, everything), Ollama and llama.cpp locally. Fidelity is
where it gets uncertain: the permission hook is client-side and will survive, but `--json-schema`
may not, and that is unverified. Maintenance risk is Anthropic changing the flag surface. Licence
risk is the real cost — the flagship "bring your own model" path would depend on a proprietary
binary under Anthropic's Commercial ToS, with the third-party-backend question resting on absence
of prohibition rather than permission.

**3. One ACP client.** 40 agents from ~500 lines of Go against a pre-1.0 community SDK, with a
permission callback that maps cleanly onto `PermissionPolicy` and real session resume. It fails on
the requirement Sirdar cares most about: no structured output anywhere in the protocol, coarse and
buggy usage data for `budget.maxUsd`, untyped model selection. Maintenance risk is moderate and
shared — the protocol is Apache-2.0 with a live registry, but the Go SDK is one company's side
project and the spec is actively churning (v2 alpha renames methods; `session/set_mode` is slated
for removal).

**4. Native Qwen Code adapter.** The only third-party runtime that satisfies every line of
Sirdar's contract: `stream-json` events, `--json-schema`, `--resume`, MCP, ACP with a `local-only`
permission mediator, and any OpenAI-compatible provider. Apache-2.0, actively released. Coverage is
identical to option 1 since it is an OpenAI-compatible client. Its weakness is being one project's
fork of another project — 27,744 stars against Gemini CLI's 106,892 — carrying fork-drift risk. If
you want a third-party runtime to do the work, this is the one.

**5. Native OpenCode adapter.** The largest community here (206,320 stars), MIT, 75+ providers,
a clean host-arbitrated permission endpoint. No structured output, and that is disqualifying for
first place. Good second-wave target once a prompt-and-parse fallback exists.

**6. Goose / Cline / Kilo / Crush / Kimi CLI natively.** All reachable through ACP; none justify a
bespoke adapter. Crush additionally carries a non-OSI licence and has no JSON output on `run`.
Kimi CLI is being wound down.

**7. Codex with a custom `model_provider`.** Effectively dead for this purpose. The removal of
`wire_api = "chat"` means every Chat-Completions-only provider — which is almost all of them —
fails at startup. Keep Codex as the OpenAI adapter it already is.

**Not recommended at all:** aider (no MCP, no events, stalled releases), OpenHands (headless
forces always-approve), Roo Code (archived), Amp and Gemini CLI (no meaningful BYO model),
Pi (no permission system by design).

### What to build first, and why

**Build the direct provider: `provider: openai` — Sirdar's own agent loop over any
OpenAI-compatible endpoint.**

The reasoning turns on one asymmetry. Every option except this one and Qwen Code makes Sirdar's
structured output a hope rather than a guarantee, and structured output is not a nice-to-have here
— a Sirdar run's entire deliverable is a schema-validated note. Meanwhile the thing that usually
argues against writing your own loop, that a mature agent CLI has better tools and prompting than
you will, is much weaker for Sirdar than for a general coding agent: Sirdar's sessions are
**read-only triage**. The tool surface it needs is roughly Read, Glob, Grep, an allow-listed Bash,
WebFetch, and an MCP client. It does not need edit, patch application, diff review, terminal
management, or any of the machinery that makes a coding CLI expensive to write. `AlwaysAllowed`
and `AlwaysDenied` in `internal/provider/policy.go` already read as a specification for that tool
set.

Coverage settles it: one adapter reaches every vendor, every aggregator and every self-hosting
runtime on the maintainer's list, because OpenAI-compatibility is the universal surface and the
Anthropic-compatible surface is the exception. And it carries no licence or terms exposure at a
moment when the two lowest-code alternatives both route through someone else's proprietary binary
or a pre-1.0 SDK.

Ship the Claude-Code-via-proxy config unlock alongside it, because it costs almost nothing and
serves users who already have `claude` installed and want GLM or Kimi behind it. But gate the
documentation on actually testing whether `--json-schema` survives a non-Anthropic backend.
If it does not, that path is a demo, not a supported provider.

---

## Recommended provider roadmap

**Phase 0 — settle two unknowns before writing code.**
Test `claude --json-schema` against `ANTHROPIC_BASE_URL=http://localhost:11434` (Ollama) and
against a vendor `/anthropic` endpoint, and check whether the schema is honoured or silently
dropped. Separately, read `anthropic.com/legal/aup` and `anthropic.com/legal/commercial-terms`
directly and record the actual clause text in `03-licensing-byo-subscription.md`. Both answers
change how Phase 2 is documented.

**Phase 1 — `provider: openai`, the direct loop.** `openai-go` v3 with `option.WithBaseURL`, the
official MCP Go SDK, and a read-only tool set mirroring `policy.go`. Structured output via
`response_format: {"type":"json_schema"}`, falling back to a tool-call-shaped extraction for
providers that reject it. Resume by persisting the message array under the existing run directory
and returning its id as `Handle()`. Usage from the OpenAI `usage` object; cost from a
per-model price table, or from OpenRouter's generation endpoint when routing through it. Config
gains `providers.openai.baseUrl`, `.apiKey` (as an existing-style `env:`/`keychain:` ref),
`.model`, `.wire` (`chat` now, `anthropic` later). Validate against Qwen3-Coder and DeepSeek V3.2
through OpenRouter, then against a local Ollama model, on the seven tickets closed 2026-09-10.

**Phase 2 — Anthropic-wire variants, config only.** Add `providers.claude.baseUrl` and
`.authToken` to the existing Claude adapter so a user with `claude` installed can point it at
DeepSeek, Z.ai, Moonshot, MiniMax, DashScope, OpenRouter, Ollama or llama.cpp. Ship a
compatibility matrix in `docs/config.md` recording, per backend, whether schema output and the
`can_use_tool` hook actually work. Add `wire: anthropic` to the Phase 1 direct provider using
`anthropic-sdk-go` with a base-URL override, so the Chinese-lab endpoints and local Ollama /
llama.cpp `/v1/messages` are reachable without the proprietary binary.

**Phase 3 — ACP client.** `coder/acp-go-sdk`, mapping `session/update` variants onto
`provider.Event`, `session/request_permission` onto `PermissionPolicy.Decide`, `session/load` onto
`Resume`. Structured output comes from the Phase 1 prompt-and-parse fallback plus `Session.Send`
retries. This is what unlocks Goose, OpenCode, Cline, Kilo, Kimi, Gemini CLI and Cursor in one go —
worth doing once the event vocabulary has been proven twice.

**Phase 4 — native Qwen Code adapter, only if Phase 3's structured-output fallback proves
unreliable in practice.** `--json-schema` plus `--output-format stream-json` plus the `local-only`
permission mediator is a full-fidelity path through a third-party runtime; it is also a second
adapter to maintain, so make Phase 3 earn it.

**Deliberately not on the roadmap:** OpenCode, Goose, Crush, Cline and Kilo native adapters (ACP
covers them), Codex custom providers (`wire_api = "chat"` removed), aider, OpenHands, Roo Code,
Amp, Pi.

One precedent is worth keeping in view. Vibe Kanban built native adapters for ten agents — Claude
Code, Codex, Gemini CLI, Copilot, Amp, Cursor, OpenCode, Droid, CCR, Qwen Code — reached 28,000
stars under Apache-2.0, and is now **sunsetting**, per the notice on
[its README](https://github.com/BloopAI/vibe-kanban). Ten hand-written adapters against ten CLIs
that each ship weekly is a treadmill. Sirdar should own one loop it fully controls, plus one
protocol client, and resist the rest.

## Open questions

- Does Claude Code's `--json-schema` constrain output when `ANTHROPIC_BASE_URL` points at a
  non-Anthropic backend, or is it an Anthropic-side feature? Load-bearing for Phase 2.
- What does Anthropic's actual Commercial ToS / AUP text say about third-party model backends
  behind Claude Code? Only secondary commentary was obtained this pass.
- Does OpenCode's SSE stream carry a permission-request event whose shape a host can parse? Only
  the response endpoint is documented; check the OpenAPI spec at `/doc`.
- Can a host answer Goose's `approve`-mode prompts over `--output-format stream-json`, or is that
  interactive-stdin only?
- What is Crush's exact REST path for posting a `PermissionNotification` back?
- Is `coder/acp-go-sdk` stable enough to depend on, and what is its actual version? Reports were
  inconsistent, and it is pre-1.0.
- Which Goose org is canonical — `aaif-goose/goose` or `block/goose`? Both redirect.
- Kimi CLI's licence: repo metadata says Apache-2.0, the ACP registry says MIT.
- Current model ids for DeepSeek (`deepseek-flash`?), Z.ai (`GLM-4.7`?) and Moonshot
  (`kimi-k2.7-code`?) — all came through a summarising fetch layer and one source URL 404'd on
  re-fetch.
- Which DashScope base-URL scheme is current: `dashscope[-intl].aliyuncs.com/compatible-mode/v1`
  or workspace-scoped `*.maas.aliyuncs.com/compatible-mode/v1`?
- Does Meta run a first-party hosted Llama API in 2026?
- LM Studio's 2026 docs — the entire section here is unverified.
- Does LiteLLM proxy actually implement an Anthropic-shaped `/v1/messages` route?
- Live BFCL v4, Terminal-Bench and tau²-bench numbers — all three leaderboards are JS-rendered and
  could not be scraped; the Aider polyglot data used here appears to stop at October 2025.
