import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

function setupDom() {
  const dom = new JSDOM(`<!doctype html><html><body>
    <aside class="sidebar" id="sidebar">
      <nav class="sidebar-actions" aria-label="Shell actions">
        <button class="sidebar-action" id="pwa-install-btn" hidden></button>
        <button class="sidebar-action" id="pet-btn" hidden>
          <span class="nav-label">Pet</span>
        </button>
        <button class="sidebar-action" id="nav-settings-btn"></button>
      </nav>
    </aside>
    <div class="ui-dialog-overlay" id="pet-install-overlay" aria-hidden="true" hidden>
      <div class="ui-dialog pet-install-dialog" role="dialog" aria-modal="true" tabindex="-1">
        <div class="ui-dialog-header">
          <h2 id="pet-install-title">Install</h2>
          <button class="ui-dialog-close" id="pet-install-close" type="button">×</button>
        </div>
        <div class="ui-dialog-body pet-install-body">
          <div class="plugin-install-error" id="pet-install-error" role="alert" hidden></div>
          <div class="pet-install-progress" id="pet-install-progress" hidden>
            <div class="pet-install-phase" id="pet-install-phase"></div>
            <div class="pet-install-bar" id="pet-install-bar-track">
              <div class="pet-install-bar-fill" id="pet-install-bar"></div>
            </div>
            <div class="pet-install-bytes" id="pet-install-bytes"></div>
          </div>
        </div>
        <div class="ui-dialog-actions">
          <button class="mini-btn ghost" id="pet-install-cancel" type="button">Cancel</button>
          <button class="mini-btn" id="pet-install-confirm" type="button">Install</button>
        </div>
      </div>
    </div>
  </body></html>`, { url: 'http://localhost/' });
  // Replace the global window/document with the jsdom instance. We also
  // pin document.activeElement's prototype so `instanceof HTMLElement`
  // resolves correctly in pet-launcher's openPetInstallDialog.
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.HTMLElement = dom.window.HTMLElement;
  // toast() (ui.js) appends into #toast-container; inject one in the body.
  const toastContainer = dom.window.document.createElement('div');
  toastContainer.id = 'toast-container';
  dom.window.document.body.appendChild(toastContainer);
  // JSDOM does not implement requestAnimationFrame; ui.js's toast() uses it
  // to schedule the slide-in animation. Stub it to a no-op so toast() can
  // run without an animation loop.
  if (!dom.window.requestAnimationFrame) {
    dom.window.requestAnimationFrame = (cb) => { cb(); return 0; };
  }
  if (typeof globalThis.requestAnimationFrame !== 'function') {
    globalThis.requestAnimationFrame = (cb) => { cb(); return 0; };
  }
  return () => {
    delete globalThis.window;
    delete globalThis.document;
    delete globalThis.HTMLElement;
  };
}

function stubFetch(handler) {
  const original = globalThis.fetch;
  globalThis.fetch = async (url, init) => handler(url, init);
  return () => { globalThis.fetch = original; };
}

// rpc.js's wire shape: top-level {ok:true, result:{...}}. Stubs return that
// envelope so applyStatus sees the payload rather than a wire-level error.
function ok(body) { return { ok: true, json: async () => ({ ok: true, ...body }) }; }

test('pet button hidden when platform is not supported', async () => {
  const restore = setupDom();
  const restoreFetch = stubFetch(async () => ok({ result: { supported: false, installed: false } }));
  try {
    const { initPets } = await import('../js/pet-launcher.js');
    await initPets();
    await new Promise((r) => setTimeout(r, 30));
    const btn = document.getElementById('pet-btn');
    assert.equal(btn.hidden, true, 'button must hide on non-Linux');
  } finally {
    restoreFetch();
    restore();
  }
});

test('pet button visible with is-installed class when supported and installed', async () => {
  const restore = setupDom();
  const restoreFetch = stubFetch(async () => ok({ result: { supported: true, installed: true, path: '/opt/pets', version: '0.2.0' } }));
  try {
    const { initPets } = await import('../js/pet-launcher.js');
    await initPets();
    await new Promise((r) => setTimeout(r, 30));
    const btn = document.getElementById('pet-btn');
    assert.equal(btn.hidden, false, 'button must show on supported + installed');
    assert.ok(btn.classList.contains('is-installed'), 'is-installed class set');
    assert.equal(btn.title.includes('0.2.0'), true, 'version surfaces in title');
  } finally {
    restoreFetch();
    restore();
  }
});

test('click on not-installed button opens the install dialog', async () => {
  const restore = setupDom();
  let lastUrl = '__unset__';
  const restoreFetch = stubFetch(async (url) => {
    lastUrl = String(url);
    return ok({ result: { supported: true, installed: false } });
  });
  try {
    const { initPets } = await import('../js/pet-launcher.js');
    await initPets();
    await new Promise((r) => setTimeout(r, 30));
    assert.ok(lastUrl.includes('pets_status'), 'initial status call');
    document.getElementById('pet-btn').click();
    await new Promise((r) => setTimeout(r, 30));
    const overlay = document.getElementById('pet-install-overlay');
    assert.equal(overlay.hidden, false, 'install dialog opens');
  } finally {
    restoreFetch();
    restore();
  }
});

test('click on installed button calls settings.pets_launch', async () => {
  const restore = setupDom();
  const calls = [];
  const restoreFetch = stubFetch(async (url) => {
    calls.push(String(url));
    console.log('TEST4 fetch url=', url);
    if (url.includes('pets_status')) {
      return ok({ result: { supported: true, installed: true, path: '/opt/pets' } });
    }
    return ok({ result: { launched: true, path: '/opt/pets' } });
  });
  try {
    const { initPets } = await import('../js/pet-launcher.js');
    await initPets();
    await new Promise((r) => setTimeout(r, 30));
    const btn = document.getElementById('pet-btn');
    console.log('TEST4 btn.before click hidden=', btn.hidden, 'classes=', btn.className);
    btn.click();
    await new Promise((r) => setTimeout(r, 40));
    console.log('TEST4 calls=', calls);
    assert.ok(calls.some((u) => u.includes('pets_launch')), 'launch RPC called');
  } finally {
    restoreFetch();
    restore();
  }
});

test('spam click while launch in flight only sends one pets_launch', async () => {
  const restore = setupDom();
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  let launchCount = 0;
  const restoreFetch = stubFetch(async (url) => {
    if (String(url).includes('pets_status')) {
      return ok({ result: { supported: true, installed: true, path: '/opt/pets', running: false } });
    }
    launchCount += 1;
    await gate;
    return ok({ result: { launched: true, path: '/opt/pets' } });
  });
  try {
    const { initPets } = await import('../js/pet-launcher.js');
    await initPets();
    await new Promise((r) => setTimeout(r, 30));
    const btn = document.getElementById('pet-btn');
    btn.click();
    btn.click();
    btn.click();
    await new Promise((r) => setTimeout(r, 40));
    assert.equal(launchCount, 1, 'overlapping clicks must not spawn extra RPCs');
    release();
    await new Promise((r) => setTimeout(r, 40));
  } finally {
    if (release) release();
    restoreFetch();
    restore();
  }
});

test('click while running still calls settings.pets_launch so the backend can stop', async () => {
  const restore = setupDom();
  const calls = [];
  const restoreFetch = stubFetch(async (url) => {
    calls.push(String(url));
    if (String(url).includes('pets_status')) {
      return ok({ result: { supported: true, installed: true, path: '/opt/pets', running: true } });
    }
    return ok({ result: { launched: false, stopped: true, path: '/opt/pets' } });
  });
  try {
    const { initPets } = await import('../js/pet-launcher.js');
    await initPets();
    await new Promise((r) => setTimeout(r, 30));
    const btn = document.getElementById('pet-btn');
    assert.ok(btn.classList.contains('is-running'), 'running class set');
    btn.click();
    await new Promise((r) => setTimeout(r, 40));
    assert.ok(calls.some((u) => u.includes('pets_launch')), 'stop still uses pets_launch');
  } finally {
    restoreFetch();
    restore();
  }
});
