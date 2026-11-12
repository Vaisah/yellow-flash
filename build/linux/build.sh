#!/usr/bin/env bash
# Builds Yellow Flash for Linux into build/bin: the app, the browser helper,
# the command line and the MCP server. Used by install.sh and make-deb.sh.
#
# Needs Go, a C compiler, pkg-config and the GTK 3 / WebKitGTK 4.1
# development files (libgtk-3-dev libwebkit2gtk-4.1-dev). With the Wails CLI
# on the PATH the frontend is rebuilt too; without it, the committed
# frontend/dist is embedded as it is.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

need() { command -v "$1" >/dev/null || { echo "build.sh: $1 is needed: $2" >&2; exit 1; }; }
need go "install Go 1.26 or later"
need pkg-config "sudo apt install pkg-config"
need cc "sudo apt install build-essential"
for lib in gtk+-3.0 webkit2gtk-4.1; do
	pkg-config --exists "$lib" ||
		{ echo "build.sh: $lib is missing: sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev" >&2; exit 1; }
done

mkdir -p build/bin
flags=(-trimpath -ldflags "-s -w")

wails_bin="$(command -v wails || true)"
[ -z "$wails_bin" ] && [ -x "$(go env GOPATH)/bin/wails" ] && wails_bin="$(go env GOPATH)/bin/wails"
if [ -n "$wails_bin" ]; then
	echo "==> the app (wails build)"
	"$wails_bin" build -trimpath -tags webkit2_41 -ldflags "-s -w"
else
	echo "==> the app (go build, with the committed frontend/dist; install the Wails CLI to rebuild the UI)"
	CGO_ENABLED=1 go build "${flags[@]}" -tags desktop,production,webkit2_41 -o "build/bin/Yellow Flash" .
fi

echo "==> the browser helper, the command line and the MCP server"
CGO_ENABLED=0 go build "${flags[@]}" -o build/bin/yellowflash-nmhost ./cmd/nm-host
CGO_ENABLED=0 go build "${flags[@]}" -o build/bin/yellowflash-cli ./cmd/yellowflash-cli
CGO_ENABLED=0 go build "${flags[@]}" -o build/bin/yellowflash-mcp ./cmd/yellowflash-mcp

echo "==> built into build/bin"
