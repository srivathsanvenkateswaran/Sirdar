<!--
  Copied from desktop/frontend/src/ui/note-pane/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Note pane

## What it is

The triage note as prose: an English body, the customer's own Arabic, and a
reply draft, at a reading measure. The only place the display serif appears
inside the app, and the only component carrying the marker sweep.

Built. `desktop/frontend/src/ui/note-pane/`. It replaces `.pane` and `.md` in
`components/run/run.css`.

## Anatomy

- `article.sd-note[dir]` — the pane. `max-width: var(--sd-measure-prose)`
  (68ch), `padding: 16px`, line-height 1.6.
- `header.sd-note__head` — `h1.sd-note__title` in the display serif, and
  `p.sd-note__source` (mono, `dir="ltr"`, the note's path on disk).
- `div.sd-note__body` — the rendered markdown: headings in the serif,
  blockquotes behind a `border-inline-start` rail, code in `--sd-sunk`, links
  with the marker sweep.

## States

| State | What changes |
|---|---|
| rest | `dir` comes from `lib/rtl.ts`: `auto` by default, `rtl` when the reader prefers it. |
| hover | Links only: the highlight band wipes from 0 to 100% width over 120ms. |
| active / pressed | n/a. |
| focus-visible | Links take the shell's ring, and the sweep runs on focus as well as hover so a keyboard reader sees the same thing. |
| disabled | n/a. |
| loading | n/a. The run detail screen shows its own line while a note is read from disk. |
| error | n/a. A note that will not parse is reported above the pane. |
| empty | n/a. A run with no note does not render a pane. |
| selected | n/a. |
| RTL | The whole pane flips when the preference is on; blockquote rails, code padding and the marker sweep's start edge all follow, because each is a logical property. An Arabic heading takes weight 600 in place of the Latin italic. |

## Tokens used

- `--sd-measure-prose` — the 68ch measure
- `--sd-font-display` — the title and the body's headings
- `--sd-font-ui` / `--sd-font-mono` — body and the source line
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — body, blockquote, source
- `--sd-rule` / `--sd-rule-strong` — the header rule and the quote rail
- `--sd-sunk` — inline code and code blocks
- `--sd-accent` / `--sd-highlight` — link colour and the marker sweep
- `--sd-radius-xs` / `--sd-radius-sm` — code and code blocks
- `--sd-space-1` … `--sd-space-5`
- `--sd-text-display-m`, `--sd-text-title`, `--sd-text-body-l`, `--sd-text-meta`
- `--sd-dur-1`, `--sd-ease` — the sweep

## Do / Don't

- **Do** leave `dir="auto"` on by default. It resolves each block from its own
  first strong character, which is what a bilingual note needs.
- **Don't** set the pane to `rtl` globally for everyone. The preference is
  per-person and lives in this browser, because two engineers share a workspace
  and read in different directions.
- **Do** set the note's headings in the serif. The note is the one screen a
  person reads rather than scans, and the serif is what says so.
- **Don't** use italic anywhere inside the app. Arabic has no italic and a
  faux slant on Naskh is a defect; in a dense table italic also reads as an
  error state.
- **Do** keep the note's file path `dir="ltr"`. A path mirrored is a different
  path.

## Accessibility

`article` with the note's own `h1`, so the note has a heading structure a
screen reader can navigate. Links keep their underline as well as the sweep —
colour alone is never the only signal — and the sweep runs on `:focus-visible`
too. Contrast: body **16.54:1**, links `--sd-accent` **8.36:1** on paper and
**7.01:1** over the highlight band once it has wiped. Reduced motion: the sweep
falls to 1ms, so the band appears rather than travels.

## Changelog

### 2026-09-15
Added. Initial spec from `components/run/run.css` `.pane` / `.md`. The measure
changes from 74ch to `--sd-measure-prose` (68ch); headings move to
`--sd-font-display`; the marker sweep is new and is defined here until the Link
component lands.
