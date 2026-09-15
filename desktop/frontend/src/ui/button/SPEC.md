# Button

## What it is

The control that does something. Three variants — primary, secondary, ghost —
and the difference between them is a promise: only the primary carries the hard
offset shadow, and only a primary button is allowed to change anything.

Built. `desktop/frontend/src/ui/button/`, used on all three surfaces. It
replaces `.button`, `.button--accent` and `.button--quiet` in
`desktop/frontend/src/styles.css`, which stay in place until the restyle branch
points the screens at this component.

## Anatomy

- `button.sd-button[data-variant]` — the whole control. `min-height: 32px`
  (the app's hit target; the hero band raises it to 44px), `padding-block:
  4px`, `padding-inline: 12px`, `border-radius: 8px`, `gap: 8px`.
- `span.sd-button__label` — the label. One line in Latin, two in Arabic
  without the box changing shape, because the height is a minimum and not a
  fixed value.
- `kbd.sd-button__kbd` — optional shortcut hint, `aria-hidden`, 1px border in
  `currentColor` at `--sd-radius-xs`.

## States

| State | What changes |
|---|---|
| rest | Secondary: surface fill, `--sd-rule-strong` border. Primary: `--sd-highlight` fill, border in the shadow colour, `--sd-shadow-hard`. Ghost: no border, no fill. |
| hover | Border and label go to `--sd-accent` (secondary), label only (primary), `--sd-accent-soft` fill (ghost). 120ms, colour only. |
| active / pressed | Primary translates `1px, 1px` (`-1px, 1px` under RTL) and the shadow shrinks to `1px 1px`. Secondary and ghost do not move. |
| focus-visible | `2px solid var(--sd-accent)` at `outline-offset: 2px`, from the shell rule. Never removed. |
| disabled | `opacity: .45`, cursor default, no shadow on the primary. The click never fires. |
| loading | `busy` sets `aria-disabled` and drops the click handler; focus stays where it is, because a `disabled` element loses focus mid-action and the reader is then nowhere. |
| error | n/a. A button does not carry an error; the form beside it does. |
| empty | n/a. A button with no label is a bug, not a state. |
| selected | n/a. A button that can be on or off is a segmented control. |
| RTL | The hard shadow flips to `-2px 2px 0 0`, from `[dir="rtl"]` in `tokens.css`. Everything else is logical and needs no rule. |

## Tokens used

- `--sd-surface` — secondary fill
- `--sd-highlight` / `--sd-highlight-ink` — primary fill and label (13.86:1)
- `--sd-rule-strong` — secondary border
- `--sd-shadow-hard` / `--sd-shadow-hard-pressed` / `--sd-shadow-hard-color` — the printed offset and the primary's border
- `--sd-accent` / `--sd-accent-soft` — hover, focus ring, ghost hover fill
- `--sd-ink` / `--sd-ink-2` — label colours
- `--sd-radius-sm`, `--sd-radius-xs` — corner, and the kbd's corner
- `--sd-space-1` / `--sd-space-2` / `--sd-space-3` — padding and gap
- `--sd-text-body`, `--sd-text-micro` — label and kbd
- `--sd-dur-1`, `--sd-ease` — the transition

## Do / Don't

- **Do** use the primary variant for the one action on the surface that changes
  something: start a run, apply a fix, add a workspace.
- **Don't** put two primary buttons on one surface. The shadow stops meaning
  "this is the one that acts" the moment it is on both.
- **Do** name the action: "Start triage", "Apply fix". The same word then
  appears in the toast that reports it.
- **Don't** write "Submit", and don't append an arrow to the label. `→` after
  a button's words is decoration standing in for a verb.
- **Do** use `busy` for an action already running.
- **Don't** use `disabled` for it: `.button:disabled` in today's header is what
  makes New triage unreachable by keyboard while a workspace loads.

## Accessibility

Role is the native `button`; `type="button"` unless a form needs a submit.
Enter and Space activate it. The busy state is `aria-disabled` rather than
`disabled`, so the control keeps its place in the tab order and a screen reader
still reads it. The shortcut hint is `aria-hidden`, so the accessible name is
the label alone. Contrast: `--sd-highlight-ink` on `--sd-highlight` is
**13.86:1** light and **9.72:1** dark; the accent hover label is **8.36:1** on
paper. Reduced motion: the press transform is dropped and every transition
falls to 1ms; nothing waits for either.

## Changelog

### 2026-09-15
Added. Initial spec from `desktop/frontend/src/styles.css` `.button`,
`.button--accent`, `.button--quiet`. Radius moves 3px to `--sd-radius-sm`
(8px); the primary variant changes from an accent fill to `--sd-highlight` and
gains `--sd-shadow-hard`; the busy state is new and replaces the pattern of
disabling a button while its job runs.
