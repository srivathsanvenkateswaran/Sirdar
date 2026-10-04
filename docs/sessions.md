# Sessions

A session is an instruction with no fixed shape of answer: ask about anything, with or without
a ticket, and the reply comes back in chat rather than as a filed note. It is read-only by
default — the agent reads the repository and the ticket, and writes nothing to either.

```
sirdar ask "Why is the refund for order 1234 stuck in pending?"
sirdar ask "Was it the PR that broke this?" OMNI-1
```

The composer starts the same thing from the desktop app or the web UI: type an instruction, with
or without a ticket reference, and the reply streams into the chat pane as it is written.
`POST /api/workspaces/{id}/sessions` takes `{"instruction": "...", "reference"?: "...",
"access"?: "read-only", "provider"?: "...", "model"?: "..."}` and answers `202` with
`{"jobId": ..., "runId": ..., "key": ...}` — the run id and the key the run directory is filed
under, both known before the job itself runs. `access` is `read-only` or `worktree`; worktree
sessions are not available yet, and asking for one is a `400`.

## The key

A session started with a ticket reference is filed under that ticket's key, the same way a
triage or an RCA is. Without one, the key is `ASK-<yyyymmdd>-<slug>`: the date of the start in
UTC, and a slug built from the first few words of the instruction. `sirdar ask "Why is the
refund for order 1234 stuck in pending?"` on 2026-10-04 files under
`ASK-20261004-why-is-the-refund-for`.

## Follow-ups are conversation

`sirdar steer RUN_ID "..."` on a session, or on a triage or RCA run that has already answered in
chat, is a follow-up: no schema, no note turn. The agent that wrote the reply (or a fresh one
handed the same context) answers again, and the new reply replaces the run's `answer.md`. See
[Steer](steer.md).

## Triage and RCA reply first, then file the note

A triage or RCA run started by anything but `eval` answers the operator in chat before it files
its note: the chat reply lands first, then a second turn — `state.json`'s `phase` is `note`,
status stays `running` — writes the triage or RCA note and the register row the way it always
did. The run's card shows "Filing the note…" while that second turn runs.

A note turn can fail without failing the run: the operator already has their answer. The run
ends `completed` with `noteWarning` set to `"note not filed: <reason>"`, and nothing is in the
notes directory for it yet. `sirdar note RUN_ID`, or `POST
/api/workspaces/{id}/runs/{runId}/note`, runs the note turn again and replaces whatever the run
filed — the same fix for a note that filed but needs a re-run, as for one that never filed at
all.

## Save as note

A session files no note on its own; `POST /api/workspaces/{id}/runs/{runId}/save` writes its
reply into the notes directory and answers with the path. The filename comes from
`notes.filenames.session`, default `Sessions/{key} {slug}.md`, where `{slug}` is built from the
instruction's first line. The file carries frontmatter — the key, the run id, when it started,
and the instruction — then a blank line, then the reply exactly as `answer.md` holds it. Saving
again overwrites the same file.
