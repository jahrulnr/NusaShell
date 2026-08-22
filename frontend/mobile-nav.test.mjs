// Mobile navigation drawer behavior.
//
// Below the 680px shell breakpoint the sidebar leaves normal flow and
// becomes an off-canvas drawer. These tests pin the controller contract:
// the title-bar hamburger toggles .body.is-nav-open, and the drawer closes
// via backdrop click, Escape, or selecting a nav item.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { initMobileNav } from './js/mobile-nav.js';

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

function withShell(fn) {
  const dom = new JSDOM(SHELL_HTML);
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

test('returns null when shell pieces are missing', () => {
  const dom = new JSDOM('<body><div class="body"></div></body>');
  const nav = initMobileNav(dom.window.document);
  assert.equal(nav, null);
});
