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

Seven ways to drive a run: `provider: claude` and `provider: codex` spawn the Claude
Code or Codex CLI already installed and signed in, so a run counts against the plan
already being paid for; `provider: openai` runs Sirdar's own agent loop against any
OpenAI-compatible endpoint (OpenRouter, Groq, Together, DeepSeek, Moonshot, Zhipu, or
a local Ollama/vLLM/llama.cpp), billed per token against a budget set in config;
`provider: acp` drives any Agent Client Protocol agent (Gemini CLI, Goose, OpenCode,
and Moonshot's Kimi Code CLI via `kimi acp`, the one of those verified against a real
binary);
`provider: qwen` is a native Qwen Code adapter with a fail-closed loopback permission
hook; `provider: cursor` drives the Cursor Agent CLI, read-only by Cursor's own
execution mode rather than by a policy Sirdar enforces — a write or a command that
completes anyway ends the session and fails the run, and `sirdar fix` is refused
before it cuts a branch; `provider: agy` drives Google's
Antigravity CLI against the operator's own Google account, triage and rca only — that CLI
gives a parent process no way to mediate a tool call, so the read-only guarantee is its own
plan mode plus a watch that fails the run: a write or a command that completes ends the
session and files nothing. `mcp.workspaceOnly` is unenforceable there, and `sirdar fix` is
refused before it cuts a branch.

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

- A read is now judged on where it looks, not on the tool's name. `Read`, `Glob`, `Grep`, `LS`
  and the same tools under each provider's own names were approved unseen, so a triage session
  could open any file on the machine — a live run read a skill file out of the operator's home
  directory. A read-class call whose target resolves outside the workspace root, the run's
  directory or its bundle is refused with `read outside the workspace: <path>`, on Claude's
  permission tool, the qwen hook, an ACP `session/request_permission` and its `fs/read_text_file`,
  and in Sirdar's own agent loop. Symlinks resolve before the check. `permissions.readAlso` is a
  new list of globs, empty by default, that widens the scope to a runbook directory or a skills
  tree. `Bash` is unchanged — its own allow-list already refuses a path argument outside the
  root — and on `provider: cursor` and `provider: agy`, which answer their own tool calls, there
  is nothing to mediate: `sirdar doctor` warns and every session says so on its own event log.
- The digest's ISSUE column reads the triage note's title, and falls back to the first sentence
  of the complaint that is not a greeting. It used to take the complaint's first sentence, which
  on an Arabic support thread is "Peace be upon you." for every row.
- A triage note's body links now move with its frontmatter. The "Register:" line names the RCA
  and Resolution notes by the slug predicted from the triage title; when the rca retitles the
  issue and files under a different name, both halves of the note are rewritten, instead of the
  frontmatter alone being right and the body links leading nowhere.
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
- Added `sirdar golden add KEY --retro --pr URL`, which builds a golden entry out of a ticket
  whose fix has already merged: the bundle is assembled as of the moment an engineer picked the
  ticket up — thread messages and attachments from after it dropped, every pull-request URL and
  `PR #N` mention redacted to `[redacted: pull request]` — and the merged pull request is filed
  beside it as ground truth in `retro.json` and `pr.diff`. The cutoff is the earliest of the
  ticket's first `in_progress` transition and the first pull request's `created_at`, or whatever
  `--as-of` names. The ticket is read through the workspace's own adapters, the pull request
  through `gh` with exec and never a shell, and nothing is written back to either system
  (`docs/eval.md`).
- Added `sirdar eval --retro`, which replays a golden key at the commit its fix branched from —
  triage from the as-of bundle, then `fix --local` from that triage note, and a blind RCA behind
  `--with-rca` — and scores what came back against the pull request a human merged: the note's
  code references against the files the change touched, the agent's own diff against the
  pull request's by file overlap and by hunk, and, behind `--rubric`, one extra provider call
  answering a fixed JSON rubric. Each stage is the ordinary command at `--at <baseCommit>`, in a
  linked worktree of its own, marked as an eval: no note is filed, no register row is appended
  and nothing is pushed. The fix and the RCA are handed the run id of the retro's own triage
  note, because the newest-triage-note lookup skips eval runs on purpose and would otherwise
  find nothing. It exits 0 whatever the table says, because a retro is a measurement and not a
  gate; the report lands in `.sirdar/eval/<ts>-retro.json` and the Eval screen's Retro section
  reads the last one (`docs/eval.md`).
- Added an as-of cutoff to bundle assembly (`run.Options.AsOf`), so a bundle can be built as the
  ticket stood at an instant rather than as it stands now. What it dropped and redacted is
  counted in `bundle/manifest.json`.
- Added `sirdar fix`, a human-gated mode that lets the agent edit a workspace and open a pull
  request for an approved triage note, confined by a per-provider write policy and a snapshot
  guard that refuses any change to `.git` or the workspace's own `.sirdar` directory
  (`docs/fix.md`).
- Added `provider: cursor`, an adapter for the Cursor Agent CLI (`cursor-agent -p`, stream-json),
  with the honest caveat attached to it: print mode approves its own tool calls, so Sirdar's
  permission policy is never consulted and the read-only guarantee is Cursor's `--mode ask`/`plan`
  plus an excluded tool list sent as a request header, plus `--sandbox enabled` for shell
  commands and `--disable-project-configs` so a checkout cannot widen its own run. `sirdar fix`
  is refused on this provider — the sandbox does not cover the edit tool, which takes an absolute
  path — and `sirdar doctor` carries a row saying so. There is no `--json-schema` flag, so the
  schema rides in the prompt and the answer is parsed out of the result text with the retry
  going through `--resume`; there is no cost or turn count on the wire, so `budget.maxMinutes`
  is the bound that works; and `mcp.workspaceOnly` cannot be honoured, because the CLI always
  merges the operator's own `~/.cursor/mcp.json` in. `CURSOR_API_ENDPOINT` and the credential
  variables are stripped from the agent's environment with no way to configure them back
  (`docs/research/11-cursor-wire-formats.md`, `docs/config.md`).
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
- The desktop app and `sirdar serve` reached parity with the CLI: an Eval screen replays the
  golden set or adds a completed run to it, a Fix panel shows a run's commit, deviation and pull
  request and lets you accept a deviation, a shared provider/model picker appears on every start
  form, an inbound-webhook panel shows each delivery and why it was started, skipped or ignored,
  and a read-only config summary shows the notify and webhook blocks a workspace has configured,
  without resolving any credential (`docs/eval.md`, `docs/config.md`).
- Closed two paths by which something other than the operator could start an agent session that
  writes code through `sirdar serve`. Every mutating route now requires `Content-Type:
  application/json`, refuses an `Origin` that is neither the listener's own nor the Wails shell's,
  and refuses `Sec-Fetch-Site: cross-site` or `same-site` — so a page the operator has open can no
  longer cross-site-post to `/api/workspaces/<id>/fix`; the `/hooks/` routes, which authenticate
  by signature, are exempt. And the fix route itself now answers 403 on a listener bound with
  `--allow-remote`: a remote caller may read notes and start a triage, but not write code and open
  a pull request under the operator's GitHub login (`docs/config.md`).
- Added optional audio transcription to bundle assembly, under `attachments.transcribe`. A
  helpdesk on a WhatsApp number gets voice notes — one ticket in the first live runs carried 28
  Arabic `audio/ogg` files, none of which the session could open, and the triage came out
  low-confidence because the complaint itself was in the audio. With a command configured, each
  audio attachment is transcribed during bundle assembly and the text lands beside it as
  `<attachment>.transcript.txt`, marked in the manifest, pointed at from the rendered
  conversation, and announced to the session as machine-produced evidence. The command template
  is split into argv at config load and executed directly, never through a shell; it runs with
  stdin closed, an environment of `PATH`, `HOME` and `LANG` alone, two minutes per file and ten
  per bundle. Every failure — a broken command, audio over `maxSeconds`, the `maxFiles` cap — is
  a per-file warning that leaves the attachment where an unreadable attachment has always been:
  named as unread in the warnings, the prompt and the note. The block is absent by default;
  Sirdar ships no model and calls no service, so audio leaves the machine only if the command the
  operator chose sends it somewhere. `sirdar doctor` has a `transcribe` row (`docs/config.md`).
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
- Added `--at COMMIT` to `sirdar triage` and `sirdar rca`: the session runs against the
  repository as it stood at that commit, in a linked worktree of the run's own under
  `.sirdar/worktrees/<run-id>` at a detached HEAD, so a ticket can be triaged against the code
  that was actually running when it was filed. The tree you are standing in is untouched — its
  HEAD, its index and its uncommitted work are all where you left them — and the workspace's
  `.sirdar/` is still read from the main tree, so the configuration, playbooks and templates are
  the ones you configured rather than the ones the repository happened to carry a year ago. The
  run state records `At:` and the note's frontmatter carries `at:`, because nothing else in a
  note would tell a reader that its code references are not about today's tip. The worktree is
  removed when the run ends, unless `--keep-worktree` or the run blocked and can be resumed.
  Triage stays exactly as read-only inside the worktree as it is at HEAD.
- Added `--local` and `--at COMMIT` to `sirdar fix`. `--local` stops the flow at the commit:
  nothing is pushed, no pull request is opened, the worktree is kept, the commit's unified diff
  is written to `fix.diff` in the run directory, and the run state records `Fix.Local`,
  `Fix.Commit` and `Fix.DiffPath`. The triage note is left on its own status, since `fix-pushed`
  would be a claim about work that never left the machine. `--accept-deviation` with `--local`
  accepts the diff and still pushes nothing. `--at` cuts the fix branch from a named commit
  instead of `origin/<base>` and skips the fetch, so a fix can be generated against the code the
  ticket was filed against. Together they are what a retrospective evaluation runs — many fixes
  against historical commits, none of which may reach a remote.
