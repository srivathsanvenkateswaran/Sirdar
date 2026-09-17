# Setting row

## What it is

One setting: its name, what it is set to now, and exactly one control to change
it, inside a `--sd-card-row` card of rows that share a heading.

Built. `desktop/frontend/src/ui/setting-row/`. It replaces the
`.workspace-row`, `.settings-toggle` and `.add-workspace-form` blocks in
`components/panels.css`, which were a different shape per section.
`docs/design/03-desktop-app.md` section 7 is where the geometry comes from.

## Anatomy

- `section.sd-setting-card` — the group. `--sd-card-row`, `--sd-radius-md`,
  no border, no shadow.
- `h3.sd-setting-card__heading` — 11.5px weight 600 `--sd-ink-2`.
- `div.sd-setting-row` — 56 min-height, `padding-inline: 16px`. Leading side
  the label stack, trailing side one control.
- `span.sd-setting-row__label` — 13px weight 500 `--sd-ink`.
- `span.sd-setting-row__value` — the current value, 13px `--sd-ink-2`,
  `dir="auto"` because a workspace path or a model name can be either script.
- `p.sd-setting-row__help` — one sentence, 11.5px `--sd-ink-3`, capped at 58ch.
- `div.sd-setting-row__control` — the one control.
- `.sd-setting-button` — the pale per-row button: `--sd-nav-active`,
  `--sd-radius-sm`, 28 tall, label 13px `--sd-ink-2`.
- `.sd-setting-input` — for a control that must show its own value: the input
  style at `--sd-sheet` with a 1px `--sd-rule-strong`.

Rows after the first carry a 1px `--sd-rule-faint` divider inset 16 from each
card edge. The last row has none, which falls out of using `+` rather than a
border on every row.

## States

| State | What changes |
|---|---|
| rest | Label, value, one control. |
| hover | Only the control: the pale button's label goes to `--sd-ink`. The row does not light up — it is not a control. |
| active / pressed | n/a on the row; the control's own. |
| focus-visible | The app's ring on the control. The row is not a tab stop. |
| disabled | A disabled control is 45% opaque with the default cursor. The row's text stays at full strength: what a setting *is* stays readable when it cannot be changed. |
| loading | n/a. A value still being fetched is rendered as the empty value, and the row shows label and control alone. |
| error | n/a on the row. A failure belongs to the page, above the card. |
| empty | A row with no value renders label and control only, with no blank line where the value would be. |
| selected | n/a. |
| RTL | Label stack leads, control trails, divider inset by `margin-inline` — the row mirrors with the modal and nothing moves by hand. `dir="auto"` on the value lets an Arabic path lay itself out. |

## Tokens used

- `--sd-card-row` — the card
- `--sd-rule-faint` — the divider between rows
- `--sd-ink` — label; `--sd-ink-2` — value, heading, control label;
  `--sd-ink-3` — help text
- `--sd-nav-active` — the pale control's fill
- `--sd-sheet` / `--sd-rule-strong` — the input variant
- `--sd-radius-md` / `--sd-radius-sm` — card and controls
- `--sd-space-1` .. `--sd-space-5` — padding, gaps and the card's own margin
- `--sd-dur-1` — the control's fill change

## Do / Don't

- **Do** put one control in a row. **Don't** put two: a picker with a Reset
  beside it makes the reader work out which one the value belongs to. A
  setting that needs two controls is two rows, and the singular `control` prop
  is what keeps that true.
- **Do** print the current value under the label. A settings page whose rows
  say only what they are called has to be opened control by control to be read.
- **Don't** give the row a hover fill. It is not a control, and a whole row
  lighting up promises a click that does nothing.
- **Don't** run the divider the full width of the card. Inset 16 is what makes
  a stack of rows read as one card rather than as a table.
- **Don't** reach for `--sd-rule` here. On `--sd-card-row` it is the heavier
  hairline; `--sd-rule-faint` exists for exactly this line.

## Accessibility

The row is a plain container; every tab stop in it is the control it holds,
which keeps its own role and ring. A control whose label is only the row's —
"Change", "Run doctor" — is given an accessible name that names the setting
too, because a screen reader reading the controls alone hears six buttons
called Change. Contrast: label `--sd-ink` on `--sd-card-row` at **15.70:1**
light and **12.51:1** dark, value `--sd-ink-2` at **7.37 / 6.60**, help
`--sd-ink-3` at **4.73 / 4.90**, and the pale control's label at
**6.42:1** in both themes on `--sd-nav-active`. Hit target 28 tall, inside a
64-tall row. Reduced motion: the control's fill change goes to 1ms.

## Changelog

### 2026-09-17 (density)
Rows go 72 → 56 tall on 8px of block padding, the card's
padding to 16, and every control in one to `--sd-control-h` (32) on 12px of
inline padding.

### 2026-09-15 (v2 register)
Re-scaled to the reviewed mocks: rows go from 64 to 72 tall with 12px block
padding; the card's padding from 12 block-only to 24 all round (12 at the
foot); the card heading from a tracked 11.5px label to `--sd-text-title`
(22px) at 500 in `--sd-ink`; the label to `--sd-text-body` (16px), the value
and help to `--sd-text-meta`. The pale button and the input are
`--sd-control-h` (40) tall, and the button's label is `--sd-ink` at 500.

### 2026-09-15
Added. Initial spec from `docs/design/03-desktop-app.md` section 7. The 86-tall
reference row becomes 64 at Sirdar's 13px base; the divider is
`--sd-rule-faint`, which is the token that exists because three components
needed a hairline `--sd-rule` was too strong for.
