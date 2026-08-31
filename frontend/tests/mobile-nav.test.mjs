// Mobile navigation drawer behavior.
//
// Below the 680px shell breakpoint the sidebar leaves normal flow and
// becomes an off-canvas drawer. These tests pin the controller contract:
// the title-bar hamburger toggles .body.is-nav-open, and the drawer closes
// via backdrop click, Escape, or selecting a nav item.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { initMobileNav } from '../js/mobile-nav.js';

const layoutCSS = await readFile(new URL('../styles/layout.css', import.meta.url), 'utf8');

const SHELL_HTML = `
<div class="window">
  <div class="titlebar">
    <div class="titlebar-actions">
      <button class="icon-btn mobile-nav-toggle" id="mobile-nav-toggle" type="button" aria-expanded="false" aria-controls="sidebar">menu</button>
    </div>
  </div>
  <div class="body">
    <div class="mobile-nav-backdrop" id="mobile-nav-backdrop"></div>
    <aside class="sidebar" id="sidebar">
      <button class="nav-item active" type="button" data-view="home" data-nav>Home</button>
      <button class="nav-item" type="button" data-view="agent" data-nav>Agent</button>
    </aside>
    <main class="content"></main>
  </div>
</div>`;

function withShell(fn, innerWidth = 390) {
  const dom = new JSDOM(SHELL_HTML);
  Object.defineProperty(dom.window, 'innerWidth', { configurable: true, value: innerWidth });
  const previousDocument = globalThis.document;
  const previousWindow = globalThis.window;
  globalThis.document = dom.window.document;
  globalThis.window = dom.window;
  return Promise.resolve()
    .then(() => fn(dom.window))
    .finally(() => {
      globalThis.document = previousDocument;
      globalThis.window = previousWindow;
      dom.window.close();
    });
}

const body = () => document.querySelector('.body');
const toggleBtn = () => document.getElementById('mobile-nav-toggle');
const click = (window, el) => el.dispatchEvent(new window.Event('click', { bubbles: true }));

async function openDrawer(window) {
  const nav = initMobileNav();
  click(window, toggleBtn());
  return nav;
}

test('hamburger opens the drawer and reflects state on aria-expanded', async () => {
  await withShell(async (window) => {
    await openDrawer(window);
    assert.ok(body().classList.contains('is-nav-open'), 'drawer should be open');
    assert.equal(toggleBtn().getAttribute('aria-expanded'), 'true');
  });
});

test('second click closes the drawer again', async () => {
  await withShell(async (window) => {
    await openDrawer(window);
    click(window, toggleBtn());
    assert.ok(!body().classList.contains('is-nav-open'));
    assert.equal(toggleBtn().getAttribute('aria-expanded'), 'false');
  });
});

test('clicking the backdrop closes the drawer', async () => {
  await withShell(async (window) => {
    await openDrawer(window);
    click(window, document.getElementById('mobile-nav-backdrop'));
    assert.ok(!body().classList.contains('is-nav-open'));
  });
});

test('Escape closes the drawer while open', async () => {
  await withShell(async (window) => {
    await openDrawer(window);
    document.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    assert.ok(!body().classList.contains('is-nav-open'));
  });
});

test('selecting a nav item closes the drawer', async () => {
  await withShell(async (window) => {
    await openDrawer(window);
    click(window, document.querySelector('[data-view="agent"]'));
    assert.ok(!body().classList.contains('is-nav-open'));
  });
});

test('opening the drawer focuses the active nav item', async () => {
  await withShell(async (window) => {
    await openDrawer(window);
    assert.equal(document.activeElement, document.querySelector('[data-view="home"]'));
  });
});

test('drawer state is exposed to assistive technology and restores focus on close', async () => {
  await withShell(async (window) => {
    initMobileNav();
    const sidebar = document.getElementById('sidebar');
    const backdrop = document.getElementById('mobile-nav-backdrop');

    assert.equal(sidebar.getAttribute('aria-hidden'), 'true');
    assert.ok(sidebar.hasAttribute('inert'));
    assert.equal(backdrop.getAttribute('aria-hidden'), 'true');

    click(window, toggleBtn());
    assert.equal(sidebar.getAttribute('aria-hidden'), 'false');
    assert.ok(!sidebar.hasAttribute('inert'));
    assert.equal(backdrop.getAttribute('aria-hidden'), 'false');

    click(window, toggleBtn());
    assert.equal(sidebar.getAttribute('aria-hidden'), 'true');
    assert.ok(sidebar.hasAttribute('inert'));
    assert.equal(document.activeElement, toggleBtn());
  });
});

test('desktop sidebar stays exposed and interactive when mobile drawer is unavailable', async () => {
  await withShell(async () => {
    initMobileNav();
    const sidebar = document.getElementById('sidebar');
    assert.equal(sidebar.getAttribute('aria-hidden'), 'false');
    assert.ok(!sidebar.hasAttribute('inert'));
  }, 1200);
});

test('mobile drawer is bounded, scrollable, and non-interactive while closed', () => {
  const mobileRules = layoutCSS.slice(layoutCSS.indexOf('@media (max-width: 680px)'));
  assert.match(mobileRules, /height:\s*100dvh/);
  assert.match(mobileRules, /max-width:\s*calc\(100vw - 24px\)/);
  assert.match(mobileRules, /overflow-y:\s*auto/);
  assert.match(mobileRules, /visibility:\s*hidden/);
  assert.match(mobileRules, /pointer-events:\s*none/);
});

test('mobile drawer gives the collapsed shell sidebar enough width for labels', () => {
  const mobileRules = layoutCSS.slice(layoutCSS.indexOf('@media (max-width: 680px)'));
  assert.match(
    mobileRules,
    /\.window \.sidebar \.nav-item,\s*\.window \.sidebar \.sidebar-mode-toggle\s*\{[\s\S]*?justify-content:\s*flex-start;[\s\S]*?width:\s*100%;[\s\S]*?padding-inline:\s*var\(--space-4\);/
  );
});

test('returns null when shell pieces are missing', () => {
  const dom = new JSDOM('<body><div class="body"></div></body>');
  const nav = initMobileNav(dom.window.document);
  assert.equal(nav, null);
});
