# Development progress

## Current phase

**Phase 2 (desktop application, branded Yellow Flash) is complete.** Phase 1 (the core engine) is complete, and it gained two fixes during Phase 2 testing, listed below. The next phase is Phase 3, download management.

## Phase 2: Yellow Flash desktop app (2026-11-06)

The layout follows a classic download manager without copying IDM: a toolbar, a status and category sidebar, a sortable download table, a details panel with a live segment map, a status bar with total speed, and dialogs for Add URL, Settings and Remove.

| Feature | Where |
|---|---|
| Add URL dialog that previews the file before downloading (name, size, type, resume support), pre-fills from the clipboard, and blocks web pages with an explanation | `frontend/src/components/Dialogs.tsx`, `App.InspectLink`, `Manager.Inspect` |
| Google, Facebook and YouTube redirect links unwrapped to the real file | `network/unwrap.go` |
| Live speed, ETA and active connections; segment map | `DownloadTable.tsx`, `DetailsPanel.tsx`, `App.GetSegments` |
| Pause, resume, cancel, retry and remove (optionally deleting the file); Resume all and Pause all | `App.*`, `Service.ResumeAll/PauseAll` |
| Open file and Show in folder, behind the platform interface (Windows: rundll32/explorer, Linux: xdg-open) | `internal/platform/shell*.go` |
| Plain-language error explanations (403, 404, 429, web page, timeouts, and others) | `frontend/src/lib/errors.ts` |
| Settings persisted in SQLite (folder, connections, auto-start, clipboard) | `App.GetSettings/SaveSettings` |
| Keyboard shortcuts (Ctrl+N, Ctrl+F, Space, Delete, Enter, arrow keys), right-click menu, drag-and-drop of links | `App.tsx`, `DownloadTable.tsx` |
| Branding: logo in the UI, favicon and `.exe` icon (`build/appicon.png`); data folder `%APPDATA%\Yellow Flash`, migrated from the old "Download Manager" folder | `app.go`, `main.go`, `wails.json` |

**Engine fixes found while testing against real servers:**
- **Bogus HEAD length.** YouTube answers HEAD with `Content-Length: 0` and then streams HTML, so watch pages "completed" as empty files. The GET's length now wins, and HTML responses are rejected with `ErrWebPage`. Downloads without an extension get one from the Content-Type.
- **Server connection limits.** Hetzner answered 8 connections with 429, and one segment exhausting its retries failed the whole download. Throttled workers (429 or 503) now hand their segment back and stop while other workers are alive, so concurrency adapts to what the server allows. Integration test: `TestAdaptsToServerConnectionLimit`.

**Testing:**
- The UI was checked in Chrome against a dev-only mock backend (`src/dev/mockBackend.ts`, excluded from production builds).
- It was then driven end to end through `wails dev` against the real Go backend:
  - Inspected and downloaded a real 100 MB file. The server throttled it, and the engine adapted from 8 to 2 connections.
  - Paused and resumed the download, which continued from 64 MB.
  - The final file was exactly 104,857,600 bytes, and 6 of 6 byte ranges matched fresh server reads.
- `go test ./...` passes, and `go test -race` passes on Linux (Docker, 2 runs).
- `wails build` produces `build/bin/Yellow Flash.exe`.

**Known limitations:**
- Video-site pages (YouTube) cannot be downloaded until media capture arrives with the browser extension (Phase 4).
- When a server limits connections, the limit is relearned on every resume rather than remembered per host.
- Some public test servers (for example proof.ovh.net) block an IP after heavy multi-connection use.

## Phase 1 summary

## Completed milestones (Phase 1)

| Milestone | Where | Verified by |
|---|---|---|
| Download and Segment model; int64 offsets | `internal/domain/download` | `model_test.go` |
| State machine with an explicit transition table and a crash-only `MarkInterrupted` | `domain/download/status.go` | All 81 transition pairs are tested |
| The Manager is the only code that changes state (always `Transition`, under one mutex) | `engine/manager.go`, `executor.go` | Race detector, lifecycle tests |
| HEAD probe with GET fallback, Content-Length/Type/Disposition, ETag/Last-Modified | `infrastructure/network/probe.go` | `network_test.go`, integration 4 |
| Range support confirmed only by `bytes=0-0` → 206 + valid Content-Range | `probe.go`, `range.go` | `TestProbeScenarios`, integration 3/5/6 |
| Overflow-safe allocator with inclusive ends and a minimum segment size | `engine/allocator.go` | `TestAllocateTilesExactly` (including `MaxInt64`) |
| Bounded worker pool (errgroup): first permanent error cancels the siblings | `engine/worker.go` | Integration 2/5/16 |
| `WriteAt` writer that cannot write past its segment's range | `engine/writer.go` | `TestRangeWriterCannotEscapeItsRange` |
| Preallocated `name.part` file, 0600 while downloading | `infrastructure/filesystem` | `filesystem_test.go` |
| Single-stream fallback, unknown length, resume only when proven | `engine/worker.go` | Integration 3/6/11/13 |
| Pause/resume with fsync before progress is persisted; partial-segment resume | `worker.go`, `resume.go` | Integration 14/18 |
| Changed remote representation → restart from zero (never mixes bytes) | `executor.go`, `metadata.go` | Integration 15 |
| Retry policy: backoff, jitter, Retry-After, resets/timeouts/HTTP classified | `engine/retry.go`, `network/errors_*.go` | `TestClassify` (real errnos), integration 9/10/11/16 |
| Idle-read timeout for stalled bodies | `worker.go` (`idleReader`) | `TestStalledConnectionTimesOut` |
| Progress: EWMA speed, average speed, ETA only when known, active connections, throttled events | `engine/progress.go` | `TestTrackerSpeedAndETA`, integration 2 |
| Verification (size and advertised digest) before an atomic, no-overwrite publish | `verifier.go`, `filesystem/writer.go` | Integration 20, `TestPublish*` |
| SQLite adapter: DSN pragmas (FK, WAL, busy timeout), transactional versioned migrations, indexes | `persistence/sqlite` | `store_test.go` (including a v1→v2 upgrade) |
| Credentials never persisted, logged, or emitted | `domain`, `repositories/downloads.go`, `engine` | Store test, integration "credentials" |
| Crash recovery: active → PAUSED, segments clamped to the temp file, revalidated on resume | `manager.go` `Recover`, `resume.go` | Integration 19a/19b |
| DownloadService on a `DownloadPort` interface | `internal/application/download` | `service_test.go` (fake port) |
| SSRF guard at dial time (covers redirects and DNS rebinding); URL and filename validation | `network/client.go`, `filesystem/paths.go` | `TestPrivateDestinationsBlockedAtDialTime`, integration |
| CLI runnable without a UI | `cmd/engine-test` | Manual smoke test (below) |

### The 20 required integration scenarios (`tests/integration/download_test.go`)

1. Normal download
2. Segmented download
3. No range support
4. HEAD unsupported
5. Invalid Content-Range
6. 200 instead of 206
7. 416 (plus an empty file)
8. 404
9. 429 with Retry-After
10. 503: transient and permanent
11. Connection reset: segmented and stream (plus a stalled connection)
12. Redirects, plus an unsafe redirect
13. Unknown content length
14. Pause and resume
15. ETag change
16. Worker failure and retry
17. Concurrent downloads
18. Shutdown mid-download
19. Crash recovery: a real abandoned process, plus clamping of recorded progress
20. Integrity and final size, checked by SHA-256 in every test

Additional tests cover: auto-rename on a name clash, cancel discarding partial data, retry/delete keeping the completed file, Content-Disposition filenames, credentials, the default SSRF block, rejection of unsafe input, and goroutine leaks.

## Test results (2026-11-06)

| Check | Result |
|---|---|
| `gofmt -l` | Clean |
| `go vet ./...` (Windows) and `GOOS=linux go vet` | Clean |
| `go test ./...` on Windows/amd64 | Pass: 68 test functions, 29 of them integration tests. The integration suite passed 5 consecutive runs. |
| `go test -race` on Linux/amd64 (Docker `golang:1.26`, Go 1.26.10), all packages | Pass, no races. Engine plus integration repeated 3 more times under `-race`: pass. |
| Cross-compile `./internal/... ./cmd/...` | windows/amd64, windows/arm64, linux/amd64, linux/arm64: all OK |
| Wails root package `go build .` (Windows) | OK. The Linux Wails build needs GTK/WebKit; it was not rebuilt in this phase. |
| CLI smoke test | 20 MiB over 8 connections from a local `http.FileServer`, SHA-256 identical to the source. `-list` reads the record back from SQLite. |

The host has no cgo or gcc, so `-race` cannot run natively on Windows; it runs on Linux in Docker instead.

## Architectural decisions

- **One state authority.** Only the Manager changes `Status`, under its mutex and through `Transition`. Workers report bytes through atomics and never touch the `Download` record.
- **Durability ordering.** Byte counters are read first, then the file is synced, then those counters are persisted. The stream path also serializes restarts with checkpoints, so a recorded offset never points at unsynced data.
- **Representation identity.** Size, a strong ETag, or Last-Modified must match before partial data is reused. `If-Range` is sent on every resumed request. A mismatch restarts the download (at most 2 restarts per run).
- **No overwrite.** Name clashes are resolved when the download is created, and again atomically at publish time with `name (n).ext` (decision confirmed with the user). Re-publishing the same hard-linked file after a crash is recognized, so it doesn't produce a duplicate.
- **HTTP/1.1 only, no transparent gzip.** Segments need independent TCP connections, and byte offsets must refer to the stored representation.
- **SSRF policy at dial time.** The check runs on the resolved IP for every connection, so it covers redirects and DNS rebinding. The engine default is restrictive. The CLI and the desktop shell opt in to private networks for URLs the user types; browser-originated URLs must keep the restriction (Phase 4).
- **Secrets.** Credential headers live only in a `platform.SecretStore`. Phase 1 has an in-memory implementation; OS keychains come in Phase 6.
- **Dependencies.** The only addition is `golang.org/x/sync` (errgroup). Everything else is the standard library, `uuid`, and `modernc.org/sqlite`.
- **Process deviation.** The plan called for splitting the old `engine.go` mechanically first. Most of its internals had to change for the fixes above, so the engine was rewritten directly into the new layout instead. Every scenario covered by the old tests was ported into the new unit and integration suites before the old files were removed. Backups of the removed files are in the session scratchpad only.

## Known limitations and bugs

- If the process dies after the final file is published but before `COMPLETED` is saved (a window of microseconds), the next run downloads the file again as `name (1).ext`.
- The memory secret store loses credentials on exit, so a credential-protected download must be re-authorised after a restart.
- A server that confirms ranges on the probe but ignores them for segment requests falls back to a single stream. That fallback discards partial progress each time the download is resumed.
- After a pause, the CLI exits with code 130 even when the download is resumable. This is intentional, so scripts can tell a paused download from a completed one.
- Segments are not split dynamically: a slow segment is not handed to an idle worker. This is a candidate for Phase 3 performance work.
- `Download.Error` can include the request URL, which may contain query tokens. The URL is already stored in the record.

## Outstanding tasks (later phases)

- **Phase 2:** Wails UI on top of `DownloadService`. The `downloads:updated` and `downloads:progress` events are already emitted by `app.go`.
- **Phase 3:** Queue scheduling (the `queues` table and `queue_id` already exist), global concurrency and bandwidth limits, dynamic segment splitting.
- **Phase 4:** Native-messaging bridge using a client with `AllowPrivate: false`.
- **Phase 5:** Proxies, custom headers UI, cookies.
- **Phase 6:** OS secret stores, installers, and rebuilding the Linux `.deb`.

## Next recommended task

Start Phase 3, download management:
- Queues with a maximum number of simultaneous downloads.
- Dynamic segment splitting, so idle connections take over the remainder of slow segments.
- A per-host connection limit learned from 429 responses.
- Bandwidth throttling and a scheduler.
