# Logo exploration, 2026-09-16

Six candidate marks for the Sirdar desktop app icon, which today ships the Wails default
"W". Open `index.html` for the review page (self-contained; the only network request is
Google Fonts for the wordmark, with system fallbacks). Nothing in the app is touched by this
round.

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

## Exporting an `.icns` once a mark is chosen

macOS wants a 1024×1024 PNG with the squircle already drawn into it (transparent corners,
no frame). Apple's grid puts the squircle at 824×824 centred in the 1024 canvas, leaving a
100pt margin on each side for the system shadow.

1. Compose the tile. Make `icon.svg` at 1024 with the squircle ground and the chosen mark
   scaled into the 824 box. The squircle path used on the review page is the `d` attribute
   of the first `<path>` in any tile; copy it from `index.html` (it is on the 512 grid, so
   scale by 824/512 = 1.609375 and translate by 100).

   ```xml
   <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">
     <g transform="translate(100 100) scale(1.609375)">
       <path d="…squircle path from index.html…" fill="#FAF7EC"/>
       <!-- paste the chosen mark's elements here, unchanged; they are already on the 512 grid -->
     </g>
   </svg>
   ```

   Use `#FAF7EC` for the light tile and `#17181C` for the dark tile, and apply the dark
   colour set from `index.html` for the dark variant.

2. Rasterise at 1024 with a transparent background. Either

   ```sh
   brew install librsvg
   rsvg-convert -w 1024 -h 1024 icon.svg -o icon_1024.png
   ```

   or, without librsvg, headless Chrome with a transparent default background:

   ```sh
   "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu \
     --hide-scrollbars --default-background-color=00000000 --window-size=1024,1024 \
     --screenshot=icon_1024.png "file://$PWD/icon.svg"
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

   Check the 16 and 32 steps by eye before shipping; `sips` downsamples with smoothing, and
   a mark with thin strokes (3, 4) may want a hand-tuned 16px raster instead.

4. Wire it in. Wails builds the macOS icon from `desktop/build/appicon.png` (1024×1024), so
   the 1024 PNG from step 2 replaces that file and `wails build` regenerates the `.icns`
   inside `Sirdar.app`. Do this in its own commit; this round deliberately does not touch
   the app.

The menu bar item, if one is added, is a separate template image (black shape with alpha,
16×16 and 32×32), not the tile: draw the mark's silhouette alone, with no squircle.
