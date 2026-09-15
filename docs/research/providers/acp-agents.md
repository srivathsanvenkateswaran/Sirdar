# ACP agents and their launch commands

What to put in the `acp:` block of `.sirdar/config.yaml` for each agent Sirdar knows about.
Sourced from the ACP registry snapshot in `acp-protocol.md` (section 3), taken 2026-09-10.

**Three rows are verified live (2026-09-15): GitHub Copilot CLI, OpenCode and Kimi CLI** — see
"Verified live" below for what each one actually did. **Every other row is unverified**: those
agents have not been run against Sirdar's ACP adapter and their commands are transcribed from the
registry, not observed working. What the adapter is otherwise exercised against is a scripted
fake agent (`internal/provider/acp/testdata/*.jsonl`). Treat an unverified row as a starting point
and confirm it with `sirdar doctor`, which starts the agent, initializes it and prints what it
says about itself — including whether it supports `loadSession` (resume) and image prompts, the
two capabilities that change what a Sirdar run can do with it.

Registry pins an exact version in each `npx` line; the pins below are the ones in that
snapshot and will be stale. Drop the `@version` to take the latest, or pin your own.

## Native ACP agents

These speak ACP themselves, usually behind a flag.

| Agent | `command` | `args` | Notes |
|---|---|---|---|
| Gemini CLI | `gemini` | `["--experimental-acp"]` | The form the scaffold and `docs/config.md` use, for a locally installed CLI. The registry's own entry is `npx` with `["@google/gemini-cli@0.59.0", "--acp"]`, so the flag name differs between versions — unverified which your build takes; `sirdar doctor` settles it in one run |
| Goose (Block) | `goose` | `["acp"]` | Distributed as a binary, no npx wrapper in the registry |
| Qwen Code (Alibaba) | `npx` | `["@qwen-code/qwen-code@0.23.2", "--acp", "--experimental-skills"]` | |
| Kimi CLI (Moonshot) | `kimi` (usually an absolute path, see below) | `["acp"]` | **Verified 2026-09-15** against `kimi` 0.43.1, handshake only — the account's quota was spent before a model turn. Protocol version 1, `loadSession`, image prompts. Sirdar selects the `plan` session mode: in `default` mode a write inside the workspace is approved before Sirdar is asked. `docs/research/12-kimi-wire-formats.md` |
| OpenCode | `opencode` | `["acp"]` | **Verified 2026-09-15**, triage and fix |
| Crush | `crush` | unverified | Not found in the registry snapshot at all — may be unregistered or renamed |
| Junie (JetBrains) | `junie` | unverified | Binary; `junie.jetbrains.com` |
| Augment (`auggie`) | `npx` | `["@augmentcode/auggie@0.36.0", "--acp"]` | |
| GitHub Copilot CLI | `copilot` | `["--acp"]` | **Verified 2026-09-15**, triage, rca and fix. The registry's own entry is `npx` with `["@github/copilot@1.0.83", "--acp"]`; the locally installed binary is what was run |
| Cursor | `cursor-agent` | unverified | Binary; docs at `cursor.com/docs/cli/acp` |
| Devin (Cognition) | `devin` | unverified | Binary |

## Adapters

These wrap an agent that does not speak ACP itself.

| Agent | `command` | `args` | Notes |
|---|---|---|---|
| Claude Code | `npx` | `["@agentclientprotocol/claude-agent-acp@0.76.0"]` | Formerly `@zed-industries/claude-code-acp`, which now redirects. Needs Claude Agent SDK auth (`ANTHROPIC_API_KEY`); whether it accepts a subscription CLI login is unverified |
| Codex | `npx` | `["@agentclientprotocol/codex-acp@1.11.0"]` | Formerly `@zed-industries/codex-acp`. A Rust binary distributed over npm |

Sirdar already drives both of these natively — `provider: claude` and `provider: codex` — and
those adapters are better: they carry a cost signal, a rate-limit signal and schema-constrained
output, none of which ACP has a field for. Reach for the ACP path here only to test the ACP
adapter itself, or to run a version of one of them that Sirdar's native adapter does not
handle.

## Kimi Code CLI, in detail

The only agent on this page that has actually been driven. `kimi acp --help` says "Run
kimi-code as an Agent Client Protocol (ACP) server over stdio", and it is a full
implementation — `@agentclientprotocol/sdk` 1.3.0, protocol version 1, `loadSession`, image
and embedded-context prompts, HTTP and SSE MCP transports, `session/set_mode` and
`session/set_config_option`.

```yaml
provider: acp
acp:
  command: /Users/you/.kimi-code/bin/kimi
  args: ["acp"]
  env: {}
budget:
  maxMinutes: 20
```

The installer does **not** put `kimi` on `PATH`. It lands in `~/.kimi-code/bin/kimi`, so
`acp.command` normally has to be the absolute path; "command not found" from `sirdar doctor` is
the expected first result otherwise.

Four things to know before running one against a repository you care about, all from
`docs/research/12-kimi-wire-formats.md`:

- **`default` mode is not read-only.** A policy the bundle calls `git-cwd-write-approve`
  approves a `Write` or `Edit` whose targets all sit inside the workspace, whenever the
  workspace is a git checkout — which a Sirdar workspace always is. The call never becomes a
  `session/request_permission`, so Sirdar's policy never sees it. Use `plan`.
- **`plan` mode is a genuine in-process veto**, not a prompt instruction: the plan feature
  registers its own `onBeforeExecuteTool` listener and vetoes `Write` and `Edit` outside the
  plan file, and a veto short-circuits the in-workspace approval above. `Bash` is deliberately
  left to the normal chain, so it arrives as a permission request and `permissions.bash`
  decides. Sirdar's ACP adapter selects this mode itself now, on every triage and rca session
  against an agent that offers it; `acp.mode` overrides the id.
- **A subagent escapes both.** `Agent` and `AgentSwarm` are approved without asking, and a
  subagent is created with its permission mode forced to `auto` and without the parent's
  plan-mode state. Nothing it does asks, and nothing vetoes its writes. No flag removes the
  tool.
- **`FetchURL` and `WebSearch` are approved without asking**, so `permissions.fetch` is never
  consulted on this agent.

The free "Kimi Code" tier's quota is small enough that it can be spent before the first real
run. It surfaces as a `-32000` JSON-RPC error on `session/prompt` whose message begins
`Authentication required: 403 You've reached your monthly usage limit…` — indistinguishable
from a broken login except by reading the text.

## Verified live, 2026-09-15

Three agents were driven end to end against the sandbox workspace
(`~/Documents/Personal/sirdar-sandbox`). What each one did:

| Agent | Launch | Triage | RCA | Fix | What it showed |
|---|---|---|---|---|---|
| GitHub Copilot CLI | `copilot --acp` | pass | pass | pass | The only agent that finished all three kinds. Earlier fix attempts failed on the report shape — an answer carrying extra root properties, and a turn that ended with nothing in it — before one came back clean. Its mode ids are URLs (`…/session-modes#plan`), which no mode was selected against until the re-test |
| OpenCode | `opencode acp` | pass | **fail** | pass | The rca answered a good root-cause analysis with the `rca` object's own fields written at the root, so the document was missing both required top-level keys, and the schema retry repeated the same shape. That failure is what the sharpened retry now names explicitly (`docs/config.md`, "No schema-constrained output"). It advertises no `availableModes` at all — its session mode is a `configOptions` entry — and it re-sends the host's whole 31-command catalogue on every `available_commands_update` |
| Kimi CLI | `~/.kimi-code/bin/kimi acp` | — | — | — | **No model turn.** The account's monthly quota was already spent, and `session/prompt` came back as a `-32000` error before a token was billed. The handshake, the session-modes machinery and `session/set_mode` were all exercised for real; everything downstream of a model reply was not |

Two things came out of these runs and are now in the adapter:

- **Session modes.** Kimi advertises `availableModes` on `session/new` and serves
  `session/set_mode`, and its `default` mode approves any write inside a git working tree before
  a permission request is even built. A client that does not select the read-only mode is asked
  about nothing. Sirdar now selects one; `acp.mode` overrides the choice.
- **A completed tool call nobody approved ends the run.** It used to be a warning in the event
  log. In a triage or rca session it is now an `EvBreach`: the session is cancelled, no note is
  written and no row reaches the register. `docs/config.md` has the exact rules, including what
  stays a warning in a fix run.

A second pass over the same three runs found four more, all now in the adapter
(`docs/config.md`, "Session modes"):

- **A mode id can be a URL.** Copilot's are
  `https://agentclientprotocol.com/protocol/session-modes#plan` and `#agent`, `#autopilot`.
  Matching whole ids alone found no read-only mode in a list that plainly had one, so ids are
  now matched on their last fragment or path segment too; the agent's own id is what goes back
  on the wire.
- **A mode can be a config option instead of a mode.** OpenCode's `session/new` reply has no
  `availableModes` and a `configOptions` entry `{id: "mode", currentValue: "build", options:
  [build, plan]}`. Sirdar now sets it with `session/set_config_option` — `plan` for triage and
  rca, `build` for fix — when an agent lists no modes.
- **`available_commands_update` can be enormous.** OpenCode sends the host's whole
  slash-command catalogue, descriptions and all, every time any of it changes: about 15 KiB a
  copy, and one run's event log grew past 1300 system events of largely the same text. The
  adapter now keeps one line — the count and the first three names — and none of the bodies.
- **An empty turn is not yet a failure.** Copilot ended two turns with nothing to say before
  answering properly on the third; the run passed and carried two error lines for it. That
  warning is now only turned into an error if the run does end with no answer.

## Worked example

```yaml
provider: acp
acp:
  command: gemini
  args: ["--experimental-acp"]
  env: {}
budget:
  maxMinutes: 20   # the bound that actually works for ACP; see docs/config.md
```

Then:

```
sirdar doctor
```

A working agent produces two rows — the agent's name and protocol version, and its
capabilities. A row that fails carries the agent's own last stderr line, which is where "command
not found" and "not logged in" actually appear.

## What to check on an agent you have not run before

- **Does it answer `initialize` at all?** `sirdar doctor` is that check. An agent that prints a
  banner before the handshake is fine — non-JSON lines are ignored — but one that needs an
  interactive login will hang until the doctor timeout.
- **`loadSession`.** Without it, `sirdar resume` starts a fresh session instead of continuing
  the old one, and a schema retry that cannot be sent on the running session has nowhere to go.
- **Image prompts.** Without them, screenshot attachments are named in the prompt text rather
  than sent; the agent can still read them off disk if it has a read tool.
- **Whether it sends `usage_update`, and whether that carries a `cost`.** Most do not. With no
  cost, `budget.maxUsd` never triggers — and since a whole prompt turn counts as one turn,
  `budget.maxTurns` does not either. `budget.maxMinutes` is the bound.
- **Whether it asks permission, and for what.** `session/request_permission` is sent at the
  agent's discretion. An `edit`, `delete`, `move` or `execute` tool call that completes without
  a permission event fails a triage or rca run outright — `read-only breach:` in the run's
  reason — so an agent that does that is one to run against a scratch checkout, or not at all.
  A `fetch` that asks nobody is a warning instead.
- **Whether it offers session modes, and what its read-only one is called.** `sirdar doctor`
  does not show these (they ride on `session/new`, which doctor does not open), so the first run's
  event log is where to look: `acp mode <id> selected for this read-only session` means one was
  found, and a line naming what the agent offered means none of them matched — set `acp.mode`
  to the right id. An agent that lists no modes may still have one under `configOptions`, which
  Sirdar reads as well; a bare word is what `acp.mode` takes either way, even where the agent's
  own id is a URL.
- **What MCP servers it already has.** Sirdar adds the workspace's to whatever the agent is
  configured with globally; `mcp.workspaceOnly` cannot reach across ACP.
- **Whether it honours the JSON-only instruction.** ACP has no schema field, so this is the
  whole of the structured-output mechanism. An agent that habitually answers in prose costs a
  retry turn on every run.
