# Design

One language across three surfaces: the landing page, the desktop app, and this site. The
values live in one tokens file and every surface reads a copy of it.

## The language { #the-language }

<div class="sd-cards" markdown>

- [Design language](00-design-language.md)

    What is borrowed and what is not, the colour system, type, spacing, elevation, motion, and the accessibility floor.

- [Tokens](01-tokens.md)

    The contract `tokens.css` implements: every colour with its measured contrast ratio, and the migration map.

- [Landing page](02-landing-page.md)

    One page, one call to action. Copy tone, the band sequence, and what each section is for.

- [Desktop app](03-desktop-app.md)

    The app as a window ground with one content sheet on it, and the measurements behind the shell tokens.

- [Component library](library/README.md)

    One folder per component, each with its spec, its states, and the tokens it reads.

</div>

## Rounds { #rounds }

Review pages from each design round, self-contained HTML. They are the record of what was
shown and what was chosen.

<div class="sd-cards" markdown>

- [Screens, 2026-09-15](2026-09-15-screens/index.html)

    Board, Session, Providers, Register, Eval, Library, Settings: the walkthrough the app was re-based on.

- [Session window, 2026-09-16](2026-09-16-session/index.html)

    Three directions for the Session window, six states each.

- [Logo, 2026-09-16](2026-09-16-logo/index.html)

    Six candidate marks in Nepal's flag colours, at 16 and at 512.

- [The mark as shipped](2026-09-16-logo/final/index.html)

    Mark 1, twin peak with a route to the summit: the icon, the favicons, the lockup.

</div>
