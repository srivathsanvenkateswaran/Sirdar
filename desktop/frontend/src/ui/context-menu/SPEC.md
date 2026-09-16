# Context menu

## What it is

A menu of actions on one thing: what a right-click on a session row opens.
Pin, Un-settle, Snooze ▸, Rename, Mark unread, Copy ▸, Open ▸, Workspace
settings, Archive, Delete…

Built. `desktop/frontend/src/ui/context-menu/`, used by
`components/shell/SessionsList.tsx`. New in the 2026-09-16 session-menu
round; before it the sidebar's rows did one thing, open the run.

## Anatomy

- `div.sd-menu[role="menu"][aria-label]` — the panel, `position: fixed`,
  pinned by `lib/anchor` below its anchor when it fits and above when it does
  not, held inside the window and no taller than the room it has (the hook
  writes `top`, `left` and `max-height`). `min-inline-size: 220px`,
  `max-inline-size: 320px`, `padding: 4px`, 1px `--sd-rule-strong`,
  `--sd-radius-md`, `--sd-surface`, `--sd-shadow-soft`, `--sd-text-meta`.
  Rows stack at a 1px gap.
- `button.sd-menu__item[role="menuitem"]` — one row, 32 tall, `--sd-radius-sm`,
  `padding-inline: 12px 8px`, `tabindex="-1"` (the panel moves focus itself).
  `data-tone="danger"` paints the label in `--sd-st-failed`; the label's own
  word still says what it does.
- `span.sd-menu__label` — the words, one line, ellipsised.
- `span.sd-menu__detail` — optional, at the inline end: a shortcut or a
  value, `--sd-text-micro` mono in `--sd-ink-3`, `dir="ltr"`.
- `svg.sd-menu__chevron` — on a submenu's row, lucide `chevron-right` at 14px
  in `--sd-ink-3`; mirrored in an Arabic pane.
- `div.sd-menu__separator[role="separator"]` — a `--sd-rule` hairline inset 8.
- `div.sd-menu__sub` — holds an open submenu: the same panel again, hung
  beside its row (`placeBeside`), `aria-controls` from the row that opened
  it.

The anchor is a `RefObject` of anything with a `getBoundingClientRect`: the
dots button at a row's end, or `pointAnchor(x, y)` for the pointer's position.

## States

| State | What changes |
|---|---|
| rest | Rows in `--sd-ink` on the surface; the first pickable row has focus. |
| hover | The row under the pointer takes `--sd-nav-hover`. The pointer on a submenu's row opens the submenu; on any other row it closes one a sibling opened. |
| active / pressed | Nothing beyond hover. A row picks on release. |
| focus-visible | The row carries the shell's ring at `outline-offset: -2px`, inside the panel's padding, plus the hover fill. |
| disabled | `aria-disabled="true"`: `--sd-ink-3`, no hover fill, no pointer, still focusable so a reader learns the action exists. Never picked. |
| loading | n/a. A menu does not wait; the thing it starts reports. |
| error | n/a. |
| empty | n/a. A menu with no rows is not opened. |
| selected | `aria-expanded="true"` on a submenu's row while its submenu is open: the hover fill held. |
| closed | Renders nothing, and focus returns to whatever had it. |
| inline | `data-inline="true"`: in the flow, no focus taken, no outside-press closing it, a submenu drawn under its row indented 16. The gallery's specimen. |
| RTL | The chevron mirrors, the detail keeps `dir="ltr"`, the submenu opens to the leading side (the row's inline end), and ArrowLeft/ArrowRight keep their meaning: Right opens, Left comes back. |

## Tokens used

- `--sd-surface`, `--sd-rule-strong`, `--sd-radius-md`, `--sd-shadow-soft` — the panel
- `--sd-nav-hover` — the row under the pointer or focus
- `--sd-ink` / `--sd-ink-3` — labels; detail, chevron and disabled rows
- `--sd-st-failed` — the danger tone
- `--sd-rule` — the separator
- `--sd-radius-sm` — a row
- `--sd-space-1` / `--sd-space-2` / `--sd-space-3` / `--sd-space-4`
- `--sd-text-meta` / `--sd-text-micro`, `--sd-font-ui` / `--sd-font-mono`
- `--sd-accent` — the focus ring, from the shell's `:focus-visible`

## Do / Don't

- **Do** open it on the row's right-click, its double-click, the dots button
  at its end, and the context-menu key or Shift+F10 with the row focused.
  Four ways in, one menu.
- **Don't** put the only way to reach an action in it. A menu is a shortcut
  to things the screens also offer; Delete is the one exception here and it
  asks first.
- **Do** keep the danger row last, after a separator, and give it an ellipsis:
  "Delete…" asks, it does not act.
- **Don't** nest a submenu in a submenu. One level beside is as far as a
  pointer can follow.
- **Do** close on any pick. A menu that stays open after an action reads as
  though the action did not happen.

## Accessibility

`role="menu"` named by `aria-label`, rows `role="menuitem"` with roving focus:
the first pickable row takes focus on open, ArrowUp/ArrowDown move and wrap,
Home/End jump, Enter and Space pick, a printable key jumps to the next row
whose label starts with it, ArrowRight opens a submenu and focuses its first
row, ArrowLeft and Escape on a submenu come back to the row that opened it,
Escape on the top level closes, Tab closes rather than leaving focus inside a
menu the window has moved on from. A press outside any level closes it. On
close, focus returns to the element that had it when the menu opened.
Contrast: `--sd-ink` on `--sd-surface` **17.44:1** light, **13.44:1** dark;
`--sd-ink-3` on the surface **5.25:1**; `--sd-st-failed` on the surface
**5.60:1** light. No motion.

## Changelog

### 2026-09-16
Added, for the sidebar's session menu. `lib/anchor` gained `pointAnchor` and
accepts any anchorable, so the menu can open under the pointer as well as
under a button.
