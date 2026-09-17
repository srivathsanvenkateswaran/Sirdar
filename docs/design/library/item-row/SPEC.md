<!--
  Copied from desktop/frontend/src/ui/item-row/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Item row

## What it is

One thing in a list: a tone rail at the leading edge, an icon in a sunk box,
a title over a line of facts, and one control at the end. "Landed today" on
the New session and Board screens is a stack of these; the settings modal's
MCP server list is too.

Built. `desktop/frontend/src/ui/item-row/`, used in the app. New in the
2026-09-15 screens round, taking over from the Board's inbound strip.

## Anatomy

- `div.sd-item[data-tone]` — flex, `gap: 16px`, `padding-block: 16px`,
  `padding-inline: 24px 20px`, `--sd-radius-md`, `--sd-card-row` fill.
- `::before` — the rail: 3px wide, `inset-inline-start: 12px`, `inset-block:
  16px`, radius 2px, `--sd-rule-strong` or the tone's hue.
- `span.sd-item__icon` — 44 by 44 at radius 10px, `--sd-sunk` fill, the
  icon at 20px in `--sd-ink-2`, `aria-hidden`.
- `span.sd-item__body` — the column: `__title` at `--sd-text-body-l` weight
  500, one line ellipsised (two with `wrap`); `__meta` at `--sd-text-meta` in
  `--sd-ink-2`.
- `span.sd-item__action` — one control, `flex: 0 0 auto`.
- `button.sd-item__open` — with `onOpen`, the leading part becomes one
  button carrying `openLabel`; the action stays its own stop.

## States

| State | What changes |
|---|---|
| rest | Neutral rail. |
| hover | n/a on the row; the open button and the action carry their own. |
| active / pressed | n/a. |
| focus-visible | The shell's ring on the open button, or on the action. |
| disabled | n/a. A row that cannot be opened has no `onOpen`. |
| loading | n/a. |
| error | The `failed` tone: a delivery rejected, a run that stopped. |
| empty | n/a. A list with no rows says so in prose; the row itself is never empty. |
| selected | n/a today. |
| live / blocked / done | The rail takes `--sd-st-live`, `--sd-st-blocked` or `--sd-st-done`. The meta line says the same word. |
| RTL | The rail moves to the right edge from `inset-inline-start`; the icon leads and the action trails; a `dir="auto"` title lays itself out from its own first letter. |

## Tokens used

- `--sd-card-row` — the fill; `--sd-sunk` — the icon box
- `--sd-rule-strong` — the neutral rail
- `--sd-st-live`, `--sd-st-blocked`, `--sd-st-done`, `--sd-st-failed` — the rail's tones
- `--sd-ink` / `--sd-ink-2` — title and meta, icon
- `--sd-radius-md`, `--sd-space-3` / `--sd-space-4` / `--sd-space-5`
- `--sd-text-body-l`, `--sd-text-meta`, `--sd-font-ui`

## Do / Don't

- **Do** say the tone's fact in the meta line too. The rail is 3px wide and
  is not the only copy of anything.
- **Don't** put two controls at the end. One action per row; a row that
  needs two is a card.
- **Do** pass `openLabel` with `onOpen`: the visible title is ellipsised and
  the button's name should be the whole of it.
- **Don't** clamp to two lines in a three-column grid. `wrap` is for a
  single-column list where the row has the width.

## Accessibility

A `div`, or a `button` for the leading part when the row opens something,
with `aria-label` from `openLabel`; the action is a separate tab stop either
way, so a row is never two buttons in one. The icon is `aria-hidden`.
Contrast: `--sd-ink` on a card row **15.70:1**, `--sd-ink-2` **7.37:1**
light. No motion.

## Changelog

### 2026-09-17 (density)
The row's padding goes to 12/16, its icon tile 44 → 36 and
the glyph inside it 20 → 16.

### 2026-09-15
Added, from `.item` in `docs/design/2026-09-15-screens/SessionEmpty.html` and
`Board.html`.
