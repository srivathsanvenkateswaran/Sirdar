#!/usr/bin/env sh
# Installs the Sirdar desktop app for the current user. No sudo, nothing
# outside $HOME, and every path is one the freedesktop specs already tell
# desktop environments to look in:
#
#   ~/.local/bin/sirdar-desktop                            the binary
#   ~/.local/share/applications/sirdar.desktop             the launcher entry
#   ~/.local/share/icons/hicolor/512x512/apps/sirdar.png   the icon
#
# $XDG_DATA_HOME replaces ~/.local/share throughout when it is set, the
# same variable the app itself honours when it picks where to keep the
# workspace registry, and $XDG_BIN_HOME replaces ~/.local/bin — the
# spelling pipx, uv and friends settled on for the user's own bin
# directory. An operator who has relocated either asked for everything to
# follow, and this is not the installer that argues.
#
#   ./install.sh              install or upgrade
#   ./install.sh --uninstall  remove all three files again
#
# This installs the desktop app only. The `sirdar` CLI is a separate
# download (`sirdar_<version>_linux_<arch>.tar.gz`) or `go install`; see
# docs/release.md.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
data=${XDG_DATA_HOME:-$HOME/.local/share}
bindir=${XDG_BIN_HOME:-$HOME/.local/bin}
appdir=$data/applications
icondir=$data/icons/hicolor/512x512/apps

bin=$bindir/sirdar-desktop
entry=$appdir/sirdar.desktop
icon=$icondir/sirdar.png

# Best-effort cache refreshes. A desktop that indexes on a timer picks the
# entry up anyway; these just make it immediate, and neither program is
# installed everywhere.
refresh() {
	if command -v update-desktop-database >/dev/null 2>&1; then
		update-desktop-database "$appdir" >/dev/null 2>&1 || true
	fi
	if command -v gtk-update-icon-cache >/dev/null 2>&1; then
		gtk-update-icon-cache -f -t "$data/icons/hicolor" >/dev/null 2>&1 || true
	fi
}

if [ "${1:-}" = "--uninstall" ]; then
	rm -f "$bin" "$entry" "$icon"
	refresh
	echo "removed $bin, $entry and $icon"
	echo "Workspaces, notes and the golden set are untouched; ~/.sirdar is yours to delete."
	exit 0
fi

if [ "${1:-}" != "" ]; then
	echo "install.sh: unknown argument $1 (try --uninstall, or no argument at all)" >&2
	exit 2
fi

for f in Sirdar sirdar.desktop sirdar.png; do
	[ -f "$here/$f" ] || { echo "install.sh: $f is missing from $here; unzip the whole archive and run it from there" >&2; exit 1; }
done

mkdir -p "$bindir" "$appdir" "$icondir"

# An upgrade over a running app: writing into a busy executable gives
# ETXTBSY, so the old inode is unlinked first and the running process keeps
# the copy it already mapped.
rm -f "$bin"
cp "$here/Sirdar" "$bin"
chmod 755 "$bin"

cp "$here/sirdar.png" "$icon"

# Exec is rewritten to the absolute path rather than left as the bare
# program name: the graphical session's PATH is assembled before any shell
# profile runs on most desktops, so ~/.local/bin is frequently not in it,
# and a launcher that cannot find its own binary fails with no message
# anywhere a person would look.
sed "s|^Exec=.*|Exec=$bin|" "$here/sirdar.desktop" > "$entry"
chmod 644 "$entry"

refresh

echo "installed:"
echo "  $bin"
echo "  $entry"
echo "  $icon"
echo
echo "Launch it from the applications menu, or run: $bin"

case ":${PATH}:" in
*":$bindir:"*) ;;
*)
	echo
	echo "note: $bindir is not on your PATH, so \`sirdar-desktop\` will not run by name."
	echo "      Add it in your shell profile: export PATH=\"\$HOME/.local/bin:\$PATH\""
	;;
esac
