# Handoff (updated 2026-09-11)

Read this first when opening a new session in this project.

## Where things are

Three worktrees, one repo (`git@github.com:srivathsanvenkateswaran/Sirdar.git`, personal identity
via the includeIf rule; never set `user.email` by hand):

| Worktree | Branch | State |
|---|---|---|
| `~/Documents/Personal/Sirdar` | `main` | Everything landed, including `provider: openai`: CLI (`init`, `doctor`, `triage`, `rca`, `resume`, `runs`, `register`, `serve`), Wails v2 desktop app under `desktop/`, built-in sources (Zoho Desk with OAuth refresh, Zendesk, Freshdesk, Jira, Linear, Azure DevOps, Rally, external stdio adapters), generic `helpdeskRef` regex, both dogfood fix waves (finish-on-final, `permissions.mcp`, attachment caps, host trust in every adapter, command policy, turn counting). Research + plans in `docs/`. |
| `desktop`, `adapters`, `providers` | branches on origin | Merged into `main` (a968576, 01172d3, 3129779); worktrees removed. |
| `~/Documents/Personal/Sirdar-evalfix` | `evalfix` | `sirdar eval` + `sirdar golden add` (replay a golden bundle through a real triage run and score it, `internal/eval`, `docs/eval.md`) and `sirdar fix` (the human-gated fix flow, `internal/fix`). Not merged. |

Ledgers (git-ignored) with every ruling and deferred minor: `.superpowers/sdd/*/progress.md` in
each worktree. Reports per task sit beside them.

Private, outside the repo: the Janus tracker adapter at `~/Documents/Work/Coding/sirdar-janus`
(work identity, no remote; binary `~/bin/sirdar-janus`; verified against the live API). The
dogfood workspace is `OXO.APIs/.sirdar/` (git-excluded) with playbooks encoding the support
gotchas and templates matching the Obsidian vault; notes go to `Support Duty/Sirdar/` so no
human-written note is touched. First real run (OMNI-3217): valid, good note in 9 min, then a success-path hang lost it; all
twelve findings fixed the same evening (`docs/research/07-dogfood-findings.md`,
`.superpowers/sdd/2026-09-10-sirdar-v0-triage-core/dogfood-report*.md`). Run 2 with the fixed
binary was in flight at handoff; its report is `dogfood-report-2.md`.

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
  (`provider: openai`, Sirdar's own loop) is implemented on `providers`; ACP and Qwen Code follow.
  Codex custom providers are a dead end (Responses API only).
- The read-only guarantee is enforced in three layers: `--disallowedTools` + policy for Claude,
  `sandbox: read-only` for Codex, and for `provider: openai` the tool set itself plus
  `provider.MatchCommand` (segment matching, no `$(`, backticks or redirection except `2>&1`
  and `2>/dev/null`) and the MCP write-verb heuristic behind `permissions.mcp`.
- On `evalfix`, `sirdar fix` is the one session that writes, and it flips all three layers at
  once through `SessionSpec.Mode`: no `--disallowedTools`, Codex's `workspace-write` sandbox,
  and `agenttools.WriteSet` in the openai loop. `provider.FixPolicy` still refuses everything
  but `Edit`/`Write`/`MultiEdit`, and matches shell commands against `permissions.fixBash`
  rather than `permissions.bash`. The human gate is the triage note's `status`, and a non-empty
  `deviationFromNote` in the agent's report stops the push until `--accept-deviation`.

## Next steps, in order

1. Extract the shared HTTP helpers per `docs/research/08-httpx-extraction.md` (host trust,
   redirect policy, Retry-After, body caps, name sanitising, per-ticket warnings): every adapter
   needed the same host-trust fix, so the helper is overdue.
2. Dogfood run 2 completed two tickets cleanly (OMNI-3217, OMNI-3193; notes written within
   milliseconds of the final answer); the run-2 findings are fixed. Next: a workspace
   `.mcp.json` in OXO.APIs listing only the read servers the playbooks need (with
   `mcp.workspaceOnly` the agent otherwise has no MCP tools), then two or three more tickets and
   a comparison against hand-written notes; fold gotchas into `.sirdar/playbooks/`. Known: Claude
   self-approves read-shaped Bash, so `permissions.bash` only sees the commands it is asked about.
3. Fix the Janus adapter's slow get-by-key (lists 200 tickets per probe; `doctor` times out).
4. Try `provider: openai` for real against Ollama (`qwen3-coder`) or OpenRouter on the golden
   ticket (it has never run against a live model; only the scripted fake server); then the roadmap's Phase 0 spikes (Anthropic-compatible endpoints behind
   `ANTHROPIC_BASE_URL`; Anthropic terms text) and Phase 2 (ACP client).
5. More helpdesks: Help Scout, Intercom, HubSpot (`docs/research/adapters/helpdesks.md`).
6. Release: goreleaser config exists; `version` is a var; CI on Go 1.26 with a frontend job;
   desktop CI matrix in `.github/workflows/desktop.yml` (unsigned artifacts).

## Known gaps (deliberate)

No writes to any helpdesk or tracker (`sirdar fix`, on `evalfix`, writes to git and GitHub and
to nothing else). No auth on `sirdar serve` (loopback only unless `--allow-remote`). Register markdown
export lacks title/company columns (`RegisterRow` has none). `provider: openai` has no resume
handle (a blocked run must be re-run). Two concurrent runs of the same key can mis-pair the UI's
Cancel button within a 2 s window.
