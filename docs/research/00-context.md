# Context: why Sirdar, and what it generalises (written 2026-09-10)

## The problem as lived

An L2 support engineer at a Saudi retail-platform company receives Arabic tickets routed by a
front-line teammate. Each ticket needs the same loop: read the helpdesk ticket and its chat
threads, translate, pull screenshots, look at production logs and APM around the reported time,
query the database or the warehouse for the customer's data, find the code path, write a root
cause note, and sometimes ship a fix PR. Doing this by hand in a coding-agent CLI works but is a
back-and-forth prompt session per ticket. On 2026-09-10 the same loop, run through T3 Code with
background subagents, closed seven tickets in about thirty minutes.

Sirdar is the productised version of that loop: an open-source harness where a ticket is the
unit of work, evidence sources are pluggable MCP servers, the agent is whichever coding CLI the
user already pays for, and the output is a reviewed root-cause note first and a PR second.

## The existing workflow Sirdar replaces

Two Claude Code skills already encode the loop. They are the spec for v0.

**support-triage** (read-only):
1. List open bugs assigned to me in the tracker under the month's support epic.
2. Fan out one subagent per ticket. Each: fetch tracker ticket, extract the helpdesk ticket id
   from the description, fetch the helpdesk ticket, threads and conversations, translate Arabic
   to English preserving tone and details, fetch every image attachment through a pre-approved
   read-only script and look at it, pull production logs (VictoriaLogs through the Grafana MCP)
   around the reported time filtered by service, error level, company id, check blast radius
   across companies, cross-check APM (New Relic NRQL) to prove whether an endpoint executed,
   validate any absence claim with a control query, debug against the codebase with file:line
   references, return a structured result.
3. Main session writes one note per ticket to an Obsidian folder with fixed frontmatter
   (tracker key, helpdesk URL, priority, service, status) and sections: complaint translated,
   conversation summary, repro steps, root-cause hypothesis with confidence, proposed fix, open
   questions.
4. Print a severity-ranked digest.
Hard constraints: helpdesk read-only, no code edits, no status transitions, SELECT-only SQL.

**support-fix** (write, only after the human reviewed the note):
branch off fresh main, apply the fix from the note, build, commit without AI attribution, push,
open a PR titled `[KEY-1234] fix: ...` with symptom, root cause, fix, links; update the note's
status and PR link; after merge, write the permanent bug-fix page and retire the triage note.

## Evidence sources in use today (all read-only, all MCP or thin scripts)

| Source | Mechanism | Encoded gotchas |
|---|---|---|
| Tracker (Janus, an in-house Jira replacement) | MCP + REST fallback | API returns 200 with an error body; check the body |
| Helpdesk (Zoho Desk) | claude.ai connector MCP + attachment fetch script with Keychain token | `include` param unreliable; three attachment URL shapes; chat transcripts via thread plainText |
| Logs (VictoriaLogs via Grafana MCP) | MCP | LogsQL not LogQL; stream selectors silently return zero rows; case-sensitive text filters; `image` label shows deployed build; some services have no inbound logging |
| APM (New Relic) | custom MCP, Keychain key | 8-day retention silently clamps; `timestamp` is start, `duration` seconds; absence claims need a control query; legacy platform has no agent |
| Database (MySQL prod replica + staging) | custom MCP via Teleport tunnel | replica only; `AddedDate` is UTC+3; MCP renders a further shift; domain vs company id are different numbers |
| Warehouse (Metabase over StarRocks) | custom MCP, read-only | table naming `dwh_src_<schema>_<table>`; not every schema mirrored |
| Chat (Slack) | claude.ai connector MCP | replies answer only what was asked; drafts go in a fenced block |

Design rules already adopted for the custom servers: stateless, secrets in the OS keychain
never in config, read-only by construction, gotchas encoded as tools (`resolve_company`,
`verify_absence`, `check_retention`) rather than as prompt text.

## Constraints Sirdar must inherit

- Human gate before any write: helpdesk replies, ticket transitions, code changes, production
  SQL all require explicit approval, and the note is the approval artefact.
- Absence claims are not findings until a control query proves the data source captures the
  event type at all.
- Scale review: any proposed fix is sized against the largest tenant before it is proposed.
- Tenancy is explicit: every query is scoped by the tenant id, and the id quoted in a ticket
  is often not the id the database keys on.
- Bilingual by default: the ticket is Arabic, the engineer's note is English, the customer
  reply (if any) is Arabic again.

## Adoption plan

Open source from day one. Two or three rounds of testing with the rotating L2 support team, then
a proposal to the company after November 2026.
