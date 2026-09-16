<!--
  Copied from desktop/frontend/src/ui/source-mark/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Source mark

## What it is

Which tracker or helpdesk a ticket number belongs to, as a tile: the product's
own mark in white on a tile of its brand colour, or two letters of its name on
the ink when no mark is public. It is ProviderMark for the ticket side — the
sessions list, the board card's footer and the session topbar put it before a
number so `OMNI-2815` and `#25312` are told apart at a glance.

Built. `desktop/frontend/src/ui/source-mark/`. The marks are the ones in
`docs/design/2026-09-15-screens/marks/sources/`, fetched from Simple Icons
(`cdn.simpleicons.org/<slug>`) on 2026-09-16 and inlined as path data so the
desktop app makes no request for them. Seven of the thirteen built-in
adapters had one there: Jira, Linear, Zoho (for Zoho Desk), Zendesk, Help
Scout, Intercom and HubSpot. Azure DevOps, Rally, ServiceNow, Freshdesk, Front
and Gorgias did not, and draw their initials, as every `exec` adapter does.

## Anatomy

- `span.sd-source-mark[role="img"][data-size][data-adapter][data-branded]` —
  the tile. 28 by 28 at `--sd-radius-sm` (`md`); 22 at 6px (`sm`); 16 at
  `--sd-radius-xs` (`xs`, the sessions list's row); 40 at 10px (`lg`).
  `direction: ltr`, since a mark is not a word.
- `svg` — the mark on a 24-unit box, 16px in the `md` tile (10, 13 and 22 in
  the others), painted in `currentColor`, `aria-hidden`.
- `span.sd-source-mark__initials` — the fallback for a source with no mark:
  the initials of the display name's first two words (`AD` for Azure DevOps,
  `SN` for ServiceNow, read at the capital), else its first two letters (`JA`
  for Janus), in the ledger face.

## States

| State | What changes |
|---|---|
| rest | The tile. Branded products (Jira, Linear, Zoho Desk, Zendesk, Help Scout, Intercom, HubSpot) on their colour with a white mark; the rest on `--sd-ink` with initials in `--sd-primary-ink`. |
| hover | n/a. The tile is not a control; the row it sits in may be. |
| active / pressed | n/a. |
| focus-visible | n/a. Not focusable. |
| disabled | n/a. |
| loading | n/a. The source is known from the config summary. |
| error | n/a. |
| empty | A source with no mark shows two letters of its name rather than a blank tile. An `exec` adapter handed no name reads as `exec`, so the tile is never empty and never a wrong mark. |
| selected | n/a. |
| RTL | Nothing moves: the tile is `direction: ltr` and its contents are a drawing or two Latin letters. The row around it mirrors. |

## Tokens used

- `--sd-ink` / `--sd-primary-ink` — the unbranded tile and its initials, which invert together per theme
- `--sd-surface` — the white mark on a branded tile
- `--sd-radius-xs`, `--sd-radius-sm` — the `xs` and `md` tiles' corners
- `--sd-font-mono`, `--sd-text-micro`, `--sd-text-meta` — the fallback initials

The brand colours are not tokens. They live in `index.tsx` as
`--sd-source-tile` set inline per product, and nowhere else: `styles/tokens.css`
names no vendor, and `src/ui/library.test.ts` holds every stylesheet to that.
Intercom's tile is the blue of its own app icon (`#286EFA`) rather than the
cyan Simple Icons files it under, on which a white mark is lost.

## Do / Don't

- **Do** pass the adapter as the config names it (`zohodesk`, not `zoho`),
  and the display name from the config summary's `sources` block. The name
  is the accessible name and what the initials are cut from.
- **Don't** put state, a number or a title on it. It says which product; the
  number sits beside it in the ledger face.
- **Do** keep the brand colours in the marks module. The one place a
  product's hex may appear is this component's TSX.
- **Don't** redraw a product's mark. They are the vendors' own, used to say
  where a ticket lives; if a vendor's brand guidelines ever object, remove its
  entry and the tile falls back to initials.
- **Don't** invent a mark for a product Simple Icons lacks. Six adapters and
  every private `exec` tracker are initials on the ink, and that is the
  design, not a gap.

## Accessibility

`role="img"` with `aria-label` of the product's display name (`title` says the
same on hover), so a screen reader reads "Zoho Desk" where a sighted reader
sees the mark; the SVG itself is `aria-hidden`. Contrast is not claimed for
the mark: it is a logo on its owner's colour, not text, and the name is
always available as the accessible name. The fallback initials are
`--sd-primary-ink` on `--sd-ink`: **17.44:1** light, **15.69:1** dark. No
motion.

## Changelog

### 2026-09-16
Added, for the picker-sources round. The sessions list, the board card footer
and the session topbar now show a ticket number under its own product's mark,
and switch between the tracker's and the helpdesk's number on the "Sessions
show" preference in Settings › General.
