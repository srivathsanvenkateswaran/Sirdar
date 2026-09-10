# Verdict: build Sirdar, and what to build (2026-09-10)

Companion to 00 to 04 in this folder. Those carry the evidence; this carries the decision.

## Does anything already do this?

No. Checked on 2026-09-10 across orchestrators, AI SRE tools and helpdesk-native AI (02-landscape).

- Nothing ingests a helpdesk ticket (Zoho Desk, Zendesk, Freshdesk, Intercom) into a coding
  agent. Orchestrators start at developer trackers. Helpdesk AIs stop at "create a Jira issue".
- The two closest products are SaaS and vendor-billed: Devin Auto-Triage and Linear Agent.
  Neither reads DB, logs or APM through the user's own tools. Neither is bring-your-own-login.
- The evidence layer exists only on the SRE side (HolmesGPT is the one open-source, BYO-key
  tool that reads Jira, databases, Grafana, Datadog and writes findings back) and never writes code.
- No agentic tool translates. The only translation feature found is Freshdesk's Copilot, for
  human support agents.
- Root-cause note as the primary output, with the PR optional, appears in four places (Brex's
  internal oncall engineer, Pluno, Devin Auto-Triage, Claude Tag). Every open-source orchestrator
  is PR-first.

The exact combination (helpdesk ticket, own Claude Code or Codex login, evidence via MCP,
translation, RCA note, gated PR, board) is unoccupied.

## Is bring-your-own-subscription allowed?

Yes on the narrow path, per Anthropic's legal page as of today (03-licensing):

- Permitted: the user signs into the unmodified official `claude` binary with their own plan,
  and Sirdar spawns that binary. This is what Multica, Paperclip, Vibe Kanban and Conductor do.
- Prohibited and server-enforced: using the OAuth token directly against the API, or Sirdar
  collecting or storing Claude.ai credentials.
- Grey: driving the Agent SDK on a subscription login. The SDK docs say "not allowed unless
  previously approved"; the Help Center says such usage currently draws from the subscription.
  A planned move to a dollar credit pool was paused on 2026-06-15, not cancelled.

For Codex there is no written prohibition and the ecosystem does it openly, but no OpenAI
first-party statement explicitly permits it.

Design consequence: the provider layer spawns the user's installed CLI and speaks the
stream-json control protocol. An Agent SDK adapter can sit behind a flag. Every provider must
be switchable to API-key billing without code changes, because the June-15 rule may return.

## What Sirdar is

A local-first, open-source harness where the unit of work is a support ticket, not a coding
task. Each L2 engineer runs it on their own machine with their own agent logins and their own
read-only credentials for the evidence sources. It turns a ticket into a reviewed root-cause
note, and only after a human approves the note does it turn the note into a PR.

The plumbing (spawn an agent, stream events, worktrees, approvals) is commoditised: T3 Code,
Vibe Kanban and Symphony each built it, and the SDKs now ship most of it. The value Sirdar adds
is the loop around the ticket: ingestion from a helpdesk, translation, evidence playbooks that
encode the gotchas of each source, a note with a fixed shape, human gates before any write, and
a board that shows where each ticket is and how much of the quota it consumed.

## Architecture (proposed, to be refined in a design session)

**Runtime.** TypeScript on Node or Bun. One local server, one browser UI, SQLite. Started as
`npx sherpa` in the style of Vibe Kanban, no Electron at first. Reasons: the Claude Agent SDK is
TypeScript-first, Codex app-server bindings generate TypeScript, T3's event contracts are
TypeScript and MIT, and the existing custom MCP servers are TypeScript.

**Provider adapters.** `claude` via CLI spawn (`-p --output-format stream-json --input-format
stream-json --permission-prompt-tool stdio --include-partial-messages`), stripping
`ANTHROPIC_API_KEY` from the child unless the user chose API billing. `codex app-server` over
stdio. Normalised into one event vocabulary modelled on T3's `providerRuntime.ts`. The user's
5-hour and weekly windows are read from the provider (`get_usage`, `rate_limit_event`,
`account/rateLimits/updated`) and shown on the board; the queue pauses when a window is spent.

**Ticket sources.** Adapter interface in the shape of Symphony's tracker adapter: list, get,
threads, attachments, and an explicit `capabilities` flag for writes. First adapters: Zoho Desk
(read-only) and Janus (read, plus gated transitions). Then Zendesk, Jira, Linear. Credentials
are executed host-side as dynamic tools, never passed into the agent's environment.

**Evidence sources.** Plain MCP servers configured per workspace, plus a playbook per source: a
markdown skill that carries the gotchas (LogsQL not LogQL, APM retention clamps, absence needs a
control query, domain is not company id). Sirdar ships generic playbooks and the user adds
company-specific ones. The existing `support-triage` skill splits into these.

**Pipeline.** New, Gathering, Triaged, Awaiting review, Fix approved, PR open, Done, Needs info.
Human gates: leaving Triaged, and every outbound write (helpdesk reply, ticket transition, code
push, production SQL). The note is the approval artefact; approving the note is the command.

**Outputs.** A root-cause note with fixed frontmatter and sections (complaint translated,
conversation summary, repro, root-cause hypothesis with confidence, proposed fix, open
questions), a ranked digest, and, after approval, a PR made by the agent in a worktree off fresh
main with the ticket key in the title.

**Translation.** A first-class step. The original language is stored beside the translation;
the note is in the engineer's language; any customer-facing draft is translated back.

**Budgets.** Per-ticket max turns and max cost; per-run timeouts and stall detection as in
Symphony; retries with backoff; "blocked" when the agent asks a question nobody is there to answer.

**Multi-user.** Version 0 is single-user. A shared mode (Postgres, shared board, each user's
own agent login) can come later; Paperclip's heartbeat and budget semantics are the reference.

## What to reuse

- T3 Code (MIT): the provider event vocabulary and the command, decider, event, projection
  discipline. Reference adapters for permission modes and resume cursors.
- Symphony (Apache-2.0): SPEC.md contract, WORKFLOW.md shape, host-side dynamic tools, retry and
  stall parameters.
- Vibe Kanban (Apache-2.0, sunset): `NormalizedEntry` model, approval service interface, the
  Rust reimplementation of the stream-json control protocol as documentation of that protocol.
- Paperclip (MIT): adapter-as-package contract, budgets, heartbeats.
- HolmesGPT (Apache-2.0): toolset ideas for evidence sources.
- Your own MCP servers and skills: the seed for the first playbooks and the first adapters.

Do not fork T3 or Vibe Kanban wholesale. Do not embed Multica.

## Phasing

1. **v0, command line only.** `sherpa triage <ticket-key>` reproduces the current
   `support-triage` skill through adapters and writes the note. No UI. Test against the seven
   tickets closed on 2026-09-10 as a golden set: same inputs, compare notes.
2. **v1, local board.** `npx sherpa` opens a browser board with the ticket queue, live agent
   stream, quota meter, note review, and the "approve fix" gate that runs the `support-fix`
   flow in a worktree.
3. **v2, breadth.** Codex adapter, Zendesk/Jira/Linear adapters, golden-set replay as an eval
   suite, team mode.

Rounds of testing with the rotating L2 team fit between v1 and v2. Proposal to the company after
November 2026.

## Risks

- Anthropic reinstates the credit pool for programmatic use, or tightens "ordinary, individual
  usage" against orchestration. Mitigation: API-key switch per provider, visible quota meter,
  conservative default concurrency.
- Helpdesk PII flows into an LLM. Mitigation: local-first, no Sirdar cloud, adapters redact
  configurable fields, the original ticket never leaves the user's machine except to the model
  the user already trusts.
- The name. Decided 2026-09-10: **Sirdar**, the expedition lead who assigns the team's work.
  Plain "Sherpa" was rejected because it is taken on GitHub, npm, PyPI and by several funded
  AI products, and because the "helper" metaphor has drawn published criticism from the
  Sherpa community (01-name-collisions). `sirdar` was free on npm and PyPI at decision time;
  sirdar.ai and sirdar.app exist in unrelated markets. The README keeps the tribute explicit.
- Helpdesk API rate limits and attachment auth shapes vary per vendor. The Zoho adapter has
  already hit three URL shapes; expect the same elsewhere.

## Open questions for the design session

- Runtime: Node or Bun. Effect or plain TypeScript.
- Where notes live: inside the Sirdar data directory, in the user's vault, or both.
- Whether the tracker (Janus) or the helpdesk (Zoho) is the system of record for status.
- What "G1 workflow" refers to. The research found no such feature in T3 Code.
