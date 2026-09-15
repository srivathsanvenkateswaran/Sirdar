<!--
  Copied from desktop/frontend/src/ui/pill-nav/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Pill nav

## What it is

The row of places you can go, as pills on a surface bar. Used by the app
header, the landing page and the docs site; the landing and docs versions float
over whatever colour band they are on.

Built. `desktop/frontend/src/ui/pill-nav/`. It replaces `.nav` and `.nav-item`
in `styles.css`, which the app still uses through
`components/shell/Nav.tsx` until the restyle branch lands.

## Anatomy

- `nav.sd-pill-nav[data-floating]` — the bar. The floating variant is sticky at
  `inset-block-start: 16px`, `--sd-radius-pill`, `--sd-shadow-soft`.
- `ul.sd-pill-nav__list` — the row. Wraps rather than scrolls.
- `li.sd-pill-nav__slot` > `button|a.sd-pill-nav__item` — one destination.
  `min-height: 32px`, `padding-inline: 12px`, `--sd-radius-pill`.
- `span.sd-pill-nav__count` — an optional count, mono and tabular. Zero is
  shown, because "0 blocked" is a fact and a missing count is not.

## States

| State | What changes |
|---|---|
| rest | No fill, label in `--sd-ink-2`. |
| hover | Label to `--sd-ink`. 120ms. |
| active / pressed | n/a. Navigation is instant; the destination is the feedback. |
| focus-visible | The shell's ring, `outline-offset: 2px`. |
| disabled | n/a. A destination that cannot be reached is left out of the list. |
| loading | n/a. |
| error | n/a. |
| empty | n/a. A nav with no items is not rendered. |
| selected | `aria-current="page"`: `--sd-accent-soft` fill, `--sd-accent` label, weight 500. |
| RTL | The row mirrors with the page. No rule: the layout is flex and every inset is logical. |

## Tokens used

- `--sd-surface` — the floating bar's ground
- `--sd-shadow-soft` — the floating bar, one of the only three things that carry it
- `--sd-accent-soft` / `--sd-accent` — the current item
- `--sd-ink-2` / `--sd-ink` / `--sd-ink-3` — labels and the count
- `--sd-radius-pill` — the bar and every item
- `--sd-space-1` / `--sd-space-3` / `--sd-space-4` — gap, padding, sticky inset
- `--sd-text-body`, `--sd-text-micro` — label and count
- `--sd-dur-1` — hover

## Do / Don't

- **Do** mark the current page with the fill.
- **Don't** mark it with a border or an underline. A 2px line under one item
  moves the text of every other item by a pixel when the bar wraps, and on a
  page that crosses three colour bands there is no single line for it to sit on.
- **Do** give every nav a `label`. Two navs on one page with the same
  accessible name are indistinguishable in a screen reader's landmark list.
- **Don't** put more than about six items in it. The seventh belongs in a menu.
- **Do** let the app's nav carry counts where a number is what the reader wants
  (blocked runs, unread inbound deliveries).

## Accessibility

Role is `navigation`, named by `label`. Items are buttons when the app
navigates by state and links when the URL is the navigation; both are one tab
stop each in DOM order, with no roving tabindex — a nav is a list of
destinations, not a composite widget. `aria-current="page"` marks the current
one. Contrast: `--sd-accent` on `--sd-accent-soft` over paper stays above
**7:1**; `--sd-ink-2` label is **7.76:1**. Reduced motion: the hover transition
falls to 1ms; nothing else moves.

## Changelog

### 2026-09-15
Added. Initial spec from `styles.css` `.nav` / `.nav-item`. Radius moves 3px to
`--sd-radius-pill`; the current item keeps `--sd-accent-soft` from today's rule;
counts and the floating variant are new.
