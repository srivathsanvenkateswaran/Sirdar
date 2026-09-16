# Ring text and marquee

## What it is

The two ambient motions, in one folder because they share one contract: neither
carries information, both reverse under RTL, and both have a static pose rather
than a faster version when the reader has asked for less motion.

Built. `desktop/frontend/src/ui/ambient/`, with the shared switch and the
keyframes in `desktop/frontend/src/ui/motion/`. Landing page only. Ring text is
used by the hero band's aside; the marquee is used by the source band. Each
motion has exactly one consumer.

## Anatomy

**Ring text**

- `div.sd-ring` — a square box, `aria-hidden`, optionally carrying
  `.sd-motion-ring` (the 48s linear rotation).
- `span.sd-ring__glyph` — one character, placed with
  `rotate(angle) translate(radius) rotate(90deg)`. The sentence starts at nine
  o'clock and reads clockwise, which is also the angle the still pose freezes
  at, so the moving and the still ring are the same drawing.

**Marquee**

- `div.sd-marquee[role="group"]` — named by `label`; a 64px gradient mask at
  each edge.
- `div.sd-marquee__track` — optionally carrying `.sd-motion-marquee` (the 34s
  linear translate of one track width).
- `div.sd-marquee__run` — the items. Rendered twice while moving, so the loop
  has no seam; the duplicate is `aria-hidden` or every source would be
  announced twice.
- `span.sd-marquee__item` — one item, set as type. Never a third-party logo.

## States

| State | What changes |
|---|---|
| rest | Ring turns once per 48s; marquee travels one track per 34s. Both linear, both infinite. |
| hover | n/a. Neither responds to a pointer. |
| active / pressed | n/a. |
| focus-visible | n/a. Neither is focusable; neither contains a control. |
| disabled | n/a. |
| loading | n/a. |
| error | n/a. |
| empty | A marquee with no items renders an empty row rather than a broken loop; a ring with an empty sentence renders nothing. |
| selected | n/a. |
| reduced motion | The ring stops at its starting angle. The marquee stops, renders its track once, wraps onto as many lines as it needs, and drops the edge mask — a mask with nothing moving under it is a gradient for its own sake. |
| RTL | Both reverse direction. A sentence travelling against its own reading direction is unreadable in either script. |

## Tokens used

- `--sd-dur-ring` (48s) / `--sd-dur-marquee` (34s) — the two durations
- `--sd-ink-2` / `--sd-ink-3` — the marquee items and the ring, or the band's own ink when the ring sits inside one
- `--sd-font-ui` — both
- `--sd-text-micro`, `--sd-text-body-l`
- `--sd-space-2` / `--sd-space-4` / `--sd-space-6` — the row's gaps

## Do / Don't

- **Do** keep both decorative. Every word in the ring is already in the
  headline beside it; the marquee's items are also listed in the page's text.
- **Don't** put information in either. A fact that only exists inside a 48s
  rotation is a fact nobody has read.
- **Do** use each motion once per page at most. Two rings on one page is a
  screensaver.
- **Don't** shorten the animation under `prefers-reduced-motion`. A faster
  animation is still motion; the answer is the static pose.
- **Don't** build the marquee from third-party logos. Sirdar names providers
  and sources as type, which is also what keeps the page free of marks it has
  no licence to.

## Accessibility

The ring is `aria-hidden`: read character by character it is noise. The marquee
is a `group` with a label, its duplicate track hidden, and its items are real
text that a screen reader reads once. Neither takes focus and neither holds a
control, so neither can strand a keyboard reader in a moving row. Contrast:
marquee items `--sd-ink-2` **7.76:1** on paper; the ring is decorative and sits
at `--sd-ink-3` **4.98:1** or takes the band's own ink. Reduced motion: both
stop, as described above, and the page loses nothing.

## Changelog

### 2026-09-16 (page enter fills backwards)
`.sd-motion-page` in `motion.css` fills `backwards`, not `both`. A transform
animation that keeps filling forwards leaves the sheet's page with an identity
transform, which makes it the containing block for every `position: fixed`
popover inside it — the model picker was drawn offset by the sidebar's width.
Nothing visible about the entrance changes.

### 2026-09-15
Added. New components. `--sd-dur-ring` and `--sd-dur-marquee` are added to
`styles/tokens.css`: `00-design-language.md` section 7 fixes both durations,
and a duration two files have to agree on is a token.
