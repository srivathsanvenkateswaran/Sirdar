# Logo exploration, 2026-09-16

Six candidate marks for the Sirdar desktop app icon, which shipped the Wails default "W"
until this round. Open `index.html` for the review page (self-contained; the only network
request is Google Fonts for the wordmark, with system fallbacks).

Mark 1 was chosen the same day and is now the app's icon, its favicons and its sidebar
wordmark. Skip to [What was chosen](#what-was-chosen) for the shipped files and where
each one went; the six below are kept as the record of the round.

## Brief

Sirdar is the Sherpa word for the head guide of a mountain expedition, the one who leads
the climb. The mark is a mountain motif in the colours of Nepal's flag:

| Role | Hex |
|---|---|
| Crimson | `#DC143C` |
| Blue | `#003893` |
| White | `#FFFFFF` |
| Paper (light ground, from the app's tokens) | `#FAF7EC` |
| Ink (dark ground, from the app's tokens) | `#17181C` |

Constraints: original vector only, no third-party marks, no fonts embedded in the mark
itself (the wordmark on the page uses Newsreader and Figtree from Google Fonts). Each mark
has to read at 16px (menu bar, favicon) and at 512px (Dock). Every mark is drawn on a 512
grid so it drops straight into the macOS squircle.

Each variation on the page shows: the tile at 512 on a light (paper) and a dark (ink)
ground; the 128, 32 and 16 steps; a true 16-pixel raster enlarged without smoothing; the
16px glyph placed in a menu bar; the lockup with the wordmark "Sirdar"; one line of
rationale and the main tradeoff.

Blue swaps to paper on the dark tile because `#003893` on `#17181C` is 1.6:1 and vanishes.
Crimson stays on both tiles.

## The six

| File | Idea | Main tradeoff |
|---|---|---|
| `mark-1-twin-peak-route.svg` | Blue peak behind a crimson one, a white route up to a summit marker | The route is gone at 16px; two triangles remain |
| `mark-2-pennant-peak.svg` | One crimson pennon with blue fimbriation, a white summit where the flag carries its sun | Reads as flag first, mountain second; weight sits lower-left |
| `mark-3-rope-peak-monoline.svg` | One continuous rope stroke over the summit and through a loop, blue summit dot | 1.4px line at 16px; the loop closes into a blob below 32 |
| `mark-4-negative-s-mountain.svg` | Crimson peak with the initial S cut out as a switchback trail (a real hole, via mask) | The S is a scratch at 16px; the letter appears from 32 up |
| `mark-5-summit-marker.svg` | Compass ring with the peak as the north needle and a pivot dot as the summit marker | Crowded category; needle has to be read as a peak |
| `mark-6-triangle-sun.svg` | Crimson triangle with a blue disc rising behind its shoulder | The most generic construction; the colour pair does the identifying |

The `.svg` files are the bare mark on a transparent ground in the light-tile colours. The
review page draws the same geometry through CSS variables, which is how the dark tiles are
produced; if a mark is chosen, the light and dark colour sets are in `index.html` under
`.mN.light` and `.mN.dark`.

## What was chosen

Mark 1, `mark-1-twin-peak-route.svg`, on 2026-09-16, with one change to the dark
variant: rather than the round's ink tile, **blue and white trade places**. The light
tile keeps the paper ground, the blue back peak, the crimson front peak and the white
route; the dark tile is a blue ground, a paper back peak, the same crimson, and the
route in blue. Crimson holds on both. That leaves two colour sets to keep in step
instead of three, and it drops the `#17181C` tile the round had drawn.

The shipped files are in [`final/`](final/), with [`final/index.html`](final/index.html)
as the record of what they look like at 512, 128, 32 and 16, on a tile and bare.

| File | What it is |
|---|---|
| `final/sirdar-mark-light.svg` | The bare mark, light set, on the 512 grid |
| `final/sirdar-mark-dark.svg` | The bare mark, dark set |
| `final/sirdar-tile-light.svg` | The mark on its squircle, 1024 grid, inset to 824 |
| `final/sirdar-tile-dark.svg` | The same, dark set. Reference only; nothing ships it |
| `final/sirdar-wordmark.svg` | Mark and word, with the word as live text in Figtree |
| `final/index.html` | The page above |

Where they went:

- `desktop/build/appicon.png` — the light tile at 1024, which is the file Wails reads
- `desktop/frontend/public/favicon.svg`, `site/favicon.svg` — copies of the light mark
- `desktop/frontend/src/ui/brand-mark/` — the mark as a component, drawn in the sidebar
  before the word. Its test reads the two master SVGs and fails if the component's
  geometry or colour drifts from them
- `site/index.html` — the mark at 18px before the nav's lowercase wordmark

## Regenerating the icon

`scripts/make-icons.sh` is the recipe below, run. It rasterises `sirdar-tile-light.svg`
at 1024 with a transparent background, writes `desktop/build/appicon.png`, and builds an
`.icns` from the ten `sips` sizes for you to look at before shipping.

```sh
scripts/make-icons.sh            # appicon.png in the repo, .icns in a temp dir
scripts/make-icons.sh /tmp/icons # or say where the .icns and the iconset go
```

It prefers `rsvg-convert` (`brew install librsvg`), then ImageMagick, then headless
Chrome, which needs nothing installed. Only `desktop/build/appicon.png` is written into
the repo: nothing in `desktop/wails.json` or `desktop/build/` reads a committed `.icns`,
and a second copy of the icon in the tree is a second thing to keep in step.

`desktop/build/windows/icon.ico` was the Wails default "W" and is now deleted, which is
the behaviour `desktop/build/README.md` documents: with no `icon.ico` present, `wails
build` makes one from `appicon.png`. That keeps one source for both platforms.

## The recipe, by hand

macOS wants a 1024×1024 PNG with the squircle already drawn into it (transparent corners,
no frame). Apple's grid puts the squircle at 824×824 centred in the 1024 canvas, leaving a
100pt margin on each side for the system shadow.

1. Compose the tile. `final/sirdar-tile-light.svg` is this, already made: the squircle
   path from `index.html` (it is on the 512 grid) and the chosen mark, both inside a
   `<g transform="translate(100 100) scale(1.609375)">`, since 824/512 = 1.609375.

   ```xml
   <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">
     <g transform="translate(100 100) scale(1.609375)">
       <path d="…squircle path from index.html…" fill="#FAF7EC"/>
       <!-- the chosen mark's elements, unchanged; they are already on the 512 grid -->
     </g>
   </svg>
   ```

   Use `#FAF7EC` for the light tile and `#003893` for the dark one, with the dark colour
   set above.

2. Rasterise at 1024 with a transparent background. Either

   ```sh
   brew install librsvg
   rsvg-convert -w 1024 -h 1024 final/sirdar-tile-light.svg -o icon_1024.png
   ```

   or, without librsvg, headless Chrome with a transparent default background. Chrome
   screenshots the viewport rather than the document, so point it at a page that is
   exactly 1024 square with no margin, which is what the script writes:

   ```sh
   "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu \
     --hide-scrollbars --default-background-color=00000000 --window-size=1024,1024 \
     --screenshot=icon_1024.png "file://$PWD/shoot.html"
   ```

3. Build the iconset. `iconutil` requires these ten names exactly.

   ```sh
   mkdir Sirdar.iconset
   for s in 16 32 128 256 512; do
     sips -z $s $s icon_1024.png --out Sirdar.iconset/icon_${s}x${s}.png
     d=$((s*2))
     sips -z $d $d icon_1024.png --out Sirdar.iconset/icon_${s}x${s}@2x.png
   done
   iconutil -c icns Sirdar.iconset -o appicon.icns
   ```

   Check the 16 and 32 steps by eye before shipping; `sips` downsamples with smoothing.

4. Wire it in. Wails builds the macOS icon from `desktop/build/appicon.png` (1024×1024),
   so the PNG from step 2 replaces that file and `wails build` regenerates
   `Contents/Resources/iconfile.icns` inside `Sirdar.app`.

The menu bar item, if one is added, is a separate template image (black shape with alpha,
16×16 and 32×32), not the tile: draw the mark's silhouette alone, with no squircle.
