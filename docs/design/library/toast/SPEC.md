<!--
  Copied from desktop/frontend/src/ui/toast/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Toast

## What it is

What happened, in the trailing bottom corner, out of the board's way: a job
started, a fix opened a pull request, a start was refused.

Built. `desktop/frontend/src/ui/toast/`. It replaces `.toasts` and `.toast` in
`styles.css`.

## Anatomy

- `ul.sd-toasts[role="status"]` — the stack. Fixed, `inset-inline-end: 12px`,
  `inset-block-end: 12px`, `max-width: min(380px, 100vw - 48px)`.
- `li.sd-toast[data-tone]` — one message. 1px `--sd-rule-strong` border with a
  2px `border-inline-start` in the tone's hue, `--sd-radius-sm`,
  `--sd-shadow-soft`.
- `span.sd-toast__text` — `dir="auto"`, so an Arabic message lays itself out.
- `button.sd-toast__dismiss` — 24px, `aria-label="Dismiss"`.

## States

| State | What changes |
|---|---|
| rest | Info tone: accent edge. |
| hover | The dismiss button's glyph goes to `--sd-ink`. |
| active / pressed | n/a. |
| focus-visible | The shell's ring on the dismiss button. |
| disabled | n/a. |
| loading | n/a. A toast reports an outcome, never a wait. |
| error | `data-tone="error"`: the edge becomes `--sd-st-failed`. The message itself says what went wrong. |
| empty | Nothing renders — no empty list, no empty region. |
| selected | n/a. |
| entrance | 140ms, 4px rise and a fade, `--sd-ease`. |
| RTL | The stack moves to the bottom-left corner, because its insets are logical. |

## Tokens used

- `--sd-surface`, `--sd-rule-strong`, `--sd-radius-sm` — the card
- `--sd-shadow-soft` — one of the only three things that carry it
- `--sd-accent` / `--sd-st-failed` — the tone edge
- `--sd-ink` / `--sd-ink-3` — text and the dismiss glyph
- `--sd-space-1` / `--sd-space-2` / `--sd-space-3` / `--sd-space-5`
- `--sd-text-body`, `--sd-text-body-l`
- `--sd-ease`

## Do / Don't

- **Do** say what happened in the interface's own voice: "Triage started for
  OMNI-2510."
- **Don't** apologise or hedge. An error toast says what failed and what to do:
  "Add a workspace before starting a run."
- **Do** let a toast dismiss itself after its time is up, and let it be
  dismissed by hand before then.
- **Don't** put a control other than Dismiss in a toast. A toast that can
  disappear cannot hold the only route to an action.
- **Do** keep the region polite. A toast that interrupts a screen reader
  mid-sentence to report a success is worse than no toast.

## Accessibility

`role="status"` with `aria-live="polite"`, so the message is read after
whatever is in progress and never takes focus. The dismiss button is a real
button with a label rather than a bare `×` glyph. Contrast: text `--sd-ink` on
`--sd-surface` **17.44:1** light and **13.44:1** dark; the failed edge is a 2px
graphic at **6.63:1**. Reduced motion: the entrance animation is dropped
entirely — the toast appears where it would have landed.

## Changelog

### 2026-09-15
Added. Initial spec from `styles.css` `.toast`. `right`/`bottom` become
`inset-inline-end`/`inset-block-end`; `border-left` becomes
`border-inline-start`; the dismiss target grows from a bare glyph to 24px; the
dismiss delay becomes a prop so the gallery can hold a specimen open.
