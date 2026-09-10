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
