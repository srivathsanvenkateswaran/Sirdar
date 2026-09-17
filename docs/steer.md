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
can be steered too; the instruction is then what the agent gets instead of an answer.

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
`{"jobId": ..., "runId": ...}`; `POST .../resume` takes `{"answer": "..."}`. Both also take an
optional `"model"`. The run's `state.json` goes to `running`, and `run.updated` and `run.event`
flow over `/api/events` as they do for any run. The desktop app has the same two calls on its
bridge.

## What the run records

- `state.json` gains a `Steers` list — when, the instruction, and who answered it — and
  `Usage.ElapsedSeconds`, the wall-clock time every session of the run has spent. A run that
  changed model partway through also gains `ModelSegments`.
- `events.jsonl` gets a `steer` line ahead of the session's own events, carrying the instruction
  and a `continuation` of `resume` or `primed`. A primed continuation also records a `system` line
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

- a run that is `preparing` or `running` — a live run is answered, not steered
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
