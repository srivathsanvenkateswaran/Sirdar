# Handoff (updated 2026-09-10, 19:10 IST)

Read this first when opening a new session in this project.

## Where things are

Three worktrees, one repo (`git@github.com:srivathsanvenkateswaran/Sirdar.git`, personal identity
via the includeIf rule; never set `user.email` by hand):

| Worktree | Branch | State |
|---|---|---|
| `~/Documents/Personal/Sirdar` | `main` | v0 CLI complete and reviewed: `init`, `doctor`, `triage`, `rca`, `resume`, `runs`, `register`. Zoho Desk (OAuth refresh) + external stdio adapters. Notes may file into subfolders. Research + plans in `docs/`. |
| `~/Documents/Personal/Sirdar-desktop` | `desktop` | Wails v2 app + `sirdar serve` (HTTP/SSE) + React UI (Board, Run detail, Register, Settings, quota). Whole-branch review was in progress at handoff; see its ledger. Not merged. |
| `~/Documents/Personal/Sirdar-adapters` | `adapters` | Built-in trackers: Jira, Linear, Azure DevOps, Rally (each task-reviewed; host-trust fixes applied). Wiring task (config, wire.go, doctor, generic `helpdeskRef` regex) was in progress. Not merged. |

Ledgers (git-ignored) with every ruling and deferred minor: `.superpowers/sdd/*/progress.md` in
each worktree. Reports per task sit beside them.

Private, outside the repo: the Janus tracker adapter at `~/Documents/Work/Coding/sirdar-janus`
(work identity, no remote; binary `~/bin/sirdar-janus`; verified against the live API). The
dogfood workspace is `OXO.APIs/.sirdar/` (git-excluded) with playbooks encoding the support
gotchas and templates matching the Obsidian vault; notes go to `Support Duty/Sirdar/` so no
human-written note is touched. First real run: see
`.superpowers/sdd/2026-09-10-sirdar-v0-triage-core/dogfood-report.md` when present.

## Decisions taken today

- Build, don't adopt (nothing exists for helpdesk → coding agent → RCA note; see `docs/research/02`).
- Name Sirdar; Apache-2.0.
- Providers spawn the user's own installed CLI (Claude Code stream-json, Codex app-server); API-key
  billing is a config switch. `docs/research/03` has the licensing detail.
- Desktop: Wails v2 (system webview), one React frontend for both the app and `sirdar serve`;
  the app observes run directories instead of hooking the runner.
- Adapters: stdlib HTTP, read-only, per-ticket warnings, credentialed downloads only to the
  configured host; `ListFilter.Limit` 0 means 100, cap 200.
- Models beyond Claude/Codex: plan only (`docs/superpowers/plans/2026-09-10-provider-roadmap.md`):
  spikes first, then a Sirdar-owned OpenAI-compatible loop, then an ACP client, then Qwen Code.
  Codex custom providers are a dead end (Responses API only).

## Next steps, in order

1. Read the desktop final-review result (ledger) and apply its fix wave; merge `main` into
   `desktop` (expect `go.mod` conflicts: take `go 1.26`, union of requires) and re-run
   `make ui && go test ./... && cd desktop && wails build`.
2. Finish A5 wiring on `adapters`, run a final review of that branch, merge `main` in, then
   open PRs `desktop` → `main` and `adapters` → `main` (or merge directly; single maintainer).
3. Dogfood: run `sirdar triage` on two or three real tickets with the OXO workspace and compare
   against the hand-written notes; fold gotchas into `.sirdar/playbooks/`.
4. Provider roadmap Phase 0 spikes (Ollama/llama.cpp behind `ANTHROPIC_BASE_URL`; Anthropic
   terms text; Qwen Code wire capture).
5. Helpdesk adapters after Zoho: Zendesk, Freshdesk, Help Scout (`docs/research/adapters/helpdesks.md`).
6. Release: goreleaser config exists; `version` is a var; CI on Go 1.26; desktop CI matrix in
   `.github/workflows/desktop.yml` (unsigned artifacts).

## Known gaps (deliberate)

No fix flow (the tool records a human's fix, it never makes one). No writes to any helpdesk or
tracker. No auth on `sirdar serve` (loopback only unless `--allow-remote`). Frontend `Cancel`
needs the store to register job ids (`setRunJob`). Register markdown export lacks
title/company columns (`RegisterRow` has none).
