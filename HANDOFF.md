# Yellow Flash: developer handoff

Last updated: 2026-11-06. Read this first, then `README.md` (setup and architecture summary) and `DEVELOPMENT_PROGRESS.md` (detailed test evidence per milestone).

## 1. Where the project stands

The goal is a cross-platform (Windows and Linux) download manager that competes with Internet Download Manager. It has a Go engine, a Wails v2 desktop shell, a React + TypeScript UI, JSON storage (a `downloads.json` list, as Mister Download Manager keeps), and (later) browser extensions.

| Phase | Scope | Status |
|---|---|---|
| 1 | Core Go download engine: segmented, resumable, crash-safe, tested | **Done** |
| 2 | Desktop app ("Yellow Flash" UI on Wails) | **Done** |
| 3 | Download management: queues, limits, scheduling, throttling, notifications | Next |
| 4 | Browser integration: Chrome, Edge and Firefox extensions + native messaging | Backend done (port step 3); settings UI to do |
| 5 | Advanced: auth, cookies, proxies, checksums, priorities, themes | Planned |
| 6 | Packaging: installers, OS keychains, notifications, signing | Planned |


## 2. Getting started

**Prerequisites**
- Go 1.26 or later. No cgo is needed.
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
go run ./cmd/yellowflash-cli get <url> -o out -c 8           # engine without any UI (list, resume <id>, help)
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
cmd/yellowflash-cli/        Command line: the engine without UI, and the app's automation API
cmd/yellowflash-mcp/        MCP server for AI assistants (the app's automation API)
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
  infrastructure/persistence/jsonstore downloads.json + resume files, as MDM (implements domain.Store)
  infrastructure/persistence/memory   in-memory domain.Store (tests, CLI without -db)
  platform/                 SecretStore, OpenPath/RevealPath (OS-specific files are build-tagged)
tests/integration/          end-to-end tests against a configurable httptest server
```

**Rules that keep the engine correct.** Don't break these.

1. **One state authority.** Only `engine.Manager` changes `Download.Status`, always through `domain.Download.Transition` while holding `Manager.mu`. Workers report bytes only through atomics (`segmentState.done`, `tracker`).
2. **Durability ordering.** Read the byte counters, then `fsync` the `.part` file, then persist those counters. Saved progress must never exceed data that is on disk. See `checkpoint` in `worker.go`.
3. **Representation identity.** Partial data is reused only when size and ETag/Last-Modified match. Otherwise the engine restarts from zero (`representationChanged`, `If-Range`).
4. **Never overwrite.** `filesystem.Publisher` publishes as `name (n).ext` on a clash. The temp file is `name.part` with mode 0600.
5. **Dependencies point inward.** The engine imports domain, `events`, `platform`, `network` and `filesystem`, but never the store, Wails or OS APIs. OS-specific code goes in build-tagged files under `infrastructure/network` or `platform`.
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
- Storage: originally SQLite; replaced on 2026-11-09 by `jsonstore` (downloads.json with generations, resume/<id>.json per download), ported from MDM.
- Security: URL validation, SSRF guard at dial time (covers redirects and DNS rebinding), filename sanitising, header-injection guard.

### Phase 2: desktop app (done)
- **UI**: toolbar; status and category sidebar; sortable table; details panel with a live segment map; status bar.
- **Dialogs**: Add URL with a link preview (name, size, type, resume support), Settings, and Remove (optionally deleting the file).
- **Interaction**: clipboard pre-fill, drag-and-drop of links, keyboard shortcuts, right-click menu, plain-language error explanations (`lib/errors.ts`).
- **Backend additions**:
  - `Manager.Inspect`, `network.UnwrapLink` (Google, Facebook and YouTube redirects).
  - `Service.ResumeAll/PauseAll`.
  - Settings stored in the download list.
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

1. **Single-instance lock: done (2026-11-09)**, by port step 1 (`internal/ipc`, see §9). We use our own lock rather than Wails' `SingleInstanceLock`, because it must be taken before the DB is opened and its channel also carries commands.
2. **Maximum simultaneous downloads and a queue runner: done (2026-11-09)**, by port step 2b (`application/queues`). The original plan follows for reference. Add `internal/application/queue` (or extend `Service`). It holds QUEUED downloads and calls `Manager.Start` while `active < limit` (a setting, default 3), and listens to `DownloadChanged` events to start the next one.
   - Use the `queues` list and `Download.QueueID`, both kept in `downloads.json`.
   - Keep the engine unaware of queues.
   - Test: 5 adds with limit 2 never exceed 2 active, and all 5 complete.
3. **Remember per-host connection limits across restarts: done (2026-11-09)**, by port step 2a (`engine/hosts.go`, saved in `downloads.json`). The original plan follows for reference. When adaptive concurrency drops connections, record the stable count for the host in a new table, `host_limits(host, max_connections, updated_at)` (migration v3). Start future downloads from that host with it.
   - Hook: the `errYielded` branch in `transferSegmented` (`engine/worker.go`).
   - Expose it to the engine through a small port, so the engine doesn't import the store.
4. **Dynamic segment splitting: done** (see above). Possible follow-up: drop a connection that is far slower than the others and re-split its remainder.
5. **Bandwidth limiting: engine done** (`Manager.SetSpeedLimit`). Still to do: a Settings or toolbar control, persisting the limit, and per-download limits.
6. **Scheduler: done (2026-11-09)**, by port step 2b (`application/queues`); only the UI is missing. Start or stop a queue at set times. Live this in the queue runner; persist schedules in `queues` or a new `schedules` table.
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
- **Security.** Browser-originated downloads use the engine's restricted client (`CreateRequest.Restricted`, persisted as `Download.Restricted`; port step 3). The desktop app allows private addresses for typed URLs only (`app.go`). Pass the request's cookies, referer and user agent as `CreateRequest.Headers`. Credentials go to `SecretStore` automatically (`engine.splitHeaders`). Never trust filenames or sizes from the extension.
- **Fallbacks.** `blob:` and `data:` URLs, and POST-only downloads, cannot be refetched, so leave them to the browser.
- **Video sites.** Capturing media streams (for example from YouTube) is a product, legal and terms-of-service decision; make it before building. The engine already rejects watch pages with a clear message. If you build it, prefer opt-in integration of an extractor such as yt-dlp over writing one.

### Phase 5: advanced features
- Custom headers, HTTP auth and cookies: the engine already accepts `CreateRequest.Headers`; add UI fields.
- Proxy support: done (`network.Options.Proxy`/`PACScript`, network settings). Policy for untrusted addresses: the proxy is kept and each target is checked before it is handed over (`guardedProxy` in `client.go`; see "Step 3 notes").
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

- **Unit tests** sit beside the code. **Integration tests** (`tests/integration`) run service, engine and the JSON store against `fileServer`, which can:
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
- **The command line's own list.** `yellowflash-cli get`/`resume` keep their downloads in `<data>/cli/downloads.json`, locked by one command line at a time; never the app's list.

**Tech debt (rough priority)**
1. ~~No single-instance lock~~: fixed by `internal/ipc`; the command line (which replaced `cmd/engine-test`) has a list and lock of its own.
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
   storage and OS APIs.
3. **Rust dependencies have Go counterparts:**
   - `reqwest` → `net/http` with our `network` client
   - `tokio` tasks → goroutines and `context`
   - `librqbit` (torrents) → `github.com/anacrolix/torrent` (MPL-2.0, compatible with GPL-3.0)
   - `craft-*` atomic saves → `jsonstore` (temp file, fsync, rename)

   Record each new dependency's licence in `NOTICE.md`.
4. Port their tests along with the code (they have many: planner simulations, lab-server
   end-to-end tests). A port is done only when its tests pass under `go test -race`.
5. Assets (icons, the geo table, flags) keep their own licences; add each to `NOTICE.md` when
   copied.

### Port order (each step builds on the previous)

| # | MDM source | Go destination | Notes |
|---|---|---|---|
| 1 ✅ | `crates/ipc` (`lib.rs`, `api.rs`) | `internal/ipc`, `internal/platform/lock*.go`, `main.go`, `commands.go` | **Done 2026-11-09**; see "Step 1 notes" below. `browsers.rs`, `capture.rs` and `extension.rs` belong to the browser host and move to step 3. |
| 2 ✅ | `crates/engine` remainder: `hosts.rs`, `limiter.rs`, `naming.rs`, `verify.rs`, `sparse.rs`, `disk.rs` (2a); `sites/gdrive.rs`, `dns.rs`, `pac.rs`, `ftp.rs`, queues and scheduler, plus JSON storage replacing SQLite (2b) | `internal/engine`, `internal/infrastructure/{network,persistence/jsonstore}`, `internal/platform`, `internal/application/queues`, `internal/domain/queue` | **Done 2026-11-09.** See "Step 2a notes" and "Step 2b notes". |
| 3 ✅ | `crates/nmhost` + `extension/`, `ipc`'s `browsers.rs`, `capture.rs`, `extension.rs` | `cmd/nm-host`, `extension/`, `internal/browser`, `browser_app.go` | **Done 2026-11-09.** See "Step 3 notes". |
| 4 ✅ | `crates/tools` | `internal/tools`, `tools_app.go` | **Done 2026-11-10** (backend; the windows are UI work). See "Step 4 notes". |
| 5 ✅ | `crates/torrent`, and `crates/app/src/torrents.rs`'s list, sharing limits and blocklist | `internal/torrent`, `torrents_app.go` | **Done 2026-11-10** (backend; the torrent windows are UI work). See "Step 5 notes". |
| 6 ✅ | `crates/remote`, and the app's answers to it | `internal/remote`, `remote_app.go` | **Done 2026-11-10** (backend; Options → Remote is UI work). See "Step 6 notes". |
| 7 ✅ | `crates/cli`, `crates/mcp`, and the app's automation API answers | `cmd/yellowflash-cli` (replaced `cmd/engine-test`), `cmd/yellowflash-mcp`, `api_app.go` | **Done 2026-11-10.** See "Step 7 notes". |
| 8 ✅ | `crates/debrid`, `crates/extract`, `crates/geo`, `crates/crash` | `internal/debrid`, `internal/extract`, `internal/geo`, `internal/crash`, `internal/platform/{keychain,mark,trash}*`; `debrid_app.go`, `debrid_torrents_app.go`, `extract_app.go`, `geo_app.go`, `crash_app.go` | **Done 2026-11-11** (backend; their windows are UI work). See "Step 8 notes". |
| 9 | `crates/labserver` | `tests/lab` | Their fault-injecting HTTP/FTP server; merge it with our `fileServer`. |

### Step 1 notes (`internal/ipc`)

**How it works.**
- `main.go` parses an IDM-style command line (`ipc.ParseArgs`) and tries `ipc.Send` to a running instance.
- If none answers, it takes `ipc.Lock` (an OS lock on `instance.lock`: `flock` on Linux, `LockFileEx` on Windows) **before** opening the DB.
- It then serves `127.0.0.1:<random>` and writes `ipc.json` `{port, token, pid}`, mode 0600, removed again on clean exit.
- A launch that loses the race keeps retrying "send, or take the lock" for 10 s, so it also takes over from an instance that is quitting. MDM only retried the send.
- Before sending, a second launch calls `platform.AllowForeground(pid)` so the running app may raise its window on Windows.

**Kept from MDM.**
- One JSON line per request, one answer line back: `ok`, `denied`, `gone`, `too-large`, `timeout` or JSON.
- Limits: requests up to 2 MiB within 3 s, at most 8 connections at once.
- No answer to HTTP requests (web pages probing the port).
- The token is compared in constant time.

**Changed from MDM.**
- Download IDs in the API are UUID strings.
- A line starting with `{` is never taken for HTTP. MDM's check dropped JSON whose last word began with `HTTP/`.
- The token has 256 bits from `crypto/rand`.
- Relative `/p` and `.torrent` paths are resolved in the sending process.
- The data folder logic moved from `app.go` to `ipc.DataDir`. `YELLOWFLASH_DATA_DIR` overrides it, and a writable `data` folder next to the exe gives portable mode.

**How the app handles commands** (`commands.go`). Commands are queued until `OnDomReady`, then:
- plain launch: focus the window;
- `/d`: open Add URL, pre-filled with `/f` and `/p`;
- `/d … /n`: add and start; `/d … /a`: add only. Both report the result as a toast through the `app:notice` event;
- `/s`: `ResumeAll` (a stand-in until the queue runner exists);
- `/exit`: quit;
- `/show`: emits `app:show`. The UI does not handle that event yet.

After `Shutdown` starts, commands are answered `gone`. MDM saved pending URLs to the queue instead.

**Not done yet.**
- Queries and actions (`query`, `action`, `api`, `pair`) all get `{"error":…,"code":"api-off"}`. Implement the automation API in step 7, using the answer types in `ipc/api.go`.
- MDM's JSON Schema generation (`docs/automation-api.schema.json`) is not ported; add it with step 7.
- Browser fields: see "Step 3 notes" for which are used, refused or still ignored.
- Magnet links and `.torrent` files: handled since step 5 (`torrentCommand`; see "Step 5 notes").

**Verified.**
- `go test -race` passes for `internal/ipc` and `internal/platform`; the tests port every MDM ipc test plus end-to-end `Serve`/`Send` tests.
- The built exe was tested by hand with `YELLOWFLASH_DATA_DIR` set: a second launch with `/d … /a` handed over in about 50 ms and the download appeared as QUEUED; `/exit` quit the app and removed `ipc.json`.

### Step 2a notes (engine: hosts, limiter, verify, naming, sparse, disk)

**Learned host limits** (`engine/hosts.go`, from `hosts.rs`)
- These replace `Manager.hostLimits`. The cap is the **most** connections seen receiving data when a refusal came. The old code kept the minimum, which can learn 1 for a host that allows 2.
- A cap applies after 2 refusals and is forgotten 10 minutes after it was last seen (`Config.HostLimitDecay`).
- "Receiving" is the new `tracker.receiving` counter: it counts connections that got their first byte, not ones still connecting.
- Learned caps are saved through `domain.HostLimitStore` (`Config.HostLimits`). The JSON store keeps them in `downloads.json` (`hostLimits`), and `app.go` passes the store.
- Verified: `TestLearnedHostLimitSurvivesRestart` learns 2 from a 2-connection server, and after a restart the next download gets **0** refusals.

**Speed limits** (`engine/limiter.go`, from `limiter.rs`)
- One token bucket with a 1 s burst now serves both the global limit (`SetSpeedLimit`) and a new per-download limit. `golang.org/x/time` is no longer used.
- The per-download limit is set by `CreateRequest.SpeedLimit` or `Manager.SetDownloadSpeedLimit`, which persists it in `downloads.speed_limit` and changes it live.
- Not yet in `Service`, the app bindings or the UI.

**Checksums** (`domain/download/checksum.go`, `network/headers.go`, `engine/verifier.go`, from `verify.rs`)
- Supported: CRC32, MD5, SHA-1, SHA-256 and SHA-512.
- From the server: Repr-Digest, Digest, Content-MD5, Content-Digest and `x-goog-hash`; the strongest one wins.
- `RemoteMetadata.Checksum` is now a `domain.Checksum`.
- A user checksum (`CreateRequest.Checksum`, any form `ParseChecksum` accepts, such as `sha256sum` output) is validated by `Create`, stored as `downloads.expected_checksum`, and takes precedence over the server's.

**File names** (`network/headers.go`, `filesystem/naming.go`, from `naming.rs`)
- `Content-Disposition` is parsed as browsers do: `filename*` wins, plain names are percent-decoded, and a parameter with no disposition type is accepted.
- Unicode format characters (RTL overrides and the like) are dropped, and every Windows device name is handled (CONIN$, COM¹…).
- `FitName` shortens names to fit Windows' 259-character path limit, keeping the extension. We kept our 200-**byte** name cap: MDM's 230-UTF-16-unit cap can exceed ext4's 255-byte limit.

**Disk** (`platform/disk*.go`, `engine/disk.go`, from `disk.rs` and `sparse.rs`)
- Before writing, the engine checks free space (the bytes still to write plus a 128 MiB margin) and FAT32's 4 GiB limit; failures return `domain.ErrNoSpace` or `domain.ErrTooBigForFileSystem`. The probe gives up after 5 s (a hung network drive), and an unknown volume skips the check.
- On Windows, new files of 5 MiB or more are made sparse before preallocation, so NTFS doesn't zero-fill ahead of far writes, and are made normal again before verification.
- Linux probes with `statfs`; other systems skip the check.

**Not ported from these files:** `Stems`/`stem_destination` (they come with yt-dlp in step 4) and MDM's process-wide destination reservations (`Manager.reserveNameLocked` already does this).

**Verified:** all tests pass on Windows and under `-race` on Linux. Ported tests are in `engine/ported_test.go`, `filesystem/naming_test.go`, `domain/download/checksum_test.go`, `network/network_test.go`, `platform/disk_windows_test.go` and `tests/integration/ported_test.go`.

### Step 2b notes (storage, Drive, DNS, PAC, FTP, queues): done 2026-11-09

Each part is its own commit; the commit messages give the details.

**Storage** (`persistence/jsonstore`, from MDM's `store.rs`)
- SQLite is gone. The download list, settings, queues and learned host limits live in `downloads.json`.
- A background saver writes it atomically and coalesces changes.
- Generations `.bak`, `.1.bak` and `.2.bak` are kept. A damaged list is set aside as `.corrupt-<time>.bak`, and a list that can't be read is never overwritten.
- `resume/<id>.json` holds each download's ranges, fsynced before progress is recorded (rule 2).
- An old `downloads.db` is not imported.

**Google Drive** (`network/sites`, `network/siteflow.go`)
- Viewer and Docs links are rewritten to the file or export address. Drive's own pages are read: the virus-scan form, confirm links, and the address in the page data.
- Refusals are given in Google's words, and folders are refused at `Create`.
- Drive's hosts get a browser User-Agent. Cookies follow redirects between Drive's three hosts only.

**DNS** (`network/dns.go`, `network/dial.go`)
- The system's DNS, plain DNS over TCP, DoT or DoH, with presets, through Go's resolver with a custom `Dial`.
- Every client now races a host's addresses (RFC 8305) and remembers the winner.
- Selected by `engine.Config.DNS`. Not yet in the settings or the UI.

**PAC** (`network/pac.go`)
- Runs in goja, pinned to a 2026-10-25 commit, with a time limit, a heap watchdog, a stack limit and no binary buffers.
- Answers are cached per URL; MDM cached per host, which was wrong for scripts that decide by path.
- Selected by `network.Options.PACScript`. Not yet in the engine config or settings; a fixed proxy URL is still to do.

**FTP** (`network/ftp.go`, test server in `network/ftptest`)
- An `http.RoundTripper`, so the engine downloads FTP like HTTP: parts, resume through MDTM/`If-Range`, and 421 shedding connections.
- Our own small client, with CR/LF injection refused.
- An address's password moves into the SecretStore at `Create`.
- Passive by default; active mode (PORT/EPRT) is a network setting. Servers that require TLS session reuse on data connections (vsftpd's `require_ssl_reuse`) may refuse Go's TLS 1.3 resumption.

**Queues and scheduler** (`domain/queue`, `application/queues`): this covers Phase 3 items 2 and 6.
- Ports `queue.rs`'s rules, adds a runner over a Get/Start/Pause port, and keeps the built-in Main and Synchronization queues.
- Schedules: start and stop times, days or a single date, retries, open or exit when done.
- `/s` and `/a` use the queues.
- Since 950188c and the power commit: download limits (`application/datacap`), turning off when done (30 s countdown; `CancelTurnOff`/`TurnOffNow`), keeping the computer awake while a queue runs, and waking it for a scheduled start (Windows waitable timer; Linux RTC wake alarm, which needs the `wakealarm` group set up by an administrator). App methods exist for the UI; the UI itself is not built yet.

**Also fixed:** `TestWorkerFailureAndRetry` depended on a part starting exactly at 256 KiB and flaked under `-race`. It now fails whichever part comes after the first.

### Step 3 notes (browser integration): done 2026-11-09

Four commits (3a–3d); the commit messages give the details.

**Shared rules** (`internal/browser`, from `ipc`'s `capture.rs`, `browsers.rs`, `extension.rs`)
- The capture rules are saved by the app as JSON under `settings["browser.capture"]` in `downloads.json`. The host reads them (falling back to `.bak`) at every hand-off, so a change applies at once.
- Our own identity: the manifest key `browser.ManifestKey` gives the unpacked ID `fhlfnlgbgkmofdmohiohgmiojleegaah`. Its private half was not kept: an unpacked extension needs only the public key, and a store signs with its own. Host name `com.yellowflash.host`; Firefox ID `integration@yellowflash`. Store IDs are placeholders and are never served until set.

**Native host** (`cmd/nm-host`)
- Build it as `yellowflash-nmhost` (`.exe` on Windows) beside the app: `go build -o "build/bin/yellowflash-nmhost.exe" ./cmd/nm-host`. Its replies match MDM's word for word, including the older `(FileType)` wording that the extension still parses.
- `--register`/`--unregister`. Windows: HKCU (HKLM with `--all-users`) through `x/sys/windows/registry`, with the manifests beside the exe. Linux and macOS: each installed browser's folder, skipping browsers a package's system-wide manifest already covers. `--package-manifests <root>` writes the system-wide ones for Linux packages.
- The app registers the host at every start when it is beside the app, the copy isn't portable, and integration is on (there is no installer yet; Phase 6).
- Downloads left to the browser are logged in `<data>/logs/yellowflash.log`, a private window's without its site or name.
- MDM's Safari path (`--safari`) is not ported.

**Extension** (`extension/`)
- MDM's extension with Yellow Flash's names, key, IDs, version (kept equal to `ipc.AppVersion`) and icons (from `build/appicon.png`, also embedded in `video.js`). MDM's sponsor link is removed.
- `node tools/extension/test.mjs` runs its 65 checks without a browser. `cmd/nm-host`'s `TestTheShippedExtensionIsTheOneServed` pins the manifests and `background.js` to the host's IDs, name and version.
- Not ported: `tools/extension/e2e.mjs` (needs a real browser), `pack.ps1` and `check-pack.mjs` (store packaging), `locales.py`.
- `docs/browser-help.md` was rewritten for what Yellow Flash does today, keeping the section ids the extension links to.

**App** (`browser_app.go`, `commands.go`)
- The host sets `ipc.Command.Browser`.
- A browser download starts at once, without the Add URL dialog, which would lose its cookies and its restriction. It carries Referer, Cookie and User-Agent, and `CreateRequest.Restricted`.
- Refused on the spot, so the browser keeps the download: private windows (the list can't keep a download out yet), POST forms (the engine has no POST), video pages (step 4), and link lists (they need the picker UI).
- The toolbar's "never from this site" and the video button's "not on this site" update the saved rules.
- Bindings for the UI: `GetBrowserSettings`, `SaveBrowserSettings`, `GetBrowserIntegration`, `RegisterBrowserIntegration`, `UnregisterBrowserIntegration`. The UI is not built.
- Still ignored: `final_url`, `size` and `mime` (never trusted; the engine asks the server), `cookie_jar` (for yt-dlp, step 4), and the browser's own proxy (`proxy`, `proxy_pac`, `proxy_login`). The app's own proxy settings apply.

**Proxy policy for untrusted addresses** (`network/client.go`)
- The user's proxy is kept: it is their own setting, and connecting to it is allowed even on this computer.
- Each target is checked before it is handed over. Literal IPs and `localhost` names are always checked. Names are looked up locally too, except through `socks5h`, which is chosen so that only the proxy resolves them. A name that doesn't resolve locally is left to the proxy.
- Each request's proxy is decided once and recorded in its context, which the transport and the dial guard both read, so a PAC script can't answer differently in between. FTP never uses the proxy and stays under the dial guard.

**Known flaky:** `TestConnectionReset`'s bytes-served bound fails about 1 run in 20 under load on Windows, on 0f24317 as well.

### Step 4 notes (helper programs: yt-dlp, ffmpeg, gallery-dl): done 2026-11-10

Seven commits (4a–4d in parts, then 4e); the commit messages give the details. Everything is in `internal/tools`; the app's bindings are in `tools_app.go`.

**Installs** (`tools.go`, `openpgp.go`, `ffmpeg.go`, `gallerydl.go`, `chosen.go`)
- yt-dlp and gallery-dl are installed only when their release's checksum list is signed by the pinned key (`internal/tools/keys`) and the program matches it. ffmpeg (BtbN's LGPL Windows build) is checked against its checksum list and GitHub's digest; BtbN signs nothing. Linux and macOS use the system's ffmpeg.
- `openpgp.go` is a minimal port of MDM's checker on Go's standard crypto (RSA 2048–8192 bits, legacy Ed25519). MDM's release key is in `testdata/` only, to test the Ed25519 path.
- Installs go in `<data>/tools/<tool>/<version>/`, switched atomically through `current.json`, never downgraded, with one previous version kept. The person's own copies (`tools/programs.json`) are used first and never updated.
- `.gitattributes` keeps the keys and signed fixtures byte-exact.

**Running** (`proc.go`, `proc_windows.go`, `proc_unix.go`)
- Helpers run without a console and are ended with everything they started: a kill-on-close Job Object on Windows, the process group elsewhere.
- Arguments go in fixed layers, CR/LF/NUL are refused, and `--` comes before the address.
- Cookies go in a private Netscape file, never on the command line.
- The app's fixed proxy reaches the helpers through their environment (`tools.SetProxy`, set in `applyLiveSettings`). A PAC script can't be passed on.
- Not ported: MDM's relay through a chosen network adapter (`relay.rs`), since adapter choice isn't ported; and `update.rs` (MDM updating itself with MDM's release key). Yellow Flash's own updates are Phase 6.

**Pages and downloads** (`pages.go`, `resolve.go`, `playlist.go`, `servers.go`, `video.go`, `gallery.go`)
- What a page is (a video, a gallery, a list), the choices it offers, and lists listed lazily.
- Video downloads with media-server fall-over, the nearest-quality fallback, subtitles retried once, and MP3, Opus and FLAC conversions.
- Galleries into their own folder.
- yt-dlp's marker lines are `YFP`/`YFF`/`YFQ` (MDM's `MDMP`/`MDMF`/`MDMQ`).
- The tests drive the test binary as a fake yt-dlp and gallery-dl.

**App** (`tools_app.go`)
- Bindings: `GetHelpers`, `InstallHelper` (events `helpers:progress`), `ChooseHelper`, `ForgetHelper`, `LookUpVideo`, `StartVideoDownload`, `StartGalleryDownload`, `ListMediaDownloads`, `StopMediaDownload` (events `media:progress`, `media:changed`).
- Video and gallery downloads are kept in memory for the session; giving them rows in the saved download list is still to do.
- Still refused from the browser: video pages and link lists, until the video and link windows exist (accepting them now would lose them). When they come, check a browser-sent page's host as the restricted client does before handing it to yt-dlp, which has no such guard.
- Playlists (`tools.StartListing`) have no binding yet: they need the list window.
- The Linux `-race` run of `internal/tools` is still to do (Docker wasn't running); every test passes on Windows, and `vet` passes for Linux and macOS.

### Step 5 notes (torrents): done 2026-11-10

Three commits (5a rules, 5b session, 5c list and app). MDM wraps librqbit; we wrap `anacrolix/torrent` v1.61.0 (MPL-2.0; it and its ~70 modules are in `NOTICE.md`).

**Rules** (`names.go`, `trackers.go`, `blocklist.go`)
- Every file name is checked before a torrent is listed or added, and again when storage opens it: no `..`, separators or control characters; on Windows no `:<>"|?*`, trailing dot or space, or device name; no case twins where case is ignored.
- Trackers on this computer or its network (and numeric-looking names such as `0x7f.1`) are dropped, in the .torrent (outside the info dictionary, so the hash stays) and in magnet links.
- Blocklists: P2P text or eMule `.dat`, gzipped or not, converted to checked `yf:start-end` lines.

**Session** (`session.go`, `files.go`)
- `Start`, `Metadata` (a magnet's file list; nothing downloaded or uploaded, and the look-up is dropped), `Add` (the files chosen; paused or not), `Stats` (speeds from deltas), `Peers`, `Pause` (drops the torrent), `Resume` (adds it again), `Remove` (optionally with its files, by name only), and the session's down and up limits (at least 16 KiB/s).
- Off: web seeds, WebTorrent, a magnet's `xs`/`as` sources, and `x.pe` peers that aren't on the internet. These are all addresses a torrent names.
- **Peers never see this computer's address** (the owner's rule, stricter than MDM, whose torrents go direct unless `torrent_proxy` is on). Every peer connection is dialled through the connection settings' fixed **SOCKS5** proxy (`socks5://` or `socks5h://`, a custom anacrolix dialer); HTTP trackers go through it too. Nothing that would show the address runs: no TCP/uTP sockets of our own (so no listening port, and no address of ours in handshakes or announces), no UPnP, no DHT and no UDP trackers (UDP can't go through the proxy). Without a fixed proxy torrents are refused (`ErrNoProxy`); an HTTP proxy can't carry peer connections (`ErrProxyKind`). Changing the proxy restarts a running session, or stops it. The only direct mode is `Config.Offline`, for the tests' loopback swarm; the tests run every leecher through a SOCKS5 server and check the seeder was reached through it.
- `files.go` is our own file storage: anacrolix's default memory-maps every file and never unmaps it, which on Windows locks a paused or removed torrent's files. It writes in place (no `.part` files), never writes padding files, and treats a piece whose file is missing or too short as not there, so deleted files download again without being hashed. Checked pieces are kept in a bolt store in the state folder.
- v2-only torrents (BEP 52) are refused: torrents are named by their v1 hash, as in MDM.
- Offline (tests), `Start` tries again when Windows refuses the port picked (Hyper-V/Docker reserved ranges); anacrolix only retries "address in use".
- A stopped torrent's storage opens no file again (a late hash would otherwise keep one open, locked on Windows).

**List and app** (`list.go`, `torrents_app.go`)
- `<data>/torrents/torrents.json` (JSON, as everywhere else) holds each torrent's hash, name, folder, files chosen, paused, finished, uploaded (added up run after run) and seconds shared. `files/<hash>.torrent` keeps each .torrent; `blocklist.p2p` and `blocklist.source` keep the blocklist, fetched again after 7 days through the network settings' proxy (public addresses only, 64 MiB at most).
- The session starts with the first look-up or add, or at startup when a torrent in the list isn't paused. A new blocklist or proxy restarts it; the upload limit applies at once. MDM's incoming, port, UPnP, DHT and LSD settings are gone: each would show the address.
- A finished torrent stops, as any download does, unless **Keep sharing** is on, and then at the ratio or time limit (MDM's D-018 and defaults: off, 2.0, 1440 minutes).
- Bindings: `GetTorrentSettings`, `SaveTorrentSettings`, `LookUpTorrent` (a magnet asks its peers for up to a minute; a `.torrent` must be an absolute path), `SelectTorrentFile`, `AddTorrent`, `ListTorrents`, `TorrentFiles`, `TorrentPeers`, `PauseTorrent`, `ResumeTorrent`, `RemoveTorrent`, `TorrentMagnet`, `ShowTorrentFolder`. Events: `torrents:progress` (every second while the session runs), `torrents:changed`, and `app:add-torrent` (a `TorrentPreview` for the window that chooses files).
- Commands: a magnet, a `.torrent` path or remote control's `.torrent` bytes is looked up, then opens that window; with `/n` or `/a` it is added whole to `/p` (or the download folder), started or paused.
- Not ported: MDM's piece order (first and last pieces first, for previews), per-torrent speed limits, the network-adapter bind, debrid hand-off (step 8), Windows file associations (packaging, Phase 6), stopping the session when idle, and fetching `.torrent` files from web addresses (a browser download of one is an ordinary download for now).
- Verified on Windows: a loopback seeder and leecher (all files, chosen files, pause and resume, a file deleted while paused, a magnet's metadata from the seeder), and the app's flow from look-up to removal. The Linux `-race` run is still to do with `internal/tools` (Docker wasn't running).

### Step 6 notes (remote control): done 2026-11-10

Two commits (6a the package, 6b the app). Everything is MDM's design; the docs are its `docs/remote-control.md`.

**The package** (`internal/remote`)
- Off by default. When on, one HTTPS listener per local network address (10/8, 172.16/12, 192.168/16, fc00::/7), never 0.0.0.0 or ::; or an address or adapter the person names. Addresses are watched every 10 s and the listeners follow them. Default port 8463.
- A self-signed certificate (ECDSA P-256, Go's standard crypto, no new dependency), made once and kept 800 days in `<data>/remote/` (`cert.der`, `key.der`, `cert.json`), owner-only; its SHA-256 fingerprint is shown to compare with the phone's.
- Pairing: an 8-digit code, ten minutes, one device; five paced tries (one a second) per address, twenty in all. Paired devices are in `devices.json` as token hashes (SHA-256) only; removing one refuses it at its next request.
- Every request: Host must be an address, localhost, this computer's name or a name the person added (DNS rebinding); `/api/` requests must come from the page's own origin (or the extension's) and not be marked cross-site; answers forbid framing, sniffing and caching. Plain `http://` is redirected to `https://`. At most 64 connections, 10 s handshakes, 60 s idle.
- The page (`internal/remote/web/`, MDM's, rebranded) is embedded. Its texts come through `remote.Texts`; the app passes none yet (English).

**The app** (`remote_app.go`)
- Each request becomes an `ipc.Command` marked `Remote` (a field never read from an IPC line, so no local program can claim it) through `ipc.Sink`; `dispatch` answers those with `answerRemote`. Other programs' queries still get `api-off` (step 7).
- A paired device lists, adds, pauses, starts and removes downloads and torrents, and starts and stops queues. Its list has no paths. A `.torrent` comes as its bytes, never a path on this computer; magnets and `.torrent` bytes are looked up in the background (answer `pending`). Torrents are `torrent:<hash>` in the list.
- Bindings: `GetRemoteSettings`, `SaveRemoteSettings` (`remote.Config`: on, port, listen, hosts), `GetRemoteInfo` (state, URLs, fingerprint, devices), `RemoveRemoteDevice`, `StartRemotePairing`, `GetRemotePairing`, `StopRemotePairing`. Event `remote:changed` when a device pairs or leaves.
- Not ported: categories in the list (Yellow Flash has none yet), the page's translations, a QR code of the pairing address, and MDM's per-download `remote` handling of adapters and kill switches (not ported either).

### Step 7 notes (automation API, command line, MCP): done 2026-11-10

Three commits (7a the app's API, 7b the MCP server, 7c the command line).

**Automation API** (`api_app.go`)
- Programs on this computer ask through the app's channel (`ipc.json`, its token): `{"query":"list"}` (downloads and torrents with paths, total speed, queues, free space), `{"action": pause|start|remove|delete, "ids": […]}`, `start_queue`/`stop_queue` by queue name, `{"pair": true}` (a remote-control code, its addresses and a terminal QR code; `rsc.io/qr`). Answer types are in `internal/ipc/api.go`.
- Off by default (`api.on` setting); `{"api": true|false}` switches it and is kept. While off, questions get `api-off`. Adding (`/d`-style commands) never needed it.
- The same code answers a paired device (step 6), which gets `remote-off` once the remote control is off, and a list without paths or passwords.
- Not ported: categories (none yet), the JSON Schema of the answers (`docs/automation-api.schema.json` in MDM).

**MCP server** (`cmd/yellowflash-mcp`)
- Tools: `list_downloads` (status, search, limit; newest first), `add_download`, `pause_downloads`, `resume_downloads`, `remove_downloads` (delete_files), `start_queue`. Download ids are strings.
- MCP's stdio transport is written by hand (`initialize`, `ping`, `tools/list`, `tools/call`; protocol versions 2024-11-05 to 2025-06-18) instead of an SDK. MDM uses rmcp.

**Command line** (`cmd/yellowflash-cli`, flags anywhere, `--json` for programs)
- Its own downloads: `get` (connections, headers, checksum), `resume`, `list`, in `<data>/cli/downloads.json` behind a lock of its own. This replaced `cmd/engine-test`.
- The running app: `status`, `add`, `pause`, `start`, `remove`, `delete`, `pair`, `api on|off`.
- Helpers (the app's, `<data>/tools`): `install-ytdlp`, `install-ffmpeg`, `install-gallery-dl`, `video-info`, `video-get`, `playlist-info`, `gallery-get`.
- Torrents: `torrent-create`, `torrent-info`, `torrent-get` (`--proxy socks5://…` required, as in the app; `--files`, `--seconds`, `--use-existing`).
- Separate program, unlike MDM's launcher that runs the app's own program with `--cli`.
- Not ported: `playlist-get`, `--dns`, `--adapter` and `--direct` (no adapters or debrid yet), MDM's `.mdm.json` state files (the list replaces them).

### Step 8 notes (debrid services): done 2026-11-10

Five commits (8a the engine's hook, 8b the services, 8c torrents through them, 8d the keychain, 8e the app).

**Engine** (`internal/engine/unlock.go`)
- `Config.Unlockers` gives a link its service account, asked on every attempt (start, resume, retry). The direct link is fetched without the host's headers (cookies, logins), capped at the service's connections (Real-Debrid's `chunks`), and asked for again when it dies mid-download (401/403/404/410), keeping the bytes. Resumes through a service compare the size only (each unlock may name another server, with its own ETag).
- The direct link is never stored, shown or logged: `URL` and `FinalURL` keep the host's link, and errors from net/http are scrubbed (`hideLink`). The Add URL preview unlocks too.
- Refusals are `*engine.ServiceError` (`Fault`: sign-in, not-premium, host-down, …), retried after the service's wait when it is within the retry policy (too many requests: 1 min, too many active: 2 min); host or service down (5 min) fails the download.

**Services** (`internal/debrid`, tests against `internal/debrid/debridtest`, a port of the lab server's fakes)
- Real-Debrid (pasted token, or the device flow with its open-source client id and token refresh), AllDebrid (key, or PIN; delayed links polled), Premiumize (API key only). Only their documented APIs; tokens in the `Authorization` header only.
- Accounts in priority order: a link goes to the first whose service lists its host (or the service's own domain). While the host lists are being read, links wait for them (up to 30 s) instead of going to the host.
- Each service stays under half its published request budget and pauses after 429.
- Torrents (`RunTorrent`): the magnet goes to the first account, the service fetches it, its files come over HTTPS through the app's downloads, then it is removed from the account's list. No peer connects to this computer. A path that climbs out of the folder refuses the whole torrent before anything is written.

**Keychain** (`internal/platform/keychain*.go`)
- Windows Credential Manager (generic credentials, this user, this computer) and the Linux Secret Service over D-Bus (`godbus/dbus/v5`), whose own prompt unlocks a locked keyring. Elsewhere, or without a Secret Service, sign-ins last until the app closes.

**App** (`debrid_app.go`, `debrid_torrents_app.go`)
- Settings key `debrid.settings` (accounts: id, service, user, on/off; "torrents through the account"); no secret in it. Sign-ins in the keychain as `Yellow Flash/debrid/<id>`.
- Bindings: `DebridServices`, `GetDebridSettings`, `SaveDebridSettings`, `AddDebridToken`, `StartDebridSignIn`/`GetDebridSignIn`/`CancelDebridSignIn` (event `debrid:signin`), `RemoveDebridAccount`, `CheckDebridAccount`, `RefreshDebridHosts`; `SendTorrentToDebrid`, `ListDebridTorrents`, `StopDebridTorrent`, `ResumeDebridTorrent`, `RemoveDebridTorrent` (event `debrid:torrents`). Host lists are read at start and every 12 h; the services are asked through the connection settings' proxy and DNS.
- With "torrents through the account" on, a magnet or `.torrent` sent to the app (command line, browser, remote control) goes to the account; no SOCKS5 proxy is needed. Its list is `<data>/debrid/torrents.json`; one the app was running continues at the next start.
- Not ported: the UI (Options → Debrid, the torrent rows), per-download "through …" choice in File Properties, the event-log window (notes go to the log and the torrent's own notes).
- Verified on Windows (the real Credential Manager included). The Linux keychain is compiled and vetted; its live test and the `-race` runs wait for the VM.

**Archives** (`internal/extract`, `extract_app.go`; step 8f)
- In process: zip (stored, Deflate, byte-split `.zip.001`), tar, `.tar.gz`, `.tar.xz`, a single `.gz`/`.xz` (`github.com/ulikunitz/xz`). Through an installed 7-Zip, run as its own program: 7z (split too), RAR, and the zips Go's reader can't open (encrypted, Deflate64). MDM decodes AES and Deflate64 zips itself; Go's `archive/zip` can't, so those need 7-Zip here.
- Every entry's path is checked (no `..`, drive or stream name; nothing written through a link; links and devices left out), with limits (200 × the archive or 20 GiB, no more than the disk's free space; 100 000 entries; 64 levels) and overlapping zip entries refused. Everything goes into a staging folder first, then into place without overwriting ("smart" / always / never a folder named after the archive).
- The app extracts an archive when its download (a split one's last volume) completes, with Options → Archives on (`extract.settings`: auto, into, deleteArchive, sevenZip), or by hand (`ExtractDownload(id, password)`); "extract:done" says how (a password needed, 7-Zip missing, …). Each file gets the archive's Mark of the Web (`platform.MarkFromInternet`; `user.xdg.origin.url` on Linux); the archive may go to the Recycle Bin / Trash (`platform.Trash`).
- Not ported: Link grabber packages (their own extract and password choices), the password and "7-Zip needed" windows (UI).

**Server location** (`internal/geo`, `network/server.go`, `geo_app.go`; step 8g)
- Each download keeps what it learnt of its server (`Download.Server`): the address that answered, the quickest TCP connect as the round trip, whether a proxy carried it, and the CDN its headers name.
- `geo.Lookup` places the address in a country from DB-IP's IP to Country Lite table (CC BY 4.0, "IP Geolocation by DB-IP"), on this computer only; flags from flag-icons (MIT). `ServerLocation(id)` and `FlagsPicture()` are the window's; DB-IP's credit is in the answer.

**Crash reports** (`internal/crash`, `crash_app.go`; step 8h)
- MDM's out-of-process minidumps have no Go equivalent; instead `debug.SetCrashOutput` (installed in `main.go` right after the instance lock) makes the runtime write an unrecovered panic's or a fatal error's stacks to `<data>/crashes/pending.log`, and the next start makes it a report (`<kind>-<time>.txt`, with the raw `.log`). A fatal error's own message line goes to stderr only, so its report says where from the stack.
- The app and the engine log to `<data>/logs/yellowflash.log` (rolled at 2 MiB, 3 kept; `YELLOWFLASH_LOG=debug`); a report takes its last 150 lines. The home folder shows as `~`.
- Bindings: `LastCrashReport` (once), `ListCrashReports`, `CrashIssueURL` (GitHub's Crash report form, `.github/ISSUE_TEMPLATE/crash.yml`, filled in and cut to fit; the user submits it), `FindCrashIssue` (sends only the signature, when asked), `DeleteCrashReport`, `ShowCrashReports`.
- Not ported: freeze detection (MDM's window watchdog; a Wails webview gives the Go side no equivalent), symbol fetching and `mdm-symbolicate` (Go's stacks are named already).

Not ported: `crates/app` (their egui desktop UI), since Yellow Flash's UI is Wails + React. Port
its *behaviour* (queues, scheduler, settings) into Go services and the React UI as each feature
is needed.

Queues and the scheduler (previously the next task) live in their app crate. Step 1 did not
include them; port them alongside step 2, reading `crates/app/src/` and
`docs/queues-and-scheduler.md`. Once the queue runner exists, `/s` and `/a … queue` should use it.
