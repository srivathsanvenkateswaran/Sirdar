# `site/` — the landing page

One page, no build step. `index.html`, `styles.css`, `hero.js`, `favicon.svg`, plus
`tokens.css`, which is a copy of `desktop/frontend/src/styles/tokens.css` and is not
edited here; `shots/` holds four screenshots of the served app and `marks/` the provider
marks the providers band shows.

## Preview

```
python3 -m http.server 8000 --directory site
```

or, from the repository root, `make site`. Either way the page is at
<http://localhost:8000>. With Node instead:

```
npx serve site
```

Opening `site/index.html` straight off the filesystem mostly works, but `file://`
blocks the clipboard API, so the Copy control falls back to selecting the command. The
Docs links point at the published docs site, so they leave a local preview.

## Publishing

`.github/workflows/site.yml` deploys on every push to `main` that touches `site/**`,
`docs/**`, `mkdocs.yml` or the workflow itself, and on `workflow_dispatch`. One job copies
this directory to `public/`, runs `mkdocs build --strict --site-dir public/docs`, uploads
`public/` as the Pages artifact and deploys it to the `github-pages` environment. The
landing page is served at <https://srivathsanvenkateswaran.github.io/Sirdar/> and the docs
at <https://srivathsanvenkateswaran.github.io/Sirdar/docs/>, which is also `site_url` in
`mkdocs.yml`.

**The one repository setting this needs:** Settings › Pages › Build and deployment ›
Source: **GitHub Actions**. Until that is switched from "Deploy from a branch", the
workflow uploads an artifact that is never served.

Every asset path in this directory is relative (`styles.css`, `shots/board.png`,
`marks/claude.svg`), so the page works under the `/Sirdar/` prefix as well as at a root.
Keep it that way; a leading slash would break the published page and pass locally.

## One collision this directory caused

`site/` was MkDocs' default build directory and was gitignored as such. Two lines
changed to make room for the landing page: `.gitignore` no longer ignores `site/`, and
`mkdocs.yml` sets `site_dir: .mkdocs-site`, which is ignored instead. The workflow passes
its own `--site-dir`, so a local `mkdocs build` and the deploy never write here.

## What to check before shipping a change

- **Reduced motion.** In Chrome DevTools, Rendering → Emulate CSS
  `prefers-reduced-motion: reduce`. The ring should sit still with `triage` at 9 o'clock
  and the sources row should be a wrapped list with no edge mask.
- **Light only.** The page sets `data-theme="light"` and `color-scheme: light`; it does
  not answer a dark preference. Every colour still comes from `tokens.css`.
- **Narrow.** 360px is the floor, and nothing may scroll horizontally. The screenshots
  scale with their frames.
- **RTL.** Set `dir="rtl"` on `<html>` in the browser console. The layout is written in
  logical properties, so it should mirror whole; keys, commands, clocks and costs stay
  left-to-right, the hard shadow under the primary button moves to the leading edge, and
  the ring and the marquee reverse.
- **Tokens.** `./scripts/check-tokens.sh` (or `make check-tokens`). If it fails, run
  `make tokens` and commit the copy — never edit `site/tokens.css` by hand.
- **Markup.** `npx html-validate site/index.html`.
- **Under the prefix.** Copy `site/` to `public/Sirdar/`, serve `public/`, and open
  `/Sirdar/`; every image and stylesheet must load.

## Fonts

Newsreader, Figtree, Inter, JetBrains Mono and Noto Naskh Arabic are loaded from Google
Fonts, each with a real fallback declared in `tokens.css` (Georgia, system-ui,
`ui-monospace`). `docs/design/00-design-language.md` prefers them self-hosted so the page
makes no third-party request; swapping is a `@font-face` block in `styles.css` and the
woff2 files in `site/fonts/`, with no other change. Nothing else on the page is fetched
from anywhere: no analytics and no third-party script.

## The screenshots

`shots/` was taken on 2026-09-16 from `sirdar serve` on `127.0.0.1:47357`, built from
this commit's frontend (`make ui && go build ./cmd/sirdar`), against the sandbox
workspace `~/Documents/Personal/sirdar-sandbox/app`, in headless Chrome with a fresh
profile at 1440×900, with reduced motion emulated so nothing was mid-transition:

| File | What it is |
|---|---|
| `session-conversation.png` | run `20260915T121105Z-076d`, Session window, Conversation layout (the hero, and the first of the three small shots) |
| `session-document.png` | the same run, Document layout, resized to 1080 wide |
| `session-workbench.png` | the same run, Workbench layout, resized to 1080 wide |
| `board.png` | the Board for the sandbox workspace |

Each is at or under 350 KB: the two small ones were resized with `sips -Z 1080` and then
quantised to a 256-colour palette, which is lossless for a flat UI. The ticket (SBX-1),
the shop and the code in every shot are the sandbox's own fabrications; no customer's text
appears. Retake all four together when the app's chrome changes, and never start a run
to do it — the sandbox already holds finished ones.

## The provider marks

`marks/` is copied from `docs/design/2026-09-15-screens/marks/`: the vendors' own marks
for Claude, Codex, OpenAI, Qwen, Cursor, GitHub Copilot, OpenCode and Kimi, as distributed
by the Simple Icons and lobehub icon sets. They are the vendors' trademarks, used only to
say which CLI a run is on, each on a neutral `--sd-sunk` tile. The `agy` provider is
disabled by Sirdar itself and is not shown. Sources and helpdesks are named in type only;
the page's own aside says why.

## Where the copy comes from

Every claim on the page is in the repository. The five steps are the CLI's own commands
(`README.md`, "Quick start"); the providers band and the read-only ledger are
`HANDOFF.md`'s provider table and `docs/concepts.md`; the sources row is
`docs/adapters.md`; the download band is `docs/release.md`, including that the desktop
builds are unsigned, that Windows needs the WebView2 runtime, that no release has been
published yet, and that the Homebrew tap does not exist, which is why the page carries no
`brew` line. If a sentence here stops being true of the product, it is a bug on this page.
