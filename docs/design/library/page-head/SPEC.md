<!--
  Copied from desktop/frontend/src/ui/page-head/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Page head

## What it is

The top of a screen: its name at `--sd-text-display-m` (32px), an optional
line under it, and its actions at the inline end. The sans on every screen;
the serif on the settings heading only, which is where the reference app
keeps its serif too.

Built. `desktop/frontend/src/ui/page-head/`, used in the app on Board,
Register, Eval, Library and the settings modal. New in the 2026-09-15 screens
round.

## Anatomy

- `div.sd-page-head` — flex, space-between, `gap: 16px`, wrapping.
- `div.sd-page-head__text` — the title and lede.
- `h1.sd-page-head__title` (or `h2` via `level`) — `--sd-text-display-m`
  weight 500, `line-height: 1.2`, `letter-spacing: -.01em`, `--sd-ink`. With
  `serif`: `--sd-font-display` at 34px weight 400.
- `p.sd-page-head__lede` — `--sd-text-body` in `--sd-ink-3`, 6px under.
- `div.sd-page-head__actions` — flex, `gap: 12px`, at the inline end.

## States

| State | What changes |
|---|---|
| rest | As above. |
| hover | n/a. |
| active / pressed | n/a. |
| focus-visible | n/a on the head; each action carries the shell's ring. |
| disabled | n/a. |
| loading | n/a. A screen's name is known before its data. |
| error | n/a. |
| empty | A head with no lede and no actions is the title alone. |
| selected | n/a. |
| RTL | The title leads and the actions trail from flex; the letter-spacing is dropped by the browser for Arabic, which is right. |

## Tokens used

- `--sd-ink` / `--sd-ink-3` — title and lede
- `--sd-text-display-m`, `--sd-text-body`
- `--sd-font-ui` / `--sd-font-display` — the sans, and the serif for Settings
- `--sd-space-3` / `--sd-space-4`

The serif's 34px is not on the ladder: it is the reference app's settings
heading, measured off the Settings mock, and one component uses it.

## Do / Don't

- **Do** use `level={2}` inside the settings modal, where the modal's own
  accessible name is the h1's job.
- **Don't** set the serif anywhere but the settings heading. Note titles use
  the note pane's own serif; everywhere else the app is one sans.
- **Do** keep to one filled button among the actions. The page head is where
  the screen's primary lives now that the sidebar's button starts sessions.
- **Don't** draw a rule under it. The content below starts at its own
  margin.

## Accessibility

A real heading at the stated level, so the screen has one landmark heading a
screen reader can jump to; `id` lets a modal point `aria-labelledby` at it.
Contrast: `--sd-ink` on the sheet **17.44:1**, the lede `--sd-ink-3` on the
sheet **5.25:1** light. No motion.

## Changelog

### 2026-09-15
Added, from `.page-head`, `.h-page`, `.lede` and `.h-serif` in
`docs/design/2026-09-15-screens/Board.html` and `Settings.html`.
