# The desktop app language

`00-design-language.md` was derived from wisprflow.ai, a marketing page. This file is
derived from a capture of the same product's macOS app, settings screen open, read on
2026-09-15. The capture is reference material and is not in this repository.

The app is a different register of the same language. Everything below says what changed
and what Sirdar takes.

## 1. How the measurements were taken

The capture is 2700x1688 device pixels at 2x, so the window is **1350 x 844 CSS px**. Every
number in this file is CSS px, halved from the pixel measurement. macOS traffic lights
measured 24 device px across, which is 12pt, which is what fixes the scale factor.

The app behind the settings modal is dimmed. The scrim solves as `#1A1A1A` at **31%**: pure
white reads back as `#B8B8B7` and `#1A1A1A` reads back as itself, which is two equations for
the two unknowns. The green traffic light checks it one channel deep, `#28C840` predicting
146 in green against a measured 146; its red and blue do not match, because the window is
not frontmost and macOS desaturates the lights. Every colour quoted for a dimmed region
below is the value with that scrim removed, and is marked *reconstructed*.

## 2. What the app does that the website does not

**The ground is grey and the content is white.** The website is one warm cream with
full-bleed colour bands cut into it. The app is a single warm off-white window ground,
reconstructed `#F8F6F3`, with one white content sheet floating on it at `#FFFFFF`. The step
between them is **1.08:1**. That is barely a colour difference and it is doing the entire
job of the website's bands: it says *this is the chrome, that is the work*.

**There are no bands at all.** Not one full-bleed colour section, no deep green, no black.
The only saturated colour in the whole window is the heatmap and one teal percentage chip.

**The header is gone.** The website's floating pill nav is replaced by a 216px left sidebar
of icon-and-label rows. Nothing is sticky, nothing floats, nothing is pill-shaped except
the badge.

**There is no shadow anywhere.** Not under the sheet, not under the settings modal, not
under the primary button. Separation is done with a 1px hairline (`#F0EEE6` around the
sheet, 1.08:1 against the ground) and with the scrim behind the modal. The website's hard
offset shadow does not appear in the app at all.

**Type is smaller and the serif is rationed.** The serif appears once per screen, on the
page heading, at 28px. Everything else is sans at 15 to 16px.

The one-line version: the app is the website with the volume down, the colour removed, and
the chrome moved to the left edge.

## 3. Measured from the capture

| Thing | Measurement |
|---|---|
| Window | 1350 x 844 |
| Window ground | `#F8F6F3` reconstructed |
| Content sheet | `#FFFFFF`, 1px `#F0EEE6` border, inset 53 top (title bar), 9 right, 9 bottom, flush left against the sidebar |
| Sidebar | 216 wide, no fill of its own, sits directly on the window ground |
| Sidebar row pitch | 40; active pill 36 tall, inset 12 from the window edge |
| Sidebar active pill | `#F0EEE6` reconstructed, **1.08:1** against the ground, no border, no accent |
| Sidebar label | 16px, cap height 11.5, ink `#1A1A1A` |
| Account card | 12 from the window edge, 159 tall, fill `#FCF5FF`, 1px `#F3E5FF` border, both reconstructed |
| Primary button | 135 x 36, fill `#1A1A1A`, radius 6, 16 inside the card |
| Plan badge | 70.5 x 25, fill `#EFDBFF`, radius 6, label `#1A1A1A` at 12px, **13.46:1** |
| Settings modal | 961 x 641, centred both axes, top 101, scrim `#1A1A1A` at 31% |
| Modal secondary nav | 210 wide, fill `#F5F4F1`, row pitch 36, active pill `#EEEBE4` inset 8 each side |
| Modal content panel | `#FFFFFF`, 48 padding |
| Page heading | serif, cap height 20, so about 28px, ink `#1A1A1A` |
| Setting card | 655 wide, fill `#F5F4F1` (**1.10:1** on the sheet), radius about 10 |
| Setting row | 86 tall, card padding-inline 20, divider 1px `#EEEBE4` (**1.08:1** on the card) inset 20 from each card edge |
| Row label / body | 15px, `#1A1A1A` (**15.82:1**) over `#30302F` (**12.01:1**) |
| Change button | 164 x 36, fill `#EEEBE4`, radius 7, label `#30302F` at 15px, **11.10:1** |
| Heatmap cell | 16 x 16, 8 gap, 24 pitch |
| Heatmap ramp | `#C2D8D6`, `#8ABBB1`, `#4D877E`, `#3B6E6A`, all reconstructed; empty cell `#F0EEE6` |
| Version string | `#ACAAA5` on `#F5F4F1`, **2.11:1** |

That last row is the one thing in the capture Sirdar copies the opposite of. A 2.11:1
version string is unreadable, and Sirdar's floor is 4.5:1 for every string including this
one.

## 4. Window ground and sheet in Sirdar

Sirdar's paper is already warm, so the pair inverts cleanly rather than being restated in
grey.

| Role | Token | Light | Dark |
|---|---|---|---|
| Window ground, behind everything | `--sd-shell` | `#F2EEE0` | `#0C0D11` |
| The content sheet | `--sd-sheet` | `#FFFDF7` | `#191A20` |

Sheet against shell is **1.14:1** light and **1.12:1** dark, close to the reference's 1.08
and enough to read as two planes. `--sd-paper` keeps its job on the landing page and the
docs site and stops being the app's background; the app's background is `--sd-shell`.

The sheet holds the screen: the board and its lanes, the run detail split, the register
table, the eval table. The sidebar sits on the shell with no fill, as in the reference. The
title bar strip above the sheet is shell, 40px in Sirdar rather than 53 because the app's
base size is 13px, not 16px.

No shadow under the sheet. A 1px `--sd-rule` border and the colour step do the separation,
which is the rule `00-design-language.md` section 6 already states for board cards and is
now the rule for the shell too.

## 5. Sidebar navigation

The header, the pill nav and the workspace select move to a left sidebar. This is the
largest structural change to the app and the reason for it is the same reason the reference
did it: a board that scrolls horizontally has no room to spare at the top, and a nav that
does not move is one less thing that can cover a lane.

```
+--------------+---------------------------------------------+
| SIRDAR   [3] |                                             |
|              |   Board                                     |
| [] Board     |   +-----------------------------------+     |
| [] Register  |   |  lanes, cards, inbound strip      |     |
| [] Eval      |   |                                   |     |
| [] Library   |   |                                   |     |
| [] Settings  |   |                                   |     |
|              |   |                                   |     |
|              |   +-----------------------------------+     |
| -----------  |                                             |
| acme-support |                                             |
| anthropic 62%|                                             |
| openai    9% |                                             |
| [ New triage]|                                             |
+--------------+---------------------------------------------+
```

**The five items, in order, with their icon.** Icon names are Lucide, 16px, stroke 1.5,
`currentColor`.

| Item | Icon | What it is |
|---|---|---|
| Board | `columns-3` | the kanban, the default screen, the inbound strip lives inside it |
| Register | `rows-3` | every run ever, as a table, with the runs-per-day heatmap above it |
| Eval | `flask-conical` | the eval suite and its last result |
| Library | `book-marked` | saved prompts, fix recipes, workspace playbooks |
| Settings | `sliders-horizontal` | opens the modal in section 6, does not change the screen behind it |

Settings is a nav row that opens a modal rather than a screen. That is what the reference
does and it is right: settings is a place you leave, and keeping the board painted behind
the scrim tells you that you are coming back.

**Geometry.** Sidebar 208 wide (216 in the reference, one step down for Sirdar's 13px
base). Row pitch 32, pill height 28, icon 16, gap 8 between icon and label, label 13px
weight 500, pill inset 8 from the window edge and 8 from the sidebar's inner edge. Collapsed
rail at 56 wide, icons only, label as a tooltip, below 900px window width.

**States.** Rest label `--sd-ink-2`. Hover fill `--sd-nav-hover`, label `--sd-ink-2`
(**6.79:1** light, **7.16:1** dark). Current fill `--sd-nav-active`, label `--sd-ink` at
weight 500 (**13.68:1** light, **12.19:1** dark). The current row carries no accent and no
border. `--sd-ink-3` is never used on a nav pill: it clears only 4.12:1 there.

The accent is deliberately absent from navigation. In Sirdar the accent means a live run,
and a nav row that wears it competes with the one card on the board that has earned it.
The reference makes the same call for a different reason and the result is the same: the
only coloured thing in the window is the data.

**A count badge** sits inline-end in the Board row when the inbound strip is not empty: a
`--sd-radius-pill` chip, 11px mono tabular, `--sd-badge-bg` fill. It is the only number in
the sidebar above the footer.

### Sidebar footer

Below a `--sd-rule` hairline, pinned to the bottom of the sidebar, in this order:

1. **Workspace switcher.** The current workspace name at 13px weight 500, a `chevrons-up-down`
   icon at the inline end, the whole row a 28-tall button with the same hover fill as a nav
   row. It opens a popover list, not a native select. The reference puts the account card
   here and this is Sirdar's version of that card.
2. **Quota chips.** One row per provider that has a budget: provider name in 11px mono, a
   4px-tall bar at `--sd-radius-pill` filled to the used fraction, the percentage in 11px
   mono tabular at the inline end. Bar fill `--sd-accent`, track `--sd-sunk`. Over budget
   swaps the fill to `--sd-st-failed` and the row gains the word `over`, because status is
   never colour alone. These are the existing `.quota-meter` component moved, not a new one.
3. **New triage**, the primary button, full width of the sidebar content, 28 tall. See
   section 7.

The reference's version string and its sync-state cloud icon go in the settings modal
footer instead of the sidebar, where 4.5:1 is affordable at 11px.

## 6. Settings as a modal sheet

Settings is a dialog, not a screen. Measured from the reference and translated:

- **Size.** 71% of window width and 76% of height in the reference (961 x 641 in a 1350 x 844
  window). Sirdar clamps rather than scaling: `min(900px, 92vw)` by `min(640px, 88vh)`,
  centred both axes.
- **Scrim.** `--sd-scrim`, which is `--sd-ink` at 32% light and `#07080A` at 56% dark. The
  reference measured 31%. The board stays legible behind it, which is the point.
- **Shape.** `--sd-radius-md`, `--sd-sheet` fill, 1px `--sd-rule`, no shadow. The reference
  has no shadow under its modal either; the scrim is the elevation.
- **Secondary nav.** A 200-wide column at `--sd-card-row`, flush to the modal's leading
  edge, full height, with the modal's leading corners. Rows at 32 pitch, 28-tall pill inset
  8 each side, fill `--sd-nav-active`. Two groups separated by a `--sd-rule-faint` hairline
  and a group label at 11px, `--sd-ink-3`, letter-spacing 0.06em.
- **Groups.** `Workspace` (General, Providers, Budgets, Sources, Eval) and `Account`
  (Identity, Keys, Data and privacy). The version string and the last-sync time sit at the
  bottom of this column at 11px `--sd-ink-3`, which is **4.60:1** on `--sd-card-row` and is
  where the reference fails.
- **Content panel.** `--sd-sheet`, 32 padding (48 in the reference, one step down for
  Sirdar). A serif page heading at 1.5rem, then setting cards. The heading is the only
  serif in the app, as `00-design-language.md` section 4 already allows for the note pane.
- **Keyboard.** Escape closes, focus traps, focus returns to the Settings nav row, arrow
  keys move within the secondary nav. That is the existing Dialog component's contract.

The same modal pattern serves New triage and Fix, which are today's dialogs. Settings is
the large variant: a secondary nav on the leading edge. Nothing else in the app gets one.

## 7. Setting rows and the card

A settings page is a stack of cards. Each card is a group of rows sharing one heading; each
row is one setting.

**Card.** `--sd-card-row` fill, `--sd-radius-md`, no border, no shadow. On `--sd-sheet` it
is **1.11:1** light and **1.12:1** dark, matching the reference's 1.10.

**Row.** 64 tall in Sirdar (86 in the reference, scaled to the 13px base), padding-inline 16.
Leading side: label at 13px weight 500 `--sd-ink` (**15.70:1** light, **12.51:1** dark),
then the current value at 13px `--sd-ink-2` on the line below (**7.37:1** / **6.60:1**).
Trailing side: one control. Rows are separated by a 1px `--sd-rule-faint` divider inset 16
from each card edge, never a full-bleed line, and the last row has none.

**The row control.** One per row, and it is the pale button the reference uses: fill
`--sd-nav-active`, `--sd-radius-sm`, 28 tall, padding-inline 12, label 13px `--sd-ink-2`.
It is flat, it is quiet, and it is the same shape whether it opens a picker, a file dialog
or a sub-page. Select controls that need to show their own value use the existing input
style instead, at `--sd-sheet` with a 1px `--sd-rule-strong`, which is what the reference
does for its one dropdown row.

A row never carries two controls. A setting that needs two is two rows.

## 8. One black button per screen

The reference has exactly one filled black button in the window, and it is the only thing
that spends money. Sirdar adopts that as a rule with teeth.

`--sd-primary` is the ink colour, inverted per theme: `#17181C` light with `#FFFDF7` label
at **17.44:1**, `#E9E7DF` dark with `#0C0D11` label at **15.69:1**. Hover is
`--sd-primary-hover`, `#2B2D33` / `#D5D2C6`, **13.53:1** / **12.82:1**. Radius
`--sd-radius-sm`; the reference measured 6, which is not a pill despite reading as one at a
glance.

**At most one per screen, and it is the screen's commit action.**

| Screen | The one button |
|---|---|
| Board | New triage (in the sidebar footer) |
| Run detail | Resume, or Answer when the run is blocked |
| Register | none; the register is read-only and has no commit |
| Eval | Run suite |
| Settings modal | Save, in the modal footer, disabled until something changes |

If a screen has no commit action it has no black button, and the Register having none is
the proof that the rule is real rather than decorative.

This replaces the hard offset shadow as the app's mutation marker. The shadow stays on the
landing page where it belongs to the printed look; inside the app a filled black button is
a clearer signal at 13px than a 2px shadow, and the reference is right about that. Section
6 of `00-design-language.md` should be read as: hard shadow on the landing page, filled ink
button in the app, both meaning the same thing.

## 9. Badges

The reference's plan badge is a lavender chip with black text at 13.46:1, 25 tall, radius
6, sitting beside the wordmark. Sirdar's badge is the same object on Sirdar's own hue:
`--sd-badge-bg` and `--sd-badge-ink`, which resolve to the existing highlight pair
(`#E4E0FF` with `#17181C` at **13.86:1** light, `#2E2A55` with `#DDD8FF` at **9.72:1**
dark). No new hue enters the palette.

Badges carry a fact about the account or the workspace, never a run state. Run state is the
existing status badge with its six hues and its word. A badge that could be either is the
status badge.

Geometry: 20 tall, `--sd-radius-xs`, padding-inline 8, 11px weight 500. Sentence case, not
uppercase.

## 10. Runs per day, on the Register

The reference puts a streak heatmap on its home screen: 16px cells, 8px gaps, a four-step
teal ramp plus an empty cell, a `More`/`Less` legend under it, and a month label with a
next-month chevron.

Taken honestly, that is a calendar of activity, and Sirdar has one worth showing: how many
runs happened each day. It goes above the Register table, not on the Board, because the
Board is about what is happening now and the Register is about what happened.

**Spec.** Rows are weekdays, columns are weeks, 12 weeks visible by default with a
horizontal scroll container. Cell 12 x 12 with a 4px gap (16/8 in the reference, scaled to
Sirdar's density), `--sd-radius-xs`. The ramp is five steps of Sirdar's accent:

| Step | Meaning | Light | Dark |
|---|---|---|---|
| `--sd-heat-0` | no runs | `#EEEADB` | `#232630` |
| `--sd-heat-1` | 1 run | `#CFCBEE` | `#34315F` |
| `--sd-heat-2` | 2 to 4 | `#A7A2DE` | `#4C4894` |
| `--sd-heat-3` | 5 to 9 | `#6E69C4` | `#736ECF` |
| `--sd-heat-4` | 10 or more | `#3B3AA6` (`--sd-accent`) | `#A6A2FF` (`--sd-accent`) |

Consecutive steps separate by 1.26:1 to 1.98:1, and step 0 separates from the sheet by
1.18:1 light and 1.15:1 dark. That is a sequential ramp, not a set of categories, so
neighbouring pairs are not held to 3:1; what the floor applies to is the text. Every cell
is a `<button>` with an accessible name reading `14 September, 6 runs`, the count is in the
tooltip and in the day's row when you click through to the filtered table, and the legend
prints the bucket boundaries as numbers rather than only `More` and `Less`. Colour is the
summary, never the only copy of the fact.

**What is not taken.** The word streak, the flame, the "longest streak" counter, the
next-month chevron that implies a future. A support engineer's good week is a week with few
runs. Counting consecutive days of work as an achievement is a habit-app idea and it would
be a lie on this screen.

The reference's grid mixes warm grey cells (`#C7C2B7` reconstructed) with teal ones, and a
single dimmed frame does not say what separates them. Sirdar's grid uses one ramp and no
second series.

## 11. Motion in the app

The landing page's ambient motions (ring text, marquee, curved caption, band radius) do not
exist in the app. `00-design-language.md` section 7 keeps them and they stay on the landing
page and the docs home.

Inside the app, motion answers an action or reports a state change, and nothing else moves.

| What | Spec |
|---|---|
| Modal open | scrim fades over `--sd-dur-2`, sheet scales 0.98 to 1 and fades over `--sd-dur-3` at `--sd-ease` |
| Modal close | both over `--sd-dur-2`, no scale |
| Nav pill | fill only, `--sd-dur-1`, plain `ease`. The pill does not slide between rows |
| Setting row control | fill and border colour, `--sd-dur-1` |
| Primary button press | translate 1px 1px, `--sd-dur-1`. No shadow to shrink in the app |
| Card moves lane | 200ms transform at `--sd-ease`, once, when the state actually changed |
| Live run pulse | the accent edge on a running card breathes opacity 1 to 0.6 over 2s. This is the one looping animation in the app and it carries information: something is running |
| Heatmap | no entrance animation. Cells paint at their final value |
| Toast | unchanged, 140ms translateY(4px) and fade |

Under `prefers-reduced-motion: reduce` the modal cross-fades with no scale, the live-run
pulse becomes a static accent edge at full opacity, the lane move is instant, and every
other duration goes to 1ms. Nothing in the app waits for an animation.

## 12. Dark mode is a variant, not a fallback

The engineer running Sirdar has a dark terminal open beside it. Dark is not the degraded
theme and it is not derived by inverting light; every token in section 4 of
`01-tokens.md` ships both values and both are measured.

What the app owes dark mode specifically:

- **The shell is darker than the sheet, so the plane order is preserved.** Light goes shell
  `#F2EEE0` under sheet `#FFFDF7`; dark goes shell `#0C0D11` under sheet `#191A20`. In both
  themes the sheet is the lifted plane.
- **A card on the sheet moves the other way.** `--sd-card-row` is darker than the sheet in
  light (`#F5F1E4`) and lighter in dark (`#22242B`), a step of 1.11:1 and 1.12:1. This is
  the standard inversion and the reason `--sd-card-row` cannot be `--sd-sunk`, which stays
  recessed in both themes for input wells and code blocks.
- **The primary button inverts entirely.** A black button on a `#0C0D11` shell is invisible.
  `--sd-primary` is the ink colour in both themes, so in dark the commit action is a pale
  button with dark text. That keeps the rule "the primary button is the ink, filled" true in
  one sentence for both themes.
- **The heatmap ramp runs the same direction in both.** More runs is always more saturated
  and more accent, never darker in one theme and lighter in the other.
- **Theme follows the OS with a manual override in Settings.** Three states: System, Light,
  Dark. The override writes an attribute on the root element and the tokens file defines
  dark under both `@media (prefers-color-scheme: dark)` and `[data-theme="dark"]`, so the
  manual choice wins in either direction.
- **The contrast test runs against both.** `styles.contrast.test.ts` already parses both
  blocks. Every ratio quoted in this file is stated for light and dark, and a token that
  ships one value ships neither.

## 13. What is not taken from the capture

- The product's name, wordmark, icon set, copy, and its lavender `#EFDBFF` and teals.
- The ring-text share control in the top-right corner of the sheet. It is a marketing
  affordance on a workspace screen and Sirdar has nothing to share. The ring-text motion
  stays on the landing page where `00-design-language.md` section 7 already put it.
- Streaks, the "get a free month" referral row, the trial countdown, the upgrade card. The
  entire commercial half of the sidebar footer has no Sirdar equivalent, and its slot is
  taken by the workspace switcher and the quota chips, which are the facts a person
  actually needs pinned.
- The 2.11:1 version string.
