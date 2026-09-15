<!--
  Copied from desktop/frontend/src/ui/toggle/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Toggle

## What it is

A switch: on or off, and nothing in between. The control on a setting row
whose value is a boolean.

Built. `desktop/frontend/src/ui/toggle/`, used in the app's settings modal.
New in the 2026-09-15 screens round; the app used native checkboxes before.

## Anatomy

- `button.sd-toggle[role="switch"][aria-checked]` — the track, 44 by 24,
  `--sd-radius-pill`, `--sd-rule-strong` off and `--sd-ink` on.
- `span.sd-toggle__knob` — 18 by 18, `--sd-surface`, at
  `inset-inline-start: 3px` off and `23px` on, `aria-hidden`.

## States

| State | What changes |
|---|---|
| rest | Off: strong-rule track, knob at the start. |
| hover | Nothing. The click is the feedback. |
| active / pressed | n/a. |
| focus-visible | The shell's ring around the track. |
| disabled | `opacity: .45`, cursor default, the click never fires. |
| loading | n/a. A setting that takes time to apply is a button with `busy`. |
| error | n/a. |
| empty | n/a. |
| selected | `aria-checked="true"`: the track is the ink, the knob at the end. The travel is `--sd-dur-2` on the logical inset. |
| RTL | The knob travels the other way with no second rule, because it is placed with `inset-inline-start`. |

## Tokens used

- `--sd-rule-strong` — the track, off
- `--sd-ink` — the track, on
- `--sd-surface` — the knob
- `--sd-radius-pill`
- `--sd-dur-1` / `--sd-dur-2` / `--sd-ease` — the track's colour and the knob's travel

## Do / Don't

- **Do** name it: `label` for a screen reader, or `labelledBy` pointing at
  the setting row's visible label.
- **Don't** use it for a choice with three answers. That is a segmented
  control.
- **Do** apply the change on click. A switch that waits for Save is a
  checkbox in a form, and the settings modal has no Save.
- **Don't** use it for a setting the app cannot write. Those rows offer
  "Open config" instead.

## Accessibility

Native `button` with `role="switch"` and `aria-checked`; Space and Enter
flip it. Named by `aria-label` or `aria-labelledby`. Contrast: the on track
is `--sd-ink` against the card row, **15.70:1** light; the off track
`--sd-rule-strong` against the card row is a 3:1 non-text boundary in both
themes, and the state is also carried by the knob's position and the
`aria-checked` value. Reduced motion: the knob jumps, nothing waits for it.

## Changelog

### 2026-09-15
Added, from `.toggle` in `docs/design/2026-09-15-screens/Settings.html`.
