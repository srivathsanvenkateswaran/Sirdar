# Event row

## What it is

One line of a run's ledger: the offset in a fixed monospace gutter, a rail with
a one-character glyph, then the content. The densest thing in the product.

Built. `desktop/frontend/src/ui/event-row/`. It replaces `.ev` and its variants
in `components/run/run.css`.

## Anatomy

- `div.sd-event[data-variant][dir="ltr"]` — the row. Grid:
  `3.3rem 1.4rem minmax(0, 1fr)`, `column-gap: 8px`.
- `span.sd-event__at` — the offset from the run's start. Mono, tabular,
  `--sd-ink-3`.
- `span.sd-event__mark` — the rail and its glyph. `border-inline-start: 2px`
  in the variant's hue; `aria-hidden`, because a glyph read aloud is noise.
- `div.sd-event__main` — the content. `overflow-wrap: anywhere`, so a long path
  wraps rather than widening the pane.

## States

| State | What changes |
|---|---|
| rest | Rail in `--sd-rule`, glyph in `--sd-ink-3`. |
| hover | n/a. A row is a record. |
| active / pressed | n/a. |
| focus-visible | n/a on the row; a disclosure button inside one has the shell's ring. |
| disabled | n/a. |
| loading | n/a. A stream with nothing in it yet is prose above the stream, not a row. |
| error | `error` — rail in `--sd-st-failed`. |
| empty | n/a. |
| selected | n/a. |
| variants | `text`, `tool`, `allow` (triaged hue), `deny` (blocked hue), `usage` (strong rule), `state`, `final` (accent), `error` (failed hue). |
| RTL | Nothing changes: the row is `dir="ltr"` and `unicode-bidi: isolate` even inside a note pane laid out right to left. |

## Tokens used

- `--sd-rule` / `--sd-rule-strong` — the default rail and the usage rail
- `--sd-st-triaged` / `--sd-st-blocked` / `--sd-st-failed` / `--sd-accent` — the variant rails
- `--sd-ink` / `--sd-ink-3` — content and gutter
- `--sd-font-mono`, `--sd-font-ui`
- `--sd-space-2` / `--sd-space-4` — the gutter gap and the trailing padding
- `--sd-text-body`, `--sd-text-meta`

## Do / Don't

- **Do** keep colour on the rail only. Eight coloured words down a column
  compete with the text they describe; a coloured rail is legible from the edge
  of vision.
- **Don't** give the row a corner radius. A 12px corner on something that
  repeats four hundred times turns the stream into a stack of pills, and this
  is the component most likely to be broken by a radius change made elsewhere.
- **Do** keep the whole row LTR. Tool names, paths and JSON are unreadable
  mirrored — `lib/rtl.ts` states the same rule for the same reason.
- **Don't** drop the offset gutter to save width. The offset is how a reader
  tells a run that stalled from one that is simply long.
- **Do** keep the glyph one character wide. The gutter is 1.4rem and a second
  character shifts every line.

## Accessibility

No role: the stream is a sequence of records, and the surrounding region names
it. The glyph is `aria-hidden` so the row reads as its offset and its content.
Contrast: content `--sd-ink` **16.54:1** on paper, gutter `--sd-ink-3`
**4.98:1**, and every rail hue clears 4.5:1 on paper and surface — though a
rail is a 2px graphic, where 3:1 is the applicable threshold. Reduced motion:
nothing moves.

## Changelog

### 2026-09-15 (restyle)
Gained a ninth variant, `callout`, for the two lines that mean the run is
waiting on a person — the agent asked a question, and the provider rate-limited
it. It takes `--sd-st-blocked`, which is the hue the blocked lane already
carries for the same fact, and the glyph `?`. Added when Run detail was pointed
at this component: the app's own row had the family and the library did not.

### 2026-09-15
Added. Initial spec from `components/run/run.css` `.ev`. `--run-line` becomes
`--sd-rule`; `--run-ok` becomes `--sd-st-triaged` rather than the accent, so an
allowed tool call and a live run stop wearing the same hue; `border-left`
becomes `border-inline-start`; the row gains an explicit `dir="ltr"`.
