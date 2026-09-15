# Kimi CLI wire formats, captured 2026-09-15

Captured against **Kimi Code CLI `kimi` 0.43.1** on macOS, driving the real binary
(`~/.kimi-code/bin/kimi`) against the owner's **Kimi Code free tier**. It is a ~180 MB
Node single-file executable; the JavaScript is readable in the binary, so claims below are
marked **verified** (exercised against the live process, output quoted) or **read off the
bundle** (taken from the embedded JavaScript, help text or documentation, and not exercised).
Where the two disagree the verified line wins.

**No model turn was ever spent.** The first `session/prompt` came back with the account's
monthly quota already exhausted, which ended the live half of the capture before a single
token was billed — see "Quota". Everything below that needed a model reply is therefore read
off the bundle and says so. The handshake, the mode machinery, the argument parser and the
failure shapes were all exercised for real.

Companion to `docs/research/06-wire-formats.md` (Claude Code, Codex),
`09-qwen-wire-formats.md` (Qwen Code), `10-antigravity-wire-formats.md` (Antigravity) and
`11-cursor-wire-formats.md` (Cursor).

## The headline: Kimi speaks ACP, natively, and well

`kimi acp` is "Run kimi-code as an Agent Client Protocol (ACP) server over stdio" (verified,
from `kimi acp --help`). It is built on `@agentclientprotocol/sdk@1.3.0` (read off the bundle)
and answers `initialize` with protocol version 1. So Sirdar does not need a native adapter:
`provider: acp` with `command: kimi`, `args: ["acp"]` reaches it today.

That settles the provider question, and `docs/research/providers/acp-agents.md` — which listed
Kimi CLI with "args unverified" — now has a verified row.

## Where the binary lives

`which kimi` finds nothing: the installer does **not** put it on `PATH`, and it is not in
`~/.local/bin`, Homebrew, pipx or a uv tool directory. On this machine it is
`/Users/srivathsanv/.kimi-code/bin/kimi` (verified). Its whole world sits under
`~/.kimi-code/`: `config.toml`, `tui.toml`, `credentials/kimi-code.json` (an OAuth
access/refresh pair, mode `0600`), `device_id`, `region`, `logs/kimi-code.log`,
`workspace-trust/`, `workspaces.json`, `telemetry/`, `updates/` (all verified — the directory
was listed).

`acp.command` therefore has to be the **absolute path** unless the operator has put the
directory on their own `PATH`. `sirdar doctor` reporting "command not found" is the expected
first result for anyone who copies the config row blind, and `docs/config.md` says so.

## The ACP handshake, verbatim

`initialize` (verified, reflowed):

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,
 "agentCapabilities":{"loadSession":true,
   "promptCapabilities":{"image":true,"audio":false,"embeddedContext":true},
   "sessionCapabilities":{"list":{},"resume":{},"close":{},"delete":{},"fork":{},
                          "additionalDirectories":{}},
   "mcpCapabilities":{"http":true,"sse":true},"auth":{"logout":{}}},
 "authMethods":[{"id":"login","type":"terminal","name":"Login with Kimi account",
   "description":"Open the device-code login flow in a terminal.","args":["--login"],"env":{},
   "_meta":{"terminal-auth":{"type":"terminal","label":"Login with Kimi account",
     "command":"/Users/srivathsanv/.kimi-code/bin/kimi","args":["login"],"env":{}}}}],
 "agentInfo":{"name":"Kimi Code CLI","version":"0.43.1"}}}
```

Two capabilities matter to a Sirdar run and both are present:

- **`loadSession: true`** — `sirdar resume` continues the real session rather than starting a
  fresh one, and a schema retry has somewhere to go.
- **`promptCapabilities.image: true`** — screenshot attachments go over the wire as image
  content parts instead of being named in the prompt text.

`authMethods` carries a `terminal` auth method: a client that finds the agent unauthenticated
is meant to run `kimi login` in a terminal. Sirdar has no terminal to offer, so an
unauthenticated machine is an operator problem to fix once, out of band, not something a run
can recover from.

`session/new` (verified) returns a `sessionId`, a `modes` block and a `configOptions` block:

```json
{"sessionId":"session_11c71176-…",
 "modes":{"currentModeId":"default","availableModes":[
   {"id":"default","name":"Default","description":"Manual approvals; tools execute normally."},
   {"id":"plan","name":"Plan","description":"Read-only planning; no tool execution."},
   {"id":"auto","name":"Auto","description":"Auto-approve safe operations."},
   {"id":"yolo","name":"YOLO","description":"Auto-approve everything."}]},
 "configOptions":[
   {"type":"select","id":"model","category":"model","currentValue":"kimi-code/kimi-for-coding",
    "options":[{"value":"kimi-code/kimi-for-coding","name":"K2.8 Preview"},
               {"value":"kimi-code/kimi-for-coding-highspeed","name":"K2.7 Code Highspeed"},
               {"value":"kimi-code/k3","name":"K3"},
               {"value":"kimi-code/k3-256k","name":"K3-256k"}]},
   {"type":"select","id":"thinking","category":"thought_level","currentValue":"max",
    "options":[{"value":"low",…},{"value":"high",…},{"value":"max",…}]},
   {"type":"select","id":"mode","category":"mode","currentValue":"default","options":[…]}]}
```

`session/set_mode` and `session/set_config_option` both work and both take effect immediately:

```
→ {"method":"session/set_mode","params":{"sessionId":"…","modeId":"plan"}}
← {"result":{}}
← {"method":"session/update","params":{"update":{"sessionUpdate":"current_mode_update",
                                                "currentModeId":"plan"}}}
```

(verified). An unknown mode is refused cleanly: `-32602 Invalid params: Unknown modeId: bogus`
(verified). `session/set_config_option` takes **`configId`**, not `optionId` — passing
`optionId` earns `-32602 Invalid params … configId: expected string, received undefined`
(verified both ways); with `configId` it returns the whole refreshed `configOptions` array with
the new `currentValue` (verified, switching the model to `kimi-code/kimi-for-coding-highspeed`).

So model selection and the thinking tier are per-session ACP calls, not command-line flags.
Sirdar's ACP adapter sends neither today; a kimi session runs on `default_model` from
`~/.kimi-code/config.toml` (`kimi-code/kimi-for-coding`, "K2.8 Preview") at thinking `max`.

The full ACP method set the server registers (read off the bundle's SDK schema): `initialize`,
`authenticate`, `providers/list|set|disable`, `session/new|load|set_mode|set_config_option|`
`prompt|cancel|list|delete|fork|resume|close`, `logout`, `mcp/message`, `nes/*` (next-edit
suggestions), `document/did*`. Client-side it calls `session/request_permission`,
`session/update`, `fs/read_text_file`, `fs/write_text_file`, `terminal/*`, `mcp/*` and
`elicitation/*`.

`session/update` kinds emitted: `agent_message_chunk`, `agent_thought_chunk`, `tool_call`,
`tool_call_update`, `plan`, `available_commands_update`, `current_mode_update`,
`config_option_update`, `session_info_update`, `usage_update`, `user_message_chunk` (read off
the bundle; `available_commands_update`, `current_mode_update` and `session_info_update` were
also seen live). Stop reasons: `end_turn`, `max_tokens`, `max_turn_requests`, `refusal`,
`cancelled`.

## The read-only decision

This is the part that shapes everything, and it does **not** come out where the mode names
suggest it would.

### Headless `-p` is unusable, and not for the usual reason

`kimi -p '<prompt>'` runs one prompt non-interactively, `--output-format text|stream-json`.
It looks like every other provider's print mode. It is not: `runV2Print` calls a helper the
bundle names `forceAuto`, which does

```js
const forceAuto = (agent) => {
  const permissionMode = agent.accessor.get(IAgentPermissionModeService);
  const previous = permissionMode.mode;
  permissionMode.setMode("auto");
  return { restorePermission: async () => { permissionMode.setMode(previous); } };
};
```

on **every** path through print mode — fresh session, `--session <id>` and `--continue` alike
(read off the bundle). "Auto" is the mode `--auto` selects interactively, described in
`kimi --help` as "Never Ask mode: never interrupts you; everything runs and is decided
automatically."

And the flags that would claw that back are refused outright (all three verified live, exit 1,
before any model turn):

```
$ kimi -p 'hi' --plan
error: Cannot combine --prompt with --plan.
$ kimi -p 'hi' --auto
error: Cannot combine --prompt with --auto.
$ kimi -p 'hi' -y
error: Cannot combine --prompt with --yolo.
```

So headless mode has no read-only setting, no approval channel, and no way to refuse a call.
There is no `--exclude-tools`, no `--allowed-tools`, no `--json-schema`, no `--input-format`
and no `--sandbox` anywhere in `kimi --help` or its subcommands (verified against the full
help output). A native `provider: kimi` adapter built on `-p` could not make the read-only
guarantee at all. **That, more than the presence of `kimi acp`, is why this provider is an ACP
provider.**

### The permission policy chain

Both surfaces share one gate. `AgentPermissionPolicyService` evaluates policies in order and
takes the first that returns a verdict (read off the bundle):

1. `auto-mode-ask-user-question-deny`
2. `user-configured-deny` — the operator's own `permissions` deny rules
3. `dangerous-command-ask` — registered only when `nonInteractive` is false
4. `auto-mode-approve`
5. `session-approval-history`
6. `user-configured-ask`
7. `user-configured-allow` — the operator's own allow rules
8. `sensitive-file-access-ask`
9. `git-control-path-access-ask`
10. `yolo-mode-approve`
11. `default-tool-approve`
12. `git-cwd-write-approve`
13. `fallback-ask`

Only headless print mode sets `nonInteractive: true` (read off the bundle — it is the single
occurrence), so row 3 is **present** in an ACP session and absent under `kimi -p`: the surface
with no approval channel is also the one that stops asking about dangerous commands.

`fallback-ask` at the end is why ACP's `default` mode works at all: anything no earlier policy
claims becomes an `ask`, which the ACP server turns into `session/request_permission` and
Sirdar's `PermissionPolicy` answers. Three earlier rows are the problem.

**`default-tool-approve` approves a fixed list without asking** (read off the bundle, verbatim):

```
Read  Grep  Glob  ReadMediaFile  SetTodoList  TodoList  TaskList  TaskOutput  WaitFor
CronList  WebSearch  FetchURL  Agent  AgentSwarm  AskUserQuestion  NotifyUser  Skill
EnterPlanMode  ExitPlanMode  CreateGoal  GetGoal  SetGoalBudget  UpdateGoal  select_tools
```

`WebSearch` and `FetchURL` are on it, so **`permissions.fetch` can never be consulted** — no
fetch ever reaches the client as a permission request. `Agent` and `AgentSwarm` are on it too,
which matters below.

**`git-cwd-write-approve` approves in-workspace writes without asking.** Its evaluate, read off
the bundle: for a tool named `Write` or `Edit`, on a posix path class, where every write access
is within the workspace directory or an `--add-dir` additional directory, and the workspace is
inside a git work tree, it returns `{kind: "approve"}`. A Sirdar triage workspace is always a
git checkout, so in ACP **`default` mode a write into the repository is approved before the
permission request is built.** Sirdar would never be asked about exactly the writes the
read-only guarantee exists to prevent. `default` mode is therefore not usable for triage.

### Plan mode is a real in-process guard, not an instruction

ACP's `plan` mode maps to `{plan: true, permission: "manual"}` (read off the bundle's
`acpModeToToggles`, which the comment says is compiler-enforced exhaustive). The plan feature
registers its **own** `onBeforeExecuteTool` listener, separate from the permission gate:

```js
async guardToolExecution(event) {
  const toolName = event.toolCall.name;
  const plan = await this.status();
  if (toolName === "ExitPlanMode") {
    if (plan !== null && this.modeService.mode !== "auto")
      event.waitUntil(() => this.review.requestApproval(event));
    return;
  }
  if (plan === null) return;
  if (toolName === "Write" || toolName === "Edit") {
    if (writesOnlyPlanFile(event, plan.path)) { event.allow(); return; }
    event.veto(denyToolExecution(…planModeWriteDeniedMessage(plan.path)));
    return;
  }
  if (toolName === "TaskStop") { event.veto(…); return; }
  if (toolName === "CronCreate" || toolName === "CronDelete") { event.veto(…); return; }
}
```

A `veto` short-circuits the whole `BeforeToolExecuteEmitter`, while `git-cwd-write-approve`
only calls `event.pass(metadata)` — so **in plan mode the veto wins over the in-workspace
approval**. This is a hard, in-process denial of `Write` and `Edit` (except the plan file), not
a line of prompt text. It is stronger than Antigravity's plan mode and stronger than Cursor's
ask mode, both of which were server-side or model-side.

`Bash` is deliberately **not** guarded: the plan-mode reminder the bundle injects says "Use
Bash only when needed; Bash follows the normal permission mode and rules." With
`permission: "manual"`, a Bash call falls through to `fallback-ask` and arrives as
`session/request_permission`, where `permissions.bash` decides. That is the right shape — it is
the same posture `provider: acp` already applies to every other agent.

`ExitPlanMode` is the escape hatch, and it is gated: while plan mode is active and the
permission mode is not `auto`, it requests approval. Over ACP that becomes a permission request
the client can refuse. **A client that sets plan mode must be ready to refuse `ExitPlanMode`**,
or a long enough session can talk its way out of the guarantee. Sirdar's `PermissionPolicy`
sees an unknown tool kind there and denies it, which is the right answer by accident rather
than by design — worth making deliberate.

### The hole: subagents

`Agent` and `AgentSwarm` are on `default-tool-approve`, so spawning a subagent is approved
without asking. And the subagent is created like this (read off the bundle):

```js
const created = this.agentLifecycle.handleOf(createdContext.agentId);
created.accessor.get(IAgentPermissionModeService).setMode("auto");
```

Plan-mode state is per-agent (`agentState.get(planKey)`), so a freshly created subagent is
**not** in plan mode **and** is in permission mode `auto`. Nothing it does produces a
`session/request_permission`, and nothing vetoes its writes. The enter-plan-mode documentation
actively encourages the model down this path: "use `Agent(subagent_type="explore")` to
investigate first when the `Agent` tool is available."

No flag, and no configuration Sirdar may write, removes the `Agent` tool. This is the one gap
in the plan-mode guarantee and it should be stated plainly rather than papered over. It was
**not** exercised — the quota ran out first — so whether an explore subagent actually writes
anything in practice is unknown; what is established is that if it tried, nothing would stop
it and nobody would be asked.

### What the operator's own config can do to a run

`user-configured-deny` (row 2) and `user-configured-allow` (row 7) both sit **above**
`fallback-ask`, so `permissions` rules in `~/.kimi-code/config.toml` silently decide calls
before the ACP client is ever asked. Sirdar cannot see that file, must not write it, and cannot
override it. This is the same caveat `provider: acp` already carries for Gemini CLI and Goose,
and the same one `docs/research/10-antigravity-wire-formats.md` records for `agy`.

### Hooks exist, and they fail open

Kimi has external hooks configured as `[[hooks]]` entries in `config.toml` —
`{event, matcher, command, timeout}`, with `timeout` an integer 1–600 seconds (read off the
bundle's zod schema). The event vocabulary is `PreToolUse`, `PostToolUse`,
`PostToolUseFailure`, `PermissionRequest`, `PermissionResult`, `UserPromptSubmit`,
`UserPromptQueued`, `TurnStarted`, `Stop`, `StopFailure`, `Interrupt`, `SessionStart`,
`SessionEnd`, `SessionHeartbeat`, `SubagentStart`, `SubagentStop`, `TaskStarted`, `PreCompact`,
`PostCompact`, `Notification`. `PreToolUse` can block a call and supply a reason.

Two reasons Sirdar does not use them. First, the only places the config is read are
`<KIMI_CODE_HOME>/config.toml` (the operator's own file) and
`<projectRoot>/.kimi-code/local.toml` (inside the customer's repository) — both outside
`.sirdar/`, which is the same wall the Cursor and Antigravity spikes hit. Second, `runHook`
returns `allowResult(...)` when the hook fails to spawn **and** when it times out (read off the
bundle), so Kimi's hooks **fail open**, with no `failClosed` option of the kind Cursor offers.
A mediator that lets the call through whenever it breaks is not a guarantee.

`KIMI_CODE_HOME` would relocate the whole Kimi home, which is the one way Sirdar could own a
`config.toml` carrying deny rules. It would also relocate `credentials/`, where the OAuth token
lives (`storage = "file"`, `key = "oauth/kimi-code"` in the default config), so the session
would arrive unauthenticated unless Sirdar copied or linked the operator's refresh token into a
directory of its own — which is precisely what a credential-handling rule exists to prevent.
Not pursued; recorded here because it is the only lever that exists, not because it is a good
one.

## Quota

The account is on the free **Kimi Code** tier and its monthly quota was already spent when the
capture began. The refusal is clean and worth recording, because it is what a spent plan looks
like on both surfaces.

Over ACP, `session/prompt` answers with a JSON-RPC error and no partial stream (verified):

```json
{"jsonrpc":"2.0","id":99,"error":{"code":-32000,
 "message":"Authentication required: 403 You've reached your monthly usage limit for this billing cycle. Your quota will be refreshed in the next cycle. To continue now, purchase extra usage or upgrade your plan: https://www.kimi.com/membership/subscription?tab=quota"}}
```

`-32000` is the generic server error; the "Authentication required:" prefix is Kimi mapping its
own `provider.auth_error` code onto ACP's auth error class (the bundle's `AUTH_ERROR_CODES` set
is `provider.auth_error`, `auth.login_required`, `auth.token_missing`,
`auth.token_unauthorized`, `auth.provisioning_required`, `auth.model_not_resolved`). So **a
spent quota is indistinguishable from a broken login on the wire, except by reading the message
text.** A client that reacts to an auth error by telling the operator to log in again will send
them somewhere useless.

Headless, the same condition is exit 1 with the message on stderr (verified):

```
$ kimi -p 'reply with the word ok' --output-format stream-json
{"role":"meta","type":"system.version","version":"0.43.1"}
error: failed to run prompt: provider.auth_error: 403 You've reached your monthly usage limit …
See log: /Users/srivathsanv/.kimi-code/logs/kimi-code.log
```

Everything below this line that would have needed a model reply — a completed tool call, a
denial arriving as `session/request_permission`, plan mode actually refusing a write, whether
the JSON-only instruction is honoured — is therefore **unexercised**.

## Headless output shapes, for the record

`--output-format stream-json` is NDJSON, but it is **OpenAI chat messages**, not an event
stream (read off the bundle's `PromptJsonWriter`; only the first line was seen live):

```json
{"role":"meta","type":"system.version","version":"0.43.1"}
{"role":"assistant","content":"…","tool_calls":[{"type":"function","id":"…",
   "function":{"name":"Bash","arguments":"{…}"}}]}
{"role":"tool","tool_call_id":"…","content":"…"}
{"role":"meta","type":"turn.step.retrying","failed_attempt":1,"next_attempt":2,
 "max_attempts":3,"delay_ms":1000,"error_name":"…","error_message":"…","status_code":429}
{"role":"meta","type":"session.resume_hint","session_id":"session_…",
 "command":"kimi -r session_…","content":"To resume this session: kimi -r session_…"}
```

Three things a parser would have to live with: assistant text is **accumulated and flushed
whole**, not streamed as deltas; thinking is **dropped entirely** in JSON mode
(`writeThinkingDelta()` is an empty method); and there is **no result line** — no usage, no
turn count, no cost, no stop reason. The session id appears only in the trailing
`session.resume_hint`.

## Models

Four, all through the managed `kimi-code` provider (verified, from `session/new`'s
`configOptions` and from `~/.kimi-code/config.toml`):

| Alias | Display name | Max context | Efforts | Default effort |
|---|---|---|---|---|
| `kimi-code/kimi-for-coding` | K2.8 Preview | 1 048 576 | low, high, max | max |
| `kimi-code/kimi-for-coding-highspeed` | K2.7 Code Highspeed | 262 144 | — | — |
| `kimi-code/k3` | K3 | 1 048 576 | low, high, max | high |
| `kimi-code/k3-256k` | K3-256k | 262 144 | low, high, max | high |

`kimi provider list` reports `managed:kimi-code  type=kimi  models=4  source=oauth`, and
`kimi provider add <url>` / `kimi provider catalog` can import third-party providers from a
registry or from models.dev (verified from help; not exercised). The default is
`kimi-code/kimi-for-coding`. No price appears anywhere — see "Usage and cost".

An unknown alias fails before any request: `error: failed to run prompt: Model "nope" is not
configured in config.toml.`, exit 1 (verified).

## MCP servers

Three discovery locations (read off the bundle's `/mcp-config` skill, which is the authority
the CLI gives its own model):

- `<KIMI_CODE_HOME>/mcp.json` — user-global, default `~/.kimi-code/mcp.json`
- `<cwd>/.kimi-code/mcp.json` — Kimi's project file, keyed on the **current working directory**,
  not the project root
- `<projectRoot>/.mcp.json` — read as a Claude-compatible file

Shape is the familiar `{"mcpServers":{"name":{"command":…,"args":[…],"env":{…}}}}`. Global
defaults `[mcp] startup_timeout_ms` / `[mcp] tool_timeout_ms` in `config.toml`, overridable per
server and by `KIMI_MCP_STARTUP_TIMEOUT_MS` / `KIMI_MCP_TOOL_TIMEOUT_MS`.

`session/new` accepts an `mcpServers` array and the agent advertises `mcpCapabilities`
`{http: true, sse: true}` (verified — an empty array was accepted). Whether the servers a
client passes there are **added to** the three files above or replace them was not exercised;
every other ACP agent adds, and nothing in the bundle suggests otherwise. So
**`mcp.workspaceOnly` is not enforceable here**, the same as for every `provider: acp` agent,
and `sirdar doctor` already says so for ACP generally.

Print mode warns on stderr about MCP servers held back by workspace trust
(`listTrustGatedMcpServers`, read off the bundle) — Kimi keeps a per-workspace trust record in
`~/.kimi-code/workspace-trust/` and `workspaces.json` (verified: both exist).

## Skills leak in from the operator's machine

`session/new`'s `available_commands_update` on this machine listed the owner's **own**
Claude-style skills — `skill:embedded-captions`, `skill:faceless-explainer`, and two dozen more
— alongside Kimi's built-ins (verified). Kimi auto-discovers `.agents/skills` at both user and
project level (read off the bundle: `USER_GENERIC_DIRS = [".agents/skills"]`,
`PROJECT_GENERIC_DIRS = [".agents/skills"]`).

`--skills-dir <dir>` "Load skills from this directory instead of auto-discovered user and
project directories" (verified from help, not exercised) would shut that off — but it is a
**command-line** flag, and `kimi acp` takes no such flag, so an ACP session cannot use it. A
Sirdar run on this provider gets whatever skills the operator happens to have installed, in the
model's context, unasked. Worth knowing; not, on its own, a safety problem.

## Environment

The variables the binary reads, from its own strings (**read off the bundle** — none was
exercised). The ones that redirect a credential or relocate state are what matter:

| Variable | What it does | Recommendation |
|---|---|---|
| `KIMI_API_KEY`, `MOONSHOT_API_KEY`, `KIMI_REGISTRY_API_KEY` | API-key auth instead of the OAuth login | strip |
| `KIMI_BASE_URL`, `KIMI_CODE_BASE_URL`, `KIMI_CODE_OAUTH_HOST`, `KIMI_OAUTH_HOST` | point the session at another endpoint | strip — the same hazard `ANTHROPIC_BASE_URL` is stripped for |
| `KIMI_CODE_CUSTOM_HEADERS` | arbitrary headers on every request | strip |
| `KIMI_CODE_HOME` | relocates the whole `~/.kimi-code` tree, credentials included | leave alone; changing it breaks the login (see "Hooks") |
| `KIMI_WEB_SEARCH_BASE_URL`, `KIMI_WEB_SEARCH_API_KEY`, `KIMI_WEB_FETCH_BASE_URL`, `KIMI_WEB_FETCH_API_KEY` | redirect the built-in search and fetch services | strip |
| `ANTHROPIC_*`, `OPENAI_API_KEY`, `OPENAI_BASE_URL` | recognised for imported/custom providers | strip |
| `KIMI_MODEL_*` (`NAME`, `MAX_TOKENS`, `TEMPERATURE`, `TOP_P`, `THINKING_EFFORT`, `OUTPUT_FORMAT`, …) | override model parameters | strip, so the session is the one Sirdar configured |
| `KIMI_CODE_DANGEROUS_COMMAND_GUARD` | toggles the dangerous-command policy | strip |
| `KIMI_CODE_BACKGROUND_*`, `KIMI_SUBAGENT_TIMEOUT_MS`, `KIMI_CODE_AGENT_SWARM_MAX_CONCURRENCY` | background-task and subagent limits | strip |
| `KIMI_CODE_DISABLE_HOST_CHECK`, `KIMI_CODE_CORS_ORIGINS`, `KIMI_CODE_ALLOWED_HOSTS` | `kimi web`'s DNS-rebinding and CORS guards | strip |
| `KIMI_CLI_NO_AUTO_UPDATE` / `KIMI_CODE_NO_AUTO_UPDATE` | suppress the auto-updater | **set**, so a run never races an upgrade (`tui.toml` defaults `[upgrade] auto_install = true`) |

`provider: acp` adds `acp.env` to the child's environment rather than replacing it, so none of
this stripping happens today for kimi. That is a real difference from `provider: claude` and
`provider: codex`, and it is a gap in the ACP adapter generally, not in kimi specifically.

## Usage and cost on the wire

`usage_update` is emitted once after a turn settles, and the bundle's own comment is explicit:
"`used` is the agent's current context token count, `size` the bound model's max context size;
`cost` stays omitted (the engine has no cost data)."

So, exactly as for every other ACP agent:

- **`budget.maxUsd` cannot bite.** There is no cost figure anywhere — not on ACP, not in the
  headless stream, not in `kimi`'s own `/usage` builtin, which prints only
  `Session total: N input, N output`.
- **`budget.maxTurns` counts one turn per `session/prompt`**, so an ACP run normally ends at
  turn one. `budget.maxMinutes` is the bound that works.
- There is no rate-limit event. A spent quota arrives as the `-32000` auth error above.

## Exit codes

Verified, all measured with output redirected so `$?` is the CLI's own:

| Code | Cause |
|---|---|
| 0 | `--version`, `doctor`, `session list`, `provider list`, and an `acp` server that reaches stdin EOF |
| 1 | argument conflict (`-p` with `--plan`/`--auto`/`--yolo`), unknown flag, unknown model, quota exhausted / auth failure |

`kimi doctor` validates `config.toml` and `tui.toml` and exits 0 on this machine. Signals map
to conventional codes in print mode: SIGINT → 130, SIGHUP → 129, SIGTERM → 143 (read off the
bundle).

## Resume

`loadSession: true`, so `session/load` is the ACP resume path and `sirdar resume` works
natively. `sessionCapabilities` also advertises `list`, `resume`, `close`, `delete`, `fork` and
`additionalDirectories` — richer than the protocol requires and richer than Sirdar's adapter
uses.

On the command line the same sessions are reachable as `kimi -S <id>` / `--session <id>`,
`-c` / `--continue`, and a hidden `-r, --resume [id]` (read off the bundle's option table; the
resume hint the CLI prints uses the hidden form). `kimi session list` prints them with
timestamps (verified). `kimi fork` and `kimi export` operate on a session by id.

Print mode refuses to resume a session created elsewhere: `Session "…" was created under a
different directory.` (read off the bundle).

## What this capture did not establish

- **Any model turn.** The quota was spent before the first prompt, so no completed tool call,
  no `session/request_permission` on the wire, no assistant text, no `usage_update` payload and
  no `stopReason` was ever observed.
- **Whether plan mode's veto actually fires.** The guard is unambiguous in the bundle, and it
  is an in-process veto rather than an instruction, but it was not watched refusing a write.
  The same goes for `touch x` arriving as a Bash permission request.
- **Whether a plan-mode subagent writes.** The mechanism that would let it is established; the
  behaviour is not.
- **Whether `session/new`'s `mcpServers` merges with or replaces the three config files.**
- **Whether Kimi honours a JSON-only instruction**, which under ACP is the whole of the
  structured-output mechanism.
- **Every environment variable above.** The names and their meaning come from the binary's
  strings; none was exercised.
