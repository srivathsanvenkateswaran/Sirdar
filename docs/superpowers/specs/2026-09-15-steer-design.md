# Steer: a follow-up instruction on a finished run

`sirdar steer RUN_ID "instruction"` and `POST /api/workspaces/{id}/runs/{runId}/steer`
continue a run that has already ended. The run's transcript grows in place, its note is
re-rendered when the answer changes, and its budget keeps counting from where it stopped.
A steer is not a new run: `sirdar runs` shows the same id, the same key, one more session.

## The run record

`store.State` grows two things. `Steers []Steer` records every instruction the run has taken,
newest last: `At`, `Text`, and `Continuation`, which is `resume` when the provider carried on
the same session or `primed` when a fresh session was opened with the run's own note. `Usage`
gains `ElapsedSeconds`, the wall-clock time every session of the run has spent, because the
minute budget has to apply to the total and nothing recorded it before.

`Handle` keeps its meaning: the provider's own token for the session, whatever that is for the
provider — a Claude session id, a Codex thread id, an ACP session id, the path of the openai
loop's `transcript.json`. Every finished run already carries one, which is what a steer resumes.

`events.jsonl` gets a `steer` line before the session's own events, with the instruction and
the continuation in its payload, so a reader of the transcript can see where the person spoke
and whether the agent that answered was the one that wrote the note. A primed continuation
also records a `system` line reading `continued in a new session`.

Status goes `completed → running → completed` (or `blocked`, `failed`, `over_budget`, as any
session can end). The transition is written to `state.json` the way the first run's was, so the
watcher sees `running`, tails the event log again from where it left off, and `run.updated` and
`run.event` flow to the UI exactly as they did the first time. A steer may start from
`completed`, `blocked` or `failed`. On a completed run the note is re-rendered and a register
row appended when the new answer differs from `result.json`; an identical answer leaves both
alone and records the steer in the state only.

## What "continue" means per provider

The optional `provider.Steerable` interface (`Continuation() Continuation`,
`SteerRefusal() error`) is asked before anything is written, the way `FixSupport` is asked
before a branch is cut. `provider.PlanSteer(p)` returns the continuation or the refusal; a
provider that says nothing is taken to resume by handle, which is what the runner's schema
retry already relies on.

| Provider | Continuation | Mechanism |
|---|---|---|
| `claude` | resume | `--resume <session id>`, with the same `--permission-prompt-tool`, `--disallowedTools`, MCP flags and policy as the first session |
| `codex` | resume | `thread/resume` on the stored thread id, same sandbox and approval policy |
| `qwen` | resume | `--resume <session id>`, the path the schema retry already takes on a live Qwen run |
| `openai` | resume | the run's `transcript.json`, reloaded as the loop's message history |
| `acp` | primed | a fresh `session/new`; `session/load` is only offered by some agents and is known only after `initialize`, so it is not relied on |
| `cursor`, `agy` | refused | neither gives Sirdar a tool call to judge before it runs; an open-ended follow-up cannot be held to the read-only guarantee |

A resume provider whose run recorded no handle degrades to primed rather than refusing.

The resumed session's prompt is the instruction, wrapped in one sentence saying the reply is the
run's JSON note again, in the same schema. The primed prompt is the run's original `prompt.md`,
then its previous answer (`result.json`, or `result.raw.txt` for a run that failed validation),
then the instruction. The bundle is still on disk, so a primed agent has what the first one had.

A steer never widens permissions: the session spec is built by the same `sessionSpec` the run
used, with the same mode, policy, root and allow-lists. The instruction reaches the agent as
prompt text and nothing else.

## Fix mode

A fix run can be steered while its worktree is still there, which means a `--local` run or one
blocked on a deviation; a pushed run's worktree was removed and the steer is refused. The
session stands in the same worktree, on the same branch, in `ModeFix` with `FixPolicy` rooted
there, and the snapshot guard is taken before and checked after it exactly as `sirdar fix` does.

If the session changed the tree, the run's commit is amended (`git commit --amend --no-verify`)
with the message rebuilt from the new report, and `Fix.Commit` and `fix.diff` are updated. The
branch is unpushed by construction — a pushed run cannot be steered — so rewriting it costs
nothing, and one reviewed commit per fix is what `--accept-deviation` compares the branch
against. An unchanged tree makes no commit. The deviation gate is re-evaluated from the new
report. A steer never pushes: publishing stays with `sirdar fix KEY --accept-deviation`.

## Budget

Turns, tokens and cost accumulate on the run: the steer's session starts from the totals the
run already has, and each session's own counters are added on top rather than replacing them.
The configured caps apply to the sum. The turn budget handed to the provider is the remainder;
the wall-clock timer is `budget.maxMinutes` less `ElapsedSeconds`; the stall watch is unchanged.
A run that has already reached any cap is refused before a session starts.

## Refused

- a run that is `preparing` or `running` — a live run is answered, not steered
- an `over_budget` run, or one whose turns, minutes or cost have reached the cap
- a provider with no continuation (`cursor`, `agy`), or a run made under a different provider
  than the one the workspace now uses — the handle would name a session the CLI never held
- a fix run whose worktree is gone, or an `--at` run whose worktree is gone
- an eval run, whose note is a measurement and stays out of the register
- an empty instruction

Every refusal happens before `state.json` is touched, so a refused steer leaves no trace.

## Surface

`sirdar steer RUN_ID "instruction"` prints the notes and the digest row, like `resume`.
`POST /api/workspaces/{id}/runs/{runId}/steer` with `{"text": "..."}` answers 202 with the
job id and the run id once the run-level refusals above have passed (a live run is 409, an
unknown run 404); the provider-level refusal is only known once the job has built its
dependencies, so it ends the job failed with the reason on the activity pane, as a refused fix
does. The Wails bridge gets the same `Steer(ws, runId, text)` for parity.
