# Blocked runs

A run is `blocked` when it is waiting on a person. Five things stop a run that way: the
agent asked a question in words, the agent wants to make a call the policy refused,
a rate limit parked it, the login ran out of one model, or it was interrupted. This page
is about the second.

## Permission questions

When a session tries a call the workspace's permissions do not cover, but that the
operator could allow, Sirdar does not refuse it outright. The provider is told the call
is waiting on the operator, the session stops, and the run blocks with the question on
its state:

```
[SBX-1] blocked asking: rg -n refund src
```

What gets asked about, and what never does:

| Asked | Never asked: a plain refusal |
|---|---|
| A shell command a segment of which is off `permissions.bash` (`permissions.fixBash` in a fix) | A write in a triage run: the read-only guarantee is not something an answer widens |
| An MCP tool `permissions.mcp`, or the write-word heuristic, refuses | A fix write outside the worktree, or into `.git/`, `.sirdar/` or the hooks directory |
| A fetch to a host `permissions.fetch` does not name | A shell construct (`$(…)`, a backquote, a redirection), a path out of the workspace, a git flag the policy refuses |
| A read outside the workspace, the run directory and its bundle | A fetch to an IP literal, a private address or a loopback port the list does not name |
| Any other tool the policy does not know | |

The rule behind the right-hand column: a question is put only when some answer could let
the call through. Allowing a command that would still be refused for its redirection is
a button that does nothing, so it is not offered.

`permissions.ask: false` in `.sirdar/config.yaml` turns this off: the call is refused and
the agent carries on without it, which is how every run behaved before. An eval never
asks; it replays a bundle with nobody there to answer.

## Answering

Three answers, each a decision rather than a sentence:

| Answer | What it does |
|---|---|
| **Allow once** | The resumed session is told to make the call again, and the policy lets that one call through. It is spent by the first call that matches it exactly and is not kept. |
| **Allow for this run** | The call, and any later call in the same run matching the same pattern, goes through. The pattern is the program (`rg *`), the program and its subcommand for one that takes one (`git log *`, `go test *`), the MCP tool's own name, the fetch's host, or the read's directory. Every other rule still applies: a granted `rg` with a redirection is still refused. |
| **Deny** | The resumed session is told not to try the call, or anything like it, again — with the operator's reason when one was given. The policy refuses any later call sharing the pattern, with that reason, without asking again. |

A grant lasts the run and nothing longer. Allow for this run and Deny are kept on the
run's `state.json` as `Grants`; nothing is written to `config.yaml`. To allow a command in
every run, add it to `permissions.bash` yourself.

Each answer is written into the run's `events.jsonl` as a `grant` line in the operator's
voice — "you allowed rg for this run" — which the session screen shows as your line in
the transcript.

### From the desktop app or `sirdar serve`

The composer of a run blocked on a permission question carries a decision bar above the
text box: the tool and the call on one line (the whole call, and the policy's reason, on
hover), then **Allow once**, **Allow for this run** and **Deny**. Deny opens a one-line
reason field, optional. ⌘⏎ (Ctrl+Enter) is Allow once; ⌘⌫ (Ctrl+Backspace) is Deny,
except in a field that has text in it, where the key keeps deleting.

The text box under the bar stays for an answer in words. That answer runs nothing: the
session is told the call was not made and is given your words instead, and it may ask
again.

A board card for such a run reads `blocked · asking: rg …`.

### From the command line

```
sirdar resume RUN_ID --allow
sirdar resume RUN_ID --allow-run
sirdar resume RUN_ID --deny --reason "the index is faster"
```

With none of the three, `sirdar resume` puts the question on the terminal and reads
`a`, `r` or `d`, or anything else as an answer in words.

### Over HTTP

`POST /api/workspaces/{id}/runs/{runId}/resume` takes the decision beside the answer:

```json
{ "decision": { "verdict": "allow_run", "reason": "" }, "answer": "" }
```

`verdict` is `allow`, `allow_run` or `deny`. A verdict outside those is a 400; a decision
sent to a run that is not waiting on a permission question is a 409. The run detail
(`GET …/runs/{runId}`) carries the question as `question`:

```json
{ "question": { "text": "rg -n refund src",
  "decision": { "kind": "bash", "tool": "Bash", "summary": "rg -n refund src",
                "patterns": ["rg *"], "verdict": "deny",
                "reason": "Sirdar policy: not permitted by permissions.bash; …" } } }
```

`kind` is `bash`, `mcp`, `fetch` or `tool`. A run blocked on a question in words carries
`question.text` alone.

## How each provider is asked and answered

The question reaches each provider through its own permission protocol, and so does the
answer once the run resumes:

| Provider | While the operator is asked | Allowed | Denied |
|---|---|---|---|
| `claude` | the permission tool answers `deny` with `interrupt: true`, which ends the turn | `allow` with the call's input | `deny` with the operator's reason as the message |
| `codex` | the approval is answered `cancel` (command execution) or `action: cancel` (MCP elicitation), which declines and interrupts the turn | `accept`; `accept` with empty content for MCP | `decline` |
| `acp` | `session/request_permission` is answered with the `cancelled` outcome — no option chosen | the agent's `allow_once` option, never `allow_always`, which an agent may keep beyond the run | the `reject_once` option |
| `qwen` | the `PreToolUse` hook answers `deny`, with `continue: false` | `allow` | `deny` with the operator's reason |
| `openai` | Sirdar's own loop answers the call as waiting, closes out the rest of the batch and stops | the tool runs | the call is answered with the refusal |

In every case the run layer then stops the session and keeps its handle; the resumed
session is the same conversation, told the answer. `cursor` and `agy` are never asked:
neither lets Sirdar judge a call before it runs (see [the providers table](config.md#providers)).
