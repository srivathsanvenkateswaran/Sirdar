# Data table

## What it is

A table of facts — the register, the eval sheet, a list of deliveries — with
tabular figures, hairline rows, sortable headers, a sticky header and its own
horizontal scroll container.

Built. `desktop/frontend/src/ui/data-table/`. It replaces `.register-table`,
`.eval-table` and `.sort-btn` in `components/panels.css`.

## Anatomy

- `div.sd-table__scroll[role="group"][tabindex="0"]` — the scroll container,
  named by the caption so a keyboard reader knows what they are scrolling.
- `table.sd-table` > `caption.sd-table__caption` — what the table lists.
- `thead th` — sticky at `inset-block-start: 0`, `--sd-surface` ground.
- `button.sd-table__sort` + `span.sd-table__arrow` — a sortable header.
- `td[data-numeric]` — mono, tabular, `text-align: end`, contents wrapped
  `dir="ltr"`.
- `th[data-col]` / `td[data-col]` — every cell carries its column's `id`, so a
  screen's own stylesheet can narrow or drop one column at a width
  (`[data-col='notes'] { display: none }`). The table neither reads the
  attribute nor knows the widths; what is hidden stays in the rows and in any
  export.

## States

| State | What changes |
|---|---|
| rest | Hairline rows, header in `--sd-ink-2`. |
| hover | Sortable header label to `--sd-ink`. |
| active / pressed | n/a. |
| focus-visible | The shell's ring on a header button and on the scroll container. |
| disabled | n/a. An unsortable column simply has no button. |
| loading | n/a. The screen above the table says so. |
| error | n/a. |
| empty | The table is not rendered at all; a bordered box of prose takes its place, saying what is missing and where it would come from. |
| selected | n/a today. |
| sorted | `aria-sort` on the header, `ascending` or `descending`, with an arrow in the accent. Every other sortable header is `aria-sort="none"`. |
| RTL | Cells align to `start`/`end`, so a numeric column sits against the reader's trailing edge; numbers themselves stay LTR. |

## Tokens used

- `--sd-surface` — table ground and the sticky header
- `--sd-rule` — every row line and the container border
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — cells, header, empty prose
- `--sd-accent` — the sort arrow
- `--sd-radius-md` — the container
- `--sd-space-1` / `--sd-space-3` / `--sd-space-4` — cell padding and the empty box
- `--sd-text-body`, `--sd-text-meta`
- `--sd-font-mono` — numeric cells
- `--sd-measure-prose` — the empty prose

## Do / Don't

- **Do** put the table in its own scroll container. A wide register scrolls
  inside its panel; the page body never scrolls sideways underneath the board.
- **Don't** let a numeric column inherit the prose font. A column of costs that
  does not line up on the decimal cannot be compared, which is the only reason
  it is a column.
- **Do** give the table a caption. It is read before the first row and it is
  what names the scroll container.
- **Don't** replace the empty state with a zero-row table. A grid of headings
  with nothing under it says less than one sentence does.
- **Do** keep `aria-sort` in step with the arrow. The arrow alone is invisible
  to a screen reader, and `aria-sort` alone is invisible to everyone else.

## Accessibility

Native `table` with a `caption`, `th[scope="col"]` headers, and `aria-sort` on
the sorted column. Sorting is a real `button` inside the header, so it is one
tab stop and works from Enter and Space. The scroll container is focusable
because a scrollable region that cannot be reached by keyboard cannot be
scrolled by keyboard. Contrast: cells **17.44:1**, headers `--sd-ink-2`
**8.19:1**, empty prose `--sd-ink-3` **5.25:1** on surface. Reduced motion:
nothing moves.

## Changelog

### 2026-09-16 (assignee round, responsive)
Every header and body cell now carries `data-col` with its column's id. It is
the hook a screen needs to drop a column at a narrow width — the register's
Assignee is the first, and under 1200 the register also drops Confidence,
Verdict and Notes — without the table taking on a media query of its own.

### 2026-09-15 (v2 register)
Re-scaled to the reviewed mocks: 48-tall rows with 16px cell padding and
`--sd-rule-faint` hairlines between them, a 12px-padded header in
`--sd-ink-3` at `--sd-text-meta`, body cells at `--sd-text-body` (16px) and
numeric cells in the ledger face at `--sd-text-ledger`. Cells no longer wrap;
the detail row still does.

### 2026-09-15 (restyle)
Gained an optional `detail` render prop, drawn as a full-width row under the
row it belongs to. Added when the Eval screen was pointed at this component:
its per-key rows carry the checks that did not hold and the reason a run
stopped, and folding that prose into a cell would have moved every column
below it out of alignment.

### 2026-09-15
Added. Initial spec from `components/panels.css` `.register-table` and
`.eval-table`. The literal fallbacks (`var(--border, #e4e6eb)`,
`var(--muted, #6b7280)`) are gone — they are what painted the dark theme in
light-theme colours; the header becomes sticky; the scroll container and the
empty state are new.
