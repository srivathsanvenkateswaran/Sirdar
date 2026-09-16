# ACP agents

`provider: acp` speaks the Agent Client Protocol, one JSON-RPC dialect that about forty coding
agents already understand, so one adapter reaches all of them: Gemini CLI, Goose, OpenCode, Qwen
Code, Kimi CLI, Crush, Junie, Augment, GitHub Copilot CLI, Cursor, Devin, and Claude Code and
Codex through their ACP adapters.

| | |
|---|---|
| Config value | `provider: acp` |
| Launch | `acp.command` and `acp.args`, the agent's own launch command (for example `gemini --experimental-acp`) |
| Environment | `acp.env` is added to the agent's environment, not substituted for it, and holds literal values; leave credentials in your shell and let the agent read them there |
| Read-only mechanism | The agent is put in its read-only session mode; a permission request is answered with the agent's own `reject_once` option and the policy's reason goes to the event log |
| MCP servers | The stdio servers from the workspace's `.mcp.json`, handed over in ACP's `mcpServers` shape |
| What a run gives up | No cost signal, so `budget.maxUsd` rarely fires; one ACP turn is a whole prompt turn, so `budget.maxMinutes` is the bound that works; no rate-limit signal; no schema-constrained output, so the note is parsed from the agent's last JSON object; no reason on a denial |

## In the configuration reference

- [`provider: acp`](../../config.md#provider-acp): the `acp:` block, what the permission
  policy can and cannot reach, session modes, what fails the run, and Kimi Code CLI over ACP.
- [Budgets](../../config.md#budgets): set `budget.maxMinutes` as if it were the only cap.

## Related

- [Research: the ACP protocol](../../research/providers/acp-protocol.md)
- [Research: ACP agents](../../research/providers/acp-agents.md), the ones Sirdar knows about and
  what each is started with
- [Research: Kimi Code wire formats](../../research/12-kimi-wire-formats.md)
