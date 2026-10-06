# Shadow mode: triage every new ticket unattended, file nothing, and score it against what the operator did

Status: draft for the owner's review (2026-10-06). Approved direction: rung 1 of the trust ladder
(Shadow → Suggest → Act → Autonomous) plus the playbook feedback loop. Rungs 2 and 3 are named as
seams at the end and are not built here.

Depends on: `2026-10-04-session-engine-design.md` (reply-first triage, note turn; landed). Reads
from `2026-10-06-investigation-skeleton-design.md` (being written in parallel, not yet on disk):
the one field this spec consumes is the triage note's `reproduction` object, and the one reader
of it is `shadow.reproductionOf` (below), so a different shape there changes one function here.

## Why

Sirdar triages a ticket only when someone asks: the CLI, the composer, or a webhook delivery
calling `Service.TriageIfIdle` (`internal/app/hooks.go`). Nothing polls; the only loop in the
process is the 500 ms run-directory watcher (`internal/app/watcher.go`, `DefaultInterval`). There
is also no record of whether a triage was right. The RCA schema's `rca.triageReview.verdict`
(`confirmed`/`partial`/`wrong`) reaches the register as `TriageVerdict`
(`internal/run/execute.go`, `completeRCA`), but only for tickets the operator carried to an RCA,
and only as a judgement on the operator's own triage.

Before Sirdar may message, comment or open a PR on its own, it has to show on the operator's real
queue, unattended, that its verdicts match what the operator ended up doing. Shadow mode produces
that evidence and changes nothing anyone else can see.

## Goals

- Every new ticket assigned to the operator that matches the configured filter gets one triage
  run, started unattended, inside `sirdar serve` or `sirdar shadow watch`.
- A shadow run leaves nothing outside `.sirdar/`: no vault note, no register row, no notification,
  no tracker or helpdesk write, no question to the operator.
- Each shadow run is later graded `confirmed`, `partial` or `wrong` against an outcome: the
  operator's own later runs on the key, a manual entry, or the tracker via an optional adapter
  method.
- A scorecard (CLI and a UI screen) answers "is it right often enough, fast enough, cheaply
  enough to earn rung 2".
- Spend stays bounded across all unattended runs: a daily run count, a daily USD cap, a
  concurrency limit, and a pause whenever the provider is rate-limited or near its quota window.
- An operator's correction in chat can become a playbook addition in two clicks, and RCA
  `playbookSuggestions` land in the same queue.

## Non-goals

- Anything outward from a shadow run: Slack DMs (rung 2), internal comments or draft PRs
  (rung 3). The optional daily digest is the one outward message, off by default and addressed
  only to the operator.
- Write methods in the exec protocol. `tracker.outcome` is a read.
- Changing the private Janus adapter (`~/Documents/Work/Coding/sirdar-janus`). It may implement
  `tracker.outcome` later; until then, outcomes for OXO.APIs come from the operator's runs and
  manual entry.
- A built-in Jira `tracker.outcome`. The interface is defined here so a built-in can follow.
- Shadow RCA or shadow fix runs. Shadow triages only.
- Tuning playbooks automatically from wrong grades. Suggestions come from the operator's
  corrections and from RCA only.
- Running the watcher inside the desktop app. It shows shadow status, but polling belongs to the
  long-lived `serve` or `shadow watch` process.

## Design

### 1. A shadow run is a triage run with a `Shadow` flag

`store.State` gains `Shadow bool` (omitempty), set from a new `runner.Options.Shadow`. A shadow run
is an ordinary reply-first triage (`State.ReplyFirst`, `prepared.noteAfter`; `internal/run/prepare.go`,
`prepare`): reply turn, then note turn, with the same prompt, bundle fetch, policy, budget and
validation. Keeping the real flow is what makes the measurement mean something. `NoteOnly` stays
false, unlike eval.

Its run directory is the normal `.sirdar/runs/<key>/<run-id>`, so the Session screen can open it
and show the transcript. What changes is what it leaves behind and who it may ask. Every place
that already excludes `State.Eval` excludes `Shadow` too, through one helper:

```go
// Measurement reports whether the run is a measurement rather than a record of a ticket:
// an eval replay or a shadow run. Neither is filed, registered, steered, or read as
// "the newest triage note".
func (s State) Measurement() bool { return s.Eval || s.Shadow }
```

Every existing `.Eval` check below moves to `.Measurement()`:

| Site | Effect for a shadow run |
|---|---|
| `run.writeNote` (`execute.go`) | `note.md` stays in the run directory; `fileNote` is never called, so the vault is untouched |
| `run.appendRegister` (`execute.go`) | no register row |
| `run.existingTriageNote` (`execute.go`) | a later real triage never mistakes a shadow note for the vault's copy |
| `store.LatestNote` (`internal/store/run.go`) | `sirdar rca` and `sirdar fix` never read a shadow note |
| `fix.go` latest-fix lookup (`internal/fix/fix.go`, `s.Kind != store.KindFix \|\| s.Eval`) | unchanged in effect (no shadow fix runs), changed for consistency |
| `run.RefuseSteer` (`internal/run/steer.go`), `app.QueueLiveSteer` (`internal/app/livesteer.go`) | a shadow run is not steered; the refusal names it a shadow run |

Sites that do not check `Eval` today and must skip shadow runs:

- `app.busyReason` (`internal/app/hooks.go`): a running or recent shadow run neither blocks nor
  starts the cooldown for a webhook triage of the same key.
- `Service.Queue` (`internal/app/service.go`): `LatestRun` is the newest non-shadow run, so the
  Board's Queue lane does not show a ticket as triaged because a shadow ran.
- `Service.Runs`: shadow runs are left out unless `includeShadow` is set. The Shadow screen sets
  it. The watcher still publishes `run.updated` for them, and `RunSummary` gains `shadow: true` so
  the frontend store keeps them out of Sessions and the Board.

Notifications are off through the existing `Options.NoNotify` (`prepared.noNotify`, checked in
`internal/run/notify.go`, `notifyFinished`).

### 2. The unattended policy

A shadow run has nobody to answer it, so nothing in it may wait on a person.

- **Refused tool calls are denied and recorded.** `sessionSpec` (`execute.go`, line ~814) becomes
  `spec.Policy.Ask = cfg.AskOperator() && !p.state.Measurement()`. With `Ask` false,
  `PermissionPolicy.Decide` (`internal/provider/policy.go`) returns the plain deny, the provider
  tells the agent, and the session continues. In a shadow run, `handleEvent` also appends every
  `EvPermission` deny to `State.Denied []DeniedCall{At, Tool, Summary}`, where `Summary` is
  `toolDetail(ev.Input)` (capped at 20 entries). The session preamble already says "When a tool
  is refused, say what was wanted and continue". The shadow notice (below) adds that the wanted
  call goes under `openQuestions`. `State.Denied` keeps the record even if the agent forgets.
- **A question in words is answered once, automatically.** Codex is the only provider that emits
  `EvQuestion` (`internal/provider/codex/codex.go`), and today it ends the run
  `blocked: agent asked: …`. In a shadow run the question is appended to
  `State.Questions []string`. When the run ends blocked with the `askedPrefix` reason, the shadow
  job resumes it straight away through `Runner.Resume` with `ResumeOptions.Answer` set to the
  fixed text `shadowNoAnswer`: "Nobody is available to answer. Continue with your best judgement
  and list this question under openQuestions." `resumeText` (`internal/run/run.go`) already
  returns `Answer` for an `askedPrefix` run. A second question leaves the run blocked, and the
  ledger records `asked twice`. Claude's question tool arrives as a permission request, so the
  deny path above covers it.
- **No grants.** A shadow run starts with no `Grants` and can never receive any, because nobody
  can answer an ask.
- **Shadow notice.** `prompt.TriageInput` gains `Shadow bool`, which adds this section after the
  Operator's request position: "This run is unattended. Nobody will read your reply before the
  ticket is worked, and nobody can answer a question or approve a refused tool. Investigate as
  you would for the operator. When a tool is refused or you need an answer, record what you
  wanted and why under openQuestions, and continue." The note turn's prompt and
  `notePolicy` stay as they are.
- **Reproduction.** The investigation skeleton's staging reproduce step runs in a shadow run only
  when its tools are allowed by the workspace policy without asking, and `shadow.reproduce` is
  true (the default). An attempt the policy refuses is recorded as a denied call, and the note's
  `reproduction` says not attempted.

Breach and blind verdicts (`EvBreach`, `EvBlind`) fail a shadow run exactly as they fail any other
run. That is a finding worth a ledger line, and the run files nothing either way.

### 3. The shadow ledger: `.sirdar/shadow.jsonl`

The ledger is the watcher's source of truth. It is append-only JSON lines, one event per line,
`"v": 1` on each. Writers open with `O_APPEND`, write one line under 64 KiB per call, and never
rewrite the file. Readers skip a line that does not parse; only a torn last line after a crash can
fail to parse. `internal/shadow/ledger.go` owns `Append(root, Entry)`, `Read(root)` and
`Fold([]Entry) map[key]*Ticket`, the per-key view everything else reads.

```json
{"v":1,"at":"2026-10-06T09:00:00+04:00","ev":"armed","since":"2026-10-06T09:00:00+04:00","by":"serve"}
{"v":1,"at":"…","ev":"seen","key":"OMNI-3520","title":"…","type":"bug","status":"Open","createdAt":"…","baseline":false}
{"v":1,"at":"…","ev":"started","key":"OMNI-3520","runId":"20261006T051500Z-ab12","attempt":1,"model":"claude-sonnet-…","operatorFirst":false,"backfill":false}
{"v":1,"at":"…","ev":"finished","key":"OMNI-3520","runId":"…","status":"completed","reason":"","costUSD":0.84,"elapsedSec":512,"turns":41,
 "verdict":{"classification":"bug","confidence":"medium","hypothesis":"≤300 chars of rootCause.hypothesis","codeRefs":["src/…:120"]},
 "reproduction":"reproduced","noteFiled":true,"denied":[{"tool":"Bash","summary":"psql -c …"}],"questions":[]}
{"v":1,"at":"…","ev":"outcome","key":"OMNI-3520","source":"operator-rca","runId":"…","prUrls":["https://github.com/…/pull/912"],"resolution":"…","status":"Done","resolvedAt":"…"}
{"v":1,"at":"…","ev":"graded","key":"OMNI-3520","shadowRunId":"…","basis":"pr+resolution","final":true,"grade":"confirmed","reason":"≤200 chars",
 "prFilesHit":{"matched":2,"total":3,"score":0.67},"codeRefsPathOverlap":{"matched":1,"total":2,"score":0.5},"judgeModel":"…","costUSD":0.03,"leadMinutes":47}
{"v":1,"at":"…","ev":"skipped","key":"OMNI-3401","reason":"stale: seen 72h ago and never started"}
{"v":1,"at":"…","ev":"paused","until":"2026-10-06T14:00:00+04:00","reason":"rate limited"}
{"v":1,"at":"…","ev":"resumed","by":"operator"}
```

Field rules:

- `verdict` is read from the shadow run's `result.json` by `shadow.verdictOf`. When the note turn
  failed (`State.NoteWarning`), `noteFiled` is false, `verdict` is absent, and grading reads
  `answer.md`.
- `reproduction` is `reproduced`, `not_reproduced`, `not_attempted` or `unknown`.
  `shadow.reproductionOf(result.json)` copies `reproduction.outcome` from the investigation
  skeleton's note object (same three values), and an absent object becomes `unknown`.
- `operatorFirst` is true when a non-measurement run on the key started before this shadow run.
  Such a ticket is graded but left out of the headline numbers, because the bundle may already
  hold the operator's findings in tracker comments.
- `leadMinutes` is the operator's first non-measurement run `StartedAt` minus the shadow run's
  `finished.at`. A positive value means the shadow verdict was ready before the operator started,
  which is the number rung 2 depends on.
- A later `graded` line for the same `shadowRunId` supersedes an earlier one. `final: false`
  marks a provisional grade (basis `operator-triage`).

Ticket titles and hypotheses are in the ledger. It stays local, under `.sirdar/`, which the
dogfood workspace git-excludes. `sirdar doctor` warns when `git check-ignore .sirdar/shadow.jsonl`
says it is not ignored.

### 4. The watcher

`internal/app/shadow.go` runs one `shadowWatcher` goroutine per registered workspace whose config
has `shadow.enabled: true`. `Service.Start` starts them when `Options.Shadow` is set.
`cmd/sirdar/cmd_serve.go` sets it, and so does `sirdar shadow watch`, which builds the same
`app.Service` without the HTTP server. The desktop shell leaves it unset. Config is reloaded on
every tick (`Service.load`), so enabling, disabling or retuning takes effect at the next tick
without a restart.

**Single poller.** A watcher takes `.sirdar/shadow.lock` (`O_CREATE|O_EXCL`, contents
`{pid, host, by, startedAt}`, the pattern `internal/app/intakelog.go` uses) and touches it on
every tick. A lock is stale when its pid is not alive on this host or its mtime is older than
3 × interval, and then it is taken over. A second `serve` or `shadow watch` against the same
workspace reports `shadow: watched by pid N (serve)` and does not poll. Manual commands
(`outcome`, `run`, `pause`) do not need the lock; they only append to the ledger.

**One tick** (every `shadow.interval`, first tick 30 s after start):

1. **Recover** (first tick only). For every `started` with no `finished`, read the run's
   `state.json`. A terminal status gets its `finished` line written. A run still `preparing` or
   `running` was orphaned by a crash: its state is written `blocked`, reason `interrupted`, and
   its `finished` line says `interrupted`. It becomes eligible for one retry (step 5).
2. **Gate.** Stop here and record why in the status when any of these hold: the ledger's latest
   `paused` is in the future; the provider's `Service.Quota()` reading has `sevenDay` or
   `fiveHour` utilization at or above `shadow.pauseAtQuota`, or a `ResetsAt` still in the future
   from a `rate_limited` reading; `shadow.yieldToOperator` is on and a non-measurement run in this
   workspace is `preparing` or `running`; today's count or USD cap is reached.
3. **List.** `deps.Tracker.List(ctx, source.ListFilter{Assignee: queueAssignee(cfg,
   shadow.assignee), Order: source.OrderNewest, Limit: 200})`, reusing the same `queueAssignee`
   and `SelfOf` resolution as `Service.Queue`. `Status` is left empty, so the adapter applies its
   own open default (Jira: `statusCategory != Done`, `internal/source/jira/search.go`). Then
   filter on the client side: `config.MatchQueueType(shadow.types, t.Type)`, status in
   `shadow.statuses` when set (case-folded), and `CreatedAt >= since`.
4. **Record.** Each listed key with no ledger entry gets a `seen` line. Tickets the adapter gives
   no `CreatedAt` for are marked `baseline: true` on the first tick after `armed` and never
   started: they were already in the queue when shadow was switched on.
5. **Start.** The candidates are seen keys with no `started` line, oldest `createdAt` first, then
   retry-eligible keys. A key is retry-eligible when its only attempt finished `failed` with
   anything but a `not_found` source error, `interrupted`, or `blocked` on a rate or model limit,
   and that was at least one interval ago. A retry resumes the run when it has a `Handle`
   (`Service.Resume`) and starts fresh otherwise. There are at most 2 attempts. A seen key not
   started within 72 h (`shadowStaleAfter`) is `skipped` as stale, because by then the operator
   has worked it. Start candidates while in-flight < `shadow.concurrency` and the daily caps
   allow.
6. **Outcomes** (every `shadow.outcomes.interval`, and immediately on the triggers in section 6):
   collect and grade.

**Starting a shadow run** goes through `Service.startJob` with a work function that clones the
workspace config and lowers `Budget.MaxUSD` to `min(budget.maxUsd, maxUSDPerDay − spentToday)`
before building the `Runner`. `sessionSpec` and `handleEvent` read `r.Config.Budget`, so the
daily cap is enforced as the per-run cap with no runner change. Then
`runner.Options{Shadow: true, NoNotify: true, Model: shadow.model}`. The `started` line is
appended before the job starts, so a crash between the two leaves a recoverable record rather
than a ticket that was silently triaged twice. When the job ends, the watcher writes `finished`
from the run's state. A blocked `askedPrefix` run is auto-answered first (section 2).

**Status.** The watcher keeps `ShadowStatus` in memory and serves it:
`{enabled, state: watching|paused|gated|locked-elsewhere|stopped|disabled, reason, pausedUntil,
lastPoll, nextPoll, lastError, consecutiveErrors, today:{runs, maxPerDay, usd, maxUSDPerDay},
inFlight:[runId]}`. It publishes `shadow.status` on the Service event stream when the status
changes.

### 5. Budget and concurrency

- **The autonomy gate.** `internal/app/autonomy.go` defines `AutonomyGate`, the single place that
  decides whether an unattended action may start. In v1 its only action kind is `shadow-triage`.
  It holds a semaphore sized `shadow.concurrency`, shared by every job of that kind in the process
  (shadow runs, their auto-answer resumes, retries), plus the daily accounting below. Judge calls
  take no slot, because they are single-turn and capped separately.
- **Day.** The machine's local calendar day.
- **Count.** `started` lines with `attempt: 1` today. Retries and auto-answers do not count again.
- **USD.** Today's `finished.costUSD`, plus the current `Usage.CostUSD` of in-flight shadow runs
  (from `state.json`), plus today's `graded.costUSD`. On `billing: subscription` this is the
  provider-reported notional cost, used as a proxy for quota burn. The quota gate in step 2 is
  the real protection there.
- **Overshoot.** With `concurrency: 1` the USD cap is hard: the one run in flight was capped at
  what remained. With concurrency N, the worst case is N − 1 runs each at the remaining amount.
  Validation rejects `concurrency > 1` unless `maxUSDPerDay >= concurrency × budget.maxUsd`.
- **Operator first.** `yieldToOperator` (default true) defers new shadow starts while the operator
  has a run going. A shadow run already in flight continues.
- **Quota.** `pauseAtQuota` defaults to 0.85. At the 88% seven-day utilization the owner reported,
  shadow does not start until that window resets. This is intended, and the status says so:
  `paused: seven-day quota at 88% (pause at 85%), resets <time>`.

### 6. Outcomes

`internal/shadow/outcome.go` merges three sources into `outcome` lines. Each distinct observation
gets one line; a source that reports the same thing again is not re-appended.

**(a) The operator's own later runs.** For each ledger key with a finished shadow run,
`store.List(root, key)` filtered to non-measurement runs:

- An RCA run, completed, gives `source: operator-rca` with its PR URL and resolution text. These
  are not stored today: `RCAOptions.PRURL` and `Resolution` only reach the prompt and
  `bundle/resolution.md` (`prepare.go`, `rcaInput`). This spec adds
  `State.RCAInputs *struct{PRURL, Resolution string}` (resolution capped at 4,000 characters),
  written by `prepare` for `KindRCA`, and `RegisterRow.PRURL` (omitempty) on the RCA row in
  `completeRCA`. The RCA's `result.json` `rca.summary` and `rca.classification` are also read as
  truth text.
- A fix run with `State.Fix.PRURL` set gives `source: operator-fix`.
- A completed triage run's `result.json` (`rootCause.hypothesis`, `classification`, `codeRefs`)
  gives `source: operator-triage`. It is the weakest truth, because the operator's triage can be
  wrong too.

**(b) Manual entry.** `sirdar shadow outcome KEY --pr URL [--pr URL] --resolution TEXT|@file`, the
UI form, and `POST …/shadow/outcomes` append `source: manual`. PR URLs are validated with the
existing `checkPRURL` (`internal/eval/retro.go`, moved to an exported `eval.CheckPRURL`). A manual
entry triggers grading of that key at once.

**(c) `tracker.outcome`.** This is an optional adapter method, specified for `docs/adapters.md` as
follows.

> `describe` may return `"methods": ["tracker.outcome"]`. Sirdar sends `tracker.outcome` only to
> an adapter that lists it, so an older adapter is never sent a method it did not advertise.
>
> | method | params | result |
> |---|---|---|
> | `tracker.outcome` | `{"key"}` | a `ticket.Outcome` |
>
> `ticket.Outcome` fields, Go names verbatim as for every protocol type: `Key` (required),
> `Status` (the tracker's status name now), `Resolution` (the resolution or closing comment as
> text, at most 8,000 characters; the adapter cuts longer text), `ResolvedAt` (RFC 3339, absent
> while open), `PullRequests` (URLs of pull requests linked to the record, merged or not, newest
> first), `Fields` (string map of extras). An adapter answers `not_found` for an unknown key and
> may leave out anything it cannot read. It must not return a pull request it cannot tie to this
> key.

Go side: `plugin.Describe` gains `Methods []string`. A new optional interface
`source.Outcomer { Outcome(ctx, key) (ticket.Outcome, error) }` is implemented by `plugin.Client`
behind a `hasMethod` check that mirrors `hasRole`. The watcher calls it only when
`shadow.outcomes.tracker` is true and the tracker implements `Outcomer`. It gives
`source: tracker`. A `Status` with no `Resolution` and no `PullRequests` is recorded but is not
enough to grade.

**When collection runs:** on the outcome tick; when the Service sees `run.updated` turn a
non-measurement RCA or fix run on a ledger key `completed` (`Service.observe`); and on manual
entry. Keys whose shadow run finished more than `shadow.outcomes.windowDays` ago and never got a
final grade are marked `graded` with `grade: ungradable` and dropped from collection.

### 7. Grading

`internal/shadow/grade.go`, `Grade(ctx, judge, shadowRun, outcomes) (Graded, error)`.

**Basis and finality.** The strongest available truth decides `basis`: any of a PR URL,
resolution text, operator RCA or manual entry makes the grade `final: true`. Basis names are
joined, for example `pr+resolution` or `operator-rca`. Operator triage alone gives
`basis: operator-triage, final: false`, which is shown and kept out of the headline. A stronger
outcome arriving later regrades the key.

**File overlap**, when the outcome has PR URLs: changed files come from the existing `gh`
reader (`internal/eval/retro.go`, `readPullRequests` and `changedFiles`, exported as
`eval.PullRequestFiles(ctx, urls)`), then
`eval.ScoreTriage(resultJSON, noteText, prFiles)` (`internal/eval/retroscore.go`) gives
`prFilesHit` and `codeRefsPathOverlap`. With no `result.json`, `doc` is empty and `text` is
`answer.md`. A missing `gh` or a failed read is a warning on the `graded` line, and the judge
still runs on the text.

**The judge**, whenever the outcome has any text or PR. The single-turn, no-tool provider call in
`eval.providerRubric.Judge` (`internal/eval/rubric.go`) is factored into
`eval.JudgeOnce(ctx, deps, model, prompt string, schema []byte) (json.RawMessage, float64, error)`,
with the same spec (no MCP, `MaxTurns: 2`, `MaxMinutes: 5`, `CloseInput`, grace cancel).
`providerRubric` and the shadow judge both call it. The shadow judge has a fixed prompt and
schema:

```json
{"type":"object","additionalProperties":false,"required":["verdict","sameRootCause","reason"],
 "properties":{"verdict":{"type":"string","enum":["confirmed","partial","wrong"]},
  "sameRootCause":{"type":"boolean"},
  "reason":{"type":"string","description":"At most 200 characters, naming what decided it."}}}
```

Prompt inputs: the shadow verdict (classification, confidence, hypothesis, codeRefs,
openQuestions) or `answer.md`; the truth (resolution text, operator RCA summary and
classification, operator triage hypothesis labelled as such, PR titles and bodies, changed files,
and the PR diff cut to 20,000 characters). The verdict definitions match the RCA's
`triageReview` enum: `confirmed` when the shadow's root cause is the one the fix addressed,
`partial` when it found the right area or a contributing cause but not the cause, `wrong`
otherwise. The judge model is `shadow.judge.model`, or the workspace model when that is empty.
An unknown verdict becomes an error rather than a guessed grade (as `RubricFromJSON` does), and
the key retries at the next outcome tick.

A shadow run that ended without a reply or note gets `grade: no-answer` with no judge call.

**Cost and time.** `graded.costUSD` is the judge cost. The run's own cost and time stay on
`finished`. Both count toward the daily USD cap.

### 8. Scorecard

`internal/shadow/report.go`, `Report(entries, since) ShadowReport`, read by the CLI and the API.

Headline, over runs whose `started.at >= since` (default 30 days), excluding `backfill`,
`operatorFirst` and `baseline` rows:

- **Graded**: count of final grades, beside "of N started".
- **Confirmed**: confirmed ÷ final grades, with confirmed-or-partial on the line under it.
- **Median time**: median `finished.elapsedSec` of completed runs. "Ready before you started: k of
  m" (positive `leadMinutes`) sits under it.
- **Cost**: total run plus judge USD in the window, with the median per ticket under it.

Rows are newest first, one per key: key (the key is the link, opening the shadow run in the
Session screen), title, what shadow said (classification and the hypothesis's first sentence),
grade (badge, with the reason on hover), cost. Outcome source, PR links, reproduction, denied
calls, questions and file overlap are in the row's drawer. Tags: `provisional`, `operator first`,
`backfill`, `no answer`, `ungradable`. Report rows also group by `classification`, which is what
rung 2 can be switched on by.

`sirdar shadow report [--since 7d|2026-10-01] [--json]` prints the headline block and a table of
the same columns.

**Daily digest** (`shadow.digest`, off by default). When enabled, once a day at `digest.at` local
time, the watcher posts to `digest.webhookUrl` (a credential ref to a Slack incoming webhook on
the operator's own DM) a message with counts only: started, graded, confirmed %, cost, and
`KEY grade` pairs. It contains no hypotheses, titles or ticket text, the same metadata-only rule
`internal/run/notify.go` follows. It goes through the same `notify` HTTP client and timestamped
HMAC path as run notifications. The ref is added to `credentialEnvNames` (`internal/run/run.go`)
so no agent session can read it.

### 9. Feedback loop: corrections into playbooks

**Pending queue.** `.sirdar/playbook-suggestions.jsonl` is append-only, with the same rules as the
ledger. `internal/app/suggestions.go`:

```json
{"v":1,"at":"…","ev":"proposed","id":"s-3f9a1c","source":"remember","runId":"…","key":"OMNI-3413","playbook":"20-orders.md","addition":"…","reason":"…","costUSD":0.02}
{"v":1,"at":"…","ev":"accepted","id":"s-3f9a1c","playbook":"20-orders.md","edited":true}
{"v":1,"at":"…","ev":"dismissed","id":"s-3f9a1c"}
```

`id` is the first 6 bytes of sha256(playbook + addition) in hex. A proposal whose id is already
pending is not appended again.

**From RCA.** `completeRCA` keeps writing `playbook-suggestions.md` (`writePlaybookSuggestions`)
and also appends one `proposed` line per `rca.playbookSuggestions` entry with `source: rca`,
except for measurement runs.

**"Remember this".** On the Session screen, every operator message (the `you` items built from
`steer` and `answer` events in `components/session/model.ts`, and the run's `Instruction`) gets a
hover action, "Remember this". It calls `POST …/runs/{runId}/remember {index}` →
`Service.Remember(ctx, wsID, runID, index)`, which:

1. Reads the event at `index` and the last assistant reply before it (capped at 4,000
   characters).
2. Lists the playbooks (`Service.Playbooks`: name, title, lede).
3. Makes one `eval.JudgeOnce` call with the workspace model and a 90 s timeout, asking for
   `{playbook, addition, reason}`: an existing playbook name or a new `NN-slug.md`, and an
   addition of at most 600 characters written as a standing rule, not as the story of this
   ticket.
4. Validates `playbook` against the listed names or `ValidPlaybookName`
   (`internal/app/playbooks.go`).
5. Falls back to a fixed template on any failure: `playbook: 90-corrections.md`,
   `addition: "- <date> <key>: <operator message>"`.
6. Appends `proposed` and returns the suggestion.

Remember calls are operator-initiated and do not count against shadow caps.

**Accept and Dismiss.** `POST …/suggestions/{id}/accept {playbook?, addition?}` lets the operator
retarget or edit. An existing playbook is changed through `Service.SavePlaybook` with its current
body, a blank line and the addition. A new one is created through `Service.AddPlaybook` with
`# <Title from slug>` followed by the addition. Both use the existing atomic write
(`writePlaybook`). If a playbook was edited between proposal and accept, the addition is still
appended to whatever is on disk at accept time: nothing is overwritten. Dismiss appends
`dismissed`.

### 10. Configuration

```yaml
shadow:
  enabled: false
  interval: 15m
  assignee: me
  types: [bug]
  statuses: []
  since: armed
  maxPerDay: 10
  maxUSDPerDay: 10
  concurrency: 1
  model: ""
  pauseAtQuota: 0.85
  yieldToOperator: true
  reproduce: true
  outcomes:
    interval: 6h
    windowDays: 30
    tracker: true
  judge:
    model: ""
  digest:
    enabled: false
    at: "09:00"
    webhookUrl: ""
```

| Key | Default | Validation |
|---|---|---|
| `enabled` | false | requires `sources.tracker` |
| `interval` | 15m | Go duration, 5m to 24h |
| `assignee` | `me` | non-empty; `me` resolved through `queueAssignee`/`SelfOf`; `sirdar doctor` fails the shadow row when `me` resolves to nothing for an adapter that cannot resolve it itself |
| `types` | `sources.tracker.queue.types`, else `[bug]` | folded through `config.NormalizeQueueTypes`; empty list rejected |
| `statuses` | `[]` (adapter's open default) | each non-empty |
| `since` | `armed` | `armed`, or an RFC 3339 date/time; `armed` writes the `armed` line on first enable and uses its time |
| `maxPerDay` | 10 | 1 to 100 |
| `maxUSDPerDay` | 10 | > 0 and ≤ 500; ≥ `concurrency × budget.maxUsd` when `concurrency > 1` |
| `concurrency` | 1 | 1 to 4 |
| `model` | `""` (workspace model) | free string, trimmed; sent as `--model` is |
| `pauseAtQuota` | 0.85 | 0 (off), or 0 < x ≤ 1 |
| `yieldToOperator` | true | |
| `reproduce` | true | |
| `outcomes.interval` | 6h | 15m to 7d |
| `outcomes.windowDays` | 30 | 1 to 180 |
| `outcomes.tracker` | true | |
| `judge.model` | `""` (workspace model) | as `model` |
| `digest.enabled` | false | |
| `digest.at` | `09:00` | `HH:MM` |
| `digest.webhookUrl` | `""` | credential ref; required when `digest.enabled`; host must be `hooks.slack.com` |

The block decodes with `KnownFields(true)` like the rest of `config.yaml`, so a misspelled key is
an error. `docs/config.md` gains a "Shadow mode" section.

### 11. API and CLI

Routes are registered in `internal/httpapi/server.go`. Every mutating one sits behind the existing
same-origin and loopback guard. Each has a Wails bridge method (`desktop/bridge.go`) and a
`Transport` entry, kept honest by `api/parity.test.ts`.

| Route | Service | Purpose |
|---|---|---|
| `GET /api/workspaces/{id}/shadow/status` | `ShadowStatus` | watcher state, caps used today, in-flight runs |
| `GET /api/workspaces/{id}/shadow/report?since=` | `ShadowReport` | headline and rows |
| `POST /api/workspaces/{id}/shadow/poll` | `ShadowPoll` | run one tick now (403 when another process holds the lock) |
| `POST /api/workspaces/{id}/shadow/pause` `{for?}` | `ShadowPause` | append `paused` (default 24 h) |
| `POST /api/workspaces/{id}/shadow/resume` | `ShadowResume` | append `resumed` |
| `POST /api/workspaces/{id}/shadow/outcomes` `{key, prUrls, resolution}` | `ShadowOutcome` | manual outcome, grades at once |
| `POST /api/workspaces/{id}/shadow/regrade` `{key}` | `ShadowRegrade` | collect and grade one key |
| `POST /api/workspaces/{id}/runs/{runId}/remember` `{index}` | `Remember` | propose a playbook addition |
| `GET /api/workspaces/{id}/suggestions` | `Suggestions` | pending suggestions |
| `POST /api/workspaces/{id}/suggestions/{sid}/accept` `{playbook?, addition?}` | `AcceptSuggestion` | append to a playbook |
| `POST /api/workspaces/{id}/suggestions/{sid}/dismiss` | `DismissSuggestion` | dismiss |

The CLI lives in `cmd/sirdar/cmd_shadow.go` and `cmd_suggestions.go`, registered in `commands`
like `serve`:

- `sirdar shadow watch [--once]`: headless watcher; `--once` runs one tick and exits.
- `sirdar shadow run KEY... [--backfill]`: shadow specific keys now, through the gate.
  `--backfill` sets `Options.AsOf` to the ticket's `CreatedAt` (later thread messages dropped,
  PR references redacted; `prepare`'s existing AsOf path) and marks the ledger entry `backfill`.
  The repository is still at HEAD, which is why backfill rows stay out of the headline.
- `sirdar shadow status`
- `sirdar shadow report [--since] [--json]`
- `sirdar shadow outcome KEY [--pr URL]... [--resolution TEXT|@file]`
- `sirdar shadow grade [KEY...]`: collect outcomes and grade now.
- `sirdar shadow pause [--for 4h]` and `sirdar shadow resume`
- `sirdar suggestions [list]`, `sirdar suggestions accept ID [--playbook NAME]`,
  `sirdar suggestions dismiss ID`

### 12. UI

- **Sidebar.** "Shadow" goes between Register and Eval (`components/shell/Sidebar.tsx`, `ROWS`),
  shown when `shadow.enabled` or when the ledger exists.
- **Shadow screen** (`screens/Shadow.tsx`). Page head: "Shadow", the status line
  ("Watching · next poll 10:45 · 3 of 10 today · $2.10 of $10"), and Pause/Resume as the primary
  action. Four `StatCard`s for the headline (Graded, Confirmed, Median time, Cost). A `DataTable`
  with the row columns from section 8. A row's `Drawer` holds the outcome, file overlap, denied
  calls, questions, and the manual outcome form (PR URLs, resolution text, Save). The text cells
  use the existing bidi-safe rendering for Arabic titles.
- **Session screen.** A shadow run opens read-only. A banner reads "Shadow run. Nothing was filed,
  sent or registered", and the composer is disabled with "Shadow runs are measurements. Start a
  session to continue this work." Operator messages on every non-measurement run get the
  "Remember this" hover action, and the toast names the target playbook with a link to Settings ›
  Playbooks.
- **Settings › Playbooks** (`screens/settings/PlaybooksPage.tsx`). A "Pending suggestions" card
  sits above the list. Each row shows the target playbook, a three-line preview of the addition
  (expand to read all), and the source ("Remembered on OMNI-3413", "RCA of OMNI-3217"). Accept
  opens an inline editor with the target dropdown and the editable addition; Dismiss is the other
  action.

### 13. Failure handling

| Failure | Behaviour |
|---|---|
| Tracker `List` fails | No ledger change. `lastError` and `consecutiveErrors` are set, and the next tick waits interval × 2^errors (capped at 4 × interval). A `source.Error{Code: auth}` gates the watcher (`state: gated`, "tracker credentials rejected") until the config file changes or the process restarts. `Unsupported` disables the watcher with that reason. |
| Ticket fetch fails in `prepare` | The run fails in `preparing` and gets a `finished` line marked failed. It is retried once after an interval unless the source said `not_found`. |
| Provider rate limit (`EvRateLimited`) | The run ends blocked (`execute`). The watcher appends `paused` until the reset time, or now + 30 min when none is given. After the pause the run is resumed once. |
| Per-model limit (`EvModelLimit`) | `providers.claude.fallbackModels` is walked as today. If the run still ends blocked, the watcher pauses 1 h and resumes it once with the same model. |
| Quota near its window | The gate in step 2. No run starts, and in-flight runs continue. |
| Budget exceeded | The run ends `over_budget`, the `finished` line records it, and there is no retry. The ledger cost counts what was spent. |
| Crash or kill mid-run | Recovered on the next start (step 1). The `interrupted` run is resumed through its handle once. |
| `Service.Stop` during a run | As today: jobs are cancelled, and the run ends blocked `interrupted`. Treated as a crash at the next start. |
| Ledger write fails | The tick aborts before starting anything (a run without a `started` line would be invisible to the caps), `lastError` is set, and the next tick retries. |
| Lock held by a live process | `state: locked-elsewhere`, with the holder named. No polling. |
| Judge fails | No `graded` line. Retried on the next outcome tick, at most 3 times per key per day, and `lastError` is shown. |
| `gh` missing or the PR is unreadable | The file overlap is left out, the judge still grades from the text, and the warning is on the `graded` line. |
| Breach or blind | The run fails and files nothing, as for any run. The `finished` line carries the reason, and `sirdar shadow status` counts breaches separately. |

### 14. Security and privacy

Nothing leaves the machine by default. Shadow runs set `NoNotify`, never call `fileNote` or
`AppendRegister`, and every tracker and helpdesk adapter is read-only (`tracker.outcome` is a
read). Agent sessions keep the unchanged read-only policy, and with no asks they can only end up
with less access, never more. The judge and Remember calls go to the workspace's own configured
provider, which already sees every bundle. PR reads go through the operator's `gh`, read-only, as
`sirdar rca --pr` does today. The digest is the one outward message: off by default,
metadata-only, and only to a `hooks.slack.com` webhook the operator configures, with its
credential stripped from agent environments. The ledger and the suggestions file stay local, and
`doctor` warns when they are not git-ignored. Webhooks and `serve --allow-remote` are unchanged:
shadow routes sit behind the same guard as every other mutating route.

## Data changes

- `store.State`: `Shadow bool`, `Denied []DeniedCall`, `Questions []string`,
  `RCAInputs *RCAInputs` (all omitempty); method `Measurement()`.
- `store.RegisterRow`: `PRURL string` (omitempty), set on RCA rows.
- `runner.Options`: `Shadow bool`.
- `prompt.TriageInput`: `Shadow bool`.
- `ticket.Outcome`; `source.Outcomer`; `plugin.Describe.Methods`.
- `app.RunSummary`: `shadow bool`. `app.Options`: `Shadow bool`.
- `eval.JudgeOnce`, `eval.PullRequestFiles`, `eval.CheckPRURL` (exported from existing code).
- New packages and files: `internal/shadow` (ledger, select, outcome, grade, report),
  `internal/app/shadow.go`, `internal/app/autonomy.go`, `internal/app/suggestions.go`,
  `cmd/sirdar/cmd_shadow.go`, `cmd/sirdar/cmd_suggestions.go`, `screens/Shadow.tsx`.

## Testing

Unit and integration tests, all offline, using the stub provider and fake tracker the run and app
tests already use:

- `store`: `Measurement()`. `LatestNote` skips shadow runs.
- `run`: a shadow triage with the stub provider replaying reply and note gives `note.md` and
  `result.json` in the run directory, nothing in `Notes.Dir`, no `register.jsonl` line, no
  notifier call, and `ReplyFirst` true. A denied call gives a deny event, `State.Denied` filled,
  and the run continuing. A codex-style `EvQuestion` blocks, the auto-answer resumes once, and a
  second question stays blocked. `Policy.Ask` is false for shadow even with `permissions.ask`
  true. An RCA run records `RCAInputs` and the register `PRURL`.
- `app`: `busyReason` ignores shadow states. `Queue.LatestRun` skips shadow. `Runs` hides shadow
  without `includeShadow`. Steer and live steer refuse shadow runs.
- `shadow/ledger`: append and fold, a torn last line, superseding grades.
- Watcher selection is table-driven over (ledger, listed tickets, now, config) → starts and
  skips: type and status filters, `since`, baseline, stale, retry eligibility, attempt cap, count
  cap, USD cap, yield-to-operator, quota gate, paused-until.
- Watcher lifecycle with a fake clock: lock take-over when the pid is dead, second watcher
  locked out, crash recovery writing `interrupted`, adapter error backoff, auth gate, config
  reload enabling and disabling.
- Gate: concurrency semaphore across jobs; the cloned budget shows up as the session's
  `Budget.MaxUSD`.
- Outcomes: the operator RCA, fix and triage sources from fixture run directories; manual entry;
  `plugin.Client.Outcome` against `examples/adapters/file` extended with `methods` and an outcome
  fixture; not called when `methods` lacks it.
- Grading: basis precedence and finality; `ScoreTriage` wiring with fixture PR files; a stub
  judge's verdict parsing, unknown verdict, and the `no-answer` path.
- Report: the headline arithmetic (exclusions, median, confirmed-or-partial, lead).
- Suggestions: RCA appends, dedupe by id, Remember with the stub judge and with the
  fallback template, Accept appending through `SavePlaybook` and `AddPlaybook`, an invalid name
  refused.
- Config: defaults and every validation row.
- Frontend: the Shadow screen renders headline and rows from a fake transport, the drawer's
  manual outcome posts, the sidebar entry is gated, the read-only shadow Session banner and
  disabled composer, the Remember action, the pending suggestions card's accept and dismiss, and
  the parity test.

**Live pass in OXO.APIs** (dogfood workspace `OXO.APIs/.sirdar/`, Janus exec adapter, before
landing):

1. Run `sirdar doctor` and check the shadow row: the tracker lists, `me` resolves, the ledger is
   ignored, and `tracker.outcome` is reported unsupported for Janus.
2. Backfill smoke: `sirdar shadow run --backfill` on 3 tickets resolved in the last 2 weeks with
   a known PR. Record each with `sirdar shadow outcome KEY --pr … --resolution …`, run
   `sirdar shadow grade`, and read `sirdar shadow report`. Check that `Support Duty/Sirdar/` and
   `register.jsonl` are byte-identical before and after, and that no Slack message was sent.
3. Forward pass: `shadow.enabled: true`, `maxPerDay: 3`, `maxUSDPerDay: 5`, `since: armed`,
   under `sirdar serve` for 5 working days. The operator works tickets as normal, with RCAs
   carrying `--pr` and `--resolution`. Daily: `sirdar shadow status` and the Shadow screen.
   Check that the quota gate held while seven-day utilization was ≥ 85%.
4. Kill `serve` mid-run once and confirm recovery and the single resume.
5. Write up the findings as `docs/research/NN-shadow-dogfood.md`, using the same format as the
   earlier dogfood reports.

## Measures of success

Rung 1 is done when the live pass runs 5 working days with:

- zero vault writes, register rows or outward messages from shadow runs (checked by the diff in
  step 2 and by `notify` call counts in the event logs);
- at least 90% of eligible tickets started within one interval of first being seen, quota gates
  aside;
- daily caps never exceeded by more than one run's overshoot;
- no lost or duplicated runs across the induced crash.

Rung 2 (Suggest) is earned, and is the owner's decision, when over at least 20 final grades:
confirmed ≥ 60%, confirmed-or-partial ≥ 85%, wrong ≤ 10%, median cost ≤ $1.50, median time ≤ 15 min,
and verdicts ready before the operator started on at least half. Rung 2 can be enabled per
classification where those hold.

## Seams for rungs 2 and 3 (named, not built)

- **Delivery sink.** The watcher's `finished` handling calls
  `shadowSink.Finished(ctx, ShadowResult)`. In v1 the only sink is the ledger. Rung 2 adds a Slack
  DM sink (one message per ticket: key, link, classification, hypothesis line, grade history for
  that classification) behind the same `AutonomyGate`, with its own action kind and caps.
- **Adopt.** `Runner.Adopt(runID)` files a shadow run's note into the vault and register through
  the existing `fileNote` and `appendRegister`, clearing `Shadow`. This is the Session screen's
  "Adopt as triage" button in rung 2, so a good shadow verdict saves the operator a run.
- **Act.** Rung 3 starts from `reproduction == reproduced` plus per-classification grade history
  in the ledger. It runs the existing fix flow in a linked worktree with `--local`
  (`State.Fix.Local`) to produce a diff, then a draft PR through `internal/fix`. Internal
  comments need a write method in the exec protocol (`tracker.comment`), deliberately absent
  today. `Transitioner` stays read-only.
- **Gate.** `AutonomyGate` is the single authority for unattended actions. Each rung adds action
  kinds and limits there, not in the callers. When a second kind arrives, its limits move from
  `shadow:` to an `autonomy:` block.
- **Deep grade.** An opt-in `sirdar shadow grade KEY --rca` could run an eval-flagged RCA with
  `RCAOptions.TriageNote` set to the shadow note and read `triageReview.verdict`. It is rejected
  for v1 because it costs a full RCA per ticket where the judge costs one turn.
