# Search bar

## What it is

One input with a magnifier before it and a hint after it, in two sizes: the
64-tall bar a session starts from (a ticket key or a URL), and the 40-tall
sunk well the board filters with.

Built. `desktop/frontend/src/ui/search-bar/`, used in the app on the New
session screen (bar) and the Board (well). New in the 2026-09-15 screens
round; the board's filter used to be a bare `.field-input`.

## Anatomy

- `form.sd-search[role="search"][data-variant]` — flex, `gap: 16px`,
  `min-block-size: 64px`, `padding-inline: 24px`, 1px `--sd-rule` border,
  `--sd-radius-sheet` (16px), `--sd-sheet` fill, `--sd-text-body-l`. The
  well: `gap: 8px`, 240 wide, `--sd-control-h` tall, `padding-inline: 12px`,
  no visible border, `--sd-radius-sm`, `--sd-sunk` fill, `--sd-text-body`.
- `label.sd-search__label` — the accessible name, read out and never drawn.
- `span.sd-search__icon` — lucide `search` at 20px (18 in the well),
  `--sd-ink-3`, `aria-hidden`.
- `input.sd-search__input[type="search"]` — borderless, inherits the font,
  placeholder in `--sd-ink-3`.
- `span.sd-search__aside` — optional, at the inline end, `--sd-text-meta` in
  `--sd-ink-2`.
- `inputRef` reaches the input itself, for a shortcut that puts the cursor in
  it (the board's `/`).

## States

| State | What changes |
|---|---|
| rest | As above. |
| hover | Nothing. A field does not react to a pointer passing over it. |
| active / pressed | n/a. |
| focus-visible | The box carries the shell's ring (via `:has(:focus-visible)`), and its border goes to `--sd-rule-strong`; the input's own outline is off so the ring is drawn once. |
| disabled | Native `disabled` on the input, passed through. Not styled apart: a disabled search is not a state the screens have. |
| loading | n/a. The field starts something; the screen shows the result. |
| error | n/a. A key the tracker does not know is reported by the screen, beside Start. |
| empty | The placeholder, in `--sd-ink-3`. |
| selected | n/a. |
| RTL | The magnifier leads and the hint trails from logical flow; the input's text runs in the reader's direction. |

## Tokens used

- `--sd-sheet` / `--sd-sunk` — the bar's and the well's fills
- `--sd-rule` / `--sd-rule-strong` — the bar's border, at rest and focused
- `--sd-accent` — the focus ring
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — text, hint, placeholder and icon
- `--sd-radius-sheet` / `--sd-radius-sm` — the two corners
- `--sd-control-h` — the well's height
- `--sd-space-2` / `--sd-space-3` / `--sd-space-4` / `--sd-space-5`
- `--sd-text-body-l` / `--sd-text-body` / `--sd-text-meta`, `--sd-font-ui`

## Do / Don't

- **Do** give it a `label`. The placeholder is a hint that disappears on the
  first keystroke; the label is what a screen reader has.
- **Don't** use the bar for a filter. The bar starts a session and is 64
  tall to say so; a filter is the well.
- **Do** put the shortcut or the alternative in `aside`: "or paste a URL",
  "/".
- **Don't** nest it in another form. It is a `form` so Enter means Start.

## Accessibility

`role="search"` on the form, `type="search"` on the input, and a visually
hidden `label` that names it. Enter submits through `onSubmit` with the page
never reloading. Contrast: `--sd-ink` on the sheet **17.44:1**; the
placeholder `--sd-ink-3` on the sheet **5.25:1** and on the well's sunk fill
**4.56:1**, the tightest pair the tokens ship. No motion.

## Changelog

### 2026-09-15 (Board build)
`inputRef` joins the props so the board's `/` shortcut can focus the well.

### 2026-09-15
Added, from `.search` in `docs/design/2026-09-15-screens/SessionEmpty.html`
and `.search-well` in `Board.html`.
