# Task F2 report — the composer reads an instruction as a session

## What was implemented

### `src/lib/composeIntent.ts`

- `IntentMode` is now `'session' | 'triage' | 'rca' | 'fix'`.
- `parseIntent` no longer scans prose for mode words and cuts them out
  unconditionally. Instead:
  - A leading slash command (`/triage`, `/rca`, `/fix`, case-insensitive) sets
    the mode outright and is cut from the instruction like any other
    reference.
  - Absent a slash, once every reference (key, helpdesk number, helpdesk URL,
    Slack link, tracker URL) is cut out, the *remainder* is checked against
    `MODE_PHRASE` (`triage`, `rca`, `root cause`, `resolution`, `fix`,
    `implement`, case and surrounding punctuation ignored). Only when the
    remainder is nothing but that one phrase does it set the mode, clearing
    the instruction. Anything less bare — "fix the tax rounding", "triage
    OMNI-1 then fix it" — leaves the mode unset and the word in the
    instruction as prose.
  - `ambiguity`'s `'no-key'` is now produced only when the mode is triage, RCA
    or fix (by slash or by the bare word) and the line names no reference; a
    session never needs one. `'two-keys'` is unchanged. `'two-modes'` is no
    longer produced, but the union member and its `AMBIGUOUS_REASON` entry
    (in `NewSession.tsx`) are both left in place, per the brief, so old
    callers still compile.
- New `intentKind(intent)`: the mode if `parseIntent` set one; else
  `'session'` when there's an instruction; else `'triage'` when there's a
  reference and nothing to say about it; else `''`.
- `intentChips` branches on `mode === 'session'`: `['Session', <key or
  resolution, if any>, <access phrase, if given>]`, with no "with your note"
  chip (a session's instruction is its task, not a note attached to one).
- `COMPOSER_PLACEHOLDER` is the new line from the brief.
- `MODE_LABEL` gains `session: 'Session'`.

### `src/components/composer/modes.ts`

- `MODES` lists Session first, with its note from the brief.
- `accessOf(mode, access?)` returns `access ?? 'read-only'` for a session;
  unchanged fixed postures for the other three.
- `ACCESS`'s two notes gain the session sentences from the brief (the
  worktree note's wording differs slightly from the brief's: see Decisions).

### `src/screens/NewSession.tsx`

- `StartOverrides` gains `reference?: string` and `access?: Access`.
- New `pinnedAccess` state. `mode` is
  `pinnedMode ?? (confirmed?.mode || intentKind(intent) || 'session')`.
  `access` is `mode === 'session' ? (pinnedAccess ?? 'read-only') :
  accessOf(mode)`.
- `begin` takes a fifth, optional `{ reference?, access? }` argument, merged
  into the overrides sent to `onStart`. A session passes `forKey: ''` since it
  names no key as its second argument; `starting` is set to `forKey || what`
  so `busy` still reflects a session start in flight (a session's `forKey` is
  always empty, so the old `setStarting(forKey)` would have been a no-op for
  it).
- `canStart`: for a session, `instruction !== '' && !badAsk && !sessionRefBad`
  (new `sessionRefBad` = the typed reference resolved to a reason and no
  key). Triage/RCA/Fix unchanged.
- `chips` and `blocked` both branch on `mode === 'session'`: chips show once
  there's an instruction; `blocked` is `askReason(badAsk)`, then
  `resolved.reason` when the reference turned out bad, then `''` once
  there's an instruction, else `'Type what you want done'`.
- `start()` calls `begin('session', '', instruction, '', { reference:
  intentRef(intent) || undefined, access })` for a session.
- The Mode chip's `modeItems` no longer disables Session when the key has no
  triage note (it already didn't need one).
- The Access chip is selectable (`onSelect`) only for a session; otherwise
  `readOnly="Fixed by the mode"`, which disables the chip outright (see
  Decisions — this is a behaviour change from the pre-F2 chip).
- `App.tsx`'s `startSession` dispatcher gained `case 'session'`, calling
  `store.startSession({ instruction: o.instruction ?? '', reference, access,
  provider, model })`.

## Tests

### `composeIntent.test.ts`

Updated five table entries that encoded the old "first prose mode word sets
the mode" behaviour, now that a mode word sharing the line with anything else
is prose:
- `'OMNI-3233 fix the tax rounding on invoice lines'` → mode `''`, the whole
  phrase stays in the instruction.
- `'root cause for OMNI-9 please'` → mode `''`, same.
- `'triage OMNI-1 then fix it'` → mode `''` (was `'two-modes'`).

And three entries where `'no-key'` no longer applies because the line sets no
ticket-mode:
- `'#123 is not a ticket'`, `'the export is empty again'`, and the
  look-alike-Slack-host case → ambiguity `''` (these are sessions now).

Renamed/rewrote the one `it` that asserted a mode word was cut from the
instruction (`'fix OMNI-1 and leave the schema alone'`); it now stays in the
instruction (`'fix and leave the schema alone'`).

Added: slash-command coverage (`/rca`, `/fix ... keep it small`, `/triage`
alone → `no-key`), bare-mode-word coverage (`triage OMNI-2510`, `OMNI-2510
root cause`), the Triage-with-a-comma session example from the brief, the
two-keys-regardless-of-wording case, an `intentKind` describe block with the
three brief examples, and an `intentChips` session case.

### `NewSession.test.tsx`

Added the six tests the brief named: starts a session from an instruction
alone; starts a triage from a reference alone; starts a session about a
ticket; takes `/rca` as RCA; offers worktree access for a session only; opens
the session once its run appears.

Updated for the new default (a key with something to say about it is a
session, not a triage pinned implicitly):
- The placeholder string.
- The three-chips test: default Mode is now Session, and the disabled Start
  button's title is `'Type what you want done'`.
- `'carries what was typed around the key...'` now pins Triage first (renamed
  to say so), since that combination is a session by default.
- The Access-chip explanation test: rewritten, because `readOnly` on
  `ChipMenu` makes the chip fully disabled rather than an explaining dialog
  (see Decisions) — it now checks the fixed/disabled state directly instead
  of opening a dialog.
- Four helpdesk/repository/Slack tests that typed `<key> <words>` now pin
  Triage after typing, since they're exercising triage-specific resolution
  and note-carrying, not the mode default.
- The four "line the parser cannot settle" tests that used plain
  no-key prose (no longer ambiguous — it's a valid session) were rewritten
  around two-key lines, which are still genuinely ambiguous, or around the
  new session default where that's what the test actually meant.

## Commands and results

- `cd desktop/frontend && npx tsc --noEmit -p .` — clean.
- `npx vitest run` — 116 files, 2044 tests, all passing.
- `go vet ./... && go test ./...` (worktree root) — all packages pass,
  including `desktop` (the bridge cross-check test).
- `gofmt -l .` — no output (no Go files touched).

## Decisions not fully spelled out in the brief

1. **`ACCESS`'s worktree note wording.** The brief gives: "A session or a fix
   writes in a linked worktree under .sirdar/worktrees; the tree you work in
   is untouched. A session commits nothing; a fix is committed and pushed by
   Sirdar, never the agent." — which is what I used verbatim.
2. **The Access chip's `readOnly` mode fully disables the popover.**
   `ChipMenu`'s existing contract is that `readOnly` makes the chip itself
   `disabled` and drops `aria-haspopup`/`aria-expanded` entirely — it's an
   inert fact with a tooltip, not a clickable explanation. Before F2 the
   Access chip used neither `onSelect` nor `readOnly`, so it rendered an
   always-reachable explaining dialog. The brief's "disabled with the title
   'Fixed by the mode'" matches `readOnly` exactly, so I used it, but that
   means the pre-F2 "click Access to read what Fix does to the tree"
   affordance is gone for ticket modes; the tooltip is what's left. I
   rewrote the one existing test that opened that dialog to check the
   disabled/title state instead, rather than leaving behaviour the test no
   longer describes correctly.
3. **`sessionRefBad` and `badAsk` gating a session's `blocked` message and
   `canStart`, beyond the two cases the brief names.** The brief states
   `blocked` as two cases (instruction present/absent) and `canStart` as
   three ANDed conditions including "no repository ask is bad" and "the
   reference has not resolved to a reason with no key". I made `blocked`
   surface those two reasons ahead of the instruction-present/absent check,
   so the message under the chips always matches why the button is actually
   disabled.
4. **`setStarting(forKey || what)` instead of `setStarting(forKey)`.** A
   session's `forKey` is always `''`, so the original `setStarting(forKey)`
   would leave `starting` at its initial `''` throughout a session's start
   call, and `busy` (which is `starting !== '' || awaiting !== '' ||
   reading`) would not reflect the in-flight request until (if ever)
   `awaiting` is set. Falling back to `what` (the mode) keeps a non-empty,
   never-collides-with-a-ticket-key sentinel for that window.
5. **New tests that check `onStart`'s arguments directly rather than
   `FakeTransport`'s `calls.startSession`.** The brief's wording for the new
   `NewSession.test.tsx` cases ("calls.startSession has one entry...") reads
   like the transport-level call log, but `NewSession`'s own tests mount it
   with a bare `onStart` mock and never wire a real store/transport
   underneath (that pairing is covered separately in `appStore.test.ts` and
   by the `App.tsx` dispatcher change itself). I kept that file's existing
   idiom — asserting on `onStart`'s call arguments — since introducing a
   `createAppStore`-backed `onStart` just for these cases would be new
   infrastructure the rest of the file doesn't use.

## Files changed

- `desktop/frontend/src/lib/composeIntent.ts`
- `desktop/frontend/src/lib/composeIntent.test.ts`
- `desktop/frontend/src/components/composer/modes.ts`
- `desktop/frontend/src/screens/NewSession.tsx`
- `desktop/frontend/src/screens/NewSession.test.tsx`
- `desktop/frontend/src/App.tsx`

`NewSessionModel.test.tsx` needed no changes (it already compiled and passed
against the new types).

## Self-review

Re-read the full diff of `composeIntent.ts` and `NewSession.tsx` end to end
against the brief's exact strings (placeholder, chip words, access phrases,
error messages) — all copied verbatim. Checked that `IntentMode` and
`SessionMode` (`RunKind`) are now the same literal union, so the cast I
removed from `NewSession.tsx`'s non-session `intentChips` call was genuinely
redundant, not a behaviour change. Confirmed `ACCESS_WORD` in
`composeIntent.ts` and `ACCESS_PHRASE` in `modes.ts` carry the same two
values independently (by design — `composeIntent.ts` doesn't import from
`modes.ts`, so the brief's one-sentence description of the access phrase is
duplicated as two tiny literal maps rather than shared).
