# Antigravity CLI wire formats, captured 2026-09-15

Captured against **Antigravity CLI `agy` 1.2.3** on macOS, driving the real binary with a real
Google account (Google AI Pro) against `gemini-3.6-flash-low` at `--effort low`. Unlike
`docs/research/09-qwen-wire-formats.md`, there is no stub backend here: every line below came
off a live model turn, and the spend was kept to six invocations in the first round because the
account's quota is unknown.

**Round 2, same day, two more live runs.** The first round left the adapter with two defects
that only a live triage could show, and both are fixed and verified below: `--mode plan` was
being cancelled by `--disable-slash-commands`, and a headless run was auto-denying every read,
so the agent answered the ticket out of its description. See "Plan mode and slash commands" and
"The project lever" — the latter is no longer a rejected option but the mechanism the adapter
runs on.

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
    --project sirdar-<16 hex> \         # the session's own permission file; see "The project lever"
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
fails at the decode, so the whole command line is exercised and no model turn is spent. Round 2
then ran the whole line on two real turns.

### Plan mode and slash commands

Round 1 also passed `--disable-slash-commands`, on the reasoning that the prompt carries ticket
text somebody else wrote and expansion would let a line of it name a command. That was wrong,
and the CLI said so on every run (**verified**, on stderr):

```
warning: --mode plan has no effect while slash command expansion is disabled.
```

Plan mode in print mode **is** an expansion: the binary's own flag plumbing carries
`DisableSlashCommands` alongside the execution mode, and with expansion off the mode never
takes. So every round-1 session ran in the CLI's *default* mode — the one the capture below
watched perform a write — while the run record said plan mode. Nothing in `agy --help` makes
the two coexist. The flag is gone, and round 2 confirmed the warning goes with it (**verified**:
stderr was empty on both runs).

What the injection surface actually is, with expansion back on:

- The CLI's own slash commands are **not** reachable from a stream-json prompt. The binary
  carries `/%s is answered by the CLI itself and is unavailable with --input-format stream-json;
  run it as its own --print /%s invocation` (**inferred**, read off the binary; not exercised).
  So `/model`, `/logout`, `/plugin` and the rest cannot be driven from ticket text on this
  command line.
- What remains expandable is a user or workspace *skill*. A skill that expanded into a write or
  a command is refused by the session's own project rules (below), and a write or command that
  completed anyway ends the run.

That is the trade the adapter takes: an expansion surface that cannot reach the session's
configuration, against a read-only mode that actually applies.

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
stderr. This is what a Sirdar run hits first, and it is what made round 1's triage read nothing
at all:

```
jetski: no output produced — a tool required the "command" permission that headless mode cannot
prompt for, so it was auto-denied. Add an allow-rule under permissions.allow in settings.json
(e.g. command(<target>)). Alternatively, re-run with --dangerously-skip-permissions to
auto-approve all tools.
```

So the permission model is **static and file-based**, not a per-call conversation. That rules
out mediation, but not policy: a set of rules chosen before the session starts is still Sirdar's
to choose, and one of the levers below turned out to be exactly that.

| Lever | Scope | Can Sirdar use it? |
|---|---|---|
| `permissions.allow` / `deny` / `ask` in `~/.gemini/antigravity-cli/settings.json` | global, all `agy` sessions | no — it would rewrite the operator's own config |
| project permissions under `~/.gemini/config/projects/<id>.json` | per project, highest precedence (verified) | **yes** — this is what the adapter uses; see "The project lever" |
| `<workspace>/.agents/hooks.json` `PreToolUse` command hook, returning `{"decision":"allow"\|"deny"\|"ask"\|"force_ask"}` | per workspace | **no** — it lives inside the customer's repository, and Sirdar never writes to the workspace |
| `--mode plan` | per session, a flag | **yes** |
| `--sandbox` | per session, a flag | yes, but see below |
| `--dangerously-skip-permissions` | per session, a flag | never for triage |

### The project lever, spiked and then adopted 2026-09-15

`--new-project` is the one flag that looked like it could give Sirdar a permissions file of its
own, so round 1 spiked it. What it does, **verified** with no model call (`agy --new-project
models`, which resolves the project before running the subcommand):

- it writes `~/.gemini/config/projects/<uuid>.json`, named after the working directory and
  pointing at it: `{"id":"<uuid>","name":"Sirdar","projectResources":{"resources":[{"folderUri":
  "file:///…/Sirdar"}]}}`. A plain `--help` run creates nothing, so the file is written during
  project resolution and not by flag parsing.
- the file it writes carries **no permission block at all** — three keys, none of them about
  what a session may do.
- the changelog embedded in the binary says project files take precedence: "Improved permission
  config merging priorities by ensuring project-specific configurations (located in
  `~/.gemini/config/projects/`) take precedence over global settings in
  `~/.gemini/antigravity-cli/settings.json`" (verified as a string).

Round 1 stopped there and did not adopt it, on the grounds that `--new-project` mints a fresh
uuid per invocation and that enforcement had never been exercised. Round 2 went further, because
the alternative turned out to be worse: with no allow rule anywhere, a headless triage is
refused **every read** and answers out of the ticket text (see "What round 1's live triage
actually did"). So the lever was read off the binary properly and then driven live.

#### The schema, read off the binary

`--project` takes a **project id or name**, not only a uuid (verified, `agy --help`), and a
project is looked up by id in the file named `<id>.json` — the CLI's own default project is
`default-cli-project`. So Sirdar does not need `--new-project` at all: it writes the file
itself, under an id that says whose it is.

The `exa.project_pb.Project` descriptor embedded in the binary gives the exact shape
(**verified**, extracted from the proto descriptor; protojson camelCase is the encoding the
CLI's own file uses):

```
Project {
  id, name
  project_conversations, project_resources → Resources { resources: [ Resource { folder_uri } ] }
  environments
  permission_grants → PermissionGrants {
      permission_grants → codeium_common_pb.PermissionGrantsConfig { allow, deny, ask }
      v2_migrated }
  settings → ProjectSettings {
      file_access_policy    : AgentSettingPolicy
      internet_policy       : AgentSettingPolicy
      sandbox_mode          : bool
      auto_execution_policy : CascadeCommandsAutoExecution
      artifact_review_mode  : ArtifactReviewMode
      permission_preset     : AgentPermissionPreset
      enable_permissioned_github, shell_setup_script, security_plugins }
  updated_at, is_workspace_only, archived
}
```

Enum values, all **verified** off the same descriptor. Round 1's note guessed at
`FILE_ACCESS_POLICY_DENY` and `AUTO_EXECUTION_POLICY_REQUIRE_REVIEW`; neither name exists.

- `AgentSettingPolicy`: `AGENT_SETTING_POLICY_UNSPECIFIED | _ALLOW | _ASK | _DENY`
- `CascadeCommandsAutoExecution`: `..._UNSPECIFIED | _OFF | _AUTO | _EAGER | _PROCEED_IN_SANDBOX`
- `AgentPermissionPreset`: `..._UNSPECIFIED | _REQUEST_REVIEW | _DEFAULT | _VETTED | _TURBO | _NONE`
- `ArtifactReviewMode`: `..._UNSPECIFIED | _ALWAYS | _TURBO | _AUTO`

`PermissionGrantsConfig` is three lists of **rule strings** — the same `allow` / `deny` / `ask`
shape `settings.json` uses (verified: the binary's `PermissionGrantsToPermissions` and
`applyUserSettings: stored shared config permissions: allow=%d deny=%d ask=%d from %s`). The
rule syntax is `<permission>(<target>)`, and the permission vocabulary is **four verbs**, not
one per tool (verified from the binary's own canonical forms `read_file(*)`, `read_file(/)`,
`write_file(*)`, `execute_url(*)`, plus the deprecated `unsandboxed`, which 1.2.2's changelog
says is ignored):

| permission | covers |
|---|---|
| `read_file` | every read tool: `view_file`, `grep_search`, `list_dir`, `find_by_name` |
| `write_file` | every write tool |
| `command` | `run_command` and friends; target matches **by prefix** ("'git' matches 'git add', 'git commit'") |
| `execute_url` | fetching a URL |

There is no way to name an individual tool, so "allow the read tools" is exactly one rule.

#### What Sirdar writes

One file per session, deleted when the session ends:

```json
{
  "id": "sirdar-58db68ce673d5fdb",
  "name": "Sirdar triage (temporary)",
  "projectResources": { "resources": [ { "folderUri": "file:///…/app-agy" } ] },
  "permissionGrants": {
    "permissionGrants": {
      "allow": ["read_file(*)"],
      "deny": ["write_file(*)", "command(*)", "execute_url(*)"]
    },
    "v2Migrated": true
  },
  "settings": {
    "autoExecutionPolicy": "CASCADE_COMMANDS_AUTO_EXECUTION_OFF",
    "permissionPreset": "AGENT_PERMISSION_PRESET_REQUEST_REVIEW"
  }
}
```

Three decisions in that file are worth stating.

**`read_file(*)` and not the workspace path.** Prefix matching is documented for `command` and
for nothing else; `read_file`'s canonical forms in the binary are `*` and `/`. A narrower rule
read as a literal path would deny every read and put the run back where round 1 was, and
establishing which it is costs a live run per guess. So the rule is the one whose meaning is not
in doubt, and the cost is stated rather than hidden: the session can read a file outside the
workspace. It cannot do anything with one — `command`, `execute_url` and every write are denied,
so a read that wanders has nowhere to go but the triage note.

**`run_command` stays denied in triage.** Sirdar cannot mediate an `agy` tool call, so a command
allowed here would be a command judged by nobody: `permissions.bash`, which is what decides this
on every other provider, has no attachment point on this wire. A triage that genuinely needs a
command's output belongs on a provider Sirdar can mediate.

**Most of `ProjectSettings` is left out.** `fileAccessPolicy` and `internetPolicy` are both
`AgentSettingPolicy` and neither the descriptor nor the help says what surface each governs — a
`fileAccessPolicy: DENY` meant as "no files outside the workspace" and read as "no files" would
blind the run it was added to protect. `sandboxMode` and `artifactReviewMode` are the same kind
of guess. The two that are set say plainly what they do.

The file lives in the operator's directory, which is why it is named `sirdar-<16 hex>.json` so
it cannot be mistaken for theirs, is never reused across runs, is removed on every exit path
(`Wait`, `Cancel`, and each failed `Start`), and is swept by the next session if a `SIGKILL`
left one behind.

#### Verified live, round 2

One probe turn in the sandbox workspace, plan mode, the project file above, no
`--disable-slash-commands`, a prompt asking for one read and one write:

```
TOOL view_file      ACTIVE  {"AbsolutePath": "/…/app-agy/ledger.go"}
TOOL view_file      DONE    {"AbsolutePath": "/…/app-agy/ledger.go"}
TOOL write_to_file  ACTIVE  {"TargetFile": "/tmp/agy_r2_probe.txt"}
TOOL write_to_file  ERROR   permission check failed for write_file "/private/tmp/agy_r2_probe.txt":
                            Permission denied for write_file(/private/tmp/agy_r2_probe.txt).
                            Matches user-configured deny rule.
RESULT status=SUCCESS turns=1
```

- **The read succeeded.** Same call that round 1 had auto-denied.
- **The write was refused, by Sirdar's own rule.** "Matches user-configured deny rule" is the
  project file's `write_file(*)` speaking, not plan mode's generic refusal — so project grants
  are confirmed to reach a headless run and to be the thing that decides.
- **stderr was empty.** No plan-mode warning, no "headless mode cannot prompt" line.
- `/tmp/agy_r2_probe.txt` does not exist on disk afterwards.

Then one real `sirdar triage SBX-1` on the sandbox, same build: four `view_file` steps completed,
`run_command "go test ./..."` was refused with `Permission denied for command(go test ./...).
Matches user-configured deny rule.`, stderr was empty, no breach was raised, the project file was
gone when the run ended, and the note that was filed cites `ledger.go#L27-L34` and
`movement.go#L51-L62` for a real double-increment bug. Round 1's note, on the same ticket, had
been written without opening a file.

#### What is still not established

Whether a path-scoped `read_file(<dir>)` rule matches by prefix the way `command` does; whether
`fileAccessPolicy` / `internetPolicy` govern what their names suggest; and whether
`ANTIGRAVITY_PERM_GRANTS` — an environment variable the binary carries alongside
`readPermGrants` / `loadPermGrants` symbols, which would let a session carry its rules without
touching the operator's directory at all — is a supported channel or an internal one between the
CLI and its own sidecar. The adapter strips every other `ANTIGRAVITY_*` variable for exactly
that reason, so this one was left alone. Each of these costs a live run to settle.

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
  The adapter's exemption is that path and no wider: `<state dir>/brain/<conversation id>/`,
  using the conversation id off the session's own stream. It deliberately does **not** cover
  the state directory as a whole, because `scratch/` sits beside `brain/` and `scratch/` is
  where the default-mode run above really did put a file.
- `run_command` → observed **both ways across two runs**. A compound
  `touch /tmp/agy_plan_marker && echo MARKERDONE` was **denied** with the same "headless cannot
  prompt" refusal. A bare `touch x` in an earlier plan-mode run reported `state: DONE` with no
  error — and yet **no `x` file exists anywhere on the machine** (searched the workspace, the
  scratch directory, the brain directory and `$HOME`). So that call produced no side effect
  either, but it was not reported as a denial and the mechanism was not established.

  **Known false-positive risk, accepted.** The adapter treats every completed `run_command` as
  a breach, which ends the run. If the unexplained `DONE`-with-no-effect case is the CLI
  reporting a plan-mode no-op rather than a command that ran, a run will occasionally fail over
  a command that did nothing. That is the direction this fails in on purpose: the alternative
  is filing a triage note that asserts a read-only run over a command that really executed, and
  a spurious failure is cheap to rerun while a false read-only claim is not visible at all.
  Removing the rule needs the CLI to explain the case — a `DONE` line that distinguishes "ran"
  from "declined to run" — not a guess about which it was.

**`--sandbox`**, verified only that it does not change `permission_mode` (still
`request-review`) and does not lift a denial. Its help text is "Run in a sandbox with terminal
restrictions enabled", so it is a *terminal* restriction, not a filesystem one (inferred). It
was not exercised against a command that the sandbox alone would refuse.

**`--dangerously-skip-permissions`**: "Auto-approve all tool permission requests without
prompting" (inferred from `--help`; deliberately not run).

### The conclusion Sirdar draws

`--mode plan` is the strongest read-only *mode* the CLI offers, and it is strictly better than
the default: it refused the out-of-workspace write that the default mode performed. On its own
it is neither a sandbox nor settings-proof — an operator's own `permissions.allow` entries in
`~/.gemini/antigravity-cli/settings.json` still apply to headless runs (the changelog is
explicit: "Fixed headless (`-p`/`--print`) runs so they now honor persisted `settings.json`
policies, including `permissions`, file access, sandbox mode, auto-execution, and artifact
review"), and Sirdar can neither see nor edit that file.

What the project file adds is the part plan mode could not: rules Sirdar chooses, for one
session, that **outrank** `settings.json`. So the guarantee this adapter makes is: **plan mode,
plus a per-session permission file allowing a read and denying every write, command and URL
fetch, plus a workspace that is not trusted, with every denial reported as an event, nothing
Sirdar writes outliving the run, and a completed write or command ending it.**

That last clause is what makes it a guarantee rather than a hope. A write that gets through is
not a warning on a note: the session is cancelled on the spot, its process group killed, and the
run ends `failed` with the reason `read-only breach: <tool> <path|command>`. No note is filed
and no register row is written, because both of them would assert the thing that just failed to
be true. It is still weaker than Claude's `--disallowedTools` + policy or qwen's
`--exclude-tools` — those refuse the call *and* let Sirdar judge each one, where this sets the
rules up front and can only refuse the run afterwards — and `doctor` says so rather than
pretending otherwise.

What counts as a completed write, in the adapter: `write_to_file`, `replace_file_content`,
`multi_replace_file_content`, `sed_file`, `notebook_edit`, and for commands `run_command`,
`send_command_input`, `notebook_execution`, `browser_subagent`,
`execute_browser_javascript` and `call_mcp_tool`. The MCP tool is on the list because the CLI
reports one `call_mcp_tool` step whatever the server went on to do, and the session sees
whichever servers the operator configured globally — left off, an MCP write is the one kind
that completes in silence.

The path a breach names comes off the step's **`ACTIVE`** line, matched to the `DONE` line by
`step_index`. The capture above shows why: the `DONE` line elides `tool_info`, so reading the
target off the line that says the write finished gets nothing at all.

And for `sirdar fix`: there is no per-call mediation, so `provider.FixPolicy`'s path
confinement — the thing that keeps a fix inside the workspace and out of `.git/` — has nothing
to attach to. A project file could allow `write_file`, but not *which* write, which is the whole
of what a fix policy decides. **This provider refuses fix mode**, and it refuses it before
`sirdar fix` touches git at all: the provider answers `SupportsFix()` false, which the fix entry
asks before it fetches the default branch, cuts a branch or adds a worktree.

## What round 1's live triage actually did

The first end-to-end triage on this provider is worth recording, because it is the reason for
two of the changes above and for a third in the run layer.

`sirdar triage SBX-1` against the sandbox workspace: **completed** in 36 seconds, three turns,
a note filed with classification `code` and confidence **high**. The event log, though:

```
tool_started   find_by_name          → tool_finished
tool_started   view_file             → permission deny
                 permission check failed for read_file "…/app-agy/ledger.go":
                 user denied permission for read_file(…)
tool_started   run_command "cat ledger.go"  → permission deny
```

and on stderr, twice:

```
jetski: no output produced — a tool required the "read_file" permission that headless mode
cannot prompt for, so it was auto-denied. Add an allow-rule under permissions.allow in
settings.json (e.g. read_file(<target>)).
warning: --mode plan has no effect while slash command expansion is disabled.
```

So: the agent never opened a file, fell back to `cat` and was refused that too, and then wrote a
high-confidence root-cause hypothesis out of the ticket description. The note reached the
register and the digest looking exactly like one built on evidence, because nothing downstream
can tell the difference.

Three separate defects, and each got its own fix:

1. **Plan mode was not in effect at all** — `--disable-slash-commands` cancelled it. Fixed by
   dropping the flag; see "Plan mode and slash commands".
2. **Nothing granted a read.** Fixed by the project file; see "The project lever".
3. **A session that read nothing still filed.** Fixed in the run layer, which is the one that
   generalises: the provider now raises `EvBlind` immediately ahead of the final answer when the
   session completed no read, and a run that sees it ends `failed` with
   `the agent could read nothing (N reads denied)` — no note, no register row, the raw answer
   kept in `result.raw.txt` where it can be read without being believed.

   A completed `find_by_name` does **not** count as a read, and that distinction comes straight
   off the log above: that run's `find_by_name` succeeded while its `view_file` was refused, so a
   check counting any completed tool would have passed a session that had seen a list of
   filenames and not one line of code. The tools that count are the ones that put file contents
   in the prompt: `view_file`, `read_file`, `grep_search`, `read_resource`, `read_url_content`,
   `read_browser_page`.

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
judged by no Sirdar policy at all. Doctor warns, loudly, and `docs/config.md` records it. A
`call_mcp_tool` step that completes is therefore treated as a breach like any other completed
command: the adapter cannot tell a read from a write through it, and the read-only side of that
guess is the one that fails silently.

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
- Why one plan-mode `run_command` reported `DONE` with no side effect instead of a denial. The
  adapter fails closed on it in the meantime; see the accepted false-positive note above.
- What `ProjectSettings.fileAccessPolicy` and `internetPolicy` actually govern. Round 2 settled
  the part that mattered — a project file's `permissionGrants` **are** honoured in a headless
  plan-mode run, and are what refused the write — but left these two unset rather than guess at
  a policy whose `DENY` might mean "no files outside the workspace" or simply "no files". See
  "The project lever".
- Whether `read_file(<path>)` matches by prefix the way `command(<prefix>)` documents. The
  adapter allows `read_file(*)` because that form's meaning is not in doubt; a scoped rule would
  be tighter and costs a live run to test.
- Whether `ANTIGRAVITY_PERM_GRANTS` is a supported way to carry a session's rules in the
  environment instead of in a file. The binary has the variable and `readPermGrants` /
  `loadPermGrants` beside it; the adapter strips every other `ANTIGRAVITY_*` variable as sidecar
  protocol and left this one alone.
- How a **trusted** workspace behaves in default mode — every write test here ran in an
  untrusted temp repository.
- Whether `--effort` is refused for the Claude and GPT-OSS models on the list.
- Anything about cost or quota: no credit figure appears on the wire, and the account's
  remaining quota was never queried.
