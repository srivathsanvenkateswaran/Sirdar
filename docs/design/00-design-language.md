# Sirdar design language

One language across three surfaces: the landing page, the desktop app, and the docs site.
It is derived from Wispr Flow (wisprflow.ai), measured rather than remembered: every value
attributed to them below was read out of
`https://cdn.prod.website-files.com/682f84b3838c89f8ff7667db/css/flowsite-dev.webflow.shared.c2654b1f8.min.css`
or sampled from a screenshot of the live page on 2026-09-15. Where a number is Sirdar's own
invention this file says so.

## 1. What is borrowed and what is not

**Borrowed (the language).** A warm paper ground instead of white. One editorial serif set
large, with the second clause of a headline in italic. A humanist sans for everything
functional. Full-bleed colour bands with corner radii measured in whole rem, so a section
reads as a card the width of the window. One soft tinted fill that appears only on the
primary action. A hard offset shadow under that action, no blur, so the button looks
printed rather than floating. Pill-shaped nav and segmented controls in a floating bar.
Slow ambient motion (a ring of rotating text, a marquee) that never blocks reading.

**Not borrowed (the assets).** No Wispr Flow logo, wordmark, product name, or screenshot.
No copy, headline, or sentence of theirs, including "Don't type, just speak" and "4x faster
than typing". No customer logo wall built from third-party marks. Their deep green
`#034f46` and lavender `#f0d7ff` are their brand pair and Sirdar does not reuse either
value: Sirdar's band is a deep indigo derived from the accent the app already ships
(`#4340c8`), and the soft highlight is a tint of that same indigo.

**Typography is a middle case.** They use EB Garamond (OFL), Figtree (OFL), Inter (OFL),
IBM Plex Mono, JetBrains Mono, and Monaspace Neon. These are open-licence families anyone
may use; choosing a serif for display is a genre convention, not their property. Sirdar
still picks a different serif, because a Garamond set at 7.5rem beside the same lavender
button is recognisably their page.

## 2. Mood and principles

Sirdar is read by an L2 support engineer who has a ticket open in another window and a
customer waiting. The page and the app both have to earn a second of attention and then
get out of the way.

1. **Evidence reads like a ledger, prose reads like a page.** Keys, run ids, offsets,
   costs, elapsed clocks, and file paths are monospace with tabular figures. Ticket titles,
   root-cause notes, and marketing copy are the sans or the serif. The split is already the
   rule in `desktop/frontend/src/styles.css` and it survives this redesign unchanged.
2. **Colour only ever means something.** Six status hues, one accent, one highlight. A
   decorative hue is a bug. A run that is live is the only moving thing on the board and it
   is the only thing wearing the accent.
3. **Read-only by construction, visible in the design.** Every mutating control in the app
   (Fix, Resume, workspace edits) is the one button shape that carries the hard offset
   shadow. Everything read-only is flat. A person should be able to tell what a click can
   change without reading the label.
4. **Warm, not cute.** The paper ground is warm and the radii are large, but the density is
   a workbench: 13px base in the app, hairline rules, no gradients, no illustration.
5. **The landing page may be slower than the app.** Ambient motion, a 4rem headline, and a
   full-bleed band belong on the landing page and the docs home. Inside the app the same
   tokens are used at the small end of every scale.

## 3. Colour system

Four roles: a warm paper ground, one deep accent (which is also the band), one soft
highlight, and an ink ramp. Status hues sit outside the four and are unchanged in meaning
from today's app.

Every ratio below was computed, not estimated. The numbers are in `01-tokens.md` alongside
each token.

### Light

| Role | Hex | Where it lands |
|---|---|---|
| Paper (ground) | `#FAF7EC` | page and app background |
| Surface | `#FFFDF7` | cards, panels, dialogs, the floating nav bar |
| Sunk | `#F1EDDF` | kanban lane wells, input rest state, code blocks |
| Band (deep) | `#1F2B52` | full-bleed section band, app title bar on the landing mock |
| Band (ink) | `#17181C` | the second full-bleed band, used once per page at most |
| Highlight | `#E4E0FF` | primary button fill, marker sweep under a hovered link |
| Ink | `#17181C` | headlines, body |
| Ink 2 | `#4A4E58` | secondary body, card titles |
| Ink 3 | `#676B74` | meta, timestamps, help text |
| Rule | `#E3DFD1` | hairlines between rows |
| Rule strong | `#C9C4B2` | card borders, input borders, the ledger rail |
| Accent | `#3B3AA6` | links, focus ring, a live run |

### Dark

| Role | Hex |
|---|---|
| Paper | `#14151A` |
| Surface | `#1C1E25` |
| Sunk | `#0F1014` |
| Band (deep) | `#232F58` |
| Band (ink) | `#0C0D10` |
| Highlight | `#2E2A55` (fill), `#DDD8FF` (its ink) |
| Ink | `#E9E7DF` |
| Ink 2 | `#A6A9B2` |
| Ink 3 | `#8C919B` |
| Rule | `#2A2D34` |
| Rule strong | `#3D424B` |
| Accent | `#A6A2FF` |

The dark ink `#E9E7DF` is warm on purpose. A neutral `#E6E8EC` (today's value) against a
warm paper in light mode makes the two themes feel like two products.

### Status hues

Meaning is fixed and matches the CLI's run states. Light values are deepened from today's
so they clear 4.5:1 on warm paper.

| State | Light | Dark | Meaning |
|---|---|---|---|
| queue | `#63676F` | `#9298A3` | waiting, nothing has happened |
| live | accent | accent | preparing or running |
| blocked | `#A15C07` | `#E0A14A` | the agent asked something, or a rate limit |
| triaged | `#0E6F66` | `#58C7BA` | completed, a note exists to read |
| done | `#17713A` | `#6EC98A` | resolved, RCA written |
| failed | `#B02418` | `#F08C8C` | failed, over budget, stalled |

The lane rail at the top of a kanban column and every status badge inside that column carry
the same hue, so the rail doubles as the legend. That rule exists today and is kept.

## 4. Typography

Three families plus two Arabic companions. All are OFL and available on Google Fonts, and
all should be self-hosted (woff2, `font-display: swap`) so the desktop app works offline
and the landing page makes no third-party request.

| Register | Family | Why this one |
|---|---|---|
| Display | **Newsreader** (200 to 800 variable, true italic, optical sizes) | Garamond-adjacent editorial voice, but drawn for screens, so a 4rem headline and a 1.25rem deck can come from one file. Its italic is a real cursive, which is what carries the "second clause in italic" pattern. |
| UI | **Inter** (variable) | Tabular figures, a large x-height at 13px, and coverage the board needs. The app is already effectively Inter-shaped: it runs on the system sans today. |
| Mono | **JetBrains Mono** | Unambiguous `0/O` and `l/1`, tabular by default, reads well in the event stream's fixed gutter. |
| Arabic UI | **IBM Plex Sans Arabic** | Naskh proportions that sit level with Inter at the same size, so a bilingual note does not step up and down. |
| Arabic display | **Noto Naskh Arabic** | The serif slot has no Arabic counterpart. Arabic display text uses Naskh at the same optical size rather than a faux-italic. |

Alternative if more character is wanted on the landing page: **Fraunces**, whose `SOFT` and
`WONK` axes give a headline more personality. Instrument Serif is the third option and is
rejected here because it ships one weight, which forces a second family for the deck.

**Italic is structural, not decorative.** A display headline is one roman clause and one
italic clause. Inside the app, italic is never used: it damages Arabic and it reads as an
error state in a dense table.

Scale (rem, off a 16px root):

| Token | Landing | App |
|---|---|---|
| display-xl | 4.5 | not used |
| display-l | 3.5 | not used |
| display-m | 2.5 | 1.625 (26px — the one figure or headline a screen leads with) |
| head | not used | 1.375 (22px — the page heading, sans and serif alike) |
| title | 2 | 1.125 (18px) |
| body-l | 1.25 | 0.9375 (15px) |
| body | 1 | 0.875 (14px) |
| meta | 0.875 | 0.8125 (13px) |
| micro | 0.75 | 0.6875 (11px) |
| ledger | not used | 0.78125 (12.5px mono) |

The app column has moved twice. It was the small end of the same ladder (13px base) until
the 2026-09-15 screens round re-based it on a 16px register; on 2026-09-17 the owner, with a
screenshot of the reference app beside ours, said the header, the side pane and the type were
all a size too big, and the whole column came down one class to the 14px register above. A
support engineer wants four facts and the room around them, not a 16px body pushing the fifth
off the screen. The landing and docs Newsreader display sizes did not move. The exact values
are in `01-tokens.md` section 3.

The shell's measures came down with the type: a control is 32 tall (36 for the composer's
send and a screen's one primary button), the sidebar 240 wide with 30-tall nav and session
rows, the run header 44, the inspector's tab row 36, a register row 36, a board card 10/12.
Every text stays at or above 11px and every hit target at or above 28.

## 5. Spacing and radius

4px base. Steps: 4, 8, 12, 16, 24, 32, 48, 64, 96, 128.

Radii are the largest change from today's app, which uses a single 3px value.

| Token | Value | Used by |
|---|---|---|
| radius-xs | 4px | badges, kbd, inline chips |
| radius-sm | 8px | inputs, buttons, cards on the board |
| radius-md | 12px | panels, dialogs, run cards |
| radius-lg | 20px | landing cards, media frames, the screenshot mock |
| radius-band | 40px mobile, 64px desktop | full-bleed section bands |
| radius-pill | 999px | nav pills, segmented control, status chips, quota chips |

Wispr Flow's own section-radius ladder runs `1rem` to `5rem` with a mobile/desktop pair at
each step. Sirdar collapses that to one pair (2.5rem/4rem) because it has one band shape,
not six.

## 6. Elevation

Two shadows, and most things get neither.

- **Hard offset, primary only.** `2px 2px 0 0 var(--sd-ink)` in light,
  `2px 2px 0 0 var(--sd-rule-strong)` in dark. This is the printed look, and it marks the
  one control on the surface that can change something. On press the button translates
  `1px, 1px` and the shadow shrinks to `1px 1px`, so it appears to be pushed down.
- **Soft ambient.** `0 4px 20px rgba(23,24,28,.10)` for dialogs, toasts, the floating nav
  bar, and hovering media cards. Nothing else.
- **Nothing** on board cards, lanes, table rows, or the event stream. Those separate with
  hairlines, which is the rule today and the reason the board reads at a glance.

Full-bleed bands do not cast a shadow. They separate by colour and corner radius.

## 7. Motion

Durations: 120ms (colour, hover), 200ms (small transforms, dropdowns), 300ms (panel and
dialog entrance), 350ms (band and page transitions).

Easings: `--sd-ease` `cubic-bezier(.22,.61,.36,1)` for entrances and transforms,
`ease` for pure colour changes. Wispr Flow uses plain `ease` everywhere at .15s to .35s;
Sirdar keeps their duration range and adds one out-easing so a dialog does not feel linear.

**Signature ambient motions.** Each is decorative, carries no information, and has a
reduced-motion answer.

| Motion | Where | Spec | Reduced motion |
|---|---|---|---|
| Ring text | landing hero, left of the headline | a short sentence set on a circular path, rotating 360deg over 48s, linear, infinite, at `--sd-ink-3` | frozen at the angle where the sentence starts at 9 o'clock and reads clockwise |
| Marquee | logo/source band, docs footer | a single row translating one track width over 34s, linear, infinite, with a 64px gradient mask at each edge | becomes a static wrapped row, mask removed |
| Curved caption | the board screenshot mock | a caption on a shallow arc across the image, static by default, drifting 3deg over 12s on hover only | no drift |
| Band radius | section boundaries | the band's top corners interpolate from `radius-band` to 0 as it reaches the viewport top, over the scroll, no JS timeline | fixed at `radius-band` |
| Toast | app | 140ms translateY(4px) and fade, already shipped | none, as today |

`@media (prefers-reduced-motion: reduce)` sets every `transition-duration` and
`animation-duration` to `1ms` except where the table above names a specific static pose.
Nothing in the app depends on an animation finishing.

## 8. Layout

- **Centred content, full-bleed colour.** A `--sd-measure` of 1200px for landing sections,
  68ch for prose (docs, note pane). Colour bands run the full window width and the content
  inside them respects the same measure.
- **The floating bar.** Landing and docs carry a nav bar that is a surface-coloured pill
  row, inset 16px from the top, `radius-pill`, soft ambient shadow, sticky. It sits on top
  of whatever band it is over, which is how it survives the cream to green to black
  sequence on Wispr's page and is the single cheapest way to make a colour-banded page feel
  like one document.
- **Band sequence.** Paper, then one deep band, then paper, then at most one ink band, then
  paper. Two adjacent coloured bands is the failure mode.
- **App shell unchanged.** Header, nav, board with horizontally scrolling lanes, inbound
  strip, run detail as a two-pane split. The redesign changes the paint, not the structure.
  Under 720px the lanes stack, as today.

## 9. Component inventory

**Shared (all three surfaces):** Button (primary, secondary, ghost), Pill nav, Segmented
control, Card, Status badge, Quota chip, Toast, Dialog, Data table, Code block, Kbd, Focus
ring, Link with marker sweep.

**Landing only:** Hero band, Ring text, Marquee, Media frame (the board mock), Text badge
row (providers and sources as type, never third-party logos), Step band ("how it works"),
Trust panel, Footer.

**App only:** Kanban column, Run card, Ticket card, Event row, Note pane, Register table,
Settings panel, Quota meter, Inbound row, Workspace switcher, New-triage dialog, Fix panel.

**Docs only:** Sidebar nav, Table of contents rail, Admonition, Tabbed block, Version chip.
These are Material for MkDocs primitives restyled through the same tokens, not new
components.

## 10. Accessibility floor

- **4.5:1 minimum for every text and token pair the tokens file ships.** The measured
  ratios are in `01-tokens.md`. `desktop/frontend/src/styles.contrast.test.ts` already
  enforces this for the app; the same test should be pointed at the shared tokens file so
  the landing page and docs inherit the check.
- **Focus is always visible.** `2px solid var(--sd-accent)` at `outline-offset: 2px`, on
  every focusable thing, in both themes. Never removed, never replaced by a colour change
  alone. The accent clears 4.5:1 against paper, surface, and sunk in both themes.
- **Status is never colour alone.** Every status hue is paired with its word in the badge.
  The lane rail is redundant with the lane heading.
- **Hit targets 28px minimum in the app (32 for a control that is not a row), 44px on the landing page.**
- **Motion.** See section 7. No parallax, no scroll-jacking, no autoplaying video with
  sound.
- **Text over a band.** Only paper-coloured ink on `band-deep` and `band-ink`. No accent
  text and no status hue inside a band.

## 11. RTL

Arabic is first-class: the customer complaint and the reply draft in a Sirdar note are
usually Arabic, and `desktop/frontend/src/lib/rtl.ts` already carries a per-person
preference for laying the note pane out right to left.

- **Logical properties only.** `margin-inline-start`, `padding-inline`, `inset-inline`,
  `border-inline-start`. No `left`, `right`, `margin-left` in any component in the library.
  The hard offset shadow is the one exception that needs a flip: it becomes
  `-2px 2px 0 0` under `[dir="rtl"]`, so the light still comes from the leading edge.
- **The card's quote rail** (`.card-reason`, a 2px left border today) becomes
  `border-inline-start`.
- **Numbers, keys, ids, paths, and the event stream stay LTR** even inside an RTL pane.
  `rtl.ts` states the reason: tool names, paths, and JSON are unreadable mirrored. Wrap
  them in `dir="ltr"` with `unicode-bidi: isolate`.
- **`dir="auto"` per block** for note prose, which handles a bilingual note paragraph by
  paragraph without a global switch.
- **Type.** IBM Plex Sans Arabic and Noto Naskh Arabic run about 8% smaller in apparent
  size than Inter at the same px. The library sets `font-size-adjust` on the Arabic faces
  rather than a second size scale.
- **Landing and docs.** The ring text and the marquee both reverse direction under
  `[dir="rtl"]`, because a sentence that rotates against its own reading direction is
  unreadable in either script.
- **Italic.** Arabic has no italic. A display headline in Arabic marks its second clause
  with weight (600 against 400), not slant.
