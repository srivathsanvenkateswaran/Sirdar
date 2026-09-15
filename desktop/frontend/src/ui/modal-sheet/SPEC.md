# Modal sheet with secondary nav

## What it is

The large Dialog: a centred sheet with a secondary nav column down its leading
edge, a serif page heading, and a footer for the one action that commits.
Settings is the only screen that gets one.

Built. `desktop/frontend/src/ui/modal-sheet/`. It replaces the Settings
*screen* — `screens/Settings.tsx` inside `.panel.settings` — with a modal over
whatever screen the reader was on. `docs/design/03-desktop-app.md` section 6 is
where the measurements come from.

## Anatomy

- `div.sd-modal-scrim` — `--sd-scrim`, `position: fixed`, `inset: 0`. A click
  on the scrim itself closes.
- `div.sd-modal` — `min(900px, 92vw)` by `min(640px, 88vh)`, centred both
  axes, `--sd-radius-md`, `--sd-sheet` fill, 1px `--sd-rule`, **no shadow**.
- `nav.sd-modal__nav` — 200 wide, `--sd-card-row`, flush to the leading edge,
  full height, carrying the modal's leading corners by way of the sheet's
  `overflow: hidden`.
  - `p.sd-modal__nav-label` — a group heading, 11px `--sd-ink-3`,
    letter-spacing 0.06em.
  - `button.sd-modal__nav-row` — 28-tall pill at 32 pitch, inset 8 each side.
  - `div.sd-modal__nav-foot` — the version and the last sync, 11px
    `--sd-ink-3`, **4.60:1** on the card.
- `div.sd-modal__panel` — `--sd-sheet`, 32 padding.
  - `h2.sd-modal__title` — `--sd-font-display` at `--sd-text-display-m`. The
    only serif in the app besides the note pane.
  - `div.sd-modal__body` — the scrolling stack of setting cards.
  - `div.sd-modal__footer` — actions, trailing-aligned, above a `--sd-rule`
    hairline.

## States

| State | What changes |
|---|---|
| rest | Open, scrim down, focus inside. |
| hover | Nav rows take `--sd-nav-hover`. The sheet itself does not respond. |
| active / pressed | n/a on the sheet. |
| focus-visible | Every control keeps the app's ring; focus moves to the first control when the modal opens and cannot leave until it closes. |
| disabled | n/a on the sheet. The footer's commit button is passed in disabled until something has changed. |
| loading | n/a. A page that is still fetching says so in its own body; the sheet does not blank. |
| error | n/a on the sheet. |
| empty | n/a. A modal with no nav groups is not rendered by the app. |
| selected | One nav row carries `aria-current="page"` with `--sd-nav-active` and `--sd-ink` at weight 500. |
| RTL | The nav column moves to the right of the sheet, and the footer's actions to its left, both from `flex` plus logical padding. The leading corners follow the nav because they are the sheet's own, not the column's. |

## Tokens used

- `--sd-scrim` — behind the sheet
- `--sd-sheet` — the sheet and the content panel
- `--sd-card-row` — the secondary nav column
- `--sd-rule` — the sheet border and the footer hairline
- `--sd-rule-faint` — the hairline between nav groups
- `--sd-nav-hover` / `--sd-nav-active` — the nav rows
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — current row, rest row, group label and the footer line
- `--sd-font-display`, `--sd-text-display-m` — the page heading
- `--sd-radius-md`, `--sd-radius-sm` — the sheet and the nav pills
- `--sd-dur-2`, `--sd-dur-3`, `--sd-ease` — scrim fade and sheet entrance

## Do / Don't

- **Do** leave the board painted behind the scrim. **Don't** blur it: the
  board can have a live run on it, and a backdrop filter repaints all of it
  every frame.
- **Don't** put a shadow under the sheet. The scrim is the elevation, and the
  reference this is read from has none either.
- **Do** return focus to the nav row that opened the modal. Dropping focus on
  the document body is the failure a keyboard reader notices first.
- **Don't** give a second screen one of these. Settings is the only place in
  Sirdar with enough pages to need a nav inside a dialog; New triage and Fix
  are the small Dialog.
- **Do** keep the version string readable. The reference prints it at 2.11:1
  and that is the one thing in the capture Sirdar deliberately does the
  opposite of.

## Accessibility

`role="dialog"` with `aria-modal="true"`, labelled by the page heading. Escape
closes, Tab cycles inside the sheet in both directions, and focus returns to
the opener on close. The secondary nav is a `nav` with its own name and its
rows are buttons carrying `aria-current="page"`; ArrowDown and ArrowUp move
within it and wrap, and the row that gains the selection gains focus with it.
Contrast: heading and current row `--sd-ink` at **15.70:1** light and
**12.51:1** dark on the card, rest rows `--sd-ink-2` at **7.37 / 6.60**, the
group label and the footer line `--sd-ink-3` at **4.73 / 4.90**. Reduced
motion: the scrim and the sheet both cross-fade at 1ms with no scale, which is
the answer `03-desktop-app.md` section 11 states.

## Changelog

### 2026-09-15
Added. Initial spec from `docs/design/03-desktop-app.md` section 6. Extends
Dialog (12) and shares its `focusable` helper rather than restating the
selector. Settings stops being a screen in `screens/Settings.tsx` and becomes
this modal over the screen behind it.
