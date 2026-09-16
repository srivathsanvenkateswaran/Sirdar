# `site/` — the landing page

One page, no build step. `index.html`, `styles.css`, `hero.js`, `download.js`,
`favicon.svg`, plus `tokens.css`, which is a copy of `desktop/frontend/src/styles/tokens.css`
and is not edited here; `shots/` holds four screenshots of the served app and `marks/` the
provider marks the providers band shows.

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

## The download buttons

`download.js` makes the hero button and the download band know the visitor's platform. The
markup is complete without it: the hero button says "Download" and every button in the band
opens the releases page, in the static order macOS, Windows, Linux. The script upgrades that
in two steps that do not depend on each other.

**Detection**, synchronous, before first paint settles. `navigator.userAgentData.platform`
where a browser has it (Chromium: `macOS`, `Windows`, `Linux`, and `Android`, `iOS`,
`Chrome OS`, which count as unknown), else `navigator.userAgent` and `navigator.platform`,
with phones and tablets ruled out first because an iPad reports `Macintosh` and Android
reports `Linux`. The hero label becomes "Download for macOS", "Download for Windows" or
"Download for Linux"; the matching card moves to the front of the band and shows a "Your
platform" tag; a macOS visitor also sees the "Intel Mac" line under the hero button,
because the chip cannot be read from a browser and Apple silicon is the default. Unknown
and mobile leave everything as the markup has it.

**Resolution**, asynchronous. One `fetch` of
`https://api.github.com/repos/srivathsanvenkateswaran/Sirdar/releases/latest`, cached in
`sessionStorage` under `sirdar.release.latest` for an hour, gives the tag and the asset
names. Each button whose `<os>_<arch>` zip is in that list gets
`https://github.com/srivathsanvenkateswaran/Sirdar/releases/download/<tag>/sirdar-desktop_<tag>_<os>_<arch>.zip`,
built from the tag and the name rather than copied from the response, and the note under
the band swaps to one that names the tag. On a 404 (no release yet, also cached), a
rate-limit, or no network, the hrefs stay on the releases page and the label keeps the OS
name.

The four suffixes the script knows, `darwin_arm64`, `darwin_x64`, `windows_x64` and
`linux_x64`, are exactly the zips `.github/workflows/release.yml`'s desktop job uploads.
Rename one there and this file and the `data-dl-asset` attributes in `index.html` change
with it; never reference a name the workflow does not produce.

**Testing.** `?os=mac`, `?os=windows`, `?os=linux` or `?os=unknown` on the URL overrides
detection (`macos`, `darwin`, `win` are accepted too), so
`http://localhost:8000/?os=windows` shows a Mac what a Windows visitor sees. Chrome's device
emulation (DevTools › toggle device toolbar, or a custom device with a Windows or Linux
user-agent string) exercises the real detection path; note that Chrome's emulated devices
also change `navigator.userAgentData.platform`, which is what the script reads first. To see
the resolved state before a release exists, seed the cache from the console:

```
sessionStorage.setItem("sirdar.release.latest", JSON.stringify({tag: "v0.1.0", at: Date.now(),
  assets: ["sirdar-desktop_v0.1.0_darwin_arm64.zip", "sirdar-desktop_v0.1.0_darwin_x64.zip",
           "sirdar-desktop_v0.1.0_windows_x64.zip", "sirdar-desktop_v0.1.0_linux_x64.zip"]}))
```

and reload. `sessionStorage.clear()` undoes it. The page makes no other request than this
one to `api.github.com`, and nothing at all with JS off.

## What to check before shipping a change

- **The download buttons.** Open the page with each of `?os=mac`, `?os=windows`, `?os=linux`
  and `?os=unknown`, at 1440 and at 400 wide, and read the hero label, its `href`, and which
  card the band puts first. With no release published, every `href` is the releases page.
- **Reduced motion.** In Chrome DevTools, Rendering → Emulate CSS
  `prefers-reduced-motion: reduce`. The ring should sit still with `triage` at 9 o'clock
  and the sources row should be a wrapped list with no edge mask.
- **Light only.** The page sets `data-theme="light"` and `color-scheme: light`; it does
  not answer a dark preference. Every colour still comes from `tokens.css`.
- **Narrow.** 360px is the floor, and nothing may scroll horizontally. The screenshots
  scale with their frames. Headless Chrome will not open a window narrower than about 500px,
  so a bare `--window-size=400,...` screenshot lays the page out wider than it captures;
  check a narrow width with device emulation or an iframe of the exact width instead.
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
woff2 files in `site/fonts/`, with no other change. The only other request the page makes
is `download.js`'s one call to `api.github.com` for the latest release; no analytics and no
third-party script.

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

Every claim on the page is in the repository. The page sells the whole arc, triage, fix
and RCA, with the read-only triage session and the fix session's worktree presented as the
confinement that makes the fix trustworthy rather than as the headline. The five steps are
the CLI's own commands (`README.md`, "Quick start"); the fix row and the safety band's
worktree, gate and deviation rows are `docs/fix.md`; the composer, the Changes pane with
Keep/Drop per hunk and the three layouts are `HANDOFF.md`'s screens list and
`docs/superpowers/specs/2026-09-15-ui-build-design.md`; steering is `docs/steer.md`; the
providers band and the rest of the safety band are `HANDOFF.md`'s provider table and
`docs/concepts.md`; the sources row is `docs/adapters.md`; the download band is
`docs/release.md` and `.github/workflows/release.yml`, including that the desktop builds are
unsigned, that Windows needs the WebView2 runtime, that Linux needs libwebkit2gtk-4.1 and
libgtk-3, that no release has been published yet, and that the Homebrew tap does not exist,
which is why the page carries no `brew` line. If a sentence here stops being true of the
product, it is a bug on this page.
