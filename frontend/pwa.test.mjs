// PWA surface: web app manifest, service worker contract, full-window
// offline screen state machine, install button wiring, and mini-window
// strategy selection.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join, resolve } from 'node:path';
import { JSDOM } from 'jsdom';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, '..');

function withDom(bodyHtml = '', fn, { url = 'http://127.0.0.1:9999/' } = {}) {
  const html = `<!DOCTYPE html><html><body>${bodyHtml}</body></html>`;
  const dom = new JSDOM(html, { url });
  const previousDocument = globalThis.document;
  const previousWindow = globalThis.window;
  globalThis.document = dom.window.document;
  globalThis.window = dom.window;
  return Promise.resolve()
    .then(() => fn(dom.window))
    .finally(() => {
      globalThis.document = previousDocument;
      globalThis.window = previousWindow;
    });
}

test('manifest is standalone-scoped and every referenced icon exists', async () => {
  const raw = await readFile(join(repoRoot, 'frontend/manifest.webmanifest'), 'utf8');
  const manifest = JSON.parse(raw);
  assert.equal(manifest.display, 'standalone');
  assert.equal(manifest.start_url, './');
  assert.equal(manifest.scope, './');
  assert.ok(manifest.icons.length >= 3, 'need any + maskable icons');
  for (const icon of manifest.icons) {
    const file = join(repoRoot, 'frontend', decodeURIComponent(new URL(icon.src, 'http://x/').pathname));
    await assert.doesNotReject(() => readFile(file), `missing icon file: ${icon.src}`);
    assert.match(icon.sizes, /^\d+x\d+$/);
    assert.ok(['any', 'maskable'].includes(icon.purpose));
  }
});

test('service worker is network-first with cache fallback and never caches dynamic endpoints', async () => {
  const sw = await readFile(join(repoRoot, 'frontend/sw.js'), 'utf8');
  assert.match(sw, /addEventListener\('fetch'/);
  assert.match(sw, /request\.method !== 'GET'/, 'only GETs are cacheable');
  assert.match(sw, /url\.origin !== self\.location\.origin/, 'same-origin only');
  for (const endpoint of ['/rpc/', '/local-file', '/sounds/', '/plugins/']) {
    assert.ok(sw.includes(`'${endpoint}'`), `must never cache ${endpoint}`);
  }
  assert.match(sw, /caches\.match\('\.\/index\.html'\)/, 'navigations fall back to the cached shell');
  assert.match(sw, /'\.\/js\/pip\.js'/, 'lazily-imported modules must be precached (unreachable offline otherwise)');
  assert.match(sw, /addEventListener\('message'/, 'page can hand its asset list to the SW');
  assert.match(sw, /allSettled/, 'one bad URL must not abort warming the rest');
  assert.match(sw, /nusashell-shell-v\d+/);
  assert.match(sw, /skipWaiting/);
  assert.match(sw, /clients\.claim\(\)/);
});

test('offline screen shows instantly on offline, after grace on reconnecting, hides on open', () =>
  withDom(
    `<div class="offline-screen" id="offline-screen" hidden><button id="offline-retry-btn" type="button"></button></div>`,
    async (window) => {
      const { initOfflineScreen } = await import('./js/offline-screen.js');
      initOfflineScreen({ graceMs: 5000 });

      const screen = document.getElementById('offline-screen');
      const emit = (status) => window.dispatchEvent(
        new window.CustomEvent('nusashell:connection-status', { detail: { status } }),
      );

      // transient WS states stay hidden inside the grace window
      emit('reconnecting');
      assert.equal(screen.hidden, true);
      emit('closed');
      emit('error');

      // an explicit offline verdict covers everything immediately
      emit('offline');
      assert.equal(screen.hidden, false);

      // recovery uncovers instantly
      emit('open');
      assert.equal(screen.hidden, true);

      // a persistent reconnect failure covers after the grace period
      emit('reconnecting');
      await new Promise((r) => setTimeout(r, 20));
      assert.equal(screen.hidden, true, 'no cover while inside grace period');
      await new Promise((r) => setTimeout(r, 5100));
      assert.equal(screen.hidden, false, 'cover appears once the outage persists');

      // and it clears again on recovery
      emit('open');
      assert.equal(screen.hidden, true);
    },
    ),
);

test('install button appears on beforeinstallprompt and hides after install flows', () =>
  withDom(`<button class="icon-btn" id="pwa-install-btn" hidden>install</button>`, async () => {
    await import('./js/pwa.js');
    const btn = document.getElementById('pwa-install-btn');
    assert.equal(btn.hidden, true, 'hidden until the browser allows installation');

    let prompted = false;
    const event = new window.Event('beforeinstallprompt');
    event.prompt = () => { prompted = true; };
    Object.defineProperty(event, 'userChoice', {
      value: Promise.resolve({ outcome: 'accepted' }),
    });
    window.dispatchEvent(event);
    assert.equal(btn.hidden, false, 'visible once installable');

    btn.click();
    await new Promise((r) => setTimeout(r, 0));
    assert.ok(prompted, 'native prompt requested');
    assert.equal(btn.hidden, true, 'hidden again after the choice');

    window.dispatchEvent(new window.Event('appinstalled'));
    assert.equal(btn.hidden, true);
  }));

test('mini window falls back to a popup when Document PiP is unavailable', () =>
  withDom('<div id="agent-thread"></div>', async (window) => {
    const pip = await import('./js/pip.js');
    assert.equal(pip.pipSupported(), false, 'jsdom has no documentPictureInPicture');
    assert.equal(pip.miniWindowOpen(), false);

    const opened = [];
    window.open = (href, target, features) => {
      opened.push({ href, target, features });
      return null;
    };

    const strategy = await pip.openMiniWindow();
    assert.equal(strategy, 'popup');
    assert.equal(opened.length, 1);
    assert.match(opened[0].href, /\?mini=1#agent$/);
    assert.equal(opened[0].target, 'nusashell-mini');
    assert.match(opened[0].features, /width=\d+/);
    assert.match(opened[0].features, /height=\d+/);
  }));

test('pwa.js boots side-effect free in a jsdom context (no service worker required)', () =>
  withDom(
    `<link rel="stylesheet" href="styles/global.css">
     <script src="js/app.js"></script>`,
    async () => {
      const { collectAssetUrls } = await import(`./js/pwa.js?boot=${Date.now()}`);
      assert.ok(Array.isArray(collectAssetUrls()), 'asset collector survives a jsdom import');
    },
    ),
);

test('cache warming collects stylesheets, scripts, icons, and font urls from the page', () =>
  withDom(
    `<link rel="stylesheet" href="styles/global.css">
     <link rel="icon" href="nusashell-mark.png">
     <script src="vendor/chartjs/chart.umd.min.js"></script>
     <script type="module" src="js/app.js"></script>`,
    async (window) => {
      const { collectAssetUrls, extractFontUrls } = await import('./js/pwa.js?warm=' + Date.now());
      const urls = collectAssetUrls();
      for (const want of ['styles/global.css', 'nusashell-mark.png', 'vendor/chartjs/chart.umd.min.js', 'js/app.js']) {
        assert.ok(urls.includes(want), `missing ${want} in ${JSON.stringify(urls)}`);
      }

      const fontsCss = `@font-face { src: url("fonts/inter.woff2") format("woff2"); }
@font-face { src: url('fonts/jetbrains.woff2') format("woff2"); }`;
      const stubFetch = async () => ({ text: async () => fontsCss });
      const fonts = await extractFontUrls(stubFetch, 'http://127.0.0.1:9999/index.html');
      assert.deepEqual(fonts, ['/fonts/inter.woff2', '/fonts/jetbrains.woff2']);
    },
    ),
);
