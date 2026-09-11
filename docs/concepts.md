# Concepts

Sirdar's vocabulary is small and every piece maps to something concrete on disk. This page walks
through it in plain prose; [Configuration](config.md) has the exact keys and
[Sources](adapters.md) has the adapter details.

## Workspace

A workspace is a checked-out codebase with a `.sirdar/config.yaml` in it. The agent session runs
with the workspace as its working directory, so the repo's own `.mcp.json`, `CLAUDE.md`, skills
and settings all apply unchanged — Sirdar doesn't invent a separate environment for the agent to
work in, it reuses the one you already have.

## Ticket key

The tracker's identifier for the ticket — `OMNI-1234`, say. The tracker record usually carries a
reference to the corresponding helpdesk ticket (sometimes natively, sometimes as a URL pasted
into the description that a `helpdeskRef` rule extracts). If a workspace has no tracker
configured at all, the helpdesk id serves as the key directly.

## Bundle

Everything the agent needs to know about a ticket, gathered and written to disk before the agent
session starts: the normalized ticket record, the full conversation thread in its original
language with roles and timestamps, and any downloaded attachments. Fetching happens host-side,
outside the agent session, so the agent never talks to the tracker or helpdesk API directly — it
only ever reads files.

## Playbook

A markdown file in `.sirdar/playbooks/`, loaded into the prompt for every run. Each playbook
covers one evidence source — a log system, a database, an APM tool — and encodes the gotchas
particular to that source and that workspace: which query language it actually speaks, what its
retention window clamps to, what a negative result does and doesn't prove. `sirdar init`
scaffolds generic starting playbooks; the value comes from editing them as you learn where the
agent goes wrong.

## Run

One attempt at producing notes for one key: a directory on disk holding a state, an event log,
and the resulting note or notes. A run moves through `preparing → running → completed`, or off
to `failed`, `blocked` (the agent asked a question, or hit a rate limit — continue it with
`sirdar resume`), or `over_budget` (it ran past its turn, time, or cost budget). A key normally
gets one triage run at intake and, later, one rca run once the fix lands.

## Register

An append-only file in the workspace with one line per note produced, recording the triage
confidence and classification, the resolution type, and the verdict on whether the original
triage hypothesis held up. It's the audit index: over time it's the record of how often the
agent's first hypothesis was right, broken down by service and confidence level.

## Note types

Sirdar produces three kinds of note, mirroring a support vault's usual shape:

- **Triage.** Written by `sirdar triage` when the ticket arrives: the translated complaint, a
  conversation summary, repro steps, a root-cause hypothesis with a confidence level and cited
  evidence, a proposed fix, and open questions. It is never rewritten once work moves past it —
  it's a record of what was known on day one, not a living document.
- **RCA.** Written by `sirdar rca` once the fix is resolved: the confirmed cause, the evidence
  behind it, the blast radius, what should prevent a recurrence, and a review of whether the
  triage hypothesis held — right, partially right, or wrong, and why. That review is how the
  playbooks improve over time.
- **Resolution.** Written by the same `sirdar rca` run: the audit record of what actually
  changed — the PR, the files, the exact statements run in production, who approved and executed
  them, and how the fix was verified. The agent drafts it from the merged PR and the resolution
  text you give it; anything it can't source from those two places is left as a visible
  `<fill: ...>` marker for a human to complete.

## The three read-only layers

Sirdar's core guarantee is that a run can only read — it never writes to the codebase, the
tracker, or the helpdesk. That guarantee isn't enforced in one place; it's layered so that no
single bug removes it:

1. **The provider's own sandbox.** Claude Code is started with its write tools
   (`Edit`, `Write`, `MultiEdit`, `NotebookEdit`) denied outright; Codex is started with
   `sandbox: readOnly`. This is the first line, enforced by the agent CLI itself before Sirdar's
   own policy ever runs.
2. **Sirdar's own permission policy.** Every tool call the provider surfaces is evaluated again
   on Sirdar's side: reads and MCP tools are allowed, write tools are denied with a message
   explaining the run is read-only, and `Bash` commands are checked against the
   `permissions.bash` glob allow-list configured for the workspace — a heuristic, not a sandbox,
   that also refuses command substitution, redirection, and paths that reach outside the
   workspace root.
3. **The MCP write-verb heuristic.** For `provider: openai`, where Sirdar drives the tool loop
   itself with no separate CLI sandbox underneath it, an MCP tool whose name contains a write
   verb (`create`, `update`, `delete`, `send`, and the rest) is denied unless it's explicitly
   allow-listed in `permissions.mcp` — and once that list is non-empty, it becomes the whole
   rule, for a run you want to be read-only by construction rather than by naming convention.

None of these three is a real sandbox on its own — they're pattern matches over text, not a
container boundary — which is exactly why there are three of them layered rather than one relied
on absolutely.

## Human gates

Sirdar never turns a note into an action on its own. Two gates keep a human in the loop at the
points that matter: nothing is written back to the tracker or helpdesk, ever — Sirdar reads
tickets and writes notes to disk, full stop — and the RCA/Resolution flow only ever records a
fix a person already made and merged; it doesn't propose one, branch for one, or open a PR for
one. Reviewing a triage note before acting on its hypothesis, and reviewing a Resolution draft's
`<fill: ...>` markers before treating it as the record of what happened, are the same kind of
gate applied to Sirdar's own output.
