# Status badge

## What it is

A run's state, as a tinted pill carrying the word for that state. Seven status
hues plus a priority variant that shows the tracker's own string verbatim.

It owns the state vocabulary. `STATE_WORDS` maps every state the app names —
`queued`, `preparing`, `running`, `blocked`, `completed`, `done`, `failed`,
`over_budget` — to its one lowercase word, and the state glyph
(`src/ui/state-glyph`) re-exports that same map, so a pill on the register, a
card footer on the board and a tooltip in the sidebar all say the same thing.
`stateWord(status)` gives the word for a string off the wire.

Built. `desktop/frontend/src/ui/status-badge/`. It replaces `.badge`,
`.run-badge` and `.badge--priority` in `styles.css`.

## Anatomy

- `span.sd-badge[data-status]` — the pill. `padding-block: 1px`,
  `padding-inline: 8px`, `--sd-radius-pill`. Fill is a 13% tint of the hue;
  the text is the hue at full strength. The text is the state's word, and with
  `detail` the word followed by ` · ` and what the state means on that screen
  (`blocked · waiting on you` on the session topbar).
- `span.sd-badge.sd-badge--priority[data-priority]` — the priority variant:
  transparent fill, 1px border, mono, the tracker's string unchanged.

## States

| State | What changes |
|---|---|
| rest | The hue named by `data-status`, or the lane's hue when the badge is inside a `.sd-lane`. `completed` is triaged teal; `done` is the deeper done green. |
| hover | n/a. A badge is a fact, not a control. |
| active / pressed | n/a. |
| focus-visible | n/a. Not focusable. |
| disabled | n/a. |
| loading | n/a. A run with no state yet is `queued`. |
| error | The `failed` and `over_budget` statuses, both `--sd-st-failed`. |
| empty | `PriorityBadge` renders nothing at all when the ticket states no priority — an empty pill would claim the tracker said something it did not. |
| selected | n/a. |
| RTL | Nothing changes. The word translates; the hue and the shape do not. |

## Tokens used

- `--sd-st-queue` / `--sd-st-live` / `--sd-st-blocked` / `--sd-st-triaged` / `--sd-st-done` / `--sd-st-failed` — the six hues over seven states
- `--sd-rule-strong` / `--sd-ink-2` — the priority variant's border and text
- `--sd-radius-pill` — the shape
- `--sd-space-2` — the inline padding
- `--sd-text-meta`, `--sd-text-micro` — the word, and the priority string
- `--sd-font-ui`, `--sd-font-mono`

## Do / Don't

- **Do** ship the word with the hue, always. `STATE_WORDS` is part of the
  component for that reason: the badge cannot be rendered without it.
- **Don't** paint a status word on `--sd-sunk`. `--sd-st-blocked` is 4.43:1
  there against 4.84:1 on paper — the one pair in the token file that does not
  clear 4.5 on all three grounds. Lane wells are sunk; the cards inside them
  are surface, which is where badges live.
- **Do** use the map's word on every screen. A screen that wants to say what
  the state means there adds it after the word with `detail`; it does not
  rename the state. `children` is for a translation of the word and nothing
  else.
- **Don't** invent a word for a state in a screen. A state the map lacks is
  added to the map, where the glyph picks it up too.
- **Don't** map the tracker's priority onto a scale of your own. Trackers
  disagree about whether the top is P1, Highest or Blocker, and a mapping hides
  what the ticket says.
- **Do** let a badge inside a board column take the lane's hue, so the rail and
  the badges under it are one signal.

## Accessibility

No role: it is text with a background, and the text is the information. Status
is never colour alone — every badge carries its word, which is also what a
screen reader reads. Contrast, hue on the 13% tint over `--sd-surface`: queue
**5.58:1**, live **8.82:1**, blocked **5.10:1**, triaged **5.93:1**, done
**5.97:1**, failed **6.63:1** in the light theme, all higher in the dark one.
Reduced motion: nothing moves.

## Changelog

### 2026-09-15 (fix round)
Owns the vocabulary. `STATE_WORDS` replaces the capitalised `STATUS_WORDS`;
the words are the mocks' lowercase
ones, `blocked` no longer reads "Needs input", and `done` joins the states with
its own hue. `detail` appends what a state means on one screen; `stateWord()`
resolves a status string. The state glyph reads this map instead of its own.

### 2026-09-15
Added. Initial spec from `styles.css` `.badge`. Radius moves 2px to
`--sd-radius-pill`; the hues deepen to the `--sd-st-*` values so each clears
4.5:1 on warm paper; `queued` gains an explicit `data-status` rather than
falling through to the default hue.
