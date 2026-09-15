# Cursor CLI wire formats, verified on 2026-09-15

Captured against **Cursor Agent CLI `2026.09.10-fd3934a`** (`cursor-agent`, also installed as
`agent`) on macOS, driving the real binary against the owner's **Free** account. Every shape
below was **exercised live** unless a line says otherwise; where a claim comes from reading the
CLI's own JavaScript bundle rather than from a turn, the line says "read off the bundle".

Seven live turns were spent, all trivial prompts, because the account is on a free plan whose
quota is not published. Session ids, request ids and token counts are illustrative.

Companion to `docs/research/06-wire-formats.md` (Claude Code, Codex) and
`docs/research/09-qwen-wire-formats.md` (Qwen Code).

## The command Sirdar runs

```
cursor-agent -p --output-format stream-json \
  --model auto \
  --mode ask|plan \
  --trust \
  --sandbox enabled \
  --disable-project-configs \
  --exclude-tools edit_tool_call --exclude-tools delete_tool_call \
  --exclude-tools shell_tool_call --exclude-tools write_shell_stdin_tool_call \
  --exclude-tools apply_agent_diff_tool_call --exclude-tools switch_mode_tool_call \
  [--resume <chatId>] \
  '<prompt>'
```

The prompt is a **positional argument**. There is no stdin protocol: `--input-format` does not
exist, so a session takes exactly one user message and `Session.Send` cannot work. The schema
retry goes through `--resume <chatId>` in a fresh process, the way the Qwen adapter does it.

### `--trust` is not optional

Without it, a directory the operator has never opened interactively fails **before any API
call**, exit 1, nothing on stdout:

```
⚠ Workspace Trust Required

  Cursor Agent can execute code and access files in this directory.
  ...
  To proceed, you can either:
    • Run 'agent' interactively to decide
    • Pass --trust, --yolo, or -f if you trust this directory
```

Trust is persisted per directory, so a machine where the operator has already used Cursor in
that repository will not show this — which is exactly why the flag has to be passed
unconditionally rather than relied on being unnecessary.

### The free plan can only use Auto

`--list-models` lists 200+ ids (see below), and **every named one is refused** on a free
account. `--model gpt-5.4-nano-low` produced, on stderr, exit 1:

```
ActionRequiredError: Named models unavailable Free plans can only use Auto. Switch to Auto or upgrade plans to continue.
```

stdout carried the `system/init` and `user` lines and then stopped — **no `result` line**. So a
model the account cannot use is an exit-1 session with a truncated stream, and the adapter has
to report the exit code and the stderr tail rather than wait for a final event. `sirdar doctor`
gets a row for this.

## Models

`cursor-agent --list-models` on this account listed 208 entries. `auto` is the default and the
only one a free plan may select. The families, for the record:

- **Composer** (Cursor's own): `composer-2.5`, `composer-2.5-fast`.
- **Grok**: `cursor-grok-4.5-{low,medium,high}[-fast]`, `cursor-grok-4.6-{low,medium,high,xhigh}[-fast]`.
- OpenAI: `gpt-5.1`…`gpt-5.6` families (`-sol`, `-luna`, `-terra` variants), `gpt-5.3-codex*`,
  `gpt-5.4-{mini,nano}-*`, `gpt-5-mini`.
- Anthropic: `claude-opus-4-7/4-8/5-*`, `claude-sonnet-4/4.5/5-*`, `claude-fable-5*` (marked
  "NO ZDR").
- Google: `gemini-3.x-flash-*`, `gemini-3.1-pro`. Others: `kimi-k3-*`, `glm-5.2-*`,
  `muse-spark-1.3-*`.

Cheapest plausible ids for a paid account are `gpt-5.4-nano-none/-low`, `gemini-3.6-flash-minimal`
and `composer-2.5-fast`; none could be exercised here. Parameterised ids accept bracket
overrides, e.g. `'claude-opus-4-8[context=1m,effort=high,fast=false]'`.

## Output lines (stdout), in the order they occurred

A complete successful `--mode ask` turn, verbatim but reflowed:

```json
{"type":"system","subtype":"init","apiKeySource":"login","cwd":"/work",
 "session_id":"f7d05f71-…","model":"Auto","permissionMode":"default"}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"reply with the word ok"}]},
 "session_id":"f7d05f71-…"}
{"type":"thinking","subtype":"delta","text":"Replying with \"ok\".","session_id":"f7d05f71-…","timestamp_ms":1789467005371}
{"type":"thinking","subtype":"completed","session_id":"f7d05f71-…","timestamp_ms":1789467005373}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]},"session_id":"f7d05f71-…"}
{"type":"result","subtype":"success","duration_ms":3697,"duration_api_ms":3697,"is_error":false,
 "result":"ok","session_id":"f7d05f71-…","request_id":"0c3d2dbe-…",
 "usage":{"inputTokens":13112,"outputTokens":28,"cacheReadTokens":7808,"cacheWriteTokens":0}}
```

A tool call adds two lines per call:

```json
{"type":"tool_call","subtype":"started","call_id":"call-973a…-0\nfc_8b6e…_0",
 "tool_call":{"editToolCall":{"args":{"path":"/work/a.txt","streamContent":"hello\n"}},
   "hookAdditionalContexts":[],"toolCallId":"call-973a…-0\nfc_8b6e…_0","startedAtMs":"1789467333873"},
 "model_call_id":"0e3f2613-…-0-97fl","session_id":"60c30226-…","timestamp_ms":1789467334031}
{"type":"tool_call","subtype":"completed","call_id":"call-973a…-0\nfc_8b6e…_0",
 "tool_call":{"editToolCall":{"args":{…},"result":{"success":{"path":"/work/a.txt","linesAdded":1,
   "linesRemoved":0,"diffString":"--- /dev/null\n+++ b//work/a.txt\n@@ -1,0 +1 @@\n+hello",
   "afterFullFileContent":"hello\n","message":"Wrote contents to /work/a.txt"}}},
   "toolCallId":"…","startedAtMs":"…","completedAtMs":"…"}, …}
```

A shell call carries a parsed command and the sandbox policy that was requested:

```json
{"type":"tool_call","subtype":"started","tool_call":{"shellToolCall":{"args":{
  "command":"touch x","workingDirectory":"","timeout":30000,"simpleCommands":["touch"],
  "hasInputRedirect":false,"hasOutputRedirect":false,
  "parsingResult":{"parsingFailed":false,"executableCommands":[{"name":"touch",
     "args":[{"type":"word","value":"x"}],"fullText":"touch x"}],
     "hasRedirects":false,"hasCommandSubstitution":false,"redirects":[]},
  "requestedSandboxPolicy":{"type":"TYPE_WORKSPACE_READWRITE","networkAccess":false,
     "additionalReadwritePaths":["/work"],"additionalReadonlyPaths":[],
     "enableSharedBuildCache":true,"readBoundary":"READ_BOUNDARY_MODE_UNSPECIFIED","additionalReadPaths":[]},
  "fileOutputThresholdBytes":"40000","isBackground":false,"skipApproval":false,
  "timeoutBehavior":"TIMEOUT_BEHAVIOR_BACKGROUND","hardTimeout":86400000}}, …}
```

A refused call reports the refusal in the *completed* line's result:

```json
"result":{"rejected":{"command":"touch x","workingDirectory":"",
  "reason":"refused by sirdar spike\n\nAgent note: Do not suggest workarounds to the blocked tool.",
  "isReadonly":false}}
```

### How this differs from Claude Code's stream-json, line by line

| Claude Code | Cursor | Consequence for the adapter |
|---|---|---|
| `system/init` with `session_id` | same, plus `apiKeySource`, `model` (display name), `permissionMode` | `apiKeySource` is `"login"` or the API-key source; it is the only on-the-wire proof of which credential paid |
| one `assistant` line per content block | one `assistant` line carrying the whole message | no per-block assembly needed |
| thinking arrives as `assistant` content blocks | its own `type:"thinking"` with `subtype:"delta"`/`"completed"` | thinking is always streamed, with or without `--stream-partial-output` |
| `tool_use` / `tool_result` inside message content | top-level `type:"tool_call"`, `subtype:"started"`/`"completed"` | the tool's identity is the **key** of the `tool_call` object (`editToolCall`, `shellToolCall`, `readToolCall`, …), not a `name` field |
| `result` with `num_turns`, `total_cost_usd`, `structured_output` | `result` with `duration_ms`, `is_error`, `result` (text), `usage` | **no turn count, no cost, no structured output** — see Budgets and Structured output |
| `control_request` permission channel | none | no per-call mediation on the command line; see Permissions |
| `rate_limit_event` line | none observed | a quota refusal surfaces as an `ActionRequiredError` on stderr with exit 1 |

`call_id` and `toolCallId` contain a **literal newline** (`call-<uuid>-0\nfc_<hex>_0`). They are
opaque identifiers and must not be printed raw into a log line or a note.

### `--stream-partial-output` is a trap

With the flag, each token becomes its own `assistant` line **of the same shape as the complete
one**, and the complete message is emitted as well at the end. There is no `subtype` to tell
them apart; the only difference is that partial lines carry `timestamp_ms` and the final one
does not. A parser that emits assistant text per line therefore duplicates the whole answer
dozens of times. Sirdar does not pass the flag: without it the `assistant` line is already
complete, and `thinking` deltas arrive either way.

## Execution modes

`--mode ask` and `--mode plan` (`--plan` is shorthand) are described by the CLI's own help as
"read-only". Read off the bundle, the mode becomes the request's `unified_mode` enum, so the
restriction is applied **by Cursor's backend**, not by the local process; `permissionMode` on
the `init` line stays `"default"` in every mode, because that field reports the *approval*
posture, not the execution mode.

Live, in a temp git repo, prompt "Create a file named a.txt containing the word hello, then run
the shell command: touch x":

| Mode | Outcome | Evidence |
|---|---|---|
| `--mode ask` | **refused**, no tool call attempted, no file created | the model answered "I'm in **Ask mode**, so I can't create files or run shell commands that change the system." Exit 0 |
| `--mode plan` | not exercised (see "What was not exercised") | — |
| default (no `--mode`) | **done** — `a.txt` written and `x` created | two `tool_call` lines, both `subtype:"completed"` with `success` results |

The ask-mode refusal is the model declining, not a tool being withheld: nothing on the stream
shows a denied call, because no call was made. That makes ask mode a **server-side, advisory**
guarantee. It held on the one turn that tested it; it is not the kind of guarantee
`--disallowedTools` or `--exclude-tools <every write tool>` is for Claude and Qwen.

### Print mode auto-approves everything

This is the finding that shapes the whole adapter. `-p`'s own help says it: "Has access to all
tools, including write and shell." The default-mode turn above wrote a file and ran a shell
command with **no approval prompt on the stream, no permission event, and no TTY to answer
one** — `skipApproval` was `false` on the shell call and it ran anyway. There is nothing to
answer and nothing to refuse: in print mode the CLI is its own approver.

So `--force` / `--yolo` ("Force allow commands unless explicitly denied") changes nothing that
matters for a print-mode session's writes; read off the bundle it sets a `force` flag consulted
by the interactive approval UI and by deny-rule matching. Sirdar passes it only in fix mode, and
only to say plainly that the session is meant to write.

### `--sandbox enabled`

Sets the `requestedSandboxPolicy` carried on every shell call:
`TYPE_WORKSPACE_READWRITE`, `networkAccess: false`, `additionalReadwritePaths: [<cwd>]`. So the
sandbox does two useful things for a **shell** command — confines its writes to the workspace
and cuts its network — and nothing at all for the **edit tool**, which wrote `/work/a.txt`
through the same session. The edit tool takes an absolute path from the model and the sandbox
policy is not consulted for it.

Read off the bundle, the sandbox is a macOS Seatbelt / Linux equivalent gated on
`isSandboxSupported()`, and `~/.cursor/sandbox.json` plus `.cursor/sandbox.json` /
`.cursor/sandbox-policies` can widen it. `--disable-project-configs` (hidden flag, verified
accepted) keeps the repository's own copies out of the decision.

### There *is* a per-call mediation channel: hooks

Cursor CLI supports hooks, and **they fire in `-p` print mode and their `deny` is honoured**.
This was exercised live and is the single most important correction to the assumption that
Cursor permissions cannot be mediated per call.

Configuration is `<workspace>/.cursor/hooks.json` (project), `~/.cursor/hooks.json` (user),
`/Library/Application Support/Cursor/hooks.json` (enterprise, macOS) and a team directory; read
off the bundle, `~/.claude/settings.json`, `<workspace>/.claude/settings.json` and
`.claude/settings.local.json` are **also** read, so a Claude Code hook already on the machine
reaches a Cursor session. Shape:

```json
{"version":1,"hooks":{"preToolUse":[{"command":"/path/to/hook","failClosed":true}]}}
```

The hook steps the bundle names: `sessionStart`, `sessionEnd`, `stop`, `beforeSubmitPrompt`,
`preToolUse`, `postToolUse`, `postToolUseFailure`, `beforeShellExecution`,
`afterShellExecution`, `beforeMCPExecution`, `afterMCPExecution`, `beforeReadFile`,
`afterFileEdit`, `afterAgentThought`, `afterAgentResponse`, `preCompact`. There is no
`beforeFileEdit` — an edit is mediated through the generic `preToolUse` step.

A command hook is spawned with the JSON request on **stdin** and answers with JSON on
**stdout**. The two `preToolUse` requests captured live, verbatim:

```json
{"conversation_id":"8194a1ff-…","generation_id":"8194a1ff-…","model":"default",
 "tool_name":"Read","tool_input":{"file_path":"/work/a.txt"},
 "tool_use_id":"call-bac41ffd-…-0\nfc_e011…_0","session_id":"8194a1ff-…",
 "hook_event_name":"preToolUse","cursor_version":"2026.09.10-fd3934a",
 "workspace_roots":["/work"],"user_email":"…","transcript_path":null}
{"conversation_id":"8194a1ff-…","generation_id":"8194a1ff-…","model":"default",
 "tool_name":"Shell","tool_input":{"command":"touch x","cwd":"","timeout":30000},
 "tool_use_id":"506e8f2f-…","cwd":"","session_id":"8194a1ff-…",
 "hook_event_name":"preToolUse", …}
```

Answering `{"permission":"deny","user_message":"refused by sirdar spike"}` to both left the
directory untouched — no `a.txt`, no `x` — and both calls came back on the stream as
`"result":{"rejected":{…,"reason":"refused by sirdar spike\n\nAgent note: Do not suggest
workarounds to the blocked tool."}}`. `permission` accepts `"allow"`, `"deny"` and `"ask"`;
`user_message` and `additional_context` (max 10 000 chars) ride along.

Two details a future implementation must not miss:

- The **edit** call's `preToolUse` request arrived with `tool_name:"Read"` and a `file_path`,
  carrying the edit call's own `tool_use_id`. Denying it rejected the whole `editToolCall`. A
  policy keyed on `tool_name` alone would read that request as a harmless read; the write
  intent is visible on the stream's `tool_call` line, not in the hook payload.
- `failClosed: true` is required, and is **not** the default. Read off the bundle, a hook that
  times out, fails to spawn, or answers unparseable JSON blocks the tool only when
  `failClosed` is set; otherwise the tool runs.

Sirdar does **not** use this yet, for one reason: the only place the hook config can be put is
a `.cursor/hooks.json` under a path Sirdar does not own — the workspace root for a triage run —
and writing into the operator's repository is what the read-only guarantee exists to prevent.
There is no environment variable that relocates the hooks file (`CURSOR_DATA_DIR` moves
`~/.cursor` for *state*, but the hooks user path is built from `homedir()` directly, read off
the bundle). Closing that gap is the provider's main open item; see `docs/config.md`.

### `--allowed-tools` / `--exclude-tools` are HTTP headers

Both flags exist, are accepted, and are hidden from `--help`. Read off the bundle they are
turned into the request headers `x-cursor-agent-allowed-tools` and
`x-cursor-agent-exclude-tools` (comma-joined) and sent to `https://api2.cursor.sh` — so, like
`--mode`, the enforcement is the backend's. `-H/--header` could set the same headers by hand.

The accepted names are the `oneof tool` field names of `agent.v1.ToolCall`, read off the
bundle's generated protobuf. The write-capable ones, which Sirdar excludes on every read-only
session:

```
edit_tool_call  delete_tool_call  shell_tool_call  write_shell_stdin_tool_call
apply_agent_diff_tool_call  switch_mode_tool_call
```

`switch_mode_tool_call` is on the list because the model has a tool for changing its own
execution mode (`aiserver.v1.SwitchModeParams` carries `from_mode_id`/`to_mode_id` and the
result carries `auto_approved`), which is the one path by which an ask-mode session could stop
being one.

The read tools left registered: `read_tool_call`, `grep_tool_call`, `glob_tool_call`,
`ls_tool_call`, `sem_search_tool_call`, `read_lints_tool_call`, `blame_by_file_path_tool_call`,
`web_search_tool_call`, `web_fetch_tool_call`, `fetch_tool_call`, `mcp_tool_call`,
`list_mcp_resources_tool_call`, `read_mcp_resource_tool_call`, `get_mcp_tools_tool_call`,
`update_todos_tool_call`, `read_todos_tool_call`, `ask_question_tool_call`.

An invalid name is a fatal argument error only when the `agent_cli_exclude_tools` feature flag
is on for the account; otherwise the whole list is silently dropped (read off the bundle). A
valid list is always applied. Sirdar therefore only ever passes names taken from the list above.

## Structured output

There is **no** `--json-schema` flag and no structured-output field on the `result` line. The
schema goes in the prompt and the answer comes back as text.

`--output-format json` returns the `result` object and nothing else:

```json
{"type":"result","subtype":"success","is_error":false,"duration_ms":32193,"duration_api_ms":32193,
 "result":"{\"verdict\":\"ok\",\"confidence\":1}","session_id":"008be211-…","request_id":"cf7a1d0b-…",
 "usage":{"inputTokens":15809,"outputTokens":45,"cacheReadTokens":7808,"cacheWriteTokens":0}}
```

Prompted with *"Answer ONLY with a JSON object, no prose and no code fence, matching this
schema: {…}"*, the model returned exactly `{"verdict":"ok","confidence":1}` — parseable on the
first try, once. One success is not reliability: the adapter parses `result` leniently (trimming
a ``` fence and any prose around the outermost JSON object) and treats a parse failure or a
schema mismatch as the retry trigger, rather than assuming the model obeys.

`--output-format stream-json` carries the same text on the `result` line's `result` field, so
the adapter reads the final answer from one place in both formats.

## Resume

`--resume <chatId>` continues a session: the retry turn came back with the **same**
`session_id`, and the model had the prior exchange in context (its answer acknowledged the
correction). That is the schema-retry path, since there is no stdin channel to send a second
user message on.

```
cursor-agent -p --output-format json --mode ask --model auto --trust \
  --resume 008be211-0c74-4d05-a518-d55919e16378 '<retry instruction>'
```

`--continue` resumes the latest session with no id, and `create-chat` mints an empty chat id
ahead of time. `--new-session-id <uuid>` (hidden) lets the caller choose the id; not exercised.

## MCP servers

Discovery is `~/.cursor/mcp.json` merged with `<projectRoot>/.cursor/mcp.json`, project winning
a name collision — read off the bundle, and consistent with `cursor-agent mcp list`, which said
`No MCP servers configured (expected in .cursor/mcp.json or ~/.cursor/mcp.json)` on this
machine.

**There is no flag that restricts a session to the project's servers.** The user layer is
always merged; `--disable-project-configs` removes the *project* layer, which is the wrong half.
`cursor-agent mcp disable <id>` writes a persistent disabled-list for the operator's own
machine, which is a configuration change Sirdar must not make on their behalf. So
`mcp.workspaceOnly` — which the Claude, Codex and Qwen adapters all honour — **cannot** be
honoured here. `sirdar doctor` says so rather than implying a restriction that is not there.

A project-configured server also needs an approval before it loads: the identity of a
`.cursor/mcp.json` server is hashed into an approvals file, and an unapproved one reports
`status: "unapproved"`. `--approve-mcps` approves every configured server for the session, which
is the opposite of what a read-only run wants; Sirdar never passes it.

## Environment

| Variable | What it does | Sirdar |
|---|---|---|
| `CURSOR_API_KEY` | API-key auth instead of the login | **stripped** unless the workspace configured a key; `apiKeySource` on the `init` line reports which was used |
| `CURSOR_API_ENDPOINT` | equivalent to `-e/--endpoint`, default `https://api2.cursor.sh` | **stripped**; a workspace cannot configure an endpoint override, for the reason `ANTHROPIC_BASE_URL` is stripped under subscription billing — a login credential would be sent to whatever host it names |
| `CURSOR_AUTH_TOKEN` | raw auth token | **stripped** |
| `CURSOR_DATA_DIR` | relocates `~/.cursor` state | **stripped**, so a session reads the login the operator's CLI actually holds |
| `CURSOR_STATSIG_OVERRIDES` | overrides feature flags, including the tool-flag gates | **stripped** |

The login itself is **not** a file on macOS: read off the bundle, the CLI keeps it in the login
keychain (reached through `/usr/bin/security`), with `~/.cursor/auth.json` as the file form used
on Linux (`$XDG_CONFIG_HOME/cursor/auth.json`) and Windows (`%APPDATA%\Cursor\auth.json`). So the
child's environment must keep `HOME`, `PATH`, `USER` and `LOGNAME` — changing `HOME` to isolate
configuration would take the keychain with it. `cursor-agent status` prints
`✓ Logged in as <email>` and `cursor-agent about` prints the version, the model and
`Subscription Tier`.

`~/.cursor/cli-config.json` also carries a `permissions` block (`{"allow":["Shell(ls)"],"deny":[]}`)
and an `approvalMode`. Those are the interactive approver's rules; nothing observed suggests
they gate a print-mode session, and Sirdar does not write to that file.

## Usage and cost on the wire

The `result` line's `usage` object is
`{inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens}`. There is **no cost figure and
no turn count** anywhere on the stream. So:

- `budget.maxUsd` cannot bite on this provider, exactly as it cannot on `qwen`.
- `budget.maxTurns` has no CLI ceiling to map onto — there is no `--max-turns` — so the turn
  budget is only what Sirdar's own runner counts from `tool_call` lines.
- Input tokens are reported in three counters and the cached ones dominate (7 808 cached against
  13 112 fresh on a one-word answer), so a total that ignores them understates the session.

## Exit codes and stderr

| Code | Meaning | Result line on stdout? |
|---|---|---|
| 0 | success, including a session whose every tool call was refused | yes, `subtype:"success"`, `is_error:false` |
| 1 | workspace trust missing | **no** — nothing at all on stdout |
| 1 | `ActionRequiredError`, e.g. a named model on a free plan | **no** — `init` and `user` lines, then the stream stops |

Both exit-1 cases put a single human-readable line on stderr and produce no `result` line, so
the stderr tail is the only account of what happened — which is what `Result.StderrTail` is for.

## What was not exercised

- `--mode plan`. Only `ask` and the default were run. Plan mode is documented as
  "read-only/planning (analyze, propose plans, no edits)" and read off the bundle it takes the
  same `unified_mode` path as `ask`, but whether it refuses a write the same way is **inferred**.
- Any named model. The account is free, so `auto` is the only model any line above was produced
  under, and the per-model behaviour of `--mode ask` is unverified.
- Whether the backend actually honours `x-cursor-agent-exclude-tools`. The header is sent; no
  turn was run that tried to use an excluded tool.
- `beforeShellExecution` and `beforeMCPExecution` hooks. Only `preToolUse` was observed firing;
  the other two were registered in the same file and the shell call was already denied by
  `preToolUse` before they could be reached.
- MCP end to end. No MCP server is configured on this machine, so the merge rule, the approval
  hash and `mcp_tool_call` on the stream are all read off the bundle rather than watched.
- `--sandbox enabled` actually confining a write. The policy object was observed on the wire;
  no command was run that tried to escape it.
- Rate limiting and quota exhaustion. Seven small turns did not reach it, so what a refused
  request looks like on a spent free plan is unknown.
