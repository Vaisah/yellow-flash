<!-- Ported from Mister Download Manager, docs/browser-help.md. Copyright (C) 2026 Ahmed Nefzaoui and contributors; changes (C) 2026 the Yellow Flash contributors. SPDX-License-Identifier: GPL-3.0-or-later -->

# The browser and Yellow Flash aren't connected

When the browser can't hand a download to Yellow Flash, it keeps the download itself, so nothing is lost. The extension's toolbar button then shows a red **!**. Click it to see what's wrong; **How to fix it** brings you to the matching section below.

The extension talks to a small helper program, `yellowflash-nmhost` (`yellowflash-nmhost.exe` on Windows), which sits beside Yellow Flash and hands each download to it.

## Adding the extension

Until the extension is listed in the browsers' stores, load it from the `extension` folder:

- **Chrome, Edge, Brave, Opera, Vivaldi:** open the extensions page (`chrome://extensions`, `edge://extensions`, `brave://extensions`), turn on **Developer mode**, click **Load unpacked** and pick the `extension` folder. Its ID is `fhlfnlgbgkmofdmohiohgmiojleegaah`.
- **Firefox and its forks:** copy the `extension` folder, rename `manifest.firefox.json` to `manifest.json` in the copy (replacing the Chromium one), then open `about:debugging`, **This Firefox → Load Temporary Add-on...** and pick that `manifest.json`. See [Firefox forgets the extension after a restart](#firefox-temporary).

<a id="not-installed"></a>
## Yellow Flash isn't installed, or browser integration isn't set up

The browser can't find the helper program.

1. Register it: run `yellowflash-nmhost --register` from Yellow Flash's folder (for everyone on the computer, as administrator or root: `yellowflash-nmhost --register --all-users`).
2. A portable copy (one with a `data` folder beside it) can't be connected: it writes nothing outside its own folder. Use an installed copy for browser integration.
3. On Linux and macOS the helper writes its file into each installed browser's own folder: register again after installing another browser.
4. Restart the browser after registering.
5. From inside an AppImage the helper refuses to register itself (the AppImage's files are gone once it quits): copy `yellowflash-nmhost` somewhere lasting, put the app's path in a file named `app-path` beside it, and register that copy.

<a id="not-running"></a>
## Yellow Flash didn't start in time

When Yellow Flash isn't running, the helper starts it for each download and waits for it. It waited about 20 seconds this time and gave up.

- Start Yellow Flash yourself and try the download again.
- A first start, a slow disk, or an antivirus scanning Yellow Flash can take that long. Next time it is usually quick.
- With a VPN or proxy program, allow connections to `127.0.0.1` (your own computer): the helper reaches Yellow Flash through one.

<a id="host-failed"></a>
## The connection to Yellow Flash failed

The browser started the helper, but it stopped before answering. Security software is the usual cause.

- Allow `yellowflash-nmhost.exe` (in Yellow Flash's folder) in your antivirus or anti-malware program.
- Reinstall Yellow Flash to restore its files, then register the helper again.
- On Linux, with Firefox or Chromium installed as a Snap, see [Flatpak and Snap browsers](#linux-sandbox).

<a id="forbidden"></a>
## Yellow Flash doesn't accept this copy of the extension

The helper serves only Yellow Flash's own extension (ID `fhlfnlgbgkmofdmohiohgmiojleegaah`, or the Firefox add-on `integration@yellowflash`).

- **Two copies installed?** Open the browser's extensions page and keep only one **Yellow Flash**.
- **A copy from elsewhere, or another download manager's extension?** Remove it and load the one in Yellow Flash's `extension` folder ([Adding the extension](#adding-the-extension)).

<a id="update-app"></a>
## The extension is newer than Yellow Flash

Downloads still work, but some of the extension's features need a newer Yellow Flash. Update Yellow Flash.

<a id="too-large"></a>
## A download's details were too large

A download carries its site's cookies (and a form's data) to Yellow Flash. A few sites set so many that together they exceed what Yellow Flash accepts from the browser (1 MB). The browser kept this one. Signing out and back in to the site usually trims them.

<a id="options"></a>
## Downloads stay with the browser although Yellow Flash is connected

The extension is connected, but Yellow Flash's browser settings leave this download to the browser. Check that:

1. Browser integration is on, and your browser is among those whose downloads are taken.
2. The file's type is in the list of file types taken.
3. The site isn't among the sites, nor the address among the addresses, left to the browser.
4. The file isn't smaller than the smallest size taken.

Also check the extension's toolbar button: with **Take downloads from this browser** off, everything stays with the browser. Each download left to the browser is noted, with why, in `logs/yellowflash.log` in Yellow Flash's data folder (a private window's without its site or name).

<a id="opened"></a>
## The browser opens PDFs and music instead of sending them

Yellow Flash also takes the file types the browser would show or play itself (a PDF, an MP3) when they're in its list of file types and taking them is on. This needs a Chromium browser (Chrome, Edge, Brave, Opera, Vivaldi) version 128 or later, or Firefox. Hold **Alt** while clicking a link to leave it to the browser.

<a id="store-youtube"></a>
## No Download video button on YouTube

A copy of the extension from a browser's store may leave YouTube alone, as the store asks. The copy in Yellow Flash's `extension` folder shows the button there ([Adding the extension](#adding-the-extension)).

<a id="site-access"></a>
## The extension can't read this site

In Chrome, Edge and Brave you can limit an extension to some sites (right-click its toolbar button → **This can read and change site data**). On a site it may not read, the extension can't pass your sign-in on, so it leaves the download to the browser. Choose **On all sites** to let Yellow Flash take downloads everywhere.

<a id="firefox-temporary"></a>
## Firefox forgets the extension after a restart

An add-on loaded with **Load Temporary Add-on** (from `about:debugging`) is removed when Firefox closes. Load it again after each restart until the add-on is listed on addons.mozilla.org.

<a id="linux-sandbox"></a>
## Linux: Flatpak and Snap browsers

Browsers installed as a Flatpak or a Snap run in a sandbox, which decides whether they can reach Yellow Flash.

- **Firefox as a Snap** (Ubuntu's own Firefox) reaches the helper through the desktop, which reads the browser files in your home folder and those registered for everyone. The first time the extension calls Yellow Flash, the desktop asks **Allow Firefox to start WebExtension backend?**: choose **Allow**. After **Don't allow**, the extension says the connection failed and the desktop doesn't ask again; to be asked again, run this in a terminal, then use the extension again:

  `gdbus call --session --dest org.freedesktop.impl.portal.PermissionStore --object-path /org/freedesktop/impl/portal/PermissionStore --method org.freedesktop.impl.portal.PermissionStore.Delete webextensions com.yellowflash.host`
- **Chromium as a Snap** (Ubuntu's Chromium) starts the helper inside its sandbox, where it can't reach Yellow Flash. Use Firefox, or Google Chrome from its own `.deb`.
- **Flatpak browsers** can't start the helper. Install the browser from your distribution's packages (or the browser maker's own `.deb`, `.rpm`, tarball or AppImage) to use browser integration.
