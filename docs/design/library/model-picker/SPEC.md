<!--
  Copied from desktop/frontend/src/ui/model-picker/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Model picker

## What it is

The chip that says which provider and model a session will run on, and the
popover that changes it. Used in the composer bar on New session and in the
session composer (read-only there: a steer resumes the same session), and on
Eval's Provider row, where a Change button opens the same popover.

Built. `desktop/frontend/src/ui/model-picker/`. The 2026-09-16 T3 round gave
it the shape of T3 Code's picker on Sirdar's own tokens: a search field, a
rail of provider marks, rows with a provider meta line, ⌘1…⌘9 shortcut
badges, and a star per row for favourites.

The lists it draws from live in `src/lib/models.ts`: a curated list per
provider with "CLI default" first, then the names the research observed on
the wire. Nothing in a list is guessed. The search field is also the
free-text entry: text that answers no row is offered back as one row, "Use
“…” as the model id", and a provider whose names were never seen carries a
hint saying where an id would come from.

## Anatomy

- `span.sd-model-picker` — the wrapper, and the box the popover is anchored
  to (`lib/anchor.ts`). The popover is `position: fixed`, so it adds nothing
  to the wrapper's width.
- `button.sd-model-chip` — the trigger, in the composer bar: a small provider
  mark, `.sd-model-chip__value` (the model's curated label — `Sonnet 5`,
  `CLI default`, or the id itself) and `.sd-model-chip__chevron`. 32 tall,
  `--sd-radius-sm`, no border of its own (the bar draws the hairlines between
  chips), `--sd-nav-hover` on hover and while open. Its accessible name and
  `title` carry the whole pair: `Model claude · Sonnet 5`, or `Model claude ·
  CLI default · last used claude-sonnet-5`. `aria-haspopup="dialog"`,
  `aria-expanded`, `aria-controls`.
- With `trigger="change"` the trigger is a pale Button reading "Change" and
  the row draws the words itself.
- `div.sd-model-picker__popover[role="dialog"]` — non-modal, fixed to the
  viewport beside the trigger: below it when it fits, above when it does not,
  clamped to the window's sides, held to the room on its side (the hook sets
  `top`, `left`, `max-height` and `data-side`). 480 wide or the window less
  16, about 420 tall: the dialog's surface (`--sd-surface`,
  `--sd-rule-strong`, `--sd-radius-md`, `--sd-shadow-soft`).
- `.sd-model-picker__search` — the 44-tall row across the top, on `--sd-sheet`
  with a `--sd-rule` hairline under it: a magnifier and
  `input.sd-model-picker__search-input[type=search]` labelled "Search
  models" (visually hidden). Focused on open. Filters by model label, model
  id, the provider's config name or the vendor's name, across every
  provider; and is where a model id nobody listed is typed.
- `.sd-model-picker__body` — the rail and the list, side by side, 376 tall at
  most; the list is what scrolls.
- `.sd-model-picker__rail[role="tablist"]` — 48 wide, one
  `button.sd-model-picker__provider[role="tab"]` per pickable provider, icon
  only (a small ProviderMark). The accessible name and `title` are the
  vendor's name, with ", workspace default" on the workspace's own. The
  selected tab has a 2px `--sd-accent` rail on its leading edge.
- `.sd-model-picker__list[role="listbox"]` — labelled "Model". Two
  `role="group"`s: "Favourites" (only when there are any) and "Models"
  ("Matches" while a search is typed), each under a
  `.sd-model-picker__heading`. Rows are `div.sd-model-picker__row[role="option"]`,
  44 tall: `.sd-model-picker__star` (a button, "Favourite <label>" /
  "Unfavourite <label>", `aria-pressed`), `.sd-model-picker__text` holding
  `.sd-model-picker__label` (16px, weight 500) over `.sd-model-picker__meta`
  (a 14px mark, the provider's config name, 13px, third ink; the CLI default
  row adds `· last used <id>` when a run has said), `.sd-model-picker__check`
  on the selected row, and `kbd.sd-model-picker__kbd` reading ⌘1 … ⌘9 on the
  first nine rows in display order (`aria-keyshortcuts="Meta+n"`).
- `.sd-model-picker__row--use` — the one row a search that answers nothing
  offers: "Use “<typed>” as the model id", the typed id in the ledger face,
  no star and no key, with `.sd-model-picker__hint` under it naming where a
  model id for this provider was observed. Enter in the search, or on the
  row, takes it.
- `.sd-model-picker__hint--foot` — the same hint at the list's foot for a
  provider whose list is "CLI default" alone, before anything is typed.

## States

| State | What changes |
|---|---|
| rest | Chip in `--sd-ink-2`, value at weight 500, chevron in `--sd-ink-3`. |
| hover | Chip to `--sd-nav-hover` and `--sd-ink`. Row to `--sd-nav-hover`; its star appears. Rail tab to `--sd-nav-hover`, full opacity. |
| active / pressed | n/a. The popover opening is the feedback. |
| focus-visible | The shell's ring on the chip, each tab, each row, each star and the search input itself (`outline-offset: 0`, inside its row). No underline: the row is a hairline, not a control. |
| open | `aria-expanded="true"`, chip drawn as hovered. Focus is in the search field. |
| selected | Rail tab: `aria-selected`, the accent rail, full opacity. Row: `aria-selected`, `--sd-card-row` fill and the check together, never the fill alone. |
| favourite | The star is filled in `--sd-accent` and stays visible; the row floats under "Favourites" and takes the first shortcuts. Kept in `localStorage` under `sirdar.modelFavourites.<provider>`. |
| searching | The heading reads "Matches"; rows from every provider that answer the query, each naming its provider in its meta. Enter picks the first match. When nothing answers, the one row is "Use “<typed>” as the model id", and Enter takes it. |
| nothing chosen | The chip reads `CLI default`; the CLI default row's meta and the chip's name say what the last run on that provider used. |
| workspace model | An override of neither provider nor model shows the workspace's configured model by its label, or the id itself when the list has no label for it (`sonnet` stays `sonnet`; the CLI takes the alias). |
| free text | An id the list lacks is typed in the search and taken from the "Use …" row, trimmed, on the provider the rail has. The chip then reads the id itself, and no row in the list is selected. |
| disabled | The chip at `opacity: .45`; nothing opens. |
| read-only | `readOnly` names the reason: the chip is a disabled button with that reason as its `title` and in its accessible name, at full opacity — a fact, not a control. |
| no provider | The value reads "not set" and no mark is drawn; the popover still opens so one can be chosen. |
| no room below | The popover opens above the chip (`data-side="above"`), or stays below held to the room when that is more; the list scrolls inside. The window never scrolls. |
| loading | n/a. The lists are static. |
| error | n/a. A wrong id is the provider's to refuse when the run starts. |
| empty | n/a. Every list has "CLI default", and a search that finds nothing offers the typed text instead of an empty list. |
| RTL | The popover lines its leading edge up with the trigger's leading (right) edge; the rail is on the inline start, its accent rail on the leading edge; the check and the shortcut key swap sides with the page. The chip's value, each row's meta and the box stay `dir="ltr"`, because a model id is Latin. |

## Tokens used

- `--sd-surface`, `--sd-sheet` — the popover and the search row
- `--sd-rule`, `--sd-rule-strong` — the search row's hairline, the rail, the keys, the popover
- `--sd-card-row`, `--sd-nav-hover` — selected and hovered rows and tabs; the chip while open
- `--sd-accent` — the selected tab's rail, the filled star
- `--sd-ink`, `--sd-ink-2`, `--sd-ink-3` — words
- `--sd-radius-xs`, `--sd-radius-sm`, `--sd-radius-md`, `--sd-radius-pill` — keys, chip and rows, popover, the accent rail
- `--sd-shadow-soft` — the popover
- `--sd-space-1` … `--sd-space-3` — gaps and padding
- `--sd-text-ledger`, `--sd-text-body`, `--sd-text-meta`, `--sd-text-micro` — type
- `--sd-font-ui`, `--sd-font-mono` — the words and the ids
- `--sd-dur-1` — the chip's and the star's fade, 1ms under reduced motion

## Do / Don't

- **Do** apply every choice as it is made. The chip is the truth; picking a
  row closes. Escape and a click outside close the same way and keep the
  choice; the search is cleared for next time.
- **Don't** invent a model name for a list. A name goes in `lib/models.ts`
  only once the research has seen it on the wire; until then the provider
  offers free text and a hint.
- **Do** pass ids through unchanged. The Claude CLI takes `opus`, `sonnet`
  and `haiku` as well as full ids; the picker does not correct a reader who
  typed one.
- **Do** say what "CLI default" turned out to be last time: the CLI default
  row's meta and the chip's name carry the newest run's reported model.
- **Don't** offer `agy`. It is disabled, and a provider a run cannot start on
  is not a choice.
- **Do** make the chip read-only where the choice is already made — a steer
  continues the run the session has, on the model it has.
- **Don't** hang the popover under the chip with `position: absolute`. The
  window is `overflow: hidden`; a popover taller than the room would be cut
  off. Anchor it with `lib/anchor`.

## Accessibility

The trigger is a button with `aria-haspopup="dialog"`, `aria-expanded` and
`aria-controls` naming the popover; its name is the whole pair. The popover
is `role="dialog"`, non-modal, named "Provider and model". Focus goes to the
search field on open and back to the trigger on close, on every path. The
rail is a vertical `tablist` with roving tabindex: ArrowUp / ArrowDown move
the provider and wrap, Home and End jump; each tab's name is the vendor's.
The list is a single-select `listbox`: ArrowDown from the search steps into
it, ArrowUp / ArrowDown move focus, ArrowUp from the first row returns to the
search, Home and End jump, Enter or Space picks the focused row (on the "Use
…" row it takes the typed id), `f` stars it. ⌘1 … ⌘9 (Ctrl on a keyboard without a
command key) pick the first nine rows in display order while the popover is
open; each row states its key with `aria-keyshortcuts`. Escape closes. Tab
is not trapped. The star is a button with `aria-pressed` and a name that
says what pressing it does. The search field has a visually hidden label,
"Search models", and wears the shell's ring. The read-only chip is a
disabled button whose accessible name carries the reason. Contrast: the meta
is `--sd-ink-3` on `--sd-surface`, **5.25:1** light and **5.26:1** dark; the
selected row is `--sd-ink` on `--sd-card-row`, **15.70:1** light and
**12.51:1** dark. Motion: the chip's fill and the star's fade over
`--sd-dur-1`, 1ms under reduced motion.

## Changelog

### 2026-09-16 (picker tidy)
The "Other…" row and its box are gone: the search field is the free-text
entry, and text that answers no row is offered back as "Use “…” as the model
id", with the alias hint under it (and at the list's foot for a provider
with no curated names). The search row lost its 2px accent underline; it
sits on the sheet over a `--sd-rule` hairline, and the input alone takes the
shell's ring. "No model matches" went with the box.

### 2026-09-16 (T3 shape)
The popover is pinned to the viewport by `lib/anchor` rather than hung under
the chip, since the window no longer scrolls. A search field, a rail of
provider marks with the accent on the selected one, rows with the provider
under the label, ⌘1…⌘9 badges that work while the popover is open, and a
star per row keeping favourites in localStorage per provider, floated under
"Favourites". The chip lost its "Model" word and the "claude ·" prefix — the
mark says the provider — and gained a chevron; the whole pair is in its name
and title. Done went: picking a row closes.

### 2026-09-16
Added, for the model-picker round. New component: the Provider and Model
fields under More options and the Eval dialog were selects and a text box,
and the chip they fed was not a control.
