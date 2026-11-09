# Yellow Flash: developer handoff

Last updated: 2026-11-06. Read this first, then `README.md` (setup and architecture summary) and `DEVELOPMENT_PROGRESS.md` (detailed test evidence per milestone).

## 1. Where the project stands

The goal is a cross-platform (Windows and Linux) download manager that competes with Internet Download Manager. It has a Go engine, a Wails v2 desktop shell, a React + TypeScript UI, SQLite storage, and (later) browser extensions.

| Phase | Scope | Status |
|---|---|---|
| 1 | Core Go download engine: segmented, resumable, crash-safe, tested | **Done** |
| 2 | Desktop app ("Yellow Flash" UI on Wails) | **Done** |
| 3 | Download management: queues, limits, scheduling, throttling, notifications | Next |
| 4 | Browser integration: Chrome, Edge and Firefox extensions + native messaging | Planned |
| 5 | Advanced: auth, cookies, proxies, checksums, priorities, themes | Planned |
| 6 | Packaging: installers, OS keychains, notifications, signing | Planned |

> **Uncommitted work.** Only `0db8c1c` (end of Phase 1) is committed. All of Phase 2 and the engine fixes listed in §4 are uncommitted in the working tree. Review `git status` and commit them first. See §7 for what is generated and whether to commit it.

## 2. Getting started

**Prerequisites**
- Go 1.26 or later. No cgo is needed: SQLite is the pure-Go `modernc.org/sqlite`.
- Node 22 or later.
- Wails CLI v2.15.0: `go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0`.
- Windows: WebView2 (built into Windows 11).
- Linux: GTK3 and WebKitGTK 4.1, built with `-tags webkit2_41`.
- Docker, only to run the race detector on Windows (see below).

**Everyday commands** (run from the repo root):

```sh
go build ./... && go vet ./... && go test ./...     # all Go tests, ~20 s
wails dev        # app with hot reload; also serves http://localhost:34115 for browser testing with the REAL backend
wails build      # → build/bin/Yellow Flash.exe (icon from build/appicon.png)
cd frontend && npm run dev     # UI only, in a browser, with a simulated backend (src/dev/mockBackend.ts)
cd frontend && npx tsc -b      # type-check the UI
go run ./cmd/engine-test -db state.db -dir out -c 8 <url>   # engine without any UI (-list, -resume <id>, -v)
```

**Race detector.** It needs cgo, and this Windows machine has no gcc, so run it on Linux in Docker (from Git Bash):

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "C:/Users/Vaisah/Videos/Go/idm-clone:/src:ro" \
  -v idm-gomod:/go/pkg/mod -v idm-gocache:/root/.cache/go-build -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./internal/... ./tests/... ./cmd/...
```

Leave the root `main` package out of that run; it needs GTK on Linux. `MSYS_NO_PATHCONV=1` stops Git Bash from rewriting `/src`.

**Where data lives.** The database is `%APPDATA%\Yellow Flash\downloads.db` (on Linux, `~/.config/Yellow Flash/`). An old `Download Manager` data folder is migrated automatically on first start.

## 3. Architecture in five minutes

```
cmd/engine-test/            CLI that drives the engine without UI
main.go, app.go             Wails shell; App = thin binding layer (no business logic)
frontend/src/               React UI (components/, hooks/useDownloads.ts, lib/, api.ts)
internal/
  domain/download           Download, Segment, Status state machine, errors, repository ports
  domain/queue              Queue model (persisted only; no scheduling yet)
  application/download      Service: use cases over the DownloadPort interface
  application/events        Event + Publisher (leaf package)
  engine/                   Manager (lifecycle), executor (probe→transfer→verify→publish),
                            worker (segment pool + single stream), retry, progress, resume …
  infrastructure/network    hardened HTTP client (SSRF dial guard), Prober, Content-Range, UnwrapLink
  infrastructure/filesystem filename sanitising, .part files, no-overwrite Publisher
  infrastructure/persistence/sqlite   migrations + repositories (implements domain.Store)
  infrastructure/persistence/memory   in-memory domain.Store (tests, CLI without -db)
  platform/                 SecretStore, OpenPath/RevealPath (OS-specific files are build-tagged)
tests/integration/          end-to-end tests against a configurable httptest server
```

**Rules that keep the engine correct.** Don't break these.

1. **One state authority.** Only `engine.Manager` changes `Download.Status`, always through `domain.Download.Transition` while holding `Manager.mu`. Workers report bytes only through atomics (`segmentState.done`, `tracker`).
2. **Durability ordering.** Read the byte counters, then `fsync` the `.part` file, then persist those counters. Saved progress must never exceed data that is on disk. See `checkpoint` in `worker.go`.
3. **Representation identity.** Partial data is reused only when size and ETag/Last-Modified match. Otherwise the engine restarts from zero (`representationChanged`, `If-Range`).
4. **Never overwrite.** `filesystem.Publisher` publishes as `name (n).ext` on a clash. The temp file is `name.part` with mode 0600.
5. **Dependencies point inward.** The engine imports domain, `events`, `platform`, `network` and `filesystem`, but never SQLite, Wails or OS APIs. OS-specific code goes in build-tagged files under `infrastructure/network` or `platform`.
6. **Credentials are never persisted or logged.** `Authorization`, `Cookie` and `Proxy-Authorization` live only in `platform.SecretStore`, which is currently in memory.

**Data flow of one download.**
1. `App.AddDownload` → `Service.Add` → `Manager.Create`. This validates and unwraps the URL, sanitises the filename, reserves a name, and saves the record as QUEUED.
2. `Manager.Start` → `run` → `execute`.
3. `execute` calls `probe` (HEAD, then GET `bytes=0-0`) and `applyMetadata` (detects a changed file, rejects web pages, infers the extension).
4. Then `transferSegmented` or `transferStream` runs, followed by `complete` (verify, publish).
5. `finish` sets the final status, persists it, and emits an event.
6. Events reach the UI as `downloads:updated` and `downloads:progress` (throttled to 250 ms).

## 4. What was built

### Phase 1: engine (done)
- Domain model with int64 offsets, and an explicit state machine (all 81 transitions are tested).
- Probing: HEAD with a GET fallback. Range support counts only when `bytes=0-0` returns 206 with a valid Content-Range. Reads Content-Length, Type, Disposition, ETag, Last-Modified and digest headers.
- Overflow-safe allocator. Bounded worker pool (errgroup). Writer that cannot write outside its segment. Preallocated `.part` file.
- Single-stream fallback; unknown-length bodies; resume only when range support is proven.
- Pause, resume, cancel and retry. Crash recovery: active downloads become PAUSED, and progress is clamped to the length of the temp file.
- Retry policy: exponential backoff with jitter, Retry-After honoured. Errors are classified as timeout, reset, network, HTTP or permanent. Idle-read timeout.
- Progress: EWMA speed, average speed, ETA only when known, active connections.
- SQLite: pragmas in the DSN (FK, WAL, busy timeout); transactional versioned migrations (v1→v2); repositories for downloads, segments, headers, settings and queues.
- Security: URL validation, SSRF guard at dial time (covers redirects and DNS rebinding), filename sanitising, header-injection guard.

### Phase 2: desktop app (done)
- **UI**: toolbar; status and category sidebar; sortable table; details panel with a live segment map; status bar.
- **Dialogs**: Add URL with a link preview (name, size, type, resume support), Settings, and Remove (optionally deleting the file).
- **Interaction**: clipboard pre-fill, drag-and-drop of links, keyboard shortcuts, right-click menu, plain-language error explanations (`lib/errors.ts`).
- **Backend additions**:
  - `Manager.Inspect`, `network.UnwrapLink` (Google, Facebook and YouTube redirects).
  - `Service.ResumeAll/PauseAll`.
  - Settings stored in SQLite.
  - `platform.OpenPath/RevealPath`.
- **Engine fixes found against real servers:**
  - **Bogus HEAD length.** The GET length now overrides a HEAD `Content-Length: 0`, and `text/html` responses fail with `ErrWebPage`. YouTube watch pages used to "complete" as empty files.
  - **Adaptive concurrency.** A worker throttled with 429 or 503 hands its segment back and exits while other workers are alive, so 8 connections settle at what the server allows. Before this, Hetzner's 2-connection limit failed the whole download.
  - **Extension inference.** A file gets an extension from its Content-Type when it has none (`watch` + `video/mp4` → `watch.mp4`).

**Test suite**: 77 Go test functions. The integration suite in `tests/integration` covers the 20 scenarios from the original spec, plus redirects, auth headers, SSRF, connection limits, stalled connections, auto-rename and goroutine leaks. It runs clean under `-race` on Linux.

## 5. Next phases: concrete plan

Each item names where it plugs in. Follow the existing pattern: domain types first, then engine or service, then an integration test in `tests/integration` with a `fileServer` knob (see `server_test.go`), then UI.

### Phase 3: download management (in progress)

**Done (2026-11-09).** These features were designed after studying Mister Download Manager (GPL-3.0) as a reference only; none of its code was copied:
- **No probe round trips for new downloads.** `network.Prober.Open` sends `Range: bytes=0-`. The answer gives size and range support, and its body is the file, so data flows from the first request. Small files finish on one request. Resumed downloads still probe first, so their validators are checked.
- **Dynamic splitting (work stealing).** The planner is in `engine/planner.go`. A free connection takes an unassigned part, or else the upper half of the largest unclaimed remainder. Claim/commit stops a split from cutting into bytes being written. It is tested with a 200-seed random simulation.
- **Connection reuse.** A finished part reads to end-of-stream so the connection returns to the pool.
- **Learned per-server connection limits.** These are kept in memory in `Manager.hostLimits`; persisting them is still to do.
- **Immediate first retry** for network blips.
- **Global speed limiter**: `Manager.SetSpeedLimit` (`golang.org/x/time/rate`). It is not yet exposed in the UI.
- **Measured:** a real 100 MB download from a connection-limiting server took 21 s with 0 retries. Before, it failed with 429s and the retried run took minutes. A 62 KB file over a slow-TLS CDN took 1.5 s; `curl` took 2.6 s for the same file.
- **Licence:** as of 2026-11-09 Yellow Flash is **GPL-3.0-or-later** (see `LICENSE`, `NOTICE.md`), so code may now be ported from Mister Download Manager. Follow the porting rules in §9.

**Still to do in Phase 3, in order:**

1. **Single-instance lock (urgent bug risk).** Two app instances on the same DB corrupt each other: each one's `Recover` pauses the other's live downloads. Set `options.App.SingleInstanceLock` in `main.go`, and focus the existing window on a second launch.
2. **Maximum simultaneous downloads and a queue runner.** Add `internal/application/queue` (or extend `Service`). It holds QUEUED downloads and calls `Manager.Start` while `active < limit` (a setting, default 3), and listens to `DownloadChanged` events to start the next one.
   - Use the existing `queues` table and the `downloads.queue_id` column (migration v2).
   - Keep the engine unaware of queues.
   - Test: 5 adds with limit 2 never exceed 2 active, and all 5 complete.
3. **Remember per-host connection limits across restarts.** Learning is already done in memory (`Manager.learnHostLimit`); what's missing is persisting it. When adaptive concurrency drops connections, record the stable count for the host in a new table, `host_limits(host, max_connections, updated_at)` (migration v3). Start future downloads from that host with it.
   - Hook: the `errYielded` branch in `transferSegmented` (`engine/worker.go`).
   - Expose it to the engine through a small port, so the engine doesn't import SQLite.
4. **Dynamic segment splitting: done** (see above). Possible follow-up: drop a connection that is far slower than the others and re-split its remainder.
5. **Bandwidth limiting: engine done** (`Manager.SetSpeedLimit`). Still to do: a Settings or toolbar control, persisting the limit, and per-download limits.
6. **Scheduler.** Start or stop a queue at set times. Live this in the queue runner; persist schedules in `queues` or a new `schedules` table.
7. **Duplicate detection.** In `Service.Create`, find existing downloads with the same `URL`/`FinalURL` and ask the user (UI: "Already downloaded — open / download again").
8. **Automatic retry of failed downloads.** A delayed re-`Retry` for transient failure kinds only. Store a structured error kind; see the tech debt list.
9. **Download-complete notifications.** Add a `platform.Notifier` interface. Windows: toast (`git.sr.ht/~jackmordaunt/go-toast` is already an indirect dependency via Wails). Linux: D-Bus `org.freedesktop.Notifications`.
10. **Categories with target folders.** IDM-style "Video → ~/Videos". The UI already classifies by extension (`frontend/src/lib/categories.ts`). Move that classification to Go (`filesystem` or domain) so it can choose the default directory at create time.

### Phase 4: browser integration

- **Native messaging host.** A separate binary, `cmd/nm-host`, launched by the browser over stdio. It must talk to the *running* app through local IPC: a named pipe on Windows, a Unix socket in `$XDG_RUNTIME_DIR` on Linux, authenticated with a random token stored in the data dir. Add an IPC server in the app that calls `Service`.
- **Extensions.** Chrome and Edge use Manifest V3 (service worker, `downloads` and `contextMenus` permissions); Firefox uses MV3 or MV2 as needed. Put them in `extension/`. Register the host manifests per browser in Phase 6.
- **Required flow (from the spec).**
  1. The browser detects a download.
  2. The extension sends its metadata to the app.
  3. The app validates and accepts it.
  4. The app sends an acknowledgement.
  5. Only then does the extension cancel the browser download.
  6. If the app is unavailable or rejects it, the browser download continues.
- **Security.** Browser-originated downloads must use a client with `network.Options{AllowPrivate: false}`. The engine's default is already restrictive; the desktop app opts in for typed URLs (`app.go`). Pass the request's cookies, referer and user agent as `CreateRequest.Headers`. Credentials go to `SecretStore` automatically (`engine.splitHeaders`). Never trust filenames or sizes from the extension.
- **Fallbacks.** `blob:` and `data:` URLs, and POST-only downloads, cannot be refetched, so leave them to the browser.
- **Video sites.** Capturing media streams (for example from YouTube) is a product, legal and terms-of-service decision; make it before building. The engine already rejects watch pages with a clear message. If you build it, prefer opt-in integration of an extractor such as yt-dlp over writing one.

### Phase 5: advanced features
- Custom headers, HTTP auth and cookies: the engine already accepts `CreateRequest.Headers`; add UI fields.
- Proxy support: add to `network.Options`. Note that the SSRF dial guard is disabled when a proxy is used (`client.go`), so decide on policy first.
- Checksum entry: a user-provided SHA-256 or MD5 that `verifyFile` checks.
- Queue priorities, scheduled downloads, bandwidth schedules.
- Start with the OS and minimise to the system tray.
- Light theme: every colour is a CSS variable in `frontend/src/style.css` `:root`, so add `[data-theme=light]` overrides.

### Phase 6: packaging
- **Windows:** NSIS installer (`wails build -nsis`), WebView2 bootstrapper, code signing, file and URL associations, native-messaging host registry keys.
- **Linux:** AppImage and `.deb`. `build/linux/Dockerfile` still uses the old "downwell" names and needs updating to Yellow Flash; `dist/linux/*.deb` is stale.
- **OS secret stores:** implement `platform.SecretStore` with Windows Credential Manager and freedesktop Secret Service (for example `github.com/zalando/go-keyring`). Only `MemorySecretStore` exists today, so credentials are lost on restart.
- XDG directories: `os.UserConfigDir` already follows XDG.

## 6. Testing approach

- **Unit tests** sit beside the code. **Integration tests** (`tests/integration`) run service, engine and SQLite against `fileServer`, which can:
  - drop HEAD or range support, or answer 200 for ranges;
  - send a bad Content-Range;
  - return any status, with Retry-After;
  - send a real TCP RST;
  - stall mid-body;
  - limit connections (`maxActive`);
  - redirect;
  - swap the ETag;
  - freeze for crash simulation.
- **Payloads** are deterministic (`payload(n, seed)`) and checked byte for byte.
- **Before you finish any change**, run `go test ./...`, `go vet ./...`, `npx tsc -b` and the Docker `-race` run.
- **UI checks**: use `npm run dev` with the mock backend for layout. Use `wails dev` with `http://localhost:34115` to drive the real backend from a browser.
- **Real-world testing**: `https://ash-speed.hetzner.com/100MB.bin` works but limits connections, which is a good test of adaptation. `proof.ovh.net` blocked this IP after heavy testing. There are no frontend unit tests yet; adding Vitest would be worthwhile.

## 7. Gotchas and tech debt

**Gotchas**
- **Stale editor errors.** After files are deleted or renamed, VS Code's gopls may keep showing errors from the old code. If `go build ./...` passes, run "Go: Restart Language Server", and close any editor tab of a deleted file without saving.
- **`frontend/dist` is committed on purpose.** `main.go` embeds it, so `go build` fails without it. It changes on every frontend build.
- **Generated by Wails:** `frontend/wailsjs/`, `frontend/package.json.md5` and `build/windows/*`. The UI calls `window.go.main.App` directly (typed in `frontend/src/wails.d.ts`), so keep that file in sync when you add `App` methods.
- **Sharing a database.** `cmd/engine-test -list` is read-only, but any other CLI command runs `Recover`. Don't point it at a database the app is using.

**Tech debt (rough priority)**
1. No single-instance lock (see Phase 3, item 1).
2. Errors are free text; the UI explains them by regex (`lib/errors.ts`). Store a structured kind and status on `Download` (for example `ErrorKind`, `HTTPStatus`), which also enables auto-retry.
3. `Download.Retries` accumulates across attempts and resumes; it is not reset per run.
4. In the desktop app the engine logger discards everything. Write a rotating log file in the data dir for support, and never log headers.
5. The UI "connections" setting is applied per download at add time, not to the engine's default config.
6. Narrow window: if the app crashes after publishing a file but before COMPLETED is saved, the file is downloaded again as `name (1).ext`.
7. The original logo uses a copyrighted anime character; commission original art before public distribution.

## 8. Original specification

The full brief (phases, the 20 required test scenarios, and security requirements) was given at project start. Its Phase 1 and 2 requirements are mapped to code and tests in `DEVELOPMENT_PROGRESS.md`. The security requirements in §14 of the brief (SSRF, path traversal, safe overwrite, bounded memory and workers, timeouts, secret storage) are implemented for the engine. Keep them in mind for the browser-facing work in Phase 4.

## 9. Porting Mister Download Manager's backend to Go

The owner chose (2026-11-09) to port Mister Download Manager's whole backend to Go. Its source
is at `C:\Users\Vaisah\Videos\Go\misterdownloadmanager-main`, version 0.5.1, GPL-3.0-or-later,
© 2026 Ahmed Nefzaoui. Yellow Flash is GPL-3.0-or-later as a result.

### Rules for every ported file

1. Start the file with this header, filling in the Rust source path:
   ```go
   // Ported from Mister Download Manager, crates/<crate>/src/<file>.rs
   // Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
   // Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later
   ```
2. Keep Yellow Flash's architecture and its six engine rules (§3). Port behaviour into the
   existing packages instead of recreating their crate layout. The engine stays free of UI,
   SQLite and OS APIs.
3. **Rust dependencies have Go counterparts:**
   - `reqwest` → `net/http` with our `network` client
   - `tokio` tasks → goroutines and `context`
   - `librqbit` (torrents) → `github.com/anacrolix/torrent` (MPL-2.0, compatible with GPL-3.0)
   - `craft-*` atomic saves → our SQLite transactions

   Record each new dependency's licence in `NOTICE.md`.
4. Port their tests along with the code (they have many: planner simulations, lab-server
   end-to-end tests). A port is done only when its tests pass under `go test -race`.
5. Assets (icons, the geo table, flags) keep their own licences; add each to `NOTICE.md` when
   copied.

### Port order (each step builds on the previous)

| # | MDM source | Go destination | Notes |
|---|---|---|---|
| 1 | `crates/ipc` | `internal/ipc` | Single-instance lock and token-protected local socket. This also fixes tech-debt item 1 (two app instances). It is needed by 2, 3, 6 and 7. |
| 2 | `crates/engine` remainder: `hosts.rs`, `limiter.rs`, `naming.rs`, `verify.rs`, `ftp.rs`, `dns.rs`, `pac.rs`, `sites/gdrive.rs`, `sparse.rs`, `disk.rs` | `internal/engine`, `internal/infrastructure/network` | Persisted host limits, per-download limiter, FTP/FTPS, DoH/DoT, proxy auto-config, Google Drive links. Compare with our existing planner, retry and first request; keep whichever is better tested. |
| 3 | `crates/nmhost` + `extension/` | `cmd/nm-host`, `extension/` | Browser capture (Phase 4). The extension is JavaScript and can be ported nearly as is (GPL-3.0); change its IDs and name to Yellow Flash's own. |
| 4 | `crates/tools` | `internal/tools` | yt-dlp and ffmpeg video, gallery-dl, run as separate programs, plus the adapter relay. |
| 5 | `crates/torrent` | `internal/torrent` | Magnet links and .torrent files via anacrolix/torrent. |
| 6 | `crates/remote` | `internal/remote` | Phone or LAN remote control over HTTPS with pairing. |
| 7 | `crates/cli`, `crates/mcp` | `cmd/engine-test` → `cmd/yellowflash-cli`, `cmd/yellowflash-mcp` | Automation API. |
| 8 | `crates/debrid`, `crates/extract`, `crates/geo`, `crates/crash` | `internal/...` | Debrid services, archive extraction, server location, crash reports. |
| 9 | `crates/labserver` | `tests/lab` | Their fault-injecting HTTP/FTP server; merge it with our `fileServer`. |

Not ported: `crates/app` (their egui desktop UI), since Yellow Flash's UI is Wails + React. Port
its *behaviour* (queues, scheduler, settings) into Go services and the React UI as each feature
is needed.

Queues and the scheduler (previously the next task) live in their app crate, so port them in
step 1 or 2 time, reading `crates/app/src/` and `docs/queues-and-scheduler.md`.
