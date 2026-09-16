# Cursor Agent CLI

`provider: cursor` drives `cursor-agent` against the login that binary already holds. It is the
one provider whose read-only guarantee Sirdar does not enforce itself, and `sirdar fix` is
refused on it outright.

| | |
|---|---|
| Config value | `provider: cursor` |
| Binary | `cursor-agent` (also installed as `agent`), looked up on `PATH`, or the path in `cursor.path` |
| Model and mode | `cursor.model` (`auto` on a Cursor Free plan) and `cursor.mode`, `ask` or `plan`, both read-only |
| Why Sirdar cannot judge a call | A print-mode session is its own approver: there is no permission channel, no approval request, and no hook Sirdar can install without writing into your repository. `permissions.bash`, `permissions.mcp` and the `PermissionPolicy` are never consulted |
| What holds instead | `--mode ask` or `plan` (a server-side read-only mode), `--exclude-tools` for the six write and mode-switch tools, `--sandbox enabled` for shell calls, `--disable-project-configs` so a checkout cannot widen a run, and `--trust` |
| A completed write | Ends the run. Sirdar watches the stream for a `tool_call` completing on any excluded tool and fails the run when it sees one |
| Fix sessions | Refused |

If your workspace needs the read-only guarantee enforced on your own machine, use
`provider: claude`, `provider: codex`, `provider: qwen` or `provider: openai`.

## In the configuration reference

- [`provider: cursor`](../../config.md#provider-cursor): the `cursor:` block and the full account
  of what a print-mode session can do.

## Related

- [Research: Cursor CLI wire formats](../../research/11-cursor-wire-formats.md), including the
  default-mode session that was watched writing a file with no prompt
