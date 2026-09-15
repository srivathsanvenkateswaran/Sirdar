# Screens round, 2026-09-15

Static mocks of every desktop screen, for review before any UI is built. Open `index.html`
for the contact sheet, or a page directly; every page is 1440×900 and self-contained (the
only network request is Google Fonts, with system fallbacks).

| Page | What it shows |
|---|---|
| `Session.html` | The harness surface: transcript with tool rows, the agent's question, the composer with Answer, the Changes pane with diff and checks |
| `SessionEmpty.html` | A new session: composer, mode chips, and what landed |
| `Board.html` | The queue as lanes, plus the inbound strip |
| `Review.html` | Full-width change review before a branch: Keep/Drop per hunk, checks, what the agent said |
| `Register.html` | Runs per day over the run table |
| `Eval.html` | Golden set, options, last retro report |
| `Settings.html` | Settings modal, MCP servers page |
| `ToolTester.html` | Settings modal, Try a tool page |
| `Providers.html` | Settings modal, Providers page |
| `Library.html` | The component gallery |

`BRIEF.md` is the brief the pages were drawn to. Every value comes from the shipped tokens
(`desktop/frontend/src/styles/tokens.css`, inlined here so the pages open without a build);
ticket SBX-1 is the sandbox ticket and every name, key and server is fabricated.
