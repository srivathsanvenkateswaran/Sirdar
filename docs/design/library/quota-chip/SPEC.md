<!--
  Copied from desktop/frontend/src/ui/quota-chip/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Quota chip

## What it is

How much of a provider's rate limit is gone: the provider, the window, a bar, a
percentage and when it resets.

Built. `desktop/frontend/src/ui/quota-chip/`. It replaces `.quota-meter`,
`.quota-bar` and `.quota-row` in `components/panels.css`.

## Anatomy

- `div.sd-quota[data-level]` — the chip. Mono throughout, tabular figures,
  `--sd-radius-pill`. Two children: the group and the countdown.
- `span.sd-quota__line` — everything that is always drawn, in one group so
  that the countdown is the only thing the chip can drop.
- `span.sd-quota__provider` — the provider name, weight 600.
- `span.sd-quota__window` — `5h`, `7d`, `used`.
- `span.sd-quota__bar[role="img"]` > `span.sd-quota__fill` — 56×4px, the fill
  sized with `inline-size` so it grows from the reader's leading edge.
- `span.sd-quota__pct` — `min-inline-size: 3ch`, so the text beside it never
  moves as the number changes.
- `span.sd-quota__word` — the words "over budget", present only when true.
- `span.sd-quota__reset` — the countdown alone, "2h 14m", `dir="ltr"`, after
  a middot the stylesheet draws on its own `::before`. The chip is one line
  tall and wraps rather than cutting, so a countdown with no room for it
  lands on a second line that is never drawn and takes its middot with it;
  the chip's `title` and the bar's name say "resets in 2h 14m" in full.

## States

| State | What changes |
|---|---|
| rest | `data-level="ok"`, fill in `--sd-st-queue`. |
| hover | n/a. |
| active / pressed | n/a. |
| focus-visible | n/a. Not focusable. |
| disabled | n/a. |
| loading | n/a. A provider with no quota reading is left out rather than shown at zero. |
| error | n/a. |
| empty | n/a. |
| warn | At 80% and above: fill and percentage in `--sd-st-blocked`. |
| over | At 100%, or when the workspace reports it: fill and percentage in `--sd-st-failed`, plus the words "over budget". |
| RTL | The bar fills from the right, because it is sized with `inline-size`; the numbers stay LTR. |

## Tokens used

- `--sd-sunk` / `--sd-rule` — the empty half of the bar
- `--sd-st-queue` / `--sd-st-blocked` / `--sd-st-failed` — the fill and the percentage
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — provider, window, reset
- `--sd-radius-pill` — chip, bar and fill
- `--sd-space-2` — the gaps
- `--sd-text-micro`, `--sd-font-mono`
- `--sd-dur-2`, `--sd-ease` — the fill's width

## Do / Don't

- **Do** say "over budget" in words. The bar turning red is the same fact
  stated a second time for people who can see it, not the first time it is
  stated.
- **Don't** compute the reset time inside the component. It takes a formatted
  string, so one clock is used for the whole window and the chip cannot drift
  from the header beside it.
- **Do** clamp a nonsense percentage rather than drawing past the end of the
  bar.
- **Don't** put the chip on `--sd-sunk`. `--sd-st-blocked` is 4.43:1 there.
- **Do** keep the percentage a fixed 3ch wide, so a bar that fills does not
  nudge the text beside it.
- **Don't** ellipsise the countdown. A separator with nothing after it —
  "57% · …" — reads as a broken chip; the whole countdown goes, and the
  title still carries it.

## Accessibility

The bar is `role="img"` with a label that reads as a sentence — `"claude 5h:
42% used"` — rather than a `progressbar`, which would promise an operation
that finishes. The percentage is also on screen as text. Contrast on surface:
`--sd-st-blocked` **5.10:1**, `--sd-st-failed` **6.63:1**, provider `--sd-ink`
**17.44:1**. Reduced motion: the fill's width transition is dropped.

## Changelog

### 2026-09-16 (the countdown is all or nothing)
A countdown the chip has no room for is dropped whole instead of ellipsised.
At the 248px sidebar the chip often has room for the reading but not for
"· 1h 58m" beside it, and the ellipsis left "57% · …" — a separator pointing
at nothing. The chip now wraps (`flex-wrap: wrap`) and is one line tall
(`max-block-size`, `align-content: flex-start`), so a countdown with no room
lands on a second line that is never drawn and takes its `::before` middot
with it. Everything else moved into `span.sd-quota__line`, which shrinks the
bar and clips inside itself as before, so nothing but the countdown can be
what wraps away.

### 2026-09-15 (responsive)
One line at every width. `resetsIn` is now the bare countdown ("2h 14m"),
drawn after a middot and cut with an ellipsis when the sidebar is too narrow
for it; the chip's `title` and the bar's `aria-label` say "resets in 2h 14m"
in full. Before this the chips wrapped "resets in / 0h 0m" onto a second
line at 1470 wide.

### 2026-09-15
Added. Initial spec from `components/panels.css` `.quota-meter` and
`components/QuotaMeter.tsx`. `--panel-warn` and `--panel-danger` become
`--sd-st-blocked` and `--sd-st-failed`, which removes the file's
`prefers-color-scheme` block; `width` becomes `inline-size`; the "over budget"
words are new.
