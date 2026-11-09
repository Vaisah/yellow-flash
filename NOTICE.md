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

## Go dependencies added for the port

| Module | Licence | Used for | Since |
|---|---|---|---|
| `golang.org/x/sys` | BSD-3-Clause | Windows file lock and foreground permission (`internal/platform/lock_windows.go`); it was already an indirect dependency via Wails | port step 1 (`internal/ipc`) |

## Third-party data and assets

- `internal/infrastructure/network/sites/testdata/drive-*.html`: Google Drive page samples (test
  data) from Mister Download Manager's `crates/engine/tests/data`, GPL-3.0-or-later.

Assets ported from Mister Download Manager keep their own licences, as listed in its
`ATTRIBUTION.md`. When one of them is added to this repository, list it here with its author,
licence and path, for example the DB-IP IP-to-Country Lite data (CC BY 4.0, which requires the
attribution "IP Geolocation by DB-IP").

The Yellow Flash logo (`frontend/src/assets/Yellow Flash Download Manager.png`) depicts a
character from a third-party franchise and is not covered by this project's licence. Replace it
before any public release.
