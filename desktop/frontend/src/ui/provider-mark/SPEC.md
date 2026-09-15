# Provider mark

## What it is

Which provider is running a session, as a tile: the vendor's own mark in white
on a tile of its brand colour, or on the ink for a vendor whose mark is black.
It says which provider and nothing else.

Built. `desktop/frontend/src/ui/provider-mark/`, used in the app: the board's
run cards, the session topbar, the register table, the Providers settings page
and the sidebar's recent list. The marks are the ones in
`docs/design/2026-09-15-screens/marks/`, as distributed by the Simple Icons and
lobehub icon sets, inlined as path data so the desktop app makes no request
for them.

## Anatomy

- `span.sd-mark[role="img"][data-size][data-provider][data-branded]` — the
  tile. 28 by 28 at `--sd-radius-sm` (`md`); 22 at 6px (`sm`); 40 at 10px
  (`lg`). `direction: ltr`, since a mark is not a word.
- `svg` — the mark on a 24-unit box, 16px in the `md` tile (13 and 22 in the
  others), painted in `currentColor`, `aria-hidden`.
- `span.sd-mark__initials` — the fallback for a provider with no mark: the
  first two letters of its id in the ledger face.

## States

| State | What changes |
|---|---|
| rest | The tile. Branded vendors (Claude, Qwen, Gemini, Antigravity, Copilot) on their colour with a white mark; the rest (Codex, OpenAI, Cursor, OpenCode, Kimi) on `--sd-ink` with the mark in `--sd-primary-ink`. |
| hover | n/a. The tile is not a control; the row it sits in may be. |
| active / pressed | n/a. |
| focus-visible | n/a. Not focusable. |
| disabled | n/a. |
| loading | n/a. The provider is known before the run starts. |
| error | n/a. |
| empty | A provider with no mark shows two letters of its id (`AC` for `acp`) rather than a blank tile, and its accessible name is the id. |
| selected | n/a. |
| RTL | Nothing moves: the tile is `direction: ltr` and its contents are a drawing. The row around it mirrors. |

## Tokens used

- `--sd-ink` / `--sd-primary-ink` — the unbranded tile and its mark, which invert together per theme
- `--sd-surface` — the white mark on a branded tile
- `--sd-radius-sm` — the `md` tile's corner
- `--sd-font-mono`, `--sd-text-micro`, `--sd-text-meta` — the fallback initials

The brand colours are not tokens. They live in `index.tsx` as `--sd-mark-tile`
set inline per vendor, and nowhere else: `styles/tokens.css` names no vendor,
and `src/ui/library.test.ts` holds every stylesheet to that.

## Do / Don't

- **Do** use it wherever a run names its provider: it is the avatar of a
  session.
- **Don't** put state, cost or a model name on it. The state glyph and the
  ledger carry those, and a tile that changed colour with the run would be a
  second status badge in a vendor's colours.
- **Do** keep the brand colours in the marks module. The one place a vendor's
  hex may appear is this component's TSX.
- **Don't** redraw a vendor's mark. They are the vendors' own, used to say
  which provider is running; if a vendor's brand guidelines ever object, remove
  its entry and the tile falls back to the name.
- **Do** pass the run's own provider string. `agy` resolves to Antigravity; an
  id nobody has a mark for gets its initials rather than a wrong mark.

## Accessibility

`role="img"` with `aria-label` of the vendor's name (`title` says the same on
hover), so a screen reader reads "Claude" where a sighted reader sees the mark;
the SVG itself is `aria-hidden`. Contrast is not claimed for the mark: it is a
logo on its owner's colour, not text, and the vendor's name is always available
as the accessible name. The fallback initials are `--sd-primary-ink` on
`--sd-ink`: **17.44:1** light, **15.69:1** dark. No motion.

## Changelog

### 2026-09-15
Added. The official marks, in the inverted form each vendor ships, chosen by
the user over original glyphs on the same day
(`docs/design/2026-09-15-screens/README.md`, "Provider marks"). Brand colours
in the TSX, never in a stylesheet.
