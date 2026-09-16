<!--
  Copied from desktop/frontend/src/ui/drawer/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Drawer

## What it is

A panel that slides over one column and leaves the rest of the window as it
was. The B session opens Bundle and Tools this way: things a reader
consults while reading the note, not destinations of their own, so they
cover the path column and nothing else. It is not modal; the document
beside it stays live.

Built. `desktop/frontend/src/ui/drawer/`, used by the session's Document
layout and offered to the other two. New in the 2026-09-16 session round.

## Anatomy

- `section.sd-drawer[role="dialog"][aria-modal="false"]` — absolute,
  `inset-block: 0`, `inset-inline-start: 0`, `--sd-drawer-w` (432px) wide,
  the sheet's fill, a `--sd-rule-strong` on its trailing edge. Positioned
  against the nearest `position: relative` ancestor, which is the caller's
  column.
- `.sd-drawer__head` — 52px, a hairline under it: the title in the tracked
  12.5px label face, a mono `meta` line, and the 32px ghost close button at
  the trailing end.
- `.sd-drawer__body` — the scroll container, `overflow-y: auto`,
  `min-block-size: 0`, padded 6/16/20/20.

## States

| State | What changes |
|---|---|
| rest | Open: the panel over the column, slid in 12px over `--sd-dur-2`. Closed: nothing is rendered. |
| hover | The close button takes `--sd-nav-hover`. |
| active / pressed | n/a. |
| focus-visible | The shell's ring. Focus lands on the close button when the drawer opens and returns to the opener when it closes. |
| disabled | n/a. |
| loading | The caller's business: the body holds whatever the block draws while it reads. |
| error | The caller's business, in the body. |
| empty | The caller's business, in the body. |
| selected | n/a. |
| RTL | Pinned with `inset-inline-start`, so it covers the right-hand column and slides in from the right; the close button sits at the trailing (left) end. |

## Tokens used

- `--sd-sheet` — the fill
- `--sd-rule` / `--sd-rule-strong` — the head's hairline and the trailing edge
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — the body, the title, the meta
- `--sd-nav-hover` — the close button under the pointer
- `--sd-radius-sm`
- `--sd-font-ui` / `--sd-font-mono`
- `--sd-dur-1` / `--sd-dur-2` / `--sd-ease`

## Do / Don't

- **Do** give it a positioned column to cover. The drawer's `absolute` is
  what keeps the document beside it untouched.
- **Do** put a count or a size in `meta` — `15 calls · 2 denied`,
  `ticket.json 2.9 kB` — so the head says what the body holds.
- **Don't** use it for something that has to be answered. A decision is a
  dialog with a scrim; this stays open beside live content.
- **Don't** open two at once over the same column. The caller keeps one
  `open` at a time.

## Accessibility

`role="dialog"` with `aria-modal="false"`, named by its title. Escape
closes; the close button is named `Close <title>` and takes focus on open,
and focus goes back to the element that had it when the drawer closes.
Contrast: `--sd-ink-2` on `--sd-sheet` is **8.2:1** light, `--sd-ink-3`
**5.2:1**. Reduced motion: the slide is not drawn; the panel appears in
place.

## Changelog

### 2026-09-16
Added, from `.drawer` and `.dh` in `docs/design/2026-09-16-session/B/`.
