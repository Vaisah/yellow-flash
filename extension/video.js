// Ported from Mister Download Manager, extension/video.js
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; changes (C) 2026 the Yellow Flash contributors.
// Modified for Yellow Flash. SPDX-License-Identifier: GPL-3.0-or-later
// Yellow Flash's download button over videos (a content script, in every page and
// frame). Our own design: a small pill at the video's edge that appears while the pointer is
// over the video (or it has the keyboard's focus) and fades when the pointer rests or leaves.
// Clicking it opens the app's Download video window for this page (an embedded player's own
// address, which yt-dlp understands), with the video's own address when it has a plain one. It can be dragged along the video's edge, and stays where
// it was put on that site. Its menu hides it on this video, keeps it off the site (the app's
// Options → General → Download panels lists those sites), or opens those settings. The app
// decides where it may show at all (the native host: integration on, this browser captured,
// the button on, the site not excluded).
(() => {
  // One button per page: a copy cut off from the extension (after an update) gives way to the
  // new one.
  const prev = globalThis.__mdmVideoButton;
  if (prev && typeof prev === "object" && prev.alive()) return;
  if (prev && typeof prev.teardown === "function") prev.teardown();
  const ext = globalThis.browser ?? globalThis.chrome;

  // A video worth a button: big enough to be content, not a short muted looping clip (an
  // animated picture or a background). A sound player (<audio> with controls) gets one too.
  const MIN_W = 200;
  const MIN_H = 112;
  const MIN_AUDIO_H = 24;
  const CLIP_S = 15;
  // Shown this long after the pointer last moved over the video, kept while it is on the button.
  const IDLE_MS = 2600;
  const INSET = 10;
  const DRAG_PX = 4;
  // Our app icon (extension/icons/icon-48.png, as data: no extension file is exposed to pages).
  const LOGO = "data:image/png;base64,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";

  const t = (key, ...subs) => ext.i18n.getMessage(key, subs) || FALLBACK[key] || key;
  const FALLBACK = {
    video_download: "Download video",
    video_more: "More options",
    video_hide_here: "Hide on this video",
    video_settings: "Button settings...",
    video_sent: "Sent to Yellow Flash",
    video_failed: "Yellow Flash isn't installed or didn't start",
    video_off: "The download button is off on this site",
    video_drm: "Protected video: it can't be saved",
    video_unnamed: "Can't tell which video this is: open its own page and try there",
    video_drag: "Drag to move it along the video's edge",
  };
  // To the background. After the extension was updated or removed, this page's copy of the script
  // is cut off from it (calls throw at once): then it takes itself away.
  const send = (m) => {
    try {
      if (!ext.runtime?.id) throw new Error("gone");
      return ext.runtime.sendMessage(m).catch(() => ({ ok: false }));
    } catch {
      teardown();
      return Promise.resolve({ ok: false, gone: true });
    }
  };
  const host = location.hostname;
  // The address the app looks the video up by — the video's, not the page's. Most pages that play
  // a video are not the video's page: YouTube's home, a search, a channel or a playlist carries a
  // list in its address ("recommended", the results, the channel's videos), and yt-dlp reads such
  // an address as the list, so the app offered the whole feed instead of the video the button was
  // on. A watch page opened from a mix or a playlist has the same problem one level down
  // (…&list=…&index=…): its own page is watch?v=<id>, and nothing else.
  //
  // Null when the page plays a video but its DOM doesn't name it: YouTube's channel masthead player
  // has no video-id attribute, no link to the video inside the player and a blob: source. The page
  // itself never goes to the app then (a channel is a whole list to yt-dlp): the background asks
  // the player what it plays, in the page's own world (background.js playingVideo), and this
  // answer is only what it falls back on.
  function videoPage(v) {
    const watch = (id) =>
      /^[\w-]+$/.test(id || "") ? `https://www.youtube.com/watch?v=${id}` : null;
    try {
      const url = new URL(location.href);
      const name = url.hostname.replace(/^(www|m|music)\./, "");
      if (name === "youtu.be") {
        const id = url.pathname.split("/")[1];
        if (watch(id)) return `https://youtu.be/${id}`;
      }
      if (name === "youtube.com" || name === "youtube-nocookie.com") {
        // An embed's own page is the video's (…/embed/<id>).
        if (url.pathname.startsWith("/embed/")) {
          const id = watch(url.pathname.split("/")[2]);
          if (id) return id;
        }
        // A Short's or a live stream's own page (/shorts/<id>, /live/<id>). yt-dlp reads these as
        // the same one video as watch?v=<id>.
        const [, kind, pathId] = url.pathname.split("/");
        if (kind === "shorts" || kind === "live") {
          const id = watch(pathId);
          if (id) return id;
        }
        // The page is this video's own: a watch page, with or without a list on it.
        const own = watch(url.searchParams.get("v"));
        if (own && url.pathname.startsWith("/watch")) return own;
        const shown = watch(document.querySelector("ytd-watch-flexy")?.getAttribute("video-id"));
        if (shown && url.pathname.startsWith("/watch")) return shown;
        const player = watch(document.querySelector("#movie_player")?.getAttribute("video-id"));
        if (player) return player;
        // A player inside the card it belongs to (a feed, a search, a playlist): the card links to
        // the video. Only a container that names exactly one video counts — walking on into the
        // page's column would find some other card's link and send the wrong video, which is worse
        // than sending none.
        for (let node = v, i = 0; node && i < 12; i += 1, node = node.parentElement) {
          const ids = new Set();
          for (const a of node.querySelectorAll?.('a[href*="/watch?"], a[href*="youtu.be/"]') || []) {
            const link = new URL(a.href, location.href);
            const id = link.hostname.endsWith("youtu.be")
              ? link.pathname.split("/")[1]
              : link.searchParams.get("v");
            if (watch(id)) ids.add(id);
          }
          if (ids.size === 1) return watch([...ids][0]);
          if (ids.size > 1) break;
        }
        // A page that plays a video and names none: not the video's page, so not the app's.
        return null;
      }
    } catch {
      // Not an address this page can reason about: its own follows.
    }
    return location.href;
  }

  // The address this page first loaded at (`download`: a page that moves in place keeps its first
  // page's og:title).
  const landedAt = location.href;
  const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;

  // "Hide on this video": the video's address (players reuse one element for the next video),
  // else the element.
  const dismissed = new WeakSet();
  const dismissedSrc = new Set();
  const isDismissed = (v) => dismissed.has(v) || (v.currentSrc && dismissedSrc.has(v.currentSrc));
  let allowed = null; // asked once per page: may it show here?
  let place = { x: 1, y: 0 }; // where on the edge: fractions of the room, top right by default
  let video = null; // the video it is over
  let lastMove = 0;
  let pointer = { x: -1, y: -1 };
  let menuOpen = false;
  let dragging = null;
  let busy = false;
  let flashing = false;
  let raf = 0;
  let scan = 0;
  // Kept on screen while the keyboard is on it (not after a mouse click: that leaves focus too),
  // or on the video's player (its own controls).
  const keyboardOn = () =>
    Boolean(shadow.activeElement && shadow.activeElement.matches(":focus-visible")) || playerFocused();
  let focusedPlayer = null;
  const playerFocused = () =>
    Boolean(video && focusedPlayer && document.activeElement === focusedPlayer && focusedPlayer.contains(video));

  // ---- the button: a closed shadow tree the page's styles and scripts can't reach ----------
  const root = document.createElement("yf-video-button");
  // Each with !important: a page's own rules can't hide or move it.
  for (const [k, v] of Object.entries({
    all: "initial", position: "fixed", "z-index": "2147483647", left: "0", top: "0", display: "none",
    contain: "layout style", opacity: "1", visibility: "visible", "pointer-events": "auto",
  })) root.style.setProperty(k, v, "important");
  const shadow = root.attachShadow({ mode: "closed" });
  const CSS = `
:host { all: initial; }
* { box-sizing: border-box; }
/* A light, glossy panel that stands out over a dark video, with our icon: the classic download
   button's feel, made modern (rounded, crisp type, soft shadow and motion). */
.pill {
  display: flex; align-items: stretch; height: 34px; border-radius: 9px;
  background: linear-gradient(#ffffff, #f1f4f8 48%, #e3e8ef 52%, #eef2f6);
  color: #17324d; border: 1px solid rgba(23, 50, 77, .28);
  box-shadow: 0 4px 14px rgba(0, 0, 0, .32), 0 1px 0 rgba(255, 255, 255, .9) inset;
  font: 600 13px/1 "Segoe UI", system-ui, -apple-system, Roboto, sans-serif;
  opacity: 0; transform: translateY(-3px) scale(.98);
  transition: opacity .16s ease-out, transform .16s ease-out, filter .12s;
  user-select: none; -webkit-user-select: none; touch-action: none; white-space: nowrap;
}
.pill.shown { opacity: 1; transform: none; }
.pill.dragging { cursor: grabbing; box-shadow: 0 10px 28px rgba(0, 0, 0, .45); transition: none; }
.pill.dragging .go { cursor: grabbing; }
.pill.ok { background: linear-gradient(#f2fff6, #d4f3df); border-color: #2f9a5b; color: #135c33; }
.pill.err { background: linear-gradient(#fff4f4, #f6d6d6); border-color: #c24848; color: #7d1f1f; }
button {
  /* all: unset resets box-sizing too, past the rule above: without it a menu item's 100 % width
     plus its padding ran past the menu's edge (#81). */
  all: unset; box-sizing: border-box; display: flex; align-items: center; gap: 8px;
  color: inherit; cursor: pointer; font: inherit;
}
.go { padding: 0 12px 0 8px; border-radius: 8px 0 0 8px; cursor: grab; }
.more { padding: 0 9px; border-left: 1px solid rgba(23, 50, 77, .18); border-radius: 0 8px 8px 0; }
.go:hover, .more:hover { background: linear-gradient(rgba(214, 232, 255, .9), rgba(182, 212, 250, .9)); }
.go:active, .more:active { background: rgba(160, 196, 242, .9); }
button:focus-visible { outline: 2px solid #2a78d6; outline-offset: -2px; }
.go img { width: 22px; height: 22px; flex: none; filter: drop-shadow(0 1px 1px rgba(0, 0, 0, .25)); }
.more svg { width: 12px; height: 12px; color: #3b5876; }
.compact .label { display: none; }
.compact .go { padding: 0 6px; border-radius: 8px 0 0 8px; }
.menu {
  position: absolute; min-width: 210px; padding: 4px; border-radius: 9px;
  background: #fbfcfe; color: #17324d; border: 1px solid rgba(23, 50, 77, .22);
  box-shadow: 0 12px 32px rgba(0, 0, 0, .35);
  font: 400 13px/1.2 "Segoe UI", system-ui, -apple-system, Roboto, sans-serif;
  display: none;
}
.menu.open { display: block; }
.menu button { display: block; width: 100%; padding: 8px 12px; border-radius: 6px; cursor: default; }
.menu button:hover, .menu button:focus-visible { background: #dceafb; outline: none; }
.menu hr { border: 0; border-top: 1px solid rgba(23, 50, 77, .12); margin: 4px 2px; }
@media (prefers-reduced-motion: reduce) { .pill { transition: none; transform: none; } }
`;
  // A constructed sheet: a page whose Content-Security-Policy refuses inline styles can't strip
  // it (Firefox applies the page's policy to what a content script adds).
  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(CSS);
    shadow.adoptedStyleSheets = [sheet];
  } catch {
    const style = document.createElement("style");
    style.textContent = CSS;
    shadow.appendChild(style);
  }
  shadow.innerHTML += `
<div class="pill" part="pill">
  <button class="go" type="button">
    <img alt="" draggable="false">
    <span class="label"></span>
  </button>
  <button class="more" type="button" aria-haspopup="menu" aria-expanded="false">
    <svg viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6"
      stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 4.5 6 7.5l3-3"/></svg>
  </button>
</div>
<div class="menu" role="menu">
  <button role="menuitem" data-act="download"></button>
  <hr>
  <button role="menuitem" data-act="hide"></button>
  <button role="menuitem" data-act="never"></button>
  <hr>
  <button role="menuitem" data-act="settings"></button>
</div>`;
  shadow.querySelector(".go img").src = LOGO;
  const pill = shadow.querySelector(".pill");
  const go = shadow.querySelector(".go");
  const label = shadow.querySelector(".label");
  const more = shadow.querySelector(".more");
  const menu = shadow.querySelector(".menu");
  const item = (act) => menu.querySelector(`[data-act="${act}"]`);
  pill.dir = ext.i18n.getMessage("@@bidi_dir") || "ltr";
  menu.dir = pill.dir;
  const idleLabel = t("video_download");
  label.textContent = idleLabel;
  go.title = `${idleLabel} — ${t("video_drag")}`;
  go.setAttribute("aria-label", idleLabel);
  more.title = t("video_more");
  more.setAttribute("aria-label", t("video_more"));
  item("download").textContent = idleLabel;
  item("hide").textContent = t("video_hide_here");
  item("never").textContent = ext.i18n.getMessage("video_never_site", [host]) || `Never on ${host}`;
  item("settings").textContent = t("video_settings");

  // ---- which video ----------------------------------------------------------------------------
  const videos = document.getElementsByTagName("video");
  const audios = document.getElementsByTagName("audio");

  // Videos inside open shadow roots (players built as web components), looked for again every
  // few seconds at most.
  let shadowed = [];
  let shadowedAt = 0;
  function shadowVideos() {
    if (Date.now() - shadowedAt < 3000) return shadowed;
    shadowedAt = Date.now();
    const found = [];
    const walk = (root, depth) => {
      if (depth > 4) return;
      const w = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT);
      for (let n = w.nextNode(), seen = 0; n && seen < 20000; n = w.nextNode(), seen++) {
        if (n.shadowRoot) {
          found.push(...n.shadowRoot.querySelectorAll("video"));
          walk(n.shadowRoot, depth + 1);
        }
      }
    };
    walk(document.documentElement, 0);
    shadowed = found;
    return found;
  }

  function eligible(v) {
    if (!v.isConnected || isDismissed(v)) return false;
    const r = v.getBoundingClientRect();
    const audio = v instanceof HTMLAudioElement;
    if (audio ? !v.controls || r.width < MIN_W || r.height < MIN_AUDIO_H : r.width < MIN_W || r.height < MIN_H) {
      return false;
    }
    // A short muted loop without controls: a moving picture, not something to save.
    if (v.muted && v.loop && !v.controls && !(v.duration > CLIP_S)) return false;
    const s = getComputedStyle(v);
    return s.visibility !== "hidden" && s.display !== "none" && Number(s.opacity) > 0.05;
  }

  // The topmost eligible video under (x, y): players lay their controls over the video, so the
  // video itself rarely gets the pointer's events.
  function videoAt(x, y) {
    let best = null;
    for (const v of [...videos, ...audios, ...shadowVideos()]) {
      const r = v.getBoundingClientRect();
      if (x < r.left || x > r.right || y < r.top || y > r.bottom || !eligible(v)) continue;
      if (!best || r.width * r.height < best.r.width * best.r.height) best = { v, r };
    }
    return best && best.v;
  }

  // ---- placing it -------------------------------------------------------------------------
  // `place` is where it sits in the room the video leaves around it: x and y from 0 to 1, one of
  // them 0 or 1 (on an edge).
  function rects() {
    const r = video.getBoundingClientRect();
    const w = pill.offsetWidth || 150;
    const h = pill.offsetHeight || 32;
    return { r, w, h, roomX: Math.max(0, r.width - w - 2 * INSET), roomY: Math.max(0, r.height - h - 2 * INSET) };
  }

  function position() {
    if (!video) return;
    const { r, w, roomX, roomY } = rects();
    // A narrow video gets the icon only.
    pill.classList.toggle("compact", r.width < 360);
    const at = dragging ? dragging.at : place;
    const left = r.left + INSET + at.x * roomX;
    // In the part of the video on screen: one scrolled half out keeps its button in view.
    const h = pill.offsetHeight || 32;
    const visTop = Math.max(r.top, 0) + INSET;
    const visBottom = Math.min(r.bottom, innerHeight) - INSET - h;
    const top = Math.min(Math.max(r.top + INSET + at.y * roomY, visTop), Math.max(visTop, visBottom));
    root.style.setProperty("transform", `translate(${Math.round(left)}px, ${Math.round(top)}px)`, "important");
    if (menuOpen) placeMenu(top, left, w);
  }

  // The menu opens below the button, or above it near the bottom of the window; aligned with
  // whichever of its sides is nearer the video's middle.
  function placeMenu(top, left, w) {
    const h = menu.offsetHeight;
    const below = top + 36 + h < innerHeight;
    menu.style.top = below ? "36px" : `${-h - 4}px`;
    const { r } = rects();
    const rightHalf = left + w / 2 > r.left + r.width / 2;
    menu.style.left = rightHalf ? "" : "0";
    menu.style.right = rightHalf ? "0" : "";
  }

  // Snap a free spot (fractions, after a drag) to the nearest edge.
  function snap(at) {
    const d = { left: at.x, right: 1 - at.x, top: at.y, bottom: 1 - at.y };
    const edge = Object.keys(d).reduce((a, b) => (d[a] <= d[b] ? a : b));
    // Along the edge it stays where it was dropped, but close to a corner it goes into it.
    const c = (n) => (n < 0.08 ? 0 : n > 0.92 ? 1 : n);
    if (edge === "left") return { x: 0, y: c(at.y) };
    if (edge === "right") return { x: 1, y: c(at.y) };
    if (edge === "top") return { x: c(at.x), y: 0 };
    return { x: c(at.x), y: 1 };
  }

  // ---- showing and hiding ---------------------------------------------------------------------
  function attach() {
    // Over the video in fullscreen: inside the fullscreen element (a fullscreen <video> itself
    // can't hold it: then it hides).
    const fs = document.fullscreenElement;
    const parent = fs && fs !== video && fs.contains(video) ? fs : document.documentElement;
    if (root.parentNode !== parent) parent.appendChild(root);
    return !(fs && (fs === video || !fs.contains(video)));
  }

  function show(v) {
    if (video !== v) {
      video = v;
      closeMenu();
    }
    if (!attach()) return hide(true);
    root.style.setProperty("display", "block", "important");
    position();
    if (!pill.classList.contains("shown")) requestAnimationFrame(() => pill.classList.add("shown"));
    if (!raf) raf = requestAnimationFrame(tick);
  }

  // `now`: at once and whatever has the focus (dismissed, the video gone).
  function hide(now) {
    if (!now && (menuOpen || dragging || keyboardOn())) return;
    if (now) {
      closeMenu();
      shadow.activeElement?.blur();
    }
    pill.classList.remove("shown");
    cancelAnimationFrame(raf);
    raf = 0;
    const was = video;
    setTimeout(() => {
      if (!pill.classList.contains("shown") && video === was) root.style.setProperty("display", "none", "important");
    }, now || reduced ? 0 : 180);
  }

  // While shown: follow the video (scrolling, layout) and fade when the pointer rests.
  function tick() {
    raf = 0;
    if (!video || !video.isConnected || !eligible(video)) {
      video = null;
      return hide(true);
    }
    const r = video.getBoundingClientRect();
    const over =
      pointer.x >= r.left && pointer.x <= r.right && pointer.y >= r.top && pointer.y <= r.bottom;
    const onButton = root.matches(":hover");
    if (!onButton && !menuOpen && !dragging && !keyboardOn() && (!over || Date.now() - lastMove > IDLE_MS)) {
      return hide();
    }
    position();
    raf = requestAnimationFrame(tick);
  }

  // At most one look per frame, however fast the pointer moves.
  function onMove(e) {
    pointer = { x: e.clientX, y: e.clientY };
    lastMove = Date.now();
    if (!scan) scan = requestAnimationFrame(look);
  }

  function look() {
    scan = 0;
    if (dragging || allowed === false) return;
    const v = videoAt(pointer.x, pointer.y);
    if (!v) return;
    if (allowed === null) return void ask().then(() => allowed && look());
    show(v);
  }

  async function ask() {
    if (allowed !== null) return;
    allowed = false; // until answered: one question per page
    const r = await send({ type: "panel" });
    allowed = Boolean(r && r.on);
    if (allowed) {
      try {
        const { positions } = await ext.storage.local.get({ positions: {} });
        if (positions[host]) place = snap(positions[host]);
      } catch {
        // Default spot.
      }
    }
  }

  // ---- acting ---------------------------------------------------------------------------------
  async function download() {
    if (busy || !video) return;
    busy = true;
    try {
      closeMenu();
      // Encrypted (DRM): no tool can save it; say so rather than send it.
      if (video.mediaKeys) return flash("err", t("video_drm"));
      const src = /^https?:/i.test(video.currentSrc || "") ? video.currentSrc : "";
      // A page that moved in place keeps its first page's og:title; its document title follows.
      const og = location.href === landedAt ? document.querySelector('meta[property="og:title"]')?.content : "";
      const title = (og || document.title || "").trim();
      // The middle of the video: where the background asks the page's own player what it plays.
      const box = video.getBoundingClientRect();
      const at = { x: box.left + box.width / 2, y: box.top + box.height / 2 };
      // Asked now, not kept: one <video> plays many videos at the same address (a feed's previews).
      const r = await send({ type: "video", src, title, url: videoPage(video), at });
      if (r.gone) return;
      flash(r.ok ? "ok" : "err", t(r.ok ? "video_sent" : r.why === "options" ? "video_off" : r.why === "unnamed" ? "video_unnamed" : "video_failed"));
    } finally {
      busy = false;
    }
  }

  // A short confirmation on the button itself, then back to normal.
  function flash(kind, text) {
    flashing = true;
    pill.classList.remove("ok", "err");
    pill.classList.add(kind);
    label.textContent = text;
    lastMove = Date.now();
    position();
    setTimeout(() => {
      flashing = false;
      pill.classList.remove(kind);
      label.textContent = idleLabel;
      if (video) position();
    }, 2200);
  }

  function openMenu() {
    menuOpen = true;
    menu.classList.add("open");
    more.setAttribute("aria-expanded", "true");
    position();
    menu.querySelector("button").focus();
  }

  function closeMenu() {
    if (!menuOpen) return;
    menuOpen = false;
    menu.classList.remove("open");
    more.setAttribute("aria-expanded", "false");
  }

  menu.addEventListener("click", (e) => {
    if (!e.isTrusted) return;
    const act = e.target.closest("[data-act]")?.dataset.act;
    if (act) run(act);
  });

  async function run(act) {
    if (act === "download") return download();
    closeMenu();
    if (act === "hide") {
      dismissed.add(video);
      if (video.currentSrc) dismissedSrc.add(video.currentSrc);
      video = null;
      return hide(true);
    }
    if (act === "never") {
      allowed = false;
      video = null;
      hide(true);
      return send({ type: "hide-site" });
    }
    if (act === "settings") send({ type: "options" });
  }

  more.addEventListener("click", () => (menuOpen ? closeMenu() : openMenu()));

  // Keys: Escape closes the menu, arrows move through it.
  shadow.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && menuOpen) {
      closeMenu();
      more.focus();
      e.stopPropagation();
    } else if (menuOpen && (e.key === "Enter" || e.key === " ") && menu.contains(shadow.activeElement)) {
      // The focused command, whatever the browser does with a key on a button.
      run(shadow.activeElement.dataset.act);
      e.preventDefault();
    } else if (menuOpen && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
      const items = [...menu.querySelectorAll("button")];
      const i = items.indexOf(shadow.activeElement);
      items[(i + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
      e.preventDefault();
    }
  });

  // The main part: a click downloads, a drag moves it (and is not a click).
  // Only the user's own clicks and keys (never events a page script makes up).
  go.addEventListener("pointerdown", (e) => {
    if (e.button !== 0 || !video || !e.isTrusted) return;
    const { r, roomX, roomY } = rects();
    dragging = {
      id: e.pointerId,
      startX: e.clientX,
      startY: e.clientY,
      // Where in the button it was taken.
      dx: e.clientX - (r.left + INSET + place.x * roomX),
      dy: e.clientY - (r.top + INSET + place.y * roomY),
      at: place,
      moved: false,
    };
    go.setPointerCapture(e.pointerId);
    e.preventDefault();
  });
  go.addEventListener("pointermove", (e) => {
    if (!dragging || e.pointerId !== dragging.id) return;
    if (!dragging.moved && Math.hypot(e.clientX - dragging.startX, e.clientY - dragging.startY) < DRAG_PX) return;
    if (!dragging.moved) {
      dragging.moved = true;
      closeMenu();
      pill.classList.add("dragging");
    }
    const { r, roomX, roomY } = rects();
    const c = (n) => Math.min(1, Math.max(0, n));
    dragging.at = {
      x: roomX ? c((e.clientX - dragging.dx - r.left - INSET) / roomX) : 0,
      y: roomY ? c((e.clientY - dragging.dy - r.top - INSET) / roomY) : 0,
    };
    position();
  });
  const endDrag = (e) => {
    if (!dragging || e.pointerId !== dragging.id) return;
    const d = dragging;
    dragging = null;
    pill.classList.remove("dragging");
    if (!d.moved) {
      if (e.type === "pointerup") download();
      return;
    }
    place = snap(d.at);
    position();
    lastMove = Date.now();
    // Remembered per site, not in a private window (it would keep the site's name), and the
    // 200 sites used last only.
    if (ext.extension?.inIncognitoContext) return;
    try {
      ext.storage.local
        .get({ positions: {} })
        .then(({ positions }) => {
          delete positions[host];
          positions[host] = place;
          const sites = Object.keys(positions);
          for (const s of sites.slice(0, Math.max(0, sites.length - 200))) delete positions[s];
          return ext.storage.local.set({ positions });
        })
        .catch(() => {});
    } catch {
      teardown();
    }
  };
  go.addEventListener("pointerup", endDrag);
  go.addEventListener("pointercancel", endDrag);
  // Keyboard: Enter / Space (a real button's click without a pointer).
  go.addEventListener("click", (e) => {
    if (e.detail === 0 && e.isTrusted) download();
  });

  // ---- wiring -----------------------------------------------------------------------------------
  // The extension updated or removed under this page: the button and its listeners go (a fresh
  // copy of this script comes with the new extension).
  function teardown() {
    cancelAnimationFrame(raf);
    cancelAnimationFrame(scan);
    removeEventListener("pointermove", onMove, true);
    removeEventListener("keydown", onTab, true);
    root.remove();
    allowed = false;
    video = null;
  }
  globalThis.__mdmVideoButton = { alive: () => Boolean(ext.runtime?.id), teardown };
  addEventListener("pointermove", onMove, { capture: true, passive: true });
  addEventListener("pointerdown", (e) => {
    if (menuOpen && !e.composedPath().includes(root)) closeMenu();
  }, { capture: true, passive: true });
  // A video with the keyboard's focus, or a player around one (its controls), gets the button
  // too, kept while the focus stays there; Tab goes from there to the button.
  addEventListener("focusin", (e) => {
    if (!(e.target instanceof Element) || e.composedPath().includes(root)) return;
    const t = e.target;
    const v =
      t instanceof HTMLMediaElement ? t : [...t.querySelectorAll("video, audio")].find(eligible) || null;
    if (!v || !eligible(v)) return;
    focusedPlayer = t;
    ask().then(() => {
      if (!allowed) return;
      lastMove = Date.now();
      show(v);
    });
  }, true);
  // Tab on the focused player moves into the button first; Tab from the button goes back to
  // the player and on from there (its own controls next), as if the button sat at its start.
  let tabOn = 0;
  function onTab(e) {
    if (e.key !== "Tab" || e.shiftKey) return;
    // The Tab that just left the button: on from the player, as the browser goes.
    if (Date.now() - tabOn < 100) {
      tabOn = 0;
      return;
    }
    if (video && playerFocused() && root.style.getPropertyValue("display") === "block") {
      go.focus();
      e.preventDefault();
    }
  }
  addEventListener("keydown", onTab, true);
  go.addEventListener("keydown", (e) => {
    if (e.key === "Tab" && !e.shiftKey && focusedPlayer && focusedPlayer.isConnected) {
      tabOn = Date.now();
      focusedPlayer.focus();
    }
  });
  document.addEventListener("fullscreenchange", () => video && show(video));
  addEventListener("blur", () => hide());
})();
