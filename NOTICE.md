# Notice

Yellow Flash Download Manager
Copyright © 2026 the Yellow Flash contributors.

Yellow Flash is free software: you can redistribute it and/or modify it under the terms of the
GNU General Public License as published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version. It is distributed WITHOUT ANY WARRANTY; see
[LICENSE](LICENSE) for the full terms.

## Based on Mister Download Manager

Parts of Yellow Flash's backend are ported to Go from **Mister Download Manager**
(<https://github.com/anefzaoui/misterdownloadmanager>), Copyright © 2026 Ahmed Nefzaoui and
contributors, licensed under GPL-3.0-or-later.

Every ported Go file starts with a header naming the Mister Download Manager source it comes
from and stating that it was translated from Rust to Go and modified. Code written before the
port began (the Phase 1 engine, the SQLite store, the Wails app and the React interface), and
code designed only after studying Mister Download Manager's behaviour (the dynamic-splitting
planner, the first-request download), is original to Yellow Flash.

The browser extension (`extension/`), its tests (`tools/extension/test.mjs`) and the browser help
page (`docs/browser-help.md`) are Mister Download Manager's, modified for Yellow Flash: its name,
host name, extension key and IDs, version and help links are changed, its sponsor link is removed,
and the parts that read MDM's Rust sources read the Go ports instead. Each JavaScript file starts
with a header saying so. The extension's icons are made from Yellow Flash's own app icon
(`build/appicon.png`), not Mister Download Manager's.

The remote control's page (`internal/remote/web/`: `index.html`, `app.js`, `app.css`,
`manifest.webmanifest`) is Mister Download Manager's, modified for Yellow Flash: its name and the
key its token is kept under are changed. Each file starts with a header saying so. Its icon
(`icon.png`) is Yellow Flash's own app icon, scaled down.

## Go dependencies added for the port

| Module | Licence | Used for | Since |
|---|---|---|---|
| `github.com/dop251/goja` | MIT | JavaScript engine for proxy auto-config scripts (`network/pac.go`); pinned to commit a8d1dbc7 (2026-10-25) | port step 2b (`pac`) |
| `github.com/dlclark/regexp2/v2` | MIT | Required by goja | port step 2b (`pac`) |
| `github.com/go-sourcemap/sourcemap` | BSD-2-Clause | Required by goja | port step 2b (`pac`) |
| `github.com/google/pprof` | Apache-2.0 | Required by goja | port step 2b (`pac`) |
| `golang.org/x/net` | BSD-3-Clause | `dns/dnsmessage`, in the DNS tests' fake servers only; it was already an indirect dependency via Wails | port step 2b (`network/dns.go`) |
| `golang.org/x/sys` | BSD-3-Clause | Windows file lock and foreground permission (`internal/platform/lock_windows.go`); it was already an indirect dependency via Wails | port step 1 (`internal/ipc`) |
| `github.com/anacrolix/torrent` v1.61.0 | MPL-2.0 | BitTorrent and magnet downloads (`internal/torrent/session.go`), in place of MDM's librqbit; used unmodified (its file storage is replaced by `internal/torrent/files.go`, Yellow Flash's own) | port step 5b (`torrent`) |
| `golang.org/x/time` | BSD-3-Clause | `rate`, the torrent session's speed limits | port step 5b (`torrent`) |
| Required by anacrolix/torrent, MPL-2.0 | MPL-2.0 | `anacrolix/dht/v2`, `generics`, `log`, `multiless`, `sync`, `upnp`, `utp` | port step 5b (`torrent`) |
| Required by anacrolix/torrent, MIT | MIT | `anacrolix/chansync`, `envpprof`, `missinggo` (v1, v2, perf), `stm`; `alecthomas/atomic`, `benbjohnson/immutable`, `cespare/xxhash`, `dustin/go-humanize`, `huandu/xstrings`, `klauspost/cpuid/v2`, `mr-tron/base58`, `multiformats/go-multihash`, `multiformats/go-varint`, `pion/*` (datachannel, dtls, ice, interceptor, logging, mdns, randutil, rtcp, rtp, sctp, sdp, srtp, stun, transport, turn, webrtc), `protolambda/ctxlock`, `rs/dnscache`, `tidwall/btree`, `go.etcd.io/bbolt`, `lukechampine.com/blake3` | port step 5b (`torrent`) |
| Required by anacrolix/torrent, Apache-2.0 | Apache-2.0 | `anacrolix/btree`, `go-logr/logr`, `go-logr/stdr`, `google/btree`, `go.opentelemetry.io/auto/sdk`; `RoaringBitmap/roaring` and `go.opentelemetry.io/otel` (otel, metric, trace) are Apache-2.0 with parts under BSD-3-Clause | port step 5b (`torrent`) |
| Required by anacrolix/torrent, BSD | BSD-3-Clause, BSD-2-Clause | BSD-3-Clause: `bahlo/generic-list-go`, `bradfitz/iter`, `edsrzf/mmap-go`, `google/go-cmp`, `google/uuid`, `spaolacci/murmur3`, `wlynxg/anet`, `golang.org/x/crypto`, `x/exp`, `x/sync`, `x/text`; BSD-2-Clause: `gorilla/websocket`, `pkg/errors` | port step 5b (`torrent`) |
| Required by anacrolix/torrent, ISC | ISC | `davecgh/go-spew` | port step 5b (`torrent`) |

## Third-party data and assets

- `internal/infrastructure/network/sites/testdata/drive-*.html`: Google Drive page samples (test
  data) from Mister Download Manager's `crates/engine/tests/data`, GPL-3.0-or-later.
- `internal/tools/keys/yt-dlp.asc` and `internal/tools/keys/gallery-dl.asc`: the public OpenPGP
  keys yt-dlp's and gallery-dl's releases are signed with (fingerprint
  AC0CBBE6848D6A873464AF4E57CF65933B5A7581 for yt-dlp), as Mister Download Manager pins them
  in `crates/tools/keys`. Public keys, published by those projects to check their releases.
- `internal/tools/testdata/`: real signed checksum lists of yt-dlp and gallery-dl releases, and
  Mister Download Manager's release key with one of its signed lists (used only to test the
  Ed25519 signature check; Yellow Flash never trusts that key), from Mister Download Manager's
  `crates/tools/tests/fixtures` and `crates/tools/keys`.

Assets ported from Mister Download Manager keep their own licences, as listed in its
`ATTRIBUTION.md`. When one of them is added to this repository, list it here with its author,
licence and path, for example the DB-IP IP-to-Country Lite data (CC BY 4.0, which requires the
attribution "IP Geolocation by DB-IP").

The Yellow Flash logo (`frontend/src/assets/Yellow Flash Download Manager.png`) depicts a
character from a third-party franchise and is not covered by this project's licence. Replace it
before any public release.
