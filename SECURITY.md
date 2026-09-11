# Security policy

## Supported versions

Sirdar is pre-1.0. Only the `main` branch is supported; there are no maintained release
branches yet. If you find a problem in a tagged release, check whether it's already fixed on
`main` before reporting.

## Reporting a vulnerability

Please report privately, not in a public issue. Use GitHub's private vulnerability reporting on
this repository: go to the **Security** tab → **Report a vulnerability**. If that's not
available to you for some reason, open a GitHub issue asking the maintainer
([@srivathsanvenkateswaran](https://github.com/srivathsanvenkateswaran)) to open a channel for a
private report, without describing the vulnerability itself in the issue.

There's no dedicated security email; don't email one you find elsewhere for this repo unless it
already appears in the repository itself.

### What to expect

This is a personal project maintained by one person, so treat these as targets, not guarantees:

- Acknowledgement within 7 days.
- After that, how long a fix takes depends on severity and my availability — I'll tell you what
  I can when I acknowledge the report.

## Threat model

Sirdar runs on your machine, reads tickets from a tracker and helpdesk you configure, spawns a
coding agent (Claude Code, Codex, or its own loop against an OpenAI-compatible endpoint) inside
your codebase, and writes a note. It never opens a pull request and never writes back to the
tracker or helpdesk. This section is the one-page version of what that means for credentials,
what the agent can do, and where the network surface is.

### Credentials

- Every credential in `.sirdar/config.yaml` — adapter tokens, API keys, PATs — must be an
  `env:NAME` or `keychain:SERVICE` reference. A literal secret in config is rejected at load
  time.
- Resolved secrets are held in memory only. They are never written to a run directory
  (`bundle/`, `prompt.md`, `events.jsonl`, `state.json`), and never appear in a note.
- Every environment variable a credential reference names is stripped from the environment the
  agent process inherits, so a session that can run shell commands cannot read the credential
  back out of its own environment. `ANTHROPIC_API_KEY` is stripped from the Claude provider's
  child process the same way, so a triage run bills against your subscription login rather than
  an API key sitting in your shell.
- A resolved secret is never logged, including in a warning or error surfaced to a run or a
  note.

### What the agent can and cannot do

The read-only guarantee is enforced in three independent layers, not one:

- **Claude**: `PermissionPolicy` denies `Edit`, `Write`, `MultiEdit`, and `NotebookEdit`
  outright, and only allows a `Bash` command whose every segment matches an operator-configured
  allow-list and whose path arguments stay inside the workspace root
  (`internal/provider/policy.go`'s `MatchCommand`). The CLI is also started with
  `--disallowedTools Write,Edit,MultiEdit,NotebookEdit` as a second line of defense.
- **Codex**: runs with `sandbox: read-only`.
- **`provider: openai`**: Sirdar's own agent loop only offers read-only tools
  (`internal/agenttools`) — there is no write tool to deny in the first place — plus the same
  `MatchCommand` allow-list and root check for its `bash` tool.
- **MCP tools** are gated by `permissions.mcp`. With no patterns configured, a tool is allowed
  unless a word in its own name is a write verb (`create`, `update`, `delete`, `send`, `deploy`,
  and so on) and not also a read word (`query`, `get`, `list`, `search`, and so on) — this is a
  naming heuristic, not a semantic check of what the tool actually does. `mcp.workspaceOnly`
  (default `true`) additionally limits which MCP servers a session can see at all, to whatever a
  `.mcp.json` in the workspace names.
- **Downloads** (attachments, pagination links) are host-checked before a credential is sent:
  an adapter refuses to send its Authorization header to a host outside the configured API, and
  refuses to follow a redirect off that host. See `docs/adapters.md` and
  `docs/research/08-httpx-extraction.md`.

### `sirdar serve`

`sirdar serve` binds to a loopback address (`127.0.0.1:7777` by default) and serves the desktop
UI's HTTP/SSE API with **no authentication**. Binding a non-loopback address requires
`--allow-remote`, and passing it is a decision to expose an unauthenticated API to whatever can
reach that address — treat it the same as running any other unauthenticated local dev server on
a shared or untrusted network. There is no built-in way to add authentication; don't pass
`--allow-remote` on a network you don't trust.

### Known limitations

- **Bash path confinement is a heuristic, not a sandbox.** `MatchCommand`'s root check reads the
  command as text: it does not follow symlinks, does not know which arguments a given program
  treats as paths, and cannot see a path a program derives at runtime. It catches the obvious
  ways out of the workspace, not all of them. Anything that needs real confinement needs a
  container, not an allow-list.
- **Claude Code self-approves some read-shaped Bash commands** before they reach Sirdar's own
  permission-prompt hook, so `permissions.bash` only ever sees the commands Claude actually asks
  about — a command Claude decides is safe enough to run without asking is not filtered by
  Sirdar's allow-list at all. This is a property of the CLI, not something Sirdar controls.
- **`provider: openai` has no resume handle.** A blocked or interrupted run under
  `provider: openai` cannot be continued with `sirdar resume`; it has to be re-run from scratch.
- **The MCP write-verb heuristic is a naming convention, not a capability check.** A tool named
  in a way that doesn't match a write verb but does something Sirdar would consider a write is
  not caught by the default heuristic — set `permissions.mcp` explicitly for a run you want to be
  read-only by construction rather than by naming convention.
