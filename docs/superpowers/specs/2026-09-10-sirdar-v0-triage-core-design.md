# Sirdar v0: triage core (design)

Date: 2026-09-10. Status: approved in design session, awaiting written review.
Companion research: `docs/research/00` to `05`.

## Goal

A single Go binary, `sirdar`, that produces three kinds of note for a support ticket by running
one coding-agent session per run inside the workspace codebase, with the ticket fetched
host-side and the evidence gathered through the workspace's existing MCP servers:

- a **Triage Note** when the ticket arrives: what we knew on day one. Complaint, conversation,
  repro, root-cause hypothesis with confidence, proposed fix, open questions. Never rewritten.
- an **RCA Note** and a **Resolution Note** draft after the ticket is resolved, produced
  together by one run. The RCA answers why it happened: confirmed cause, evidence, blast
  radius, prevention, plus a review of the triage note so the agent's accuracy is recorded and
  its playbooks improve. The Resolution note is the audit record of what changed: PR, files,
  exact production statements, approvals, verification. The agent drafts it from the PR and
  the human's account; fields only a human can attest stay marked for the human.

The three notes mirror the existing vault templates (Triage, RCA, Resolution) and the register
rule that every issue gets all three, including trivial ones.

Every run leaves a complete audit trail on disk. No UI, no writes to any external system, no
code changes. Validated against the seven tickets closed on 2026-09-10.

## Decisions carried into this spec

| Decision | Choice |
|---|---|
| First sub-project | Command-line triage core; the board is sub-project two |
| Ticket sources in v0 | Zoho Desk built in; Janus as a private external adapter |
| Who fetches the ticket | Sirdar, host-side; the agent receives a bundle on disk |
| Workspace config | Reuse the repo's Claude Code setup; add `.sirdar/` |
| Language | Go, single static binary, lowest footprint |
| Providers in v0 | Claude Code and Codex, behind one interface |
| Run loop | One agent session per run, structured JSON at the end |
| Note types | Triage at intake; RCA + Resolution draft after resolution (added mid-session by the user, aligned to the vault templates) |

## Non-goals for v0

No board or web UI. No fix flow, branches or PRs (the RCA and Resolution notes record a fix
made by a human, they do not make one). No writes to tracker or helpdesk, including status transitions and
replies. No separate translation step (the agent translates in-session). No MCP server inside
Sirdar. No team or shared mode. No Slack. No automatic playbook edits (suggestions are written,
a human applies them). No evaluation runner beyond the register and the golden bundles.

## Concepts

- **Workspace.** A checked-out codebase with `.sirdar/config.yaml`. The agent session runs with
  the workspace as its working directory, so the repo's own `.mcp.json`, `CLAUDE.md`, skills
  and settings apply unchanged.
- **Ticket key.** The tracker's identifier (`OMNI-1234`). The tracker record carries a reference
  to the helpdesk ticket (for Janus, a URL in the description). If a workspace has no tracker,
  the key is the helpdesk id.
- **Bundle.** Everything the agent needs about the ticket, written to disk before the session
  starts.
- **Playbook.** A markdown file in `.sirdar/playbooks/` that tells the agent how to use one
  evidence source and which mistakes to avoid there.
- **Run.** One attempt for one key: a directory, a state, an event log, and at the end one or
  two notes. A key normally has one triage run and, later, one rca run (which writes the RCA
  note and the Resolution draft).
- **Register.** An append-only file in the workspace with one line per note produced: the
  triage confidence and classification, the resolution type, and the verdict on whether the
  triage hypothesis held. The audit index.

## CLI

```
sirdar init                      scaffold .sirdar/config.yaml and generic playbooks
sirdar doctor                    check CLIs, logins, adapters, notes directory
sirdar triage KEY [KEY...]       run triage for each key; prints a digest at the end
  --provider claude|codex        override config
  --model NAME                   override config
  --concurrency N                parallel runs across keys (default from config, default 1)
  --dry-run                      fetch and write the bundle and prompt, do not start the agent
sirdar rca KEY                   produce the RCA note and the Resolution draft for a resolved ticket
  --pr URL                       merged pull request; its diff is fetched with `gh` if available
  --resolution TEXT|FILE         what was done: remediation SQL, config change, guidance given,
                                 who authorised and executed it, verification output
  --provider, --model            as above
sirdar resume RUN_ID             continue a blocked or interrupted run
sirdar runs [KEY]                list runs and states
sirdar register                  print the register as a table (accuracy per ticket)
```

Exit code is non-zero if any run ended in `failed` or `over_budget`.

## Package layout

```
cmd/sirdar/            command wiring only
internal/config        config.yaml schema, credential refs, defaults, validation
internal/ticket        Ticket, Thread, Message, Attachment; bundle writer
internal/source        Tracker and Helpdesk interfaces; zohodesk/ (built in); plugin/ (external)
internal/provider      Provider and Session interfaces; claude/; codex/
internal/prompt        playbook loading, prompt assembly, embedded note JSON schema
internal/run           lifecycle, permission policy, budgets, concurrency, resume
internal/note          schemas for the three note types, validation, template rendering, digest, register
internal/store         run directory layout, events.jsonl, state.json
```

Each package exposes a small interface and hides its I/O. `internal/run` is the only package
that depends on all the others.

## Ticket sources

### Interfaces

```go
type Tracker interface {
    Get(ctx, key string) (TrackerTicket, error)         // fields + helpdesk reference
    List(ctx, filter ListFilter) ([]TrackerTicket, error) // optional; used by the board later
}

type Helpdesk interface {
    Get(ctx, id string) (HelpdeskTicket, error)
    Threads(ctx, id string) ([]Thread, error)             // chronological, with author role
    Attachments(ctx, id string, dir string) ([]Attachment, error) // downloads into dir
}
```

`TrackerTicket` carries key, title, description, priority, status, assignee, created/updated
times, URL, and `HelpdeskRef` (parsed from the description by a per-adapter rule; for Janus,
`Zoho Ticket URL:\s*(\S+)` and the trailing digits). `HelpdeskTicket` carries id, subject,
status, priority, channel, contact display name, company or account fields the helpdesk
exposes, created/updated times, URL. `Thread` is a list of `Message{At, Author, Role
(customer|agent|system), Text, AttachmentIDs}`. `Attachment{ID, Name, MIME, Path}`.

### Built-in: Zoho Desk

Reads `orgId`, base URL and a token ref from config. Uses the Desk REST API for the ticket,
conversations, thread content in plain text, and attachment download. Handles the three
attachment URL shapes seen so far (inline, thread, IM session). Refresh-token flow for the
self-client is in scope only if the stored token is a refresh token; otherwise the ref points to
a valid access token and `doctor` reports expiry.

### External adapters (any private source)

An external adapter is an executable path in config. Sirdar spawns it once per run and
exchanges newline-delimited JSON on stdin/stdout:

```
→ {"id":1,"method":"describe"}
← {"id":1,"result":{"name":"janus","roles":["tracker"],"version":"1"}}
→ {"id":2,"method":"tracker.get","params":{"key":"OMNI-1234"}}
← {"id":2,"result":{ ...TrackerTicket... }}
→ {"id":3,"method":"tracker.list","params":{ ...ListFilter... }}
← {"id":3,"error":{"code":"unsupported","message":"..."}}
```

Methods: `describe`, `tracker.get`, `tracker.list`, `helpdesk.get`, `helpdesk.threads`,
`helpdesk.attachments` (params include `dir`). Errors carry a `code` from a fixed set
(`not_found`, `auth`, `unsupported`, `rate_limited`, `internal`). The adapter's stderr is
captured into the run log. Sirdar sends `{"method":"shutdown"}` and waits up to 5 seconds
before killing the process. The Janus adapter is a separate, private Go program implementing
this protocol; a reference adapter that reads tickets from a JSON file ships in the public repo
under `examples/adapters/file/` and is what the tests use.

### Credentials

Config values of the form `env:NAME` or `keychain:SERVICE` are resolved at fetch time.
`keychain:` uses the macOS `security find-generic-password -s SERVICE -w` command; other
platforms only support `env:` in v0. Resolved values are held in memory, never written to the
run directory, and never placed in the agent's environment.

## Bundle

```
.sirdar/runs/<KEY>/<run-id>/       (kind: triage | rca, recorded in state.json)
  bundle/ticket.json        normalised TrackerTicket + HelpdeskTicket
  bundle/thread.md          chronological conversation, original language, roles, timestamps
  bundle/attachments/       downloaded files, named <index>-<original-name>
  prompt.md                 exactly what was sent to the agent
  events.jsonl              raw provider events, one per line, with sirdar timestamps
  result.json               validated note JSON (absent on failure)
  result.raw.txt            agent's final text when validation failed
  note.md                   rendered note (also copied to the notes directory)
  note-resolution.md        rca runs only: the Resolution draft (also copied)
  playbook-suggestions.md   rca runs only: proposed additions to playbooks, for human review
  state.json                run kind and state, provider, session/thread handle, budgets, timings
```

An rca run's bundle also contains `bundle/triage-note.md` (the triage note being reviewed),
`bundle/resolution.md` (the `--resolution` text) and, when `--pr` is given and `gh` is
installed and authenticated, `bundle/pr.md` (title, body, merge date) and `bundle/pr.diff`.

`run-id` is a sortable timestamp plus a short random suffix. `thread.md` writes each message as
a heading with time and role, then the text verbatim, then a list of attachment file names.
Image attachments are referenced by path so both providers can read them: Claude through its
file-reading tool, Codex as image input items attached to the turn.

## Configuration

`.sirdar/config.yaml`:

```yaml
workspace: OXO.APIs
provider: claude            # claude | codex
model: ""                   # provider default when empty
billing: subscription       # subscription | api   (api keeps ANTHROPIC_API_KEY in the child env)
sources:
  tracker:
    adapter: exec
    command: ~/bin/sirdar-janus
  helpdesk:
    adapter: zohodesk
    orgId: "60044805777"
    baseUrl: https://desk.zoho.in
    token: keychain:zoho-desk-token
notes:
  dir: ~/Documents/Work/Obsidian/Support Duty
  templates: .sirdar/templates       # optional; overrides the embedded defaults per note type
  filenames:
    triage: "{key} {slug}.md"
    rca: "{key} RCA {slug}.md"
    resolution: "{key} RES {slug}.md"
budget:
  maxTurns: 60
  maxMinutes: 25
  maxUsd: 5
concurrency: 1
permissions:
  bash:
    - "git log*"
    - "git show*"
    - "git grep*"
    - "rg *"
    - "dotnet build*"
playbooks: .sirdar/playbooks
```

Validation happens at load time with clear messages. Unknown keys are errors. `sirdar init`
writes this file with placeholders and adds `.sirdar/runs/` to `.git/info/exclude`.

## Playbooks and prompt assembly

Playbooks are loaded from the configured directory in filename order. `sirdar init` scaffolds
five: `10-helpdesk.md`, `20-logs.md`, `30-apm.md`, `40-database.md`, `50-code.md`, each with a
short generic body and a marked section for workspace-specific gotchas. All note types use the
same playbooks. The triage prompt is:

1. Fixed preamble (embedded in the binary): the agent's role; the run is read-only; every
   claim in the note must cite a source; an absence claim is invalid without a control query
   proving the source captures that event type; timestamps must state their timezone; do not
   guess ticket matches; stop and report when information is missing rather than inventing it.
2. The playbooks, verbatim, each under its filename as a heading.
3. The ticket: key, title, priority, tracker and helpdesk URLs, the path to the bundle, and the
   first 40 lines of `thread.md` inline so the session starts with context.
4. The note schema with a sentence of guidance per field.
5. The closing instruction: answer only with JSON matching the schema.

The rca prompt keeps steps 1 and 2, adds the triage note, the resolution text and the PR
material after step 3, replaces the triage schema in step 4 with the RCA and Resolution
schemas, and states the audit rule: fill only what the PR or the resolution text supports,
leave the rest null.

The assembled prompt is saved as `prompt.md` before the session starts, so `--dry-run` shows
exactly what would be sent.

## Providers

```go
type SessionSpec struct {
    Cwd          string
    Prompt       string
    Model        string
    OutputSchema []byte          // JSON schema for the note
    Policy       PermissionPolicy
    Budget       Budget
    Resume       string          // provider session/thread handle, empty for new
    Images       []string        // attachment paths for providers that take image input
    Env          []string
}

type Provider interface {
    Name() string
    Start(ctx context.Context, spec SessionSpec) (Session, error)
    Doctor(ctx context.Context) []Check   // installed, signed in, version
}

type Session interface {
    Events() <-chan Event
    Wait() (Result, error)
    Handle() string                       // for resume
    Cancel()
}
```

`Event` is a small tagged union: `AssistantText`, `ToolStarted{Name, Input}`, `ToolFinished`,
`PermissionAsked{Tool, Input, Decision}`, `Usage{Turns, InputTokens, OutputTokens, CostUSD}`,
`RateLimited{Message}`, `Question{Text}` (the agent asked the user something), `Final{Text,
Structured json.RawMessage}`. `Result` carries the final event, cumulative usage, and the
session handle.

### Claude Code

Command: the `claude` binary found on PATH or at `providers.claude.path`.

```
claude -p --output-format stream-json --input-format stream-json --verbose
       --include-partial-messages --permission-prompt-tool stdio
       --permission-mode default --json-schema <schema>
       [--model M] [--max-turns N] [--resume HANDLE]
```

The prompt is written to stdin as a `user` message and stdin is kept open for control
responses. Sirdar parses each stdout line: `system/init` (capture session id), `assistant`
deltas, `tool_use`/`tool_result`, `control_request` with `can_use_tool` (answer from the
policy with allow or deny plus a message), `rate_limit_event`, and `result` (structured output
and usage). `ANTHROPIC_API_KEY` is removed from the child environment unless `billing: api`.
`--bare` is never passed. On cancel, Sirdar sends SIGINT and waits up to 10 seconds.

### Codex

Command: `codex app-server` over stdio. Sequence: `initialize`, `initialized`, `thread/start`
with `cwd`, `sandbox: readOnly`, `approvalPolicy: never`; on resume `thread/resume`. Then
`turn/start` with the prompt as text plus `localImage` items for attachments, `model` if set,
and `outputSchema`. Notifications `item/*`, `turn/*`, `thread/tokenUsage/updated` map to
events. Any `requestApproval` server request is answered with decline, since the sandbox is
read-only and the policy denies writes; the denial is logged. `turn/completed` yields the final
agent message, parsed as the structured output. Codex reads MCP servers from the user's Codex
config; Sirdar does not inject them in v0. `Doctor` calls `mcpServerStatus/list` and reports.

## Permission policy

Evaluated by Sirdar for every tool request the provider surfaces:

| Tool class | Decision |
|---|---|
| Read, Glob, Grep, LS, WebFetch, WebSearch, any MCP tool | allow |
| Edit, Write, MultiEdit, NotebookEdit | deny, "triage runs are read-only" |
| Bash | allow if the command matches a pattern in `permissions.bash`, else deny with the pattern list in the message |
| Task or subagent spawning | allow (children inherit the same policy through the same channel) |
| Anything unknown | deny |

Patterns are shell-style globs matched against the trimmed command. Codex enforces the same
outcome through `sandbox: readOnly`; the policy still runs for logging.

## Run lifecycle

```
preparing  → running → completed
                     → failed        (provider crash, fetch error, schema failure twice)
                     → blocked       (agent asked a question, or rate limited)
                     → over_budget   (turns, minutes, or USD exceeded)
```

- Preparing: resolve config, fetch tracker then helpdesk, download attachments, write bundle
  and prompt. Any error here fails the run before an agent process exists.
- Running: start the session, stream events to `events.jsonl` and a one-line-per-event progress
  view on stderr, watch budgets. Wall-clock budget cancels the session and marks
  `over_budget`; turn and USD budgets are enforced from usage events.
- Completion: validate `Final.Structured` against the schema. On failure, send one follow-up
  user message quoting the validation errors and asking for corrected JSON; on a second
  failure, save `result.raw.txt` and mark `failed`.
- Blocked: persist the session handle. `sirdar resume RUN_ID` restarts with `Resume` set and,
  for a question, prompts on the terminal for the answer and sends it as the next message.
- Concurrency: a worker pool of `concurrency` runs across the given keys. Runs never share
  state. Rate-limit events pause the pool for the interval the provider reports, if any.

`state.json` is rewritten on every transition and includes timings, usage, and the handle.

## Notes

All three note types are produced the same way: bundle, prompt, one session, structured JSON,
validation, template rendering. A triage run yields one JSON document and one note. An rca run
yields one JSON document with two top-level objects, `rca` and `resolution`, and two notes.

### Rendering is template-driven

Each note type has a Go `text/template` applied to the validated JSON. Defaults are embedded
in the binary and mirror the vault's three templates, with generic frontmatter keys
(`tracker_key`, `tracker_url`, `helpdesk_id`, `helpdesk_url`, `customer`, `customer_id`).
A workspace can drop its own `triage.md.tmpl`, `rca.md.tmpl`, `resolution.md.tmpl` into
`notes.templates` to use its vault's exact frontmatter keys and headings (`janus_key`,
`zoho_url`, and so on). `sirdar init --templates` writes the defaults there as a starting
point. Sirdar validates that a custom template parses and renders against a sample document
before it is ever used on a real run.

### Triage Note

Schema (abridged; full JSON Schema embedded in `internal/prompt`):

```
ticket:         { key, title, trackerUrl, helpdeskId, helpdeskUrl, priority, service,
                  customer, customerId }
title:          string                    # one-line issue title
complaint:      string                    # translated, faithful to tone and urgency
timeline:       [ { at, role, summary } ] # incl. what L1 already told the customer
reproSteps:     [ string ]
rootCause:      { hypothesis, confidence: high|medium|low|unknown,
                  evidence: [ { source, query, finding } ],
                  codeRefs: [ "path:line" ] }
blastRadius:    string
classification: code|data|config|not-a-bug|unknown
proposedFix:    { description, files: [string], remediationSql: string, risks: string }
openQuestions:  [ string ]
```

Frontmatter (default template): `tags: [support-duty, triage]`, tracker and helpdesk keys and
URLs, `customer`, `customer_id`, `date`, `priority`, `service`, `status: triaged`, `run`,
`provider`. Body sections in the order of the vault template: Customer Complaint (translated),
Conversation Summary, Repro Steps, Root Cause Hypothesis, Proposed Fix, Open Questions, with a
register/RCA/Resolution link line under the title. An existing triage note for the same key is
overwritten only while its `status` is still `triaged`.

### RCA Note

Produced by `sirdar rca KEY`. Inputs: the latest triage note for the key (required), the
resolution supplied by the human, and the merged PR when there is one. The session runs in
the workspace with the same read-only policy, so the agent can read the merged code, re-query
evidence sources to confirm the cause, and compare against the triage hypothesis.

Schema (abridged), following the vault's RCA template section by section:

```
rca:
  title:               string
  summary:             string                     # 3 to 5 sentences a manager can read alone
  impact:              { customersAffected, recordsAffected, financialImpact,
                         firstOccurrence, detection, timeToDetect }
  timeline:            [ { at, event, evidence } ]
  rootCause:           { description, codeRefs: ["path:line"], offendingCode, mechanism }
  contributingFactors: [ string ]
  evidence:            { database: [..], logs: [..], apm: [..], code: [..], attachments: [..] }
                       # each entry { query, result } or { ref, note }
  blastRadius:         { query, count, scope: one-off|systemic, reasoning }
  whyNotCaughtEarlier: string
  prevention:          [ { action, type: code|test|monitoring|process, owner, ticket } ]
  openQuestions:       [ string ]
  classification:      code|data|config|not-a-bug
  severity:            high|medium|low
  confidence:          high|medium|low|unknown
  origin:              string                     # workspace-defined vocabulary, e.g. omni|legacy|pos|integration
  triageReview:        { verdict: confirmed|partial|wrong,
                         gotRight: string, missed: string, whyMissed: string }
  lessons:             [ string ]
  playbookSuggestions: [ { playbook, addition, reason } ]
```

`triageReview`, `lessons` and `playbookSuggestions` are the self-improvement fields. They are
rendered at the end of the RCA note under "Triage Review" and "Lessons", and
`playbookSuggestions` are additionally written to `playbook-suggestions.md` in the run
directory as a ready-to-paste block per playbook. Sirdar never edits a playbook itself.

Frontmatter (default): `tags: [support-duty, rca]`, tracker and helpdesk keys and URLs,
`customer`, `customer_id`, `date_reported`, `date_rca`, `service`, `classification`,
`severity`, `confidence`, `origin`, `triage_verdict`, `related`, `run`, `provider`.

### Resolution Note (draft)

Produced by the same rca run. It is the audit record, so the agent may only fill what it can
source from the PR or the human's `--resolution` text. Anything it cannot source is left null
and rendered as a visible `<fill: ...>` marker; the note's `status` is `proposed` until a
human edits it.

Schema (abridged), following the vault's Resolution template:

```
resolution:
  title:           string
  resolutionType:  code-fix|data-fix|config-change|guidance|wont-fix|duplicate
  whatWasWrong:    string                       # 2 to 3 sentences, no repeat of the RCA
  whatWeChanged:   string
  codeChange:      { pr, prStatus, mergedAt, files: [ { path, what } ], reviewer, deployed } | null
  dataChange:      { authorisedBy, authorisedAt, executedBy, executedAt,
                     preVerificationSql, preOutput, changeSql, rowsAffected,
                     postVerificationSql, postOutput, rollbackPlan, sideEffects } | null
  verification:    [ { check, environment, result, date, by } ]
  customerOutcome: { told, confirmedFixed, helpdeskClosed, trackerStatus }
  residualRisk:    [ string ]
  lessons:         string
```

SQL fields are copied verbatim from the human's resolution text; the agent must not compose
production statements. Frontmatter (default): `tags: [support-duty, resolution]`, keys and
URLs, `customer`, `customer_id`, `service`, `resolution_type`, `status: proposed`, `pr`,
`pr_status`, `approved_by`, `applied_by`, `applied_at`, `verified_at`, `related`, `run`.

After an rca run, the triage note's frontmatter gets `status: resolved` and `rca:` and
`resolution:` links; nothing else in it changes.

### Register

`.sirdar/register.jsonl`, one line per note produced:

```
{"key","kind":"triage|rca|resolution","runId","date","provider","model","service",
 "classification","confidence","severity","turns","costUsd","triageVerdict","notePath"}
```

`sirdar register` prints one row per key: triage date, confidence, classification, RCA date,
resolution type, triage verdict, and which of the three notes exist. `--markdown` prints the
rows in the vault's `_Issue Register` table shape for pasting. Over time this is the record of
how often the agent's first hypothesis held, per service and per confidence level, and the
input to later evaluation work. `init` excludes the register alongside `runs/`; committing it
is the user's choice.

## Error handling summary

| Failure | Behaviour |
|---|---|
| Config invalid | exit before any run, message names the key |
| Credential ref unresolvable | fail that run in `preparing` |
| Tracker or helpdesk error | fail that run, adapter error code and message in `state.json` |
| Attachment download error | continue; list the URL under open questions in the prompt |
| Provider binary missing or not signed in | fail fast; `doctor` explains |
| Provider process exits non-zero | fail; last 50 stderr lines in `state.json` |
| Malformed provider line | log and skip; ten in a row fails the run |
| Schema validation fails | one retry turn, then fail with raw text kept |
| `sirdar rca` with no triage note for the key | exit with a message; run `triage` first |
| `--pr` given but `gh` missing or unauthenticated | continue without the diff; noted in `state.json` and in the prompt |
| Budget exceeded | cancel, `over_budget`, note not written |
| Ctrl-C | cancel all sessions cleanly, mark runs `blocked` with handles for resume |

## Testing

- `internal/source/zohodesk`: unit tests against recorded JSON fixtures (redacted), including
  all three attachment URL shapes.
- `internal/source/plugin`: tests drive the file-based example adapter through the protocol,
  including error codes and shutdown timing.
- `internal/provider/claude` and `codex`: tests point the provider at a fake binary
  (`testdata/fakeclaude`, `testdata/fakecodex`, built from Go test helpers) that replays a
  scripted transcript: init, tool calls, a permission request that must be denied, a rate-limit
  event, a malformed final output followed by a valid one. Assertions cover the event
  sequence, the policy decisions written back, and the resume handle.
- `internal/prompt` and `internal/note`: golden-file tests for the three note types with the
  default templates, a custom-template override, the `<fill>` markers, the register line, and
  the triage-note frontmatter update performed by an rca run.
- `internal/run`: state transitions with a stub provider and stub sources; budget enforcement;
  concurrency; Ctrl-C handling.
- Integration: `sirdar doctor` and `sirdar triage --dry-run` against the file adapter in CI.
- Golden set: the seven 2026-09-10 tickets' bundles stored under `~/.sirdar/golden/<KEY>/`
  (never in the repo). Manual comparison of generated notes against the human-written ones;
  an eval command is a later sub-project.

## Resolved open questions

- Notes live in the configured `notes.dir`, with a copy in the run directory.
- The tracker is the system of record for the key; the helpdesk is the system of record for
  the conversation. Neither is written in v0.
- Translation happens inside the session; the note holds the English, the bundle holds the
  original.
- Self-improvement is human-gated: the RCA run proposes playbook additions and records its own
  triage verdict; a person applies the additions. Nothing in v0 rewrites prompts or playbooks
  automatically.

## Still open (not blocking v0)

- GitHub handle to publish under; `sirdar` is taken by an inactive user.
- Whether Codex should receive MCP config from Sirdar in a later version.
- The meaning of "G1 workflow" from the original brief; no such T3 Code feature was found.
