# Codex

`provider: codex` drives OpenAI's Codex CLI through its app server, against the login the CLI
already holds. A triage session runs inside Codex's own read-only sandbox with approvals on.

| | |
|---|---|
| Config value | `provider: codex` |
| Binary | `codex`, looked up on `PATH`, or the path in `providers.codex.path` |
| Transport | `codex app-server`; the session is a `thread/start` with Sirdar's config merged over the CLI's own |
| Read-only mechanism | The CLI's `sandbox: read-only` for a triage session, with approvals routed to Sirdar's `PermissionPolicy`; a fix session runs with a wider sandbox on its own worktree |
| Your `~/.codex` | Never modified: `config.toml` and `auth.json` are read, and a token refresh inside a run cannot damage the file your own `codex` sessions read. Per-run homes are `sirdar-codex-home-*` directories in `TMPDIR`, swept after 24 hours |
| MCP servers | The workspace's `.mcp.json`, passed as `mcp_servers` config; Codex's TOML config is not a source Sirdar reads for MCP |
| Rate limits | `account/rateLimits/updated` pauses the queue |

## In the configuration reference

- [Providers](../../config.md#providers): `providers.codex.path`.
- [MCP access](../../config.md#mcp-access): how the workspace's servers reach a Codex session,
  and how the copied home keeps your own config untouched.
- [Budgets](../../config.md#budgets): the turn, wall-clock and USD caps every run carries.

## Related

- [Architecture: the three read-only layers](../../architecture.md#the-three-read-only-layers)
- [Research: wire formats](../../research/06-wire-formats.md)
- [Research: licensing under a BYO subscription](../../research/03-licensing-byo-subscription.md)
