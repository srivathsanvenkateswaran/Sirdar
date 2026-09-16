# Handoff (updated 2026-09-15)

Read this first when opening a new session in this project.

## Where things are

One worktree, one repo (`git@github.com:srivathsanvenkateswaran/Sirdar.git`, personal identity
via the includeIf rule; never set `user.email` by hand):

| Worktree | Branch | State |
|---|---|---|
| `~/Documents/Personal/Sirdar` | `main` | Everything landed. CLI: `init`, `doctor`, `triage`, `rca`, `resume`, `runs` (including `runs diff`), `register`, `mcp`, `steer`, `serve`, `eval`, `golden`, `fix`. Wails v2 desktop app under `desktop/`, and `sirdar serve` giving the same frontend over HTTP: the UI wave landed at `cccdc55` — eight screens (Board, New session, Session with the live transcript, composer and Changes pane, Change review, Register, Eval, the Settings modal with providers, MCP servers and Try a tool, and the Library) on the `src/ui` component library, over one `Transport` the HTTP+SSE client, the Wails bridge and the test fake all implement; `make ui` stages the frontend `sirdar serve` embeds, `make desktop` (`wails build`) makes the app. Seven providers: `claude`, `codex`, `openai` (Sirdar's own agent loop, any OpenAI-compatible endpoint), `acp` (any Agent Client Protocol agent — Copilot CLI, OpenCode and Kimi are the three driven live, `internal/provider/acp`), `qwen` (native Qwen Code adapter, fail-closed loopback permission hook), `cursor` (Cursor Agent CLI, `internal/provider/cursor`), `agy` (Google's Antigravity CLI, `internal/provider/agy`, **disabled** — Google's terms do not allow driving it from another program, and the adapter is kept for reference only); the table under Providers below says what each one's read-only guarantee rests on and whether it can fix or be steered. Reads are confined to the workspace, the run directory and its bundle, widened only by `permissions.readAlso`. Codex workspace-MCP parity: a per-session `CODEX_HOME` carrying only the workspace's `.mcp.json` servers under `mcp.workspaceOnly`, with MCP, shell and file-change approvals routed through Sirdar's permissions. 13 built-in source adapters plus external stdio adapters: tracker role — Jira Cloud/Data Center, Linear, Azure DevOps, Rally, ServiceNow; helpdesk role — Zoho Desk (OAuth refresh), Zendesk, Freshdesk, Help Scout, Intercom, HubSpot, Front, Gorgias, ServiceNow. ServiceNow is the one adapter that serves either role from the same incident record. All on the shared `internal/source/httpx` HTTP helpers (host trust, redirect policy, Retry-After, capped reads). Credential stores: `env:`, `keychain:` (login Keychain on macOS, libsecret/Secret Service on Linux, the Credential Manager read through PowerShell `CredRead` on Windows), `file:`, `cmd:` (`docs/credentials.md`). Arabic/RTL i18n: `language:` config block, bilingual note fields, RTL-aware desktop UI. Inbound webhooks: `sirdar serve` triggers per source with signature verification (`docs/webhooks.md`). Run-completion notifications: Slack, Teams, generic webhook, timestamped HMAC (`docs/notifications.md`). `sirdar eval` + `sirdar golden add` (golden-set scoring, `internal/eval`, `docs/eval.md`) and the confined `sirdar fix` (human-gated fix flow, `internal/fix`, running in a linked git worktree under `.sirdar/worktrees/<run-id>` with `fix.inPlace` as the fallback). `budget.stallMinutes` cancels a run whose provider goes silent. Release pipeline: goreleaser, Homebrew tap, desktop zips (`docs/release.md`). Repo hygiene: CONTRIBUTING, SECURITY, CODE_OF_CONDUCT, issue/PR templates, dependabot, `docs/architecture.md`. MkDocs docs site published via GitHub Pages. Cross-provider web-fetch allow-list (`permissions.fetch`, empty by default, denies every fetch). Same-origin and loopback guard on every mutating `sirdar serve` route (`docs/config.md`). Both dogfood fix waves (finish-on-final, `permissions.mcp`, attachment caps, host trust in every adapter, command policy, turn counting). `sirdar runs diff` and the diff-review API (per-hunk drop with an amend, `docs/fix.md`). `sirdar mcp list/tools/call` and the matching serve routes, answering what a run's MCP access would be without starting one (`docs/config.md`, "Checking it"). `sirdar steer` continuing a finished run in place (`docs/steer.md`). The screen mocks the UI was built from (`docs/design/2026-09-15-screens`) and the design it was built to (`docs/superpowers/specs/2026-09-15-ui-build-design.md`). Research + plans in `docs/`. |

Ledgers (git-ignored) with every ruling and deferred minor: `.superpowers/sdd/*/progress.md` in
each worktree. Reports per task sit beside them.

Private, outside the repo: the Janus tracker adapter at `~/Documents/Work/Coding/sirdar-janus`
(work identity, no remote; binary `~/bin/sirdar-janus`; verified against the live API). The
dogfood workspace is `OXO.APIs/.sirdar/` (git-excluded) with playbooks encoding the support
gotchas and templates matching the Obsidian vault; notes go to `Support Duty/Sirdar/` so no
human-written note is touched. First real run (OMNI-3217): valid, good note in 9 min, then a success-path hang lost it; all
twelve findings fixed the same evening (`docs/research/07-dogfood-findings.md`,
`.superpowers/sdd/2026-09-10-sirdar-v0-triage-core/dogfood-report*.md`). Run 2, with the fixed
binary, completed two tickets cleanly (OMNI-3217, OMNI-3193); its findings are fixed too
(`dogfood-report-2.md`).

## Providers

Seven, and no two of them rest the read-only guarantee on the same thing.

| Provider | What makes a run read-only | `sirdar fix` | `sirdar steer` |
|---|---|---|---|
| `claude` | `--permission-prompt-tool stdio`: every tool call the CLI is not already allowed to make arrives as `can_use_tool` and the policy answers it, with the write set on `--disallowedTools`. Reads are judged on the path, not the tool name. | yes | resume (`--resume`) |
| `codex` | `sandbox: read-only` with `approvalPolicy: untrusted`, so shell, MCP and file-change calls are asked about. A fix session moves to `workspace-write` behind the snapshot guard. | yes | resume (`thread/resume`) |
| `openai` | Sirdar's own loop runs the tools, so the policy decides before each call and each tool re-checks the same rule inside itself. | yes | resume (`transcript.json`) |
| `acp` — Copilot CLI, OpenCode, Kimi, Gemini CLI, Goose | The policy answers every `session/request_permission` and every `fs/read_text_file`, and the adapter now selects the agent's read-only session mode (`plan`, `read-only`, …; `acp.mode` overrides) before the first prompt. A write, command or sub-agent spawn that completes having asked nobody fails the run. | yes | primed (fresh `session/new`) |
| `qwen` | `--exclude-tools` for every non-read tool plus an authenticated, fail-closed loopback `PreToolUse` hook. In fix mode `write_file`, `edit` and `replace` come back, and every call still reaches the hook. | yes | resume (`--resume`) |
| `cursor` | Not mediated: `cursor-agent -p` approves its own tool calls, so the policy is never consulted. The guard is Cursor's `--mode ask`/`plan`, an excluded-tool list sent as a request header, `--sandbox enabled` and `--disable-project-configs`. A completed edit or shell call is a breach that fails the run and files nothing. | refused | refused |
| `agy` — **disabled** | Not driven at all: Google's Antigravity terms do not allow driving the CLI from another program, so config load and `--provider agy` both refuse it and `sirdar doctor` prints `agy — disabled (Antigravity terms)`. `agy.acknowledgeTerms: true` takes the refusal off at the operator's own risk, and then: not mediated — `--mode plan` plus a project file Sirdar writes per session under `~/.gemini/config/projects` (allow `read_file(*)`; deny `write_file`, `command`, `execute_url`), which outranks the operator's own `settings.json`. A completed write or command fails the run, and so does a session that read nothing. | refused | refused |

`permissions.bash`, `permissions.mcp`, `permissions.fetch` and the read scope reach the first
five. On `cursor` and `agy` there is no call to judge, so `sirdar doctor` carries a warning row
for each and every session records the same on its own event log.

`agy` is off by default as of the `agy-off` change: the adapter stays in
`internal/provider/agy` and its tests still drive it directly, but nothing in a default
workspace can start a session with it.

## Platforms

macOS, Linux and Windows, for both the CLI and the desktop app. The CLI has shipped for all
three since the first goreleaser config; the Windows desktop app is real as of the `windows`
round.

| | macOS | Linux | Windows |
|---|---|---|---|
| CLI | native | native | native (amd64, arm64) |
| Desktop app | `wails build` | `wails build -tags webkit2_41`, needs `libwebkit2gtk-4.1-dev` | `wails build -platform windows/amd64`, cross-compiles from either of the others; needs the WebView2 runtime to **run** |
| `keychain:` refs | login Keychain (`security`) | Secret Service (`secret-tool`) | Credential Manager (PowerShell `CredRead`) |
| Opening a file or URL | `open` | `xdg-open` | `rundll32 url.dll,FileProtocolHandler` |
| Killing a run's subtree | `SIGKILL` to the process group | same | `taskkill /T /F` on the pid tree |
| Shell for `permissions.bash` | `sh -c` | `sh -c` | `cmd /C` |

The per-platform opener lives in `internal/osopen` and nowhere else; the shell choice is
`agenttools.shellFor`; the kill is `internal/procgroup`. Each has its non-host branches
unit-tested by passing the GOOS in rather than reading `runtime.GOOS`, which is how a macOS
laptop covers them at all.

Two things are Windows-shaped rather than merely Windows-ported. `os.Symlink` fails there for
any account without Developer Mode, so a workspace-only Codex session falls back to a directory
junction (`mklink /J`) or a hard link when it links the entries a generated `CODEX_HOME` shares
with the operator's real one. And `taskkill /T` walks parent pids rather than holding a kernel
object, so a descendant that re-parents itself escapes it — a Job Object would not, but
`os/exec` hands out neither the suspended thread nor a post-start hook to assign one.

Keyboard shortcuts already answer to Ctrl everywhere they answer to ⌘ (every handler tests
`e.metaKey || e.ctrlKey`). Their **labels** still draw ⌘ on every platform; see Known gaps.

## Decisions taken today

- Build, don't adopt (nothing exists for helpdesk → coding agent → RCA note; see `docs/research/02`).
- Name Sirdar; Apache-2.0.
- Providers spawn the user's own installed CLI (Claude Code stream-json, Codex app-server); API-key
  billing is a config switch. `docs/research/03` has the licensing detail.
- Desktop: Wails v2 (system webview), one React frontend for both the app and `sirdar serve`;
  the app observes run directories instead of hooking the runner.
- Adapters: stdlib HTTP, read-only, per-ticket warnings, credentialed downloads only to the
  configured host; `ListFilter.Limit` 0 means 100, cap 200.
- Models beyond Claude/Codex: `docs/superpowers/plans/2026-09-10-provider-roadmap.md`. Phase 1
  (`provider: openai`), Phase 2 (`provider: acp`), and Phase 3 (`provider: qwen`) are all landed
  on `main`. Codex custom providers are a dead end (Responses API only).
- `provider: agy` is the one provider whose read-only guarantee Sirdar sets up front rather than
  judging per call. The Antigravity CLI gives a parent process no way to mediate or pre-empt a
  tool call — no control_request, no HTTP hook, nothing answerable on stdin — and a headless run
  auto-denies whatever needs approval while deciding everything else from files. So every
  session runs in `--mode plan` **and** under a project file Sirdar writes for that session
  under `~/.gemini/config/projects/sirdar-<hex>.json` (allow `read_file(*)`; deny
  `write_file(*)`, `command(*)`, `execute_url(*)`), passed as `--project` and deleted on every
  exit path, with a sweep for files a killed run left behind. Project rules outrank the
  operator's `settings.json`, which is what makes them worth writing. `--disable-slash-commands`
  is deliberately **not** passed: it cancels `--mode plan`, which is how round 1 ran every
  session in the CLI's default mode without noticing. `permissions.bash`/`mcp`/`fetch` are never
  consulted; the adapter reports the CLI's own refusals as deny permission events, raises an
  `EvBreach` when a write or shell command actually completes in a triage session, raises an
  `EvBlind` when a session completed **no** read (which fails the run rather than filing a note
  written out of the ticket text); and `sirdar fix` is refused at `Start`. `mcp.workspaceOnly` cannot be enforced (one global
  `mcp_config.json`, no narrowing flag) and there is no cost on the wire, so `budget.maxUsd`
  never bites. Capture: `docs/research/10-antigravity-wire-formats.md`.
- The read-only guarantee is enforced per provider: `--disallowedTools` + policy for Claude,
  `sandbox: read-only` for Codex, for `provider: openai` the tool set itself plus
  `provider.MatchCommand` (segment matching, no `$(`, backticks or redirection except `2>&1`
  and `2>/dev/null`) and the MCP write-verb heuristic behind `permissions.mcp`, and for
  `provider: acp` the same `PermissionPolicy` applied to every `session/request_permission`
  call, declining outright when the agent asks for a write-shaped capability. `cursor` and
  `agy` are the two that cannot be asked at all; the Providers table above is the summary.
- `internal/source/httpx` centralises what every adapter needed anyway (host trust, redirect
  policy, Retry-After, capped reads, per-ticket warnings). All 13 built-in source adapters,
  trackers and helpdesks alike, use it now.
- Credential refs resolve four schemes: `env:`, `keychain:` (Keychain on macOS, libsecret on
  Linux, a DPAPI-backed store on Windows), `file:`, `cmd:`. A resolved secret is held only for
  the run and goes only to the HTTP client or model endpoint that needs it (`docs/credentials.md`).
- Webhooks: `sirdar serve` exposes one `/hooks/` route per source, signature-verified, off by
  default. The routes stay registered even when disabled, so a disabled hook answers 404 instead
  of falling through to the single-page app (`docs/webhooks.md`).
- Notify: a finished run can post to Slack, Teams, or a generic webhook. Only metadata goes out,
  never the note body. Each destination gets a 15-second ceiling and the run's own return blocks
  on the post finishing (`docs/notifications.md`).
- Help Scout, Intercom and HubSpot joined the helpdesk adapters, all read-only over the shared
  `httpx` client (`docs/adapters.md`).
- Release, hygiene and docs landed together: goreleaser, a Homebrew tap and desktop zips
  (`docs/release.md`); CONTRIBUTING, SECURITY, CODE_OF_CONDUCT, issue/PR templates and dependabot
  (`docs/architecture.md`); a MkDocs site published through GitHub Pages.
- i18n: a `language:` config block plus bilingual note fields (the original-language complaint,
  a customer-language draft) and an RTL-aware desktop UI.
- Three more helpdesk adapters, each on the shared `httpx` client: Front (merges the `messages`
  and `comments` feeds into one ordered thread, trusts both the `api2.frontapp.com` and
  per-company `.api.frontapp.com` host forms since a live tenant's actual host could not be
  confirmed), Gorgias (per-account host, HTTP Basic with `email` + `apiKey`, cursor-paginated
  `/api/messages`, downloads trust only `*.gorgias.com` since the signed-URL storage host is
  undocumented), and ServiceNow (the Table API plus `sys_journal_field` for the conversation and
  the Attachment API for files, basic or OAuth-bearer auth). ServiceNow is the first adapter that
  serves either role — tracker or helpdesk — from the same incident record (`docs/adapters.md`,
  `docs/research/adapters/`).
- Desktop and `sirdar serve` reached parity with the CLI: fix, eval and golden routes, a
  provider/model picker shared by every start form, an inbound-webhook delivery panel, and a
  read-only config summary of the notify and webhook blocks. Every mutating route is now refused
  unless it looks like the UI's own request — JSON content type, a same-origin or Wails
  `Origin`, no cross-site `Sec-Fetch-Site` — and the fix route itself answers 403 on a listener
  bound with `--allow-remote` (`docs/config.md`).
- Web fetches are judged by destination, not by tool name. `permissions.fetch` is one
  allow-list of hosts (`docs.example.com`, `*.example.com` for subdomains only,
  `http://localhost:3000` for a loopback service), empty by default, and empty denies every
  fetch. `provider.DecideFetchURL` is the single entry point: https outside a loopback entry,
  no userinfo, no IP literals, nothing `provider.BlockedIP` refuses — that check moved out of
  `internal/agenttools` so the policy and the dial guard share it. It reaches Claude (WebFetch
  is on `--disallowedTools` while the list is empty, because a user-level `WebFetch(domain:…)`
  allow rule would otherwise let the CLI approve a fetch before Sirdar is asked), qwen (the
  existing PreToolUse hook, under the name `WebFetch`), the openai loop (the policy, and again
  inside `agenttools.web_fetch`, including per redirect hop) and ACP (`fetch` is now judged on
  the kind, so an agent cannot retitle it into the laxer MCP rules). Not Codex: its web search
  is a built-in tool it never asks approval for, governed only by its own `config.toml`
  `web_search` key and `sandbox: read-only` — `thread/start` exposes no separate network flag.
  `WebSearch`/`web_search` stay allowed; the query text remains a residual channel.
- On `sirdar fix`, the confinement is stated per provider, because the layers are not the same for
  all three: Claude and `openai` get policy + in-tool path check + the snapshot guard; Codex
  gets its own `workspace-write` sandbox + the snapshot guard (`decideWrite` is never consulted
  — Codex approves its own tool calls with `approvalPolicy: never`); ACP gets whatever the
  agent implements + the snapshot guard. The guard (`internal/fix/guard.go`) sha256s `.sirdar/`
  (bar `runs/`, `register.jsonl`, `eval/`, `worktrees/`) and `provider.HooksDir` before the session and again
  the moment it ends, before any git command; a difference fails the run, restores nothing, and
  commits and pushes nothing.
- `sirdar fix` is the one session that writes, and it flips all three layers at
  once through `SessionSpec.Mode`: `--disallowedTools` drops to `NotebookEdit` alone, Codex's
  `workspace-write` sandbox, and `agenttools.WriteSet` in the openai loop. `provider.FixPolicy`
  still refuses everything but `Edit`/`Write`/`MultiEdit`, and matches shell commands against
  `permissions.fixBash` rather than `permissions.bash` (whose default git entries are the
  read-only ones: Sirdar commits and pushes, never the agent). Where a write may land is
  checked on every call, in the policy and again inside `agenttools`, through one shared
  helper — `provider.ResolveWithin` confines it to the root through symlinks and
  `provider.ReservedWrite` refuses `.git/` at any depth, the workspace `.sirdar/`, and the
  repository's `core.hooksPath` when it sets one (read once per run by `provider.HooksPath`,
  carried on `PermissionPolicy.ExtraReserved` and `agenttools.Options.ExtraReserved`). Every
  segment comparison folds case: macOS is the dogfood machine and `.GIT/hooks/pre-commit` is
  the same file as `.git/hooks/pre-commit` there; `provider.HooksPath` also expands a leading
  `~`/`~user` in `core.hooksPath` the way git itself does, rather than joining it onto root as a
  literal `~x` entry. `provider.MatchCommand` additionally refuses `git config`, git's
  `--output`/`--output-directory`/`-o`/`--upload-pack`/`--receive-pack` wherever they fall in the
  command, its `-c`/`-C`/`--git-dir`/`--work-tree`/`--exec-path`/`--config-env` when they fall
  before the subcommand (git accepts them nowhere else; `git grep -c` and `git rev-parse
  --git-dir` reuse the same short flags after the subcommand for an unrelated meaning and are
  allowed), and a `GIT_*` environment assignment ahead of any command, git or not (`GIT_DIR=x git
  log`, `env GIT_DIR=x git log`, `GIT_DIR=x make test`). A `permissions.fixBash` command's own
  flag-value path arguments go through `provider.ReservedWrite` too, not only `Edit`/`Write`:
  `go test -coverprofile=.git/hooks/pre-commit` is refused although it matches `go test*`.
  Sirdar's own commit and push both pass `--no-verify`. The human gate is the triage note's `status`, and a
  non-empty `deviationFromNote` in the agent's report stops the push until
  `--accept-deviation`, which on a rerun pushes the commit that was reviewed rather than
  starting a second session.
- A fix session stands in a linked worktree, `<root>/.sirdar/worktrees/<run-id>`, not in the
  operator's tree (`fix.inPlace: true` restores `git checkout -B` in place). Everything that
  names a root follows the worktree — the session's cwd, the write policy's root, the reserved
  paths, the hooks directory — while the workspace's `.sirdar/` is still read from, and guarded
  in, the main tree. `provider.HooksDir` therefore asks git for `--git-common-dir` rather than
  joining `.git/hooks` onto root, since a worktree's `.git` is a file. The worktree is removed on
  success and kept on a deviation block, which is the tree `--accept-deviation` publishes from.
  The dirty-tree preflight applies to in-place mode alone (`docs/fix.md`).
- `budget.stallMinutes` (default 6, `0` off) cancels a run whose provider has said nothing at
  all for that long, marking it `failed` with `stalled: no activity for Nm` and recording an
  `EvError` in its event log. The timer restarts on every event and is suspended once a run is
  waiting on a person (a question, a rate limit), which stays `blocked` with its resume handle.
- `provider: cursor` is the second provider Sirdar cannot mediate, and it says so rather than
  implying otherwise. `cursor-agent -p` approves its own tool calls: no permission channel, no
  approval request, no hook that could be installed without writing into the workspace Sirdar
  is promising not to touch. So the guarantee is Cursor's own `--mode ask`/`plan`, an excluded
  tool list sent as a request header, `--sandbox enabled` for shell, and
  `--disable-project-configs` so a checkout cannot widen its own run; `permissions.bash` and
  `permissions.mcp` govern nothing here, and `permissions.fetch` is honoured only in its empty
  state, by excluding the fetch tools. What Sirdar does enforce is the consequence — a
  completed edit or shell call is a breach that ends the run and files nothing — and
  `sirdar fix` is refused before the command touches git, because the sandbox does not cover
  the edit tool, which takes an absolute path. There is no `--json-schema` flag, so the schema
  rides in the prompt and the answer is parsed out of the result text with the retry going
  through `--resume`; no cost or turn count on the wire, so `budget.maxMinutes` is the bound;
  and `mcp.workspaceOnly` cannot be honoured, since the CLI always merges the operator's own
  `~/.cursor/mcp.json` (`docs/research/11-cursor-wire-formats.md`).
- A qwen fix session can now actually write, and three things had to move together for it:
  `write_file`, `edit` and `replace` come off `--exclude-tools`, go onto `--allowed-tools`
  (headless `denyUnlessAllowed` refuses them otherwise) and come off the settings file's
  `permissions.deny` (a deny beats every allow). Getting two of the three right registers
  nothing, which is what the first live fix run did — its agent reported the edit it had been
  refused and the run still said "completed". The writes are mediated, not trusted: each call
  reaches the PreToolUse hook and `FixPolicy.decideWrite` resolves its `file_path` against the
  worktree root. Nothing else moves. A fix run that ends with no changed files now ends
  `failed` with the agent's own summary as the reason.
- A provider's error sentence is not an answer (`internal/run/answer.go`). `handleFinal` used
  to fall back to the final event's text whenever there was no structured answer, so a CLI
  narrating its own failure had that narration handed to the note validator — which is how the
  first live rca run ended `parse document: invalid character 'M'`, the M of "Model produced
  plain text instead of calling the structured_output tool", and how the schema retry came to
  quote that back at the agent as its own mistake. Text is a candidate answer only when it
  carries a JSON object; otherwise it is the reason the run failed. At the run layer, so it
  holds for every provider and every kind.
- ACP sessions now pick their own mode. `session/new` answers with `availableModes`, and the
  mode an agent opens in is often the wrong one — kimi's `default` approves any write inside a
  git working tree before a permission request is even built. A triage or rca session takes the
  first of `plan`, `read-only`, `readonly`, `read_only`, `ask`; a fix session the first of
  `default`, `edit`; `acp.mode` overrides both. The chosen id is a system event, and it is sent
  even when the agent says it is already current, so the mode was set by this client rather
  than inferred. That is defence in depth, not the guarantee — the guarantee is still that
  every `session/request_permission` is answered by the policy.
- On ACP, a completed write, delete, move, command or sub-agent spawn that never asked is no
  longer a warning: it fails the run on the spot, cancelled, no note, no register row, reason
  `read-only breach: <kind> <path>`. Nothing can be undone at that point; what the failure buys
  is that the run does not go on to file a note asserting a read-only investigation that did not
  happen. A sub-agent spawn is a breach in a fix run too, because a second agent loop has its
  own permission state and nothing it does reaches Sirdar. An unannounced `fetch` stays a
  warning, and so does an unannounced write or command in a fix run whose every named path
  resolves inside the run's own worktree.
- A finished fix run can be reviewed before anything is published, with no agent in it.
  `sirdar runs diff <run-id>` prints the commit as a unified patch (`--files` for the list) and
  `GET …/runs/{runId}/diff` serves the same reading as JSON — base and head, the branch, whether
  the worktree is still there, whether the branch is pushed, per-file line counts, patch capped
  at 2 MiB. `--drop <path>:<n>` and `POST …/diff/drop` revert one hunk and amend the commit in
  place, keeping its message and appending a `review` event. The drop is refused while the run
  is live, once the worktree is gone, once the branch is pushed, and whenever the `etag` says
  the patch the hunk index was counted in is not the patch that is there now (`docs/fix.md`).
- `sirdar mcp` answers what a run's MCP access would be without starting a run: `list` names the
  servers with transport and command or URL and `env`/`headers` reduced to key names, `--connect`
  starts each and counts its tools, `tools SERVER` prints every tool with the verdict and the
  rule that settled it, and `call SERVER TOOL` runs one by hand with a denied tool refused before
  its server is started and an allowed one's output capped at 64 KiB. The verdicts come from the
  same `provider.DecideMCPTool` the policy calls — one function, so the two cannot drift. The
  same three are on `sirdar serve`, loopback-only behind the existing cross-site guard, a denied
  tool answered `403` with the reason. `internal/mcpclient` gained a streamable-HTTP transport
  for it; Sirdar's own agent loop still starts stdio servers only, and the listing says so.
- `sirdar steer RUN_ID "instruction"` continues a run that is already finished, rather than
  starting a second one. The same run goes back to `running`, the transcript grows in place with
  a `steer` line saying who answered, and turns, minutes and cost accumulate against the same
  caps. `claude`, `codex`, `qwen` and `openai` resume by handle; `acp` is primed — a fresh
  session handed the run's prompt, its earlier answer and the instruction — and `cursor` and
  `agy` refuse through the new `provider.Steerable` contract, because neither lets Sirdar judge
  a tool call before it runs. The note is rendered again and a register row appended only when
  the answer changes. A `--local` or deviation-blocked fix run is steered in its own worktree
  with its commit amended, never pushed (`docs/steer.md`).
- A read is judged on where it looks, not on the tool's name. `Read`, `Glob`, `Grep` and `LS`
  were approved unseen, and each takes an absolute path, so a triage session could open any file
  the operator's account could — a live run read a skill file out of `~/.claude/skills/`. Reads
  are now confined to the workspace root (the worktree, in a fix run), the run directory, and the
  run's bundle, refused otherwise with `read outside the workspace: <path>`. Symlinks resolve
  before the check and a leading `~` is never expanded into an approval. `permissions.readAlso`
  is a new list of globs, empty by default, that widens it to a runbook directory or a skills
  tree; a relative entry or a bare `*` fails the config load. It reaches Claude's permission
  tool, the qwen hook, an ACP `session/request_permission` and its `fs/read_text_file`, and
  Sirdar's own loop. `Bash` is unchanged — its own allow-list already refuses a path outside the
  root — and on `cursor` and `agy` there is nothing to mediate.
- Two smaller fixes rode along: the digest's ISSUE column reads the triage note's title rather
  than the complaint's first sentence, which on an Arabic support thread was "Peace be upon you."
  for every row; and a triage note's body links now move with its frontmatter, so an rca that
  retitles the issue rewrites both halves instead of leaving the body links pointing nowhere.
- The desktop UI was designed before being built. `docs/design/2026-09-15-screens` holds static
  1440×900 artboards of every screen — Session and SessionEmpty, Board, Review, Register, Eval,
  Settings, ToolTester, Providers, Library — plus `index.html` as a contact sheet and `BRIEF.md`
  as the brief they were drawn to. Round 1's skeleton was right and too dense; round 2 re-scaled
  everything to the reference app's register (16px base, 248px sidebar, 40px controls, 48px table
  rows, cards with a pale fill) and pinned the two motions the app is allowed — content rising
  8px over 320ms on navigation, the settings modal scaling from 0.98 over 300ms — both frozen
  under reduced motion. Every value comes from the shipped tokens
  (`desktop/frontend/src/styles/tokens.css`). Built from, screen by screen, on the day: see
  the UI paragraph under Next steps.

## Next steps, in order

The UI wave landed on `main` at `cccdc55` on 2026-09-15, built from the reviewed mocks one
screen per branch on top of `ui-foundation` (the tokens on the mocks' 16px register, the 248px
sidebar with Recent sessions and the Plan-usage footer, `src/ui/motion`, thirty-two `src/ui`
components each with a `SPEC.md` mirrored under `docs/design/library`, and `Transport` carrying
`runDiff`, `dropHunk`, `steer`, `mcpServers`, `mcpTools` and `mcpCall` on HTTP, Wails and the
fake alike). What is there now:

- **Screens**: Board (`#/`), New session (`#/new`), Session (`#/runs/<ws>/<id>`: the live
  transcript with tool rows, permission decisions and the agent's question; a composer that
  answers a blocked run or steers a finished one; Note, Bundle, Tools and, on a fix, a Changes
  pane with Keep/Drop per hunk), Change review (`#/runs/<ws>/<id>/review`), Register, Eval, the
  Settings modal (`#/settings/<page>`: config read back, Providers with doctor's word on each,
  MCP servers with Test, Try a tool) and the Library (`#/library`, behind its switch). A run
  link names its workspace because a run id means nothing without one; the design spec was
  amended to say so.
- **Data**: only through `Transport`. `src/store/appStore.ts` subscribes to the event stream
  before its first read, pairs each started job with the run it produces (and keeps every eval
  job for the Eval screen's Cancel), and, on the HTTP transport, reports the stream lost and
  resyncs runs, queue and quota when it is back. `RunSummary` carries the ticket `title`, read
  by `internal/app` off the run's bundle or its note.
- **Preferences**: theme (light by default; System and Dark are choices, stored), reading
  direction and the library switch live in localStorage, never in the config file.
- **Build**: `make ui` (`npm ci && npm run build` under `desktop/frontend`, staged where
  `internal/httpapi` embeds it) for `sirdar serve`; `make desktop` (`wails build`) for the app;
  `make desktop-dev` for the live shell. Checks from `desktop/frontend`: `npx tsc --noEmit`,
  `npx vitest run`, `npm run check-specs`.
- **Rules the code keeps**: only `--sd-*` tokens in stylesheets (`styles.library.test.ts` fails
  on a hex), logical properties, one filled primary per screen through
  `useProvidePrimaryAction` (Settings publishes its disabled Save so the sidebar's New session
  steps down), status never colour alone, a modal makes the window behind it `inert`.
- **Budgets** (the `ui-snappy` round, measured with `desktop/frontend/scripts/perf-trace.mjs`
  against `sirdar serve` in headless Chrome on the MacBook): boot to the board's first card
  under 400ms cold, sidebar navigation to a painted screen under 80ms, a run with a few thousand
  event lines open to its first painted turn under 300ms. What keeps them: the shell reads store
  slices through `useAppState(selector)` and the sidebar and toasts sit on their own subscriptions
  (`App.renders.test` holds that an emit not touching the runs draws no sessions row); one shared
  clock in `lib/useNow` ticks only the `components/Age` leaves that print a time; stream frames
  are coalesced every 16ms in `api/coalesce`; the transcript keeps an unchanged turn's object and
  `TurnGroup` is memoised (`EventStream.memo.test`); the page enter is 160ms on the first paint
  only; fonts are served from the bundle.

Next:

1. Live verification of the UI against a real workspace: a run watched end to end in the
   Session screen, a fix reviewed and a hunk dropped, Try a tool against the OXO.APIs servers.
   Nothing in the wave has been driven by a person against a real run yet; the fake transport
   is what every screen test runs on.
2. Pushing a fix branch from the app, once there is an API for it: the review footer shows the
   branch and the CLI line that pushes it until then. Editing `config.yaml` from the app is
   deliberately out of scope; every settings row opens the file instead.
3. `acp-r3` is in flight (5 commits off `main`): selecting a session mode through
   `configOptions` when an agent lists no modes, matching mode ids by the last segment of a URL
   id, one summary line per `available_commands_update` instead of the whole catalogue,
   stripping git's cosmetic global flags before matching the allow-list, and looking for an
   existing triage note only where the pattern files it.

Then live verification, since almost nothing built in the last two weeks has run against a real
account, a real model, or a real browser:

4. Dogfood the rest of the new features for real: a webhook-triggered run, a notify post, and
   `sirdar eval` against the golden set. None of the three has a live run yet. `sirdar fix` has
   two (Copilot CLI and OpenCode, against the sandbox workspace) but not against a real ticket,
   and `sirdar mcp --connect` has met a filesystem server but not the OXO.APIs ones.
5. First live runs against a real model for the providers still on fixtures: `provider: openai`
   (Ollama `qwen3-coder` or OpenRouter) has not run against anything but a scripted fake, and
   `provider: acp` has three agents live but Gemini CLI and Goose are not among them. Kimi needs
   a refreshed quota before any of it means anything past the handshake.
6. Verify the three new helpdesk adapters against real accounts: Front's actual tenant host
   (`api2.frontapp.com` vs. per-company `.api.frontapp.com`), Gorgias's signed-URL attachment
   storage host, and ServiceNow's `sys_journal_field` rows and timestamp shape against a live
   instance.
7. Open `sirdar serve` in a real browser against a workspace and confirm the same-origin /
   `Sec-Fetch-Site` guard behaves as documented — it has only been driven by tests that construct
   requests directly, never by an actual cross-site page.
8. Live-check `permissions.fetch` against a real Claude Code install: confirm that a
   `WebFetch(domain:…)` rule in `~/.claude/settings.json` is in fact what it is documented to
   be — an allow rule the CLI applies before `--permission-prompt-tool`, and one that
   `--disallowedTools WebFetch` still beats. Both halves are read off Claude Code's documented
   precedence, not watched on a live turn.
9. Continue dogfood run 2's follow-ups: the workspace `.mcp.json` in OXO.APIs exists (doctor reports
   it with 12 `permissions.mcp` patterns); next is two or three more tickets and a comparison against hand-written notes; fold gotchas into
   `.sirdar/playbooks/`. Known: Claude self-approves read-shaped Bash, so `permissions.bash` only
   sees the commands it is asked about.
10. Janus get-by-key is fixed in the private adapter (`?ticket_keys=` filter); `sirdar doctor` in
   OXO.APIs passed every row on 2026-09-11 with the merged binary, and a `--dry-run` triage of the
   golden ticket produced its bundle and prompt. Nothing to do here unless doctor regresses.

Then the not-built items:

11. A Salesforce adapter (tracker and/or Service Cloud helpdesk role) — no research pass or code
   exists yet.
12. A query guard for `WebSearch`/`web_search`, the one residual channel `permissions.fetch`
   leaves open: both carry a query rather than a destination, so there is no host to allow-list
   against, and nothing narrows what a session can search for today.
13. A multi-user shared board, so more than one operator can see the same runs and workspaces —
    pending a design conversation; nothing here is built or even planned in detail.

## What is unverified

Nothing on Windows has been run on Windows. The `windows` round was done on a macOS machine:
`wails build -platform windows/amd64` produces a real PE32+ GUI binary and
`GOOS=windows go build ./... && go vet ./...` are green, but a cross-build proves only that the
code compiles for the target. Left for a Windows machine, or for the `windows` CI job, to
answer: whether `go test ./...` passes there (the tests that need a POSIX shell, a symlink or
Unix mode bits now skip themselves and say so, but the list was arrived at by reading, not by
running); whether the Credential Manager reader finds a secret `cmdkey` stored; whether
`taskkill /T` actually reaps a wrapper's grandchildren; whether the junction fallback satisfies
a real Codex session; whether the WebView2 host draws the frontend, what the native title bar
and DPI scaling look like, and whether SmartScreen's warning is the one `docs/release.md`
describes. The CI job is the standing check for the first of those; the rest need someone at a
Windows desktop.

`provider: agy` has now run end to end twice against a real Google account: a probe turn that
watched a `view_file` succeed and a `write_to_file` be refused by Sirdar's own project rule, and
a full `sirdar triage` that read four files, was refused `go test ./...`, and filed a note
citing real line numbers. What is still unsettled is written up at the end of
`docs/research/10-antigravity-wire-formats.md`: what `--sandbox` refuses on its own; why one
plan-mode `run_command` reported DONE with no side effect instead of a denial; what
`ProjectSettings.fileAccessPolicy` and `internetPolicy` govern, which is why Sirdar sets
neither; whether a path-scoped `read_file(<dir>)` rule matches by prefix, which is why the read
allow is `read_file(*)` and a session can therefore read outside the workspace; whether
`ANTIGRAVITY_PERM_GRANTS` is a supported way to carry the same rules in the environment instead
of a file; and how a workspace the operator has already trusted behaves in the CLI's default
mode.

`provider: acp` has run live on three agents (`docs/research/providers/acp-agents.md`, "Verified
live"): GitHub Copilot CLI passed triage, rca and fix; OpenCode passed triage and fix and failed
rca on a schema shape the sharpened retry now names; Kimi CLI got no model turn at all — the
free Kimi Code tier's monthly quota was spent before the first prompt, so the handshake, the
session-modes machinery and `session/set_mode` were exercised for real and everything downstream
of a model reply was not. `provider: qwen` has run live too, against a real OAuth login, and the
two bugs those runs found — fix mode registering no write tool, an error sentence fed to the note
validator — are fixed but have not been watched succeed: the free Qwen tier's quota was exhausted on 2026-09-15 (`Free quota exhausted` on every turn), so the fix-mode run only confirmed from the CLI's own `system/init` that the write tools are registered. Its fail-closed loopback permission hook
still has no live exercise: the tools it would have judged were excluded, so `permission_denials`
came back empty.

No live run yet for `provider: openai`, `notify`, or `webhooks`. Each has only been exercised
against a scripted fake server or fixture, never a real model or a real destination. Help Scout's `threadsPageSize`
(50) is inferred from the vendor's documented default for list endpoints, not observed against a
real paginated account.
`permissions.fetch` has been exercised against the fake CLIs and the tool set, never against a
real agent's fetch: the argument shape a live Qwen `web_fetch` or a given ACP agent's fetch
request actually uses is read off the protocol and the tool schemas, and a shape carrying the URL
under none of `url`, `urls` or `prompt` would be denied as "named no URL" rather than judged.

Codex's fix-mode sandbox is a `workspace-write` config Sirdar sets but does not implement or
verify; the snapshot guard (`internal/fix/guard.go`) is the actual backstop if that sandbox lets
a write through, including onto `.git`. Within that sandbox, whether
`item/fileChange/requestApproval` actually fires under `sandbox: workspace-write` +
`approvalPolicy: untrusted` is itself unverified: `docs/research/06-wire-formats.md` confirms
the command-approval and MCP-elicitation paths against a live `codex app-server` turn, and the
file-change path was built the same way, off the app-server's declared `ServerRequest` schema,
but has not itself been watched fire — no turn that actually has Codex write a file has been run
against it. One dogfood fix turn where the agent writes through a hook would confirm it; none has
been run, to keep this round free of paid Codex turns.

Three new helpdesk adapters carry the same kind of gap. Front's per-company API host is inferred
from reference examples rather than confirmed against a real workspace, so the adapter trusts
both the `api2.frontapp.com` form and a per-company `.api.frontapp.com` form
(`docs/research/adapters/front.md`). Gorgias's attachment download redirects to a signed URL
whose host no page names; the adapter trusts only `*.gorgias.com` and refuses any other redirect
target (`docs/research/adapters/gorgias.md`). ServiceNow's conversation history depends on
`sys_journal_field`, which is ACL-restricted on plenty of instances and unverified against a live
one, and its timestamps are read as naive values in the integration user's own display timezone
with no offset named — both flagged as the adapter's biggest open risks
(`docs/research/adapters/servicenow.md`).

`sirdar serve`'s same-origin and `Sec-Fetch-Site` guard on mutating routes
(`internal/httpapi/guard.go`, `docs/config.md`) is exercised only by tests that build requests
directly with the headers a real browser would send; no real cross-site page has been pointed at
a running listener to confirm the browser actually sends those headers the way the guard assumes.

`sirdar fix`'s worktree mode (`internal/fix/git.go`, the default since `stallwt` landed) carried
the two live ACP fix runs against the sandbox workspace, so the linked-worktree lifecycle, the
`--git-common-dir` hooks lookup and the removal-on-success path have now run outside fixtures
once each. A real ticket in the dogfood workspace, a deviation block, and `--accept-deviation`
publishing the reviewed commit have not.

Today's three review-and-inspect features ran live on 2026-09-15, against `provider: claude` and
a filesystem MCP server: `sirdar steer`, `sirdar runs diff --drop`, and `sirdar mcp
list/tools/call`. The steer answered the one number the feature depended on — Claude's `--resume`
reports `num_turns` and `total_cost_usd` for the resumed invocation alone, 3 turns and $0.3043
added to a run's existing 14 and $0.7198 to give 17 and $1.0241 — so the usage accumulation is
right and nothing is double-counted (`docs/steer.md`, "Verified live"). What is still fixture-only
in those three: steer's primed path for ACP and its fix-run amend, and the streamable-HTTP MCP
transport in `internal/mcpclient`, since the server driven was stdio.

`provider: cursor` is built from a capture of seven small turns
(`docs/research/11-cursor-wire-formats.md`): rate limiting and quota exhaustion were never
reached, so what a refused turn looks like is inferred, and there is no cost or turn count on the
wire to check a budget against. The breach watch — a completed edit or shell call failing the run
— has been exercised against fixtures, not against a Cursor session that actually wrote something.

## Known gaps (deliberate)

- Shortcut **labels** are drawn as ⌘↵, ⌘B, ⌘\, ⌘F, ⌘1…⌘9 on every platform, so a Windows or
  Linux operator reads a glyph their keyboard does not have. Every handler accepts Ctrl, so the
  shortcuts work; only the hint is wrong. The fix is one formatter over the ~12 render sites and
  a platform signal the frontend does not currently get from either shell, which is why it was
  not done blind in the `windows` round.

No writes to any helpdesk or tracker (`sirdar fix` writes to git and GitHub and
to nothing else). No auth on `sirdar serve` (loopback only unless `--allow-remote`); the `/hooks/`
webhook routes rely on the operator's own TLS termination plus each source's shared-secret
signature. Two concurrent runs of the same key can mis-pair the UI's Cancel button within a 2 s
window.

Four dogfood-deferred items from `docs/research/07-dogfood-findings.md` are now fixed (`quality`
branch): `doctor` has a third level, `[!!]` for a warning, so the MCP-visibility and no-`.mcp.json`
rows no longer print `[OK]` with a "warning:" detail and only a real failure exits non-zero
(`provider.Check.Level`, `cmd/sirdar/cmd_doctor.go`, desktop Settings). `MCPLooksLikeWrite` now
tokenises the whole tool name and denies a generically named passthrough
(`*_api_request`, `graphql`, `sql_execute`) instead of letting a read word beside a write word
win — `run_query` and `run_select` are denied by the default heuristic now and need
`permissions.mcp`. `sirdar register --markdown` fills Title and Company from the note's own
title and its `company`/`customer` frontmatter. `provider: openai` writes its message transcript
to `transcript.json` (0600) in the run directory after every turn, which is now its resume
handle for `sirdar resume` and the schema retry.

## Standing constraints

Sirdar is read-only by construction, enforced per provider, not by convention. It never writes
to a helpdesk or a tracker; `sirdar fix` writes only to git and GitHub. Every credential in
config is a reference (`env:`, `keychain:`, `file:`, `cmd:`), never a literal secret. Commits
carry no AI attribution. The git identity is never set by Sirdar or by a session working on
it; it is always whatever `git config user.email` already resolves to in that worktree.
