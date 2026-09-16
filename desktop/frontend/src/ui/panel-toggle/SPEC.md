# Panel toggle

## What it is

The 28px ghost button that folds a pane away and brings it back: the
session's artefacts pane at the sheet's end edge, the app sidebar at its
start edge. One glyph — Lucide's `panel-left` or `panel-right` — a label
that says what a press will do, and the shortcut in the tooltip.

Built. `desktop/frontend/src/ui/panel-toggle/`. New in the 2026-09-16
hover-panes round, from the desktop app's feedback that the transcript
wanted the whole sheet and the sidebar wanted to get out of the way.

## Anatomy

- `button.sd-panel-toggle[aria-expanded][data-side]` — 28 by 28, radius 6,
  no border, no fill at rest. `aria-expanded` is the pane's state;
  `aria-controls` names the pane; `aria-label` is `hideLabel` while the pane
  is open and `showLabel` while it is not; `title` is the label with the
  shortcut in brackets, "Hide panel (⌘\)"; `aria-keyshortcuts` spells the
  same shortcut as `Meta+\`.
- `svg` — 16 by 16 on the 24 grid, stroke 1.6, `currentColor`,
  `aria-hidden`. A rounded 18-square with one vertical rule: at `x=9` for
  `side="start"`, at `x=15` for `side="end"`. The glyph does not change with
  the state; the label does, and the pane itself is the evidence.

## States

| State | What changes |
|---|---|
| rest | `--sd-ink-3` glyph, no fill. |
| hover | `--sd-nav-hover` fill, `--sd-ink` glyph, over `--sd-dur-1`. |
| active / pressed | Nothing beyond hover. The pane moving is the feedback. |
| focus-visible | The shell's ring, 2px `--sd-accent` at offset 2. Never removed. |
| disabled | `opacity: .45`, cursor default; the tooltip is `disabledReason` — "The sidebar is a rail at this width" — rather than a label for a press that would do nothing. |
| loading | n/a. |
| error | n/a. |
| empty | n/a. |
| selected | n/a. `aria-expanded` carries the pane's state; the glyph is the same either way. |
| RTL | `scaleX(-1)` on the glyph, so `side="start"` still points at the start edge, which is the right one. The button's own box is symmetric. |

## Tokens used

- `--sd-ink-3` — the glyph at rest (4.60:1 on the shell, 4.98:1 on the sheet)
- `--sd-ink` — the glyph under the pointer
- `--sd-nav-hover` — the hover fill
- `--sd-dur-1` — the fill change

## Do / Don't

- **Do** put it at the edge the pane is on: the end of the session's tab
  row, the sidebar's head beside the wordmark. A toggle across the window
  from its pane is a hunt.
- **Do** keep a rail when the pane folds. The session pane leaves a 36px
  strip of its tab icons, the sidebar its 56px icon rail; the toggle is how
  the pane comes back, and it has to stay reachable.
- **Don't** flip the glyph on toggle. `panel-right` with the pane hidden
  still names the right edge; a glyph that turns into `panel-left` says
  the pane moved.
- **Don't** use `aria-pressed`. This is not a mode; it is a disclosure, and
  `aria-expanded` with `aria-controls` is what reads correctly.
- **Do** disable it when the window has folded the pane by itself, and say
  so in `disabledReason`. Below 1024 the sidebar is a rail regardless; a
  button that appears to offer the choice is worse than none.

## Accessibility

Native `button`; Space and Enter toggle. Named by `aria-label`, which
changes with the state so a screen reader hears the action rather than a
noun. `aria-expanded` and `aria-controls` tie it to the pane;
`aria-keyshortcuts` names the key the screen also binds (⌘\ for the
session pane, ⌘B for the sidebar). Contrast: `--sd-ink-3` glyph at
**4.60:1** on the shell and **4.98:1** on the sheet, both past the 3:1
non-text floor; hover `--sd-ink` at 13:1 and better. Hit target 28 by 28,
inside a 36-tall head. Reduced motion: the fill change goes to 1ms.

## Changelog

### 2026-09-16
Added. The session's artefacts pane and the app sidebar gain a manual
collapse; both use this one button.
