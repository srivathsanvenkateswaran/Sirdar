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

Built-in tracker adapters for Jira Cloud, Jira Data Center, Linear, Azure DevOps, Rally,
and ServiceNow; built-in helpdesk adapters for Zoho Desk (with OAuth refresh), Zendesk,
Freshdesk, Help Scout, Intercom, HubSpot Service Hub, Front, Gorgias, and ServiceNow. Anything
else — an internal tracker, a different helpdesk — is a separate executable speaking a small
line-delimited JSON protocol over stdin/stdout, so its credentials and vendor-specific
code never touch Sirdar's core.

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
- Added a built-in Front helpdesk adapter, read-only over an API token (`token: env:FRONT_TOKEN`).
  It merges Front's two thread resources — `messages`, which is what was sent to and received
  from the customer, and `comments`, the teammate notes Front keeps internal — into one ordered
  thread, follows each feed's `_pagination.next` under a page cap, and downloads attachments from
  Front's own authenticated `/download/{id}` endpoint.
- Added a built-in Gorgias helpdesk adapter (`adapter: gorgias`): per-account host from
  `account` or `baseUrl`, HTTP Basic with the login `email` and an `apiKey` credential
  reference, the cursor-paginated `/api/messages` feed as the thread, and attachment
  downloads that send the key only to the configured account host.
- Added a built-in ServiceNow adapter, the first that serves either role: one incident is both
  the customer's ticket and the work item, so the same block works under `sources.tracker` or
  `sources.helpdesk`. It reads the Table API for the record, `sys_journal_field` for the
  conversation (work notes internal, comments customer-visible) and the Attachment API for the
  files, authenticating with a basic username/password pair or an OAuth bearer token
  (`docs/adapters.md`, `docs/research/adapters/servicenow.md`).
- Added `sirdar eval`, which replays a golden set of previously triaged tickets and scores a new
  run against the assertions and note you recorded for each one, and `sirdar golden add` to build
  that set from a completed run (`docs/eval.md`).
- Added `sirdar fix`, a human-gated mode that lets the agent edit a workspace and open a pull
  request for an approved triage note, confined by a per-provider write policy and a snapshot
  guard that refuses any change to `.git` or the workspace's own `.sirdar` directory
  (`docs/fix.md`).
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
- Closed two paths by which something other than the operator could start an agent session that
  writes code through `sirdar serve`. Every mutating route now requires `Content-Type:
  application/json`, refuses an `Origin` that is neither the listener's own nor the Wails shell's,
  and refuses `Sec-Fetch-Site: cross-site` or `same-site` — so a page the operator has open can no
  longer cross-site-post to `/api/workspaces/<id>/fix`; the `/hooks/` routes, which authenticate
  by signature, are exempt. And the fix route itself now answers 403 on a listener bound with
  `--allow-remote`: a remote caller may read notes and start a triage, but not write code and open
  a pull request under the operator's GitHub login (`docs/config.md`).
- Added `budget.stallMinutes`, a stall watch on the provider's stream. A session that says
  nothing at all — no tool call, no assistant text, no usage line — for six minutes (the default;
  `0` turns the check off) is cancelled, marked `failed` with `stalled: no activity for 6m`, and
  given an `error` event in its own log. A provider that died mid-stream used to hold the run
  until `budget.maxMinutes` expired, 25 minutes after it had stopped existing. The timer restarts
  on every event, so a slow tool call is not a stall, and it is suspended for a run waiting on a
  person — the agent asked a question, or a rate limit parked it — which stays `blocked` and
  keeps its resume handle.
- `sirdar fix` now runs its session in a linked git worktree under `.sirdar/worktrees/<run-id>`
  instead of in the tree you are standing in. Your uncommitted work is neither in the way nor
  swept into the fix's commit, your HEAD does not move, and the dirty-tree preflight that used to
  refuse the run now applies only to `fix.inPlace: true`, which restores the old
  `git checkout -B` behaviour. The agent's root, the reserved paths, the snapshot guard's hooks
  directory and the reservation handed to the session all follow the worktree, while the
  workspace's configuration and playbooks are still read from the main tree. The commit, push and
  pull request are made from the worktree; it is removed on success and kept when a run is
  blocked on a deviation, so `--accept-deviation` publishes the commit you reviewed out of the
  tree it was made in (`docs/fix.md`).
- `sirdar doctor` now reports a third level: `[!!]` for a warning, alongside `[OK]` and `[XX]`
  for a failure. Only a failure exits non-zero, so an advisory row — every user-level MCP
  server visible to the agent, a Codex session that will see none, a custom Anthropic base URL
  under `billing: api`, a qwen session that keeps folder trust and loads the repository's own
  `.qwen/settings.json` — no longer trips a CI gate. The desktop Settings screen reads the same
  `level` field.
- Rewrote the `permissions.mcp` write-verb heuristic: it now tokenises the whole tool name
  rather than reading only the leading word, denies a generically named passthrough
  (`*_api_request`, `graphql`, `sql_execute`, a bare `query`) whose arguments decide what it
  does, and no longer lets a read word beside a write word win — `run_query` and `run_select`
  are denied by the default heuristic now, and go in `permissions.mcp` for a workspace that
  needs them. `read_query`, `list_tables` and similar names with no write word still go
  through.
- `sirdar register --markdown` now fills the Title and Company cells from the note's own title
  and its `company`/`customer` frontmatter, instead of leaving them for a human to fill in by
  hand. `RegisterRow` gained `Title` and `Company`, both omitted from the JSON line when empty,
  so an existing `register.jsonl` reads back unchanged.
- `provider: openai` can now resume a blocked or interrupted run: the loop writes its message
  transcript to `transcript.json` (mode `0600`) in the run directory after every turn, and
  `sirdar resume` and the runner's schema retry both continue from it instead of starting the
  triage over.
