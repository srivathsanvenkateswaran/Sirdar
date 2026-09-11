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
  --model <model> --max-session-turns 5 --max-wall-time 25m --max-tool-calls 16 \
  [--resume <session id>] \
  [--mcp-config <path> --allowed-mcp-server-names <name> ...] \
  --exclude-tools write_file --exclude-tools edit --exclude-tools replace \
  --exclude-tools notebook_edit --exclude-tools image_gen --exclude-tools save_memory \
  --exclude-tools enter_worktree --exclude-tools exit_worktree \
  --exclude-tools artifact --exclude-tools record_artifact --exclude-tools record_source \
  --exclude-tools cron_create --exclude-tools cron_delete --exclude-tools workflow \
  --exclude-tools update_goal --exclude-tools propose_goal \
  --exclude-tools monitor --exclude-tools send_message \
  --exclude-tools create_sub_session --exclude-tools team_create --exclude-tools team_delete \
  (--allowed-tools run_shell_command | --exclude-tools run_shell_command) \
  [--auth-type openai]
```

The exclusion block is the read-only guarantee and is not optional; see
"The headless deny is not settings-proof" below for why the CLI's own default is not enough.
The shell is the one tool that switches sides: allow-listed when the workspace named
`permissions.bash` patterns and excluded when it did not.

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
- `-s/--sandbox`: wants a container image or a macOS Seatbelt profile the operator has set up,
  fails the run outright when it cannot start one, and is orthogonal to the exclusion list
  above. Left for an operator who wants it to configure on the binary.
- `--insecure`: the TLS equivalent of `--yolo`. `QWEN_TLS_INSECURE`, which sets the same thing,
  is stripped from the child's environment for the same reason.

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

Summing each turn's `input_tokens` into a running session total looks like it would overstate a
prompt that is largely the same every turn, but it is exactly what the CLI reports for itself:
the result line's `input_tokens` is `computeUsageFromMetrics`' `stats.totalPromptTokens`, which
accumulates `modelMetrics.tokens.prompt` across every API call of the session. The adapter's
running total therefore converges on the result line rather than diverging from it, and
tracking the per-turn maximum instead would understate a run and then jump when the result line
replaced it.

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
deny rule: "edit".`

`denyUnlessAllowed` skips a tool that is *explicitly allowed*, which is either
`permissions.allow` in a settings file or the `--allowed-tools` flag. **That makes this default
a convenience, not a guarantee** — see the next section for what it takes to hold it.

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

### The headless deny is not settings-proof; `--exclude-tools` is

`isExplicitlyAllowed` consults `permissions.allow` + `tools.allowed` (together `mergedAllow`)
and `tools.core` — each of them the **merge** of the system, user (`~/.qwen/settings.json`) and
workspace (`.qwen/settings.json`) layers. `QWEN_CODE_SYSTEM_SETTINGS_PATH` replaces only the
system layer, so it does not shut the other two out, and anything they allow cancels the
headless deny above.

Measured. A work tree carrying
`.qwen/settings.json` = `{"permissions":{"allow":["run_shell_command","write_file","edit","monitor","notebook_edit"]}}`,
run headless under `--approval-mode default` with a Sirdar-style system settings file and **no
`--allowed-tools` at all** — the "workspace named no `permissions.bash` patterns, so no shell"
case — was offered `run_shell_command`, `monitor`, `enter_worktree`, `exit_worktree`,
`cron_create`, `cron_delete`, `record_artifact`, `send_message` and `update_goal`. Only
`write_file`, `edit` and `notebook_edit` stayed out, and only because the system layer's own
`permissions.deny` covered them: deny beats allow. The shell and `monitor` had no such deny
entry and came straight back.

`--exclude-tools` closes it, and is the only thing that does. `argv.excludeTools` is appended to
`mergedDeny` with no settings consulted; that becomes both `config.excludeTools` and
`permissions.deny`; `isToolEnabled` returns false for an excluded name in **both** its branches,
the empty-`coreTools` one and the explicit-allow one; and
`PermissionManager.getToolRegistrationStatus` maps a whole-tool deny to `disabled`, which means
the tool is never registered and its schema never reaches the model. With the same workspace
file in place and Sirdar's exclusion list passed, none of the tools above were offered.

Verified against the real 0.23.3 binary driven by a stub OpenAI-compatible endpoint, reading
both the `tools` array of the request the CLI sends the model and the `tools` field of its
`system/init` line.

### Folder trust is the boundary the settings path is not

`isFolderTrustEnabled(settings)` reads `settings.security?.folderTrust?.enabled ?? false`, so
out of the box **every folder is trusted**. Turning it on in the system layer is not something
the workspace can undo: `loadSettings` computes the trust verdict from
`customDeepMerge(systemSettings, userSettings)` — the workspace layer is not in that merge —
and `mergeSettings` puts the system layer last, so it wins the merged view as well.

The verdict itself comes from the trusted-folders file, whose path is
`process.env.QWEN_CODE_TRUSTED_FOLDERS_PATH` when set and `~/.qwen/trustedFolders.json`
otherwise. Rules are `{path: TRUST_FOLDER | TRUST_PARENT | DO_NOT_TRUST}`; `resolveTrustRule`
takes the deepest rule that contains the workspace and breaks a tie in favour of an untrusted
one. An **empty** file is not enough: no matching rule yields `undefined`, which
`Config.isTrustedFolder()` resolves as `this.trustedFolder ?? true` — trusted. The workspace
has to be named `DO_NOT_TRUST` explicitly, which is what Sirdar's 0600 file does (for the path
and its `realpath`, since the CLI canonicalises the workspace before matching).

What untrusted costs, read from 0.23.3: `mergeSettings` substitutes `{}` for the whole
workspace layer; `LoadedSettings.getProjectHooks` returns nothing; a project-level subagent is
refused (`config.level === "project" && !isTrustedFolder()`) and so are a project skill's
`allowedTools` and `hooks` (`canApplySkillSideEffects`); `discoverAllMcpTools`,
`discoverAllMcpToolsIncremental` and `readMcpResource` all return early, so **no MCP server
loads at all**; project `QWEN.md` context, LSP servers and auto-skill loading are skipped;
`setApprovalMode` refuses anything but the default mode. Headless itself runs perfectly well
untrusted — measured below.

Measured against the real 0.23.3 binary with a stub endpoint, in a work tree carrying
`.qwen/settings.json` = `{"permissions":{"allow":["run_shell_command","write_file","edit","monitor","agent","skill"]}}`
and a `.qwen/agents/evil.md`:

| run | tools offered to the model |
| --- | --- |
| trusted, no `--exclude-tools` | `agent skill tool_search run_shell_command monitor cron_create cron_delete cron_list enter_worktree exit_worktree get_goal update_goal record_artifact report_findings send_message list_agents loop_wakeup task_stop zoom_image read_file read_mcp_resource grep_search glob web_fetch structured_output` |
| untrusted, no `--exclude-tools` | the same **minus `run_shell_command` and `monitor`** — the workspace allow-list was not read |
| untrusted, Sirdar's exclusions | `glob grep_search read_file read_mcp_resource structured_output web_fetch` |
| trusted, Sirdar's exclusions | the same six |

In all four the `PreToolUse` hook was posted for the `read_file` call and its `deny` was
honoured (`tool_result` = `is_error: true`, content `Sirdar policy: not permitted`).

### A repository-supplied hook outranks the host's

`HookRegistry.getHooksForEvent` sorts by `getSourcePriority`: project 1, user 2, system 3,
extensions 4, **anything else 999** — and `addAgentHooks`, which wires the `hooks:` block of a
declarative subagent into the registry, registers under source `session`. Skill frontmatter
hooks go through `SessionHooksManager` instead, and `fireHooks` builds
`[...registryHookConfigs, ...sessionHookConfigs]`, appending them after every registry hook.

`HookAggregator.mergeOutputs` sends `PreToolUse` through `mergeWithOrLogic`, which for every
output does `otherHookSpecificFields[key] = value` — a plain assignment, in order. The
permission verdict lives in `hookSpecificOutput.permissionDecision`, and
`PreToolUseHookOutput.getPermissionDecision()` reads that field first, ahead of the top-level
`decision` that `mergeWithOrLogic` does harden (`hasBlock` wins there). So the **last**
`permissionDecision` written is the one that counts, and a repo-registered session hook running
after Sirdar's overwrites Sirdar's deny with an allow.

Both routes to registering one — the `agent` tool for `.qwen/agents/*.md`, the `skill` tool for
a project skill — are on Sirdar's exclusion list, and both are additionally dead in an
untrusted folder. Read from the bundle, not run: making the real CLI spawn a subagent needs a
model that calls it.

### `~/.qwen/settings.json` hooks displace the system layer's

`loadCliConfig` receives `{userHooks: settings.getUserHooks(), projectHooks:
settings.getProjectHooks()}`, and `LoadedSettings.getUserHooks()` returns
`this.user.settings.hooks` — the **user scope alone**, not the merge. Config then stores
`userHooks = hooksConfig?.userHooks ?? settings.hooks`. So with no user-scope hooks the merged
settings (carrying the system layer's, which is Sirdar's) are used and the hook is registered
ungated; with user-scope hooks present, Sirdar's hook is only reachable through
`Config.getProjectHooks()`, which is `this.projectHooks ?? this.hooks` behind an
`isTrustedFolder()` gate.

Measured, same harness, with `$HOME/.qwen/settings.json` =
`{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"true"}]}]}}`:

- untrusted: **no hook post at all**, and the `read_file` call ran and returned the file — an
  unmediated session.
- trusted: the hook was posted as usual (the project-slot fallback).

Sirdar refuses to start a session while that file registers hooks, rather than run one whose
mediator may not be there.

### The host-arbitrated channel: a `PreToolUse` hook

Hooks *are* consulted in headless mode, for every tool call that survives the deny list, and
they can allow or deny with a reason. That is the per-call mediator Sirdar's
`PermissionPolicy.Decide` plugs into. Hooks come from a settings file; Sirdar writes a private
one, mode 0600, and points the child at it with **`QWEN_CODE_SYSTEM_SETTINGS_PATH`**. That
replaces the system layer only — the user's and the workspace's files are still merged on top,
which is why the guarantees live on the command line and this file only carries the hook:

```json
{
  "hooks": {
    "PreToolUse": [
      { "matcher": "*",
        "hooks": [{ "type": "http", "url": "http://127.0.0.1:54321/decide/<64 hex chars>", "timeout": 15, "name": "sirdar-policy" }] }
    ]
  },
  "permissions": { "deny": ["write_file", "edit", "notebook_edit"] },
  "security": { "folderTrust": { "enabled": true } }
}
```

The last path segment is a 32-byte random per-session token. Without it the endpoint is
unauthenticated on a port any local process — or any page the operator has open — can reach,
which would let anything forge `EvPermission` records into the run's event log or flood the
endpoint until a genuine decision missed the 15 s timeout and failed open. Sirdar compares it
with `subtle.ConstantTimeCompare` and additionally requires `POST` with
`Content-Type: application/json` and no `Origin` header; a request that fails any of those gets
a bare 401 or 415, no decision payload and no event. Reading the bundle's HTTP hook executor
confirms the CLI's own request matches — `fetch(url, {method:"POST", headers:{"Content-Type":
"application/json"}, redirect:"manual"})`, a server-side fetch that sends no `Origin` — and a
live run against the real binary confirmed it on the wire.

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

The two ids are not interchangeable. **`tool_call_id` is the one the stdout stream uses** — it
is the `id` of the assistant line's `tool_use` block and the `tool_use_id` of the matching
`tool_result` block — while the payload's own `tool_use_id` (`toolu_<millis>_<rand>`) is the
CLI's internal handle and appears nowhere on stdout. Sirdar records both when it answers a
call, and reconciles on the first, which is what lets it report a tool result that arrived with
no decision behind it.

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
treated as a non-blocking hook failure and the call proceeds. The bundle's executor confirms
it: every one of those three branches returns `{success: true, output: {continue: true}}`.

So the hook is a mediator, not a sandbox, and the static guarantee is `--exclude-tools`. What
the adapter does about the fail-open itself:

- **Probe before handing the session back.** After `cmd.Start`, Sirdar posts to a `/probe/<token>`
  route on the same listener, mux and token, and fails the session outright if it does not get
  a 204. It is a separate route so the probe judges nothing and leaves no permission record.
- **Watch for the listener dying.** If `http.Server.Serve` returns before the process has been
  reaped, and it was not Sirdar that closed it, the child's whole process group is SIGKILLed —
  the child is started with `Setpgid`, so anything it spawned goes too — and the run ends with
  an `error` event and a `Result.ExitErr` saying the permission hook stopped serving. Half a
  run with no mediator is not a degraded run, it is an unmediated one.
- **Never let a slow consumer become an allow.** The decision is written to the
  `ResponseWriter` and explicitly `Flush`ed before the `EvPermission` event is published,
  because `net/http` buffers a handler's response until it returns and publishing an event can
  block on the event channel. Emitting first — which is what keeps a permission event ahead of
  the `tool_result` line it belongs to — would mean a full 64-slot channel could hold the
  decision past the 15 s hook timeout, and a deny that arrives late is an allow.
- **Report a bypass.** Nothing on stdout says whether the hook was consulted. Sirdar matches
  each `tool_result` against the decisions it made, keyed on `tool_call_id`, and emits an
  `error` event reading `tool started without a Sirdar decision` for a call that succeeded
  without one. Errored results are passed over: an excluded tool is refused by the CLI before
  any hook is consulted, and those refusals arrive as errored results.

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

The canonical list is `ToolNames` in `packages/core/src/tools/tool-names.ts` (49 entries in
0.23.3), with `ToolNamesMigration` resolving three legacy names: `search_file_content` →
`grep_search`, `replace` → `edit`, `task` → `agent`. Sirdar copies that list into
`qwenCoreTools` and excludes all of it bar eleven read tools and the conditional shell, so the
flag reads as a whitelist: a core tool is either judged by the policy under a name it
understands, or never registered. A tool a later Qwen adds is on neither list, which leaves it
registered and refused by the hook — the safe direction, and the reason a version bump should
diff `ToolNames` against `qwenCoreTools`.

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

**The allow-list narrows by name and by nothing else.** `allowedMcpServers` is a
`Set<string>` of names checked against the merged map's keys; it carries no notion of where a
definition came from. So an operator-configured server that happens to share a name with one
the workspace's `.mcp.json` declares passes the filter, and which of the two definitions
survives `assembleMcpServers` is the merge's business. `mcp.workspaceOnly` therefore means
"only these *names*", not "only these *servers*" — the one gap in the restriction, and the
reason workspace server names should be distinctive.

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
  behaviour under a schema are all synthetic. The settings-layer and `--exclude-tools`
  measurements above were taken the same way, against the real 0.23.3 binary with a stub
  endpoint: what they establish is which tools the CLI registers and offers, and whether the
  hook is called, neither of which depends on the model.
- The probe, the mid-run listener death and the abort on an unmediated tool call are exercised
  against the scripted fake binary in `internal/provider/qwen/qwen_test.go`, not against the
  real CLI.
- A repository-supplied subagent or project skill actually registering its `hooks:` block and
  overwriting a decision. The mechanism is read from the 0.23.3 bundle (source priority, the
  session-hook append, `mergeWithOrLogic`'s assignment); making the real CLI spawn a subagent
  needs a model that calls the `agent` tool, which is excluded.
- `--max-wall-time` / `--max-tool-calls` overruns (exit 55) and SIGINT's exit 130.
- MCP tools actually running — the servers in the MCP table never completed a handshake, which
  is enough to prove which ones were assembled but not what a `mcp__server__tool` hook payload
  looks like. The `mcp__` naming is from source.
- `qwen --acp` / `qwen serve`, the ACP bridge and its `MultiClientPermissionMediator`
  (`local-only` policy). That is the other route to a host-decided permission and it is worth
  revisiting if the hook's fail-open behaviour ever becomes a problem — but ACP is rejected
  together with `--json-schema`, so it would cost the structured output.
- `--continue`, `--fork-session`, `--session-id`.
