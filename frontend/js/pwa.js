// PWA plumbing: service-worker registration + install prompt.
//
// - The service worker makes the shell bootable while the local server is
//   down; the full-screen offline state (js/offline-screen.js) then covers
//   the app. See frontend/sw.js for the caching strategy.
// - beforeinstallprompt is captured so NusaShell can offer its own install
//   button in the title bar. The button only appears when the browser
//   actually allows installation; after install (or dismissal) it hides.

import { toast } from './ui.js';

function registerServiceWorker() {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return;
  // Relative URL: the app lives at the server root scope.
  navigator.serviceWorker.register('./sw.js').catch((err) => {
    console.error('service worker registration failed:', err);
  });
}

export function initInstallButton() {
  const btn = document.getElementById('pwa-install-btn');
  if (!btn) return;

  let deferredPrompt = null;

  window.addEventListener('beforeinstallprompt', (event) => {
    event.preventDefault();
    deferredPrompt = event;
    btn.hidden = false;
  });

  btn.addEventListener('click', async () => {
    if (!deferredPrompt) return;
    deferredPrompt.prompt();
    try {
      const choice = await deferredPrompt.userChoice;
      if (choice?.outcome === 'accepted') toast('NusaShell installed.', 'success');
    } catch { /* userChoice can reject on some engines; ignore */ }
    deferredPrompt = null;
    btn.hidden = true;
  });

  window.addEventListener('appinstalled', () => {
    deferredPrompt = null;
    btn.hidden = true;
    toast('NusaShell installed.', 'success');
  });
}

registerServiceWorker();
initInstallButton();

// Warm the service-worker cache with everything this very page loaded.
// On a first visit the assets are fetched by the browser directly (the SW
// was not controlling yet), so they are NOT in the runtime cache — a reload
// after the server dies would then serve cached HTML without cached CSS/JS
// (blank UI). The page parses its own DOM (single source of truth:
// index.html) and hands the URL list to the SW.
function collectAssetUrls() {
  const urls = new Set();
  for (const node of document.querySelectorAll('link[rel="stylesheet"][href], link[rel="icon"][href]')) {
    urls.add(node.getAttribute('href'));
  }
  for (const node of document.querySelectorAll('script[src][type="module"]')) {
    urls.add(node.getAttribute('src'));
  }
  for (const node of document.querySelectorAll('script[src]:not([type="module"])')) {
    urls.add(node.getAttribute('src'));
  }
  return [...urls];
}

// Fonts live inside fonts.css, not in the HTML — pull their URLs out of the
// loaded stylesheet so glyphs survive offline too. `fetchFn` and `baseUri`
// are injectable so unit tests (jsdom, no ambient location/fetch) can stub
// them; page-side callers omit both and use the ambient ones.
async function extractFontUrls(fetchFn, baseUri) {
  const fetcher = fetchFn ?? (typeof fetch === 'function' ? fetch : undefined);
  if (!fetcher) return [];
  const base = baseUri ?? (typeof location !== 'undefined' && location.href ? location.href : 'http://localhost/');
  try {
    const css = await (await fetcher('fonts/fonts.css')).text();
    return [...css.matchAll(/url\((['"]?)([^'")]+)\1\)/g)]
      .map((match) => new URL(match[2], base).pathname)
      .filter((path, index, all) => all.indexOf(path) === index);
  } catch {
    return [];
  }
}

function warmServiceWorkerCache() {
  if (!navigator.serviceWorker?.controller) return; // first load: SW not in control yet
  const fetcher = typeof fetch === 'function' ? fetch : undefined;
  Promise.all([extractFontUrls(fetcher, location.href)]).then(([fontUrls]) => {
    const urls = [...collectAssetUrls(), ...fontUrls, './agent-offline-mascot.png', './nusashell-mark.png'];
    navigator.serviceWorker.controller.postMessage({ precache: urls });
  });
}

if (navigator.serviceWorker?.controller) {
  warmServiceWorkerCache();
} else if (navigator.serviceWorker) {
  // First visit: once the SW takes control (claim), tell it what to warm.
  navigator.serviceWorker.ready.then(() => warmServiceWorkerCache());
}

// Popup fallback mini windows (see js/pip.js) open with ?mini=1 — mark the
// body so shell chrome hides and the small window stays usable. Module
// scripts run after parsing, so document.body always exists here.
if (new URLSearchParams(window.location.search).has('mini')) {
  document.body.classList.add('mini-mode');
}

// Exported for unit tests (jsdom boot + asset collector).
export { collectAssetUrls, extractFontUrls };
