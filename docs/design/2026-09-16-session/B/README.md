# Direction B — the note is the work

## The idea

The agent's deliverable is a document, so the window is built around that document: the triage note in the centre, rendered as a note and not as a chat reply, with the path the agent took as a secondary timeline on the left. The reader gets the conclusion first (the title is the finding, the root-cause paragraph is the mechanism) and can audit backwards: every evidence item in the note carries a marker (E1…E9) and the same marker sits on the tool call in the timeline that produced it, so a claim like "ledger.go:33 and again at ledger.go:34" is one glance away from the `Read ledger.go` step at 00:06. The timeline is grouped by turn into steps that read as sentences ("Read ledger.go · 65 lines", "Ran rg -n "Return|restock" · 10 matches", "Ran go test ./... · FAIL · 1 test") and expand in place to input and output; the model's own words sit between steps as short quoted annotations, and permission decisions are stamps (denied, allowed, waiting on you) rather than rows. Bundle and Tools are drawers that slide over the timeline and leave the document untouched, because the ticket and the call log are things you consult while reading the note, not destinations of their own. The agent's question and the composer live under the document: a Reply strip when the run is blocked, a Steer strip when it has finished.

## Primary and secondary

Primary: the document. For a triage or RCA run it is the note (metadata strip, title in Newsreader, root cause with inline `file:line` references and evidence markers, the customer's complaint in Arabic beside its translation, evidence callouts, proposed fix, blast radius, repro, conversation, open questions, and the reply draft as a letter with Copy and Open desk). For a fix run it is the change: branch, base, commit, worktree, checks, then the diff per file with Keep/Drop on each hunk, the agent's risks paragraph, and the push decision.

Secondary: the path on the left (432px), scrolling on its own, grouped by turn with the elapsed time on the right. Read steps carry evidence markers; Bash steps carry their result count; denied steps carry the policy reason in one line; the steer you sent appears as a "you" card in the sequence, with "resumed" after it; the two rate-limit warnings the run emitted appear as small grey system lines. The drawers (Bundle, Tools) open from the path header and cover only the path column.

## Long outputs

A collapsed step shows only its result count or size ("65 lines", "17 matches", "1 commit · 6 files"). Expanded, the input is shown verbatim and the output is rendered by shape: `rg` and `ls` and `git --stat` output become tables (file:line · match), test output becomes a monospace block with the failing line in red, file reads keep their line numbers. The expanded output is capped at about 300px with its own scrollbar and a footer ("17 rows · 1.1 kB · 80 ms", the one-line reading the agent gave it) plus "Open in Tools", which opens the full call in the Tools drawer. The Tools drawer lists every call with time, decision, duration, output size and which evidence it produced; the note's markers are clickable there too.

## Blocked, streaming, finished

Blocked (S2): the topbar badge is amber "blocked · waiting on you", the pending step is amber with a "waiting on you" stamp and stays expanded showing what the agent asked to run and the policy that stopped it, and the strip under the document is a Reply strip in the same amber with the question, its reason, the suggested rule, a decision segment (Allow once / Allow `go test *` this run / Deny) and the one filled button, Answer.

Streaming: the topbar badge is the accent "running", the newest turn group pins to the bottom of the path with a live elapsed counter, steps land as they finish (collapsed), and the document shows what exists so far (the fix diff as it grows; for triage, the metadata strip and "the note arrives when the agent finishes" with a progress line of turns and cost against budget). The strip is Steer, with the send button greyed until the run yields.

Finished (S1, S6): the badge is green ("completed · note saved" / "completed · committed f144936"), the last step in the path is the accent-coloured "Wrote the note → document" or "Wrote the fix report → document", and the strip is Steer with the composer active and the resume handle shown as a chip. Steering puts your message into the path as a "you" card and the run continues below it, exactly as the real 02:02 steer in this run did.

## What to cut from today's window

The four-tab right pane. Note stops being a tab and becomes the window; Changes is the same document for a fix run; Bundle and Tools become drawers. The raw event stream as the primary surface goes: no `·` marks, no bare `edit`/`no` glyphs, no per-event grid. The tests-passed banner at the top of the transcript goes into the document's Checks section. Cost, turns and elapsed stay in the topbar only, not repeated in the stream. The "Create branch" button goes: Sirdar already committed on the branch, so the decision is Push or Discard, and it sits at the end of the change where the reviewer arrives after reading the diff.

## Notes on the data

Every string is from the SBX-1 runs 20260915T121105Z-076d (triage), 20260915T121451Z-bf19 (fix) and their bundle, note.md, fix.diff and result.json. Three things the real runs made me decide:

- The Claude runs emitted no interstitial prose (thinking is redacted, no text blocks), so the "model's own words" between steps are its per-call descriptions ("Run tests before the fix", "Search Go code for partial-return handling") and the note's per-call findings. Where a Read had no description there is no annotation, and the mock does not pretend otherwise.
- No sandbox run asked an `AskUserQuestion`. S2 uses the fix run's real pause at 00:11: `go test ./...` needed approval ("This command requires approval", suggested rule `go test *`). That is the blocked moment the design has to serve.
- The fix run's events record a review action at 00:49: `drop` on ledger_test.go hunk 0. S6 shows that stamp as recorded ("dropped by you · 00:49") with Restore, rather than a tidier invented state.

Files: `B-S1-Triage.html`, `B-S2-Blocked.html`, `B-S3-Expanded.html`, `B-S4-Pane.html`, `B-S5-Tools.html`, `B-S6-Changes.html` (1470×900, open in a browser; each region scrolls on its own). Sources in the mocks scratchpad: `B-session.css`, `B-gen.py` (assembles the six bodies), `B-build.sh`.
