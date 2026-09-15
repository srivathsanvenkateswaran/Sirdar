<!--
  Copied from desktop/frontend/src/ui/dialog/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Dialog

## What it is

A modal question: start a run, add a workspace, confirm a fix that will open a
pull request.

Built. `desktop/frontend/src/ui/dialog/`. It replaces `.scrim` and `.dialog` in
`styles.css`, which `components/shell/NewTriageDialog.tsx` still uses.

## Anatomy

- `div.sd-scrim` — the backdrop. Fixed, `color-mix(in srgb, var(--sd-ink) 32%,
  transparent)`, content aligned to the top at `8vh` so a tall dialog does not
  jump when it grows.
- `div.sd-dialog[role="dialog"][aria-modal]` — the panel. `max-width: 420px`,
  `--sd-radius-md`, `--sd-shadow-soft`, `tabindex="-1"` so it can hold focus
  when it contains no control.
- `h2.sd-dialog__title` — names the dialog through `aria-labelledby`.
- `div.sd-dialog__body` — the question.
- `div.sd-dialog__actions` — the actions, trailing-aligned; the one that
  commits is the primary button.

## States

| State | What changes |
|---|---|
| rest | Open, focus inside. |
| hover | n/a on the dialog; its buttons have their own. |
| active / pressed | n/a. |
| focus-visible | Every control inside keeps the shell's ring. |
| disabled | n/a. |
| loading | n/a. The form inside marks its own commit button busy. |
| error | n/a. The form shows the reason beside the button that failed. |
| empty | n/a. |
| closed | Renders nothing at all, and focus returns to whatever opened it. |
| entrance | 300ms, 6px rise and a fade. |
| RTL | The actions row follows the flex direction; the panel is centred either way. |

## Tokens used

- `--sd-ink` — the scrim, at 32%
- `--sd-surface`, `--sd-rule-strong`, `--sd-radius-md` — the panel
- `--sd-shadow-soft` — the panel
- `--sd-space-2` / `--sd-space-3` / `--sd-space-4`
- `--sd-text-body`, `--sd-text-title`
- `--sd-dur-3`, `--sd-ease`

## Do / Don't

- **Do** return focus to the control that opened the dialog when it closes.
  Without it, dismissing a dialog drops focus on the document body and the next
  Tab starts again from the address bar.
- **Don't** trap focus without an escape. Escape closes, and so does a click on
  the scrim.
- **Do** tint the scrim.
- **Don't** blur it. A backdrop filter repaints the whole board behind it every
  frame, and the board can have a live run on it.
- **Do** make the committing action the primary button and the only one with
  the hard shadow.

## Accessibility

`role="dialog"` with `aria-modal="true"`, labelled by its own heading. Focus
moves to the first control on open, cycles inside on Tab and Shift+Tab, and
returns to the opener on close. Escape closes from anywhere in the window.
While it is open everything outside it carries the `inert` attribute — every
sibling of every ancestor, since the dialog is rendered in place — so the
board behind the scrim takes no click, no focus and no place in the
accessibility tree; `aria-modal` alone only tells a screen reader so. The
attribute comes off before focus returns, because an inert opener cannot take
it.
Contrast: title and body `--sd-ink` on `--sd-surface` **17.44:1** light and
**13.44:1** dark. Reduced motion: the entrance animation is dropped; the dialog
is simply there.

## Changelog

### 2026-09-15 (inert)
The rest of the window is `inert` while the dialog is open, through the
exported `inertOutside` helper the Modal sheet shares. Before this a pointer
could still press the board behind the scrim, and a screen reader's virtual
cursor could still wander into it.

### 2026-09-15
Added. Initial spec from `styles.css` `.dialog` / `.scrim`. Radius moves 5px to
`--sd-radius-md`; the panel gains `--sd-shadow-soft`; the focus trap, the
Escape handler and the focus return are new — today's `NewTriageDialog` handles
Escape in `App.tsx` and does neither of the other two.
