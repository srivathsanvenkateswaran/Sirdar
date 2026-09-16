# Marker

## What it is

The small chip that ties a claim to the call that produced it: E1…En on
the answer's evidence items and on the tool calls that read or searched
what they cite, C1…Cn on a change's hunks and on the edits that wrote
them. The same id on both sides is the point; clicking one finds the other.

Built. `desktop/frontend/src/ui/marker/`, used by the session blocks
(ToolStep, NoteDocument, AnswerCard, ToolsTable, ChangesView) in every
layout. New in the 2026-09-16 session round.

## Anatomy

- `.sd-marker` — a `span`, or a `button` when `onClick` is given. 20px
  tall, at least 22 wide, `border-radius: 5px`, `--sd-highlight` under
  `--sd-highlight-ink`, `--sd-font-mono` at 11.5px, weight 600.
- `[data-kind]` — `E` or `C`, read off the id's first letter.
- `[data-hot="true"]` — the twin is in view: `--sd-accent` under
  `--sd-primary-ink`.
- `[data-size="sm"]` — 16px tall for a chip beside a `file:line` in prose.

## States

| State | What changes |
|---|---|
| rest | The highlight fill, the ink on it. |
| hover | A button darkens to `--sd-heat-2`; a span does nothing. |
| active / pressed | `aria-pressed` follows `hot`. |
| focus-visible | The shell's ring. |
| disabled | n/a. A marker with no twin is a `span`, not a disabled button. |
| loading | n/a. |
| error | n/a. |
| empty | n/a. The chip always carries its id. |
| selected | `hot`: the accent fill with the primary ink. |
| RTL | The chip is a single word; nothing to mirror. |

## Tokens used

- `--sd-highlight` / `--sd-highlight-ink` — the fill and its ink
- `--sd-accent` / `--sd-primary-ink` — hot
- `--sd-heat-2` — a button under the pointer
- `--sd-font-mono`
- `--sd-dur-1` — the fill's change

## Do / Don't

- **Do** give the same id to both sides of a pairing and nothing else. A
  marker that appears once is decoration.
- **Do** pass `onClick` only when the click goes somewhere. The chip is a
  button only then, so a screen reader is never offered a button that does
  nothing.
- **Don't** invent a third letter. `E` is evidence, `C` is a change; a new
  kind of pairing is a new row in this spec first.
- **Don't** colour the two families apart. The letter says which; the hue
  says only whether the twin is in view.

## Accessibility

A `button` named `Marker E1` with `aria-pressed` for hot; a `span` when it
only labels. The id is the visible text, so the name and the label agree.
Contrast: `--sd-highlight-ink` on `--sd-highlight` is the ink on the
highlight tint, **13.86:1** light and 9.72 dark (the tokens file states
both); `--sd-primary-ink` on `--sd-accent` is **8.8:1** light. Hot is the fill and `aria-pressed` together, never the fill
alone.

## Changelog

### 2026-09-16
Added, from `.mk` in `docs/design/2026-09-16-session/B/`.
