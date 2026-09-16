# Direction A — Conversation first

Open any `A-S*.html` in a browser at 1470×900 (the pages set the viewport). Every string comes from the SBX-1 runs
`20260915T121105Z-076d` (triage) and `20260915T121451Z-bf19` (fix), their bundle, and the triage note; trimmed where a
sentence ran long, never rewritten. Sources: `sirdar-mocks/A-session.css`, `A-assemble.py` (fragments), `A-s1..6.body.html`.

## The idea

The transcript is a chat, in the shape of Codex App and T3 Code: the person's messages are bubbles on the right, the
model's own words run as plain prose on the left, and everything the agent did to the machine sits between them as a
stack of one-line tool cards that expand in place. The run's conclusion is not another log line; it is a rich card at
the end of the conversation (title, root cause with `file:line` links, evidence, confidence, blast radius, proposed fix,
the Arabic reply draft) and the note it wrote is linked from that card's footer. When the agent is blocked, the block is
a highlighted message in the same flow and the composer under it is already focused to answer. The right pane is an
inspector, not a second transcript: the note rendered as a document, the bundle as a ticket card plus an RTL thread,
the tool calls as a sortable table, the change as a file list plus diff with the checks and the branch decision beneath.
Type carries the hierarchy: the answer is 22px and the person's steer is 16px, while the log is 13px mono at 36px a row.

## Primary and secondary

Primary: the answer card (S1), the question card and its composer (S2), the diff plus checks plus branch decision (S6).
These get the large type, the most surface, and the one filled control (the round send; "Answer" when blocked).

Secondary: tool cards, thinking stamps, permission stamps, system lines, the topbar stats. They are small, grey and
mono, and they collapse: eight consecutive reads are one stack with a summary row ("8 calls · 00:03 – 00:10 · all within
policy"), a denied call is one row plus a one-line reason in the blocked colour, and thinking is a stamp ("Thought for
16 s · ~1,100 tokens") because the provider returned no thinking text. The earlier answer that the steer superseded is
folded to a single row so the revision stands alone.

## Long outputs

A card's header always shows the output's size (lines, bytes, elapsed) so you know what expanding will cost. Expanded,
the card shows Input as a key–value list (command, the model's own description, cwd) and Output in a region of at most
262px that scrolls inside the card with its own scrollbar. Line-oriented output (rg, ls, git log --stat) is parsed into
a table with sticky headers; file contents render as code with line numbers; test output as a code block with the
failing assertion coloured. Cells truncate with an ellipsis rather than wrapping so a 17-row table stays 17 rows tall.
"As text" flips back to the raw stream and "Copy" copies it. The Tools tab in the inspector is the same data as a table
for every call, with a bytes and lines column, and clicking a row scrolls the transcript to that card (S5 shows the
pairing highlighted).

## Blocked, streaming, finished

Blocked (S2): the topbar badge reads "blocked · waiting on you"; the last card carries a "waiting for your approval"
stamp; below it the question is a card with an amber left bar, the command in a box, the reason, and Allow once / Allow
`go test *` here / Deny; the composer has the accent border and a caret, and the send is a wide "Answer" button. The
inspector jumps to Changes so the pending test is readable while you decide.

Streaming (not an artboard; the same components): the badge reads "running", the newest card has a live-tinted stamp
with the elapsed time counting, a thinking stamp animates its token count, the composer placeholder says the run resumes
with your message, and Cancel sits in the topbar.

Finished (S1, S6): the badge reads "completed"; the window opens scrolled to the answer card, not to the first tool call;
a system line closes the transcript with turns, cost and tokens; the composer offers a follow-up and the note or the
branch action lives in the card footer and the inspector.

## What I would cut from today's window

The four-column event ledger (time · mark · tool · summary) as the primary surface; the raw-JSON style of the final
answer; the "turn N" separators (the thinking stamps and cards already give the rhythm, and the topbar has the count);
the permission reason repeated as a row of its own (it is a stamp on the card, with the reason underneath only when
denied); the banner above the transcript; hunk Keep/Drop buttons in the transcript (they belong to the Changes pane);
and the note as a text dump (it is a document with a metadata strip, callouts for root cause and blast radius, and the
Arabic block set RTL).

## Artboards

- `A-S1-Triage.html` — completed triage run opened fresh: the revised answer card, the note in the inspector.
- `A-S2-Blocked.html` — fix run waiting on approval to run `go test ./...`; composer focused to answer; Changes shows the test just added.
- `A-S3-Expanded.html` — the `rg -n -i "partial|Quantity"` call expanded into input plus a 17-row table; the Note pane on Root cause.
- `A-S4-Pane.html` — Bundle: tracker card, helpdesk card, the four Arabic messages RTL, empty attachments, playbooks.
- `A-S5-Tools.html` — Tools: all 15 calls with time, decision, duration and output size; one row paired with its card.
- `A-S6-Changes.html` — fix run's change review: two files, both ledger.go hunks, the four checks, no deviation, Push branch.
