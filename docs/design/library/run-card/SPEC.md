<!--
  Copied from desktop/frontend/src/ui/run-card/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Run card

## What it is

A run as the board shows it: a key in the ledger face, the ticket's title, the
reason it stopped if it stopped, and a footer of status, elapsed clock and cost.
The whole card opens Run detail.

Built. `desktop/frontend/src/ui/run-card/`. It replaces `.card--run` and the
`RunCard` component in `components/cards/`.

## Anatomy

- `button.sd-run-card[data-status][data-live]` — the card, `--sd-radius-md`.
- `span.sd-run-card__top` — `__key` (mono, 600, `dir="ltr"`), `__kind` (mono,
  micro), and the priority badge.
- `span.sd-run-card__title` — `dir="auto"`, clamped to two lines.
- `span.sd-run-card__reason` — `dir="auto"`, behind a 2px
  `border-inline-start` rail, shown only for a stopped run.
- `span.sd-run-card__foot` — status badge, `__elapsed`, `__cost`, both mono,
  tabular and `dir="ltr"`.

## States

| State | What changes |
|---|---|
| rest | Hairline border, no edge. |
| hover | Border to `--sd-accent`. |
| active / pressed | n/a. Opening the run is the feedback. |
| focus-visible | The shell's ring on the whole card. |
| disabled | n/a. Every run can be opened, including a failed one. |
| loading | `queued` — the card shows its key and nothing else it does not yet know. |
| error | `failed` and `over_budget` show the reason behind the rail. |
| empty | A run whose tracker has no title shows the key alone, rather than an empty heading line. |
| selected | n/a today. |
| live | `preparing` and `running` take `border-inline-start: 2px solid var(--sd-accent)` and the elapsed clock goes to the accent. Nothing else on the board wears the accent. |
| RTL | Title and reason lay themselves out from their own first letter; the key, clock and cost stay LTR; the reason rail moves to the right. |

## Tokens used

- `--sd-surface`, `--sd-rule-strong`, `--sd-radius-md` — the box
- `--sd-accent` — the live edge, the live clock, the hover border
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — key, title and reason, kind and cost
- `--sd-font-mono`, `--sd-font-ui`
- `--sd-space-1` / `--sd-space-2` — gap and padding
- `--sd-text-body`, `--sd-text-meta`, `--sd-text-micro`
- `--sd-dur-1` — hover

## Do / Don't

- **Do** keep the key monospace and the title not. An identifier is matched
  character by character; a title is read.
- **Don't** let the title push the footer off the card. Two lines, clamped.
- **Do** show the reason only for a run that stopped on something. A reason on
  a running card is last week's news.
- **Don't** mirror the key, the clock or the cost in an Arabic pane. A run id
  read right to left is a different string.
- **Do** reserve the accent for the live run. It is the product's one accent
  and its meaning is "Sirdar itself, right now".

## Accessibility

Native `button` with `aria-label` of `"<key>: <title>"`, because the visible
title is clamped and the key alone does not say what the run is about. Status
is carried by the badge's word, not by the edge colour. Contrast: key
**17.44:1** on surface, title and reason `--sd-ink-2` **8.19:1**, cost
`--sd-ink-3` **5.25:1**, live clock `--sd-accent` **8.82:1** (light; all higher
in dark). Reduced motion: only the hover border transitions.

## Changelog

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
