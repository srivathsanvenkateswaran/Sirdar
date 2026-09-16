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
| 6 | **Kanban column** | app | exists: `.lane`, `.lane-head`, `.lane-body` | A 248-wide card-row well with a 3px rail in the lane hue, which is the board's legend, and a tracked uppercase head. Stacks under 720px. Empty state is prose, not an icon. |
| 7 | **Run card** | app | exists: `.card--run` | Key in mono, title clamped to two lines, reason behind an inline-start rail, footer of badge + elapsed + cost. A live run gets the accent inline-start edge. |
| 8 | **Event row** | app | exists: `.ev` and its eight variants | The ledger row: fixed mono gutter, one-character glyph, a hairline rail down the column, colour on the rail only. The densest thing in the product and the one most likely to be broken by a radius change. |
| 9 | **Note pane** | app | exists: `.pane`, `.md` | Prose at `--sd-measure-prose`. Carries `dir` from `lib/rtl.ts`. Arabic body, English headings, both in one column. The only place the display serif appears inside the app. |
| 10 | **Data table** | app, docs | exists: `.register-table`, `.eval-table` | Tabular figures, hairline rows, sortable headers, sticky header, a horizontal scroll container so the page body never scrolls sideways. |
| 11 | **Toast** | app | exists: `.toast`, `.toasts` | Inline-start 2px accent edge, error tone swaps it for `--sd-st-failed`. 140ms entrance, already reduced-motion guarded. |
| 12 | **Dialog** | app, landing | exists: `.dialog`, `.scrim` | `--sd-radius-md`, `--sd-shadow-soft`, scrim at 32% ink. Focus trap, Escape closes, focus returns to the opener. |
| 13 | **Quota chip** | app, landing | exists: `.quota-meter`, `.quota-bar`, `.cost` | Provider name, a bar, a percentage, a reset time. Mono tabular throughout. Over-budget is the failed hue and says the word. |
| 14 | **Hero band** | landing, docs home | new | Full-bleed, `--sd-radius-band` on the leading corners, paper ink on `--sd-band-deep` or `--sd-band-ink`. Holds a display headline with an italic second clause, one deck line, one button. |
| 15 | **Marquee / ring text** | landing | new | Both ambient motions in one folder because they share the reduced-motion contract and the RTL direction flip. Ring text: a sentence on a circular path, 48s linear. Marquee: one track, 34s linear, 64px edge mask. Neither carries information. |

## The app-shell six

Added from `../03-desktop-app.md`, which reads the desktop app language off a capture of
Wispr Flow's macOS settings screen. Five are new and Badge extends the markup `.badge`
already has. They come after the fifteen because each one is the Card, the Dialog or the
Button with a job, and none of them can be specified before those are settled.

| # | Component | Surfaces | Status today | Notes for the spec |
|---|---|---|---|---|
| 16 | **Sidebar nav item** | app | new | Icon 16 plus label 13/500 in a 28-tall pill at 32 pitch, inset 8. Rest `--sd-ink-2`, hover `--sd-nav-hover`, current `--sd-nav-active` with `--sd-ink`. No accent, no border, and never `--sd-ink-3`, which clears only 4.12:1 on the active fill. |
| 17 | **Sidebar footer card** | app | new | The block pinned under a `--sd-rule` hairline at the foot of the sidebar: workspace switcher row, one quota chip per budgeted provider, then the screen's primary button at full content width. `--sd-card-row` fill, `--sd-radius-md`, no shadow. |
| 18 | **Modal sheet with secondary nav** | app | new; extends Dialog (12) | The large Dialog variant: `min(900px,92vw)` by `min(640px,88vh)`, centred, `--sd-scrim` behind it, a 200-wide `--sd-card-row` nav column on the leading edge carrying the modal's leading corners, and a `--sd-sheet` content panel at 32 padding with the app's one serif heading. Settings is the only screen that gets one. |
| 19 | **Setting row** | app | new | One setting per row, 64 tall, inside a `--sd-card-row` card: label 13/500 over its current value in `--sd-ink-2` on the leading side, exactly one control on the trailing side, rows split by a `--sd-rule-faint` divider inset 16 and none after the last. Two controls means two rows. |
| 20 | **Heatmap** | app | new | Runs per day above the Register table: 12px cells, 4px gaps, weekday rows by week columns, 12 weeks in a scroll container, five `--sd-heat-*` steps. Every cell is a button with an accessible name like `14 September, 6 runs`, and the legend prints bucket boundaries as numbers, not only More and Less. |
| 21 | **Badge** | app, landing | partial: shares markup with `.badge` | The account or workspace chip: 20 tall, `--sd-radius-xs`, padding-inline 8, 11/500 sentence case, `--sd-badge-bg` with `--sd-badge-ink` at 13.86:1 light and 9.72:1 dark. It carries a fact about the account, never a run state; anything that could be either is the Status badge (5). |

After these twenty-one: Code block, Kbd, Link with marker sweep, Text badge row, Media
frame, Step band, Inbound row, Fix panel. Workspace switcher and Settings panel leave that
list: the switcher is a row inside 17 and the settings panel is 18 and 19 together.

## The index

All thirty-six are built. Each row links the spec in this folder, which is a
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
| 16 | Sidebar nav item | app | built | `src/ui/sidebar-nav-item/` | [sidebar-nav-item/SPEC.md](sidebar-nav-item/SPEC.md) |
| 17 | Sidebar footer card | app | built | `src/ui/sidebar-footer-card/` | [sidebar-footer-card/SPEC.md](sidebar-footer-card/SPEC.md) |
| 18 | Modal sheet with secondary nav | app | built | `src/ui/modal-sheet/` | [modal-sheet/SPEC.md](modal-sheet/SPEC.md) |
| 19 | Setting row | app | built | `src/ui/setting-row/` | [setting-row/SPEC.md](setting-row/SPEC.md) |
| 20 | Heatmap | app | built | `src/ui/heatmap/` | [heatmap/SPEC.md](heatmap/SPEC.md) |
| 21 | Badge | app, landing | built | `src/ui/badge/` | [badge/SPEC.md](badge/SPEC.md) |

### The screens-round ten

Added on 2026-09-15 with the reviewed mocks in `../2026-09-15-screens/`, which
re-based the app on the reference's 16px register. Each is a piece the mocks
draw on more than one screen; the components above were re-scaled at the same
time (40px controls, 72px setting rows, 48px table rows, 14px heat cells, the
16px sheet) and each one's changelog says what moved.

| # | Component | Surfaces | Status | Code | Spec |
|---|---|---|---|---|---|
| 22 | Provider mark | app | built | `src/ui/provider-mark/` | [provider-mark/SPEC.md](provider-mark/SPEC.md) |
| 23 | Banner | app | built | `src/ui/banner/` | [banner/SPEC.md](banner/SPEC.md) |
| 24 | Group label | app | built | `src/ui/group-label/` | [group-label/SPEC.md](group-label/SPEC.md) |
| 25 | Item row | app | built | `src/ui/item-row/` | [item-row/SPEC.md](item-row/SPEC.md) |
| 26 | Search bar | app | built | `src/ui/search-bar/` | [search-bar/SPEC.md](search-bar/SPEC.md) |
| 27 | Stat card | app | built | `src/ui/stat-card/` | [stat-card/SPEC.md](stat-card/SPEC.md) |
| 28 | Toggle | app | built | `src/ui/toggle/` | [toggle/SPEC.md](toggle/SPEC.md) |
| 29 | Page head | app | built | `src/ui/page-head/` | [page-head/SPEC.md](page-head/SPEC.md) |
| 30 | Kind chip | app | built | `src/ui/kind-chip/` | [kind-chip/SPEC.md](kind-chip/SPEC.md) |
| 31 | State glyph | app | built | `src/ui/state-glyph/` | [state-glyph/SPEC.md](state-glyph/SPEC.md) |
| 32 | Diff view | app | built | `src/ui/diff-view/` | [diff-view/SPEC.md](diff-view/SPEC.md) |
| 33 | Model picker | app | built | `src/ui/model-picker/` | [model-picker/SPEC.md](model-picker/SPEC.md) |
| 34 | Source mark | app | built | `src/ui/source-mark/` | [source-mark/SPEC.md](source-mark/SPEC.md) |
| 35 | Panel toggle | app | built | `src/ui/panel-toggle/` | [panel-toggle/SPEC.md](panel-toggle/SPEC.md) |

### The logo round

Added on 2026-09-16 with the chosen mark in `../2026-09-16-logo/`, which put
Sirdar's own logo into the app for the first time — the Wails default "W" was
the icon until then.

| # | Component | Surfaces | Status | Code | Spec |
|---|---|---|---|---|---|
| 36 | Brand mark | app | built | `src/ui/brand-mark/` | [brand-mark/SPEC.md](brand-mark/SPEC.md) |

`src/ui/motion/` is not a component. It holds the reduced-motion switch, the
two ambient keyframes and the RTL reversal that ring text and the marquee
share, and since 2026-09-15 the app's two entrances as well — the sheet's
content rising 8px over 160ms on the window's first paint (a move between
screens after that is drawn in place), and the settings modal scaling from
0.98 over 200ms behind a 120ms scrim, both frozen under reduced motion.
It is described inside the ambient spec and is skipped by the sync script, the
same way this folder's `_`-prefixed partials are skipped by the catalogue
scanner.

Every screen is on the library now. Board draws its lanes with Kanban column
and its cards with Run card and Card; Run detail's ledger is Event row, its
note is Note pane and its state is Status badge; Register is Data table with
Heatmap above it; Eval is Data table; and Settings is Modal sheet with Setting
row inside it. The rules those components replaced are gone from `styles.css`,
`components/panels.css` and `components/run/run.css` rather than left beside
them, and `src/styles.library.test.ts` holds the app's own stylesheets to the
same two rules this file states for the library: no component declares a
colour, and logical properties only.

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
