<!--
  Copied from desktop/frontend/src/ui/kanban-column/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Kanban column

## What it is

One column of the board: a card-row well, one sixth of the row and never
under 160 wide, with a 3px rail across
its top in the lane's hue, a tracked uppercase head with a count, and either
cards or a sentence saying why there are none.

Built. `desktop/frontend/src/ui/kanban-column/`, in the shape of `.col` in
`docs/design/2026-09-15-screens/Board.html`. It replaces `.lane`,
`.lane-head` and `.lane-body` in `styles.css`.

## Anatomy

- `section.sd-lane[data-lane]` — the column. `flex: 1 1 0`, `min-inline-size: 160px`,
  `padding: 8px`, `--sd-radius-sm`, `--sd-card-row` fill, `overflow: hidden`.
- `span.sd-lane__rail` — 3px tall, radius 2px, the lane's hue via
  `--sd-lane-hue`; `aria-hidden`.
- `h2.sd-lane__head` — the name and the count: `--sd-text-micro` at weight
  600, `letter-spacing: .08em`, uppercase, `--sd-ink-3`, `padding-block:
  12px`, `padding-inline: 8px`.
- `span.sd-lane__count` — tabular, in the same ink as the name.
- `div.sd-lane__body` — the scrolling stack of cards, `gap: 8px`.
- `p.sd-lane__empty` — prose for an empty column.

## States

| State | What changes |
|---|---|
| rest | The hue rail over the head; the head and count in `--sd-ink-3`. |
| hover | n/a. A column is not a control. |
| active / pressed | n/a. |
| focus-visible | n/a on the column; the cards inside it are the tab stops. |
| disabled | n/a. |
| loading | n/a. The board's own status line says "loading"; a column does not repeat it. |
| error | n/a. A workspace whose tracker cannot list a queue is reported by the board, not by a column. |
| empty | The body holds one paragraph of prose. Not an icon. |
| selected | n/a. |
| RTL | The column order mirrors with the board's flex row; the rail spans the well and does not move. |

## Tokens used

- `--sd-card-row` — the well
- `--sd-st-queue` / `--sd-st-live` / `--sd-st-blocked` / `--sd-st-triaged` / `--sd-st-done` / `--sd-st-failed` — the rail, through `--sd-lane-hue`
- `--sd-ink-3` — head, count and empty prose
- `--sd-radius-sm` — the well's corner
- `--sd-space-2` / `--sd-space-3` — gap and padding
- `--sd-text-micro`, `--sd-text-meta` — head and empty prose

## Do / Don't

- **Do** keep the rail and the heading saying the same thing. The rail is the
  board's legend precisely because it is redundant with a word.
- **Don't** colour the count. The rail carries the hue; a count in it beside
  a tracked grey head reads as a second badge.
- **Don't** add a legend elsewhere on the board. If the rail needs explaining,
  the hue is wrong.
- **Do** write the empty state as a sentence that says what the emptiness
  means and where the next item comes from.
- **Don't** put an illustration or an icon there. An empty box drawn in grey
  says only that somebody thought about the empty case.
- **Do** let the lanes stack under 720px and let the board scroll as one page.

## Accessibility

`role="region"` by virtue of `section` with an accessible name, and the name
includes the count — `"Blocked (3)"` — so a screen reader user gets the same
summary a sighted reader gets from the rail. The heading is an `h2` inside the
board's own heading structure. Contrast: the head and the empty prose are
`--sd-ink-3` on `--sd-card-row`, **4.73:1** light; 12px uppercase at weight
600 is held to 4.5:1, which it clears. The rail is `aria-hidden`. Reduced
motion: nothing moves.

## Changelog

### 2026-09-15 (responsive)
The column shares its row: `flex: 1 1 0` with `min-inline-size: 160px`
replaces `flex: 0 0 248px`, so six lanes fit the sheet from 1440 up (167
each there, once the sidebar and the page inset are taken) and the board's
row only scrolls sideways below that. Under 1200 the head steps down one
notch (micro minus 1px, tracked at 0.06em).

### 2026-09-15 (Board build)
Reshaped to the Board mock's `.col`: a 248-wide card-row well at radius 8
with 8px padding, a 3px rounded rail as its own element rather than a border,
and the head as tracked uppercase micro text in `--sd-ink-3` with the count
in the same ink. The cards inside sit 8px apart.

### 2026-09-15
Added. Initial spec from `styles.css` `.lane`. `--lane` becomes `--sd-sunk`;
`border-top` becomes `border-block-start`; the accessible name gains the count.
