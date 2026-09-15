# Banner

## What it is

A tinted line that says what just finished: tests passed, a note was filed,
the agent asked a question. One per transcript, for the last finished step,
drawn above the composer so the reader sees it before typing.

Built. `desktop/frontend/src/ui/banner/`, used in the app's session screen.
New in the 2026-09-15 screens round.

## Anatomy

- `div.sd-banner[role="status"][data-tone]` — flex, `gap: 12px`,
  `padding-block: 16px`, `padding-inline: 20px`, `--sd-radius-md`, a 10% tint
  of the tone's hue, text in the hue, `--sd-text-body`.
- `span.sd-banner__text` — the sentence, wrapping.
- `b.sd-banner__title` — the lead at weight 600.
- `span.sd-banner__sep` — a middle dot at 60% opacity, `aria-hidden`, drawn
  only when there is a body.
- `span.sd-banner__body` — the rest of the sentence.
- `span.sd-banner__action` — one control at the inline end, optional.

## States

| State | What changes |
|---|---|
| rest | `ok`: `--sd-st-triaged` (default). `done`: `--sd-st-done`. `blocked`: `--sd-st-blocked`. `failed`: `--sd-st-failed`. `live`: `--sd-st-live`. |
| hover | n/a. The banner is not a control; its action may be. |
| active / pressed | n/a. |
| focus-visible | n/a on the banner; the action carries the shell's ring. |
| disabled | n/a. |
| loading | n/a. A banner reports something finished. |
| error | The `failed` tone. |
| empty | A banner with no body draws the lead alone and no separator. |
| selected | n/a. |
| RTL | `dir="auto"` from the caller lets an Arabic question lay itself out from its own first letter; the action sits at the inline end in either direction. |

## Tokens used

- `--sd-st-triaged`, `--sd-st-done`, `--sd-st-blocked`, `--sd-st-failed`, `--sd-st-live` — the hue, and its 10% tint
- `--sd-radius-md`, `--sd-space-3`, `--sd-space-4`
- `--sd-text-body`, `--sd-font-ui`

## Do / Don't

- **Do** put one banner in a transcript, for the last finished step. Two
  banners are a log.
- **Don't** use it for a toast's job. A toast is about something the window
  did; a banner is about something the run did, and it stays until the next
  step finishes.
- **Do** put the fact in the lead: "Tests passed", "Note filed". The body is
  the detail.
- **Don't** use the `failed` tone for a refused tool call. That is an event
  row; a banner is for the step's outcome.

## Accessibility

`role="status"`, so a screen reader announces a new banner politely and
without stealing focus. The separator is `aria-hidden`. Contrast: every hue
on its own 10% tint over the sheet clears what it clears on the sheet itself
within 0.1 — **5.6:1** for triaged, **4.8:1** for blocked (the tightest),
**6.3:1** for failed in light; higher in dark. No motion.

## Changelog

### 2026-09-15
Added, from `.banner` in `docs/design/2026-09-15-screens/Session.html`.
