# Kind chip

## What it is

What a run is — `triage`, `rca` or `fix` — as a small tracked chip. The word
is the chip; the fill is a second copy of it for people who can see it.

Built. `desktop/frontend/src/ui/kind-chip/`, used in the app on the board's
run cards and in the session topbar. New in the 2026-09-15 screens round; the
board used to print the kind as a mono word beside the key.

## Anatomy

- `span.sd-kind[data-kind]` — the whole chip. `padding-block: 3px`,
  `padding-inline: 8px`, `--sd-radius-xs`, `--sd-text-micro` at weight 700,
  `letter-spacing: .06em`, uppercase, `dir="ltr"`.

## States

| State | What changes |
|---|---|
| rest | `triage`: `--sd-highlight` with `--sd-highlight-ink`. `rca`: a 16% tint of `--sd-st-triaged` with the hue as text. `fix`: a 16% tint of `--sd-st-blocked` with the hue as text. |
| hover | n/a. Not a control. |
| active / pressed | n/a. |
| focus-visible | n/a. Not focusable. |
| disabled | n/a. |
| loading | n/a. The kind is known before the run starts. |
| error | n/a. A failed run keeps its kind; the state glyph says it failed. |
| empty | n/a. A run has a kind. An unknown kind (`eval`) is drawn verbatim in the triage fill rather than dropped. |
| selected | n/a. |
| RTL | `dir="ltr"` on the chip: the word is a category id, and it stays as written inside an Arabic card. |

## Tokens used

- `--sd-highlight` / `--sd-highlight-ink` — the triage fill and word (13.86:1 light, 9.72:1 dark)
- `--sd-st-triaged` — the RCA word, and its 16% tint
- `--sd-st-blocked` — the fix word, and its 16% tint
- `--sd-radius-xs`, `--sd-space-2`
- `--sd-text-micro`, `--sd-font-ui`

## Do / Don't

- **Do** use it for the kind of a run and nothing else. A chip that sometimes
  means "fix" and sometimes means "failed" cannot be read at a glance.
- **Don't** give it a fourth fill. The three kinds are the CLI's three
  commands; a new kind is a new command first.
- **Do** keep the word. The tint alone does not clear 3:1 against the sheet
  and is not meant to.
- **Don't** use tracked uppercase anywhere else in the app for it. The chip is
  the one place it is a mark and not a label above a value.

## Accessibility

A `span` with the word in it; no role is needed, since the word is the
content. Contrast: `--sd-highlight-ink` on `--sd-highlight` is **13.86:1**
light and **9.72:1** dark; `--sd-st-triaged` and `--sd-st-blocked` on their
own 16% tints over the sheet clear **5.3:1** and **4.6:1** in light, since the
tint barely moves the ground. No motion.

## Changelog

### 2026-09-15
Added, from `.chip` and `.chip[data-k]` in
`docs/design/2026-09-15-screens/Board.html`.
