# Changelog

This file is hand-maintained. The `Unreleased` section is a running summary of what
exists on `main` since the last tag; each release moves it under a version heading.
The changelog goreleaser attaches to a GitHub release itself is generated separately
from conventional-commit prefixes in the git log, and is not a replacement for this.

## Unreleased

Sirdar as it stands today, before the first tagged release:

A command-line harness (`sirdar init`, `doctor`, `triage`, `rca`, `resume`, `runs`,
`register`, `mcp`, `serve`) that reads an engineering-support ticket from a tracker and a
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
before it cuts a branch; `provider: agy` is disabled (see the entry at the end of this
section) but its adapter drives Google's Antigravity CLI against the
operator's own Google account, triage and rca only — that CLI gives a parent process no
way to mediate a tool call, so permissions are set for the session in a project file
Sirdar writes under `~/.gemini/config/projects` and deletes when the run ends (a read
allowed, every write, command and URL fetch denied), on top of the CLI's own plan mode,
plus a watch that fails the run: a write or a command that completes ends the session
and files nothing, and so does a session that completed no read at all.
`mcp.workspaceOnly` is unenforceable there, and `sirdar fix` is refused before it cuts
a branch.

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

- The UI wave landed at `cccdc55`: the desktop app and `sirdar serve` are now the
  session-first surface the reviewed mocks in `docs/design/2026-09-15-screens` drew, on the
  16px register and the `src/ui` component library. Eight screens — Board, New session,
  Session (the live transcript with a composer that answers a blocked run or steers a finished
  one, and a Changes pane with Keep/Drop per hunk), Change review, Register, Eval, Settings
  (a modal: config read back, providers with doctor's word on each, MCP servers, Try a tool)
  and the Library — over one `Transport` that both the HTTP+SSE client and the Wails bridge
  implement, and one fake the tests run against. Every run link carries its workspace
  (`#/runs/<workspaceId>/<runId>`). The two build paths are `make ui` for the frontend
  `sirdar serve` embeds and `make desktop` (`wails build`) for the app.
- The fix round after it: runs carry the ticket's title (`title` on `RunSummary`, read off the
  run's bundle or its note), the theme opens light unless a choice is stored, a modal makes
  the window behind it `inert`, the HTTP event stream reports itself lost and the window
  resyncs when it is back, every eval job can be cancelled from the Eval screen, and the dead
  `panels.css`, `RCAForm` and `FixForm` are gone.
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
- Added `sirdar steer RUN_ID "instruction"` and `POST /api/workspaces/{id}/runs/{runId}/steer`:
  a follow-up instruction on a finished (or blocked) run continues the same run. The transcript
  grows in place with a `steer` line saying who answered — the session that wrote the note
  (`claude`, `codex`, `qwen`, `openai` resume it by handle) or a fresh session primed with the
  run's prompt and answer (`acp`); `cursor` and `agy` refuse through the new
  `provider.Steerable` contract. The note is rendered again and a register row appended only
  when the answer changes; turns, minutes and cost accumulate on the run and the same caps apply
  to the total. A `--local` or deviation-blocked fix run is steered in its own worktree with its
  commit amended, never pushed (`docs/steer.md`).
- `provider: acp` reads session modes the way the agents actually publish them. A mode id may be
  a URL — every one of GitHub Copilot's is a link into the protocol's own documentation, ending
  `#plan`, `#agent` or `#autopilot` — so ids are now matched on their last fragment or path
  segment as well as whole, and the agent's own id is what goes back on the wire. An agent that
  advertises no modes at all may still expose one as a session config option, which is where
  OpenCode keeps its `build`/`plan` choice: Sirdar sets it with `session/set_config_option`,
  `plan` for a triage or rca run and `build` for a fix (`docs/config.md`, "Session modes").
- An ACP agent's `available_commands_update` is now kept as one line — the count and the first
  three names — instead of verbatim. OpenCode re-sends the host's whole slash-command catalogue,
  descriptions and all, on every update, and one run's event log grew past 1300 system events of
  largely the same 15 KiB of text.
- A prompt turn that ends with no answer at all is a warning while the run can still recover, and
  becomes the run's error only if no answer ever arrives. Copilot ends two such turns before a
  good third often enough that runs which filed a perfectly good note were carrying error lines
  for the turns it took to get there.
- `permissions.bash` and `permissions.fixBash` now ignore git's cosmetic global options when
  matching: `git --no-pager diff -- x` matches `git diff*`, as do `--no-optional-locks` and a
  `-c color.ui=…` / `-c core.pager=cat` that runs no program. `-C`, `--git-dir`, `--work-tree`
  and any other `-c` setting still stay in the command and fail the match, because each of them
  changes what git does rather than how it prints (`docs/config.md`).
- Re-triage looks for a key's existing note only where the configured filename pattern files it,
  rather than walking the notes directory. An archived copy under `notes/previous/` was being
  read for its status and taken as reason to file nothing, leaving the run's note in the run
  directory with one warning in the state file. When filing really is refused because a person
  has moved the note on, the run now says so on its last line, naming the note and its status.
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
- Added a review pass over a finished fix run, with no agent in it: `sirdar runs diff <run-id>`
  prints the commit as a unified patch (`--files` for the file list), and
  `GET /api/workspaces/{id}/runs/{runId}/diff` serves the same reading as JSON — the base and
  head commits, the branch, whether the worktree is still there, whether the branch is pushed, a
  per-file list with line counts, and the patch capped at 2 MiB. `sirdar runs diff <run-id>
  --drop <path>:<n>`, and `POST .../diff/drop`, revert one hunk out of the commit and amend it in
  place, keeping the original commit message and appending a `review` event to the run log. The
  drop is refused while the run is live, once the worktree is gone, once the branch is pushed, and
  whenever the `etag` says the patch the hunk index was counted in is not the patch that is there
  now (`docs/fix.md`, "Reviewing the change").
- `sirdar serve` now sends `Cache-Control: no-store` on the UI's HTML, so an upgraded binary is
  not shadowed by an `index.html` the browser kept from the build before it. The hashed assets
  beside it are content-addressed and stay cacheable.
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
- Added `sirdar mcp`, which answers what a run's MCP access would be without starting a run.
  `sirdar mcp list` names the servers the workspace declares — and, with `mcp.workspaceOnly`
  off, the operator's global ones too, scoped `global` — with their transport and command or
  URL, and with `env` and `headers` reduced to key names so no credential value is ever
  printed. `--connect` starts each, initializes, counts its tools and times it, or prints the
  error; an HTTP 401 or 403 reads `401 from the token, check its scope` and never carries the
  token. `sirdar mcp tools SERVER` lists every tool with the verdict a run would get and the
  rule that settled it (a `permissions.mcp` pattern, a write word, a generic passthrough, a
  read word, or a name the heuristic recognises nothing in), from the same
  `provider.DecideMCPTool` the policy calls — one function, so the two cannot drift.
  `sirdar mcp call SERVER TOOL [--args '<json>']` runs one by hand: a denied tool is refused
  with that same reason and exit 2, its server never started, and an allowed one's output is
  capped at 64 KiB with a `truncated` line. `sirdar serve` gains the same three at
  `GET /api/workspaces/{id}/mcp` (`?connect=1`), `GET …/mcp/{server}/tools` and
  `POST …/mcp/{server}/call`, loopback-only and behind the existing cross-site guard, with a
  denied tool answered `403` carrying the reason (`docs/config.md`, "Checking it").
- Added a streamable-HTTP MCP transport to `internal/mcpclient`, so an `"type": "http"` entry in
  `.mcp.json` can be listed, inspected and called by `sirdar mcp`. Sirdar's own agent loop
  (`provider: openai`) still starts stdio servers only, and the listing says so on the row.
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
- `provider: agy` is disabled. Google's Antigravity terms do not allow driving the CLI from
  another program, and an account that does it can be banned, so config load refuses
  `provider: agy` and so does `--provider agy` on `triage` and `rca`: `provider agy is disabled:
  Google's Antigravity terms do not allow driving the CLI from another program; choose claude,
  codex, openai, acp, qwen or cursor`. `sirdar doctor` reports the whole provider as one row,
  `agy — disabled (Antigravity terms)`, instead of its binary, model and permission rows. The
  adapter stays in the tree (`internal/provider/agy`) and `docs/research/10-antigravity-wire-formats.md`
  is retained for reference. `agy.acknowledgeTerms: true` takes the refusal off — at your own
  risk, and not recommended.
- A run now records the model that actually answered it. Every adapter that is told one on the
  wire reports it on the system event its first line becomes — Claude Code's `system`/`init`,
  Codex's `thread/start` result, Qwen's and Cursor's init lines, agy's, and an ACP agent's
  `model` config option when it advertises one — and a run whose configuration named no model,
  or named an alias like `sonnet`, replaces `state.json`'s `Model` with the reported id and
  keeps the configured value in the new `ModelRequested`. The write happens while the run is
  still going, so the desktop's `run.updated` carries it and the Session topbar stops reading
  "model unknown" halfway through a run. `sirdar runs` has a `MODEL` column (and `model` in its
  `--json` rows), `sirdar register` has one too, and the register line each run appends already
  carried `Model`, which now names the model that wrote the note. A later session on the same
  run — a steer, a resume — asks for `ModelRequested` again, so a workspace that configured an
  alias on purpose keeps getting one.
- The model's prose reaches the transcript on a Claude run. Claude Code streams a message as
  `stream_event` text deltas while it is written and repeats the finished block on the turn's
  `assistant` line, and the adapter reported only the second — so a run that talked while it
  worked showed tool calls and no words until the turn ended. The deltas are now
  `assistant_text` events marked `delta`, the finished block is marked `replace`, and
  `conversation()` grows one message from the deltas and lets the finished block stand in for
  them rather than printing the answer twice. A text delta is no longer also stored as a
  `system` line, which is most of what a real run's `events.jsonl` used to be; every other
  stream event, tool-input deltas included, is kept. qwen, cursor and codex already reported
  their prose and are unchanged but for tests that now pin the words.
