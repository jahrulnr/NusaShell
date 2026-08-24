// NusaShell service worker — PWA-grade offline shell.
//
// Strategy: network-first with cache fallback for same-origin GETs. While the
// local server is up the browser always gets fresh assets (the server sends
// Cache-Control: no-cache, and network-first never serves a stale shell after
// an upgrade). When the server is down, every navigation and asset is served
// from this cache so the app boots and shows the full-screen offline state.
//
// Never cached: /ws (WebSocket handshake), POST /rpc/*, /local-file (large
// dynamic local files), /sounds (notification blips), /plugins/* (plugin UIs
// come from the plugin store and can be installed/uninstalled at runtime).

const CACHE = 'nusashell-shell-v1';

const NEVER_CACHE = [
  '/rpc/',
  '/local-file',
  '/sounds/',
  '/plugins/',
];

self.addEventListener('install', (event) => {
  // Precache the navigation entry plus every lazily-imported module (they
  // never appear in the runtime cache because nothing fetches them until the
  // user clicks). Everything else fills the runtime cache as the first
  // session fetches it.
  event.waitUntil(
    caches.open(CACHE)
      .then((cache) => cache.addAll(['./', './index.html', './manifest.webmanifest', './js/pip.js']))
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
