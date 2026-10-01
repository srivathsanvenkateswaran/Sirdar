# Steer

`sirdar steer RUN_ID "instruction"` continues a run that has already finished. The same run
goes back to `running`, its transcript grows in place, its usage keeps counting against the
same caps, and its note is rendered again when the answer changes.

```
sirdar steer 20260915T091200Z-3f2a "Re-check the partial-return path"
sirdar steer 20260915T091200Z-3f2a "Now write the RCA from this"
sirdar steer 20260915T104500Z-9c01 "Keep the change to the Return branch; leave the pager alone"
sirdar steer 20260915T091200Z-3f2a "Read it again, carefully" --model claude-opus-5
```

`sirdar resume` is for a run that stopped and is waiting — a question, a rate limit, an
interrupt. `sirdar steer` is for a run that is done and that you want more from. A blocked run
can be steered too; the instruction is then what the agent gets instead of an answer. A run that
is still working takes the instruction into a queue instead of refusing it (below). A run
blocked on a permission question is answered with a decision — `sirdar resume RUN --allow`,
`--allow-run` or `--deny` — rather than words; see [Blocked runs](blocked.md).

## While the run works

A steer on a `preparing` or `running` run is queued, not refused:

```
$ sirdar steer 20260915T091200Z-3f2a "Also check the export worker"
queued; delivered at turn 4
```

The instruction is appended to the run's `steers.jsonl`, an inbox any process may append to —
the desktop app hosting a run the CLI steers, or the other way round. The executor that owns the
run reads it every half second and at every turn boundary, writes a `steer_queued` line to
`events.jsonl` for each instruction it finds, and records it in `state.json`'s `QueuedSteers`.

The turn boundary is the moment a provider turn ends with an answer. The answer is filed as
usual; then, if instructions are queued, they go into the same session as its next user message,
joined in the order typed and framed the way a steer on a finished run is ("Follow-up
instruction from the operator…", then the whole document again). The run stays `running`, the
`steer` line in `events.jsonl` carries `"continuation": "live"` and the turn it was read after,
and the next answer is validated and filed the same way — or left alone if it is the same
document. `sirdar steer` prints `queued; delivered at turn N`.

Whether a session takes another message is decided by the session itself, through the same
`Send` the schema retry uses, so nothing here guesses:

| Provider | Mid-run | Why |
|---|---|---|
| `claude` | delivered | `claude -p --input-format stream-json` reads further user messages on stdin; after its result line the CLI waits for the next one (`claude --help`: "realtime streaming input") |
| `acp` | delivered | a second `session/prompt` on the same session |
| `openai` | delivered | the loop takes a follow-up message on its own channel |
| `codex` | held, in practice | `turn/start` on the active thread works only while the event stream is open, and the app-server stream closes when the turn completes — the boundary usually arrives after it has |
| `qwen`, `cursor` | held | one message per session: `Send` refuses |
| `agy` | held | disabled |

A held instruction waits for the run to settle and is then applied as an ordinary steer — the
`completed → running` segment above, with `continuation` of `resume` or `primed` — by whatever
hosted the run: the desktop app or `sirdar serve` in the same job, the CLI command before it
prints its digest. `sirdar steer` prints `queued; applied when the run settles`. A queued steer
that names a different `--model` is always held, since a model is a session. A run stopped
with Stop or Ctrl-C drops its held steers (`dropped`, reason "the run was stopped"); a run
blocked on a question or a limit keeps them for the session that resumes it; a run that cannot
be steered at all — over budget, a pushed fix — drops them with that reason.

Each entry in `QueuedSteers` says what became of it: `queued`, `delivered` (with `Turn`),
`held`, `applied` or `dropped` (with `Reason`). The run summary carries them as `queuedSteers`,
so `run.updated` moves the desktop composer's chips as they resolve.

In the desktop app the composer stays editable while the run works on a provider that can be
steered. Enter queues what is typed; the round button stays Stop. A row of chips above the box
lists the newest few queued steers with `queued for the next turn`, `delivered at turn N`,
`waiting for the run to finish` or `not delivered: …`, and the transcript shows each one as a
"you" card at the point the agent read it.

## Changing the model

`--model NAME` on `steer`, and on `resume`, puts the continued session — and every session of the
run after it — on another model. It is the way past a run blocked on a per-model limit
(`providers.claude.fallbackModels` in `docs/config.md`), and it is also how to ask a second model
to check the first one's note without starting the triage again.

It is honoured, not merely passed: `claude -p --resume <id> --model <other>` answers under the
model named, in the same session, with the transcript the first model built. Verified against the
CLI on 2026-09-17 — a session started on `haiku` and resumed with `--model sonnet` reported
`claude-sonnet-5` on its `system/init` line, on the assistant message and in the result line's
`modelUsage`, under the session id it was started with.

The run records which model answered which stretch of it (`ModelSegments` in `state.json`, with
"start", "model limit: Fable", "resume --model" or "steer --model" as the reason), and `Model`
stays the model the run is on now — which is what a register row and a session header name, so
they name the model that actually finished the run. Over HTTP and on the desktop bridge the
model travels as `model` on the same `resume` and `steer` calls; in the desktop app the
composer's Model chip picks it on a run that has stopped.

## Over HTTP

`POST /api/workspaces/{id}/runs/{runId}/steer` with `{"text": "..."}`, answering `202` with
`{"jobId": ..., "runId": ...}` — or, on a run that is still working, `{"jobId": "", "runId":
..., "queued": true}`, since the executor already running it takes the instruction and no job
starts. The desktop bridge's `Steer` returns an empty job id for the same case.
`POST .../resume` takes `{"answer": "..."}`. Both also take an
optional `"model"`. The run's `state.json` goes to `running`, and `run.updated` and `run.event`
flow over `/api/events` as they do for any run. The desktop app has the same two calls on its
bridge.

## What the run records

- `state.json` gains a `Steers` list — when, the instruction, and who answered it — and
  `Usage.ElapsedSeconds`, the wall-clock time every session of the run has spent. A run that
  changed model partway through also gains `ModelSegments`.
- `events.jsonl` gets a `steer` line ahead of the session's own events, carrying the instruction
  and a `continuation` of `resume` or `primed` — or `live`, with `turns`, for one delivered into
  a working session. A steer typed while the run worked also leaves `steer_queued` and, when it
  had to wait for the run to settle, `steer_held`. A primed continuation also records a `system` line
  reading `continued in a new session`.
- The status goes `completed → running → completed` (or `blocked`, `failed`, `over_budget`, as any
  session can end). If the new answer differs from `result.json`, the note is rendered again in
  the run directory and in the notes directory, and a new register row is appended for the same
  run id. An identical answer leaves the note and the register alone.

## Who answers: resume or primed

Each provider continues a run one of two ways, and the transcript says which.

| Provider | Continuation | How |
|---|---|---|
| `claude` | resume | `--resume <session id>`, with the same permission tool, `--disallowedTools`, MCP flags and policy as the first session |
| `codex` | resume | `thread/resume` on the recorded thread id, same sandbox and approval policy |
| `qwen` | resume | `--resume <session id>` |
| `openai` | resume | the run's `transcript.json`, reloaded as the loop's message history |
| `acp` | primed | a fresh `session/new`, handed the run's original prompt, its earlier answer, and the instruction |
| `cursor`, `agy` | refused | neither lets Sirdar judge a tool call before it runs, so an open-ended follow-up cannot be held to the read-only guarantee |

**Resume** means the agent that wrote the note picks up its own context: it still has every
file it read and every command it ran. The prompt it gets is the instruction, plus one sentence
saying the reply is the run's JSON document again, whole, in the same schema.

**Primed** means a new session is handed the run's original `prompt.md`, its previous answer
(`result.json`, or `result.raw.txt` for a run whose answer never validated), and the
instruction. The bundle is still on disk where the prompt points, so the new session has what
the first one had — minus the memory of having read it. ACP is always primed, because an agent's
`session/load` capability is only known once the agent process exists. A resume provider whose
run recorded no handle is primed too, rather than refused.

A steer never widens permissions. The session is built by the same code path the run used, with
the same mode, policy, root and allow-lists; the instruction reaches the agent as prompt text
and nothing else. Nothing is written to a helpdesk or a tracker.

## Fix runs

A fix run can be steered while its worktree is still there: a `--local` run keeps it, and so
does a run blocked on a deviation. A pushed run had its worktree removed and its branch is on
the remote, so it is refused — a follow-up on it is a new `sirdar fix`, or a commit of your own.

The session stands in the same worktree on the same branch, in fix mode with the fix policy
rooted there, and the snapshot guard is taken before and checked after it exactly as `sirdar
fix` does. If the session changed the tree, the run's commit is amended
(`git commit --amend --no-verify`) with a message rebuilt from the new report, and
`Fix.Commit` and `fix.diff` follow it. The branch is unpushed by construction, so rewriting it
costs nothing, and one reviewed commit per fix is what `--accept-deviation` compares the branch
against. An unchanged tree makes no commit. The deviation gate is judged again from the new
report. A steer never pushes: publishing stays with `sirdar fix KEY --accept-deviation`.

## Budget

Turns, tokens and cost accumulate on the run. The steer's session starts from the totals the
run already has, and its own counters are added on top; the configured caps apply to the sum.
The turn budget handed to the provider is what remains, the wall-clock timer is
`budget.maxMinutes` less the seconds already spent, and `budget.stallMinutes` is unchanged. A
run that has already reached any cap is refused before a session starts.

## Refused

- an `over_budget` run, or one whose turns, minutes or cost have reached the cap
- a provider with no continuation (`cursor`, `agy`), or a run made under a different provider
  than the one the workspace now uses — the handle would name a session that CLI never held
- a fix run whose worktree is gone, or whose branch has moved off the commit the run made
- a triage or rca run made with `--at` whose worktree is gone (keep it with `--keep-worktree`)
- an eval run, whose note is a measurement and stays out of the register
- an empty instruction

A model named on a steer or a resume is not a refusal of any kind: the run's *provider* cannot
change — the session handle names a session that CLI holds — but the model can.

Every refusal happens before `state.json` is touched. Over HTTP a run-level refusal is a `409`;
a provider-level one is only known once the job has built its dependencies, so it ends the job
failed with the reason on the activity pane, the way a refused fix does.

## Verified live, 2026-09-15

A steer ran end to end on `provider: claude`, which settles the one number this feature depended
on. Claude's `--resume` reports `num_turns` and `total_cost_usd` **for the resumed invocation
alone**, not for the whole conversation: the resumed result line carried 3 turns and $0.3043, and
the run that already held 14 turns and $0.7198 came out at 17 turns and $1.0241. So the
accumulation above is right as written, and a resumed session's usage is not counted twice
against the cap.
