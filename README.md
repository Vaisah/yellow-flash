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
- **Progress.** Speed is an exponential moving average. The engine reports average speed, ETA (only when it is known), and active connections, at most every 250 ms.
- **Security.**
  - URLs and redirect targets are validated.
  - Optional SSRF protection is checked when each connection is made, so it also covers every redirect and DNS rebinding.
  - Filenames are sanitized for both Windows and Linux.
  - Header injection is blocked.
  - Credentials (`Authorization`, `Cookie`) are never written to the database, logs, or events.

## Requirements

- Go 1.26 or later. No cgo is needed: SQLite comes from the pure-Go `modernc.org/sqlite`.
- Docker, only to run the race detector on Windows hosts without a C toolchain.
- For the desktop shell only: Node 22, the Wails v2 CLI, and on Linux GTK 3 and WebKitGTK 4.1.

## Using the engine from the command line

```sh
go run ./cmd/engine-test -dir ./downloads -c 8 https://example.com/file.iso

# Keep state in SQLite so you can resume after Ctrl+C or a crash:
go run ./cmd/engine-test -db state.db -dir ./downloads https://example.com/file.iso
go run ./cmd/engine-test -db state.db -list
go run ./cmd/engine-test -db state.db -resume <id>
```

Pressing Ctrl+C pauses the download and saves progress. When a download completes, the command prints the file's path and SHA-256. Other flags: `-name`, `-retries`, `-header "Name: value"` (repeatable), `-allow-private=false` (blocks loopback and private-network destinations), and `-v` (debug logging).

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

The integration suite in `tests/integration` runs the service, engine, and SQLite together against a configurable `httptest` server. That server can withhold `HEAD` or range support, send bad `Content-Range` headers, answer with 404, 416, 429, or 503, send real TCP resets, stall mid-body, redirect, change the `ETag`, and simulate a process crash. Payloads are deterministic and checked by SHA-256.

## Architecture

```
cmd/engine-test            CLI entry point (no UI)
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
  infrastructure/persistence/sqlite   migrations + repositories (domain.Store)
  infrastructure/persistence/memory   non-durable domain.Store
  platform                 SecretStore interface (OS keychains arrive in Phase 6)
tests/integration          end-to-end scenarios
```

Dependencies point inward. The engine imports only domain types, the leaf `events` and `platform` packages, and the network and filesystem helpers. It never imports SQLite, Wails, or any OS-specific API. Persistence is injected as `domain.Store`.

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
