# Group label

## What it is

A small tracked label over a group of things — "Landed today", "Workspace",
"This app" — with a dashed rule running out to the edge. It names a group; it
does not start a section, which is what the rule is there to say.

Built. `desktop/frontend/src/ui/group-label/`, used in the app on the New
session and Board screens ("Landed today") and in the settings modal's
secondary nav (the group headings).

## Anatomy

- `p.sd-group-label[data-rule]` (or `h2` / `h3` via `as`) — flex,
  `gap: 12px`, `--sd-text-micro` at weight 600, `letter-spacing: .08em`,
  uppercase, `--sd-ink-2`.
- `::after` — the dashed rule, `flex: 1`, 1px dashed `--sd-rule-strong`,
  drawn only with `rule` on.

## States

| State | What changes |
|---|---|
| rest | The label and its rule. |
| hover | n/a. Not a control. |
| active / pressed | n/a. |
| focus-visible | n/a. Not focusable. |
| disabled | n/a. |
| loading | n/a. |
| error | n/a. |
| empty | n/a. A label with no words is not rendered. |
| selected | n/a. |
| RTL | The rule runs out to the other edge, from flex; nothing is positioned. |

## Tokens used

- `--sd-ink-2` — the words (7.37:1 on a card row, 8.19:1 on the sheet, light)
- `--sd-rule-strong` — the dashed rule
- `--sd-space-3` — the gap
- `--sd-text-micro`, `--sd-font-ui`

## Do / Don't

- **Do** use it over a list of items or a group of nav rows.
- **Don't** use it as a screen or card title. Those are the page head and the
  card's own heading, in sentence case; a tracked label above a value is the
  commonest piece of template chrome there is, and the reference's own
  register keeps it to group names.
- **Do** drop the rule inside a narrow column, where twelve pixels of dashes
  say nothing.

## Accessibility

A `p` by default; `as="h2"` or `h3` when the label heads a section that a
screen reader should find by heading. Contrast: `--sd-ink-2` on the sheet is
**8.19:1** light and on a card row **7.37:1**; 12px uppercase at weight 600
is held to 4.5:1, which it clears. No motion.

## Changelog

### 2026-09-15
Added, from `.group-label` in
`docs/design/2026-09-15-screens/SessionEmpty.html` and `Settings.html`.
