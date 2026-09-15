# Handoff (updated 2026-09-11)

Read this first when opening a new session in this project.

## Where things are

Three worktrees, one repo (`git@github.com:srivathsanvenkateswaran/Sirdar.git`, personal identity
via the includeIf rule; never set `user.email` by hand):

| Worktree | Branch | State |
|---|---|---|
| `~/Documents/Personal/Sirdar` | `main` | Everything landed. CLI: `init`, `doctor`, `triage`, `rca`, `resume`, `runs`, `register`, `serve`, `eval`, `golden`, `fix`. Wails v2 desktop app under `desktop/`. Five providers: `claude`, `codex`, `openai` (Sirdar's own agent loop, any OpenAI-compatible endpoint), `acp` (any Agent Client Protocol agent, e.g. Gemini CLI, Goose, OpenCode, `internal/provider/acp`), `qwen` (native Qwen Code adapter, fail-closed loopback permission hook). Codex workspace-MCP parity: a per-session `CODEX_HOME` carrying only the workspace's `.mcp.json` servers under `mcp.workspaceOnly`, with MCP, shell and file-change approvals routed through Sirdar's permissions. Tracker adapters: Zoho Desk (OAuth refresh), Zendesk, Freshdesk, Jira Cloud/Data Center, Linear, Azure DevOps, Rally, external stdio adapters, generic `helpdeskRef` regex. Helpdesk adapters: Zoho Desk, Zendesk, Freshdesk, Help Scout, Intercom, HubSpot, all on the shared `internal/source/httpx` HTTP helpers (host trust, redirect policy, Retry-After, capped reads). Credential stores: `env:`, `keychain:` (Keychain on macOS, libsecret on Linux, DPAPI-backed store on Windows), `file:`, `cmd:` (`docs/credentials.md`). Arabic/RTL i18n: `language:` config block, bilingual note fields, RTL-aware desktop UI. Inbound webhooks: `sirdar serve` triggers per source with signature verification (`docs/webhooks.md`). Run-completion notifications: Slack, Teams, generic webhook, timestamped HMAC (`docs/notifications.md`). `sirdar eval` + `sirdar golden add` (golden-set scoring, `internal/eval`, `docs/eval.md`) and the confined `sirdar fix` (human-gated fix flow, `internal/fix`). Release pipeline: goreleaser, Homebrew tap, desktop zips (`docs/release.md`). Repo hygiene: CONTRIBUTING, SECURITY, CODE_OF_CONDUCT, issue/PR templates, dependabot, `docs/architecture.md`. MkDocs docs site published via GitHub Pages. Cross-provider web-fetch allow-list (`permissions.fetch`, empty by default, denies every fetch). Both dogfood fix waves (finish-on-final, `permissions.mcp`, attachment caps, host trust in every adapter, command policy, turn counting). Research + plans in `docs/`. |
| `desktop`, `adapters`, `providers` | branches on origin | Merged into `main` (a968576, 01172d3, 3129779); worktrees removed. |
| `~/Documents/Personal/Sirdar-qwen`, `~/Documents/Personal/Sirdar-codexmcp` | `qwen`, `codexmcp` | Merged into `main` (a411cfa, 38104a8); worktrees removed. |

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
- The read-only guarantee is enforced per provider: `--disallowedTools` + policy for Claude,
  `sandbox: read-only` for Codex, for `provider: openai` the tool set itself plus
  `provider.MatchCommand` (segment matching, no `$(`, backticks or redirection except `2>&1`
  and `2>/dev/null`) and the MCP write-verb heuristic behind `permissions.mcp`, and for
  `provider: acp` the same `PermissionPolicy` applied to every `session/request_permission`
  call, declining outright when the agent asks for a write-shaped capability.
- `internal/source/httpx` centralises what every adapter needed anyway (host trust, redirect
  policy, Retry-After, capped reads, per-ticket warnings). All ten built-in source adapters,
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
  (bar `runs/`, `register.jsonl`, `eval/`) and `provider.HooksDir` before the session and again
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

## Next steps, in order

1. Dogfood the new features for real: a webhook-triggered run, a notify post, `sirdar eval`
   against the golden set, and `sirdar fix` on an actual ticket. None of them has a live run yet.
2. First live runs against a real model: `provider: openai` (Ollama `qwen3-coder` or
   OpenRouter), `provider: acp` (start with Gemini CLI per `docs/research/providers/acp-agents.md`),
   and `provider: qwen` against a real Qwen Code login. None of the three has run against
   anything but scripted fake agents so far.
3. Janus get-by-key is fixed in the private adapter (`?ticket_keys=` filter); `sirdar doctor` in
   OXO.APIs passed every row on 2026-09-11 with the merged binary, and a `--dry-run` triage of the
   golden ticket produced its bundle and prompt. Nothing to do here unless doctor regresses.
4. Continue dogfood run 2's follow-ups: the workspace `.mcp.json` in OXO.APIs exists (doctor reports
   it with 12 `permissions.mcp` patterns); next is two or three more tickets and a comparison against hand-written notes; fold gotchas into
   `.sirdar/playbooks/`. Known: Claude self-approves read-shaped Bash, so `permissions.bash` only
   sees the commands it is asked about.
5. Live-check `permissions.fetch` against a real Claude Code install: confirm that a
   `WebFetch(domain:…)` rule in `~/.claude/settings.json` is in fact what it is documented to
   be — an allow rule the CLI applies before `--permission-prompt-tool`, and one that
   `--disallowedTools WebFetch` still beats. Both halves are read off Claude Code's documented
   precedence, not watched on a live turn.

## What is unverified

No live run yet for `provider: openai`, `provider: acp`, `provider: qwen`, `notify`, `webhooks`,
or `sirdar fix`. Each has only been exercised against a scripted fake server or fixture, never a
real model or a real destination. `provider: qwen`'s fail-closed loopback permission hook has
never been run against a real Qwen login — only against the scripted fake CLI in tests. Help
Scout's `threadsPageSize` (50) is inferred from the vendor's documented default for list
endpoints, not observed against a real paginated account. `permissions.fetch` has been exercised
against the fake CLIs and the tool set, never against a real agent's fetch: the argument shape a
live Qwen `web_fetch` or a given ACP agent's fetch request actually uses is read off the protocol
and the tool schemas, and a shape carrying the URL under none of `url`, `urls` or `prompt` would
be denied as "named no URL" rather than judged.

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

## Known gaps (deliberate)

No writes to any helpdesk or tracker (`sirdar fix` writes to git and GitHub and
to nothing else). No auth on `sirdar serve` (loopback only unless `--allow-remote`); the `/hooks/`
webhook routes rely on the operator's own TLS termination plus each source's shared-secret
signature. Register markdown
export lacks title/company columns (`RegisterRow` has none). `provider: openai` has no resume
handle (a blocked run must be re-run). Two concurrent runs of the same key can mis-pair the UI's
Cancel button within a 2 s window.

## Standing constraints

Sirdar is read-only by construction, enforced per provider, not by convention. It never writes
to a helpdesk or a tracker; `sirdar fix` writes only to git and GitHub. Every credential in
config is a reference (`env:`, `keychain:`, `file:`, `cmd:`), never a literal secret. Commits
carry no AI attribution. The git identity is never set by Sirdar or by a session working on
it; it is always whatever `git config user.email` already resolves to in that worktree.
