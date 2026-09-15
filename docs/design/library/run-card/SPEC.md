<!--
  Copied from desktop/frontend/src/ui/run-card/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Run card

## What it is

A run as the board shows it, in the Jira-shaped anatomy of the 2026-09-15
screens round: the ticket's title first, a kind chip under it, and a footer
of the state's glyph and word (with a clock while the run is live or waiting)
and, at the other end, the key in the ledger face beside the provider's mark.
The whole card opens the session.

Built. `desktop/frontend/src/ui/run-card/`. It is drawn by
`components/cards/RunCard.tsx`, which does the clock arithmetic the component
refuses to. The old shape — key in the head, a quoted reason behind a rail, a
footer of badge, clock and cost — is retired; the reason and the cost are the
session's.

## Anatomy

- `button.sd-run-card[data-status][data-live]` — the card: `--sd-sheet` fill,
  1px `--sd-rule` border, radius 6px, `padding-block: 14px`, `padding-inline:
  16px`, a column aligned to the start.
- `span.sd-run-card__title` — `dir="auto"`, `--sd-text-body` (16px) at
  weight 400, `line-height: 1.35`, clamped to two lines. No key above it.
- `span.sd-run-card__kind` — the kind chip (`src/ui/kind-chip`), 10px under
  the title.
- `span.sd-run-card__foot` — flex, space-between, 12px under the chip. On the
  leading side the state glyph (`src/ui/state-glyph`) with its word in the
  state's hue and, on a live or blocked run only, the mono clock. On the
  trailing side `__who`: `__key` (mono, `--sd-text-ledger`, 500,
  `--sd-ink-3`, `dir="ltr"`) and the provider mark at `sm`.

## States

| State | What changes |
|---|---|
| rest | Hairline border, flat. |
| hover | Border to `--sd-rule-strong`. |
| active / pressed | n/a. Opening the run is the feedback. |
| focus-visible | The shell's ring on the whole card. |
| disabled | n/a. Every run can be opened, including a failed one. |
| loading | `queued`: the dashed ring and the word, no clock. |
| error | `failed` and `over_budget`: the cross and the word in `--sd-st-failed`. The reason is in the session, not on the card. |
| empty | A run whose tracker has no title shows the key as its title, rather than an empty first line. |
| selected | n/a today. |
| live | `preparing` and `running` take `border-inline-start: 2px solid var(--sd-accent)`, the play glyph in `--sd-st-live`, and the clock. Nothing else on the board wears the accent. |
| blocked | The question glyph in `--sd-st-blocked` and the clock: how long a person has been asked. |
| RTL | The title lays itself out from its own first letter; the key and the clock stay LTR; the footer's two ends swap from flex, and the accent edge moves to the right with `border-inline-start`. |

## Tokens used

- `--sd-sheet`, `--sd-rule`, `--sd-rule-strong` — the box and its hover
- `--sd-accent` — the live edge
- `--sd-ink` / `--sd-ink-3` — title and key
- `--sd-font-mono`, `--sd-font-ui`
- `--sd-space-2` / `--sd-space-3` / `--sd-space-4` — gaps and inline padding
- `--sd-text-body`, `--sd-text-ledger`
- `--sd-dur-1` — hover

The kind chip, state glyph and provider mark bring their own tokens. The 6px
radius and the 14px block padding are the mock's; one component uses each.

## Do / Don't

- **Do** keep the title first and the key at the foot. A board is read by
  title; the key is looked up second, beside the mark that says who is
  working on it.
- **Don't** put the reason on the card. A blocked card says "blocked" and
  how long; the question itself is the session's banner, where it can be
  answered.
- **Don't** put the cost on the card. Cost belongs to the session topbar and
  the register; on a card it competed with the state for the one number a
  reader takes in.
- **Do** pass a clock only for a live or blocked run, already formatted. The
  component hides it for every other state anyway, so a stale clock cannot
  reach the board.
- **Do** reserve the accent for the live run. It is the product's one accent
  and its meaning is "Sirdar itself, right now".

## Accessibility

Native `button` with `aria-label` of `"<key>: <title>"`, because the visible
title is clamped and the key alone does not say what the run is about; `label`
overrides it for the one card whose click is not an open — the board's queued
ticket, which starts a triage, and says so. State
is carried by the glyph's word, kind by the chip's word, provider by the
mark's `aria-label`; none of the three is colour alone. Contrast: title
`--sd-ink` **17.44:1** on the sheet, key `--sd-ink-3` **5.25:1**, the state
hues **4.84:1** or more (blocked, the tightest) in light. Reduced motion:
only the hover border transitions.

## Changelog

### 2026-09-15 (Board build)
`label` joins the props: an accessible-name override for a card whose click
does something other than open a session. The board draws a queued ticket
with it, named "Start triage of <key>: <title>".

### 2026-09-15 (Jira-shaped)
Rebuilt to the Board mock's `.tcard`: sheet fill on a `--sd-rule` hairline at
radius 6, 14/16 padding; the title first at 16px up to two lines with no key
in the head; a kind chip; a footer of state glyph and word with the clock only
while running or blocked, and the mono key beside the provider mark at the
other end. The key-in-the-head, the priority badge, the quoted reason rail,
the status badge, and the cost are gone; `reason`, `priority`, `elapsed`,
`elapsedTitle`, `cost` and `costTitle` leave the props, `provider` and
`clock` join them.

### 2026-09-15 (restyle)
`elapsed` and `cost` gained optional `elapsedTitle` and `costTitle` tooltips,
so the board keeps the exact timestamp behind the relative clock and the turn
and token counts behind the one cost figure. Both strings are still formatted
by the caller; the card does no arithmetic on a clock.

### 2026-09-15
Added. Initial spec from `styles.css` `.card--run` and
`components/cards/RunCard.tsx`. The live marker changes from
`box-shadow: inset 2px 0 0` to `border-inline-start`, which is what makes it
correct in an Arabic pane; the reason rail changes from `border-left` to
`border-inline-start`; the elapsed clock and cost gain `dir="ltr"`.
