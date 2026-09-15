<!--
  Copied from desktop/frontend/src/ui/hero-band/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Hero band

## What it is

The full-bleed colour band a page opens on: a display headline whose second
clause is italic, one deck line, one action, and optionally one ambient
decoration beside it.

Built. `desktop/frontend/src/ui/hero-band/`. New; it is the landing page's and
the docs home's opening section, and nothing in the app uses it.

## Anatomy

- `section.sd-hero[data-tone]` — the band. Full width, `padding-block: 64px`,
  `border-start-start-radius` and `border-start-end-radius` at
  `--sd-radius-band` (40px, 64px from 768px up). No shadow.
- `div.sd-hero__inner` — the content, centred at `--sd-measure-wide` (1200px).
- `div.sd-hero__aside` — ambient decoration; the ring text lives here.
- `h1.sd-hero__headline` — `span.sd-hero__clause` (roman) plus
  `span.sd-hero__clause-tail` (italic), one space between them so the headline
  reads and copies as one sentence. `max-width: 18ch`.
- `p.sd-hero__deck` — one line, `max-width: 46ch`.
- `div.sd-hero__action` — one button, at the landing page's 44px hit target.

## States

| State | What changes |
|---|---|
| rest | `deep` tone: `--sd-band-deep`. `ink` tone: `--sd-band-ink`, at most once per page. |
| hover | n/a on the band; the button inside has its own. |
| active / pressed | n/a. |
| focus-visible | The button keeps the shell's ring, which clears 4.5:1 against both bands. |
| disabled | n/a. |
| loading | n/a. |
| error | n/a. |
| empty | n/a. A band with no headline is not a hero. |
| selected | n/a. |
| narrow | Under 720px the aside stacks above the content and the headline drops to `--sd-text-display-m`. |
| RTL | The leading corners are `border-start-start-radius`/`border-start-end-radius`, so the rounded pair follows the reading direction. Arabic has no italic: `[dir="rtl"]` sets the second clause to weight 600 and slant back to normal. |

## Tokens used

- `--sd-band-deep` / `--sd-band-ink` — the ground
- `--sd-band-fg` — the ink on it: `--sd-paper` in light (12.83:1 on the deep band), `--sd-ink` in dark (10.48:1)
- `--sd-radius-band` — the leading corners
- `--sd-measure-wide` — the content measure
- `--sd-font-display` / `--sd-font-ui` — headline and deck
- `--sd-text-display-l`, `--sd-text-display-m`, `--sd-text-body-l`
- `--sd-space-4` … `--sd-space-8`

## Do / Don't

- **Do** put exactly one action in the band. Two calls to action is none.
- **Don't** put an accent or a status hue inside a band. Only band ink is
  allowed on a band; a status hue there means nothing and still looks like it
  does.
- **Do** write the headline as two clauses, roman then italic. It is the
  typographic figure the whole design language is built around.
- **Don't** fake the italic in Arabic. A slanted Naskh is a defect, not an
  emphasis; the Arabic band marks its second clause with weight.
- **Do** keep the band sequence paper, band, paper. Two coloured bands touching
  is the failure mode this component is most often put into.

## Accessibility

The band is a `section` containing the page's `h1`. The deck is a paragraph, not
a heading. The ambient aside is `aria-hidden` and carries nothing the headline
does not already say. Text over a band is `--sd-band-fg` and nothing else:
**12.83:1** on the deep band and **16.54:1** on the ink one in light,
**10.48:1** and **15.69:1** in dark. The deck's `opacity: .86` keeps it above
4.5:1 on both. Reduced motion: the band itself does not animate; the ring text
inside it has its own static pose.

## Changelog

### 2026-09-15
Added. New component. `--sd-band-fg` is added to `styles/tokens.css` alongside
it: `01-tokens.md` states the band's ratios ("12.83:1 light, 10.48:1 dark")
without naming the token that paints them, and two components have to agree on
it, which by section 4 of that file makes it a token.
