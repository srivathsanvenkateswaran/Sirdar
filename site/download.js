/*
 * Sirdar landing page — the download buttons know the visitor's platform.
 *
 * The markup is already right without this file: the hero button says
 * "Download" and every button opens the releases page. This script only
 * upgrades it, in two independent steps:
 *
 *   1. Detection, synchronous. The hero label becomes "Download for macOS"
 *      (or Windows, or Linux), the matching card in the download band moves
 *      to the front, and a macOS visitor is shown the Intel alternative.
 *      Unknown and mobile platforms change nothing.
 *
 *   2. Resolution, asynchronous. One request to the GitHub releases API,
 *      cached in sessionStorage for an hour, tells us the latest tag and its
 *      asset names. Each button whose `<os>_<arch>` asset is in that list gets
 *      the github.com download URL for it. On 404 (no release yet), a
 *      rate-limit, or no network, nothing changes: the static hrefs stand.
 *
 * The asset URL is built here from the tag and the asset name, never copied
 * from the response or the page, so the only host a button can ever point at
 * is github.com/srivathsanvenkateswaran/Sirdar. The four suffixes are the ones
 * .github/workflows/release.yml uploads; a name it does not produce is never
 * referenced.
 *
 * Testing: `?os=mac|windows|linux|unknown` overrides detection.
 */
(function () {
  "use strict";

  var REPO = "srivathsanvenkateswaran/Sirdar";
  var API = "https://api.github.com/repos/" + REPO + "/releases/latest";
  var DOWNLOAD_BASE = "https://github.com/" + REPO + "/releases/download/";
  var CACHE_KEY = "sirdar.release.latest";
  var CACHE_TTL_MS = 60 * 60 * 1000;

  /* The label the hero button takes, and the asset the button resolves. macOS
     defaults to Apple silicon because the chip cannot be read from a browser;
     the Intel link is the visible alternative. */
  var PLATFORMS = {
    mac: { label: "macOS", asset: "darwin_arm64" },
    windows: { label: "Windows", asset: "windows_x64" },
    linux: { label: "Linux", asset: "linux_x64" }
  };

  /* Detection ------------------------------------------------------------- */

  function normalise(value) {
    var v = String(value || "").toLowerCase();
    if (/^(mac|macos|darwin|osx)$/.test(v)) { return "mac"; }
    if (/^(windows|win|win32|win64)$/.test(v)) { return "windows"; }
    if (v === "linux") { return "linux"; }
    return "unknown";
  }

  function fromQuery() {
    var m = /[?&]os=([^&#]*)/.exec(window.location.search);
    return m ? normalise(decodeURIComponent(m[1])) : null;
  }

  /* navigator.userAgentData.platform is the honest signal where it exists
     (Chromium): "macOS", "Windows", "Linux", "Android", "Chrome OS", "iOS". */
  function fromClientHints() {
    var uad = navigator.userAgentData;
    if (!uad || typeof uad.platform !== "string" || !uad.platform) { return null; }
    if (uad.mobile) { return "unknown"; }
    var p = uad.platform.toLowerCase();
    if (p === "macos") { return "mac"; }
    if (p === "windows") { return "windows"; }
    if (p === "linux") { return "linux"; }
    return "unknown";
  }

  /* Everything else reads the strings. Phones and tablets go first because an
     iPad reports "Macintosh" and Android reports "Linux". */
  function fromStrings() {
    var ua = navigator.userAgent || "";
    var plat = navigator.platform || "";
    if (/iPhone|iPad|iPod|Android|Mobile|CrOS/i.test(ua)) { return "unknown"; }
    if (/Mac/i.test(plat) && navigator.maxTouchPoints > 1) { return "unknown"; }
    if (/Mac/i.test(plat) || /Macintosh|Mac OS X/i.test(ua)) { return "mac"; }
    if (/Win/i.test(plat) || /Windows/i.test(ua)) { return "windows"; }
    if (/Linux|X11/i.test(plat) || /Linux|X11/i.test(ua)) { return "linux"; }
    return "unknown";
  }

  function detect() {
    var q = fromQuery();
    if (q !== null) { return q; }
    var hint = fromClientHints();
    if (hint !== null) { return hint; }
    return fromStrings();
  }

  /* Step 1: name the platform ------------------------------------------- */

  function present(os) {
    var info = PLATFORMS[os];
    if (!info) { return; }

    var hero = document.getElementById("dl-hero");
    if (hero) {
      hero.textContent = "Download for " + info.label;
      hero.setAttribute("data-dl-asset", info.asset);
    }

    var alt = document.getElementById("dl-hero-alt");
    if (alt && os === "mac") { alt.hidden = false; }

    var list = document.getElementById("plats");
    var card = list && list.querySelector('[data-os="' + os + '"]');
    if (card) {
      if (card !== list.firstElementChild) { list.insertBefore(card, list.firstElementChild); }
      card.setAttribute("data-detected", "");
      var tag = card.querySelector(".plat-tag");
      if (tag) { tag.hidden = false; }
    }
  }

  /* Step 2: resolve the assets ------------------------------------------ */

  function readCache() {
    try {
      var raw = window.sessionStorage.getItem(CACHE_KEY);
      if (!raw) { return null; }
      var v = JSON.parse(raw);
      if (!v || typeof v.at !== "number" || Date.now() - v.at > CACHE_TTL_MS) { return null; }
      if (!Array.isArray(v.assets)) { return null; }
      return v;
    } catch (err) {
      return null;
    }
  }

  function writeCache(release) {
    try {
      release.at = Date.now();
      window.sessionStorage.setItem(CACHE_KEY, JSON.stringify(release));
    } catch (err) {
      /* Storage refused (private mode, quota): the fetch simply repeats next load. */
    }
  }

  /* Resolves to {tag, assets} for a published release, {tag: null, assets: []}
     for a 404 (also cached, so an empty repository is not asked once per
     page), or null when nothing can be said (rate-limit, offline, bad body),
     which is not cached so a later load can try again. */
  function fetchLatest() {
    var hit = readCache();
    if (hit) { return Promise.resolve(hit); }
    if (typeof window.fetch !== "function") { return Promise.resolve(null); }

    return window.fetch(API, { headers: { Accept: "application/vnd.github+json" } })
      .then(function (res) {
        if (res.status === 404) {
          var none = { tag: null, assets: [] };
          writeCache(none);
          return none;
        }
        if (!res.ok) { return null; }
        return res.json().then(function (body) {
          if (!body || typeof body.tag_name !== "string") { return null; }
          var release = { tag: body.tag_name, assets: [] };
          (Array.isArray(body.assets) ? body.assets : []).forEach(function (a) {
            if (a && typeof a.name === "string") { release.assets.push(a.name); }
          });
          writeCache(release);
          return release;
        });
      })
      .catch(function () { return null; });
  }

  /* A tag is what `git tag vX.Y.Z` produces and an asset name is what the
     workflow zips; anything outside those alphabets is refused rather than
     placed in a URL. */
  var SAFE_TAG = /^v?[0-9A-Za-z][0-9A-Za-z._-]*$/;
  var SAFE_NAME = /^[0-9A-Za-z][0-9A-Za-z._-]*$/;

  function assetURL(release, suffix) {
    if (!release || !release.tag || !SAFE_TAG.test(release.tag)) { return null; }
    var name = "sirdar-desktop_" + release.tag + "_" + suffix + ".zip";
    if (!SAFE_NAME.test(name) || release.assets.indexOf(name) === -1) { return null; }
    return DOWNLOAD_BASE + encodeURIComponent(release.tag) + "/" + encodeURIComponent(name);
  }

  function resolve(release) {
    if (!release || !release.tag) { return; }
    var linked = 0;
    var links = document.querySelectorAll("[data-dl-asset]");
    Array.prototype.forEach.call(links, function (a) {
      var url = assetURL(release, a.getAttribute("data-dl-asset"));
      if (url) { a.href = url; linked++; }
    });
    if (!linked) { return; }

    Array.prototype.forEach.call(document.querySelectorAll("[data-dl-when]"), function (p) {
      p.hidden = p.getAttribute("data-dl-when") !== "some";
    });
    Array.prototype.forEach.call(document.querySelectorAll("[data-dl-tag]"), function (s) {
      s.textContent = release.tag;
    });
  }

  present(detect());
  fetchLatest().then(resolve);
})();
