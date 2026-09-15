# Kanban column

## What it is

One column of the board: a sunk well with a 2px rail across its top in the
lane's hue, a heading with a count, and either cards or a sentence saying why
there are none.

Built. `desktop/frontend/src/ui/kanban-column/`. It replaces `.lane`,
`.lane-head` and `.lane-body` in `styles.css`.

## Anatomy

- `section.sd-lane[data-lane]` — the column. `--sd-sunk` fill,
  `border-block-start: 2px solid var(--sd-lane-hue)`, `min-width: 208px`,
  `flex: 1 1 0`.
- `h2.sd-lane__head` — the name and the count, baseline-aligned.
- `span.sd-lane__count` — mono, tabular, in the lane's hue.
- `div.sd-lane__body` — the scrolling stack of cards, `gap: 4px`.
- `p.sd-lane__empty` — prose for an empty column.

## States

| State | What changes |
|---|---|
| rest | The hue rail and the count in that hue. |
| hover | n/a. A column is not a control. |
| active / pressed | n/a. |
| focus-visible | n/a on the column; the cards inside it are the tab stops. |
| disabled | n/a. |
| loading | n/a. The board's own status line says "loading"; a column does not repeat it. |
| error | n/a. A workspace whose tracker cannot list a queue is reported by the board, not by a column. |
| empty | The body holds one paragraph of prose. Not an icon. |
| selected | n/a. |
| RTL | The column order mirrors with the board's flex row; the rail is `border-block-start` and does not move. |

## Tokens used

- `--sd-sunk` — the well
- `--sd-st-queue` / `--sd-st-live` / `--sd-st-blocked` / `--sd-st-triaged` / `--sd-st-done` / `--sd-st-failed` — the rail, through `--sd-lane-hue`
- `--sd-ink` / `--sd-ink-3` — heading and empty prose
- `--sd-space-1` / `--sd-space-2` — gap and padding
- `--sd-text-body`, `--sd-text-meta` — heading and count

## Do / Don't

- **Do** keep the rail and the heading saying the same thing. The rail is the
  board's legend precisely because it is redundant with a word.
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
board's own heading structure. Contrast: heading `--sd-ink` on `--sd-sunk` is
**15.14:1** light and **15.36:1** dark; the empty prose `--sd-ink-3` is
**4.56:1**, the tightest pair the token file ships and the reason `--sd-sunk`
is no darker. Reduced motion: nothing moves.

## Changelog

### 2026-09-15
Added. Initial spec from `styles.css` `.lane`. `--lane` becomes `--sd-sunk`;
`border-top` becomes `border-block-start`; the accessible name gains the count.
