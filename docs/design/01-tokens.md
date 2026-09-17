# Tokens

## 1. Source of truth

**One file, `desktop/frontend/src/styles/tokens.css`, is the only place a value is
declared.** Three consumers read it and none of them redeclares anything:

| Consumer | How it gets the file |
|---|---|
| Desktop app | `@import './styles/tokens.css'` at the top of `styles.css`, which keeps the shell rules and drops its own `:root` block |
| Landing site | copied into the site build by a `make tokens` step that runs `cp`, plus a CI check that the copy is byte-identical to the source |
| Docs site | `docs/stylesheets/tokens.css`, same copy step, imported ahead of `sirdar.css` so Material for MkDocs' own `--md-*` variables can be assigned from Sirdar tokens |

The copy is a copy, not a fork. If the three ever disagree, the desktop file is right and
the other two are stale. That is the same rule Tatak states at the top of its
`app/styles/tokens.css`, and it exists for the same reason: the alternative is five
spellings of the word hairline.

**Nothing outside this file may declare a colour.** `components/panels.css` and
`components/run/run.css` currently declare seven local colours between them
(`--run-warn`, `--run-error`, `--panel-warn`, `--panel-danger`, and their dark-mode
overrides). Those move here. The migration map in section 5 says where each one lands.

Naming: every token is prefixed `--sd-`. The app's current unprefixed names collide with
Material for MkDocs and with anything a landing-page component library brings in, and a
prefix is what makes one file safe to drop into three builds.

## 2. Colour tokens

Ratios are measured against the ground the token is used on and are stated per row. All
clear 4.5:1.

### Ground and surface

| Token | Light | Dark | Rationale |
|---|---|---|---|
| `--sd-paper` | `#FAF7EC` | `#14151A` | Warm paper, not white. The warmth is what makes a dense board readable for an hour without the glare of `#ffffff`. Today's `--paper` is `#fbfbfa`, which is warm by 1 step; this is warm by about 8. |
| `--sd-surface` | `#FFFDF7` | `#1C1E25` | A half-step above paper, not pure white. Pure white on a warm ground reads blue. |
| `--sd-sunk` | `#F1EDDF` | `#0F1014` | Lane wells, input rest, code blocks. Replaces `--lane`. |
| `--sd-band-deep` | `#1F2B52` | `#232F58` | The full-bleed section band. Derived from the app's existing accent `#4340c8` pulled down in lightness and desaturated, so the band and the accent are the same hue family. Paper on it: **12.83:1** light, **10.48:1** dark. |
| `--sd-band-ink` | `#17181C` | `#0C0D10` | The second band. Same value as `--sd-ink` in light, so an ink band is literally the text colour enlarged. |

### App shell

Added after reading a capture of Wispr Flow's macOS app on 2026-09-15. The app is a window
ground with one content sheet on it rather than a page with colour bands, and the two planes
need their own names. Measurements and reconstructions are in `03-desktop-app.md` section 3.
Nothing above is renamed: `--sd-paper` keeps the landing page and the docs site, and stops
being the desktop app's background.

| Token | Light | Dark | Rationale |
|---|---|---|---|
| `--sd-shell` | `#F2EEE0` | `#0C0D11` | The window ground: behind the sheet, under the sidebar, under the title bar strip. `--sd-paper` pulled down about 3%. The sheet reads against it at **1.14:1** light and **1.12:1** dark; the reference's own pair is 1.08:1, which is enough. `--sd-ink-3` on it is **4.60:1** light, **6.14:1** dark, which is what fixes the value: `#EFEBDD` would have been 4.48 and would not ship. |
| `--sd-sheet` | `#FFFDF7` | `#191A20` | The content sheet: the board, the run split, the register table, the modal's content panel. In light it is the same value as `--sd-surface` and that is deliberate, because the sheet *is* the app's surface; in dark they diverge, since the sheet must lift off the shell while a card on the sheet lifts again. `--sd-ink` on it: **17.44:1** light, **14.02:1** dark. |
| `--sd-card-row` | `#F5F1E4` | `#22242B` | The settings card, the sidebar footer card, the modal's secondary nav column. A step off the sheet in the direction that reads as raised: **1.11:1** light, **1.12:1** dark. It inverts between themes (darker than the sheet in light, lighter in dark) and that is why it cannot be `--sd-sunk`, which stays recessed in both. `--sd-ink` **15.70** / **12.51**, `--sd-ink-2` **7.37** / **6.60**, `--sd-ink-3` **4.73** / **4.90**. |
| `--sd-rule-faint` | `#EAE6D6` | `#24262C` | The hairline *inside* a card row, where `--sd-rule` is too strong against `--sd-card-row`. **1.11:1** light, **1.12:1** dark, matching the reference's 1.08 divider. Not a text colour. Three components read it (Setting row, Sidebar footer card, Modal secondary nav), which is what makes it a token rather than a constant. |
| `--sd-scrim` | `rgba(23,24,28,.32)` | `rgba(7,8,10,.56)` | Behind a dialog or the settings modal. The reference solves to `#1A1A1A` at 31%. The board has to stay legible through it, which is the reason a modal is used at all. |

### Navigation and the primary action

| Token | Light | Dark | Rationale |
|---|---|---|---|
| `--sd-nav-hover` | `#EDE8D8` | `#1B1D24` | Sidebar row hover, secondary-nav row hover. A step toward the active fill, **1.06:1** off the shell light and **1.15:1** dark. `--sd-ink-2` on it: **6.79** / **7.16**. |
| `--sd-nav-active` | `#E7E2D1` | `#24262E` | The current page's soft pill. **1.12:1** off the shell light, **1.29:1** dark, no border and no accent. `--sd-ink` on it: **13.68** / **12.19**. `--sd-ink-2`: **6.42** both. `--sd-ink-3` clears only 4.12 here and is not allowed on a nav pill. Deliberately neutral: in Sirdar the accent means a live run, and a nav row wearing it competes with the one card on the board that earned it. Also the fill for the pale per-row button on a setting card. |
| `--sd-primary` | `#17181C` | `#E9E7DF` | The one filled commit button per screen. It is `--sd-ink`, inverted per theme, so a black button in light does not become an invisible black button on a `#0C0D11` shell in dark. |
| `--sd-primary-ink` | `#FFFDF7` | `#0C0D11` | Its label: **17.44:1** light, **15.69:1** dark. |
| `--sd-primary-hover` | `#2B2D33` | `#D5D2C6` | Label on it: **13.53:1** light, **12.82:1** dark. |

### Badge

| Token | Light | Dark | Rationale |
|---|---|---|---|
| `--sd-badge-bg` | `var(--sd-highlight)` | `var(--sd-highlight)` | The account or workspace chip, the reference's lavender plan badge on Sirdar's own hue. It points at the existing highlight rather than introducing a ninth near-identical tint; the name exists because a badge and a primary button are different roles that happen to share a fill today. |
| `--sd-badge-ink` | `var(--sd-highlight-ink)` | `var(--sd-highlight-ink)` | **13.86:1** light, **9.72:1** dark. |

A badge carries a fact about the account or the workspace. Run state stays on
`--sd-st-*` with its word, and anything that could be either is a status badge.

### Heatmap

Five steps of the accent for the runs-per-day grid on the Register.

| Token | Light | Dark | Means |
|---|---|---|---|
| `--sd-heat-0` | `#EEEADB` | `#232630` | no runs |
| `--sd-heat-1` | `#CFCBEE` | `#34315F` | 1 |
| `--sd-heat-2` | `#A7A2DE` | `#4C4894` | 2 to 4 |
| `--sd-heat-3` | `#6E69C4` | `#736ECF` | 5 to 9 |
| `--sd-heat-4` | `var(--sd-accent)` | `var(--sd-accent)` | 10 or more |

Step 0 against `--sd-sheet` is **1.18:1** light and **1.15:1** dark. Consecutive steps
separate by **1.30 / 1.51 / 1.98 / 1.91** light and **1.26 / 1.52 / 1.83 / 1.90** dark. A
sequential ramp's neighbouring pairs are not held to 3:1; the floor applies to the text,
and the count is carried by the cell's accessible name, its tooltip, and a legend that
prints bucket boundaries as numbers. The top step is the accent itself, so the busiest day
and a live run are the same colour, which is true: both mean the machine is working.

### Ink ramp

| Token | Light | Dark | On paper | Rationale |
|---|---|---|---|---|
| `--sd-ink` | `#17181C` | `#E9E7DF` | 16.54 / 14.72 | Headlines and body. The dark value is warm (`#E9E7DF`, not a neutral grey) so the two themes read as one product. |
| `--sd-ink-2` | `#4A4E58` | `#A6A9B2` | 7.76 / 7.76 | Secondary body, card titles, field labels. The two themes are matched to the same ratio on purpose. |
| `--sd-ink-3` | `#676B74` | `#8C919B` | 4.98 / 5.76 | Meta, timestamps, help text. This is the floor of the ramp: on `--sd-sunk` it is **4.56:1**, which is the tightest pair the file ships and the reason `--sd-sunk` is not any darker. |
| `--sd-rule` | `#E3DFD1` | `#2A2D34` | n/a | Hairlines between rows. Not a text colour and never used as one. |
| `--sd-rule-strong` | `#C9C4B2` | `#3D424B` | n/a | Card and input borders, the event stream's rail, the dark-mode hard shadow. |

### Accent and highlight

| Token | Light | Dark | Rationale |
|---|---|---|---|
| `--sd-accent` | `#3B3AA6` | `#A6A2FF` | Links, focus ring, and a live run. One accent means "Sirdar itself", which is the rule the app already follows. Deepened from `#4340c8` to clear the warm ground: **8.36:1** light, **8.02:1** dark. |
| `--sd-accent-ink` | `#FFFDF7` | `#14151A` | Text on an accent fill. **8.82:1** light, **8.02:1** dark. |
| `--sd-accent-soft` | `color-mix(in srgb, var(--sd-accent) 10%, transparent)` | `... 16% ...` | Nav current-page fill, selected row. A tint, never a text colour. |
| `--sd-highlight` | `#E4E0FF` | `#2E2A55` | The one soft fill, on the primary button and the marker sweep under a hovered link. A tint of the accent, which is what keeps it from being Wispr Flow's lavender by a different name. |
| `--sd-highlight-ink` | `#17181C` | `#DDD8FF` | Text on the highlight. **13.86:1** light, **9.72:1** dark. |

### Status

Meaning is fixed by the run state machine in the CLI. Light values are deepened from
today's to clear 4.5:1 on warm paper.

| Token | Light | Dark | On paper | Means |
|---|---|---|---|---|
| `--sd-st-queue` | `#63676F` | `#9298A3` | 5.29 / 6.29 | queued, nothing has happened |
| `--sd-st-live` | `var(--sd-accent)` | `var(--sd-accent)` | 8.36 / 8.02 | preparing, running |
| `--sd-st-blocked` | `#A15C07` | `#E0A14A` | 4.84 / 8.13 | agent asked something, or a rate limit |
| `--sd-st-triaged` | `#0E6F66` | `#58C7BA` | 5.62 / 8.94 | completed, a note exists |
| `--sd-st-done` | `#17713A` | `#6EC98A` | 5.66 / 9.02 | resolved, RCA written |
| `--sd-st-failed` | `#B02418` | `#F08C8C` | 6.29 / 7.67 | failed, over budget, stalled |

`--sd-st-blocked` at 4.84:1 is the tightest status pair. It is used as 11px uppercase mono
in the inbound strip, where 4.5:1 is the applicable threshold, not 3:1.

## 3. Type, spacing, radius, elevation, motion

| Token | Value | Rationale |
|---|---|---|
| `--sd-font-display` | `Newsreader, Georgia, 'Noto Naskh Arabic', serif` | Variable weight with a true italic, drawn for screens. Arabic sits behind it as a fallback and carries no Latin glyphs, so Latin still renders in Newsreader. That fallback trick is already how `--sans` works today. |
| `--sd-font-ui` | `Figtree, Inter, -apple-system, 'IBM Plex Sans Arabic', system-ui, sans-serif` | Figtree leads since the 2026-09-15 screens round, which re-based the app on the reference's 16px register; Inter stays as the fallback with the tabular figures and the Arabic companions behind it. |
| `--sd-font-mono` | `'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace` | Unambiguous `0/O`, tabular by default. |
| `--sd-text-display-m` | `1.625rem` | The one figure or headline a screen leads with: the New session question, a stat card's number. Was 2rem before the 2026-09-17 density round. |
| `--sd-text-head` | `1.375rem` | The page heading on Board, Register, Eval and Settings, sans and serif alike. Added 2026-09-17, replacing the 32px sans and the 34px serif that were the loudest thing in the window. |
| `--sd-text-title` | `1.125rem` | Card titles, section names. Was 1.375rem. |
| `--sd-text-body-l` | `0.9375rem` | Item-row titles, the session start bar, the run header's key. Was 1.0625rem. |
| `--sd-text-body` | `0.875rem` | The UI face: nav rows, buttons, setting labels. Was 1rem; the 2026-09-17 round moved the whole app one size class down, to the register the owner's reference screenshot reads at. |
| `--sd-text-meta` | `0.8125rem` | Secondary lines, table cells, board card titles, inspector tabs, help text. Was 0.875rem. |
| `--sd-text-micro` | `0.6875rem` | Status chips, kind chips, group labels, sticky table headers, kbd hints. Was 0.75rem. It is the floor: no text in the app is smaller. |
| `--sd-text-ledger` | `0.78125rem` | The mono ledger: keys, clocks, costs, event rows, at 12.5px. Was 0.84375rem; it still sits a half-step under the body so a run id does not out-weigh the title beside it. |
| `--sd-space-1` .. `--sd-space-10` | 4, 8, 12, 16, 24, 32, 48, 64, 96, 128px | 4px base. |
| `--sd-radius-xs` | `4px` | badges, kbd, chips |
| `--sd-radius-sm` | `8px` | inputs, buttons, board cards |
| `--sd-radius-md` | `12px` | panels, dialogs, run cards |
| `--sd-radius-lg` | `20px` | landing cards, media frames |
| `--sd-radius-band` | `40px` / `64px` at `min-width: 768px` | full-bleed bands |
| `--sd-radius-pill` | `999px` | nav pills, segmented control, status chips |
| `--sd-radius-sheet` | `16px` | the content sheet and the settings modal, the one box the whole app sits in. Added 2026-09-15 |
| `--sd-shadow-hard` | `2px 2px 0 0 var(--sd-ink)` (light), `2px 2px 0 0 var(--sd-rule-strong)` (dark) | Primary action only. Flips to `-2px 2px 0 0` under `[dir="rtl"]`. |
| `--sd-shadow-soft` | `0 4px 20px rgba(23,24,28,.10)` | Dialogs, toasts, the floating nav bar. Nothing else. |
| `--sd-dur-1` .. `--sd-dur-4` | `120ms`, `200ms`, `300ms`, `350ms` | |
| `--sd-ease` | `cubic-bezier(.22,.61,.36,1)` | Entrances and transforms. Colour changes use plain `ease`. |
| `--sd-measure-wide` | `1200px` | landing section content |
| `--sd-measure-prose` | `68ch` | docs body, note pane |
| `--sd-gutter` | `12px` app, `24px` landing | today's app gutter is 12px and stays |
| `--sd-control-h` | `32px` | every control's height: chip, select, search well, segmented track, secondary button. Was 40px before the 2026-09-17 density round |
| `--sd-control-h-lg` | `36px` | the two controls the hand goes to and nothing else: the composer's send and a screen's one primary button. Added 2026-09-17 |
| `--sd-sidebar-w` | `240px` (208px under 1200) | the sidebar's width; the shell and the gallery's sidebar specimens both read it. Was 248 / 220 |
| `--sd-nav-row-h` | `30px` | a sidebar row, a session row, a modal nav row and a popover menu row, with 18px icons. Was 44px with 20px icons |

## 4. What a token is not

A token is not a component-local constant. `--run-line` is an alias for `--border`, not a
new idea, and it does not belong in the file. A value used by exactly one selector stays in
that selector's rule. The test: if two components would have to agree on it, it is a token.

## 5. Migration map

Left column is what exists today. Nothing is renamed in place: the new names land first,
the old names become one-line aliases so no rule has to be rewritten on the same commit,
and the aliases are deleted in a second pass once `grep -r 'var(--paper'` returns nothing.

### `desktop/frontend/src/styles.css`

| Today | Becomes | Note |
|---|---|---|
| `--paper` | `--sd-shell` | the app's `body` background becomes the window ground, `#fbfbfa` to `#F2EEE0`. `--sd-paper` still exists and still means the landing page and docs ground; the app just stops using it. See `03-desktop-app.md` section 4 |
| `--surface` | `--sd-surface` | `#ffffff` to `#FFFDF7` |
| `--surface`, where it paints the screen itself | `--sd-sheet` | the board, the run split, the register and eval tables sit on the sheet; cards on top of it keep `--sd-surface` |
| `--lane` | `--sd-sunk` | renamed: the same value serves lane wells, inputs and code blocks, and "lane" is board-specific |
| `--ink` | `--sd-ink` | `#191a1c` to `#17181C` |
| `--ink-2` | `--sd-ink-2` | `#5b5f66` to `#4A4E58` |
| `--ink-3` | `--sd-ink-3` | `#686d75` to `#676B74`. Note today's `--ink-3` is *lighter* than `--ink-2` in dark mode and darker in light; the new ramp is monotonic in both |
| `--rule` | `--sd-rule` | |
| `--rule-strong` | `--sd-rule-strong` | |
| `--accent` | `--sd-accent` | `#4340c8` to `#3B3AA6` light, `#9b98ff` to `#A6A2FF` dark |
| `--accent-ink` | `--sd-accent-ink` | |
| `--accent-soft` | `--sd-accent-soft` | changes from a literal `rgba()` to `color-mix`, so it follows the accent |
| `--st-queue` | `--sd-st-queue` | `#6b7280` to `#63676F` |
| `--st-live` | `--sd-st-live` | unchanged, still `var(--sd-accent)` |
| `--st-blocked` | `--sd-st-blocked` | `#b45309` to `#A15C07` |
| `--st-triaged` | `--sd-st-triaged` | `#0f766e` to `#0E6F66` |
| `--st-done` | `--sd-st-done` | `#15803d` to `#17713A` |
| `--st-failed` | `--sd-st-failed` | `#b91c1c` to `#B02418` |
| `--sans` | `--sd-font-ui` | the system stack is replaced by self-hosted Inter; the Arabic fallbacks stay in the stack, in the same position and for the same reason |
| `--mono` | `--sd-font-mono` | JetBrains Mono ahead of `ui-monospace` |
| `--radius` (3px) | split: `--sd-radius-xs`, `--sd-radius-sm`, `--sd-radius-md` | one value cannot serve a badge and a dialog once radii get large. Every `border-radius: var(--radius)` needs a per-selector decision; the component specs in `library/` carry it |
| `--gutter` | `--sd-gutter` | unchanged at 12px in the app |
| `--bg` | alias `var(--sd-surface)` | delete after the second pass |
| `--fg` | alias `var(--sd-ink)` | delete after the second pass |
| `--muted` | alias `var(--sd-ink-3)` | 45 uses in `panels.css` alone; the alias is what makes this migration one commit rather than forty |
| `--border`, `--line` | alias `var(--sd-rule)` | |

### `components/run/run.css`

| Today | Becomes |
|---|---|
| `--run-line` | `var(--sd-rule)` directly; the local declaration and its fallback chain go |
| `--run-ok` | `var(--sd-st-triaged)`. Today it aliases `--accent`, which means an allowed tool call and a live run wear the same hue. They are different facts |
| `--run-warn` (`#9a6207` / `#e0a35c`) | `var(--sd-st-blocked)` (`#A15C07` / `#E0A14A`) |
| `--run-error` (`#b42318` / `#f08a80`) | `var(--sd-st-failed)` (`#B02418` / `#F08C8C`) |
| `--run-tint` | `color-mix(in srgb, var(--sd-st-triaged) 12%, transparent)` |

### `components/panels.css`

| Today | Becomes |
|---|---|
| `--panel-ok` | `var(--sd-st-done)`. Today it aliases `--accent`; a passing doctor check is not a live run |
| `--panel-warn` (`#9a6b00` / `#d9a441`) | `var(--sd-st-blocked)` |
| `--panel-danger` (`#b3392a` / `#e0685a`) | `var(--sd-st-failed)` |

That removes both `@media (prefers-color-scheme: dark)` blocks from `panels.css` and
`run.css`: once the hues are tokens, the theme switch happens in one place.

### Docs site

`docs/stylesheets/sirdar.css` assigns Material's variables from Sirdar's, rather than
restating hexes:

| Material | Sirdar |
|---|---|
| `--md-primary-fg-color` | `var(--sd-band-deep)` |
| `--md-accent-fg-color` | `var(--sd-accent)` |
| `--md-default-bg-color` | `var(--sd-paper)` |
| `--md-default-fg-color` | `var(--sd-ink)` |
| `--md-code-bg-color` | `var(--sd-sunk)` |
| `--md-typeset-a-color` | `var(--sd-accent)` |

`mkdocs.yml` keeps `primary: indigo` as the palette name; the CSS overrides the values.

## 6. Test

`desktop/frontend/src/styles.contrast.test.ts` already parses the app's `:root` blocks and
asserts contrast. Point it at `styles/tokens.css` instead and add the pairs this file
states a ratio for, including the tight ones: `--sd-ink-3` on `--sd-sunk` at 4.56,
`--sd-st-blocked` on `--sd-paper` at 4.84, `--sd-ink-3` on `--sd-shell` at 4.60, and
`--sd-ink-3` on `--sd-card-row` at 4.73. A token that cannot state its ratio does not ship.

Two assertions in the test are about a step rather than a ratio, and both are upper bounds
as well as lower ones, because a plane that separates too hard stops reading as the same
window:

- `--sd-sheet` against `--sd-shell`, and `--sd-card-row` against `--sd-sheet`, each between
  1.08:1 and 1.25:1 in both themes.
- `--sd-ink-3` on `--sd-nav-active` is recorded at 4.12 and marked as a pair the app does
  not use, so the number stays visible to anyone who reaches for it.

The five `--sd-heat-*` steps are asserted monotonic in relative luminance in both themes,
in the same direction, and every consecutive pair at 1.2:1 or more. They are not asserted
against 4.5:1; `03-desktop-app.md` section 10 says why and what carries the number instead.
