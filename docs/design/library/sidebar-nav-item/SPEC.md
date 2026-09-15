<!--
  Copied from desktop/frontend/src/ui/sidebar-nav-item/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Sidebar nav item

## What it is

One row of the desktop app's left sidebar: a 20px icon, a 16px label, and an
optional count at the inline end, inside a 44-tall pill.

Built. `desktop/frontend/src/ui/sidebar-nav-item/`. It replaces `.nav-item` in
`styles.css`, which was a pill in a horizontal header bar; the header is gone
and `docs/design/03-desktop-app.md` section 5 is where the sidebar comes from.

## Anatomy

- `button.sd-nav-row` — the whole row is the control. `min-block-size:
  var(--sd-nav-row-h)` (44px), `padding-inline: 14px`, `gap: 12px`, radius
  10px, no border. The shell stacks rows at a 2px gap.
- `span.sd-nav-row__icon` — 20 by 20, `currentColor`, `aria-hidden`. Lucide
  outlines at stroke 1.6 in the app; the component takes whatever node it is
  given.
- `span.sd-nav-row__label` — `--sd-text-body` (16px) weight 500, ellipsised
  rather than wrapped.
- `span.sd-nav-row__count` — the pill count at the inline end, `--sd-badge-bg`
  with `--sd-badge-ink`, `--sd-text-micro` mono tabular on a 20px line. Hidden
  when the count is zero.
- `span.sd-nav-row__count-name` — the count's name, read out and never drawn.

## States

| State | What changes |
|---|---|
| rest | No fill. Label and icon `--sd-ink`. |
| hover | Fill `--sd-nav-hover`, label still `--sd-ink` (13.68:1 light on the active fill, higher on hover). |
| active / pressed | Nothing beyond the hover fill. The row navigates; there is nothing to hold down. |
| focus-visible | The app's ring: `2px solid var(--sd-accent)` at `outline-offset: 2px`. |
| disabled | n/a. A screen a person cannot reach is not listed. |
| loading | n/a. A row does not wait for the screen behind it. |
| error | n/a. |
| empty | n/a. A row with no label is not rendered by the shell. |
| selected | `aria-current="page"`, fill `--sd-nav-active`, label `--sd-ink` (13.68:1 light, 12.19:1 dark). No accent, no border. |
| RTL | Icon leads, label follows, count sits at the inline end — all three from logical properties, so the row mirrors with the window and nothing moves by hand. |

## Tokens used

- `--sd-nav-hover` — the hover fill
- `--sd-nav-active` — the current row's fill
- `--sd-ink` — the label, at rest and current
- `--sd-badge-bg` / `--sd-badge-ink` — the count pill
- `--sd-nav-row-h` — the row's height; `--sd-radius-pill` — the count
- `--sd-space-3` — the icon gap
- `--sd-text-body` / `--sd-text-micro` — label and count
- `--sd-font-ui` / `--sd-font-mono` — label and count
- `--sd-dur-1` — the fill change

## Do / Don't

- **Do** let the current row be neutral. **Don't** give it `--sd-accent-soft`,
  which is what `.nav-item[aria-current="page"]` did in `styles.css`: in Sirdar
  the accent means a live run, and a nav row wearing it competes with the one
  card on the board that earned it.
- **Don't** paint a label `--sd-ink-3`. On `--sd-nav-active` it is 4.12:1, the
  one pair `01-tokens.md` records specifically so nobody reaches for it.
- **Do** keep the count's name in the accessible tree. A bare `3` beside
  "Board" is a number with no noun.
- **Don't** hide a zero behind a grey pill. Nothing waiting is best said by
  nothing being drawn.
- **Don't** animate the pill from row to row. Only the fill changes, over
  `--sd-dur-1`; a thumb travelling down five rows outlasts the click it
  answers.

## Accessibility

A `button` per row, `aria-current="page"` on the one the reader is on, which is
what a screen reader announces rather than the fill. The count is drawn from
one span and named by a second that is visually hidden, so it reads as
"3 waiting on Board". Contrast: rest and hover `--sd-ink-2` at **6.79:1** light
and **7.16:1** dark; current `--sd-ink` at **13.68:1** and **12.19:1**; the
count **13.86:1** light and **9.72:1** dark. Hit target 28 tall inside a 32
pitch, with the sidebar's own inline padding taking the clickable box past the
32px floor in width. Reduced motion: the fill change goes to 1ms; nothing else
moves.

## Changelog

### 2026-09-15 (v2 register)
Re-scaled to the reviewed mocks: the pill goes from 28 tall at 32 pitch to
`--sd-nav-row-h` (44px), the icon from 16 to 20, the label from 13px to
`--sd-text-body` (16px), the padding from 8 to 14, the radius from
`--sd-radius-sm` to 10px, and the label rests on `--sd-ink` rather than
`--sd-ink-2`. Forced by the type-ladder re-base in `tokens.css`.

### 2026-09-15
Added. Initial spec from `docs/design/03-desktop-app.md` section 5. Replaces
`styles.css` `.nav-item`: the current-row fill moves from `--accent-soft` to
`--sd-nav-active`, the radius from 3px to `--sd-radius-sm`, and the row gains
an icon slot and the inbound count.
