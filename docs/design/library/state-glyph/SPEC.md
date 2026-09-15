<!--
  Copied from desktop/frontend/src/ui/state-glyph/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# State glyph

## What it is

A run's state as a 16px outline glyph and its word, both in the state's hue,
with an optional ledger clock beside them. It is the state line in a run
card's footer and in the session topbar, where the status badge's pill would
be too heavy.

Built. `desktop/frontend/src/ui/state-glyph/`, used in the app. New in the
2026-09-15 screens round. The status badge (`src/ui/status-badge/`) stays for
the places that want a pill — the register and the session topbar's badge —
and the two share their hues.

## Anatomy

- `span.sd-state[data-state][data-glyph]` — inline flex, `gap: 6px`,
  `--sd-text-meta`, in the state's hue.
- `svg` — a 16px, 1.5-stroke drawing on a 24-unit box, `aria-hidden`. Five
  drawings: a dashed ring (queued), a play mark in a ring (preparing,
  running), a question mark in a ring (blocked), a check in a ring (completed,
  done), a cross in a ring (failed, over budget).
- `span.sd-state__word` — the word, lowercase, always present.
- `span.sd-state__clock` — optional, `--sd-font-mono` at `--sd-text-ledger`,
  tabular, `dir="ltr"`. Already formatted by the caller.

## States

| State | What changes |
|---|---|
| rest | `queued` in `--sd-st-queue`; `preparing` and `running` in `--sd-st-live`; `blocked` in `--sd-st-blocked`; `completed` in `--sd-st-triaged`; `done` in `--sd-st-done`; `failed` and `over_budget` in `--sd-st-failed`. |
| hover | n/a. Not a control. |
| active / pressed | n/a. |
| focus-visible | n/a. Not focusable. |
| disabled | n/a. |
| loading | n/a. `queued` and `preparing` are states, not a loading pose. |
| error | `failed` and `over_budget`, with the cross. |
| empty | n/a. A run has a state. |
| selected | n/a. |
| RTL | The glyph leads and the word follows, from logical flow; the clock is `dir="ltr"` and keeps its digits in order. |

## Tokens used

- `--sd-st-queue`, `--sd-st-live`, `--sd-st-blocked`, `--sd-st-triaged`, `--sd-st-done`, `--sd-st-failed` — the hues
- `--sd-font-ui`, `--sd-text-meta` — the word
- `--sd-font-mono`, `--sd-text-ledger` — the clock

## Do / Don't

- **Do** hand it a clock only when the clock means something: a live run's
  elapsed time, a blocked run's wait. A clock on a completed card is last
  week's news.
- **Don't** drop the word to save space. The glyphs are five drawings for
  eight states, and colour alone is never status in this product.
- **Do** use `done` for a key whose RCA is written; it is the board's word,
  not the CLI's, and the CLI's `completed` keeps the triaged hue.
- **Don't** animate the running glyph. The live-run pulse on the board is the
  app's one loop, and a second one beside it is noise.

## Accessibility

The word is the content; no role is needed and the SVG is hidden. Contrast:
each hue on the sheet clears **4.84:1** or more in light (`--sd-st-blocked`
is the tightest) and **6.29:1** or more in dark, as the tokens test records.
No motion.

## Changelog

### 2026-09-15
Added, from `.tcard .st` and its glyphs in
`docs/design/2026-09-15-screens/Board.html`.
