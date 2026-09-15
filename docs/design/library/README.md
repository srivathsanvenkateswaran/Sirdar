# The Sirdar UI asset library

One folder per component. The folder is the component's whole record: what it is made of,
every state it has, which tokens it reads, what it must not do, a page you can open in a
browser, and a log of what changed.

The convention is Tatak's, adapted. Tatak organises by *round* (`docs/design/<date>-<slug>/`
with a brief, a decisions file, and a contact sheet) and keeps a flat `screens/` folder of
self-contained mockups, with `library/STANDARD.md` recording the vocabulary those rounds
converged on and `library/tokens.css` as the file new work links instead of hand-copying a
`:root`. Sirdar keeps the first two habits and changes the third.

**Keep: derived, not invented.** Tatak's STANDARD.md opens with "Every claim below was
measured out of the 39 mockups". A spec in this library states what the component *is*,
sourced from the code or from an approved example, and marks anything aspirational as
aspirational. A spec that describes a component nobody has built says so in its first line.

**Keep: one tokens file, linked, never copied.** No example in this library declares a
`:root`. Every one of them links `../../../../desktop/frontend/src/styles/tokens.css`.
Tatak's own header explains what happens otherwise: hand-copying also copies mistakes, and
five rounds of it produced five spellings of the same idea.

**Change: organise by component, not by round.** Tatak is a consumer app designed in
discrete rounds, so a round is the natural unit. Sirdar has one app, one landing page, and
one docs site that all have to stay in step for years, so the unit is the component and the
round becomes a line in a changelog. Design rounds still happen and still get a folder at
`docs/design/<date>-<slug>/` with a brief and a contact sheet; they just do not own the
components they touch.

## Folder layout

```
docs/design/library/
  README.md            this file
  index.html           the catalogue: every component, its status, a link to its example
  button/
    SPEC.md
    example.html
    CHANGELOG.md
  status-badge/
    SPEC.md
    example.html
    CHANGELOG.md
  ...
  _kit.css             shared example-page chrome (the dark catalogue page, the frames)
```

Files beginning with `_` are shared partials and are not components. The catalogue's
scanner skips them, which is the same convention Tatak's explorer build uses.

## What SPEC.md contains

Six headings, in this order, every time.

1. **What it is.** One sentence. Then where it is used: which of the three surfaces
   (landing, app, docs), and the file and selector if it already exists in code.
2. **Anatomy.** The parts, named, in DOM order, with the box model that matters (padding,
   gap, min-height, hit target). A labelled list, not a paragraph.
3. **States.** Every state as a row: rest, hover, active/pressed, focus-visible, disabled,
   loading, error, empty, selected, RTL. A state that does not apply is written "n/a" with
   the reason, not omitted, because omission is indistinguishable from an oversight.
4. **Tokens used.** The exact `--sd-*` names, one per line, with what each one paints. If
   the component needs a value that is not a token, this section says so and the component
   does not ship until section 4 of `../01-tokens.md` has been consulted.
5. **Do / Don't.** Pairs. Each "don't" names the failure it prevents and, where one exists,
   the place in the codebase where it already went wrong.
6. **Accessibility.** The role and ARIA, keyboard behaviour, the contrast pair being
   claimed with its measured ratio, and the reduced-motion answer.

Specs are written in plain prose. No component is described as "flexible", "modern", or
"clean".

## What example.html contains

A single self-contained page that opens with a double-click and no build step:

```html
<!doctype html>
<meta charset="utf-8">
<title>Sirdar library - Button</title>
<link rel="stylesheet" href="../../../../desktop/frontend/src/styles/tokens.css">
<link rel="stylesheet" href="../_kit.css">
<style>/* only rules this component needs, copied from or destined for the real CSS */</style>

<h1>Button</h1>
<p class="lede">...</p>

<section data-theme="light">  <!-- every state, labelled, at real size -->
<section data-theme="dark">   <!-- the same grid again -->
<section dir="rtl">           <!-- the same grid a third time -->
```

Three grids, always: light, dark, RTL. A component whose example does not render the RTL
grid is not finished, because Arabic is first-class in Sirdar's notes and RTL bugs are
found by looking, not by reading.

The example renders the component at the size it ships at. A button shown at 3x is a
drawing, not an example.

## What CHANGELOG.md contains

Newest first. One entry per change:

```
## 2026-09-15
Added. Initial spec from desktop/frontend/src/styles.css `.button`, `.button--accent`,
`.button--quiet`. Radius moves 3px to --sd-radius-sm (8px); the accent variant gains
--sd-shadow-hard.
```

An entry says what changed and what it was before. "Polish" and "improvements" are not
entries. When a change is forced by a token change, the entry names the token.

## Rules that hold across every component

- **No component declares a colour.** If the spec needs a hex, the answer is a token.
- **Logical properties only.** `padding-inline`, `margin-inline-start`, `inset-inline-end`.
  The one documented exception is `--sd-shadow-hard`, which flips under `[dir="rtl"]`.
- **Every focusable part gets `:focus-visible` with `2px solid var(--sd-accent)` at
  `outline-offset: 2px`.** No component removes it.
- **Status is never colour alone.** The word is always present.
- **Motion is optional behaviour.** Nothing a component does depends on an animation
  finishing, and every animation has a `prefers-reduced-motion` answer stated in the spec.
- **The example is the proof.** A spec whose example disagrees with it is a bug in the
  spec.

## The first 15 components

In build order. The first five are shared by all three surfaces and unblock the rest.

| # | Component | Surfaces | Status today | Notes for the spec |
|---|---|---|---|---|
| 1 | **Button** (primary, secondary, ghost) | all | exists: `.button`, `.button--accent`, `.button--quiet` | Primary is the only variant carrying `--sd-shadow-hard`, and the only one that may mutate anything. Pressed state translates 1px and shrinks the shadow. Ghost has no border at rest. |
| 2 | **Pill nav** | landing, docs, app | partial: `.nav`, `.nav-item` | Surface-coloured pill row, `--sd-radius-pill`, `--sd-shadow-soft`, sticky, sits over any band. Current item uses `--sd-accent-soft`, not a border. |
| 3 | **Segmented control** | landing, app | new | Two to four options, one track, a sliding thumb at `--sd-dur-2`. Roving tabindex, arrow keys. The reduced-motion answer is that the thumb jumps. |
| 4 | **Card** | all | exists: `.card`, `.panel` | The base box: `--sd-surface`, 1px `--sd-rule-strong`, `--sd-radius-md`, no shadow. Everything else on this list that looks like a box is this component with content. |
| 5 | **Status badge** | app, landing | exists: `.badge`, `.run-badge`, `.badge--priority` | Six status hues plus a priority variant. Hue comes from the lane when inside one, from `data-status` otherwise. Always carries its word. |
| 6 | **Kanban column** | app | exists: `.lane`, `.lane-head`, `.lane-body` | 2px top rail in the lane hue, which is the board's legend. Stacks under 720px. Empty state is prose, not an icon. |
| 7 | **Run card** | app | exists: `.card--run` | Key in mono, title clamped to two lines, reason behind an inline-start rail, footer of badge + elapsed + cost. A live run gets the accent inline-start edge. |
| 8 | **Event row** | app | exists: `.ev` and its eight variants | The ledger row: fixed mono gutter, one-character glyph, a hairline rail down the column, colour on the rail only. The densest thing in the product and the one most likely to be broken by a radius change. |
| 9 | **Note pane** | app | exists: `.pane`, `.md` | Prose at `--sd-measure-prose`. Carries `dir` from `lib/rtl.ts`. Arabic body, English headings, both in one column. The only place the display serif appears inside the app. |
| 10 | **Data table** | app, docs | exists: `.register-table`, `.eval-table` | Tabular figures, hairline rows, sortable headers, sticky header, a horizontal scroll container so the page body never scrolls sideways. |
| 11 | **Toast** | app | exists: `.toast`, `.toasts` | Inline-start 2px accent edge, error tone swaps it for `--sd-st-failed`. 140ms entrance, already reduced-motion guarded. |
| 12 | **Dialog** | app, landing | exists: `.dialog`, `.scrim` | `--sd-radius-md`, `--sd-shadow-soft`, scrim at 32% ink. Focus trap, Escape closes, focus returns to the opener. |
| 13 | **Quota chip** | app, landing | exists: `.quota-meter`, `.quota-bar`, `.cost` | Provider name, a bar, a percentage, a reset time. Mono tabular throughout. Over-budget is the failed hue and says the word. |
| 14 | **Hero band** | landing, docs home | new | Full-bleed, `--sd-radius-band` on the leading corners, paper ink on `--sd-band-deep` or `--sd-band-ink`. Holds a display headline with an italic second clause, one deck line, one button. |
| 15 | **Marquee / ring text** | landing | new | Both ambient motions in one folder because they share the reduced-motion contract and the RTL direction flip. Ring text: a sentence on a circular path, 48s linear. Marquee: one track, 34s linear, 64px edge mask. Neither carries information. |

After these fifteen: Code block, Kbd, Link with marker sweep, Text badge row, Media frame,
Step band, Inbound row, Workspace switcher, Settings panel, Fix panel.

## The index

Fifteen components are built. Each row links the spec in this folder, which is a
copy of the one beside the code; `desktop/frontend/scripts/sync-specs.mjs`
writes the copies and `--check` fails CI when one is stale. The live examples
are not static pages here but the gallery inside the app, at `#/library`, which
renders every component in every state with a light/dark switch and an
LTR/RTL switch — that is the "example is the proof" rule, moved from a folder of
hand-written HTML into the app itself so a specimen cannot drift from the
component it is showing.

| # | Component | Surfaces | Status | Code | Spec |
|---|---|---|---|---|---|
| 1 | Button | all | built | `src/ui/button/` | [button/SPEC.md](button/SPEC.md) |
| 2 | Pill nav | landing, docs, app | built | `src/ui/pill-nav/` | [pill-nav/SPEC.md](pill-nav/SPEC.md) |
| 3 | Segmented control | landing, app | built | `src/ui/segmented-control/` | [segmented-control/SPEC.md](segmented-control/SPEC.md) |
| 4 | Card | all | built | `src/ui/card/` | [card/SPEC.md](card/SPEC.md) |
| 5 | Status badge | app, landing | built | `src/ui/status-badge/` | [status-badge/SPEC.md](status-badge/SPEC.md) |
| 6 | Kanban column | app | built | `src/ui/kanban-column/` | [kanban-column/SPEC.md](kanban-column/SPEC.md) |
| 7 | Run card | app | built | `src/ui/run-card/` | [run-card/SPEC.md](run-card/SPEC.md) |
| 8 | Event row | app | built | `src/ui/event-row/` | [event-row/SPEC.md](event-row/SPEC.md) |
| 9 | Note pane | app | built | `src/ui/note-pane/` | [note-pane/SPEC.md](note-pane/SPEC.md) |
| 10 | Data table | app, docs | built | `src/ui/data-table/` | [data-table/SPEC.md](data-table/SPEC.md) |
| 11 | Toast | app | built | `src/ui/toast/` | [toast/SPEC.md](toast/SPEC.md) |
| 12 | Dialog | app, landing | built | `src/ui/dialog/` | [dialog/SPEC.md](dialog/SPEC.md) |
| 13 | Quota chip | app, landing | built | `src/ui/quota-chip/` | [quota-chip/SPEC.md](quota-chip/SPEC.md) |
| 14 | Hero band | landing, docs home | built | `src/ui/hero-band/` | [hero-band/SPEC.md](hero-band/SPEC.md) |
| 15 | Ring text and marquee | landing | built | `src/ui/ambient/` | [ambient/SPEC.md](ambient/SPEC.md) |

`src/ui/motion/` is not a component. It holds the reduced-motion switch, the
two keyframes and the RTL reversal that ring text and the marquee share; it is
described inside the ambient spec and is skipped by the sync script, the same
way this folder's `_`-prefixed partials are skipped by the catalogue scanner.

Nothing here is restyled into the app's screens yet. The components exist, they
are tested, and they are on show at `#/library`; the branch that points Board,
Run detail, Register, Eval and Settings at them is a separate one, which is why
`styles.css` still carries the rules they will replace.

### Where each spec lives

The original is `desktop/frontend/src/ui/<component>/SPEC.md`, beside the
component, so a change to one is reviewed with the other. The copy in this
folder is generated and opens with a comment saying so. Editing the copy is
lost work: the next sync overwrites it.

### The changelog

Each spec ends with its own changelog rather than carrying a separate
`CHANGELOG.md`, so the record of what changed travels with the description of
what the thing is. The layout sketch above keeps the separate file for the
components that have not been built yet.

## Catalogue

`index.html` lists every component: name, the three-surface flags, status (`spec only`,
`built`, `drifted`), a one-line description, and an Open link to its example. It is written
by hand, newest entries at the bottom since the order is build order, not recency. A
component that is not in `index.html` does not exist.
