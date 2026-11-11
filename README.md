# Yellow Flash Download Manager

A free download manager for Windows and Linux: fast, resumable multi-connection downloads,
queues and schedules, browser capture, videos, torrents and debrid accounts. A Go engine with a
Wails + React desktop app, a command line and an MCP server for AI assistants.

**Version 0.2.0.** The engine and the desktop app are done and tested. Mister Download Manager's
backend has been ported to Go (see [HANDOFF.md](HANDOFF.md) §9). Some backend features don't
have a screen yet; see [What the window doesn't show yet](#what-the-window-doesnt-show-yet).

## Getting it

Releases are built by GitHub Actions when a release is published
([.github/workflows/release.yml](.github/workflows/release.yml)). Each one attaches:

- `yellow-flash-<version>-windows-amd64-setup.exe`: the Windows installer (recommended)
- `yellow-flash-<version>-windows-amd64.zip`: the same files without installing
- `yellow-flash-<version>-linux-amd64.tar.gz` (needs GTK 3 and WebKitGTK 4.1)
- `SHA256SUMS`

**The installer** needs no administrator rights: it installs for your user into
`%LOCALAPPDATA%\Programs\Yellow Flash` and brings everything the app uses: the app, the browser
helper (`yellowflash-nmhost`), `yellowflash-cli`, `yellowflash-mcp` and the browser extension. It
connects the helper to every installed browser, adds Start menu and desktop shortcuts, and its
last page starts Yellow Flash, which then offers to add the extension to each browser (see
[Browser integration](#browser-integration)). Updating closes a running Yellow Flash first.
Uninstalling removes all of it but keeps your download list and settings
(`%APPDATA%\Yellow Flash`). The installer isn't code-signed yet, so Windows SmartScreen warns
before it runs.

The zip and the tarball hold the same files; keep them together, since the app finds the browser
helper and the extension beside itself.

## Building from source

Requirements:

- Go 1.26 or later.
- Node 22 or later, and the Wails v2 CLI:
  `go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0`
- Windows: WebView2 (built into Windows 11).
- Linux: GTK 3 and WebKitGTK 4.1 (`libgtk-3-dev libwebkit2gtk-4.1-dev`), built with
  `-tags webkit2_41`.
- Docker, only to run the race detector on Windows without a C toolchain.

```sh
wails dev                         # the app with hot reload; also serves http://localhost:34115
                                  # so a browser can test the UI against the real backend
wails build                       # build/bin/Yellow Flash.exe
wails build -tags webkit2_41      # on Linux

go build -o build/bin/yellowflash-nmhost.exe ./cmd/nm-host   # the browser host, beside the app
go build ./cmd/yellowflash-cli ./cmd/yellowflash-mcp
```

The engine, the command line and the MCP server need no cgo. The UI alone runs in a browser with
a simulated backend: see [frontend/README.md](frontend/README.md).

## Where data lives

Downloads and settings are kept in `downloads.json` in the data folder:

- Windows: `%APPDATA%\Yellow Flash`
- Linux: `~/.config/Yellow Flash`
- Portable mode: a writable `data` folder next to the program is used instead.
- `YELLOWFLASH_DATA_DIR` overrides all of these, for a separate instance (tests, screenshots).

Passwords (site logins, debrid accounts) go in the system keychain (Windows Credential Manager,
or the freedesktop Secret Service on Linux), never in Yellow Flash's files. `Authorization`,
`Cookie` and proxy credentials are never written to the list, logs or events.

## The desktop app

- **Downloads.** Add URL previews the file first (name, size, type, whether it can resume) and
  takes a server login. Batch add, drag and drop of links, clipboard pre-fill. Pause, resume,
  retry, delete with or without the file, open the file or its folder.
- **The list.** Categories (music, video, archives, programs, documents, APKs, images) and
  finished/unfinished filters, search, sorting, a right-click menu, keyboard shortcuts.
- **Details.** Live speed and ETA, a segment map, and the server's country, distance and whether
  it is a CDN or proxy.
- **Scheduler and queues.** Create and delete queues; add, reorder and remove their downloads;
  set how many files run at once; start daily or once on a date, at a time and on chosen days;
  stop at a time; turn off the computer or exit when done. A data cap ("no more than N MB every
  H hours").
- **Speed limiter**, a disk meter for the download folder, and a disk cleaner that removes only
  Yellow Flash's own leftovers (unfinished temporary files of removed downloads, extraction
  staging folders).
- **Checksums.** MD5, SHA-1 or SHA-256 of a finished file, compared with a published one.
- **Videos.** Paste a video page's address to download it with yt-dlp, which Yellow Flash can
  install (checked against yt-dlp's signing key).
- **Archives.** Extract a finished zip, tar, gz or xz safely; 7z, RAR and encrypted zips go
  through an installed 7-Zip. A password is asked for when needed.
- **Options.** Launch at sign-in, the Download Complete window, browser capture and which file
  types it takes, the download folder, connections, and site logins.

### What the window doesn't show yet

The backend for these is done and tested; their screens are still to build:

- Torrents and magnet links (also through `yellowflash-cli add`)
- Debrid accounts (Real-Debrid, AllDebrid, Premiumize)
- Remote control from a phone (pair with `yellowflash-cli pair`)
- Crash reports
- Network settings (proxy: system, fixed or PAC; DNS) and extraction settings

## What the engine does

- **Segmented downloads.** A file is split into 1 to 32 byte ranges, written by a bounded pool of
  workers at their own offsets. A worker can't write outside its range.
- **Probing.** `HEAD`, falling back to `GET`. Range support counts only when `Range: bytes=0-0`
  returns `206` with a valid `Content-Range`, never on `Accept-Ranges` alone. Web pages are
  refused with an explanation instead of being saved as the file.
- **Servers and links.** HTTP, HTTPS, FTP and FTPS. Google Drive links lead to the file. Per-host
  connection limits are learned when a server refuses extra connections, and a throttled
  connection (429, 503) hands its range back to the others.
- **Pause, resume and crash recovery.** Progress is saved only for data already on disk. Resume
  checks `ETag` and `Last-Modified` and starts over if the file changed. After a crash, active
  downloads come back paused.
- **Retries** with exponential backoff and jitter; `Retry-After` is respected.
- **Integrity.** The final size and any digest the server advertises (`Repr-Digest`, `Digest`,
  `Content-MD5`, `Content-Digest`) are checked before the file is published. A finished file
  never overwrites another; a clash becomes `name (1).ext`.
- **Disk.** Free space is checked before a download, and files are allocated sparsely where the
  file system allows.
- **Network.** System, fixed or PAC proxies; the system DNS or DNS over HTTPS; a global speed
  limit and a data cap.
- **Security.** URLs and redirects are validated, optional SSRF protection is applied at each
  connection (covering redirects and DNS rebinding), filenames are sanitised for Windows and
  Linux, and header injection is blocked.

## Videos, galleries and torrents

- **Helpers.** yt-dlp (videos), ffmpeg (merging video and audio) and gallery-dl (image galleries)
  are installed on request and verified: yt-dlp and gallery-dl against their signing keys,
  ffmpeg against its checksum list. On Linux the system's ffmpeg is used.
- **Torrents** use `github.com/anacrolix/torrent`. Peers must never see this computer's address,
  so torrents run only through a SOCKS5 proxy, or through a debrid account that downloads the
  torrent for you.
- **Debrid accounts.** With your own Real-Debrid, AllDebrid or Premiumize account, file-host links
  are turned into direct links and downloaded like any file.

## Browser integration

The extension in [extension/](extension/) (Chrome, Edge, Brave, Opera, Firefox) hands downloads
to Yellow Flash through the native messaging host `yellowflash-nmhost`. The installer and every
start of the app register the host with the installed browsers; a portable copy doesn't.

**Connect Your Browsers.** On first start, and from Options → General → Connect browsers…, the
app lists the browsers installed and which already have the extension; a row turns green when
its extension connects. Browsers don't let a program add an extension by itself, so each
"Set up" opens the browser where the person confirms it:

- **Today** the extension isn't in any store yet. Chromium browsers (Chrome, Edge, Brave and
  the others) load it from the installed `browser-extension` folder: the app opens the browser's
  extensions page and copies the folder's path; the person turns on Developer mode and chooses
  Load unpacked. Firefox needs the add-on from addons.mozilla.org, so it shows "Coming soon".
- **Once it is listed**, put the store IDs in `internal/browser/extension.go` (`StoreID`,
  `OtherStoreIDs`, `FirefoxPage`). Then "Set up" opens the store page (one click on Add), and
  registering the host also writes Chrome's and Edge's external-extension entries, so those
  browsers offer the extension by themselves at their next start.

[docs/browser-help.md](docs/browser-help.md) explains each connection problem the extension can
report.

## Command line

Only one Yellow Flash runs per data folder. Launching it again hands that launch's command line to
the running app, in IDM's syntax:

```sh
"Yellow Flash.exe" /d https://example.com/file.iso                          # opens Add URL with the link
"Yellow Flash.exe" /d https://example.com/file.iso /p D:\dl /f a.iso /n    # adds and starts it, no dialog
"Yellow Flash.exe" /d https://example.com/file.iso /a                       # adds it without starting
"Yellow Flash.exe" /s                                                       # starts every waiting download
"Yellow Flash.exe" /exit                                                    # the running app saves and quits
```

Flags are case-insensitive and may also start with `-`.

`yellowflash-cli` downloads without the window, and controls the running app through its
automation API (off until `api on`):

```sh
yellowflash-cli get https://example.com/file.iso -o ./downloads -c 8
yellowflash-cli resume <id>          # after Ctrl+C or a failure
yellowflash-cli api on               # once: let programs control the running app
yellowflash-cli status               # the app's downloads, as JSON
yellowflash-cli add <url or magnet>  # add to the running app
yellowflash-cli pair                 # pair a phone with the remote control (shows a QR code)
yellowflash-cli help                 # every command: videos, playlists, galleries, torrents…
```

Ctrl+C pauses and saves progress; a finished download prints its path and SHA-256. `--json`
prints one JSON object per line.

`yellowflash-mcp` is an MCP server for AI assistants (Claude, Cursor, VS Code). The assistant
starts it, and it lists, adds, pauses, resumes and removes downloads through the automation API.

## Tests

```sh
gofmt -l .
go vet ./...
go test ./...                    # unit and integration tests (httptest and FTP test servers)
cd frontend && npx tsc -b        # type-check the UI

# The race detector needs cgo; on Windows without gcc, run it on Linux in Docker (Git Bash):
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src:ro" -v idm-gomod:/go/pkg/mod -w /src \
  -e CGO_ENABLED=1 golang:1.26 go test -race ./internal/... ./tests/... ./cmd/...
```

The root `main` package is left out of the Docker run because it needs GTK on Linux.
`tests/integration` runs the service, engine and JSON store together against an `httptest` server
that can withhold `HEAD` or range support, send bad `Content-Range` headers, answer 404, 416, 429
or 503, reset connections, stall mid-body, redirect, change the `ETag` and simulate a crash.

## Architecture

```
main.go, *_app.go, commands.go   Wails shell: the App bindings the window calls (no business logic)
frontend/                        React + TypeScript UI (see frontend/README.md)
cmd/
  nm-host                        browser native messaging host (built as yellowflash-nmhost)
  yellowflash-cli                command line, and the automation API's client
  yellowflash-mcp                MCP server for AI assistants
internal/
  domain/download                Download and Segment model, the state machine, errors, ports
  domain/queue                   queues and their schedules
  application/download           use cases over the engine
  application/events             events shared by the engine and the UI (leaf package)
  application/queues             runs queues and schedules
  application/datacap            the data cap
  engine                         downloads: planning, workers, retries, progress, resume, verify,
                                 per-host limits, speed limiter, disk checks
  infrastructure/network         hardened HTTP and FTP clients, probing, proxies and PAC, DNS
  infrastructure/network/sites   sites whose links aren't the file (Google Drive)
  infrastructure/filesystem      filenames, temp files, no-overwrite publishing
  infrastructure/persistence     jsonstore (downloads.json) and an in-memory store
  ipc                            single instance, the data folder, the command channel
  platform                       OS services: keychain, disk, autostart, open/reveal, locks
  browser                        capture rules and browser registration
  tools                          yt-dlp, ffmpeg and gallery-dl: install, verify, run
  torrent                        torrents and magnets
  debrid                         debrid accounts
  remote                         remote control over HTTPS on the local network
  extract                        safe archive extraction
  geo                            a server's country (DB-IP's offline table) and flags
  crash                          the rolling log and crash reports
extension/                       the browser extension
tests/integration                end-to-end scenarios
```

Dependencies point inward. The engine imports only domain types, the leaf `events` and `platform`
packages, and the network and filesystem helpers; never the store, Wails or OS APIs.

### Durability rules

1. Workers write with `WriteAt`, then add to an atomic counter.
2. A checkpoint reads the counters, `fsync`s the `.part` file, then saves those counts (every 2 s,
   when a segment completes, on pause and on shutdown).
3. Saved progress therefore never exceeds what is on disk; on recovery it is also clamped to the
   temporary file's length.
4. A download is `COMPLETED` only after verification and publishing, which never overwrites an
   existing file.

### State machine

```
QUEUED → INITIALIZING → DOWNLOADING → VERIFYING → COMPLETED
              │  ↘           │   ↘          ↘
              │   PAUSING ←──┘    FAILED ←──── (any active state)
              ↓      ↓              ↓
          CANCELLED  PAUSED → INITIALIZING (resume / retry)
```

The exact transition table is in `internal/domain/download/status.go`; every legal and illegal
pair is tested.

## License

Yellow Flash is free software under the GNU General Public License v3.0 or later
([LICENSE](LICENSE)). Much of its backend is ported from
[Mister Download Manager](https://github.com/anefzaoui/misterdownloadmanager) © 2026 Ahmed
Nefzaoui and contributors (GPL-3.0-or-later). [NOTICE.md](NOTICE.md) lists the credits and
third-party licences. IP Geolocation by DB-IP.
