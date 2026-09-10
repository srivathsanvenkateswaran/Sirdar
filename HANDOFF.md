# Handoff (2026-09-10)

Read this first when opening a new thread in this project.

## Where this came from

The research was done in a T3 Code thread started under the OXO.APIs work project, while the
project was still called Sherpa. T3 Code has no thread-move command and its importer binds a
transcript to a project by the working directory recorded inside it, so a copy of that
transcript was re-homed to this directory's Claude Code project folder with the recorded working
directory rewritten. If T3 Code lists an imported thread titled "Research AI Support Harnesses"
under this project, that is it. If not, everything the thread produced is in `docs/research/`.

## State

- Repo initialised on `main`; personal git identity resolves through the `includeIf` rule in
  `~/.gitconfig`. Do not set `user.email` by hand.
- Nothing committed yet.
- Name decided: Sirdar (was Sherpa during research; the research docs keep "Sherpa" where they
  discuss the name or the people).
- Research complete:
  - `docs/research/00-context.md`: the problem, the current workflow, the evidence sources.
  - `docs/research/01-name-collisions.md`: why plain "Sherpa" was rejected; decision recorded.
  - `docs/research/02-landscape.md`: nothing does the full loop; nearest are Devin Auto-Triage
    and Linear Agent (SaaS, vendor-billed).
  - `docs/research/03-licensing-byo-subscription.md`: spawning the unmodified official CLI
    with the user's own login is permitted; touching the OAuth token is not; SDK is grey.
  - `docs/research/04-harness-internals.md`: how T3 Code, Vibe Kanban, Symphony, Paperclip
    are built, and which pieces to copy.
  - `docs/research/05-verdict-and-proposal.md`: build it; proposed architecture and phasing.

## Decisions taken

- Build, don't adopt.
- Name: Sirdar.
- Local-first, open source, bring-your-own agent login.
- Provider layer spawns the user's installed CLI; API-key fallback is mandatory.
- Root-cause note first, PR second, human gate between them and before any write.

## Decisions pending (owner: Srivathsan)

- Runtime choices (Node/Bun, Effect or plain TS).
- System of record for ticket status (tracker vs helpdesk).
- GitHub handle to publish under (`sirdar` is taken by an inactive user).
- What "G1 workflow" means. The research found no such feature in T3 Code.

## Next step

Run a design session (brainstorming skill) against `05-verdict-and-proposal.md`, produce a spec,
then a plan for v0 (`sirdar triage <key>` on the command line, validated against the seven
tickets closed on 2026-09-10).

## Work-project material Sirdar generalises (not copied here)

- `~/.claude/skills/support-triage/SKILL.md` and `support-fix/SKILL.md`
- `~/Documents/Work/MCPs/` (oxo-mysql, metabase, newrelic, grafana) and its README
- `~/.claude/scripts/zoho-fetch-attachment.sh`

These contain employer-specific identifiers. Generalise the patterns; do not copy the files.
