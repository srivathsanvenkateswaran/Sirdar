# Antigravity CLI wire formats, captured 2026-09-15

Captured against **Antigravity CLI `agy` 1.2.3** on macOS, driving the real binary with a real
Google account (Google AI Pro) against `gemini-3.6-flash-low` at `--effort low`. Unlike
`docs/research/09-qwen-wire-formats.md`, there is no stub backend here: every line below came
off a live model turn, and the spend was kept to six invocations because the account's quota is
unknown.

Every claim is marked **verified** (watched on a live run, output quoted) or **inferred** (read
off `--help`, the binary's embedded documentation, or its own changelog, and not exercised).
Where the two disagree the verified line wins.

Conversation ids and token counts below are the real ones from the capture session; paths are
the scratch directories the capture ran in.

## The binary

`agy` is a single ~180 MB Go binary. `agy --version` prints `1.2.3` (verified). It is the CLI
front end for the same backend Antigravity 2.0 and the Antigravity IDE use; the CLI's own
state lives under `~/.gemini/antigravity-cli/` and its shared configuration under
`~/.gemini/config/` (verified: both directories exist and are written during a run).

## Models

`agy models` lists the models the signed-in account may use (verified):

```
gemini-3.8-flash-high / -medium / -low          Gemini 3.8 Flash
gemini-3.7-flash-high / -medium / -low          Gemini 3.7 Flash
gemini-3.6-flash-high / -medium / -low          Gemini 3.6 Flash
gemini-3.1-pro-high / -low                      Gemini 3.1 Pro
claude-sonnet-4-6                               Claude Sonnet 4.6 (Thinking)
claude-opus-4-6-thinking                        Claude Opus 4.6 (Thinking)
gpt-oss-120b-medium                             GPT-OSS 120B (Medium)
```

The reasoning tier is baked into the model id **and** available separately as `--effort
low|medium|high`. Passing both is accepted (verified: `--model gemini-3.6-flash-low --effort
low` ran). `--effort` is not universal — the binary carries the message `--effort is not
supported for model %q` (inferred; no model was found that rejects it in this capture).

No price list is published on the wire and the account is a subscription, so "cheapest" here
means the smallest tier: `gemini-3.6-flash-low`, which is what Sirdar defaults to.

## The command Sirdar runs

```
agy --output-format stream-json \
    --input-format stream-json \
    --json-schema '<json schema>' \
    --model <model> --effort <low|medium|high> \
    --mode plan \                       # triage only; see "Modes"
    --print-timeout <budget.maxMinutes>m \
    [--conversation <id>] \
    --print=
```

`--print=` (with the empty value attached) is load-bearing. `-p` / `--print` / `--prompt` all
take the prompt **as the flag's value**, so the bare `agy -p --output-format stream-json` form
is rejected outright (verified):

```
Error: -p took "--output-format" as its prompt, so the intended prompt was left as an argument
and ignored.
Attach the prompt to the flag (-p='your prompt') and move --output-format elsewhere on the
command line.
```

With `--input-format stream-json` the prompt comes from stdin instead, so the flag is given an
empty value to select print mode without claiming the next argument.

That exact argv — every flag together, with and without `--conversation` — was verified to
parse by running it against a deliberately malformed stdin line: the CLI emits `init` and then
fails at the decode, so the whole command line is exercised and no model turn is spent. This is
also the only evidence for `--disable-slash-commands`, which was never exercised on a turn of
its own.

## Output: `--output-format stream-json`

NDJSON, one object per line, each with a top-level `event` discriminator and a payload object
named after it. Three event types were seen: `init`, `step_update`, `result` (verified — the
changelog names exactly these three as the closed vocabulary).

### `init`

One per process, first line:

```json
{"event":"init","conversation_id":"e836d45d-753d-4919-94ea-c42bb802fd9a",
 "init":{"model":"gemini-3.6-flash-low",
         "cwd":"/private/tmp/.../agyspike/t1",
         "tools":["ask_custom_permission","ask_permission","ask_question","browser_click_element",
                  "...","write_to_file"],
         "permission_mode":"request-review",
         "json_schema":{"type":"object","properties":{"word":{"type":"string"}},"required":["word"]}}}
```

`json_schema` is present only when `--json-schema` was passed (verified). `conversation_id` is
the resume handle (see "Resume").

The advertised tool set is fixed and large — 57 names in this build (verified, full list in the
capture). The ones that matter to a read-only guarantee:

- writes: `write_to_file`, `replace_file_content`, `multi_replace_file_content`, `sed_file`,
  `notebook_edit`
- shell: `run_command`, `send_command_input`, `command_status`, `notebook_execution`
- network: `read_url_content`, `search_web`, and the whole `browser_*` family plus
  `open_browser_url`, `execute_browser_javascript`
- MCP: `call_mcp_tool`, `list_resources`, `read_resource`
- permission surface: `ask_permission`, `ask_custom_permission`, `list_permissions`
- terminal: `finish`

**There is no flag that removes a tool.** `agy --help` offers nothing resembling
`--exclude-tools`, `--allowed-tools` or `--disallowedTools` (verified against the full help
output). This is the single most important difference from every other provider Sirdar drives.

### `step_update`

One or more per step; `state` moves `ACTIVE` → `DONE`, or `ACTIVE` → `ERROR`.

Assistant text, streamed as deltas (verified):

```json
{"event":"step_update","step_update":{"conversation_id":"…","step_index":1,"state":"ACTIVE",
 "step_type":"agent_response","text_delta":"ok"}}
{"event":"step_update","step_update":{"conversation_id":"…","step_index":1,"state":"DONE",
 "step_type":"agent_response","text_delta":"\n","duration_seconds":3.024587,
 "usage":{"input_tokens":14375,"output_tokens":1,"thinking_tokens":0,
          "cache_read_tokens":0,"total_tokens":14376}}}
```

`text_delta` is a **delta**, not the whole text: the final `DONE` line carries only the last
fragment. The `usage` object on the terminal `DONE` line is that step's own usage.

A tool call (verified):

```json
{"event":"step_update","step_update":{"conversation_id":"…","step_index":2,"state":"ACTIVE",
 "step_type":"tool","tool_name":"write_to_file",
 "tool_info":{"name":"write_to_file","parameters":{"TargetFile":"/…/scratch/a.txt"}}}}
{"event":"step_update","step_update":{"conversation_id":"…","step_index":2,"state":"DONE",
 "step_type":"tool","tool_name":"write_to_file","duration_seconds":0.099246,
 "tool_info":{…}}}
```

A refused tool call (verified):

```json
{"event":"step_update","step_update":{"…":"…","step_index":4,"state":"ERROR","step_type":"tool",
 "tool_name":"run_command",
 "tool_info":{"name":"run_command","parameters":{"CommandLine":"touch x"},
   "error":{"type":"TOOL_ERROR",
     "message":"permission check failed for command \"touch x\": user denied permission to run command:\ntouch x"}}}}
```

`step_type` values seen live: `user_input`, `agent_response`, `tool`, `finish`,
`system_message` (verified). Others exist in the binary and were not exercised (inferred).

Tool argument keys are **PascalCase** (`TargetFile`, `CommandLine`), not snake_case — this is
Cascade's own convention and it matters for any policy that reads a path out of a tool call.

### `result`

**One `result` event per turn**, not one per process (verified — a two-message stdin stream
produced two `result` lines on one conversation):

```json
{"event":"result","result":{"conversation_id":"b05ca9ac-…","status":"SUCCESS",
 "response":"{\"toolAction\":\"Finish task\",\"toolSummary\":\"Finish task\",\"word\":\"ok\"}\n",
 "duration_seconds":3.740046,"num_turns":1,
 "structured_output":{"word":"ok"},
 "json_schema":{…},
 "usage":{"input_tokens":6447,"output_tokens":30,"thinking_tokens":0,
          "cache_read_tokens":8131,"total_tokens":6477}}}
```

- `status`: `SUCCESS` or `ERROR` (verified both).
- `error`: string, present on `ERROR`.
- `structured_output`: the validated object, present only with `--json-schema` (verified).
  `response` carries the raw model text, which under a schema includes the `finish` tool's own
  `toolAction` / `toolSummary` keys — `structured_output` is the clean one and is what Sirdar
  reads.
- `num_turns` and `usage` on the `result` line are **cumulative for the conversation**
  (verified: turn 2 reported `num_turns: 2` and `input_tokens: 9031 = 6447 + 2584`).
- `denied_actions`: present when the turn had a permission refused (verified):
  `"denied_actions":[{"action":"command","display_name":"RunCommand"}]`. Seen with
  `action` values `command` and `write_file`.

**There is no cost field anywhere on the wire.** No `total_cost_usd`, no credit figure, nothing.
`budget.maxUsd` therefore cannot bite on this provider, exactly as with `provider: qwen`.

## Input: `--input-format stream-json`

One NDJSON object per line on stdin, one turn run per line, all in a single conversation
(verified). The shape is **not** Claude Code's. Each line needs a top-level `event`:

```
error: stream input message is missing the "event" field
```

and the `user` event's `message` is an object, not a string:

```
error: failed to decode stream input: json: cannot unmarshal string into Go struct field
streamInputMessage.message of type printmode.streamInputUserMessage
```

The working shape (verified):

```json
{"event":"user","message":{"role":"user","content":[{"type":"text","text":"reply with the word ok"}]}}
```

Both of the malformed attempts above cost nothing: the decode fails before any model turn, and
the process exits 1 after emitting `init` and an `ERROR` `result`.

Two consequences, both good and both unlike qwen:

1. `--json-schema` and `--input-format stream-json` **coexist** (verified — `init` carried
   `json_schema` and both turns produced `structured_output`). Qwen Code makes them mutually
   exclusive, which is why that adapter has to carry its schema retry into a `--resume`.
   Here the retry is just another stdin line.
2. Follow-up user messages work, so `Session.Send` is a real in-session turn and
   `Session.CloseInput` is what ends the process. On stdin EOF `agy` exits 0 (verified).

## Permissions: there is no mediation channel

This is the finding that shapes the whole adapter.

**Verified:** no permission request ever appears on the stream, and there is no way to answer
one. With no TTY, a tool call that needs approval is **auto-denied**, and the CLI says so on
stderr:

```
jetski: no output produced — a tool required the "command" permission that headless mode cannot
prompt for, so it was auto-denied. Add an allow-rule under permissions.allow in settings.json
(e.g. command(<target>)). Alternatively, re-run with --dangerously-skip-permissions to
auto-approve all tools.
```

So the permission model is **static and file-based**, not a per-call conversation:

| Lever | Scope | Can Sirdar use it? |
|---|---|---|
| `permissions.allow` / `deny` / `ask` in `~/.gemini/antigravity-cli/settings.json` | global, all `agy` sessions | no — it would rewrite the operator's own config |
| project permissions under `~/.gemini/config/projects/<id>.json` | per project, higher precedence than global (inferred, from the changelog) | no — same file-ownership problem, and the project id is not the workspace |
| `<workspace>/.agents/hooks.json` `PreToolUse` command hook, returning `{"decision":"allow"\|"deny"\|"ask"\|"force_ask"}` | per workspace | **no** — it lives inside the customer's repository, and Sirdar never writes to the workspace |
| `--mode plan` | per session, a flag | **yes** |
| `--sandbox` | per session, a flag | yes, but see below |
| `--dangerously-skip-permissions` | per session, a flag | never for triage |

The `.agents/hooks.json` hook is the one thing here that resembles qwen's `PreToolUse` mediator
— same event names, same allow/deny verdict, plus an `overwrite` that rewrites the tool's
arguments — but it is a `type: "command"` hook only (the embedded documentation states "Only
`type: "command"` is supported (no HTTP or prompt hooks yet)"), and its only discovery location
is the workspace's own `.agents/` directory or the global `~/.gemini/config/`. Both are files
Sirdar would have to write outside its own `.sirdar/`, which the standing constraint forbids.
That is why this adapter has no equivalent of the qwen loopback hook.

### What was actually observed, per mode

A temp git repository, one prompt asking for a file write and a `touch`, no TTY, no allow rules:

**Default mode** (`permission_mode: "request-review"`), verified:

- `run_command "touch x"` → **denied**, `state: ERROR`, `denied_actions: [{"action":"command"}]`.
- `write_to_file` → **allowed and executed**. The model chose
  `/Users/…/.gemini/antigravity-cli/scratch/a.txt` and the file was really created on disk
  (content `hi`, confirmed by `stat`). The workspace itself was untouched, but a file did land
  outside it, unasked and ungated.

**`--mode plan`**, verified:

- `write_to_file /tmp/agyplan_a.txt` → **denied**:
  `permission check failed for write_file "/private/tmp/agyplan_a.txt": user denied permission
  for write_file(/private/tmp/agyplan_a.txt)`, `denied_actions: [{"action":"write_file"}]`.
- `write_to_file ~/.gemini/antigravity-cli/brain/<conv>/implementation_plan.md` → **allowed**.
  Plan mode's own artifact directory is exempt; it is inside `agy`'s state, not the workspace.
- `run_command` → observed **both ways across two runs**. A compound
  `touch /tmp/agy_plan_marker && echo MARKERDONE` was **denied** with the same "headless cannot
  prompt" refusal. A bare `touch x` in an earlier plan-mode run reported `state: DONE` with no
  error — and yet **no `x` file exists anywhere on the machine** (searched the workspace, the
  scratch directory, the brain directory and `$HOME`). So that call produced no side effect
  either, but it was not reported as a denial and the mechanism was not established.

**`--sandbox`**, verified only that it does not change `permission_mode` (still
`request-review`) and does not lift a denial. Its help text is "Run in a sandbox with terminal
restrictions enabled", so it is a *terminal* restriction, not a filesystem one (inferred). It
was not exercised against a command that the sandbox alone would refuse.

**`--dangerously-skip-permissions`**: "Auto-approve all tool permission requests without
prompting" (inferred from `--help`; deliberately not run).

### The conclusion Sirdar draws

`--mode plan` is the strongest read-only lever the CLI offers, and it is strictly better than
the default: it refused the out-of-workspace write that the default mode performed. But it is
not a sandbox and it is not settings-proof — an operator's own `permissions.allow` entries in
`~/.gemini/antigravity-cli/settings.json` still apply to headless runs (the changelog is
explicit: "Fixed headless (`-p`/`--print`) runs so they now honor persisted `settings.json`
policies, including `permissions`, file access, sandbox mode, auto-execution, and artifact
review"), and Sirdar cannot see, let alone override, what is in that file.

So the guarantee this adapter can make is: **plan mode plus a workspace that is not trusted,
with every denial reported as an event, and no allow rule ever written by Sirdar.** That is
weaker than Claude's `--disallowedTools` + policy or qwen's `--exclude-tools`, and the adapter
says so in `doctor` rather than pretending otherwise.

And for `sirdar fix`: there is no per-call mediation, so `provider.FixPolicy`'s path
confinement — the thing that keeps a fix inside the workspace and out of `.git/` — has nothing
to attach to. The only way to let `agy` write is `--dangerously-skip-permissions`, which
approves everything including `.git/hooks/pre-commit`. **This provider refuses fix mode.**

## Workspace trust

`~/.gemini/antigravity-cli/settings.json` carries a `trustedWorkspaces` array (verified — the
file on this machine reads `{"trustedWorkspaces":["/Users/srivathsanv/Documents/Personal/Sirdar"]}`).
An untrusted workspace produces the banner "%s requires permission to read, edit, and execute
files here" (inferred, from the binary's strings) and, on the default-mode run above, the model
redirected its write into `agy`'s own scratch directory rather than the workspace — consistent
with the workspace being untrusted, though the causal link was not proved.

This cuts both ways and is worth stating plainly: the default-mode write test ran in an
**untrusted** temp repository. A workspace the operator has already trusted interactively —
which is the normal case for a Sirdar workspace — may well behave differently, so the observed
"it wrote to scratch, not the repo" must **not** be read as a guarantee. Plan mode's refusal is
the property to rely on, because it refused an absolute path outside the workspace too.

Sirdar does not add a workspace to `trustedWorkspaces` and does not remove one.

## MCP

`~/.gemini/config/mcp_config.json` is the **global** server file, "applies to all sessions"
(verified location; the file exists and is empty on this machine). The only other location is
`plugins/<plugin>/mcp_config.json`, active when the plugin is enabled. The embedded
documentation lists exactly these two and no project-scoped file.

`agy mcp list|add|remove|enable|disable` manages that one global file. `agy mcp add --help`
shows `--type`, `--env`, `--header` and nothing resembling `--scope`, `--project` or `--local`
(verified). `agy mcp list` reports `No MCP servers configured.` on this machine (verified).

There is no `--mcp-config` flag, no `--strict-mcp-config`, and no allow-list by server name.

**So `mcp.workspaceOnly` is not enforceable on this provider.** The adapter cannot point a
session at the workspace's `.mcp.json`, and cannot keep the operator's own global servers out.
The tool set the session is offered still contains `call_mcp_tool`, and a call through it is
judged by no Sirdar policy at all. Doctor warns, loudly, and `docs/config.md` records it.

## Environment

The variables `agy` reads, from the binary's own strings and embedded changelog (**inferred**
— none was exercised):

- `GEMINI_API_KEY` with `modelProvider: "gemini"` in settings runs against the Gemini API
  directly instead of the signed-in account; `GOOGLE_GEMINI_BASE_URL` points that at a custom
  endpoint. This is the same class of credential-redirection hazard the Claude adapter strips
  `ANTHROPIC_BASE_URL` for, so the adapter strips all three.
- `GOOGLE_CLOUD_QUOTA_PROJECT` selects the billing/quota project.
- `ANTIGRAVITY_LS_ADDRESS`, `ANTIGRAVITY_CSRF_TOKEN`, `ANTIGRAVITY_SIDECAR_WEB_PORT`,
  `ANTIGRAVITY_SIDECAR_UI_TOKEN`, `ANTIGRAVITY_AGENTAPI_EXE`, `ANTIGRAVITY_CONVERSATION_ID`,
  `ANTIGRAVITY_PROJECT_ID`, `ANTIGRAVITY_EXECUTABLE_DATA_DIR` — the sidecar/extension
  protocol, injected by the server into child processes. Stripped so a stray export cannot
  make a Sirdar session think it is a sidecar.
- `AGY_CLI_*` — presentation only (`AGY_CLI_HIDE_LOGO`, `AGY_CLI_DISABLE_LATEX`,
  `AGY_CLI_CMD_OUTPUT_PERCENTAGE`, …). Left alone.
- `JETSKI_APP_DATA_DIR`, `JETSKI_BROWSER_PORT` — internal state and browser-tool wiring.
  Stripped.

The OAuth credential itself is **not** in `~/.gemini`: the binary carries
`Failed to persist token to keyring: %v` and `keyringAuth: context cancelled, skipping keyring
auth`, so the login lives in the OS keyring (macOS Keychain here). That was not opened — the
capture deliberately did not dump the keychain. Consequence for the adapter: it must **not**
try to relocate `HOME` to isolate `~/.gemini`, because whether the keyring lookup survives a
changed `HOME` is unknown, and a failed lookup is a broken run.

## Resume

`--continue` / `-c` continues the most recent conversation; `--conversation <id>` resumes a
named one. **Verified**: resuming `435f336d-0acc-46d7-93c0-2885aa5d03a6` with a fresh stdin
message kept the same `conversation_id`, carried the prior turn's context (the model correctly
recalled the shell command from the earlier message), and continued `num_turns` at 2.

So `conversation_id` from the `init` line is the resume handle, and `SessionSpec.Resume` maps
to `--conversation`. Note that flags are **not** carried across a resume: the resumed session
above did not re-apply `--mode plan` because it was not passed again. The adapter therefore
passes the full flag set on every start, resume included.

## Exit codes

Verified:

- `0` — the conversation ran, whatever the model decided, **including** a turn whose only tool
  call was denied. The changelog confirms this is deliberate: "Fixed print mode … treating
  benign tool execution errors and permission denials as fatal run failures with non-zero exit
  codes, ensuring headless exit codes reflect only cascade-level failures".
- `1` — a stream-input decode error (bad stdin line). An `ERROR` `result` is emitted first.
- `2` — argument-parse failure (the `-p` swallowing case above). Nothing is emitted on stdout.

Not exercised (inferred): timeouts under `--print-timeout` and auth failures.

## `--print-timeout`

Defaults to `5m0s` and takes a Go duration. Sirdar maps `budget.maxMinutes` onto it, since the
CLI has no turn or tool-call ceiling of its own — no `--max-turns`, no `--max-tool-calls`, no
`--max-wall-time`. The turn budget has to be enforced by Sirdar counting `result` events.

## What this capture did not establish

- Whether `--sandbox` refuses anything the permission layer would otherwise allow.
- Why one plan-mode `run_command` reported `DONE` with no side effect instead of a denial.
- How a **trusted** workspace behaves in default mode — every write test here ran in an
  untrusted temp repository.
- Whether `--effort` is refused for the Claude and GPT-OSS models on the list.
- Anything about cost or quota: no credit figure appears on the wire, and the account's
  remaining quota was never queried.
