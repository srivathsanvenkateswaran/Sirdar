<!--
  Copied from desktop/frontend/src/ui/heatmap/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Heatmap

## What it is

Runs per day, above the Register table: weekday rows by week columns, twelve
weeks by default, five steps of the accent, and a legend that prints the bucket
boundaries as numbers.

Built. `desktop/frontend/src/ui/heatmap/`. New — nothing in the app drew one
before. `docs/design/03-desktop-app.md` section 10 is where it comes from, and
section 10 is also where the reference's streak counter, flame and next-month
chevron are refused.

## Anatomy

- `section.sd-heatmap[data-size]` — the block, named for a screen reader,
  pinned `direction: ltr`. `default` or `compact`.
- `div.sd-heatmap__body` — the weekday column beside the scroll container.
- `div.sd-heatmap__weekdays[aria-hidden]` — seven letters, M to S, on the
  cells' own 14px rows, in `--sd-ink-3` at `--sd-text-ledger`.
- `div.sd-heatmap__scroll` — the horizontal scroll container, focusable so a
  keyboard reader can scroll it. It holds the month row and the grid, so
  they scroll together.
- `div.sd-heatmap__months[aria-hidden]` — a 16px row on the grid's columns;
  a three-letter month name sits on each column whose Monday starts a new
  month. The first column is never labelled.
- `div.sd-heatmap__grid` — `grid-auto-flow: column`, seven 14px rows, 6px
  gap, one column per week, Monday at the top.
- `button.sd-heatmap__cell[data-heat]` — 14 by 14, `--sd-radius-xs`, one of
  the five steps. Every cell is a button.
- `p.sd-heatmap__legend[data-size]` — the word "Runs a day", then five
  swatches each labelled with its bucket: `0`, `1`, `2-4`, `5-9`, `10+`.
  Drawn under the grid unless `legend={false}`; the exported `HeatmapLegend`
  is the same element on its own, for a screen that places it beside the
  grid's title.

Compact (`size="compact"`): 10px cells at a 2px gap, a 14px month row, the
weekday letters and month names at `--sd-text-micro`, the legend at the same
size with 10px swatches. Twenty-six weeks come to 310px across and 98px tall
with the month row; the Register uses it so the grid shares a row with the
stat strip and the table under both gets the height.

## States

| State | What changes |
|---|---|
| rest | The cell's step. |
| hover | The tooltip, which carries the same string as the accessible name. The cell does not change colour: a hover tint on a sequential ramp reads as a different bucket. |
| active / pressed | n/a beyond the browser's own. Clicking a cell filters the table below. |
| focus-visible | The app's ring on the cell, and on the scroll container. |
| disabled | n/a. A day with no runs is still a day. |
| loading | n/a. The grid is drawn from the rows the Register already has; there is no second request to wait for. |
| error | n/a. |
| empty | A workspace with no runs draws a full grid of `--sd-heat-0`, which is the true answer. It is not replaced by a sentence. |
| selected | n/a today. The cell filters the table rather than staying lit. |
| RTL | The grid stays left to right. It is a calendar of numbers, and mirroring it puts last week on the right of this week; the legend and the heading around it follow the window. |

## Tokens used

- `--sd-heat-0` .. `--sd-heat-4` — the five steps
- `--sd-radius-xs` — the cell corner
- `--sd-space-1` / `--sd-space-2` — the 4px gap and the legend's gaps
- `--sd-ink-3` — the legend
- `--sd-font-mono` / `--sd-text-micro` — the bucket numbers
- `--sd-font-ui` — the legend's one word

## Do / Don't

- **Do** print the bucket boundaries. **Don't** ship "More" and "Less": a
  reader cannot tell 3 runs from 8 from two shades of the same hue, and the
  numbers are what the colour is standing in for.
- **Do** give every cell an accessible name that reads as a sentence. **Don't**
  put the count in a `title` alone: a tooltip is not in the accessible tree on
  a touch device and is not read out on any device.
- **Don't** count streaks. A support engineer's good week is a week with few
  runs, and consecutive days of work is a habit-app idea that would be a lie on
  this screen.
- **Don't** add a second series in a second hue. The reference's grid mixes
  warm grey cells with teal ones and a single frame does not say what separates
  them; Sirdar's grid is one ramp.
- **Don't** animate the cells in. They paint at their final value, which is
  what `03-desktop-app.md` section 11 states.

## Accessibility

Every cell is a `button` with an accessible name like `Sunday 14 September, 6
runs`, so the grid is navigable and readable without colour. The grid sits in a
focusable `role="group"` with the same name as the section, so a keyboard
reader can scroll the twelve weeks without tabbing through eighty-four cells.
The five steps are a sequential ramp, not a set of categories: consecutive
pairs separate by **1.30 / 1.51 / 1.98 / 1.91** in light and **1.26 / 1.52 /
1.83 / 1.90** in dark, and step 0 separates from the sheet by **1.18:1** and
**1.15:1**. That is deliberately not held to 3:1 — the floor applies to text,
and the count is carried by the name, the tooltip and the legend. The legend's
own text is `--sd-ink-3`, **4.98:1** on the paper and **4.73:1** on a card.
Reduced motion: nothing here moves in any state.

## Changelog

### 2026-09-17 (register compact)
Gained `size="compact"` — 10px cells at a 2px gap, the month row at 14px,
letters and legend at the micro size — and `legend={false}` with an exported
`HeatmapLegend`, so the Register can put the legend on the title row and
give the table the height the grid was taking. The default size is unchanged.

### 2026-09-15 (register screen)
Weeks now run Monday to Sunday, as the Register mock draws them and ISO
8601 counts them (the grid ended on a Saturday before). A month row over the
grid names each column whose Monday starts a new month, and a weekday
column letters the rows; both are `aria-hidden`, since every cell already
speaks its weekday and date. `monthLabels` is exported so the screen's tests
can pin which columns are named.

### 2026-09-15 (v2 register)
Re-scaled to the reviewed mocks: 14px cells at a 6px gap (were 12 and 4),
26 weeks by default (were 12), the legend in the ledger face at
`--sd-text-ledger` with a 16px gap.

### 2026-09-15
Added. Initial spec from `docs/design/03-desktop-app.md` section 10. The
reference's 16px cell at 8px gap becomes 12px at 4px for Sirdar's 13px base,
and its four-step teal becomes five steps of Sirdar's own accent.
