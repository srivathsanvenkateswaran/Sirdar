# Landing page

One page, one call to action: Install. It links to the GitHub releases page. There is no
signup, no waitlist, no demo request, and no email field, because there is nothing to sign
up for. The page's whole job is to let a support engineer decide in forty seconds whether
this belongs on their machine.

## Copy tone

Concrete nouns and real commands. The page quotes the product rather than describing it:
`sirdar triage KEY` is better copy than "streamlines your triage workflow". Every claim on
the page is one a reader can check in the repository within a minute, and the trust section
is written so that a sceptical reader is the intended reader.

Rules for every line on this page:

- Say what it does, then what it refuses to do. The refusals are the product.
- No adjective that cannot be tested. Not "fast", "powerful", "seamless", "intelligent".
- No invented numbers. No "10x", no "saves N hours", no fake logo wall.
- Name the constraint out loud: it runs on your machine, on your logins, on a subscription
  you already pay for, and it is read-only everywhere except one gated command.
- The agent's output is a hypothesis. The page says so, in the hero deck, not in a
  footnote.

## Sections

### 0. Floating pill nav

Sticky, inset 16px from the top, `--sd-surface` on `--sd-radius-pill`, `--sd-shadow-soft`.
Left: the wordmark in mono at `--sd-ink`. Centre: a segmented control, `Triage` / `Fix` /
`RCA`, which scrolls to the matching step in section 3 and reflects scroll position.
Right: `Docs`, `GitHub`, then the primary Install button.

It sits over paper, over the deep band, and over the ink band without changing, which is
what makes a colour-banded page read as one document.

**Motion:** none of its own. The segmented thumb slides at `--sd-dur-2`. On scroll past the
hero the bar's shadow appears over `--sd-dur-1`.

### 1. Hero

Ground `--sd-paper`. Centred, `--sd-measure-wide`.

- Eyebrow, mono, uppercase, `--sd-ink-3`: `OPEN SOURCE - MIT - RUNS ON YOUR MACHINE`
- Headline, Newsreader, `display-xl`, with the second clause italic:
  **"A ticket arrives. *Something reads it first.*"**
- Deck, `body-l`, `--sd-ink-2`, two lines: "Sirdar gathers the evidence a support ticket
  needs, in the customer's own language, and writes a root-cause note for you to review.
  The note is a hypothesis, not a verdict."
- Primary button: **Install**, linking to `/releases/latest`. Carries `--sd-shadow-hard`.
- Under it, one copyable line in mono on `--sd-sunk`:
  `brew install srivathsanvenkateswaran/sirdar/sirdar`, with a copy control and a smaller
  `go install` alternative behind a text link.
- Below the fold line: `Requires a coding agent you already pay for: Claude Code, Codex, or
  any OpenAI-compatible endpoint.`

**Motion:** the ring text, positioned to the inline-start of the headline, partially
cropped by the viewport edge exactly as it is on a wide screen. The sentence on the ring is
the product's actual scope, so the decoration is also information:
`triage - evidence - root cause - proposed fix - rca - resolution -` rotating 360deg over
48s, linear, infinite, at `--sd-ink-3`. Reduced motion freezes it with the first word at 9
o'clock.

### 2. Sources and providers

A short band on `--sd-paper`, no card.

Two labelled rows of **text badges**, never third-party logos: pill outlines in
`--sd-rule-strong` with the name set in mono at `--sd-ink-2`.

- `READS FROM`: Jira, Zoho Desk, Linear, GitHub Issues, Zendesk, Freshdesk
- `RUNS ON`: Claude Code, Codex, OpenAI-compatible, ACP, Qwen, Ollama, vLLM, llama.cpp

Using a company's logo implies they endorse this; setting their name in Sirdar's own type
states a fact about what the adapters read. That is the whole reason for the rule, and it
is worth stating in a comment in the component.

**Motion:** the marquee. One row translating one track width over 34s, linear, infinite,
with a 64px gradient mask at each edge. Hovering pauses it. Reduced motion turns it into a
static wrapped row with the mask removed. Under `[dir="rtl"]` it reverses.

### 3. How it works

Full-bleed `--sd-band-deep`, `--sd-radius-band` on all four corners, paper ink. This is the
page's one colour anchor.

Headline, Newsreader, `display-l`, italic second clause: **"Four commands. *Read the note
between the third and the fourth.*"**

Four steps as cards on the band, each a mono command, a one-line what, and a one-line
consequence:

| Command | What | Consequence |
|---|---|---|
| `sirdar init` | scaffolds `.sirdar/config.yaml` and the playbooks | nothing runs yet |
| `sirdar triage KEY` | fetches the ticket, runs one agent session, writes a Triage Note | reads only |
| `sirdar fix KEY` | implements that note's proposed fix on a branch and opens the PR | running it is the approval |
| `sirdar rca KEY --pr URL` | writes the RCA and the Resolution draft | marks the note resolved |

The gap between rows 3 and 4 is where a human reads. The layout makes that gap physical: a
hairline rule in `--alpha` paper at 30% runs across the band between `fix` and `triage`,
labelled `you read the note here`.

**Motion:** on scroll into view the four cards stagger in, 40ms apart, translateY 12px and
fade, `--sd-dur-3` at `--sd-ease`. The band's top corners interpolate from
`--sd-radius-band` to 0 as the band meets the viewport top. Reduced motion: cards appear at
final position, band corners stay at `--sd-radius-band`.

### 4. Read-only by construction

Back on `--sd-paper`. The most important section on the page and the plainest.

Headline, `display-m`, no italic here: **"It cannot write to your tracker."**

A card grid, `--sd-surface`, each card one guarantee with the mechanism named, because a
guarantee without a mechanism is a promise:

1. **Never writes to the tracker or the helpdesk.** In any mode. There is no write path in
   any adapter.
2. **Triage cannot edit files.** `Edit`, `Write`, `MultiEdit` and `NotebookEdit` are denied
   outright. Bash is pattern-matched segment by segment; substitution and redirection are
   refused.
3. **Fetching is judged by destination.** `permissions.fetch` lists the hosts a session may
   reach, and it is empty by default. A ticket comment cannot pick the destination.
4. **Fix is gated on a person.** It refuses to start unless the note's status is `triaged`
   or `fix-approved`. Running the command is the approval.
5. **The snapshot check.** sha256 of `.sirdar/` and the git hooks directory before and
   after the session. Any difference fails the run before the first git command, so nothing
   is committed and nothing is pushed.
6. **Your logins, your machine.** No Sirdar account, no server, no telemetry.

Each card is quiet: `--sd-rule-strong` border, `--sd-radius-md`, no shadow, the numeral in
mono at `--sd-ink-3`. The `permissions.fetch` and snapshot cards link into
`docs/config.md` and `docs/fix.md`.

**Motion:** none. This is the section a sceptic reads twice and motion is in the way. The
only transition is the link hover, a `--sd-highlight` marker sweep that grows from 0 to a
25px inset underline over `--sd-dur-3`.

### 5. The board

A media frame, `--sd-radius-lg`, `--sd-shadow-soft`, on `--sd-paper`, sitting slightly
wider than the prose measure.

Content: a screenshot of the desktop app's board, six lanes with their status rails, run
cards with keys in mono, elapsed clocks, and cost chips. Use a real capture from a workspace
with fabricated ticket keys and titles, never a real customer's text. If the capture is not
ready, the fallback is a rendered mock built from the library's own Kanban column and Run
card examples, which is one reason those two are components 6 and 7.

A caption on a shallow arc crosses the lower third of the image in paper ink:
`six lanes, one hue each, the rail is the legend`.

**Motion:** the arc caption is static, drifting 3deg over 12s on hover only. Inside the
image, one card in the `gathering` lane has a live elapsed clock that counts, in the accent,
at 1s intervals. Reduced motion: the clock shows a fixed value and the caption does not
drift.

### 6. What a note looks like

`--sd-band-ink`, `--sd-radius-band`, paper ink. Used once per page and this is the once.

Headline, `display-m`, italic second clause: **"The output is a note. *You decide if it is
right.*"**

Two columns on wide screens, stacked under 900px:

- Left: an actual Triage Note rendered in the display serif at `--sd-measure-prose`,
  showing the sections (Complaint, Repro, Root-cause hypothesis with a confidence, Proposed
  fix, Open questions) with real Arabic in the Complaint block, laid out RTL, English
  headings, English body. This is the page's proof that bilingual is not a checkbox.
- Right: the confidence badge and the open-questions list pulled out, with one line of copy:
  "A low-confidence note with three open questions is a useful note. It tells you what to
  look at."

**Motion:** none. The Arabic block is a real quotation of a fabricated complaint, never a
customer's.

### 7. Cost

Short, on `--sd-paper`. A quota chip row showing a provider, a bar, a percentage, and a
reset time.

Copy: "Sirdar spends your subscription, not a token you buy from us. Every run has a turn
budget, a wall-clock budget, and a USD budget; a session that goes silent for six minutes is
cancelled rather than held to the end of the clock."

**Motion:** the quota bar fills from 0 to its value once on scroll into view, `--sd-dur-4`,
`--sd-ease`. Reduced motion: it starts full.

### 8. Footer

`--sd-band-deep`, `--sd-radius-band` on the top corners only, so the page ends on the same
colour it turned at section 3.

Four columns: Product (Install, Docs, Changelog, Releases), Source (GitHub, Contributing,
Code of conduct, Security policy), Docs (Getting started, Configuration, Sources, Fix flow,
Evaluation), and About.

The About column carries the name's origin in two sentences, in the display serif italic,
because it is the one piece of copy on the page that is not functional and it belongs at the
end: on a Himalayan expedition the sirdar is the lead Sherpa, the one who assigns the team's
work and answers for the outcome.

Bottom rule: `MIT` - `(c) Srivathsan Venkateswaran` - the version, in mono at paper 70%.

**Motion:** none.

## Section order and band sequence

paper, paper, **deep band**, paper, paper, **ink band**, paper, **deep band footer**.

Never two coloured bands adjacent. The paper sections between them are what make the two
coloured ones land.

## What the page does not have

No testimonials, because there are no users yet and inventing one is fraud. No customer
logo wall. No comparison table against a named competitor. No pricing section, because
there is no price. No cookie banner, because there are no cookies: the page loads
self-hosted fonts, no analytics, and no third-party script.

## Publishing

`.github/workflows/site.yml` deploys the page on every push to `main` that touches
`site/**`, `docs/**`, `mkdocs.yml` or the workflow, and on `workflow_dispatch`: `site/` is
copied to `public/`, `mkdocs build --strict` writes into `public/docs/`, and the directory
is uploaded with `actions/upload-pages-artifact` and deployed with `actions/deploy-pages`
to the `github-pages` environment. The page is served at
<https://srivathsanvenkateswaran.github.io/Sirdar/> and the docs at
<https://srivathsanvenkateswaran.github.io/Sirdar/docs/>, which is `site_url` in
`mkdocs.yml`. The old `docs.yml` (`mkdocs gh-deploy` to a `gh-pages` branch) is retired.

This needs the repository's Pages source set to **GitHub Actions** (Settings › Pages ›
Build and deployment › Source); with the source still on a branch, the workflow uploads
an artifact that is never served. Every asset path in `site/` is relative so the page
works under the `/Sirdar/` prefix.

## What the built page does differently (2026-09-16)

The page as built follows this document's language and departs from its section list in
these ways, each for a reason in the repository: the hero shows a real screenshot of the
Session window (Conversation layout) rather than an animated ticket, and names the desktop
app for macOS and Windows beside the CLI; the step band has five commands (`doctor` is a
real step between `init` and `triage`); the providers band shows the vendors' marks, which
the user chose over original glyphs on 2026-09-15, while sources stay as type; a "three
ways to read a run" band shows the Conversation, Document and Workbench layouts; the note
and cost sections are folded into the hero shot and the read-only lede; the licence is
Apache-2.0, not MIT; and there is no `brew` line because the tap does not exist yet.
`site/README.md` says where each sentence comes from.
