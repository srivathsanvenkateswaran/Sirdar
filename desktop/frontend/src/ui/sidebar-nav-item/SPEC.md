# Sidebar nav item

## What it is

One row of the desktop app's left sidebar: a 20px icon, a 16px label, and an
optional count at the inline end, inside a 44-tall pill.

Built. `desktop/frontend/src/ui/sidebar-nav-item/`. It replaces `.nav-item` in
`styles.css`, which was a pill in a horizontal header bar; the header is gone
and `docs/design/03-desktop-app.md` section 5 is where the sidebar comes from.

## Anatomy

- `button.sd-nav-row` — the whole row is the control. `min-block-size:
  var(--sd-nav-row-h)` (44px), `padding-inline: 14px`, `gap: 12px`, radius
  10px, no border. The shell stacks rows at a 2px gap.
- `span.sd-nav-row__icon` — 20 by 20, `currentColor`, `aria-hidden`. Lucide
  outlines at stroke 1.6 in the app; the component takes whatever node it is
  given.
- `span.sd-nav-row__label` — `--sd-text-body` (16px) weight 500, ellipsised
  rather than wrapped.
- `span.sd-nav-row__count` — the pill count at the inline end, `--sd-badge-bg`
  with `--sd-badge-ink`, `--sd-text-micro` mono tabular on a 20px line. Hidden
  when the count is zero.
- `span.sd-nav-row__count-name` — the count's name, read out and never drawn.

## States

| State | What changes |
|---|---|
| rest | No fill. Label and icon `--sd-ink`. |
| hover | Fill `--sd-nav-hover`, label still `--sd-ink` (13.68:1 light on the active fill, higher on hover). |
| active / pressed | Nothing beyond the hover fill. The row navigates; there is nothing to hold down. |
| focus-visible | The app's ring: `2px solid var(--sd-accent)` at `outline-offset: 2px`. |
| disabled | n/a. A screen a person cannot reach is not listed. |
| loading | n/a. A row does not wait for the screen behind it. |
| error | n/a. |
| empty | n/a. A row with no label is not rendered by the shell. |
| selected | `aria-current="page"`, fill `--sd-nav-active`, label `--sd-ink` (13.68:1 light, 12.19:1 dark). No accent, no border. |
| RTL | Icon leads, label follows, count sits at the inline end — all three from logical properties, so the row mirrors with the window and nothing moves by hand. |

## Tokens used

- `--sd-nav-hover` — the hover fill
- `--sd-nav-active` — the current row's fill
- `--sd-ink` — the label, at rest and current
- `--sd-badge-bg` / `--sd-badge-ink` — the count pill
- `--sd-nav-row-h` — the row's height; `--sd-radius-pill` — the count
- `--sd-space-3` — the icon gap
- `--sd-text-body` / `--sd-text-micro` — label and count
- `--sd-font-ui` / `--sd-font-mono` — label and count
- `--sd-dur-1` — the fill change

## Do / Don't

- **Do** let the current row be neutral. **Don't** give it `--sd-accent-soft`,
  which is what `.nav-item[aria-current="page"]` did in `styles.css`: in Sirdar
  the accent means a live run, and a nav row wearing it competes with the one
  card on the board that earned it.
- **Don't** paint a label `--sd-ink-3`. On `--sd-nav-active` it is 4.12:1, the
  one pair `01-tokens.md` records specifically so nobody reaches for it.
- **Do** keep the count's name in the accessible tree. A bare `3` beside
  "Board" is a number with no noun.
- **Don't** hide a zero behind a grey pill. Nothing waiting is best said by
  nothing being drawn.
- **Don't** animate the pill from row to row. Only the fill changes, over
  `--sd-dur-1`; a thumb travelling down five rows outlasts the click it
  answers.

## The sessions list under the nav

Not a library component — it lives with the shell in
`src/components/shell/SessionsList.tsx` and `sessions.css` — but it is the
other thing the sidebar is made of, and this is where its shape is written
down. The 2026-09-16 picker-sources round rebuilt it on T3 Code's sidebar,
which the user showed as the reference.

- One flat list, newest first. No day headings. The runs still `running`,
  `preparing` or `blocked` come first; then `button.sd-sessions__settled`, a
  "Settled" label in `--sd-ink-3` micro caps with a `--sd-rule` hairline to
  the end and a chevron, `aria-expanded`, which folds the finished rows away
  and remembers the fold in `localStorage` (`sirdar.settledCollapsed`).
  "Show N more" follows the first eight settled rows; every live row shows.
- `button.sd-session-row` — 36 tall, radius 10, `padding-inline: 12px 14px`,
  gap 10. One line: `span.sd-session-row__tile` holding a 16px SourceMark
  (`src/ui/source-mark`) for the number's product, `span.sd-session-row__number`
  (the ticket number in `--sd-font-mono` at `--sd-text-meta`, ellipsised) and
  `span.sd-session-row__age` (`5d`, `19h`, `--sd-ink-3` micro, at the end).
  No title, no kind, no provider. Which number — the tracker's `OMNI-2815`
  or the helpdesk's `#25312` — follows Settings › General's "Sessions show";
  a run with no helpdesk number shows its key under the tracker's mark
  either way.
- `span.sd-session-row__dot` — 7px over the tile's trailing top corner,
  ringed in `--sd-shell`: `--sd-accent` for a live run, `--sd-st-blocked`
  for one waiting on a person, absent otherwise. The row's accessible name
  carries the same state in words (`OMNI-2815, triage, running`).
- Current: `aria-current="page"`, `--sd-nav-active`; hover `--sd-nav-hover`.
  The row itself opens the run.
- `div.sd-session-card[role="tooltip"]` — the hover card. One element,
  drawn once after the list as the sidebar's child (not inside the scroll
  region, not one per row), pinned by `lib/anchor`'s `placeBeside` to the
  row's trailing edge so it opens into the sheet (the leading side when
  there is no room), 280 wide on the dialog's surface (`--sd-surface`,
  `--sd-rule-strong`, `--sd-radius-md`, `--sd-shadow-soft`). The ticket
  title at 15px, then rows of icon and text at 13px: the other number under
  its product's mark with the product's name ("Zoho Desk #25312", or the
  row's own number when there is only one), the kind chip and the state
  word, the provider mark and the model id (`model unknown` when none), a
  folder icon and the workspace. The row carries `aria-describedby` to it
  while it is open. In RTL it opens at the row's trailing (left) edge and
  the folded chevron points into the text.
- Its timing is `useHoverCard` (`components/shell/useHoverCard.ts`), on
  T3 Code's behaviour: the pointer rests `CARD_OPEN_MS` (120ms) on a row
  before the card opens; while one is open the next row takes it with no
  wait; it stays `CARD_CLOSE_MS` (150ms) after the pointer has left both
  the row and the card, so the pointer can cross onto the card and rest
  there. Escape, any scroll and any press close it and it stays closed
  until the pointer re-enters a row. Keyboard focus on a row opens it on
  the same delay and blur closes it, without focus ever gating the pointer;
  the focus a click gives a row is left to the pointer path. Every timer is
  a ref keyed by run id, so the store's re-renders never reset one.
- In the 56px rail (`.sd-sidebar[data-collapsed='true']`, automatic under
  1024 or the reader's own fold on the head's Panel toggle / ⌘B, remembered
  as `sirdar.sidebarCollapsed`) a row is its tile alone in a 36px square,
  the Settled divider is its chevron, "Show N more" reads `+N` (still named
  "Show N more"), and the card carries the row's own number as a first row
  — "Jira OMNI-2815" — before the other one.
- The gallery draws it with `pinnedCard`, which puts the card in the flow
  under the list (`data-static`) instead of beside a row.

## Accessibility

A `button` per row, `aria-current="page"` on the one the reader is on, which is
what a screen reader announces rather than the fill. The count is drawn from
one span and named by a second that is visually hidden, so it reads as
"3 waiting on Board". Contrast: rest and hover `--sd-ink-2` at **6.79:1** light
and **7.16:1** dark; current `--sd-ink` at **13.68:1** and **12.19:1**; the
count **13.86:1** light and **9.72:1** dark. Hit target 28 tall inside a 32
pitch, with the sidebar's own inline padding taking the clickable box past the
32px floor in width. Reduced motion: the fill change goes to 1ms; nothing else
moves.

## Changelog

### 2026-09-16 (hover and panes)
The hover card is rebuilt on `useHoverCard`: 120ms of intent instead of
300, an instant swap from row to row, a 150ms grace that lets the pointer
rest on the card, closed by Escape, scroll or a press; one element drawn
after the list. The sidebar gains the reader's own fold to the 56px rail
(Panel toggle in the head, ⌘B), in which the sessions stay as tiles with
the number on the card.

### 2026-09-16 (sessions list)
The sessions list under the nav is rebuilt on T3 Code's sidebar: one flat
list, a "Settled" fold instead of day headings, one line per session (a 16px
source mark, the ticket number on the "Sessions show" preference, the age),
and a hover card beside the row carrying the title and everything the row no
longer says. Described in "The sessions list under the nav" above.

### 2026-09-15 (v2 register)
Re-scaled to the reviewed mocks: the pill goes from 28 tall at 32 pitch to
`--sd-nav-row-h` (44px), the icon from 16 to 20, the label from 13px to
`--sd-text-body` (16px), the padding from 8 to 14, the radius from
`--sd-radius-sm` to 10px, and the label rests on `--sd-ink` rather than
`--sd-ink-2`. Forced by the type-ladder re-base in `tokens.css`.

### 2026-09-15
Added. Initial spec from `docs/design/03-desktop-app.md` section 5. Replaces
`styles.css` `.nav-item`: the current-row fill moves from `--accent-soft` to
`--sd-nav-active`, the radius from 3px to `--sd-radius-sm`, and the row gains
an icon slot and the inbound count.
