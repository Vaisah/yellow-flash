#!/usr/bin/env bash
# Installs Yellow Flash for the current user, without root:
#
#   ~/.local/lib/yellow-flash/   the app, the browser helper, the command line,
#                                the MCP server and the browser extension
#   ~/.local/bin/                yellow-flash, yellowflash-cli, yellowflash-mcp
#   ~/.local/share/applications  the menu entry
#
# then connects the browser helper to every installed browser.
#
#   build/linux/install.sh               build, then install
#   build/linux/install.sh --no-build    install what is already in build/bin
#   build/linux/install.sh --uninstall   remove it (downloads and settings stay)
#
# PREFIX (default ~/.local) moves all of it.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
prefix="${PREFIX:-$HOME/.local}"
lib="$prefix/lib/yellow-flash"
bin="$prefix/bin"
apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
desktop="$apps/yellow-flash.desktop"

uninstall() {
	if [ -x "$lib/yellowflash-nmhost" ]; then
		"$lib/yellowflash-nmhost" --unregister || true
	fi
	rm -f "$bin/yellow-flash" "$bin/yellowflash-cli" "$bin/yellowflash-mcp" "$desktop"
	rm -rf "$lib"
	command -v update-desktop-database >/dev/null && update-desktop-database "$apps" 2>/dev/null || true
	echo "Yellow Flash is removed. Your downloads and settings are still in ~/.config/Yellow Flash."
}

build=1
case "${1:-}" in
	--uninstall) uninstall; exit 0 ;;
	--no-build) build=0 ;;
	"") ;;
	*) echo "usage: $0 [--no-build | --uninstall]" >&2; exit 2 ;;
esac

if [ "$build" = 1 ]; then
	"$root/build/linux/build.sh"
fi
for f in "Yellow Flash" yellowflash-nmhost yellowflash-cli yellowflash-mcp; do
	[ -x "$root/build/bin/$f" ] || { echo "install.sh: build/bin/$f is missing: run without --no-build" >&2; exit 1; }
done

# A running copy keeps its old program file open; it is replaced, not
# written over, so this is safe, but the new version starts next time.
pgrep -x yellow-flash >/dev/null && echo "note: Yellow Flash is running; restart it to use the new version"

echo "==> installing into $lib"
mkdir -p "$lib" "$bin" "$apps"
# The helper looks for the app beside itself as "yellow-flash", and the app
# looks for the helper and browser-extension beside itself.
install -m 0755 "$root/build/bin/Yellow Flash" "$lib/yellow-flash.new" && mv -f "$lib/yellow-flash.new" "$lib/yellow-flash"
for f in yellowflash-nmhost yellowflash-cli yellowflash-mcp; do
	install -m 0755 "$root/build/bin/$f" "$lib/$f.new" && mv -f "$lib/$f.new" "$lib/$f"
done
install -m 0644 "$root/build/appicon.png" "$lib/yellow-flash.png"
install -m 0644 "$root/LICENSE" "$root/NOTICE.md" "$lib/"
rm -rf "$lib/browser-extension"
cp -r "$root/extension" "$lib/browser-extension"

for f in yellow-flash yellowflash-cli yellowflash-mcp; do
	ln -sf "$lib/$f" "$bin/$f"
done

cat > "$desktop" <<EOF
[Desktop Entry]
Name=Yellow Flash
Comment=Fast, resumable multi-connection downloads
Exec="$lib/yellow-flash"
Icon=$lib/yellow-flash.png
Terminal=false
Type=Application
Categories=Network;FileTransfer;
StartupNotify=true
EOF
command -v update-desktop-database >/dev/null && update-desktop-database "$apps" 2>/dev/null || true

echo "==> connecting the browser helper to your browsers"
"$lib/yellowflash-nmhost" --register || echo "note: the browser helper could not be registered; Yellow Flash tries again when it starts"

echo
echo "Yellow Flash is installed. Start it from the menu or with: yellow-flash"
echo "Browser extension (Load unpacked): $lib/browser-extension"
case ":$PATH:" in *":$bin:"*) ;; *) echo "note: $bin is not on your PATH; add it to use the commands" ;; esac
