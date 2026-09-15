# `site/` — the landing page

One page, three files, no build step. `index.html`, `styles.css`, `hero.js`, plus
`tokens.css`, which is a copy of `desktop/frontend/src/styles/tokens.css` and is not
edited here.

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
blocks the clipboard API, so the Copy control falls back to selecting the command.

## One collision this directory caused

`site/` was MkDocs' default build directory and was gitignored as such. Two lines
changed to make room for the landing page: `.gitignore` no longer ignores `site/`, and
`mkdocs.yml` sets `site_dir: .mkdocs-site`, which is ignored instead. `mkdocs gh-deploy`
in the existing docs workflow reads `site_dir`, so its behaviour is unchanged — but a
local `mkdocs build` on an older checkout would still write over this directory.

## What to check before shipping a change

- **Reduced motion.** In Chrome DevTools, Rendering → Emulate CSS
  `prefers-reduced-motion: reduce`. The hero sheet should render as the finished note
  with no scan rule, and the ring should sit still with `triage` at 9 o'clock.
- **Dark.** Rendering → Emulate `prefers-color-scheme: dark`. Every colour comes from
  `tokens.css`, so dark should need no page-level fix; if it does, the fix belongs in
  the token file.
- **Narrow.** 360px is the floor. The lanes in the board mock scroll horizontally on
  purpose; nothing else may.
- **RTL.** Set `dir="rtl"` on `<html>` in the browser console. The layout is written in
  logical properties, so it should mirror whole; keys, commands, clocks and costs stay
  left-to-right, and the hard shadow under the Install button moves to the leading edge.
- **Tokens.** `./scripts/check-tokens.sh` (or `make check-tokens`). If it fails, run
  `make tokens` and commit the copy — never edit `site/tokens.css` by hand.
- **Markup.** `npx html-validate site/index.html`.

## Fonts

Newsreader, Inter, JetBrains Mono and Noto Naskh Arabic are loaded from Google Fonts,
each with a real fallback declared in `tokens.css` (Georgia, system-ui, `ui-monospace`).
`docs/design/00-design-language.md` prefers them self-hosted so the page makes no
third-party request; swapping is a `@font-face` block in `styles.css` and four woff2
files in `site/fonts/`, with no other change. Nothing else on the page is fetched from
anywhere: no analytics, no third-party script, and no image — the board is real markup
and the favicon is an inline SVG.

## Where the copy comes from

Every claim on the page is in the repository. The trust section is `README.md` and
`docs/concepts.md`; the four commands are the CLI's own; the install line is
`docs/release.md`, including the fact that the Homebrew tap goes live with the first
published release. The Arabic complaint and the ticket keys in the board mock are
fabricated. If a sentence here stops being true of the product, it is a bug on this page.

## Publishing

`.github/workflows/site.yml` is a **proposal**: it would serve this directory at the
Pages root with the MkDocs site under `/docs/`. It is not wired to `push` and must not
be merged without the repository owner's say-so, because merging it changes what
<https://srivathsanvenkateswaran.github.io/Sirdar/> serves. Until then the docs live at
that root, which is where this page's "Docs" links point — one URL, in the nav and in
the footer, to update if the proposal lands.
