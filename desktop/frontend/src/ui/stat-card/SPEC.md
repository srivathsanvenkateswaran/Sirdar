# Stat card

## What it is

One big figure with a small grey label over it and one line of context under
it. The Register's three — runs this week, spent, confirmed — sit beside the
heatmap.

Built. `desktop/frontend/src/ui/stat-card/`, used in the app on the
Register. New in the 2026-09-15 screens round.

## Anatomy

- `div.sd-stat` — a column, `gap: 4px`, `padding-block: 16px`,
  `padding-inline: 24px`, `--sd-radius-md`, `--sd-card-row` fill.
- `span.sd-stat__label` — `--sd-text-body` weight 500 in `--sd-ink-3`.
- `span.sd-stat__value` — 36px weight 500, tabular, `line-height: 1`,
  `dir="ltr"`, with the exact number as its `title` when the shown one is
  rounded.
- `span.sd-stat__detail` — `--sd-text-meta` in `--sd-ink-2`, one line, no
  wrapping.

## States

| State | What changes |
|---|---|
| rest | As above. |
| hover | n/a. Not a control. The tooltip on the figure is the one hover behaviour. |
| active / pressed | n/a. |
| focus-visible | n/a. Not focusable. |
| disabled | n/a. |
| loading | n/a. The screen shows the card once it has the figure; a card with a spinner in it is a lie about the width. |
| error | n/a. |
| empty | The screen passes "0" or "—"; the card draws what it is given. |
| selected | n/a. |
| RTL | The label and detail run in the reader's direction; the figure stays `dir="ltr"`, since a number is read left to right in either script. |

## Tokens used

- `--sd-card-row` — the fill
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — figure, detail, label
- `--sd-radius-md`, `--sd-space-1` / `--sd-space-4` / `--sd-space-5`
- `--sd-text-body`, `--sd-text-meta`, `--sd-font-ui`

The 36px figure is not on the type ladder. It is the reference app's own
size for a headline number, measured off the Register mock, and it is used by
one component, which by `01-tokens.md` section 4 keeps it here.

## Do / Don't

- **Do** format the figure on the screen and hand it over as a string, with
  the exact value in `valueTitle` when it is rounded.
- **Don't** let the card compute. A card that rounded its own number would
  disagree with the table under it.
- **Do** keep the detail to one line. It is the denominator or the
  comparison, not a paragraph.
- **Don't** stack more than three. The Register has three because it has
  three facts worth a headline.

## Accessibility

Plain spans; the label precedes the figure in DOM order so a screen reader
reads "Runs this week, 38". Contrast: `--sd-ink` on a card row **15.70:1**,
`--sd-ink-2` **7.37:1**, `--sd-ink-3` **4.73:1** light. No motion.

## Changelog

### 2026-09-15
Added, from `.figs .card` in `docs/design/2026-09-15-screens/Register.html`.
