# Changelog

This file is hand-maintained. The `Unreleased` section is a running summary of what
exists on `main` since the last tag; each release moves it under a version heading.
The changelog goreleaser attaches to a GitHub release itself is generated separately
from conventional-commit prefixes in the git log, and is not a replacement for this.

## Unreleased

Sirdar as it stands today, before the first tagged release:

A command-line harness (`sirdar init`, `doctor`, `triage`, `rca`, `resume`, `runs`,
`register`, `serve`) that reads an engineering-support ticket from a tracker and a
helpdesk, hands it to a coding agent inside a read-only workspace, and writes a
Triage Note recording the agent's root-cause hypothesis for a human to review. Once a
human has made and merged the actual fix, `sirdar rca` writes the RCA and Resolution
notes that record what changed and why — Sirdar never opens a PR or writes to a
tracker or helpdesk itself.

Three ways to drive a run: `provider: claude` and `provider: codex` spawn the Claude
Code or Codex CLI already installed and signed in, so a run counts against the plan
already being paid for; `provider: openai` runs Sirdar's own agent loop against any
OpenAI-compatible endpoint (OpenRouter, Groq, Together, DeepSeek, Moonshot, Zhipu, or
a local Ollama/vLLM/llama.cpp), billed per token against a budget set in config.

Built-in tracker adapters for Jira Cloud, Jira Data Center, Linear, Azure DevOps, and
Rally; built-in helpdesk adapters for Zoho Desk (with OAuth refresh), Zendesk, and
Freshdesk. Anything else — an internal tracker, a different helpdesk — is a separate
executable speaking a small line-delimited JSON protocol over stdin/stdout, so its
credentials and vendor-specific code never touch Sirdar's core.

A Wails v2 desktop app under `desktop/` sharing the same React frontend that
`sirdar serve` serves over HTTP; the desktop build talks to the core in-process
through a bound Go bridge instead of over the network, and observes run directories
rather than hooking the runner directly.

The read-only guarantee (Sirdar reads a workspace and runs allow-listed commands in
it, but never edits or writes) is enforced per provider: `--disallowedTools` and a
command-pattern policy for Claude, `sandbox: read-only` for Codex, and for
`provider: openai` the tool set itself plus command-pattern matching and an MCP
write-verb heuristic.

Release packaging: darwin/linux/windows binaries on amd64/arm64 via goreleaser, deb/rpm
packages, a Homebrew tap, and desktop app zips for all three platforms — see
`docs/release.md`.

- Tagging a release now builds and drafts it end to end: CLI archives for all three platforms,
  deb/rpm packages, checksums, a Homebrew tap formula, and desktop app zips, all attached to one
  GitHub release that stays a draft until a human clicks Publish (`docs/release.md`).
- Added CONTRIBUTING, SECURITY and CODE_OF_CONDUCT, issue and pull request templates, and
  Dependabot version updates, plus an architecture map for new contributors
  (`docs/architecture.md`).
- Added a documentation site built with MkDocs and published to GitHub Pages, covering
  concepts and getting started alongside the existing reference docs.
- Added support for triaging Arabic and other right-to-left tickets: a `language` config block,
  note fields that keep the customer's original-language complaint alongside a draft in the
  customer's language, and an RTL-aware desktop UI.
- Credentials can now be stored in a `file:` or a `cmd:` reference, in the Linux Secret Service
  (libsecret), or in Windows Credential Manager, alongside the existing `env:` and macOS
  Keychain support (`docs/credentials.md`).
- Every built-in adapter, tracker and helpdesk alike, now shares one internal HTTP client with
  consistent host-trust checks, redirect handling, and response-size caps.
- Added `provider: acp`, an Agent Client Protocol client that can drive any ACP-speaking coding
  agent (Gemini CLI, Goose, OpenCode, and others) the same way Sirdar already drives Claude Code
  and Codex.
- Added inbound webhooks: `sirdar serve` can now be triggered directly by a tracker or helpdesk
  when a ticket is assigned, with per-source signature verification (`docs/webhooks.md`).
- Added run-completion notifications to Slack, Microsoft Teams, or any HTTP endpoint you run
  yourself, posting a metadata-only digest (never the note's content) with a timestamped HMAC
  signature (`docs/notifications.md`).
- Added three more built-in helpdesk adapters: Help Scout, Intercom, and HubSpot Service Hub.
- Added a built-in Gorgias helpdesk adapter (`adapter: gorgias`): per-account host from
  `account` or `baseUrl`, HTTP Basic with the login `email` and an `apiKey` credential
  reference, the cursor-paginated `/api/messages` feed as the thread, and attachment
  downloads that send the key only to the configured account host.
- Added `sirdar eval`, which replays a golden set of previously triaged tickets and scores a new
  run against the assertions and note you recorded for each one, and `sirdar golden add` to build
  that set from a completed run (`docs/eval.md`).
- Added `sirdar fix`, a human-gated mode that lets the agent edit a workspace and open a pull
  request for an approved triage note, confined by a per-provider write policy and a snapshot
  guard that refuses any change to `.git` or the workspace's own `.sirdar` directory
  (`docs/eval.md`).
- Added `provider: qwen`, a native Qwen Code adapter: every non-read tool is excluded, and an
  authenticated loopback PreToolUse hook fails closed, so the workspace stays read-only even
  though the run is untrusted.
- Added Codex workspace-MCP parity: a per-session `CODEX_HOME` carries only the workspace's own
  `.mcp.json` servers under `mcp.workspaceOnly`, and MCP, shell, and file-change approvals all
  route through Sirdar's permissions.
- Added `permissions.fetch`, a cross-provider allow-list of the hosts a session may fetch a URL
  from. `WebFetch` and `web_fetch` used to be approved on the tool name alone, so an instruction
  injected into anything a read tool pulled in could name its own destination and carry what the
  run had read there. The destination is now judged on every call — https only outside an
  explicit loopback entry, no userinfo, no IP literals or private addresses — on Claude, on
  qwen, in Sirdar's own agent loop, and for an ACP `fetch` request. The list is empty by
  default, which denies every fetch; Codex's built-in web search stays governed by Codex's own
  `config.toml` and sandbox, which is documented rather than fixed.
