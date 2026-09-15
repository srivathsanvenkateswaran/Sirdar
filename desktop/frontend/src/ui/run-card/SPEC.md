# Run card

## What it is

A run as the board shows it, in the Jira-shaped anatomy of the 2026-09-15
screens round: the ticket's title first, a kind chip under it, and a footer
of the state's glyph and word (with a clock while the run is live or waiting)
and, at the other end, the key in the ledger face beside the provider's mark.
The whole card opens the session; a card for a queued ticket, which has no
session yet, is a link to the ticket in the tracker instead, and the control
that would start a paid run is drawn beside the card by the board, never as
the card's own click.

Built. `desktop/frontend/src/ui/run-card/`. It is drawn by
`components/cards/RunCard.tsx`, which does the clock arithmetic the component
refuses to. The old shape — key in the head, a quoted reason behind a rail, a
footer of badge, clock and cost — is retired; the reason and the cost are the
session's.

## Anatomy

- `.sd-run-card[data-status][data-live]` — the card: `--sd-sheet` fill,
  1px `--sd-rule` border, radius 6px, `padding-block: 14px`, `padding-inline:
  16px`, a column aligned to the start. A `button` when `onOpen` is given, an
  `a[target=_blank]` when `href` is, and a `div` with neither; the pointer
  cursor and the hover border belong to the two that do something.
- `span.sd-run-card__title` — `dir="auto"`, `--sd-text-body` (16px) at
  weight 400, `line-height: 1.35`, clamped to two lines. No key above it.
- `span.sd-run-card__kind` — the kind chip (`src/ui/kind-chip`), 10px under
  the title.
- `span.sd-run-card__foot` — flex, space-between, 12px under the chip. On the
  leading side `__state`: the state glyph (`src/ui/state-glyph`) with its
  word in the state's hue and, on a live or blocked run only, the mono
  clock; it is the side that gives way when the card is narrow, and the
  word is what is cut with an ellipsis — the foot never wraps. On the
  trailing side `__who`, which never shrinks: `__key`
  (mono, `--sd-text-ledger`, 500, `--sd-ink-3`, `dir="ltr"`, `nowrap`), the
  assignee's avatar, and the provider mark at `sm`. The key is left out of the
  foot when it is already the title.
- `span.sd-avatar` — the assignee, as a 22px circle of their initials:
  `--sd-sunk` fill, 11px at weight 600 in `--sd-ink-2`, `dir="ltr"`, the whole
  name in `title`. One letter for a one-word name, two when the name has two
  parts; an address is read by its local part, whose `.`, `_`, `-` and `+` are
  word breaks. It is `aria-hidden`, because the card's own label already names
  the person. Nothing at all is drawn without an assignee. Its rules are in
  `Avatar.css`, which the register and the session topbar import too.

## States

| State | What changes |
|---|---|
| rest | Hairline border, flat. |
| hover | Border to `--sd-rule-strong`. |
| active / pressed | n/a. Opening the run is the feedback. |
| focus-visible | The shell's ring on the whole card. |
| disabled | n/a. Every run can be opened, including a failed one. |
| loading | `queued`: the dashed ring and the word, no clock. |
| error | `failed` and `over_budget`: the cross and the word in `--sd-st-failed`. The reason is in the session, not on the card. |
| empty | A run whose tracker has no title shows the key as its title, once: the foot's key is left out, since a card that said the key twice was a card with nothing else to say. |
| selected | n/a today. |
| live | `preparing` and `running` take `border-inline-start: 2px solid var(--sd-accent)`, the play glyph in `--sd-st-live`, and the clock. Nothing else on the board wears the accent. |
| blocked | The question glyph in `--sd-st-blocked` and the clock: how long a person has been asked. |
| unassigned | No avatar at all. An empty circle reads as a person whose name went missing, not as a ticket nobody owns. |
| RTL | The title lays itself out from its own first letter; the key, the clock and the initials stay LTR; the footer's two ends swap from flex, and the accent edge moves to the right with `border-inline-start`. |

## Tokens used

- `--sd-sheet`, `--sd-rule`, `--sd-rule-strong` — the box and its hover
- `--sd-accent` — the live edge
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — title, initials, key
- `--sd-sunk` — the avatar's fill
- `--sd-font-mono`, `--sd-font-ui`
- `--sd-space-2` / `--sd-space-3` / `--sd-space-4` — gaps and inline padding
- `--sd-text-body`, `--sd-text-ledger`
- `--sd-dur-1` — hover

The kind chip, state glyph and provider mark bring their own tokens. The 6px
radius and the 14px block padding are the mock's; one component uses each.

## Do / Don't

- **Do** keep the title first and the key at the foot. A board is read by
  title; the key is looked up second, beside the mark that says who is
  working on it.
- **Don't** put the reason on the card. A blocked card says "blocked" and
  how long; the question itself is the session's banner, where it can be
  answered.
- **Don't** put the cost on the card. Cost belongs to the session topbar and
  the register; on a card it competed with the state for the one number a
  reader takes in.
- **Do** pass a clock only for a live or blocked run, already formatted. The
  component hides it for every other state anyway, so a stale clock cannot
  reach the board.
- **Do** reserve the accent for the live run. It is the product's one accent
  and its meaning is "Sirdar itself, right now".

## Accessibility

Native `button` (or `a`) with `aria-label` of
`"<key>: <title>, <state>, assigned to <name>"` — the key alone when there is
no title, and the assignee clause left off when nobody is assigned — because
the visible title is clamped, the key alone does not say what the run is
about, the state is the one fact a reader scanning a lane by name needs
before deciding to open it, and initials are not a name; `label` overrides it
when the default does not say what the click does. State is carried by the
glyph's word, kind by the chip's word, provider by the mark's `aria-label`,
assignee by the label's own clause; none of the four is colour alone.
Contrast: title `--sd-ink` **17.44:1** on the sheet, key `--sd-ink-3`
**5.25:1**, initials `--sd-ink-2` on `--sd-sunk` **7.11:1** in light and
**8.09:1** in dark, the state hues
**4.84:1** or more (blocked, the tightest) in light. Reduced motion: only the
hover border transitions.

## Changelog

### 2026-09-15 (responsive)
The foot is `flex-wrap: nowrap` and the cut moves inside the glyph: `__state`
is a flex item that shrinks, and it is `.sd-state__word` that ellipsises, so
the icon and the clock keep their width on a 200-wide lane. Under 1200 the
card steps down one notch — 12px padding, the title at `--sd-text-meta`, the
state at `--sd-text-ledger`, the key at `--sd-text-micro`.

### 2026-09-16 (assignee round)
`assignee` joins the props and draws an avatar at the foot, before the
provider mark: a 22px circle of the person's initials over `--sd-sunk`, the
whole name in `title`, and the name spelled out at the end of the card's
accessible label. Nothing is drawn for a ticket nobody owns. The circle's
rules live in `Avatar.css` beside the component, because the register's
assignee column and the session topbar's "assigned to" line draw the same
person the same way.

### 2026-09-15 (fix round)
The accessible name ends in the state's word. A card with no title shows the
key once, as the title, and leaves it out of the foot. The foot's key no
longer wraps: `__who` keeps its width and the state side is the one cut. The
card is a `button` only while `onOpen` is given; `href` makes it a link to
the tracker, opened in the browser, and neither makes it a plain box. The
board's queued ticket is now the link, with Triage a separate button beside
it, so no card body starts a paid run.

### 2026-09-15 (Board build)
`label` joins the props: an accessible-name override for a card whose click
does something other than open a session. The board draws a queued ticket
with it, named "Start triage of <key>: <title>".

### 2026-09-15 (Jira-shaped)
Rebuilt to the Board mock's `.tcard`: sheet fill on a `--sd-rule` hairline at
radius 6, 14/16 padding; the title first at 16px up to two lines with no key
in the head; a kind chip; a footer of state glyph and word with the clock only
while running or blocked, and the mono key beside the provider mark at the
other end. The key-in-the-head, the priority badge, the quoted reason rail,
the status badge, and the cost are gone; `reason`, `priority`, `elapsed`,
`elapsedTitle`, `cost` and `costTitle` leave the props, `provider` and
`clock` join them.

### 2026-09-15 (restyle)
`elapsed` and `cost` gained optional `elapsedTitle` and `costTitle` tooltips,
so the board keeps the exact timestamp behind the relative clock and the turn
and token counts behind the one cost figure. Both strings are still formatted
by the caller; the card does no arithmetic on a clock.

### 2026-09-15
Added. Initial spec from `styles.css` `.card--run` and
`components/cards/RunCard.tsx`. The live marker changes from
`box-shadow: inset 2px 0 0` to `border-inline-start`, which is what makes it
correct in an Arabic pane; the reason rail changes from `border-left` to
`border-inline-start`; the elapsed clock and cost gain `dir="ltr"`.
