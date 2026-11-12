#!/usr/bin/env bash
# Builds a Debian/Ubuntu package of Yellow Flash:
#   dist/linux/yellow-flash_<version>_<arch>.deb
#
#   build/linux/make-deb.sh              build, then package
#   build/linux/make-deb.sh --no-build   package what is already in build/bin
#
# Install it with:  sudo apt install ./dist/linux/yellow-flash_<version>_<arch>.deb
#
# Layout: the programs and the extension in /usr/lib/yellow-flash (each finds
# the others beside itself), links in /usr/bin, and the browsers' system-wide
# native messaging manifests naming /usr/bin/yellowflash-nmhost, so the
# browser helper works for every user and goes away with the package.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

command -v dpkg-deb >/dev/null || { echo "make-deb.sh: dpkg-deb is needed (Debian/Ubuntu)" >&2; exit 1; }

if [ "${1:-}" != "--no-build" ]; then
	build/linux/build.sh
fi
for f in "Yellow Flash" yellowflash-nmhost yellowflash-cli yellowflash-mcp; do
	[ -x "build/bin/$f" ] || { echo "make-deb.sh: build/bin/$f is missing: run without --no-build" >&2; exit 1; }
done

version="$(sed -n 's/^const AppVersion = "\(.*\)"$/\1/p' internal/ipc/datadir.go)"
[ -n "$version" ] || { echo "make-deb.sh: no AppVersion in internal/ipc/datadir.go" >&2; exit 1; }
arch="$(dpkg --print-architecture)"
out="dist/linux/yellow-flash_${version}_${arch}.deb"

pkg="$(mktemp -d)"
trap 'rm -rf "$pkg"' EXIT
lib="$pkg/usr/lib/yellow-flash"
mkdir -p "$pkg/DEBIAN" "$lib" "$pkg/usr/bin" "$pkg/usr/share/applications" \
	"$pkg/usr/share/pixmaps" "$pkg/usr/share/doc/yellow-flash"

install -m 0755 "build/bin/Yellow Flash" "$lib/yellow-flash"
install -m 0755 build/bin/yellowflash-nmhost build/bin/yellowflash-cli build/bin/yellowflash-mcp "$lib/"
cp -r extension "$lib/browser-extension"
find "$lib/browser-extension" -type d -exec chmod 0755 {} + -o -type f -exec chmod 0644 {} +
for f in yellow-flash yellowflash-nmhost yellowflash-cli yellowflash-mcp; do
	ln -s "../lib/yellow-flash/$f" "$pkg/usr/bin/$f"
done

install -m 0644 build/appicon.png "$pkg/usr/share/pixmaps/yellow-flash.png"
install -m 0644 LICENSE NOTICE.md README.md "$pkg/usr/share/doc/yellow-flash/"
cat > "$pkg/usr/share/applications/yellow-flash.desktop" <<'EOF'
[Desktop Entry]
Name=Yellow Flash
Comment=Fast, resumable multi-connection downloads
Exec=yellow-flash
Icon=yellow-flash
Terminal=false
Type=Application
Categories=Network;FileTransfer;
StartupNotify=true
EOF

# The system-wide host manifests (Chrome, Chromium, Edge, Firefox).
build/bin/yellowflash-nmhost --package-manifests "$pkg" >/dev/null

size="$(du -sk --exclude=DEBIAN "$pkg" | cut -f1)"
cat > "$pkg/DEBIAN/control" <<EOF
Package: yellow-flash
Version: $version
Section: net
Priority: optional
Architecture: $arch
Installed-Size: $size
Depends: libgtk-3-0t64 | libgtk-3-0, libwebkit2gtk-4.1-0
Maintainer: Yellow Flash contributors <noreply@yellow-flash.invalid>
Homepage: https://github.com/Vaisah/yellow-flash
Description: Fast, resumable multi-connection download manager
 Yellow Flash downloads files over several connections at once, resumes
 them after a pause or a crash, and runs queues on a schedule. It takes
 downloads from the browser through its extension, downloads videos with
 yt-dlp, and comes with a command line (yellowflash-cli) and an MCP server
 for AI assistants (yellowflash-mcp).
EOF

cat > "$pkg/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
echo "Yellow Flash is installed. Browser extension (Load unpacked): /usr/lib/yellow-flash/browser-extension"
EOF
chmod 0755 "$pkg/DEBIAN/postinst"

# Whatever the umask: folders 0755, files 0644 or 0755, owned by root.
chmod 0755 "$pkg"
chmod -R go-w "$pkg"
chmod 0644 "$pkg/usr/share/applications/yellow-flash.desktop"

mkdir -p dist/linux
dpkg-deb --root-owner-group --build "$pkg" "$out" >/dev/null
echo "==> $out"
dpkg-deb --info "$out" | sed -n '/Package:/,/Description:/p'
