# ACP agents and their launch commands

What to put in the `acp:` block of `.sirdar/config.yaml` for each agent Sirdar knows about.
Sourced from the ACP registry snapshot in `acp-protocol.md` (section 3), taken 2026-09-10.

**Every row here is unverified.** None of these agents has been run against Sirdar's ACP
adapter; the commands are transcribed from the registry, not observed working. What the
adapter *has* been run against is a scripted fake agent
(`internal/provider/acp/testdata/*.jsonl`). Treat a row as a starting point and confirm it with
`sirdar doctor`, which starts the agent, initializes it and prints what it says about itself —
including whether it supports `loadSession` (resume) and image prompts, the two capabilities
that change what a Sirdar run can do with it.

Registry pins an exact version in each `npx` line; the pins below are the ones in that
snapshot and will be stale. Drop the `@version` to take the latest, or pin your own.

## Native ACP agents

These speak ACP themselves, usually behind a flag.

| Agent | `command` | `args` | Notes |
|---|---|---|---|
| Gemini CLI | `gemini` | `["--experimental-acp"]` | The form the scaffold and `docs/config.md` use, for a locally installed CLI. The registry's own entry is `npx` with `["@google/gemini-cli@0.59.0", "--acp"]`, so the flag name differs between versions — unverified which your build takes; `sirdar doctor` settles it in one run |
| Goose (Block) | `goose` | `["acp"]` | Distributed as a binary, no npx wrapper in the registry |
| Qwen Code (Alibaba) | `npx` | `["@qwen-code/qwen-code@0.23.2", "--acp", "--experimental-skills"]` | |
| Kimi CLI (Moonshot) | `kimi` (usually an absolute path, see below) | `["acp"]` | **Verified** against `kimi` 0.43.1 — the one row on this page that has been run. Protocol version 1, `loadSession`, image prompts. Set the session mode to `plan`: in `default` mode a write inside the workspace is approved before Sirdar is asked. `docs/research/12-kimi-wire-formats.md` |
| OpenCode | `opencode` | unverified | Binary; the registry lists no argv |
| Crush | `crush` | unverified | Not found in the registry snapshot at all — may be unregistered or renamed |
| Junie (JetBrains) | `junie` | unverified | Binary; `junie.jetbrains.com` |
| Augment (`auggie`) | `npx` | `["@augmentcode/auggie@0.36.0", "--acp"]` | |
| GitHub Copilot CLI | `npx` | `["@github/copilot@1.0.83", "--acp"]` | |
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
  decides. Sirdar's ACP adapter does **not** send `session/set_mode` today, so this has to be
  done by hand or added to the adapter.
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
  agent's discretion. Watch a first run's event log: an `edit` or `execute` tool call that
  completes without a permission event raises a warning, and that warning is the signal to run
  this agent against a scratch checkout rather than a real one.
- **What MCP servers it already has.** Sirdar adds the workspace's to whatever the agent is
  configured with globally; `mcp.workspaceOnly` cannot reach across ACP.
- **Whether it honours the JSON-only instruction.** ACP has no schema field, so this is the
  whole of the structured-output mechanism. An agent that habitually answers in prose costs a
  retry turn on every run.
