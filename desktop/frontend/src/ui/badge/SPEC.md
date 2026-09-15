# Badge

## What it is

A chip carrying one fact about the account or the workspace: the plan, the
billing mode, which tracker a workspace reads from. Never a run state.

Built. `desktop/frontend/src/ui/badge/`. New as a component; the markup is the
shape `.badge` in `styles.css` already had, but the class is `.sd-fact-badge`
rather than `.sd-badge`, because `.sd-badge` belongs to the Status badge and a
shared class would let a status hue leak onto an account chip.

## Anatomy

- `span.sd-fact-badge` — 20 tall, `--sd-radius-xs`, `padding-inline: 8px`,
  11px weight 500, `--sd-badge-bg` with `--sd-badge-ink`. One line, never
  wrapped.

## States

| State | What changes |
|---|---|
| rest | The fill and the word. That is the whole component. |
| hover | Nothing. A badge is not a control. If it needs a tooltip it is given one, and the tooltip says what the word is about. |
| active / pressed | n/a. |
| focus-visible | n/a. Not focusable. A badge that opens something is a button with a badge in it, not a badge. |
| disabled | n/a. |
| loading | n/a. A fact that is not known yet is not drawn as an empty chip. |
| error | n/a. A failure is not a fact about the account. |
| empty | n/a. A badge with nothing in it is not rendered by its caller. |
| selected | n/a. |
| RTL | `padding-inline` and a logical `min-block-size`, so the chip mirrors with its row and the word lays itself out from its own script. |

## Tokens used

- `--sd-badge-bg` — the fill, which resolves to `--sd-highlight`
- `--sd-badge-ink` — the label, which resolves to `--sd-highlight-ink`
- `--sd-radius-xs` — the corner
- `--sd-space-2` — the inline padding
- `--sd-font-ui`, `--sd-text-micro` — the label

## Do / Don't

- **Do** use it for a fact about the account or the workspace. **Don't** use it
  for a run state: that is the Status badge (5), which carries its own word and
  one of six fixed hues, and anything that could be either is the Status badge.
- **Don't** set the label in uppercase. Sentence case, because the word is one
  a person wrote and a tracked-out capital label is the commonest tell there
  is.
- **Don't** introduce a second fill for a second kind of fact. There is one
  badge hue. A fact that needs a different colour is a fact that means a state,
  which is the Status badge again.
- **Do** give it a `title` when the word alone is ambiguous — `pro`, `api` —
  so the chip says what it is about as well as what it says.

## Accessibility

A `span` with text in it: no role, no tab stop, read in document order as part
of the row it sits in. The word is always present, so the fill is decoration
rather than the message. Contrast: `--sd-badge-ink` on `--sd-badge-bg` is
**13.86:1** light and **9.72:1** dark. Reduced motion: nothing here moves.

## Changelog

### 2026-09-15
Added. Initial spec from `docs/design/03-desktop-app.md` section 9. The
reference's 25-tall lavender chip becomes 20 tall at Sirdar's base, on the
existing highlight pair rather than on a new hue.
