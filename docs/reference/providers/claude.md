# Claude Code

`provider: claude` is the default. Sirdar spawns the `claude` binary you are already signed in
to, in the workspace, with its write tools off and every other tool call sent to Sirdar's
permission policy to answer.

| | |
|---|---|
| Config value | `provider: claude` (the default) |
| Binary | `claude`, looked up on `PATH`, or the path in `providers.claude.path` |
| Billing | `billing: subscription` (default) removes `ANTHROPIC_API_KEY` and the gateway variables from the child environment so the run draws on the CLI's own login; `billing: api` leaves them in place and bills per token |
| Read-only mechanism | `--disallowedTools Write,Edit,MultiEdit,NotebookEdit` on the CLI, plus Sirdar's `PermissionPolicy` answering every `control_request`: `permissions.bash` globs for shell, `permissions.mcp` for MCP tools |
| MCP servers | The workspace's own `.mcp.json`, gated by `mcp.workspaceOnly` |
| Rate limits | The CLI's `rate_limit_event` pauses the queue until the window resets |
| Doctor | A `claude environment` row fails when a gateway variable is set under `billing: subscription`, and names which ones Sirdar is about to strip |

## In the configuration reference

- [Providers](../../config.md#providers): `providers.claude.path` and the two `billing` modes,
  including why `ANTHROPIC_BASE_URL` is stripped under a subscription.
- [MCP access](../../config.md#mcp-access): which servers a session sees and which of their
  tools it may call.
- [Budgets](../../config.md#budgets): the turn, wall-clock and USD caps every run carries.

## Related

- [Architecture: the three read-only layers](../../architecture.md#the-three-read-only-layers)
- [Research: wire formats](../../research/06-wire-formats.md), the CLI's stream-json protocol as
  observed
- [Research: Anthropic-compatible endpoints (spike)](../../research/providers/spike-anthropic-compatible.md),
  what `billing: api` behind a custom base URL does and does not report
