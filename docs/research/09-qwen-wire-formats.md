# Qwen Code wire formats, verified on 2026-09-11

Captured against **Qwen Code 0.23.3** (`npm i -g @qwen-code/qwen-code`) on macOS by driving the
real binary from a script. Every shape below was **exercised live** unless a line says
otherwise: the model behind it was a stub OpenAI-compatible server (a Go program serving
`POST /v1/chat/completions` with scripted `tool_calls`), so the CLI's own emitter, permission
path, resume path and MCP assembly are real and only the model's choices were fixed. No
Ollama model and no vendor key were available on the machine, which is why the backend is a
stub rather than a live model; nothing in the capture depends on which model answered.

Session ids, paths and token counts are illustrative. Companion to
`docs/research/06-wire-formats.md`, which did the same for Claude Code and Codex.

## The command Sirdar runs

```
qwen --output-format stream-json \
  --approval-mode default \
  --json-schema '<json schema>' \
  --model <model> --max-session-turns 5 --max-wall-time 25m \
  [--resume <session id>] \
  [--allowed-tools run_shell_command] \
  [--mcp-config <path> --allowed-mcp-server-names <name> ...] \
  [--auth-type openai]
```

The prompt goes in **on stdin** and stdin is then closed. `-p/--prompt` is deprecated in
0.23.3 in favour of a positional argument, and both put the whole triage prompt on the
command line; stdin has no length limit and is the documented third form
("Accepts prompts via command line arguments or stdin"). `--json-schema` with no prompt on
argv and no piped stdin is rejected at argument-parse time, so the pipe is what makes the
run legal.

Environment for a configured endpoint (all three are needed before Qwen Code infers
`openai` auth — `getAuthTypeFromEnv` requires `OPENAI_API_KEY` **and** `OPENAI_BASE_URL`
**and** one of `OPENAI_MODEL` / `QWEN_MODEL`):

```
OPENAI_API_KEY=<key>  OPENAI_BASE_URL=https://.../v1  OPENAI_MODEL=<model>
```

`--auth-type openai` forces the same choice when only some of them are present. With none of
them set the CLI falls back to the operator's own `qwen` login (`qwen-oauth`, credentials in
`~/.qwen/`), which is the Claude-adapter-style "use the login you already have" path.

### Flags Sirdar deliberately does not pass

- `--input-format stream-json`: **rejected at parse time together with `--json-schema`**
  ("The single-shot terminal contract is incompatible with the long-lived stream-json input
  protocol"). This is the one structural difference from Claude Code and it is what makes a
  Qwen session single-shot: there is no second `user` line to send, so `Session.Send` cannot
  work and the schema retry has to go through `--resume` in a fresh process.
- `--include-partial-messages`: only adds `stream_event` delta lines. The complete
  `assistant` / `user` lines carry everything the adapter reads.
- `--bare` and `--safe-mode`: both disable settings-sourced inputs, which means **no hooks and
  no headless deny rules**. A bare run would hand the model an unguarded shell.
- `--yolo` / `--approval-mode yolo`: auto-approves every tool with no sandbox.

## Output lines (stdout), in the order they occurred

```json
{"type":"system","subtype":"init","uuid":"7d235fdb-...","session_id":"7d235fdb-...","cwd":"/work",
 "tools":["read_file","grep_search","glob","web_fetch","run_shell_command","structured_output","tool_search","agent","skill","task_stop","..."],
 "mcp_servers":[],"model":"stub-model","permission_mode":"default","slash_commands":[...],
 "qwen_code_version":"0.23.3","agents":["general-purpose","Explore","statusline-setup","review-agent"]}
{"type":"stream_event","uuid":"...","session_id":"...","parent_tool_use_id":null,"event":{"type":"goal_state","goal_state":{"v":2,"goal":null,"activity":"idle"}}}
{"type":"assistant","uuid":"788c701e-...","session_id":"...","parent_tool_use_id":null,
 "message":{"id":"788c701e-...","type":"message","role":"assistant","model":"stub-model",
   "content":[{"type":"text","text":"Looking at the repository history."}],
   "stop_reason":null,"usage":{"input_tokens":0,"output_tokens":0}}}
{"type":"assistant","uuid":"9136b455-...","session_id":"...",
 "message":{"id":"9136b455-...","role":"assistant","model":"stub-model",
   "content":[{"type":"tool_use","id":"call_1","name":"run_shell_command",
     "input":{"command":"git log -1 --oneline","description":"read the last commit"}}],
   "stop_reason":"tool_use",
   "usage":{"input_tokens":1200,"output_tokens":40,"cache_read_input_tokens":800,"total_tokens":1240}}}
{"type":"user","uuid":"58c8d964-...","session_id":"...","parent_tool_use_id":null,
 "message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","is_error":false,
   "content":"9645ba3 (HEAD -> main) first"}]}}
{"type":"assistant","uuid":"eff68a6a-...","message":{"content":[{"type":"tool_use","id":"call_2","name":"write_file","input":{"file_path":"/tmp/probe.txt","content":"x"}}], ...}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_2","is_error":true,
   "content":"Qwen Code requires permission to use \"write_file\", but that permission was declined. Matching deny rule: \"edit\"."}]}}
{"type":"assistant","uuid":"7f3394f6-...","message":{"content":[{"type":"tool_use","id":"call_3","name":"structured_output","input":{"greeting":"hello","n":7}}], ...}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_3","is_error":false,"content":"Structured output accepted."}]}}
{"type":"result","subtype":"success","uuid":"7ed768dc-...","session_id":"7d235fdb-...","is_error":false,
 "duration_ms":375,"duration_api_ms":301,"num_turns":3,
 "result":"{\"greeting\":\"hello\",\"n\":7}",
 "structured_result":{"greeting":"hello","n":7},
 "usage":{"input_tokens":3600,"output_tokens":120,"cache_read_input_tokens":2400,"total_tokens":3720},
 "permission_denials":[{"tool_name":"run_shell_command","tool_use_id":"call_1","tool_input":{"command":"git log -1 --oneline","description":"read the last commit"}},
                       {"tool_name":"write_file","tool_use_id":"call_2","tool_input":{"file_path":"/tmp/probe.txt","content":"x"}}]}
```

Read `structured_result` (the object). `result` is the same payload stringified, kept for
consumers that always want a string in that field.

### How this differs from Claude Code's stream-json, line by line

| | Claude Code | Qwen Code 0.23.3 |
|---|---|---|
| init line | `system`/`init` | `system`/`init` — same shape, plus `qwen_code_version`, `agents`, `slash_commands` |
| structured output | `result.structured_output` | `result.structured_result` |
| session total cost | `total_cost_usd` | **absent** — no cost signal at all |
| input tokens | `input_tokens` + `cache_creation_input_tokens` + `cache_read_input_tokens`, disjoint | `input_tokens` is the whole prompt; `cache_read_input_tokens` is a **subset** of it, and `total_tokens == input_tokens + output_tokens` |
| permission channel | `control_request` on stdout, answered on stdin | not emitted in headless mode (see below) |
| rate limits | `rate_limit_event` lines | none observed |
| follow-up turn | second `user` line on stdin | impossible under `--json-schema`; resume instead |
| tool names | `Bash`, `Read`, `Write`, `Grep` | `run_shell_command`, `read_file`, `write_file`, `grep_search` |

The token arithmetic is the detail most likely to be got wrong by copying the Claude adapter:
adding `cache_read_input_tokens` to `input_tokens` double-counts a cached Qwen turn. Verified
against a stub that reported `prompt_tokens: 1200` with `cached_tokens: 800` — Qwen emitted
`input_tokens: 1200, cache_read_input_tokens: 800, total_tokens: 1240`.

## Permissions

### There is no `control_request` in headless mode

`emitPermissionRequest` — which writes the `control_request` / `control_response` pair
documented under [Dual Output](https://qwenlm.github.io/qwen-code-docs/) — is called from the
interactive TUI's dual-output bridge only. Reading the bundled sources, the only caller is in
`startInteractiveUI`; the headless `--output-format stream-json` path never emits one. A
headless run therefore has no stdout/stdin approval handshake. The dual-output shapes, for
reference (from source, not exercised):

```json
{"type":"control_request","request_id":"...","request":{"subtype":"can_use_tool","tool_name":"run_shell_command","tool_use_id":"...","input":{"command":"rm -rf /tmp/x"},"permission_suggestions":null,"blocked_path":null}}
{"type":"control_response","response":{"subtype":"success","request_id":"...","response":{"allowed":true}}}
```

The host answers by appending `{"type":"confirmation_response","request_id":"...","allowed":true}`
to the file named by `--input-file`, and that channel needs the TUI (and therefore a PTY),
which rules it out for Sirdar.

### What headless mode does instead: a built-in deny list

From the argument-resolution source (`chunks/chunk-DYFOSJHU.js`), for any run that is not
interactive, not ACP, not `--bare`, and not `--input-format stream-json`:

```js
case PLAN: case DEFAULT: case AUTO:
  denyUnlessAllowed(SHELL); denyUnlessAllowed(MONITOR);
  denyUnlessAllowed(EDIT);  denyUnlessAllowed(WRITE_FILE);
case AUTO_EDIT:
  denyUnlessAllowed(SHELL); denyUnlessAllowed(MONITOR);
case YOLO: /* nothing */
```

So a headless `--approval-mode default` run starts with `run_shell_command`, `monitor`, `edit`
and `write_file` denied outright, and the model is told so in the `tool_result`:
`Qwen Code requires permission to use "write_file", but that permission was declined. Matching
deny rule: "edit".` **This is a genuine read-only default and Sirdar leans on it**, the same
way the Codex adapter leans on `sandbox: read-only`.

`denyUnlessAllowed` skips a tool that is *explicitly allowed*, which is either
`permissions.allow` in a settings file or the `--allowed-tools` flag.

### Allow-rule specifiers are not enforced for the shell tool

Measured, one tool call per run, the model always asking for `git log -1 --oneline`:

| Rule | `git log -1 --oneline` |
|---|---|
| (none) | denied |
| `--allowed-tools run_shell_command` | allowed |
| `--allowed-tools "run_shell_command(npm run *)"` | **allowed** |
| `permissions.allow: ["run_shell_command(npm run *)"]` | **allowed** |
| `permissions.allow: ["run_shell_command(git *)"]` | allowed |
| `permissions.allow: ["Bash(npm *)"]` | denied |
| `permissions.allow: ["Bash(git *)"]` | **denied** |
| `permissions.allow: ["read_file"]` | denied |

Two things fall out of that table, and both are load-bearing:

1. A rule written against the **canonical** name `run_shell_command(...)` lifts the headless
   deny for *every* shell command — the specifier is ignored. The documented per-command
   syntax (`Bash(git *)`) is honoured by the matcher but the alias never lifts the deny, so
   there is no way to say "shell, but only `git log`" at the CLI level in 0.23.3.
2. Shell access is therefore all-or-nothing. Sirdar passes `--allowed-tools run_shell_command`
   **only when the workspace named `permissions.bash` patterns**, and the per-command
   allow-list is enforced by the hook below, not by Qwen.

`permissions.deny: []` changes nothing; the headless deny list is assembled after settings are
merged and an empty array does not clear it.

### The host-arbitrated channel: a `PreToolUse` hook

Hooks *are* consulted in headless mode, for every tool call that survives the deny list, and
they can allow or deny with a reason. That is the per-call mediator Sirdar's
`PermissionPolicy.Decide` plugs into. Hooks come from a settings file; Sirdar writes a private
one and points the child at it with **`QWEN_CODE_SYSTEM_SETTINGS_PATH`**, so no workspace file
and no `~/.qwen/settings.json` is touched:

```json
{
  "hooks": {
    "PreToolUse": [
      { "matcher": "*",
        "hooks": [{ "type": "http", "url": "http://127.0.0.1:54321/decide", "timeout": 15, "name": "sirdar-policy" }] }
    ]
  },
  "permissions": { "deny": ["write_file", "edit", "notebook_edit"] }
}
```

HTTP hooks are blocked from private IP ranges but **loopback is explicitly allowed**, and the
listener is an `httptest`-style server inside the Sirdar process on `127.0.0.1:0`, so nothing
leaves the machine. Request body (exercised live):

```json
{"session_id":"3cb194ec-...","transcript_path":"~/.qwen/projects/<sanitized-cwd>/chats/<id>.jsonl",
 "cwd":"/work","hook_event_name":"PreToolUse","timestamp":"2026-09-11T08:23:42.053Z",
 "permission_mode":"default","tool_name":"run_shell_command",
 "tool_input":{"command":"git log -1 --oneline","description":"read the last commit"},
 "tool_use_id":"toolu_1789115022053_n4cka89p7","tool_call_id":"call_3"}
```

Response body:

```json
{"hookSpecificOutput":{"hookEventName":"PreToolUse",
  "permissionDecision":"deny",
  "permissionDecisionReason":"Sirdar policy: triage runs are read-only"}}
```

`permissionDecision` is `allow`, `deny` or `ask`; in a headless run `ask` falls back to `deny`.
The reason reaches the model verbatim as the tool result — a denied call came back as
`{"type":"tool_result","is_error":true,"content":"Sirdar policy: triage runs are read-only"}`,
which is exactly how the Claude adapter's deny message surfaces.

A `command` hook works identically (JSON on stdin, JSON on stdout, exit 2 = blocking error);
the HTTP form is used because it needs no helper binary on disk.

**The hook fails open.** Measured: with `--allowed-tools run_shell_command` and the hook URL
pointed at a dead port, and again with a `command` hook naming a missing executable,
`git log -1 --oneline` *ran*. A non-2xx response, a connection failure and a timeout are all
treated as a non-blocking hook failure and the call proceeds. So the hook is a mediator, not a
sandbox: the static guarantees have to come from the deny list (writes) and from not
allow-listing the shell at all when the workspace named no bash patterns.

### Tool names

Rules and hook payloads use runtime ids, with documented aliases:

| Alias | Runtime id | | Alias | Runtime id |
|---|---|---|---|---|
| `Bash`, `Shell` | `run_shell_command` | | `Grep`, `SearchFiles` | `grep_search` |
| `Read`, `ReadFile` | `read_file` | | `Glob`, `FindFiles` | `glob` |
| `Edit`, `EditFile` | `edit` | | `ListFiles` | `list_directory` |
| `Write`, `WriteFile` | `write_file` | | `WebFetch` | `web_fetch` |
| `NotebookEdit` | `notebook_edit` | | `Agent` | `task` |

`Read` and `Edit` are meta-categories: a `Read(...)` rule covers `read_file`, `grep_search`,
`glob` and `list_directory`; `Edit(...)` covers `edit`, `write_file` and `notebook_edit`.
MCP tools are named `mcp__<server>__<tool>`, the same convention Sirdar's policy already
parses — the registry renames a colliding MCP tool to that form so the synthetic
`structured_output` keeps the bare name.

## Structured output (`--json-schema`)

Accepts an inline JSON literal or `@./path/to/schema.json`. Qwen registers a synthetic
`structured_output` tool, and the session ends on the first call whose arguments validate
against the schema (Ajv, strict mode). The schema must accept object-typed values at the root
(a root `$ref` is rejected; wrap it in `allOf`), and it is re-sent as the tool's `parameters`
block on **every** model request, so a large schema is paid for per turn.

On a validation failure the tool returns Ajv's message as an error tool result and the model
gets another turn — Qwen runs its own retry loop inside the session, which is a different
thing from Sirdar's retry against the *note* schema after the run.

Restrictions that matter: `--json-schema` is rejected with `-i/--prompt-interactive`, with
`--input-format stream-json`, with `--acp`, and with no prompt at all. It is a per-run flag,
so it has to be re-passed on every `--resume`.

## Resume

`--resume <sessionId>` (exercised live) and `--continue` for the most recent session in the
project. The handle is the `session_id` from the `init` line; transcripts live in
`~/.qwen/projects/<sanitized-cwd>/chats/<sessionId>.jsonl`, so resumption is project-scoped.
A resumed run re-emits an `init` line carrying **the same** `session_id` and produces its own
`result`:

```json
{"type":"system","subtype":"init","session_id":"a2569383-87c7-473b-b321-bca5cd3b6f3f", ...}
{"type":"result","subtype":"success","session_id":"a2569383-...","num_turns":1,"structured_result":{"greeting":"hello"}, ...}
```

`--fork-session` starts a branch instead of continuing, and `--session-id` names the session up
front. Resumption needs `general.chatRecording` left on (the default); `--chat-recording false`
disables it and breaks `--continue` / `--resume`.

## MCP servers

`--mcp-config` takes a path or inline JSON in `{"mcpServers":{...}}` form, and it **merges**
with the settings-file servers rather than replacing them:
`mcpServers = assembleMcpServers(settings.mcpServers, cwd, cliMcpServers)`. There is no
`--strict-mcp-config`. The lever that restricts the set is `--allowed-mcp-server-names`, which
overrides both `mcp.allowed` and `mcp.excluded` from settings. Measured against the
`mcp_servers` array on the `init` line, with one server (`globalsrv`) in the settings file and
one (`wssrv`) in a `--mcp-config` file:

| Flags | `init.mcp_servers` |
|---|---|
| settings only | `[{"name":"globalsrv","status":"disconnected"}]` |
| `--mcp-config ws.json` | `[globalsrv, wssrv]` |
| `--mcp-config ws.json --allowed-mcp-server-names wssrv` | `[wssrv]` |
| `--allowed-mcp-server-names __sirdar_none__` | `[]` |

So Sirdar's `MCPStrict` is expressed as: name the workspace file with `--mcp-config`, then
repeat `--allowed-mcp-server-names <name>` once per server the file declares; with no
workspace file, pass one sentinel name that matches nothing, which yields an empty server
list. `--bare` would also give exactly the CLI-supplied servers, but it disables hooks and the
headless deny list along with them, so it is not usable here.

## Exit codes, stderr, and the missing result line

| Code | Meaning | Result line on stdout? |
|---|---|---|
| 0 | success | yes, `subtype:"success"` |
| 1 | the model answered in prose instead of calling `structured_output` | yes, `subtype:"error_during_execution"`, `is_error:true` |
| 53 | `--max-session-turns` overrun | **no** — the stream just stops |
| 55 | `--max-wall-time` / `--max-tool-calls` overrun (`FatalBudgetExceededError`) | not exercised |
| 130 | SIGINT | normally no |

Exit 53's stderr, verbatim:

```
Reached max session turns for this session. Increase the number of turns by specifying maxSessionTurns in settings.json.
Note: --json-schema is active. If the model never called structured_output, verify it isn't denied by permissions.deny / --exclude-tools and that the schema is satisfiable.
```

A stream that ends with no `result` line is therefore a normal outcome, not a parse failure,
and the adapter has to report the exit code and the stderr tail rather than wait for a final
event. There is also an always-on loop detector that ends a run with
`subtype:"error_during_execution"` and
`error.message: "Loop detection halted the run (global_tool_call_duplicate: ...)"` when the
model repeats a tool call — observed when a denied `structured_output` was re-issued with the
same call id.

## Budgets

`--max-session-turns N` (exit 53), `--max-wall-time 25m` (accepts `90`, `30s`, `5m`, `1.5h`;
exit 55), `--max-tool-calls N` (exit 55). `structured_output` is exempt from `--max-tool-calls`
but **not** from `--max-session-turns`, so a turn budget needs one spare turn for the terminal
call. There is no spend budget, because there is no cost figure on the wire.

## What was not exercised

- A real model. The backend was a stub; token counts, `duration_api_ms` and the model's own
  behaviour under a schema are all synthetic.
- `--max-wall-time` / `--max-tool-calls` overruns (exit 55) and SIGINT's exit 130.
- MCP tools actually running — the servers in the MCP table never completed a handshake, which
  is enough to prove which ones were assembled but not what a `mcp__server__tool` hook payload
  looks like. The `mcp__` naming is from source.
- `qwen --acp` / `qwen serve`, the ACP bridge and its `MultiClientPermissionMediator`
  (`local-only` policy). That is the other route to a host-decided permission and it is worth
  revisiting if the hook's fail-open behaviour ever becomes a problem — but ACP is rejected
  together with `--json-schema`, so it would cost the structured output.
- `--continue`, `--fork-session`, `--session-id`.
