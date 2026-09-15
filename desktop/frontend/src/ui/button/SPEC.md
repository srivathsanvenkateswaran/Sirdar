# Button

## What it is

The control that does something. Four variants — primary, secondary, pale,
ghost — and the difference between them is a promise: only the primary is
filled with the ink, and only a primary button is allowed to change anything.
Nothing casts a shadow.

Built. `desktop/frontend/src/ui/button/`, used on all three surfaces. It
replaces `.button`, `.button--accent` and `.button--quiet` in
`desktop/frontend/src/styles.css`, which stay in place until the restyle branch
points the screens at this component.

## Anatomy

- `button.sd-button[data-variant][data-size][data-icon-only]` — the whole
  control. `min-height: var(--sd-control-h)` (40px), `padding-inline: 18px`
  (20 on the primary), `border-radius: 8px`, `gap: 8px`, `--sd-text-body`.
  `sm` is 32 tall at `--sd-text-ledger`; `lg` is 48 tall and at least 160
  wide, for the wide pale button on a setting row. `icon-only` is a 40px
  square carrying one icon and an accessible name.
- `span.sd-button__label` — the label. One line in Latin, two in Arabic
  without the box changing shape, because the height is a minimum and not a
  fixed value.
- `kbd.sd-button__kbd` — optional shortcut hint, `aria-hidden`, 1px border in
  `currentColor` at `--sd-radius-xs`.

## States

| State | What changes |
|---|---|
| rest | Secondary: surface fill, `--sd-rule-strong` border. Primary: `--sd-primary` fill with `--sd-primary-ink`, no border. Pale: `--sd-nav-active` fill, no border. Ghost: no border, no fill. |
| hover | Border and label go to `--sd-accent` (secondary), fill to `--sd-primary-hover` (primary), fill to `--sd-nav-hover` (pale), `--sd-accent-soft` fill (ghost). 120ms, colour only. |
| active / pressed | Nothing moves. The fill change is the feedback. |
| focus-visible | `2px solid var(--sd-accent)` at `outline-offset: 2px`, from the shell rule. Never removed. |
| disabled | `opacity: .45`, cursor default. The click never fires. |
| loading | `busy` sets `aria-disabled` and drops the click handler; focus stays where it is, because a `disabled` element loses focus mid-action and the reader is then nowhere. |
| error | n/a. A button does not carry an error; the form beside it does. |
| empty | n/a. A button with no label is a bug, not a state. |
| selected | n/a. A button that can be on or off is a segmented control. |
| RTL | Everything is logical and needs no rule; the icon leads the label in either direction. |

## Tokens used

- `--sd-surface` — secondary fill
- `--sd-primary` / `--sd-primary-ink` / `--sd-primary-hover` — the primary's fill, label (17.44:1 light, 15.69:1 dark) and hover
- `--sd-nav-active` / `--sd-nav-hover` — the pale fill and its hover
- `--sd-rule-strong` — secondary border
- `--sd-accent` / `--sd-accent-soft` — hover, focus ring, ghost hover fill
- `--sd-ink` / `--sd-ink-2` — label colours
- `--sd-control-h` — the height
- `--sd-radius-sm`, `--sd-radius-xs` — corner, and the kbd's corner
- `--sd-space-1` / `--sd-space-2` / `--sd-space-3` / `--sd-space-5` — padding and gap
- `--sd-text-body`, `--sd-text-ledger`, `--sd-text-micro` — label, small label and kbd
- `--sd-dur-1` — the transition

## Do / Don't

- **Do** use the primary variant for the one action on the surface that changes
  something: start a run, apply a fix, add a workspace.
- **Don't** put two primary buttons on one surface. The ink fill stops
  meaning "this is the one that acts" the moment it is on both. The sidebar's
  New session is the primary on every screen that has no other.
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
the label alone; an icon-only button takes its string child as `aria-label`.
Contrast: `--sd-primary-ink` on `--sd-primary` is **17.44:1** light and
**15.69:1** dark, **13.53:1** / **12.82:1** on the hover fill; the accent
hover label is **8.36:1** on paper. Reduced motion: every transition falls to
1ms; nothing waits for it.

## Changelog

### 2026-09-15 (v2 register)
Re-scaled to the reviewed mocks: 40 tall (`--sd-control-h`) at
`--sd-text-body`, with `sm` and `lg` sizes and an icon slot. The primary
changes from `--sd-highlight` with the hard offset shadow to `--sd-primary`
with `--sd-primary-ink` and no shadow; the press transform goes with it. A
`pale` variant (`--sd-nav-active` fill) is new, for setting rows and the
board's Filters.

### 2026-09-15
Added. Initial spec from `desktop/frontend/src/styles.css` `.button`,
`.button--accent`, `.button--quiet`. Radius moves 3px to `--sd-radius-sm`
(8px); the primary variant changes from an accent fill to `--sd-highlight` and
gains `--sd-shadow-hard`; the busy state is new and replaces the pattern of
disabling a button while its job runs.
