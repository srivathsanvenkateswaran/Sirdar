# Handoff (updated 2026-09-10, ~20:25 IST)

Read this first when opening a new session in this project.

## Where things are

Three worktrees, one repo (`git@github.com:srivathsanvenkateswaran/Sirdar.git`, personal identity
via the includeIf rule; never set `user.email` by hand):

| Worktree | Branch | State |
|---|---|---|
| `~/Documents/Personal/Sirdar` | `main` | v0 CLI + desktop merged: `init`, `doctor`, `triage`, `rca`, `resume`, `runs`, `register`, `serve`; Wails v2 app under `desktop/`; Zoho Desk (OAuth refresh) + external stdio adapters; dogfood fixes (finish-on-final, `permissions.mcp`, attachment caps, Zoho host trust, command policy). Research + plans in `docs/`. |
| `~/Documents/Personal/Sirdar-adapters` | `adapters` | Built-in trackers Jira, Linear, Azure DevOps, Rally and helpdesks Zendesk, Freshdesk, all wired into config/doctor with the generic `helpdeskRef` regex. Whole-branch reviewed; last fix round (warning accumulation in Jira) in flight at handoff. Merge `main` in, then merge to `main`. |
| `~/Documents/Personal/Sirdar-providers` | `providers` | `provider: openai`: Sirdar's own loop over OpenAI-compatible APIs (`internal/provider/openai`, `internal/agenttools`, `internal/mcpclient`). Whole-branch reviewed; fix wave + merge of `main` in flight at handoff (keep `main`'s `policy.go`, port the agenttools names). Then merge to `main`. |
| `~/Documents/Personal/Sirdar-desktop` | `desktop` | Merged into `main` at a968576; worktree can be removed (`git worktree remove`). |

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

## Next steps, in order

1. Land `adapters` and `providers` on `main` (each: finish the in-flight fix round, merge `main`
   in, re-run the module tests, merge). At the providers merge keep `main`'s `policy.go` and port
   only the agenttools names, `bash` routing and the redirection/substitution rejection if not
   already identical. Follow-up: `docs/research/08-httpx-extraction.md` (shared HTTP helpers for
   the seven adapters).
2. Read `dogfood-report-2.md`; if the run completed cleanly, do two or three more tickets and
   compare against hand-written notes; fold gotchas into `.sirdar/playbooks/`.
3. Fix the Janus adapter's slow get-by-key (lists 200 tickets per probe; `doctor` times out).
4. Try `provider: openai` for real against Ollama (`qwen3-coder`) or OpenRouter on the golden
   ticket; then the roadmap's Phase 0 spikes (Anthropic-compatible endpoints behind
   `ANTHROPIC_BASE_URL`; Anthropic terms text) and Phase 2 (ACP client).
5. More helpdesks: Help Scout, Intercom, HubSpot (`docs/research/adapters/helpdesks.md`).
6. Release: goreleaser config exists; `version` is a var; CI on Go 1.26 with a frontend job;
   desktop CI matrix in `.github/workflows/desktop.yml` (unsigned artifacts).

## Known gaps (deliberate)

No fix flow (the tool records a human's fix, it never makes one). No writes to any helpdesk or
tracker. No auth on `sirdar serve` (loopback only unless `--allow-remote`). Register markdown
export lacks title/company columns (`RegisterRow` has none). `provider: openai` has no resume
handle (a blocked run must be re-run). Two concurrent runs of the same key can mis-pair the UI's
Cancel button within a 2 s window.
