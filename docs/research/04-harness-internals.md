# How the existing harnesses are built (checked 2026-09-10)

## Comparison

| Project | Stack | Agent integration | Event normalisation | Isolation | UI | License | Stars |
|---|---|---|---|---|---|---|---|
| T3 Code (pingdotgg/t3code) | TypeScript + Effect; Node/Bun server; SQLite; monorepo | Claude: Agent SDK in-process (`query()`, `pathToClaudeCodeExecutable` = user's own binary). Codex: `codex app-server` stdio JSON-RPC via generated Effect client. Cursor/Grok: ACP. OpenCode: its SDK | Canonical `ProviderRuntimeEvent` union in `packages/contracts/src/providerRuntime.ts`; orchestration is event-sourced (command, decider, events, projections, reactors) | Per-thread git worktree or local checkout; checkpoints as hidden refs | Electron desktop, React web, Expo mobile; WS RPC | MIT | 22.3k |
| Vibe Kanban (BloopAI) | Rust (axum, sqlx/SQLite, ts-rs), React, Tauri, `npx vibe-kanban` | `StandardCodingAgentExecutor` trait. Claude: CLI spawn `npx -y @anthropic-ai/claude-code@<pin> -p --output-format=stream-json --input-format=stream-json --permission-prompt-tool=stdio` with the control protocol reimplemented in Rust. Codex: app-server. Plus Gemini, Amp, Cursor, Copilot, generic ACP | `NormalizedEntry {entry_type, content, timestamp, metadata}` with `ActionType` and `ToolStatus` incl. `PendingApproval`; streamed as json-patch through a broadcast ring | One worktree + branch per workspace | Kanban + workspace + embedded preview browser | Apache-2.0, **sunset 2026-04-10** | 28k |
| Multica | Go (Chi, sqlc, Postgres 17); Next.js; Electron; Expo | Local daemon receives tasks over WS and spawns any of 26 CLIs; per-runtime exec-env shims | Not inspected | Worktree per task inside daemon-owned root | Board, issues, inbox, chat; agents as assignees | Apache-2.0 + no hosted/embedded use without commercial licence | 49k |
| Paperclip | Node server + React; adapters as packages | `claude-local` adapter: ACP engine by default, or CLI spawn; `codex-local`, `cursor-local`, `gemini-local`; experimental Rust runner (PRP) | Adapter-specific; shared adapter-utils | `workspaceStrategy: git_worktree`; optional Bubblewrap confinement | Org chart, tickets, heartbeats, budgets, approvals | MIT | 80k |
| Conductor | Native macOS | Claude Code, Codex, Cursor, OpenCode in parallel | n/a | Worktree per workspace | Mac app | Closed | n/a |
| Symphony (openai) | Elixir/OTP reference + language-agnostic SPEC.md; optional LiveView dashboard | `codex app-server` from an Erlang port: `initialize`, `thread/start` (approvalPolicy, sandbox, `dynamicTools`), `turn/start`, loop on `turn/completed` | Consumes app-server notifications directly; tracks tokens per session | One workspace dir per issue, populated by `hooks.after_create` | Terminal + optional dashboard | Apache-2.0 | 27k |

## T3 Code in detail

- Claude launch (`apps/server/src/provider/Layers/ClaudeAdapter.ts`): `query({prompt, options})` with the user's own `claude` binary, `systemPrompt` preset `claude_code` with append, `settingSources`, `permissionMode` mapped from four runtime modes (supervised, auto-accept edits, auto, full access), `resume` + `resumeSessionAt` cursor per thread, `includePartialMessages`, `canUseTool`, `onUserDialog`, `additionalDirectories` (cwd + attachments), and its own per-thread HTTP MCP server (`apps/server/src/mcp/McpHttpServer.ts`) exposing browser-preview and PR-link tools.
- Codex launch: `codex app-server`; handles `thread/started`, `turn/*`, `item/*` deltas, and server requests for command/file approval and user input. Text generation for titles and commit messages uses `codex exec` separately. Multiple Codex accounts via shadow `CODEX_HOME`.
- Driver SPI (`provider/ProviderDriver.ts`): driver kind, config schema, `create()` returning an instance with snapshot, adapter, text generation.
- Streaming: the event log is the source of truth (`orchestration/Layers/OrchestrationEngine.ts`, `decider.ts`, `projector.ts`). Commands like `thread.turn.start`, `thread.approval.respond`; events like `thread.activity-appended`, `thread.message.assistant.delta`. Clients subscribe over WS; a coalescer and stream budget throttle deltas.
- Permissions: four modes (`docs/user/permission-modes.md`); approvals are durable activities; async Codex questions are answered by a new user message.
- Persistence: SQLite event store + projection migrations; `AgentSessionImporter.ts` imports existing Claude/Codex sessions, matching by the `cwd` recorded in the transcript.
- Read first: `docs/internals/overview.md`, `docs/internals/providers.md`, `docs/internals/glossary.md`.
- "G1 workflow": no trace in repo, docs or web. What T3 has is an Agents surface that shows Claude Code dynamic-workflow scripts under `~/.claude/projects` plus `task.*` / `hook.*` events for subagents. Treat the name as unverified.

## Vibe Kanban in detail

- Executor trait: `spawn(cwd, prompt, env)`, `spawn_follow_up(cwd, prompt, session_id, reset_to_message_id, env)`, `spawn_review`, `normalize_logs`, `default_mcp_config_path()`, `discover_options()`, `use_approvals()`. Capabilities: `SessionFork`, `SetupHelper`, `ContextUsage`.
- Approvals (`crates/executors/src/approvals.rs`): create then wait with cancellation. Claude routes through `canUseTool` control requests; ExitPlanMode approval flips to `bypassPermissions` via `updated_permissions`. Codex routes through `item/*/requestApproval`.
- MCP: `mcp_config.rs` writes servers into each agent's native config (JSON, JSONC, TOML). `crates/mcp` ships an "orchestrator mode" router: context, workspaces, sessions, create-workspace-and-first-session, link workspace to issue, remote issues/tags/assignees.
- Task model: `Task{project_id, title, description, status: Todo|InProgress|InReview|Done|Cancelled, parent_workspace_id}`, then workspaces, sessions, execution processes, coding-agent turns.
- `crates/executors/src/executors/codex/normalize_logs.rs` is a good reference for turning app-server items into UI entries.

## Symphony

- The Elixir implementation drives `codex app-server`, not `codex exec`.
- Poll loop: GenServer ticks every `polling.interval_ms` (default 30s), fetches active-state issues from the tracker adapter (Linear, GitHub Issues, Jira, Asana, GitLab), sorts by priority, applies required labels, claims, `agent.max_concurrent_agents` (10), per-state caps.
- Workspace: issue identifier mapped to a sanitised key under `workspace.root`; hooks `after_create`, `before_run`, `after_run`, `before_remove`; terminal-state issues get workspaces removed.
- Run: `thread/start {cwd, approvalPolicy, sandbox, dynamicTools}` then `turn/start`; loops turns while the issue stays active up to `agent.max_turns` (20). Tracker tools are **dynamic tools executed host-side with secrets stripped from the child environment** (`codex/dynamic_tool.ex`). This is the right pattern for helpdesk credentials.
- Retries and budgets: exponential backoff from 10s capped at 300s; stall timeout 300s; turn timeout 1h of silence; approval or input-required marks the issue blocked.
- `WORKFLOW.md` = YAML front matter (tracker, polling, workspace, hooks, agent, codex) + Liquid-style prompt body with `{{ issue.identifier }}`; status routing lives in the prompt. `SPEC.md` section 5 defines the contract.

## Claude Agent SDK (TS and Python)

| Capability | Notes | Doc |
|---|---|---|
| Entry | `query({prompt, options})` returns an async generator with `interrupt()`, `setPermissionMode()`, `setModel()`, `rewindFiles()`. Python: `query()` or `ClaudeSDKClient` for multi-turn | agent-sdk/typescript, /python |
| Streaming input | `prompt` as async iterable; `includePartialMessages` | agent-sdk/streaming-vs-single-mode |
| In-process tools | `tool()` + `createSdkMcpServer()` passed via `mcpServers`. The Claude equivalent of Codex `dynamicTools` | agent-sdk/custom-tools |
| External MCP | stdio, http, sse; `onElicitation` | agent-sdk/mcp |
| Permissions | order: hooks, deny, ask, mode, allow, `canUseTool`. Modes `default|dontAsk|acceptEdits|bypassPermissions|plan|auto`. `canUseTool` returns `{behavior:"allow", updatedInput, updatedPermissions}` or `{behavior:"deny", message, interrupt}` | agent-sdk/permissions |
| Hooks | PreToolUse, PostToolUse, UserPromptSubmit, Stop, SubagentStart/Stop, PreCompact, PermissionRequest, SessionStart/End, TaskCreated/Completed, WorktreeCreate/Remove and more | agent-sdk/hooks |
| Subagents | `agents` map, `forwardSubagentText`, `task_progress` messages | agent-sdk/subagents |
| Sessions | `resume`, `forkSession`, `resumeSessionAt`, `persistSession`, `sessionStore` adapter, `listSessions` | agent-sdk/sessions |
| Structured output | `outputFormat: {type:'json_schema', schema}` gives `result.structured_output` | agent-sdk/structured-outputs |
| Cost | `total_cost_usd`, `usage`, `maxBudgetUsd`, `maxTurns` | agent-sdk/cost-tracking |
| Settings/plugins | `settingSources`, `plugins`, `systemPrompt` preset with append | agent-sdk/claude-code-features |
| Also | `sandbox`, file checkpointing + `rewindFiles`, `pathToClaudeCodeExecutable`, `env`, `extraArgs`, OTel | agent-sdk/hosting |

## Codex

- `codex app-server`: JSON-RPC 2.0 over stdio, WebSocket or unix socket. Threads and turns: `thread/start|resume|fork|read|list|archive`, `turn/start` (input, model, cwd, `approvalPolicy`, `sandboxPolicy`, `outputSchema`, `dynamicTools`), `turn/steer`, `turn/interrupt`, `review/start`. Server-to-client requests the harness must answer: `item/commandExecution/requestApproval`, `item/fileChange/requestApproval`, `item/permissions/requestApproval`, `item/tool/requestUserInput`, `mcpServer/elicitation/request`. Generate bindings with `codex app-server generate-ts --out`.
- `@openai/codex-sdk` (TS) wraps `codex exec --experimental-json`: `startThread()`, `resumeThread(id)`, `run()`, `runStreamed()`, `outputSchema`, `sandboxMode`. Riding `exec` means no interactive approval callback (inferred, unverified).
- `openai-codex` (Python) drives app-server: `thread_start(approval_mode, sandbox)`, `thread_resume`, `thread_fork`, `stream()`, `steer()`, `interrupt()`, `output_schema`.

## Reusable pieces for a ticket harness

Copy the shape, don't fork:
- Symphony `SPEC.md` and `elixir/WORKFLOW.md`: tracker adapter, poll, per-ticket workspace, agent session, retry/backoff/stall/blocked. Mirror `tracker/*.ex` and the host-side tool execution for Zoho Desk and Janus.
- T3 `packages/contracts/src/providerRuntime.ts`: the best provider-agnostic event vocabulary found. `docs/internals/overview.md` for the command, decider, event, projection, reactor discipline. MIT.
- T3 `ClaudeAdapter.ts` query-options block and `CodexAdapter.ts` app-server switch: reference mapping for permission modes, resume cursors, approvals, user-input dialogs.
- Vibe Kanban `crates/executors/src/logs/mod.rs` (`NormalizedEntry`, `ActionType`, `ToolStatus`) and `msg_store.rs`: compact UI-facing model that already handles pending approvals. `approvals.rs` is a clean approval-service interface. Sunset project: copy, don't depend.
- Vibe Kanban `crates/mcp` orchestrator router and `mcp_config.rs`: exposing the harness to the agent as an MCP server, and injecting MCP config into each agent's native config.
- Paperclip adapter contract (`packages/adapters/claude-local/src/index.ts` header), heartbeat/budget/approval semantics, `doc/architecture/paperclip-runner.md` for a durable out-of-process runner. MIT.

Embed directly:
- `@anthropic-ai/claude-agent-sdk`: `query()`, `canUseTool`, `createSdkMcpServer()` for ticket tools, `outputFormat` for a typed triage verdict, `hooks` for audit, `maxBudgetUsd`/`maxTurns`, `resume` per ticket. Subject to the licensing question in 03.
- Codex: `codex app-server` (not `exec`) when approvals or steering matter.

Avoid: Multica's UI and hosted use (licence), Conductor (closed), a wholesale fork of T3 (large, Effect-idiomatic, contributions not accepted) or Vibe Kanban (unmaintained).

## Unverified

- "G1 workflow" in T3 Code.
- Conductor internals; Multica's exact Claude spawn code.
- Codex TS SDK lacking interactive approvals (inferred from `sdk/typescript/src/exec.ts`).
- Star counts from `gh repo view` on 2026-09-10.

## Sources

github.com/pingdotgg/t3code · github.com/BloopAI/vibe-kanban · vibekanban.com/blog/shutdown · github.com/openai/symphony · github.com/multica-ai/multica · github.com/paperclipai/paperclip · conductor.build/docs · code.claude.com/docs/en/agent-sdk/overview · code.claude.com/docs/en/agent-sdk/typescript · code.claude.com/docs/en/agent-sdk/python · code.claude.com/docs/en/agent-sdk/permissions · code.claude.com/docs/en/agent-sdk/sessions · code.claude.com/docs/en/agent-sdk/hooks · code.claude.com/docs/en/agent-sdk/structured-outputs · learn.chatgpt.com/docs/app-server · learn.chatgpt.com/docs/codex-sdk · github.com/openai/codex
