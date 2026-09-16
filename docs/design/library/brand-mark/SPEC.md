<!--
  Copied from desktop/frontend/src/ui/brand-mark/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Brand mark

## What it is

Sirdar's own mark: a twin peak with a route climbing to a summit marker, in the
colours of Nepal's flag, because a sirdar is the head guide of a Himalayan
expedition. It says which product this is, and nothing else.

Built. `desktop/frontend/src/ui/brand-mark/`, used in the app in one place: the
sidebar's brand row, at 20px before the word "Sirdar". The same drawing is the
app icon (`desktop/build/appicon.png`, cut from the light tile by
`scripts/make-icons.sh`), the two favicons and the landing page's wordmark. The
masters are in `docs/design/2026-09-16-logo/final/`; the mark was chosen from
the six in `docs/design/2026-09-16-logo/` on 2026-09-16.

## Anatomy

- `span.sd-brand-mark[data-size]` — the box. 20 square (`md`), 16 (`sm`), 32
  (`lg`). `direction: ltr`, since a mountain is not a word. No ground, no tile,
  no padding: the mark sits on whatever surface it is put on.
- `svg[viewBox="0 0 512 512"]` — the drawing, `aria-hidden`, filling the box.
- `g[data-variant="light"]` and `g[data-variant="dark"]` — both variants, both
  always in the markup. The theme shows one and hides the other.
- Inside each: the back peak, the front peak in crimson, the route as a 22-wide
  round-capped stroke, and the summit marker as a 19 circle.

## States

| State | What changes |
|---|---|
| rest | The mark. Light: a blue back peak, a crimson front peak, a white route. Dark: blue and white trade places — a paper back peak, the crimson unchanged, the route in blue. |
| hover | n/a. The mark is not a control. The sidebar row it sits in is not one either. |
| active / pressed | n/a. |
| focus-visible | n/a. Not focusable. |
| disabled | n/a. |
| loading | n/a. It is a constant. |
| error | n/a. |
| empty | n/a. There is one mark and it is always drawn. |
| selected | n/a. |
| RTL | Nothing moves: the box is `direction: ltr` and its contents are a drawing. The brand row around it mirrors, so in an Arabic pane the mark sits to the right of the word, which is the same reading order. |

## Tokens used

None. A mark that followed the palette would stop being the mark.

The four brand colours — crimson `#DC143C`, blue `#003893`, white `#FFFFFF`,
paper `#FAF7EC` — live in `index.tsx` and nowhere else, the same rule
`provider-mark` follows for the vendors' colours: `styles/tokens.css` names no
brand, and `src/ui/library.test.ts` holds every stylesheet to that.

The stylesheet carries only the box sizes and the theme switch. That switch is
two custom properties of its own, `--sd-brand-light` and `--sd-brand-dark`,
each set to `block` or `none` in the same four blocks `styles/tokens.css` uses
— the system theme, the chosen theme, and both themes again as a subtree. They
are declared and read in `BrandMark.css` alone, and they hold no colour.

## Do / Don't

- **Do** put it where the product names itself: the sidebar's brand row, and a
  future About panel or empty state that does the same.
- **Don't** use it as an avatar for a run, a workspace or a source. Those have
  their own marks — `provider-mark`, `source-mark` — and a brand mark repeated
  down a list stops meaning anything.
- **Do** pass `decorative` when a word beside it already says "Sirdar", which
  is the sidebar's case. Two accessible names for one thing makes a screen
  reader read the product twice.
- **Don't** add a tile, a ring or a shadow to it in a stylesheet. The squircle
  belongs to the app icon; the marks here are bare on purpose so they work on
  the shell, on a card and on the sheet without three variants.
- **Don't** pick the variant from a prop or from `matchMedia`. The theme picks
  it, through the same `data-theme` the palette reads, which is what lets the
  gallery's dark specimen frame hold a dark mark inside a light page.
- **Do** change `docs/design/2026-09-16-logo/final/sirdar-mark-*.svg` and this
  component together. `BrandMark.test.tsx` reads the masters and fails when the
  two drift, because the app wearing a logo nothing else does is the failure
  that would otherwise ship quietly.

## Accessibility

`role="img"` with an `aria-label` of "Sirdar", or `aria-hidden` when
`decorative` is set and a word beside it carries the name; the SVG is
`aria-hidden` either way, so the name is announced once. Contrast is not
claimed: it is a logo, not text, and it is never the only thing saying which
app this is — the word is beside it in the sidebar and in the window title.
The crimson peak against the blue is 2.12:1, which is a mark's business and not
a reading task; what does have to separate is the route from the peak it climbs,
and that is 10.57:1 light (white on blue) and 9.85:1 dark (blue on paper). No
motion.

## Changelog

### 2026-09-16
Added. Mark 1 of the six in `docs/design/2026-09-16-logo/`, chosen by the user
the same day, with their instruction for the dark variant: blue and white trade
places rather than the round's original ink tile, so there is no third colour
set to keep in step. The sidebar's wordmark gained the 20px mark before the
word at the same time.
