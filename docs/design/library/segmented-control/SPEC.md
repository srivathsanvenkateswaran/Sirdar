<!--
  Copied from desktop/frontend/src/ui/segmented-control/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Segmented control

## What it is

Two to four options, one of which is true, in a single track with a thumb that
slides to the chosen one. Used for a board filter, a provider choice, and the
gallery's own theme and direction switches.

Built. `desktop/frontend/src/ui/segmented-control/`. New: the app has no
equivalent today.

## Anatomy

- `div.sd-segmented[role="radiogroup"]` — the track. `--sd-sunk` fill, 1px
  `--sd-rule` border, `--sd-radius-pill`, 3px padding, one grid column per
  option so every option is the same width.
- `span.sd-segmented__thumb` — the moving part. Absolute, `inset-block: 3px`,
  width `(100% - 6px) / count`, positioned by `inset-inline-start` from
  `--sd-segmented-index`.
- `button.sd-segmented__option[role="radio"]` — one option. `min-height: 32px`
  inside the 3px track, so the whole control is `--sd-control-h` (40) tall;
  `padding-inline: 16px`, `--sd-text-meta` on a 22px line.

## States

| State | What changes |
|---|---|
| rest | Label in `--sd-ink-2`. |
| hover | Label to `--sd-ink`. |
| active / pressed | n/a. The thumb arriving is the feedback. |
| focus-visible | The shell's ring on the option itself, not on the track. |
| disabled | `aria-disabled` on the group, `disabled` on every option, `opacity: .45`. Arrows and clicks both do nothing. |
| loading | n/a. |
| error | n/a. |
| empty | n/a. Fewer than two options is a label, not a control. |
| selected | `aria-checked="true"`: label to `--sd-ink` at weight 500, thumb beneath it. |
| RTL | The thumb travels the other way with no second rule, because it is placed with `inset-inline-start`. The arrow keys swap: ArrowRight moves to the previous option, which is the one to its right. |

## Tokens used

- `--sd-sunk` — the track
- `--sd-surface` — the thumb
- `--sd-rule` / `--sd-rule-strong` — the track border and the thumb's hairline
- `--sd-ink` / `--sd-ink-2` — labels
- `--sd-radius-pill` — track, thumb and options
- `--sd-space-1` / `--sd-space-3` — padding
- `--sd-text-body` — labels
- `--sd-dur-2`, `--sd-dur-1`, `--sd-ease` — the slide and the label colour

## Do / Don't

- **Do** use it when the options are few, fixed, and visible at once.
- **Don't** use it for more than four. A fifth option makes every label too
  narrow to read, and the answer is a select.
- **Do** keep the options mutually exclusive and exhaustive; "All" is an
  option, not an absence.
- **Don't** reach for `transform: translateX` to move the thumb. It is a
  physical direction and would send the thumb the wrong way in an Arabic pane.
- **Do** let the arrows wrap at both ends. A reader holding an arrow key should
  not be stopped without being told why.

## Accessibility

`role="radiogroup"` named by `label`, with `role="radio"` and `aria-checked` on
each option. Roving tabindex: the group is one tab stop and the arrows move the
choice, which is the radio-group pattern rather than the tab-list one. Home and
End jump to the ends. ArrowUp and ArrowDown are direction-blind; ArrowLeft and
ArrowRight are read in the reader's own direction. Contrast: the checked label
is `--sd-ink` on `--sd-surface`, **17.44:1** light and **13.44:1** dark; the
rest are **7.76:1** on `--sd-sunk` or better. Reduced motion: the thumb jumps
rather than slides, and nothing waits for the transition.

## Changelog

### 2026-09-15 (v2 register)
Re-scaled to the reviewed mocks: the option's padding goes from 4/12 to 5/16
and its type from 13px to `--sd-text-meta` (14px) on a 22px line, so the
track measures the 40px control height.

### 2026-09-15
Added. New component; no predecessor in the app.
