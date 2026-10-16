// NusaShell service worker — PWA-grade offline shell (widget-conversation shell).
//
// Strategy: network-first with cache fallback for same-origin GETs. While the
// local server is up the browser always gets fresh assets (the server sends
// Cache-Control: no-cache, and network-first never serves a stale shell after
// an upgrade). When the server is down, every navigation and asset is served
// from this cache so the app boots and shows the full-screen offline state.
//
// Never cached: /ws (WebSocket handshake), /stream (agent and ACP SSE),
// POST /rpc/*, /local-file (large dynamic local files), /sounds
// (notification blips), /plugins/* (plugin UIs come from the plugin store
// and can be installed/uninstalled at runtime). Cloning an SSE body in
// this worker breaks Firefox (Failed to load /stream: unexpected error).
//
// The worker is registered from the shell directory — during development that
// is /.experimental/widget-conversation/sw.js, after promotion /sw.js — so all
// "./" paths below always resolve to the shell root in both layouts. Same rule
// as production: cache assets exactly as requested, "?v=" query included.

const CACHE = 'nusashell-shell-v19';

const NEVER_CACHE = [
  '/rpc/',
  '/ws',
  '/stream',
  '/local-file',
  '/sounds/',
  '/plugins/',
];

// Precache the navigation entry plus every lazily-imported module (they never
// appear in the runtime cache because nothing fetches them until the user
// clicks) and the shell chrome the first offline boot needs. Stylesheet and
// script URLs carry the same ?v= markers index.html requests — the query is
// part of the cache key, so these entries must track index.html version bumps.
// The shell is self-contained: js/, vendor/, fonts/, styles/ mirror the
// production frontend/ layout, so './js/*' entries resolve under the
// experimental mount and unchanged after promotion. The js/ entries below
// are the shell-init.js static import graph — the minimum module set a cold
// offline boot must evaluate; deeper graphs (markdown renderers, media-zoom,
// CodeMirror) warm through the runtime/message caches on first online load.
const PRECACHE = [
  './',
  './index.html',
  './manifest.webmanifest',
  './shell-init.js?v=5',
  './page/conversation/app.js?v=104',
  './page/conversation/room-menu.js?v=1',
  './page/conversation/context-inspector.js?v=3',
  './js/views/agent/subagent-prompt.js?v=1',
  './shell.css?v=62',
  './loading-layer.css?v=1',
  './overlays.css?v=5',
  './dock.css?v=27',
  './home.css?v=4',
  './skills.css?v=7',
  './plugins.css?v=8',
  './providers.css?v=12',
  './logs.css?v=8',
  './automation.css?v=4',
  './telemetry.css?v=2',
  './settings.css?v=3',
  './learning.css?v=7',
  './code-surface.css?v=1',
  './vendor/lucide.min.js',
  // Page controllers and the shared plugin launcher, pinned to the exact
  // ?v= specifiers page/conversation/app.js (+ page/plugins/app.js) import.
  './page/skills/app.js?v=2',
  './page/plugins/app.js?v=1',
  './page/learning/app.js?v=5',
  './page/providers/app.js?v=4',
  './page/logs/app.js?v=2',
  './page/automation/app.js?v=3',
  './js/plugin-window.js?v=1',
  './page/settings/app.js',
  './page/settings/model.js',
  // shell-init.js import graph (offline cold boot).
  './js/pwa.js',
  './js/pwa-install.js',
  './js/desktop-file-path.js',
  './js/rpc.js',
  './js/ui.js',
  './js/pairing.js',
  './js/pet-launcher.js',
  './js/shell-shortcuts.js',
  './js/pip.js',
  './js/views/agent/room-info.js',
  './vendor/slim-select/slimselect.es.js',
  './vendor/qrcode/qrcode.mjs',
  './fonts/fonts.css',
  './nusashell-mark.png',
];

self.addEventListener('install', (event) => {
  // Unlike production's cache.addAll, entries are added one by one with
  // allSettled: a URL that does not exist in the current layout (./js/pip.js
  // before promotion, a stale ?v= after an index.html bump) must not abort
  // installing an otherwise-working offline shell. Failures are warned, not
  // silent.
  event.waitUntil(
    caches.open(CACHE)
      .then((cache) => Promise.allSettled(PRECACHE.map((url) => cache.add(url))))
      .then((results) => {
        const failed = PRECACHE.filter((_, i) => results[i].status === 'rejected');
        if (failed.length) console.warn(`[sw] precache skipped: ${failed.join(', ')}`);
      })
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((key) => key !== CACHE).map((key) => caches.delete(key))))
      .then(() => self.clients.claim()),
  );
});

// The page tells us what it loaded (link/script URLs parsed from its own
// DOM, plus fonts referenced by stylesheets) right after the very first
// visit. Without this, the runtime cache would only fill on a *second*
// page load (SW-controlled fetches), leaving a first-visit user with an
// unstyled blank shell if the server dies before they ever reload.
self.addEventListener('message', (event) => {
  const urls = Array.isArray(event.data?.precache) ? event.data.precache : [];
  if (!urls.length) return;
  event.waitUntil(
    caches.open(CACHE).then((cache) =>
      // allSettled: a single bad URL must not abort warming the rest.
      Promise.allSettled(urls.map((url) => cache.add(new URL(url, self.location.origin)))),
    ),
  );
});

function neverCache(url) {
  return NEVER_CACHE.some((prefix) => url.pathname.startsWith(prefix));
}

self.addEventListener('fetch', (event) => {
  const request = event.request;
  if (request.method !== 'GET') return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (neverCache(url)) return;

  event.respondWith(
    fetch(request)
      .then((response) => {
        if (response && response.ok) {
          const copy = response.clone();
          caches.open(CACHE).then((cache) => cache.put(request, copy)).catch(() => {});
        }
        return response;
      })
      .catch(() =>
        caches.match(request, { ignoreSearch: request.mode === 'navigate' }).then((cached) => {
          if (cached) return cached;
          if (request.mode === 'navigate') return caches.match('./index.html');
          throw new Error(`offline and uncached: ${url.pathname}`);
        }),
      ),
  );
});
