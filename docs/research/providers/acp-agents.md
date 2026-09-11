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
| Kimi CLI (Moonshot) | `kimi` | unverified | Binary; the registry lists no argv |
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
