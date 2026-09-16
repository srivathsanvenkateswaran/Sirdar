#!/usr/bin/env sh
# Rebuilds the app icon from the logo SVGs.
#
# The SVGs in docs/design/2026-09-16-logo/final/ are the source. This script
# rasterises the light tile once at 1024 and writes:
#
#   desktop/build/appicon.png      the file Wails reads; it generates
#                                  Contents/Resources/iconfile.icns from this
#                                  on every `wails build`, and build/windows/
#                                  icon.ico too when that file is absent
#   <outdir>/Sirdar.iconset/       the ten sizes iconutil wants
#   <outdir>/Sirdar.icns           assembled from them, for eyeballing
#
# The .icns is not committed: nothing in desktop/wails.json or desktop/build/
# reads one, and a second copy of the icon in the tree is a second thing to
# keep in step. Build it to look at the 16 and 32 steps before shipping, then
# throw it away.
#
#   scripts/make-icons.sh [outdir]
#
# outdir defaults to a fresh directory under $TMPDIR, whose path is printed at
# the end. Nothing outside desktop/build/appicon.png is written into the repo.
#
# Rasteriser: rsvg-convert (`brew install librsvg`) or ImageMagick if either is
# on PATH, else headless Chrome, which every Mac with Chrome already has. All
# three are given a transparent background, which the squircle's corners need.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
src="$root/docs/design/2026-09-16-logo/final/sirdar-tile-light.svg"
appicon="$root/desktop/build/appicon.png"

out=${1:-$(mktemp -d "${TMPDIR:-/tmp}/sirdar-icons.XXXXXX")}
mkdir -p "$out"
png="$out/icon_1024.png"

[ -f "$src" ] || { echo "make-icons: missing $src" >&2; exit 1; }

chrome="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

if command -v rsvg-convert >/dev/null 2>&1; then
  echo "rasterising with rsvg-convert"
  rsvg-convert -w 1024 -h 1024 "$src" -o "$png"
elif command -v magick >/dev/null 2>&1; then
  echo "rasterising with ImageMagick"
  magick -background none -density 384 "$src" -resize 1024x1024 "$png"
elif [ -x "$chrome" ]; then
  echo "rasterising with headless Chrome"
  # Chrome screenshots the viewport, so the SVG goes on a page that is exactly
  # 1024 square with no margin and no scrollbars. --default-background-color
  # takes RRGGBBAA; 00000000 keeps the squircle's corners transparent.
  page="$out/shoot.html"
  cat > "$page" <<HTML
<!doctype html><meta charset="utf-8">
<style>
  html, body { margin: 0; padding: 0; background: transparent; }
  img { display: block; width: 1024px; height: 1024px; }
</style>
<img src="file://$src" alt="">
HTML
  "$chrome" --headless=new --disable-gpu --hide-scrollbars \
    --default-background-color=00000000 --window-size=1024,1024 \
    --screenshot="$png" "file://$page" >/dev/null 2>&1
  rm -f "$page"
else
  echo "make-icons: no rasteriser. Install librsvg (brew install librsvg) or Google Chrome." >&2
  exit 1
fi

[ -s "$png" ] || { echo "make-icons: the rasteriser wrote nothing to $png" >&2; exit 1; }

size=$(sips -g pixelWidth -g pixelHeight "$png" | awk '/pixel/ { print $2 }' | paste -sd x -)
[ "$size" = "1024x1024" ] || { echo "make-icons: got ${size}, wanted 1024x1024" >&2; exit 1; }

cp "$png" "$appicon"
echo "ok   desktop/build/appicon.png (1024x1024)"

# The ten names iconutil requires, nothing else in the folder.
iconset="$out/Sirdar.iconset"
rm -rf "$iconset"
mkdir -p "$iconset"
for s in 16 32 128 256 512; do
  sips -z "$s" "$s" "$png" --out "$iconset/icon_${s}x${s}.png" >/dev/null
  d=$((s * 2))
  sips -z "$d" "$d" "$png" --out "$iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$out/Sirdar.icns"
echo "ok   $out/Sirdar.icns"

echo
echo "Look at the small steps before shipping; sips downsamples with smoothing:"
echo "  open $iconset/icon_16x16.png $iconset/icon_32x32.png"
echo "Then rebuild the app so Wails regenerates iconfile.icns from the PNG:"
echo "  make desktop"
