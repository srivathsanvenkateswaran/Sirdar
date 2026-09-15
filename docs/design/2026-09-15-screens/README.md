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

## Round 2 (same day): the reference app's register

The user's review: the skeleton is right, the UI is too dense. The second round re-scales every
screen to the reference app's register and borrows these elements from it:

- 16px base type (Figtree, with Inter as the fallback), a 248px sidebar with 44px rows and 20px
  icons, a white sheet on the warm ground with 16px corners, 40px controls, 48px table rows.
- Sans 32px screen titles (Register, Eval, Library, Board); the serif returns for the settings
  page heading and note titles, which is where the reference uses it too.
- Cards with a pale fill and 24px padding; big figures with a small grey label under them.
- Tracked small labels with a dashed rule for groups of things ("Landed today"), list items with
  a left tone rail and an icon box, a tinted banner for a step that finished.
- Settings rows at 104px with an 18px label and a wide pale button.
- Motion (the walkthrough shows it on page load): content rises 8px and fades in over 320ms;
  the settings modal scales from 0.98 and fades over 300ms behind a 200ms scrim; both freeze
  under reduced motion. In the app the same two motions answer navigation and opening
  settings, and nothing else moves except the live-run pulse.

## Provider marks

`marks/` holds the vendors' own marks (Claude, Codex, OpenAI, GitHub Copilot, Antigravity, Gemini,
Qwen, Cursor, OpenCode, Kimi), as distributed by the Simple Icons and lobehub icon sets. They are
the vendors' trademarks, used only to say which provider is running a session: each is shown in
its own colour on a neutral tile and never recoloured or altered. The user chose the official
marks over original glyphs on 2026-09-15; if a vendor's brand guidelines ever object, the tile
falls back to the provider's name.
