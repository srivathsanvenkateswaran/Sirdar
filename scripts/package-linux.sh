#!/usr/bin/env sh
# Stages the Linux desktop release next to the binary `wails build` just
# produced, so that whatever zips desktop/build/bin picks up an archive a
# person can actually install from rather than a bare executable:
#
#   Sirdar          the app, as wails built it
#   sirdar.desktop  the launcher entry
#   sirdar.png      the 512x512 hicolor icon
#   install.sh      copies all three into ~/.local, no sudo
#
# The three added files are committed under desktop/build/linux/ and are
# copied, never generated, so the archive and a source checkout cannot
# disagree about what the launcher says.
#
#   scripts/package-linux.sh
#
# Run it after `wails build -tags webkit2_41` (or `make desktop-linux`) on
# a Linux host. Both .github/workflows/desktop.yml and release.yml run it
# between the build and the upload, which is what keeps the CI artifact and
# the release zip the same thing.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
src="$root/desktop/build/linux"
bin="$root/desktop/build/bin"

[ -f "$bin/Sirdar" ] || {
	echo "package-linux: $bin/Sirdar is missing — build the app first:" >&2
	echo "  cd desktop && wails build -tags webkit2_41" >&2
	exit 1
}

for f in sirdar.desktop sirdar.png install.sh; do
	cp "$src/$f" "$bin/$f"
done
chmod 755 "$bin/install.sh"

echo "ok   $bin now holds: $(ls "$bin" | tr '\n' ' ')"
