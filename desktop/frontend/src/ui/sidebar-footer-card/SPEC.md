# Sidebar footer card

## What it is

The block pinned to the bottom of the app's sidebar: a title row ("Plan
usage"), one quota chip per budgeted provider, and the one button that starts
a session. While a screen publishes a primary action of its own, that button
steps down to a secondary one: the screen draws its commit action in its page
head, and the footer never draws a second filled copy of it.

Built. `desktop/frontend/src/ui/sidebar-footer-card/`. It takes over from the
right-hand half of `styles.css` `.header` — the workspace `select`, the
`.quota-meter` widget and the New triage button — which moved out of the top
bar when `docs/design/03-desktop-app.md` section 5 replaced it with a sidebar.

## Anatomy

- `div.sd-sidebar-foot` — `--sd-card-row` fill, `--sd-radius-md`, 16px
  padding, 12px gap, no border, no hairline and no shadow. `margin-block-start:
  auto`, which is what pins it.
- `div.sd-sidebar-foot__title` — the title row: `--sd-text-meta` weight 500,
  a chevron at the inline end in `--sd-ink-3`.
- `div.sd-sidebar-foot__switcher` — one row, full width: the workspace name
  and the control that opens the list. The app no longer fills it; the
  switcher is the workspace badge beside the wordmark.
- `div.sd-sidebar-foot__quotas` — a column of quota chips, scrolling past
  32vh so a workspace with six budgeted providers cannot push the action off
  the window.
- `div.sd-sidebar-foot__action` — the New session button, full width: filled
  on a screen with no commit action of its own, secondary on one that has.

## States

| State | What changes |
|---|---|
| rest | All three slots drawn. |
| hover | n/a on the card. The rows inside it carry their own hover. |
| active / pressed | n/a. |
| focus-visible | n/a on the card; the switcher and the button are the tab stops and each keeps the app's ring. |
| disabled | n/a on the card. |
| loading | n/a. Quota chips that have not arrived are simply absent; an empty row is not drawn. |
| error | n/a. A quota that could not be read is reported by the store's toast, not here. |
| empty | A slot given nothing renders nothing: a workspace with no budget shows no quota column, and a screen with no commit action shows no button. The card itself stays, because the switcher is always there. |
| selected | n/a. |
| RTL | Everything is a column, and the two rows inside it use logical padding, so the block mirrors with the sidebar and nothing moves by hand. |

## Tokens used

- `--sd-card-row` — the card fill
- `--sd-rule` — the hairline that separates it from the nav rows
- `--sd-radius-md` — the corners
- `--sd-space-2` — padding and the gap between slots

## Do / Don't

- **Do** keep the card to the three slots. **Don't** add a version string, a
  sync icon or an upgrade row: the reference has all three and
  `03-desktop-app.md` section 13 says why none of them is Sirdar's. The version
  belongs in the settings modal's footer, where 4.5:1 is affordable at 11px.
- **Do** let the quota column scroll. **Don't** let it grow: the primary
  action is the one thing in the sidebar that must always be reachable.
- **Don't** put a second button beside New session. One filled commit action
  per screen is the rule the app shell is built on: the card holds it on a
  screen with no commit action of its own, and steps down on one that has.
- **Do** give the card a `role="group"` with a name. Pinned chrome with no
  name is a run of unrelated controls to a screen reader.

## Accessibility

`role="group"` with an accessible name, so the three slots are announced as one
block rather than as loose controls after the nav list. Nothing in the card
carries text of its own: every ratio is the switcher's, the quota chip's or the
button's, each measured in its own spec. The card fill is **1.11:1** light and
**1.12:1** dark off `--sd-sheet`, and the `--sd-ink` and `--sd-ink-2` inside it
clear **15.70 / 12.51** and **7.37 / 6.60**. Reduced motion: nothing here
moves.

## Changelog

### 2026-09-15 (screens)
The action slot no longer draws a screen's published primary. A screen with a
commit action draws it in its own page head (the Eval screen's Run suite is
the first), and New session steps down to a secondary button while it is up,
so the window holds one filled button at a time.

### 2026-09-15 (v2 register)
Gains a `title` slot ("Plan usage" in the app, with a chevron), drawn above the
quota chips. Padding goes from 8 to 16, the gap from 8 to 12, the quota gap to
6, and the hairline above the card is gone: the card-row fill on the shell is
the separation, as it is for the sheet. The app's workspace switcher moved up
beside the wordmark as a badge; the `switcher` slot stays.

### 2026-09-15
Added. Initial spec from `docs/design/03-desktop-app.md` section 5. The three
slots are the header's workspace `select`, `.quota-meter` and New triage
button, moved out of `styles.css` `.header` and given a card.
