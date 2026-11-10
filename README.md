# Downwell — Download Manager

A cross-platform download manager for Windows and Linux. The Go download engine runs and is tested on its own. A Wails + React desktop shell exists, but it isn't part of the current phase. Browser integration comes in a later phase.

**Status:** Phase 1 (the engine) and Phase 2 (the Yellow Flash desktop app) are complete and verified. See [DEVELOPMENT_PROGRESS.md](DEVELOPMENT_PROGRESS.md) for test results and the remaining work.

## Running the desktop app

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0   # once
wails dev        # hot-reload; also serves http://localhost:34115 for browser testing with the real backend
wails build      # produces build/bin/Yellow Flash.exe (icon from build/appicon.png)
```

`cd frontend && npm run dev` runs the UI alone in a browser, using a simulated backend. That backend is dev-only and never bundled.

## What the engine does

- **Segmented downloads.** The file is split into 1 to 32 inclusive byte ranges. A bounded pool of workers writes each range at its own offset with `WriteAt`, and a worker can't write outside its range.
- **Metadata probing.** The engine sends a `HEAD` request and falls back to `GET` if that fails. It claims range support only after a `Range: bytes=0-0` request returns `206` with a valid `Content-Range`. It never trusts `Accept-Ranges` alone.
- **Single-stream fallback.** Servers without range support, and resources of unknown length, download over one connection.
- **Pause and resume.** On pause the engine stops the workers, syncs the file, and saves progress. On resume it checks the `ETag` and `Last-Modified` again and starts over if the file on the server changed.
- **Crash recovery.** Downloads left in an active state become `PAUSED`. Saved progress is checked against the temporary file.
- **Retries.** Delays grow exponentially with random jitter, and the server's `Retry-After` is respected for 429 and 503. Timeouts, connection resets, other network errors, and HTTP statuses are classified separately so the engine knows which to retry.
- **Integrity.** Before publishing, the engine checks the final size and any digest the server advertises (`Repr-Digest`, `Digest`, `Content-MD5`, `Content-Digest`). The finished file never overwrites an existing one; a name clash is published as `name (1).ext`.
- **Debrid accounts.** With your own Real-Debrid, AllDebrid or Premiumize account, file-host links come through the service's direct links, and torrents can go to the account, so no peer ever connects to this computer. Sign-ins are kept in the system keychain, never in Yellow Flash's files.
- **Progress.** Speed is an exponential moving average. The engine reports average speed, ETA (only when it is known), and active connections, at most every 250 ms.
- **Security.**
  - URLs and redirect targets are validated.
  - Optional SSRF protection is checked when each connection is made, so it also covers every redirect and DNS rebinding.
  - Filenames are sanitized for both Windows and Linux.
  - Header injection is blocked.
  - Credentials (`Authorization`, `Cookie`) are never written to the database, logs, or events.

## Requirements

- Go 1.26 or later. No cgo is needed.
- Docker, only to run the race detector on Windows hosts without a C toolchain.
- For the desktop shell only: Node 22, the Wails v2 CLI, and on Linux GTK 3 and WebKitGTK 4.1.

## Desktop app command line

Only one Yellow Flash runs per data folder. Launching it again hands that launch's command line to the running app (or just brings its window to the front), using IDM's syntax:

```sh
"Yellow Flash.exe" /d https://example.com/file.iso            # opens Add URL with the link
"Yellow Flash.exe" /d https://example.com/file.iso /p D:\dl /f a.iso /n   # adds and starts it, no dialog
"Yellow Flash.exe" /d https://example.com/file.iso /a         # adds it without starting
"Yellow Flash.exe" /s                                          # starts every waiting download
"Yellow Flash.exe" /exit                                       # the running app saves and quits
```

Flags are case-insensitive and may also start with `-`. Set `YELLOWFLASH_DATA_DIR` to run a separate instance with its own list, for example in tests. A writable `data` folder next to the program turns on portable mode.

## The command line

`yellowflash-cli` downloads without the window, and asks the running app through its automation API:

```sh
go run ./cmd/yellowflash-cli get https://example.com/file.iso -o ./downloads -c 8
go run ./cmd/yellowflash-cli list                  # its own downloads (kept in <data>/cli)
go run ./cmd/yellowflash-cli resume <id>           # after Ctrl+C or a failure
go run ./cmd/yellowflash-cli api on                # once: let programs ask the running app
go run ./cmd/yellowflash-cli status                # the app's downloads, as JSON
go run ./cmd/yellowflash-cli help                  # every command: videos, galleries, torrents, pairing…
```

Ctrl+C pauses a download and saves progress; a finished one prints its path and SHA-256. `--json` prints one JSON object per line for programs. Torrents need `--proxy socks5://…`: peers never see this computer's address.

`yellowflash-mcp` is an MCP server for AI assistants (Claude, Cursor, VS Code): the assistant starts it and, through the app's automation API, lists, adds, pauses, resumes and removes downloads.

## Tests

```sh
gofmt -l .
go vet ./...
go test ./...                       # unit + integration (httptest servers)

# Race detector. Needs cgo, so on Windows without gcc run it on Linux in Docker:
docker run --rm -v "$PWD:/src:ro" -v idm-gomod:/go/pkg/mod -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./internal/... ./tests/... ./cmd/...

# Cross-compile the engine and the CLI:
GOOS=linux   go build ./internal/... ./cmd/...
GOOS=windows go build ./internal/... ./cmd/...
```

The integration suite in `tests/integration` runs the service, engine, and JSON store together against a configurable `httptest` server. That server can withhold `HEAD` or range support, send bad `Content-Range` headers, answer with 404, 416, 429, or 503, send real TCP resets, stall mid-body, redirect, change the `ETag`, and simulate a process crash. Payloads are deterministic and checked by SHA-256.

## Architecture

```
cmd/yellowflash-cli        command line (no UI) and the app's automation API
cmd/yellowflash-mcp        MCP server for AI assistants
app.go, main.go            Wails desktop shell (Phase 2)
internal/
  domain/download          Download/Segment model, state machine, errors, repository ports
  domain/queue             Queue model
  application/download     DownloadService: use-case orchestration over a DownloadPort
  application/events       Event + Publisher (leaf package shared by engine and delivery)
  engine                   Manager (lifecycle, single state authority), executor, workers,
                           allocator, segment planning, retry, progress, resume, verifier
  infrastructure/network   hardened HTTP client (SSRF dial guard), prober, Content-Range parsing
  infrastructure/filesystem filename sanitising, temp files, no-overwrite publishing
  infrastructure/persistence/jsonstore downloads.json + resume/<id>.json (domain.Store)
  infrastructure/persistence/memory   non-durable domain.Store
  platform                 SecretStore, and the system keychain (Windows Credential Manager, Secret Service)
  debrid                   debrid service accounts: unlocking links, sign-in, torrents through the account
tests/integration          end-to-end scenarios
```

Dependencies point inward. The engine imports only domain types, the leaf `events` and `platform` packages, and the network and filesystem helpers. It never imports the store, Wails, or any OS-specific API. Persistence is injected as `domain.Store`.

### Durability rules

1. Workers write bytes with `WriteAt` and then increment an atomic counter.
2. A checkpoint reads the counters, runs `fsync` on the `.part` file, and then saves those counts. Checkpoints happen every 2 s, when a segment completes, on pause, and on shutdown.
3. Saved progress therefore never exceeds data that is actually on disk. On recovery, progress is also clamped to the length of the temporary file.
4. A download becomes `COMPLETED` only after verification succeeds and the file has been published. Publishing uses a hard link with an exclusive-create-and-rename fallback, so it never overwrites an existing file.

### State machine

```
QUEUED → INITIALIZING → DOWNLOADING → VERIFYING → COMPLETED
              │  ↘           │   ↘          ↘
              │   PAUSING ←──┘    FAILED ←──── (any active state)
              ↓      ↓              ↓
          CANCELLED  PAUSED → INITIALIZING (resume / retry)
```

The exact transition table is in `internal/domain/download/status.go`, and every legal and illegal pair has a test.

## Desktop shell and packaging

The Wails shell (`main.go`, `app.go`, `frontend/`) compiles against the engine but is Phase 2 work. The Debian package in `dist/linux` comes from an earlier scaffold and has not been rebuilt. Don't treat it as a release.

```sh
docker build -f build/linux/Dockerfile -t downwell-linux:0.1.0 .   # builds /out/downwell_0.1.0_amd64.deb
```

## License

Yellow Flash is free software, licensed under the GNU General Public License v3.0 or later
([LICENSE](LICENSE)). Parts of its backend are being ported from
[Mister Download Manager](https://github.com/anefzaoui/misterdownloadmanager) © 2026 Ahmed
Nefzaoui and contributors (GPL-3.0-or-later); see [NOTICE.md](NOTICE.md) for credits and
third-party licences.
