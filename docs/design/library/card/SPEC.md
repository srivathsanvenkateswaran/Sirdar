<!--
  Copied from desktop/frontend/src/ui/card/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Card

## What it is

The base box: a surface, a hairline, a 12px corner, and no shadow. Everything
on the board that looks like a box is this component with different content in
it.

Built. `desktop/frontend/src/ui/card/`. It replaces `.card` in `styles.css` and
is the box `.panel` in `components/panels.css` becomes.

## Anatomy

- `div|button.sd-card[data-tone]` — the box. `padding: 8px`, `gap: 4px`,
  1px `--sd-rule-strong` border, `--sd-radius-md`, `text-align: start`.
- `span.sd-card__head` > `span.sd-card__title` + `span.sd-card__meta` — the
  heading line. The title is UI sans at weight 600; the meta is mono and
  tabular, for a key, a count or a clock.
- `span.sd-card__body` — the content, `--sd-ink-2`.
- `span.sd-card__foot` — badges, chips, anything that wraps.

## States

| State | What changes |
|---|---|
| rest | Surface fill, hairline border. |
| hover | Only when interactive: border to `--sd-accent`, 120ms. |
| active / pressed | n/a. The screen it opens is the feedback. |
| focus-visible | The shell's ring, when the card is a button. |
| disabled | n/a. A card that cannot be opened is rendered as a `div`. |
| loading | n/a. A card with nothing in it yet is not rendered; the lane's empty prose covers it. |
| error | n/a. The `failed` tone states the fact; the text beside it says what happened. |
| empty | n/a. See loading. |
| selected | n/a today. Reserved for the register's row selection. |
| RTL | The tone edge is `border-inline-start`, so it moves to the right-hand side with the text. |

## Tokens used

- `--sd-surface` — the fill
- `--sd-rule-strong` — the border
- `--sd-st-live` / `--sd-st-blocked` / `--sd-st-failed` — the tone edge
- `--sd-accent` — the interactive hover border
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — title, body, meta
- `--sd-radius-md` — the corner
- `--sd-space-1` / `--sd-space-2` — gap and padding
- `--sd-text-body`, `--sd-text-meta` — title and meta
- `--sd-dur-1` — hover

## Do / Don't

- **Do** leave the card flat.
- **Don't** give it a shadow. Twenty cards each casting the same soft grey is a
  texture rather than a hierarchy, and it is the single clearest tell of a
  generated interface. Sirdar's only elevation is the hard offset under a
  primary button and the soft one under a dialog and a toast.
- **Do** make the whole card one button when the whole card opens one thing.
- **Don't** put a second button inside an interactive card. Nesting a control
  inside a control gives a keyboard reader two stops for one destination and
  makes the outer click ambiguous.
- **Do** use `border-inline-start` for the tone edge.
- **Don't** use `box-shadow: inset 2px 0 0`, which is what `.card--run` does
  today: it is a physical offset and stays on the left of an Arabic card.

## Accessibility

A plain card is a `div` with no role — it is a box, and inventing `role="group"`
for it only adds noise. An interactive card is a native `button` with an
`aria-label` that names what it opens in full, because the visible content is a
key and a truncated title. Contrast: title `--sd-ink` on `--sd-surface` is
**17.44:1** light and **13.44:1** dark; body `--sd-ink-2` is **8.19:1** and
**7.08:1**. Reduced motion: only the hover border transitions, and it falls to
1ms.

## Changelog

### 2026-09-15
Added. Initial spec from `styles.css` `.card`. Radius moves 3px to
`--sd-radius-md` (12px); the border moves from `--rule` to `--sd-rule-strong`
so a card reads against the sunk lane behind it; the live edge moves from an
inset box-shadow to `border-inline-start`.
