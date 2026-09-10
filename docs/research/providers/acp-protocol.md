# Agent Client Protocol (ACP), evaluated for Sirdar

Researched 2026-09-10, against the current ACP docs site and schema. ACP moved from the
`zed-industries` GitHub org to a dedicated `agentclientprotocol` org at some point before this
date (the site, registry, and the Claude/Codex adapter packages all live there now; the old
`zed-industries/*` names redirect or are marked superseded). Where a fact could not be pinned to
primary-source text, it is marked **unverified**.

Sirdar's own captured wire formats for the two protocols it already speaks
(`docs/research/06-wire-formats.md`, captured 2026-09-10 against Claude Code 2.1.267 and
codex-cli 0.154.0) are used as the comparison baseline in section 4. Note also that
`docs/research/05-verdict-and-proposal.md` and `HANDOFF.md` describe Sirdar as a TypeScript/Node
program, while this research brief was scoped as "Sirdar is a Go program" — flagged here since
it changes how literally to read section 3's Go-SDK sizing, not resolved by this document.

## 1. Specification

**Transport.** JSON-RPC 2.0 over stdio: the client spawns the agent as a subprocess and
exchanges newline-delimited JSON-RPC objects over stdin/stdout, exactly like Sirdar's existing
Claude Code and Codex integrations. A remote transport (HTTP/WebSocket) is on the roadmap; a
"Transports Working Group" was formed 2026-04-22 and "Transports" chapters exist in both the v1
and v2 docs trees, but as of this research remote transport is not shipped — local stdio is the
only transport agents actually implement. [Overview](https://agentclientprotocol.com/overview/introduction),
[Updates](https://agentclientprotocol.com/updates).

**Two protocol versions coexist right now.** v1 is the stable, implemented protocol; v2 was
published in **draft** form on 2026-07-20 and is not yet what agents/clients speak. Everything
below is v1 unless stated otherwise. [v1 schema](https://agentclientprotocol.com/protocol/v1/schema),
[v2 overview](https://agentclientprotocol.com/protocol/v2/overview) (fetched but the page did not
expose a status banner in the fetch — **unverified** beyond the 2026-07-20 draft-publish date from
the updates log).

**`initialize`** — request: `protocolVersion` (int, required), `clientCapabilities` (required),
`clientInfo` (optional: name/title/version). Response: `protocolVersion` (required, the agreed
version — the agent echoes back its own latest supported version if it can't match the client's),
`agentCapabilities` (required), `agentInfo` (optional), `authMethods` (array, may be empty).

`clientCapabilities` shape:
```json
{
  "fs": { "readTextFile": true, "writeTextFile": true },
  "terminal": true,
  "session": { "configOptions": {} }
}
```
`agentCapabilities` shape:
```json
{
  "loadSession": true,
  "promptCapabilities": { "image": true, "audio": false, "embeddedContext": true },
  "mcpCapabilities": { "http": false, "sse": false }
}
```
`mcpCapabilities.http`/`.sse` gate whether the agent can consume those two MCP transports; stdio
MCP servers are assumed universally supported and don't need a capability flag.
[Initialization](https://agentclientprotocol.com/protocol/v1/initialization),
[Schema](https://agentclientprotocol.com/protocol/v1/schema).

**`authenticate`** — request: `methodId` (required, one of the ids the agent advertised in
`authMethods`). Response is an empty result. Used for agents that need an interactive login step
before `session/new` will succeed (e.g. a device-code OAuth flow) — largely irrelevant to Sirdar
since the CLIs it targets are expected to already be logged in outside of Sirdar's process, the
same assumption it makes for Claude Code and Codex today.

**`session/new`** — request: `cwd` (required, absolute path), `mcpServers` (required, array),
`additionalDirectories` (optional, array of absolute paths, gated by a capability). Response:
`sessionId` (required, string), plus optionally `configOptions` and `modes` (the session's
available modes/config toggles, if the agent exposes any — e.g. Claude Code's plan/accept-edits
modes have an ACP analogue here). MCP server config shapes:

```json
// stdio (the only transport every agent must accept)
{ "name": "my-server", "command": "npx", "args": ["-y", "@org/server"],
  "env": [{ "name": "TOKEN", "value": "..." }] }

// http (optional, gated by agentCapabilities.mcpCapabilities.http)
{ "type": "http", "name": "my-server", "url": "https://...", "headers": [{ "name": "Authorization", "value": "Bearer ..." }] }

// sse (optional, gated by mcpCapabilities.sse; marked deprecated in current MCP itself)
{ "type": "sse", "name": "my-server", "url": "https://...", "headers": [...] }
```
[Session Setup](https://agentclientprotocol.com/protocol/v1/session-setup).

**`session/load`** — same required fields as `session/new` (`sessionId`, `cwd`, `mcpServers`),
gated by `agentCapabilities.loadSession`. Instead of returning a fresh `sessionId`, the agent
replays the entire prior conversation back to the client as a burst of `session/update`
notifications, then responds with a null result. The spec's stated intent is explicit —
"persistence across restarts and sharing sessions between different Client instances" — i.e.
cross-process resume is a design goal, not just same-process continuation. [Session
Setup](https://agentclientprotocol.com/protocol/v1/session-setup).

**`session/prompt`** — request: `sessionId` (required), `prompt` (required, `ContentBlock[]`).
Response: `stopReason` (required). Content blocks in the prompt (and everywhere else in ACP) are:
- `text`: `{ "type": "text", "text": "..." }`
- `image`: `{ "type": "image", "mimeType": "image/png", "data": "<base64>" }` (optional `uri`)
- `audio`: `{ "type": "audio", "mimeType": "audio/wav", "data": "<base64>" }`
- `resource_link`: `{ "type": "resource_link", "uri": "file:///...", "name": "...", "mimeType": "...", "size": 123 }` — a reference the agent may or may not dereference
- `resource` (embedded resource): `{ "type": "resource", "resource": { "uri": "file:///...", "mimeType": "...", "text": "..." } }` — the content inlined by the client

Clients must restrict which of these they send to what `promptCapabilities` (e.g.
`image`/`audio`/`embeddedContext`) the agent advertised in `initialize`.
[Prompt Turn](https://agentclientprotocol.com/protocol/v1/prompt-turn),
[Content](https://agentclientprotocol.com/protocol/v1/content).

Important for Sirdar's "text + local image paths" requirement: **there is no first-class
"local file path" image type.** An `image` block only carries `data` (base64) + `mimeType` (+
optional `uri`, whose semantics for a *local* path are not spelled out in the pages fetched —
**unverified** whether an agent will read a `file://` URI on its own vs. requiring inline
`data`). The safe, portable move — and what Sirdar already does for Claude Code and the Agent
SDK — is to read the file and inline it as base64 `data`.

**`session/update`** notifications — method `session/update`, params `{ "sessionId", "update" }`
where `update.sessionUpdate` is a discriminated-union tag. Confirmed variants and fields:
- `agent_message_chunk` — streamed assistant text (chunked `ContentBlock`, optional `messageId`)
- `agent_thought_chunk` — streamed reasoning/thinking text, same shape
- `tool_call` — first sighting of a tool call: `toolCallId` (required), `title` (required),
  `kind` (optional: `read`, `edit`, `delete`, `move`, `search`, `execute`, `think`, `fetch`,
  `switch_mode`, `other`), `status` (optional, defaults `pending`), `content`, `locations`,
  `rawInput`, `rawOutput`
- `tool_call_update` — same fields as `tool_call`, all optional except `toolCallId`; "only the
  fields being changed need to be included" — this is how Sirdar would see a tool call's
  start→in_progress→completed/failed lifecycle instead of Claude Code's paired
  `tool_use`/`tool_result` messages or Codex's `item/started`/`item/completed`
- `plan` — the agent's structured task plan
- `available_commands_update` — advertises slash-commands the agent currently supports
- `current_mode_update` — `{ "sessionUpdate": "current_mode_update", "modeId": "code" }`,
  fired when the agent's own session mode changes

`ToolCallStatus` enum: `pending`, `in_progress`, `completed`, `failed`. Tool call `content` can
itself be regular content blocks, a `diff` (`path`/`oldText`/`newText`), or a `terminal`
reference (`{ "type": "terminal", "terminalId": "..." }`) pointing at a live `terminal/*`
session. `locations` is `[{ "path": "...", "line": 123 }]`, meant for "follow the agent" UI.
[Tool Calls](https://agentclientprotocol.com/protocol/v1/tool-calls),
[Session Modes](https://agentclientprotocol.com/protocol/v1/session-modes).

There is now also a **`usage_update`** session/update variant (stabilized 2026-06-05, via the
"Session Context Size and Cost" RFD), which the smaller-model page extraction surfaced alongside
the other variants during this research. Its shipped shape is intentionally narrow: context
`used`/`size` (tokens currently in context / context window size) and an optional `cost` object
(`amount` + `currency`). The RFD text explicitly scopes this to session-level context/cost and
explicitly punts on both fine-grained per-turn token accounting ("end-turn token accounting
covered separately" — not resolved by the pages fetched) and rate limits ("Rate limits and
quotas are separate concerns that could be addressed in a future RFD"). [Session Context Size
and Cost RFD](https://agentclientprotocol.com/rfds/session-usage).

**`session/request_permission`** — request: `sessionId` (required), `toolCall` (required, a
`ToolCallUpdate` describing what's being requested), `options` (required, array of
`PermissionOption`). `PermissionOption`: `optionId` (required), `name` (required,
agent-authored human-readable label), `kind` (required, one of `allow_once`, `allow_always`,
`reject_once`, `reject_always`). Response: `{ "outcome": <RequestPermissionOutcome> }`. The
client answers by picking one `optionId` from the list the agent offered — **there is no field
for the client to attach a free-text reason/message to its answer.** The agent chooses the
option labels; the client can only select among them, it cannot inject its own explanation back
to the model the way Claude Code's `control_response` lets Sirdar attach a `message` on deny.
[Schema](https://agentclientprotocol.com/protocol/v1/schema) (page fetch was truncated before
fully rendering the `cancelled` vs. selection-outcome variants of `RequestPermissionOutcome` —
**unverified** exact tag names for that union; general-purpose JSON-RPC request cancellation by
id was separately stabilized 2026-06-29 and would be the likely mechanism if a permission
request is cancelled mid-flight).

**`session/cancel`** — a **notification** (no response expected), `{ "sessionId" }`. Cancels the
in-flight prompt turn; the agent should wind down and the eventual `session/prompt` response (or
a JSON-RPC error, if it was already in flight) should reflect `stopReason: "cancelled"`. Any
outstanding request from the agent to the client (e.g. a pending `session/request_permission`)
must still get *some* response — either a real answer or a `-32800` "Request Cancelled" JSON-RPC
error. [Cancellation](https://agentclientprotocol.com/protocol/v1/cancellation).

**`session/set_mode`** — request: `sessionId`, `modeId` (must be one of the modes the session
advertised). No meaningful response payload beyond acknowledgement; the mode change itself is
echoed back to the client asynchronously via a `current_mode_update` session/update.

**`fs/read_text_file`** — client method the agent calls: `sessionId`, `path` (absolute),
optional `line` (1-based start), `limit` (max lines). Response: `content`. **`fs/write_text_file`**
— `sessionId`, `path`, `content`; response is null on success. Both are gated by
`clientCapabilities.fs.readTextFile` / `.writeTextFile` — "If ... is false or not present, the
Agent MUST NOT attempt to call the corresponding filesystem method." [File
System](https://agentclientprotocol.com/protocol/v1/file-system).

**`terminal/*`** client methods, all keyed by `sessionId` + `terminalId` after creation:
`terminal/create` (`command`, optional `args`, `env`, `cwd`, `outputByteLimit`; returns
`terminalId`), `terminal/output` (returns `output`, `truncated`, optional `exitStatus` with
`exitCode`/`signal`), `terminal/wait_for_exit` (blocks until exit, returns `exitCode`/`signal`),
`terminal/kill` (terminal stays queryable afterward), `terminal/release` (terminal id becomes
invalid for all other `terminal/*` calls afterward). [Terminals](https://agentclientprotocol.com/protocol/v1/terminals).

**`StopReason`** enum, returned by `session/prompt`: `end_turn` ("the language model finishes
responding without requesting more tools"), `max_tokens`, `max_turn_requests`, `refusal`,
`cancelled`. [Prompt Turn](https://agentclientprotocol.com/protocol/v1/prompt-turn).

**Other v1-stable features worth knowing about, not asked for explicitly but adjacent to
Sirdar's needs:** `session/list` and `session/delete` (stabilized 2026-06-05 alongside
`usage_update`) for enumerating/removing session history; a generic `session` config-option
framework with a `model_config` category (stabilized 2026-07-20) that lets an agent expose
model/effort/context-size choices as typed options a client can render near a model picker,
rather than a hardcoded "model" field on `session/new`; a `logout` method (stabilized
2026-05-21); additional-workspace-roots support (stabilized 2026-06-01); and Elicitation
(`elicitation/create` / `elicitation/complete`, stabilized 2026-07-22/24), which is the
*agent-asks-client-for-structured-input* direction — useful for an agent that needs a form filled
in, not for getting the agent's own output to conform to a schema (see section 2).
[Updates](https://agentclientprotocol.com/updates),
[Elicitation](https://agentclientprotocol.com/protocol/v1/elicitation).

## 2. What ACP does not cover, for Sirdar's purposes

- **Structured JSON output by schema.** No field anywhere in `session/prompt`,
  `session/new`, or the capability objects lets a client hand the agent a JSON Schema and get
  a validated final answer back. The only channel is prose in `agent_message_chunk` content, and
  the agent decides on its own whether/how to format it. Workaround: prompt-only (ask for JSON
  in the system/user prompt and parse the concatenated `agent_message_chunk` text at `end_turn`),
  or — better, and consistent with what Sirdar already does for Claude Code, which forces a
  synthetic `StructuredOutput` tool call (`docs/research/06-wire-formats.md`) — expose a
  Sirdar-owned MCP tool (e.g. `submit_result`) whose input schema *is* the target schema, and
  instruct the agent to call it as its last action. ACP tool calls made against
  Sirdar-configured MCP servers surface as ordinary `tool_call`/`tool_call_update` events with
  `rawInput` populated, so Sirdar reads the structured object out of `rawInput` on that call
  instead of parsing free text. This needs no protocol support beyond what ACP already has
  (`session/new.mcpServers` + normal tool-call reporting), only agent cooperation via the prompt
  — no worse than the Claude Code and Codex workaround, and it sidesteps free-text parsing
  entirely if the agent actually calls the tool.
- **Token usage / cost, fine-grained.** Partially covered as of 2026-06-05: `usage_update`
  gives context-window `used`/`size` and an optional session-cumulative `cost`
  (`amount`+`currency`), but not a per-turn input/output/cache-token breakdown comparable to
  Claude Code's `result.usage` (`input_tokens`, `output_tokens`,
  `cache_creation_input_tokens`, `cache_read_input_tokens`) or Codex's
  `thread/tokenUsage/updated` (`totalTokens`, `inputTokens`, `cachedInputTokens`,
  `outputTokens`, `reasoningOutputTokens`). Whether individual agents actually populate
  `usage_update` (vs. leaving `cost` null and only reporting context `used`/`size`) is
  agent-specific and **unverified** here — Sirdar would need to probe each target agent.
- **Rate-limit signals.** Not covered at all, and the RFD that added `usage_update`
  explicitly says so ("Rate limits and quotas are separate concerns that could be addressed in a
  future RFD"). Claude Code has `rate_limit_event` with `rateLimitType`/`unifiedWindows`/
  `utilization`/`resetsAt`; Codex app-server has `account/rateLimits/updated` with
  `usedPercent`/`windowDurationMins`/`resetsAt`. ACP has neither a stable notification nor an RFD
  in flight for this as of this research. Sirdar's board feature that pauses the queue when a
  provider's window is spent has **no ACP signal to drive it** — it would have to fall back to
  detecting an agent-specific error string/code in a `stop_reason`/refusal, or simply not support
  quota-aware pausing for ACP-driven agents.
- **Model selection, as a first-class field.** No `model` parameter on `session/new` or
  `session/prompt`. The 2026-07-20 `model_config` config-option category gives agents a
  standard *category tag* to hang a model/effort/context picker on, but the actual option id/
  values are agent-defined strings inside the generic `configOptions` mechanism, not an ACP enum
  Sirdar can drive uniformly across agents — Sirdar would still need per-agent knowledge of
  which config option id means "model."
- **Permission policy as a client-side rule set, vs. per-request prompts.** ACP's model is
  interactive: the agent calls `session/request_permission` per tool call, offering
  agent-chosen options (`allow_once`/`allow_always`/`reject_once`/`reject_always`), and the
  client answers by selecting one. There's no ACP-level concept of the client pre-declaring a
  policy ("always deny Bash", "always allow Read") the *agent* consults before even asking —
  `allow_always`/`reject_always` are scoped to the option the agent itself offered and are
  session/agent-local memory, not a client-side rule Sirdar can hand over at `session/new`. A
  Sirdar-side permission engine still has to answer every `request_permission` call itself
  (fast, deterministically, based on its own rules) — functionally fine, since Sirdar wants
  read-only-by-default gating anyway, but it means the "policy" lives entirely in Sirdar's
  request handler, not in a payload the protocol lets you configure once.
  There is also, as noted in section 1, **no field for the client to attach a message/reason to
  its permission answer** — only `optionId` selection. This directly limits the "deny writes via
  reject_once with a message" pattern Sirdar uses with Claude Code today (where `deny` carries a
  `message` the model sees, e.g. `"Sirdar policy: triage runs are read-only"`). In ACP, Sirdar
  can only select the agent's own `reject_once`-labeled option; if it wants the model to see a
  policy explanation, that has to be threaded through the *original prompt* ("do not write
  files; if you attempt to, explain in your next message that Sirdar policy is read-only") rather
  than injected at the moment of denial.
- **Resume across processes.** Nominally covered — `session/load` is explicitly designed for
  "persistence across restarts and sharing sessions between different Client instances"
  (section 1) — but whether it's *actually implemented* by any given agent is a per-agent
  question gated by `agentCapabilities.loadSession`, and how widely it's implemented in practice
  was not stated on the pages fetched (**unverified**, needs a per-agent capability probe; see
  section 3 for what's known about the two agents Sirdar already targets under other protocols).

## 3. Implementations

### Agents

The ACP registry (`https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json`) lists
~40 registered agents as of this research; selected entries relevant to Sirdar, with launch
command from the registry:

| Agent | Native / adapter | Launch |
|---|---|---|
| Claude (Anthropic) | Adapter, official, maintained under the ACP org | `npx @agentclientprotocol/claude-agent-acp@0.76.0` (formerly `@zed-industries/claude-code-acp`, which "has been renamed... migrate to continue receiving updates") |
| Codex (OpenAI) | Adapter, official-ish (maintained under the ACP org, not OpenAI itself) | `npx @agentclientprotocol/codex-acp@1.11.0` (formerly `@zed-industries/codex-acp`) |
| Gemini CLI | Native (Google ships `--acp` directly) | `npx @google/gemini-cli@0.59.0 --acp` |
| Goose (Block) | Native, binary | binary, no npx wrapper listed |
| Qwen Code (Alibaba) | Native | `npx @qwen-code/qwen-code@0.23.2 --acp --experimental-skills` |
| Kimi CLI (Moonshot) | Native, binary | binary |
| Crush | not found in the registry snapshot fetched — **unverified**, may be unregistered or renamed | — |
| OpenCode | Native, binary | binary |
| Junie (JetBrains) | Native, binary | binary; homepage `junie.jetbrains.com` |
| Augment (`auggie`) | Native (`--acp` flag) | `npx @augmentcode/auggie@0.36.0 --acp` |
| GitHub Copilot CLI | Native | `npx @github/copilot@1.0.83 --acp` |
| Cursor | Native, binary | binary; docs at `cursor.com/docs/cli/acp` |
| Devin (Cognition) | Native, binary | binary |

[Registry](https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json).

The two entries Sirdar already has native adapters for are worth detailing, since they show what
"native or adapter" costs in practice — both `claude-agent-acp` and `codex-acp` are maintained,
actively developed npm/GitHub projects under the `agentclientprotocol` org (2.5k★/687 commits and
366★/509 commits respectively at fetch time), not throwaway shims:

- **`claude-agent-acp`** wraps the official Claude Agent SDK and translates
  ACP↔Agent-SDK-protocol↔MCP. README-documented feature surface: image handling, tool
  permission workflows, plan/TODO reporting, nested subagent transcripts, interactive/background
  terminals, custom slash commands, client-supplied MCP servers, and a "provider-neutral goal
  extension" for session-scoped long-running goals. Needs `ANTHROPIC_API_KEY` (or equivalent
  Claude Agent SDK auth) in the environment — **unverified** whether it supports subscription-CLI
  login the way Sirdar's direct `claude -p` spawn does; this is the same
  BYO-subscription-vs-API-key question `docs/research/03-licensing-byo-subscription.md` already
  flags for the Agent SDK generally, and using this adapter would inherit it.
  [github.com/agentclientprotocol/claude-agent-acp](https://github.com/agentclientprotocol/claude-agent-acp).
- **`codex-acp`** is a Rust binary (npm-distributed) that "translates ACP requests into Codex
  operations, and maps Codex events back into the client." Documented feature surface: native
  ACP subagent sessions, permission-request events, embedded images in prompts, client-supplied
  MCP servers (stdio + HTTP), per-turn file-change reports, text/resource-link/shell-command/
  terminal-output/reasoning events.
  [github.com/agentclientprotocol/codex-acp](https://github.com/agentclientprotocol/codex-acp).

Since Sirdar already talks Claude Code's native stream-json and Codex's native app-server
directly, these two ACP adapters are not where ACP adds value for Sirdar — they're a second,
lossier path to agents Sirdar already drives better. The value is the other ~35 entries in that
table: agents Sirdar has no other way to reach.

### Clients

Also large and heterogeneous — the get-started/clients page lists 50+ entries across editors
(Zed native; JetBrains native; Neovim via CodeCompanion/agentic.nvim/avante.nvim/hermes.nvim;
Emacs via `agent-shell.el`; multiple VS Code extensions), terminal/TUI clients (`acpx`, `Toad`,
`Hydra`), and a long tail of desktop/web/mobile wrappers. None of this changes Sirdar's build —
Sirdar is itself an ACP *client*, and the existence of many other clients only matters
insofar as it signals the protocol's health. [get-started/clients](https://agentclientprotocol.com/get-started/clients).

### SDKs

- **Rust** (`agent-client-protocol` crate): official, used by Zed itself to integrate agents —
  the highest-confidence, most battle-tested SDK. Provides `Agent`/`Client` traits to implement
  either side; version/1.0 status not confirmed from the docs page fetched (**unverified**, check
  crates.io directly). [libraries/rust](https://agentclientprotocol.com/libraries/rust).
- **TypeScript** (`@agentclientprotocol/sdk`): official, fluent `agent()`/`client()` handler
  registration API, with the older `AgentSideConnection`/`ClientSideConnection` classes kept for
  back-compat but deprecated in favor of the fluent API. Both Rust and TypeScript SDKs reached
  1.0.0 on 2026-06-25 per the updates log. [libraries/typescript](https://agentclientprotocol.com/libraries/typescript),
  [Updates](https://agentclientprotocol.com/updates).
- **Python and Kotlin**: both listed as official libraries on the docs site
  (`agentclientprotocol/python-sdk`, `agentclientprotocol/kotlin-sdk` per search results), also a
  PyPI package `agent-client-protocol`. Not independently fetched/assessed in this research —
  **unverified** maturity beyond their existence.
- **Go: no official SDK.** The community-libraries page lists **six** unofficial Go
  implementations: `github.com/coder/acp-go-sdk`, `github.com/ironpark/acp-go`,
  `github.com/eino-contrib/acp`, `github.com/spachava753/acp-sdk`, `github.com/Tangerg/acp`,
  `github.com/caelis-labs/acp-go-sdk`. The two inspected directly:
  - `coder/acp-go-sdk` (Coder, the company): `Agent`/`Client` interfaces, generated types from
    the official schema, `NewAgentSideConnection`/`NewClientSideConnection` stdio transports,
    extension-method support (`_`-prefixed methods, `_meta`), union-type helpers
    (`TextBlock`/`ImageBlock`/`ToolContent`). 230★, v0.13.5, Apache-2.0, cites production use in
    Gemini CLI and Claude Code bridge implementations. The most credible of the six on
    provenance (backed by a company with its own production agent-integration need) and
    completeness. `go get github.com/coder/acp-go-sdk@v0.13.5`.
    [github.com/coder/acp-go-sdk](https://github.com/coder/acp-go-sdk).
  - `ironpark/acp-go`: pluggable transport (stdio default, HTTP+SSE also supported — ahead of
    the official spec's own transport work), middleware system, exhaustive pattern-matching
    helpers for the discriminated unions, generated types. Explicitly labeled unofficial with a
    "may lag behind active protocol development" caveat. 30★, no version tags found. Smaller and
    less proven than `coder/acp-go-sdk` but has a design (middleware, pluggable transport) that
    might fit Sirdar's existing provider-adapter shape well.
    [github.com/ironpark/acp-go](https://github.com/ironpark/acp-go).
  - The other four were not fetched — **unverified**.

  **If Sirdar is in fact Go** (per this brief's framing; see the note at the top about the
  TypeScript/Node architecture actually on record), `coder/acp-go-sdk` is the reasonable starting
  point rather than hand-rolling, given it already has generated types tracking the official
  schema and both connection directions implemented. A hand-rolled minimal client, for scope
  comparison, would need: (1) generated or hand-written Go structs for
  `InitializeRequest/Response`, `NewSessionRequest/Response`, `LoadSessionRequest`,
  `PromptRequest/Response`, `ContentBlock` (as a manually-tagged union — Go has no native sum
  types, so this is the most tedious part), `SessionUpdate` (another manual union, ~7 variants),
  `RequestPermissionRequest/Response`, `McpServerConfig` (3 variants), `ToolCallUpdate`,
  `StopReason` (const enum) — roughly 250-400 lines of types given ACP's field count; (2) a
  newline-delimited JSON-RPC 2.0 reader/writer over a subprocess's stdin/stdout with id-keyed
  pending-request tracking for both directions (client calling agent, agent calling client
  concurrently) — roughly 150-250 lines, very close to what Sirdar's Codex app-server adapter
  already has since that's also bidirectional JSON-RPC over stdio; (3) a dispatch layer mapping
  incoming method names to handlers (`session/update` notifications, `session/request_permission`
  /`fs/*`/`terminal/*` requests from the agent) — roughly 100-150 lines. All told, a working
  minimal client purpose-built for exactly Sirdar's needs (skip `terminal/*`, skip audio,
  skip elicitation) is plausibly **600-900 lines of Go**, comparable in size to one of Sirdar's
  existing provider adapters — i.e. not a large lift either way, which weakens the case for
  taking on a less-proven community dependency over writing it directly against the schema.

## 4. Comparison vs. Claude Code stream-json and Codex app-server

Baseline for the two existing columns is Sirdar's own captured traffic
(`docs/research/06-wire-formats.md`, 2026-09-10, Claude Code 2.1.267 / codex-cli 0.154.0), not
general documentation — these are the real field names Sirdar's adapters already parse.

| Capability | ACP v1 | Claude Code stream-json | Codex app-server |
|---|---|---|---|
| Session start (cwd + prompt text/images) | `session/new{cwd, mcpServers}` then `session/prompt{prompt: ContentBlock[]}`; image is inline base64 `data`+`mimeType`, no native local-path type | `claude -p --input-format stream-json`; first `user` line, image as `{"type":"image","source":{"type":"base64",...}}` — same inline-base64 pattern | `thread/start{cwd}` then `turn/start{input:[{"type":"text",...},{"type":"localImage","path":"..."}]}` — **Codex accepts a literal local file path**, no client-side base64 step needed |
| Streamed assistant text | `session/update` → `agent_message_chunk` (+ `agent_thought_chunk` for reasoning) | `assistant` message lines with `content:[{"type":"text"|"thinking",...}]`, plus `stream_event` partials when `--include-partial-messages` | `item/started`/`item/agentMessage/delta`/`item/completed` on an `agentMessage` item, with a `phase` (`commentary` vs `final_answer`) distinguishing scratch talk from the answer |
| Tool call start/end, name + input | `session/update` → `tool_call` (creation) then `tool_call_update` (status transitions); `rawInput` carries the args | `assistant` message with `content:[{"type":"tool_use","name":...,"input":...}]`, paired later with a `user`/`tool_result` message | `item/started`/`item/completed` on `commandExecution`/`fileChange`/`mcpToolCall` items, with `item/commandExecution/outputDelta` for streaming output |
| Permission request, allow/deny + message | `session/request_permission{options:[{optionId,name,kind}]}`; client answers by **selecting one agent-defined option** — no client-supplied message/reason field | `control_request{subtype:"can_use_tool"}`; client answers `control_response` with `behavior:"allow"` (optional `updatedInput`) or `"deny"` **with a free-text `message` the model sees** | `item/*/requestApproval` (command execution, file change, permissions) plus `item/tool/requestUserInput`; approve/decline, exact message-field support on decline **unverified** from the captured probe (no denial was exercised — sandbox refused the write before a request was even sent) |
| Usage / token accounting | `usage_update`: context `used`/`size` + optional `cost{amount,currency}` only; no per-turn input/output/cache breakdown in the schema as shipped | `result.usage`: `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, plus `total_cost_usd`, `modelUsage` | `thread/tokenUsage/updated`: `totalTokens`, `inputTokens`, `cachedInputTokens`, `outputTokens`, `reasoningOutputTokens`, `modelContextWindow` |
| Rate-limit notices | **None.** Explicitly out of scope per the usage RFD | `rate_limit_event{rate_limit_info:{status,resetsAt,rateLimitType,unifiedWindows:{five_hour,seven_day}}}` — dedicated, structured, native | `account/rateLimits/updated{rateLimits:{limitId,primary:{usedPercent,windowDurationMins,resetsAt},secondary,credits}}` — dedicated, structured, native |
| Final answer as JSON matching a schema | No schema field anywhere; prose-only, or an MCP tool-call trick (section 2) | No native `--json-schema`-style enforcement either **in the base CLI flags**, but Sirdar's own probe used a `--json-schema` flag and got the model to call a synthetic `StructuredOutput` tool, with `result.structured_output` carrying the parsed object — i.e. Claude Code (as Sirdar drives it) has an explicit, first-class structured-output path | `turn/start{outputSchema:{...}}` accepted directly as a request param; the answer lands as the `text` of the `agentMessage` item with `phase:"final_answer"`, which Sirdar parses as JSON — **native `outputSchema` support**, no tool-call trick required |
| Resume handle | `sessionId` from `session/new`, resumed via `session/load` if `agentCapabilities.loadSession` is true — designed for cross-process resume, adoption per-agent and **largely unverified** here | `session_id` from the `system/init` line, resumed via `claude --resume <session_id>` — native, cross-process (session state is a file on disk) | `threadId`/`sessionId` from `thread/start`, resumed via `thread/resume` — native, listed as a top-level app-server method |
| Cancel | `session/cancel` notification (`{sessionId}`), plus generic by-request-id JSON-RPC cancellation (stabilized 2026-06-29) | Not directly captured in the probe; interrupt is via the control protocol / SIGINT (Sirdar's existing implementation, not re-verified here) | `turn/interrupt` — native, dedicated method |
| MCP server configuration | `session/new.mcpServers`: stdio required, http/sse optional and capability-gated | `--mcp-config` JSON file, `mcpServers` map, stdio/sse/http, same shape as Claude Desktop's config | Config-file (`config.toml`) driven, `mcpServer/startupStatus/updated` events surface per-server startup; `mcpServerStatus/list` method for introspection |

The pattern across every row: **ACP is the generic, lowest-common-denominator shape**, and the
two protocols Sirdar already speaks are each richer than ACP *for that specific vendor* — which
is expected, since Claude Code and Codex's own wire protocols aren't trying to also describe
Gemini CLI or Goose. Rate limits and schema-constrained output are the two rows where ACP isn't
just "less rich" but genuinely has nothing, not even a lesser version.

## 5. Recommendation

**Add an ACP provider adapter as Sirdar's "everything else" provider.** Not to replace the
Claude Code or Codex adapters — both of those should stay on their native protocols, since
section 4 shows ACP is strictly worse for those two vendors specifically (no schema-constrained
output without a tool-call workaround for Claude, no native rate-limit signal for either, no
free-text permission-denial message). ACP earns its place by being the only way to reach the
other ~35 agents in the registry — Gemini CLI, Goose, Qwen Code, Kimi CLI, OpenCode, Junie,
Augment, Cursor, GitHub Copilot CLI, Devin, and whatever ships next — with one adapter instead of
N bespoke ones. That matches Sirdar's own stated design principle
(`docs/research/05-verdict-and-proposal.md`): "normalised into one event vocabulary," which ACP
gives close to for free across everything except usage/rate-limits/structured-output, all three
of which Sirdar already has to special-case per provider anyway.

**Concrete protocol steps for one Sirdar run over ACP:**

1. Spawn the chosen agent binary (e.g. `npx @google/gemini-cli --acp`), speak JSON-RPC 2.0
   over its stdin/stdout.
2. `initialize{protocolVersion, clientCapabilities:{fs:{readTextFile:true,writeTextFile:false},
   terminal:false}}` (Sirdar declines write-fs and terminal capabilities up front where its
   permission policy would deny them anyway — cheaper than denying every individual request).
   Read back `agentCapabilities` to learn `loadSession`, `promptCapabilities`,
   `mcpCapabilities.http`/`.sse`.
3. `session/new{cwd: <ticket worktree>, mcpServers: [<Sirdar's evidence-source MCP servers,
   stdio>, <Sirdar's submit_result MCP server>]}` → get back `sessionId`; persist it as the
   resume handle.
4. `session/prompt{sessionId, prompt:[{type:"text", text: <triage prompt including the target
   JSON schema and an instruction to call submit_result>}, {type:"image", mimeType, data:
   <base64 of each local attachment>}]}` — held open as a pending JSON-RPC request while...
5. ...`session/update` notifications stream in: `agent_message_chunk`/`agent_thought_chunk` for
   the live transcript view, `tool_call`/`tool_call_update` for the tool-call timeline (including
   the eventual `submit_result` call, whose `rawInput` is the structured answer), `plan` for the
   task list, `usage_update` for whatever context/cost signal the agent chooses to send.
6. Any `session/request_permission` arriving mid-turn is answered per Sirdar's policy engine —
   e.g. for a read-only triage run, select whichever offered option has `kind: "reject_once"`
   (there is no message field to attach a policy explanation to the answer itself, per section 2
   — the read-only instruction has to already be in the step-4 prompt so the model doesn't need
   telling twice).
7. `session/prompt` finally resolves with `{stopReason}`. On `end_turn`, read the structured
   answer out of the `submit_result` tool call's `rawInput` (primary path) or, if the agent never
   called it, fall back to parsing the last `agent_message_chunk` text as JSON (fallback path,
   same posture as Sirdar's Claude Code/Codex adapters already take toward malformed output). On
   `refusal`/`max_turn_requests`/`max_tokens`, surface as a failed run the same way Sirdar
   already handles those cases for its other two providers. On `cancelled` (e.g. because Sirdar
   sent `session/cancel{sessionId}` due to a timeout/budget), tear down as an aborted run.
8. To resume later (new Sirdar process, same ticket): re-spawn the agent, `initialize` again,
   `session/load{sessionId, cwd, mcpServers}`, drain the replayed `session/update` history, then
   send a fresh `session/prompt` continuing the conversation — contingent on that agent's
   `agentCapabilities.loadSession` being true, which is not universal (section 2/3).

**Fallback plan for structured output**, since no ACP agent is guaranteed to honor an
instruction-only JSON request faithfully: ship a Sirdar-owned MCP server exposing one tool
(`submit_result`) whose JSON Schema is generated from Sirdar's target output shape per run, tell
every ACP agent in its prompt "call `submit_result` exactly once with your final answer," and
treat the `tool_call`'s `rawInput` as authoritative. If the run ends without that tool ever being
called, fall back to best-effort parsing of the concluding `agent_message_chunk` text (wrapped in
a lenient JSON extractor — strip code fences, take the last top-level JSON object found) and mark
the result low-confidence for the human-review gate. This mirrors what Sirdar's own probe shows
Claude Code effectively does today with its synthetic `StructuredOutput` tool call, and needs no
ACP protocol feature beyond what already exists (MCP servers + normal tool-call reporting).

## Open questions

- **Language mismatch**: this brief was scoped assuming Sirdar is a Go program; Sirdar's own
  `HANDOFF.md`/`05-verdict-and-proposal.md` describe a TypeScript/Node architecture instead. If
  Node is in fact current, the official `@agentclientprotocol/sdk` (TypeScript, 1.0.0 since
  2026-06-25) is the correct starting point, not a Go SDK — worth confirming which is live before
  acting on section 3's Go sizing.
- Which of the priority agents (Gemini CLI, Goose, Qwen Code, Kimi CLI, OpenCode) actually
  populate `usage_update` with a non-null `cost`, and which just report context `used`/`size` —
  not established here, needs a per-agent probe the way `06-wire-formats.md` did for Claude Code
  and Codex.
- Real-world `session/load` (cross-process resume) support rate across agents Sirdar would
  target — the spec's intent is confirmed, adoption is not.
- Exact `RequestPermissionOutcome` JSON shape for the `cancelled` case (schema page fetch was
  truncated before rendering it) — matters for how Sirdar's ACP adapter should model a permission
  request that gets cancelled mid-flight by a `session/cancel`.
- Whether an `image` content block's optional `uri` field is ever honored by an agent as a
  "go read this local/remote file yourself" signal, vs. `data` being the only reliable path —
  affects whether Sirdar can skip base64-encoding large images for agents that support it.
- ACP v2's actual scope and timeline to general availability — draft since 2026-07-20, no v1
  deprecation date found; worth a follow-up check before building against v1 gets locked in for a
  long time.
