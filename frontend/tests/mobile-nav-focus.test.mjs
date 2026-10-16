// Mobile navigation drawer regression tests.
//
// The titlebar hamburger was retired once the top-bar #global-nav-toggle
// learned the mobile drawer: page/conversation/app.js now owns the whole
// contract through setGlobalNavOpen — the is-nav-open scrim class, inert,
// aria-hidden, focus hand-off, and the backdrop/Escape/nav-item dismissals
// the removed js/mobile-nav.js module used to drive.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';
import { JSDOM } from 'jsdom';

const root = new URL('../', import.meta.url);

async function extractFunctions(names) {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  return names.map((name) => {
    const start = app.indexOf(`function ${name}(`);
    assert.ok(start >= 0, `expected ${name} in app.js`);
    return app.slice(start, app.indexOf('\n}', start) + 2);
  }).join('\n');
}

const NAV_FUNCTIONS = [
  'narrowPanels', 'setDrawerOpen', 'closeOverlayPanels', 'panelKeydownHandler',
  'setGlobalNavOpen', 'setGlobalNavExpanded', 'syncGlobalNavExpand', 'syncGlobalNavChrome',
];

async function navHarness({ narrow = true } = {}) {
  const dom = new JSDOM(await readFile(new URL('index.html', root), 'utf8'));
  const { document } = dom.window;
  const code = await extractFunctions(NAV_FUNCTIONS);
  const store = {};
  const context = {
    document,
    matchMedia: () => ({ matches: narrow }),
    shell: document.querySelector('.shell'),
    roomDrawer: document.querySelector('#room-drawer'),
    dock: document.querySelector('#context-dock'),
    globalNav: document.querySelector('#global-nav'),
    globalNavToggle: document.querySelector('#global-nav-toggle'),
    globalNavExpand: document.querySelector('#global-nav-expand'),
    localStorage: {
      getItem: (key) => store[key] ?? null,
      setItem: (key, value) => { store[key] = value; },
    },
  };
  runInNewContext(code, context);
  return { dom, document, context };
}

test('the titlebar hamburger is gone — the top-bar toggle is the single drawer owner', async () => {
  const dom = new JSDOM(await readFile(new URL('index.html', root), 'utf8'));
  const { document } = dom.window;
  const shellInit = await readFile(new URL('shell-init.js', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');

  assert.equal(document.getElementById('mobile-nav-toggle'), null);
  assert.equal(document.querySelector('.titlebar'), null);
  assert.equal(document.querySelector('.mobile-shell-label'), null);
  assert.ok(document.getElementById('mobile-nav-backdrop'), 'the nav scrim stays');
  assert.ok(document.getElementById('global-nav-toggle'), 'the top-bar toggle owns the drawer');

  // The retired module and its shell-init bridge are gone too: exactly one
  // click path may toggle the drawer or a single gesture would flip it twice.
  await assert.rejects(readFile(new URL('js/mobile-nav.js', root), 'utf8'));
  assert.doesNotMatch(shellInit, /initMobileNav|mobile-nav-toggle|is-nav-open/);
  assert.equal(app.match(/globalNavToggle\.addEventListener\('click'/g)?.length, 1,
    'exactly one click binding toggles the drawer');
  assert.doesNotMatch(app, /mobile-nav-toggle/);
});

test('the nav seam drives drawer chrome: scrim class, inert, aria-hidden, and focus', async () => {
  const { document, context } = await navHarness();
  const nav = document.querySelector('#global-nav');
  const toggle = document.querySelector('#global-nav-toggle');

  context.setGlobalNavOpen(false);
  assert.equal(nav.hidden, true);
  assert.equal(context.shell.classList.contains('is-nav-open'), false);
  assert.equal(context.shell.classList.contains('global-nav-closed'), true);
  assert.equal(nav.getAttribute('aria-hidden'), 'true');
  assert.ok(nav.hasAttribute('inert'), 'a closed overlay drawer leaves the tab order');
  assert.equal(toggle.getAttribute('aria-expanded'), 'false');

  context.setGlobalNavOpen(true);
  assert.equal(nav.hidden, false);
  assert.equal(context.shell.classList.contains('is-nav-open'), true);
  assert.equal(context.shell.classList.contains('global-nav-closed'), false);
  assert.equal(nav.getAttribute('aria-hidden'), 'false');
  assert.ok(!nav.hasAttribute('inert'));
  assert.equal(toggle.getAttribute('aria-expanded'), 'true');
  assert.equal(document.activeElement, document.querySelector('.global-nav-item.is-active'),
    'opening the drawer focuses the active rail item');

  context.setGlobalNavOpen(false);
  assert.equal(document.activeElement, toggle,
    'closing returns focus to the top-bar toggle — the retired module did the same for its hamburger');
});

test('the nav rail and conversation panels share one overlay slot at narrow widths', async () => {
  const { document, context } = await navHarness();
  const navToggle = document.querySelector('#global-nav-toggle');
  const dockToggle = document.querySelector('#context-dock-toggle');
  const drawerToggle = document.querySelector('#room-drawer-toggle');

  // Nav → dock (the reported 414px trap): opening a panel must dismiss the
  // rail, drop the scrim class, and re-inert it so the nav backdrop can
  // never intercept the panel underneath.
  context.setGlobalNavOpen(true);
  context.setDrawerOpen('dock', true);
  assert.equal(context.globalNav.hidden, true, 'the panel replaces the nav overlay');
  assert.equal(context.shell.classList.contains('is-nav-open'), false,
    'a stranded scrim class is what let the backdrop eat the Context tab');
  assert.equal(context.globalNav.getAttribute('aria-hidden'), 'true');
  assert.ok(context.globalNav.hasAttribute('inert'));
  assert.equal(navToggle.getAttribute('aria-expanded'), 'false');
  assert.equal(document.activeElement, navToggle,
    'closing the rail still returns focus to its toggle');
  assert.equal(context.dock.hidden, false);
  assert.equal(dockToggle.getAttribute('aria-expanded'), 'true');

  // Panels → nav: opening the rail clears whichever panel was open.
  context.setDrawerOpen('rooms', true);
  context.setGlobalNavOpen(true);
  assert.equal(context.roomDrawer.hidden, true);
  assert.equal(context.dock.hidden, true);
  assert.equal(drawerToggle.getAttribute('aria-expanded'), 'false');
  assert.equal(dockToggle.getAttribute('aria-expanded'), 'false');
  assert.equal(context.globalNav.hidden, false);
  assert.equal(context.shell.classList.contains('is-nav-open'), true);

  // Escape still unwinds the single remaining overlay.
  let prevented = 0;
  context.panelKeydownHandler({ key: 'Escape', defaultPrevented: false, preventDefault() { prevented += 1; } });
  assert.equal(context.globalNav.hidden, true, 'Escape closes the nav drawer');
  assert.equal(prevented, 1);
  assert.equal(context.closeOverlayPanels(), false, 'mutual exclusion left nothing stacked beneath');

  // Desktop keeps the in-flow rail and panels independent.
  const wide = await navHarness({ narrow: false });
  wide.context.setGlobalNavOpen(true);
  wide.context.setDrawerOpen('dock', true);
  wide.context.setDrawerOpen('rooms', true);
  assert.equal(wide.context.globalNav.hidden, false, 'desktop rail stays open beside the panels');
  assert.equal(wide.context.dock.hidden, false);
  assert.equal(wide.context.roomDrawer.hidden, false);
});

test('the real bindings route backdrop, toggle, and nav-item gestures through the seam', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const { dom, document, context } = await navHarness();
  const nav = document.querySelector('#global-nav');
  const toggle = document.querySelector('#global-nav-toggle');
  const backdrop = document.querySelector('#mobile-nav-backdrop');

  // Evaluate the verbatim listener registrations so the assertions exercise
  // the shipped wiring, not a copy of it.
  for (const pattern of [
    /globalNavToggle\.addEventListener\('click'[^;]*;/,
    /document\.querySelector\('#mobile-nav-backdrop'\)\?\.addEventListener\('click'[\s\S]*?\);/,
    /globalNav\.addEventListener\('click',[\s\S]*?\}\);/,
    /document\.addEventListener\('keydown',\s*panelKeydownHandler\);/,
  ]) {
    const binding = app.match(pattern)?.[0];
    assert.ok(binding, `binding missing in app.js: ${pattern}`);
    runInNewContext(binding, context);
  }

  context.setGlobalNavOpen(false, { focus: false });
  toggle.click();
  assert.equal(nav.hidden, false, 'the top-bar toggle opens the overlay drawer');
  assert.equal(context.shell.classList.contains('is-nav-open'), true);

  backdrop.click();
  assert.equal(nav.hidden, true, 'the nav scrim closes the drawer');
  assert.equal(context.shell.classList.contains('is-nav-open'), false);

  toggle.click();
  document.querySelector('[data-global-route="agent"]').dispatchEvent(
    new dom.window.MouseEvent('click', { bubbles: true }));
  assert.equal(nav.hidden, true, 'selecting a nav item dismisses the drawer');

  toggle.click();
  document.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  assert.equal(nav.hidden, true, 'Escape dismisses the drawer');
});

test('in-flow widths keep the rail free of drawer bookkeeping', async () => {
  const { document, context } = await navHarness({ narrow: false });
  const nav = document.querySelector('#global-nav');
  const toggle = document.querySelector('#global-nav-toggle');

  context.setGlobalNavOpen(false);
  assert.equal(nav.hidden, true);
  assert.equal(context.shell.classList.contains('is-nav-open'), false,
    'the scrim class is overlay-only — it must never latch on desktop');
  assert.ok(!nav.hasAttribute('inert'));
  assert.equal(nav.getAttribute('aria-hidden'), 'false');
  assert.notEqual(document.activeElement, toggle,
    'closing the in-flow rail does not steal focus');

  context.setGlobalNavOpen(true);
  assert.equal(nav.hidden, false);
  assert.ok(!nav.hasAttribute('inert'));
  assert.notEqual(document.activeElement, document.querySelector('.global-nav-item.is-active'),
    'opening the in-flow rail does not move focus either');
});

test('the nav scrim keeps the body-region extent and the 680px paint gate', async () => {
  const css = await readFile(new URL('shell.css', root), 'utf8');
  const rule = css.match(/\.mobile-nav-backdrop\s*\{[^}]*\}/s)?.[0] || '';

  // The scrim starts below the 50px topbar — the toggle that dismisses the
  // drawer must never sit under its own dimming layer.
  assert.doesNotMatch(rule, /inset:\s*0/, 'full-viewport inset dims the shell header controls');
  assert.match(rule, /top:\s*50px/, 'scrim starts below the 50px topbar like .panel-backdrop');
  assert.match(css, /@media\s*\(max-width:\s*680px\)\s*\{[\s\S]*?\.body\.is-nav-open\s+\.mobile-nav-backdrop\s*\{[^}]*opacity:\s*1/,
    'the scrim paints only inside the phone breakpoint');
});
