# Wire formats, verified on 2026-09-10

Captured against Claude Code 2.1.267 and codex-cli 0.154.0 on macOS by driving both binaries
over stdio from a script. These are the shapes the provider adapters and their fake-binary
tests are built on. Session ids, paths and token counts are illustrative.

## Claude Code, `claude -p` with stream-json in both directions

Command used:

```
claude -p --output-format stream-json --input-format stream-json --verbose \
  --include-partial-messages --permission-prompt-tool stdio --permission-mode default \
  --json-schema '<json schema>' --max-turns 4 --model <model>
```

`ANTHROPIC_API_KEY` removed from the environment so the subscription login is used.

Input line (stdin), one JSON object per line:

```json
{"type":"user","message":{"role":"user","content":"<prompt text>"}}
```

Output lines (stdout), in the order they occurred. `stream_event` lines (partial deltas) are
omitted; they carry `event.type` of `message_start`, `content_block_start`,
`content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`.

```json
{"type":"system","subtype":"hook_started","hook_name":"SessionStart:startup", ...}
{"type":"system","subtype":"hook_response", ... "exit_code":0,"outcome":"success"}
{"type":"system","subtype":"init","cwd":"/work","session_id":"ab02...","tools":["Task","Bash","Read",...],
 "mcp_servers":[...],"model":"claude-haiku-4-5-20251001","permissionMode":"default",
 "apiKeySource":"none","claude_code_version":"2.1.267","output_style":"default", ...}
{"type":"system","subtype":"status","status":"requesting", ...}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":50, ...}
{"type":"assistant","message":{"role":"assistant","model":"...","content":[{"type":"thinking","thinking":"","signature":"..."}],"usage":{...}}, "session_id":"..."}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01Y1...","name":"Bash",
   "input":{"command":"echo hello-from-bash","description":"Run the requested shell command"}}], ...}}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1789049400,"rateLimitType":"five_hour",
   "overageStatus":"rejected","isUsingOverage":false,
   "unifiedWindows":{"five_hour":{"utilization":0.5,"resetsAt":1789049400},"seven_day":{"utilization":0.4,"resetsAt":1789531200}}}, ...}
{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01Y1...","type":"tool_result","content":"hello-from-bash","is_error":false}]},
 "parent_tool_use_id":null,"tool_use_result":{"stdout":"hello-from-bash","stderr":"","interrupted":false}, ...}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01E9...","name":"StructuredOutput","input":{"greeting":"Hello!","n":7}}], ...}}
{"type":"user","message":{"content":[{"tool_use_id":"toolu_01E9...","type":"tool_result","content":"Structured output provided successfully"}]}, ...}
{"type":"result","subtype":"success","is_error":false,"num_turns":3,"session_id":"ab02...",
 "result":"{\"greeting\":\"Hello!\",\"n\":7}","structured_output":{"greeting":"Hello!","n":7},
 "total_cost_usd":0.0698,"duration_ms":9000,"duration_api_ms":7586,"stop_reason":"tool_use",
 "usage":{"input_tokens":18,"output_tokens":516,"cache_creation_input_tokens":32277,"cache_read_input_tokens":26899, ...},
 "modelUsage":{...},"permission_denials":[...],"subagent_stats":{...}, ...}
```

Structured output arrives twice: the model calls a synthetic `StructuredOutput` tool, and the
`result` line carries the parsed object in `structured_output`. Read the latter.

### Permission request (`--permission-prompt-tool stdio`)

When a tool is not already allowed by the user's settings, the CLI writes a control request
and waits:

```json
{"type":"control_request","request_id":"ad91bf9a-...","request":{"subtype":"can_use_tool",
  "tool_name":"Write","display_name":"Write",
  "input":{"file_path":"/work/probe.txt","content":"x"},
  "description":"probe.txt",
  "permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}],
  "tool_use_id":"toolu_016d..."}}
```

For Bash the request also carries `"blocked_path"` and `addRules` suggestions:

```json
{"type":"control_request","request_id":"1ebc...","request":{"subtype":"can_use_tool","tool_name":"Bash",
  "input":{"command":"touch probe2.txt","description":"Create probe2.txt file"},
  "permission_suggestions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"touch probe2.txt"}],"behavior":"allow","destination":"localSettings"}, ...],
  "blocked_path":"/work/probe2.txt","tool_use_id":"toolu_01Sm..."}}
```

Reply on stdin. Deny:

```json
{"type":"control_response","response":{"subtype":"success","request_id":"ad91bf9a-...",
  "response":{"behavior":"deny","message":"Sirdar policy: triage runs are read-only"}}}
```

Allow (optionally with modified input):

```json
{"type":"control_response","response":{"subtype":"success","request_id":"...",
  "response":{"behavior":"allow","updatedInput":{...original or modified input...}}}}
```

After a deny the model sees a `tool_result` with `is_error: true` and the message text, and
continues. Some Bash commands (`echo ...`) were auto-allowed without a request in default mode.
The final `result.permission_denials` lists denials.

### Not exercised yet

`--resume <session_id>` (resume handle is `session_id` from `init`), `control_request` with
other subtypes (hooks, MCP elicitation), sending a second `user` line after `result` in the
same process.

## Codex, `codex app-server` over stdio (JSON-RPC 2.0, one object per line)

Client to server:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"sirdar","title":"Sirdar","version":"0.0.1"}}}
{"jsonrpc":"2.0","method":"initialized"}
{"jsonrpc":"2.0","id":2,"method":"thread/start","params":{"cwd":"/work","sandbox":"read-only","approvalPolicy":"never"}}
{"jsonrpc":"2.0","id":3,"method":"turn/start","params":{"threadId":"01a08b0e-...",
   "input":[{"type":"text","text":"<prompt>"},{"type":"localImage","path":"/work/bundle/attachments/1-shot.png"}],
   "outputSchema":{ ...json schema... }}}
```

Enum values: `sandbox` is `read-only | workspace-write | danger-full-access`; `approvalPolicy`
is `untrusted | on-request | never` (or a granular object). `thread/start` also accepts `model`,
`baseInstructions`, `developerInstructions`, `config`, `ephemeral`. `turn/start` also accepts
`model`, `effort`, `cwd`, `sandboxPolicy`.

Server to client, in order:

```json
{"jsonrpc":"2.0","id":1,"result":{"userAgent":"sirdar/0.154.0 (...)","codexHome":"/Users/x/.codex","platformOs":"macos", ...}}
{"jsonrpc":"2.0","method":"remoteControl/status/changed","params":{...}}
{"jsonrpc":"2.0","id":2,"result":{"thread":{"id":"01a08b0e-...","sessionId":"01a08b0e-...","cwd":"/work", ...},"model":"...","sandbox":{...},"approvalPolicy":"never", ...}}
{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{...}}}
{"jsonrpc":"2.0","method":"mcpServer/startupStatus/updated","params":{"threadId":"...","name":"codex_apps","status":"starting"}}
{"jsonrpc":"2.0","id":3,"result":{"turn":{"id":"01a08b0e-9c04-...","items":[],"status":"inProgress"}}}
{"jsonrpc":"2.0","method":"thread/status/changed","params":{"threadId":"...","status":{"type":"active","activeFlags":[]}}}
{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"...","turn":{"id":"...","status":"inProgress","startedAt":1789039385}}}
{"jsonrpc":"2.0","method":"item/started","params":{"item":{"type":"userMessage","id":"...","content":[{"type":"text","text":"..."}]},"threadId":"...","turnId":"...","startedAtMs":...}}
{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"type":"userMessage", ...}, ...}}
{"jsonrpc":"2.0","method":"item/started","params":{"item":{"type":"reasoning","id":"rs_...","summary":[],"content":[]}, ...}}
{"jsonrpc":"2.0","method":"item/reasoning/summaryTextDelta","params":{...}}
{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"type":"reasoning", ...}, ...}}
{"jsonrpc":"2.0","method":"item/started","params":{"item":{"type":"agentMessage","id":"msg_...","text":"","phase":"commentary"}, ...}}
{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{...}}
{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"type":"agentMessage","id":"msg_...","text":"{\"greeting\":\"...\",\"n\":7}","phase":"commentary"}, ...}}
{"jsonrpc":"2.0","method":"thread/tokenUsage/updated","params":{"threadId":"...","turnId":"...",
   "tokenUsage":{"total":{"totalTokens":17270,"inputTokens":17104,"cachedInputTokens":0,"outputTokens":166,"reasoningOutputTokens":40},"last":{...},"modelContextWindow":...}}}
{"jsonrpc":"2.0","method":"account/rateLimits/updated","params":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":91,"windowDurationMins":43200,"resetsAt":1789819250},"secondary":null,"credits":{...}}}}
{"jsonrpc":"2.0","method":"item/started","params":{"item":{"type":"agentMessage","id":"msg_...","text":"","phase":"final_answer"}, ...}}
{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"type":"agentMessage","id":"msg_...","text":"{\"greeting\":\"Hello! ... writes are not permitted.\",\"n\":7}","phase":"final_answer"}, ...}}
{"jsonrpc":"2.0","method":"thread/status/changed","params":{"threadId":"...","status":{"type":"idle"}}}
{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"...","turn":{"id":"...","status":"completed","error":null,"items":[{"type":"agentMessage","phase":"final_answer","text":"{...}"}, ...],"completedAt":...,"durationMs":...}}}
```

The structured output is the `text` of the `agentMessage` item whose `phase` is
`final_answer` (also present in `turn/completed.turn.items`). Parse it as JSON. Other item
types seen in the schema: `commandExecution` (with `item/commandExecution/outputDelta`),
`fileChange`, `mcpToolCall` (with `item/mcpToolCall/progress`), `webSearch`, `plan`,
`reasoning`, `userMessage`, `hookPrompt`. `Turn.status` is
`completed | interrupted | failed | inProgress`; `Turn.error.message` when failed.

Server-to-client requests (carry an `id`, must be answered):
`item/commandExecution/requestApproval`, `item/fileChange/requestApproval`,
`item/permissions/requestApproval`, `item/tool/requestUserInput`,
`mcpServer/elicitation/request`. With `sandbox: read-only` and `approvalPolicy: never` the
write attempt was refused inside the sandbox and no request was sent; the model reported the
failure in its final answer. Sirdar still answers any request that does arrive with a decline.

Rate-limit note from the probe: the Codex account showed `usedPercent: 91` on a 30-day
window. Keep Codex test runs minimal.

Other useful methods: `thread/resume`, `thread/fork`, `turn/interrupt`, `turn/steer`,
`account/rateLimits/read`, `mcpServerStatus/list`, `model/list`. Bindings:
`codex app-server generate-json-schema --out <dir>` writes one JSON Schema per type plus a
combined `codex_app_server_protocol.v2.schemas.json`.

### Codex MCP servers: which mechanism can restrict a thread (probed 2026-09-11, codex-cli 0.154.0)

Codex reads its MCP servers from `[mcp_servers.<name>]` tables in `config.toml` under
`CODEX_HOME`. Three mechanisms were probed against the installed CLI to find one that gives a
thread the workspace's servers *and no others*. Only the third does. No turn was started in any
of these probes, so none of them spent model quota.

**`-c mcp_servers=...` merges, it does not replace.** With a home whose `config.toml` declares
`notes`, both an empty override and one naming a different server left `notes` in place:

```
$ codex app-server -c 'mcp_servers={}'            → mcpServerStatus/list: codex_apps, notes
$ codex app-server -c 'mcp_servers={swapped=...}' → mcpServerStatus/list: codex_apps, notes, swapped
```

**`thread/start`'s `config` object merges too.** `ThreadStartParams.config` is an untyped
`object|null` in the app-server schema, and an `mcp_servers` key in it is added to the home's
table rather than replacing it. With `notes` in `config.toml`:

```json
{"jsonrpc":"2.0","id":2,"method":"thread/start","params":{"cwd":"/tmp","sandbox":"read-only","approvalPolicy":"never",
  "config":{"mcp_servers":{"injected":{"command":"/usr/bin/python3","args":["<fake-mcp>"],"env":{"FAKE_MCP_NAME":"injected"}}}}}}
{"jsonrpc":"2.0","id":2,"result":{"thread":{"id":"01a08f9f-…","modelProvider":"openai","model":"gpt-5.6-luna", …}}}
```

then `mcpServerStatus/list` with that `threadId` answers `codex_apps, injected, notes` — the
injected server is live, the home's server is still there. So the `config` object can add a
server to a thread but can never take the operator's away.

**A generated `CODEX_HOME` does what is wanted.** A directory holding a `config.toml` that
declares only the workspace's servers, a copy of the operator's `auth.json`, and symlinks to
every other entry of their real home:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"sirdar","title":"Sirdar","version":"0.1.0-dev"}}}
{"jsonrpc":"2.0","id":1,"result":{"userAgent":"sirdar/0.154.0 (Mac OS 26.6.2; arm64) …","codexHome":"/private/var/folders/…/sirdar-codex-home-…","platformFamily":"unix","platformOs":"macos"}}
{"jsonrpc":"2.0","method":"initialized","params":{}}
{"jsonrpc":"2.0","id":2,"method":"mcpServerStatus/list","params":{}}
{"jsonrpc":"2.0","id":2,"result":{"data":[
  {"name":"codex_apps","authStatus":"bearerToken","serverInfo":{"name":"plugin-runtime","version":"0.1.0"},"tools":{"codex_document_control.list_document_sessions":{…}, …},"toolsError":null},
  {"name":"notes","authStatus":"unsupported","serverInfo":{"name":"notes","version":"1.0.0"},"tools":{"notes_lookup":{…}},"toolsError":null},
  {"name":"timeteller","authStatus":"unsupported","serverInfo":{"name":"timeteller","version":"1.0.0"},"tools":{"timeteller_lookup":{…}},"toolsError":null}],
  "nextCursor":null}}
```

Notes on the shapes and the caveats, all observed rather than assumed:

- `mcpServerStatus/list` answers `{"data":[…],"nextCursor":…}` in 0.154.0, not `{"servers":…}`.
  Each entry carries `name`, `authStatus`, `runtimeStatus`, `serverInfo`, a `tools` map and
  `toolsError`; `ListMcpServerStatusParams` also takes `threadId`, `detail`, `limit`, `cursor`.
- `codex_apps` (`serverInfo.name` `plugin-runtime`) is listed even in a home with an empty
  `config.toml` and no `plugins` directory. It is built into the CLI; nothing in the config
  removes it.
- The login travels in `auth.json` alone: `CODEX_HOME=<scratch> codex login status` says
  `Logged in using ChatGPT` with the file copied in and `Not logged in` without it. The file
  carries `auth_mode`, `tokens.{id_token,access_token,refresh_token,account_id}` and
  `last_refresh` — Codex rewrites it when it refreshes, so the copy must stay writable.
- A server whose command fails to start is still listed, with its `toolsError`, e.g.
  `MCP startup failed: handshaking with MCP server failed: connection closed: initialize
  response`. `RUST_LOG=codex_rmcp_client=trace` puts the child's own stderr in the app-server's
  (`MCP server stderr (/usr/bin/python3): … No such file or directory`), which is how a bad
  `command` or `args` in `.mcp.json` is diagnosed.
- `thread/resume` reads the thread's rollout from `CODEX_HOME/sessions`: resuming an id from a
  home that never held it fails with `{"code":-32600,"message":"no rollout found for thread id
  <id>"}`. That is why Sirdar symlinks the real home's state into the generated one instead of
  leaving it empty. (An empty thread is not written to `sessions` at all, so the probe could
  not tell a missing rollout from an unwritten one without spending a turn.)

### Codex approvals: what gates an MCP tool call (probed 2026-09-11, codex-cli 0.154.0)

Sirdar ran Codex with `approvalPolicy: "never"` on the reading that the read-only sandbox was
the whole confinement and no approval would ever arrive. For MCP tool calls that reading was
wrong in both directions: `never` does not mean "runs ungated", it means **refused**, and the
policy that does gate them routes the approval somewhere the adapter was not looking.

Two turns were spent establishing this, against a scratch `CODEX_HOME` whose `config.toml`
declared one stdio MCP server — a Python stub exposing `delete_everything` with no
`readOnlyHint` annotation — in a throwaway `cwd`. Same prompt both times: *Call the MCP tool
delete_everything on the server named probe, with an empty arguments object.*

**`approvalPolicy: "never"` refuses the call.** No server-to-client request of any kind; the
item completes as failed:

```json
{"method":"item/completed","params":{"item":{"type":"mcpToolCall","id":"exec-54932ee5-…",
  "server":"probe","tool":"delete_everything","status":"failed","arguments":{},
  "readOnlyHint":null,"result":null,
  "error":{"message":"MCP tool call requires approval, but approval policy is never"}}, …}}
```

So a workspace `.mcp.json` attached to a Codex session was being started, listed, offered to
the model — and then unusable. The string is in the binary next to `core/src/mcp_tool_call.rs`
and `mcp_tool_call_approval`, alongside `internal error: … only MCP actions can request MCP
tool approval`, which is also why a client-initiated `mcpServer/tool/call` runs under every
policy: probed directly, `never`, `on-request` and `untrusted` all returned the tool's result
without asking. Only the *model's* call is gated.

**`approvalPolicy: "untrusted"` asks — on the elicitation channel.** The approval does not
arrive as `item/permissions/requestApproval` (whose params are a filesystem/network profile,
with no tool in them) and there is no `item/mcpToolCall/requestApproval` in `ServerRequest` at
all. It arrives as an `mcpServer/elicitation/request` marked by `_meta`:

```json
{"method":"mcpServer/elicitation/request","id":0,"params":{
  "threadId":"01a08fb9-8f31-…","turnId":"01a08fb9-8f93-…","serverName":"probe","mode":"form",
  "_meta":{"codex_approval_kind":"mcp_tool_call","persist":["session","always"],
           "tool_description":"Deletes every record in the probe store. Destructive.",
           "tool_params":{},"tool_params_display":[]},
  "message":"Allow the probe MCP server to run tool \"delete_everything\"?",
  "requestedSchema":{"type":"object","properties":{}}}}
```

Answered `{"action":"decline"}` (the response is `{action, content?}`, actions
`accept | decline | cancel`), the call completes as `failed` with `user rejected MCP tool
call`, and the turn carries on. Ordering matters and is reliable: the `item/started` for the
`mcpToolCall` — which carries `server`, `tool` and `arguments` as *data* — precedes the
elicitation, then `serverRequest/resolved` follows the reply.

```
26: item/started            mcpToolCall exec-2d15e1cc… server=probe tool=delete_everything
28: mcpServer/elicitation/request id=0
30: serverRequest/resolved  requestId=0
32: item/completed          mcpToolCall … status=failed
```

That ordering is what Sirdar decides on: the elicitation names the server in a field but the
tool only inside a sentence written for a person, so the preceding item supplies the tool name
and the quoted name in `message` is the fallback.

Notes and limits:

- **`on-request` was not turn-verified.** The `never` branch is a special case in the refusal
  string, so any other policy plausibly asks; `untrusted` is what was actually observed, so
  that is what Sirdar sends. `untrusted` also puts shell commands up for approval, which is
  the point — `permissions.bash` had no way to reach a Codex session before this.
- **`approvalPolicy: {"granular":{…}}`** would be the narrower instrument
  (`mcp_elicitations`, `sandbox_approval`, `rules`, `request_permissions`, `skill_approval`),
  but the app-server rejects it without an opt-in: `askForApproval.granular requires
  experimentalApi capability`, declared in `initialize.capabilities.experimentalApi`. Not a
  thing to build a permission gate on while it is flagged experimental.
- **What Codex auto-approves never reaches Sirdar.** A tool the CLI decides needs no approval
  raises no request, so `permissions.mcp` never sees it. The stub's tool carried no
  `readOnlyHint`; whether an annotation a *server* supplies can win itself an exemption was
  not established, and the annotation would be the untrusted party's own claim. Worth a probe
  before trusting the gate with a server nobody has read.
- 0.154.0 also has hooks (`hooks/list`, `HookEventName` including `preToolUse` and
  `permissionRequest`, `HookRunStatus` `blocked`). A `preToolUse` hook is a second, policy-
  independent way to gate tool calls and does not depend on the approval policy at all. Not
  used here; noted as the thing to reach for if the approval channel proves porous.

### Codex and the MCP child's environment (probed 2026-09-11, codex-cli 0.154.0, no turn)

The worry was that Codex hands its MCP servers the app-server's whole inherited environment,
where Sirdar's own `mcpclient` gives them PATH, HOME and LANG and nothing else — so the same
`.mcp.json` would expose a workspace's credentials on the Codex path and not on the openai
one. It does not. Probed by declaring two servers whose `tools/list` names one tool per
variable it can see (`user__present`, `zoho_refresh_token__absent`, …) and reading the answer
off `mcpServerStatus/list`, with the app-server itself started with every variable set:

| Variable in the app-server's env | Reaches the MCP child |
|---|---|
| `PATH`, `HOME`, `LANG`, `LOGNAME`, `SHELL`, `TERM`, `TMPDIR`, `USER` | yes |
| `ZOHO_REFRESH_TOKEN`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `SIRDAR_BILLING`, `PWD`, `PYTHONPATH`, `NODE_PATH`, and every other name tried | no |

And an `[mcp_servers.<name>.env]` table **merges** with that core set rather than replacing
it: the server declaring `PROBE_DECLARED` saw it *and* still saw PATH, HOME and LANG, and
still did not see the parent's other variables.

So Codex filters by the same principle `mcpclient.childEnv` does, over a slightly wider core
set — the difference being `LOGNAME`, `SHELL`, `TERM`, `TMPDIR` and `USER`, which a server
started by the openai loop does not get and one started by Codex does. No credential crosses
either way. Nothing is emitted into the generated `config.toml` to change this; the difference
is documented in `docs/config.md` instead.

A side note from the same probes: a `CODEX_HOME` that symlinks the real home's `sessions` is
enough for `thread/resume` — a thread started in a generated home resumed cleanly in a second
process against the same home, and `thread/items/list` returned its items. Codex also creates
its own state files in whatever `CODEX_HOME` it is given (`state_*.sqlite`, `logs_*.sqlite`,
`queue_*.sqlite`, `shell_snapshots/`, `tmp/`, `thread-writer-locks/`), which in Sirdar's
generated home are symlinks to the operator's, so those writes land in the real files.
