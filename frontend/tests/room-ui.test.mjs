// Room UI regression tests — comments 4/5/7/8 of the widget-conversation UX
// fixes: room-info must not cover #jump-latest, .icon-btn must be styled,
// narrow overlay panels need an escape hatch, and room actions collapse into
// one local menu shared by the ellipsis button and right-click.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';
import { JSDOM } from 'jsdom';

import { filterConversations } from '../page/conversation/room-state.js';
import { closeRoomMenu, isRoomMenuOpenFor, openRoomActionsMenu } from '../page/conversation/room-menu.js';

const root = new URL('../', import.meta.url);

// This suite runs from two mounts: the prototype tree, where the new ui-map
// is staged under promote/, and frontend/tests/ after promote.sh — promote/
// itself is staging-only and never copied, so the promoted mount reads the
// same map at its committed repo-root location. The mount path is the
// deterministic distinction; each environment has exactly one canonical map.
const uiMapURL = root.pathname.endsWith('/frontend/')
  ? new URL('../resources/agent/docs/ui-source/ui-map.json', root)
  : new URL('promote/ui-map.json', root);

async function extractFunctions(names) {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  return names.map((name) => {
    const start = app.indexOf(`function ${name}(`);
    assert.ok(start >= 0, `expected ${name} in app.js`);
    return app.slice(start, app.indexOf('\n}', start) + 2);
  }).join('\n');
}

async function conversationDom() {
  const html = await readFile(new URL('index.html', root), 'utf8');
  return new JSDOM(html);
}

// --- Comment 7: .icon-btn needs real themed styling, not browser defaults --

test('icon-btn controls are themed with an adequate click target', async () => {
  const css = await readFile(new URL('shell.css', root), 'utf8');

  assert.match(css, /\.icon-btn\s*\{[^}]*min-height:\s*32px/s, '.icon-btn needs a themed base rule with click area');
  assert.match(css, /\.icon-btn\s*\{[^}]*border-radius/s);
  assert.match(css, /\.icon-btn:hover/);
});

// --- Comment 4: room-info icons must not cover #jump-latest ---------------

test('room info controls sit inside the stream toolbar flow next to jump-latest', async () => {
  const dom = await conversationDom();
  const { document } = dom.window;
  const strips = await readFile(new URL('page/conversation/strips.css', root), 'utf8');
  const shellCss = await readFile(new URL('shell.css', root), 'utf8');

  const info = document.querySelector('#agent-room-info');
  const toolbar = document.querySelector('.stream-toolbar');
  const jump = document.querySelector('#jump-latest');

  assert.ok(info && toolbar && jump);
  assert.equal(info.parentElement, toolbar, 'room info is a toolbar flow item, not an overlay');
  assert.equal(info.previousElementSibling, jump, 'icons keep the right edge after Latest');
  assert.match(strips, /\.stream-toolbar\s*>\s*\.agent-room-info\s*\{[^}]*flex:\s*none/s,
    'the toolbar child keeps the production absolute rule overridden');
  assert.match(strips, /\.stream-toolbar\s*>\s*\.agent-room-info\s*\{[^}]*position:\s*relative/s);
  // The filter region yields readable space instead of squishing or covering.
  assert.match(shellCss, /\.event-filters\s*\{[^}]*min-width:\s*0/s);
  assert.match(shellCss, /\.event-filters\s*\{[^}]*overflow-x:\s*auto/s);
});

test('room diagnostics live in the Context dock — the (i) trigger and popover are gone', async () => {
  const dom = await conversationDom();
  const { document } = dom.window;
  const strips = await readFile(new URL('page/conversation/strips.js', root), 'utf8');
  const roomInfo = await readFile(new URL('js/views/agent/room-info.js', root), 'utf8');
  const shellInit = await readFile(new URL('shell-init.js', root), 'utf8');
  const agentCss = await readFile(new URL('styles/agent.css', root), 'utf8');
  const uiMap = JSON.parse(await readFile(uiMapURL, 'utf8'));

  // Only the mini-window control remains in the toolbar cluster; Room ID,
  // tool calls, and message counts are covered by the Context dock rows.
  assert.ok(document.querySelector('#mini-window-btn'), 'the PiP control stays');
  for (const dead of [
    'agent-room-info-trigger', 'agent-room-info-popover', 'agent-room-info-title',
    'agent-room-info-close', 'agent-room-tool-count', 'agent-room-compaction-count',
    'agent-room-id', 'agent-room-id-copy',
  ]) {
    assert.equal(document.getElementById(dead), null, `#${dead} removed from index.html`);
    assert.equal(uiMap.controls[dead], undefined, `#${dead} removed from the ui map`);
    assert.doesNotMatch(strips, new RegExp(`id="${dead}"`), `mountRoomInfo stops generating #${dead}`);
  }

  // The module keeps only the visibility update the mini-window section
  // still needs; popover binding and metadata formatting are deleted too.
  assert.match(roomInfo, /export function updateRoomInfo\(/);
  assert.doesNotMatch(roomInfo, /bindRoomInfo|agent-room-info-popover|getRoomMetadata/);
  assert.doesNotMatch(shellInit, /bindRoomInfo/);
  assert.doesNotMatch(agentCss, /\.agent-room-info-(trigger|popover|title|close|id|label|metric|copy|eyebrow)/);
});

// --- Comment 8: overlay panels need an escape hatch at narrow widths -------

test('narrow overlay panels get a backdrop, explicit close controls, and clear z-order', async () => {
  const dom = await conversationDom();
  const { document } = dom.window;
  const css = await readFile(new URL('shell.css', root), 'utf8');

  const backdrop = document.querySelector('#panel-backdrop');
  assert.ok(backdrop, 'shared panel backdrop exists');
  assert.equal(backdrop.getAttribute('aria-hidden'), 'true');
  assert.equal(backdrop.closest('.shell') !== null, true);

  const drawerClose = document.querySelector('#room-drawer-close');
  const dockClose = document.querySelector('#dock-close');
  assert.ok(drawerClose, 'room drawer exposes an explicit close');
  assert.ok(dockClose, 'context dock exposes an explicit close');
  assert.equal(document.querySelector('#room-drawer').contains(drawerClose), true);
  assert.equal(document.querySelector('#context-dock').contains(dockClose), true);
  assert.equal(drawerClose.type, 'button');
  assert.equal(dockClose.type, 'button');

  assert.match(css, /\.panel-backdrop\s*\{[^}]*position:\s*fixed/s);
  assert.match(css, /\.panel-backdrop\s*\{[^}]*z-index:\s*1[0-9]/s,
    'backdrop stacks below the fixed panels (20/30) but above the stream');
  assert.match(css, /\.shell\.agent-active:not\(\.rooms-closed\)\s+\.panel-backdrop/);
  assert.match(css, /\.shell\.agent-active:not\(\.dock-closed\)\s+\.panel-backdrop/);
  // Overlay panels start below the 50px topbar at every narrow width — the
  // titlebar that used to push them down to 108px is gone.
  assert.match(css, /@media\s*\(max-width:\s*720px\)\s*\{[\s\S]*?\.room-drawer\s*\{[^}]*top:\s*50px/);
  assert.doesNotMatch(css, /top:\s*108px/);
});

test('the retired mobile titlebar leaves the shell at full viewport height', async () => {
  const css = await readFile(new URL('shell.css', root), 'utf8');
  const dom = await conversationDom();
  const { document } = dom.window;

  // The <=680px titlebar only existed to host the drawer hamburger; once the
  // top-bar nav toggle took over the drawer the whole bar went away, so
  // nothing may keep subtracting its 58px from the shell or pushing overlays
  // further down — the composer keeps the full viewport height again.
  assert.equal(document.querySelector('.titlebar'), null, '.titlebar markup removed');
  assert.equal(document.getElementById('mobile-nav-toggle'), null, '#mobile-nav-toggle removed');
  assert.equal(document.querySelector('.mobile-shell-label'), null, 'mobile shell label removed');
  assert.doesNotMatch(css, /^\.(titlebar|mobile-shell-label|mobile-nav-toggle)/m,
    'the titlebar chrome rules are deleted, not merely hidden');
  assert.doesNotMatch(css, /calc\(100%\s*-\s*58px\)/,
    'no leftover subtraction for a bar that no longer exists');
  assert.match(css, /^\.shell\s*\{[^}]*height:\s*100%/m,
    'the shell keeps its full-viewport base height');
});

test('narrow panels keep a single overlay and close through one shared path', async () => {
  const dom = await conversationDom();
  const { document } = dom.window;
  const code = await extractFunctions(['narrowPanels', 'setDrawerOpen', 'closeOverlayPanels', 'panelKeydownHandler', 'setGlobalNavOpen', 'syncGlobalNavExpand', 'syncGlobalNavChrome']);
  const context = {
    document,
    window: dom.window,
    shell: document.querySelector('.shell'),
    roomDrawer: document.querySelector('#room-drawer'),
    dock: document.querySelector('#context-dock'),
    globalNav: document.querySelector('#global-nav'),
    globalNavToggle: document.querySelector('#global-nav-toggle'),
    globalNavExpand: document.querySelector('#global-nav-expand'),
    matchMedia: () => ({ matches: true }),
  };
  runInNewContext(code, context);

  const drawerToggle = document.querySelector('#room-drawer-toggle');
  const dockToggle = document.querySelector('#context-dock-toggle');

  context.setDrawerOpen('rooms', true);
  assert.equal(context.roomDrawer.hidden, false);
  assert.equal(drawerToggle.getAttribute('aria-expanded'), 'true');
  assert.equal(context.shell.classList.contains('rooms-closed'), false);

  // Opening the dock on a narrow viewport swaps the overlay instead of
  // stacking two fixed panels over the toggles.
  context.setDrawerOpen('dock', true);
  assert.equal(context.dock.hidden, false);
  assert.equal(context.roomDrawer.hidden, true, 'only one overlay stays open at narrow widths');
  assert.equal(drawerToggle.getAttribute('aria-expanded'), 'false');
  assert.equal(dockToggle.getAttribute('aria-expanded'), 'true');
  assert.equal(context.shell.classList.contains('rooms-closed'), true);
  assert.equal(context.shell.classList.contains('dock-closed'), false);

  // Escape walks through the same close path as the backdrop.
  let prevented = 0;
  context.panelKeydownHandler({ key: 'Escape', defaultPrevented: false, preventDefault() { prevented += 1; } });
  assert.equal(context.dock.hidden, true, 'Escape closes the open overlay panel');
  assert.equal(dockToggle.getAttribute('aria-expanded'), 'false');
  assert.equal(prevented, 1);
  assert.equal(context.closeOverlayPanels(), false, 'nothing left to close');

  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  assert.match(app, /panel-backdrop/);
  assert.match(app, /room-drawer-close/);
  assert.match(app, /dock-close/);
  assert.match(app, /document\.addEventListener\('keydown',\s*panelKeydownHandler\)/);
});

test('desktop viewports keep both persistent panels', async () => {
  const dom = await conversationDom();
  const { document } = dom.window;
  const code = await extractFunctions(['narrowPanels', 'setDrawerOpen', 'closeOverlayPanels', 'panelKeydownHandler']);
  const context = {
    document,
    window: dom.window,
    shell: document.querySelector('.shell'),
    roomDrawer: document.querySelector('#room-drawer'),
    dock: document.querySelector('#context-dock'),
    matchMedia: () => ({ matches: false }),
  };
  runInNewContext(code, context);

  context.setDrawerOpen('rooms', true);
  context.setDrawerOpen('dock', true);
  assert.equal(context.roomDrawer.hidden, false, 'desktop keeps the room drawer');
  assert.equal(context.dock.hidden, false, 'desktop keeps the dock beside it');
  assert.equal(context.closeOverlayPanels(), false, 'Escape path is narrow-mode only');
});

// --- Comment 5: one local menu for ellipsis + right-click -----------------

test('room rows collapse rename, compact, and delete into one action menu for click and right-click', async () => {
  const dom = await conversationDom();
  const { document } = dom.window;
  const code = await extractFunctions(['renderRooms', 'buildRoomRow']);
  const opens = [];
  const calls = { rename: [], remove: [], open: [], close: 0 };
  const context = {
    document,
    window: dom.window,
    window: dom.window,
    conversations: [{ id: 'c1', title: 'Room one', status: 'idle', updated_at: '2026-10-13T08:00:00Z' }],
    roomSearchQuery: '',
    activeConversation: null,
    filterConversations,
    syncRoomItem() {},
    openRoomActionsMenu: (options) => { opens.push(options); return { close() {} }; },
    isRoomMenuOpenFor: () => false,
    closeRoomMenu: () => { calls.close += 1; return true; },
    registerOverlayDismiss: () => () => {},
    startRoomRename: (...args) => calls.rename.push(args),
    deleteConversation: (...args) => calls.remove.push(args),
    openConversation: (...args) => calls.open.push(args),
  };
  runInNewContext(code, context);

  context.renderRooms();
  assert.ok(calls.close >= 1, 're-rendering the list dismisses any open menu');

  const row = document.querySelector('.room-item');
  const actions = row.querySelector('.room-actions');
  assert.ok(actions, 'the row keeps one action affordance');
  assert.equal(actions.getAttribute('aria-haspopup'), 'menu');
  assert.equal(actions.getAttribute('aria-expanded'), 'false');
  assert.ok(actions.querySelector('[data-lucide="ellipsis-vertical"]'), 'ellipsis-vertical affordance');
  assert.equal(row.querySelector('.room-rename'), null, 'inline rename button is gone');
  assert.equal(row.querySelector('.room-delete'), null, 'inline delete button is gone');

  actions.click();
  assert.equal(opens.length, 1);
  assert.equal(opens[0].anchor, actions, 'ellipsis opens the menu anchored to itself');
  assert.equal(actions.getAttribute('aria-expanded'), 'true');
  assert.deepEqual(Array.from(opens[0].items, (item) => item.id), ['rename', 'compact', 'delete']);

  row.dispatchEvent(new dom.window.MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 40, clientY: 60 }));
  assert.equal(opens.length, 2, 'right-click opens a menu too');
  assert.deepEqual(Array.from(opens[1].items, (item) => item.id), ['rename', 'compact', 'delete'], 'both gestures build the same menu');
  assert.ok(opens[1].point, 'right-click positions from the pointer');

  // The menu actions still run the existing flows — including the delete fix.
  opens[0].items[0].onSelect();
  assert.equal(calls.rename.length, 1);
  assert.equal(calls.rename[0][0], row);
  assert.equal(calls.rename[0][1].id, 'c1');
  opens[0].items.find((item) => item.id === 'delete').onSelect();
  assert.equal(calls.remove.length, 1);
  assert.equal(calls.remove[0][0], 'c1');

  opens[0].onClose?.();
  assert.equal(actions.getAttribute('aria-expanded'), 'false', 'closing the menu syncs the trigger');

  // The room itself still opens from the row body, not the menu affordance.
  row.querySelector('.room-item-main').click();
  assert.equal(calls.open.length, 1);
  assert.equal(calls.open[0][0], 'c1');
});

test('renderRooms leaves a mid-rename row untouched', async () => {
  const dom = await conversationDom();
  const { document } = dom.window;
  const code = await extractFunctions(['renderRooms', 'buildRoomRow']);
  const context = {
    document,
    window: dom.window,
    conversations: [{ id: 'c1', title: 'Room one', status: 'idle', updated_at: '2026-10-13T08:00:00Z' }],
    roomSearchQuery: '',
    activeConversation: null,
    filterConversations,
    syncRoomItem() {},
    openRoomActionsMenu: () => null,
    isRoomMenuOpenFor: () => false,
    closeRoomMenu: () => true,
    registerOverlayDismiss: () => () => {},
    startRoomRename: () => {},
    deleteConversation: () => {},
    openConversation: () => {},
  };
  runInNewContext(code, context);

  context.renderRooms();
  const row = document.querySelector('.room-item');
  // An in-progress rename swaps .room-title for an input — the row then has
  // no title node, which a naive full-row refresh would dereference.
  row.dataset.renaming = '1';
  row.querySelector('.room-title').replaceWith(
    Object.assign(document.createElement('input'), { className: 'room-rename-input' }),
  );

  context.conversations[0].title = 'Changed elsewhere';
  context.renderRooms();
  assert.ok(row.querySelector('.room-rename-input'), 'rename input survives a background refresh');
  assert.equal(row.dataset.conversationId, 'c1');
});

function menuHarness({ items } = {}) {
  const dom = new JSDOM('<!doctype html><body><button id="anchor" type="button"></button></body>');
  const doc = dom.window.document;
  const anchor = doc.getElementById('anchor');
  const selected = [];
  const menu = openRoomActionsMenu({
    doc,
    anchor,
    restoreTarget: anchor,
    items: items || [
      { id: 'rename', label: 'Rename', icon: 'pencil', onSelect: () => selected.push('rename') },
      { id: 'delete', label: 'Delete', icon: 'trash-2', danger: true, onSelect: () => selected.push('delete') },
    ],
  });
  return { dom, doc, anchor, menu, selected };
}

test('room action menu exposes menu semantics, roving focus, and dismiss rules', () => {
  const { dom, doc, anchor, selected } = menuHarness();
  const menuRoot = doc.querySelector('.room-action-menu');

  assert.ok(menuRoot);
  assert.equal(menuRoot.getAttribute('role'), 'menu');
  assert.equal(menuRoot.getAttribute('aria-label'), 'Conversation actions');
  const items = [...menuRoot.querySelectorAll('[role="menuitem"]')];
  assert.equal(items.length, 2);
  assert.equal(items[1].classList.contains('is-danger'), true);
  assert.equal(doc.activeElement, items[0], 'the first item takes focus on open');
  assert.ok(isRoomMenuOpenFor(anchor));

  items[0].dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
  assert.equal(doc.activeElement, items[1], 'ArrowDown roves to the next item');
  items[1].dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
  assert.equal(doc.activeElement, items[0], 'roving wraps around');
  items[0].dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'End', bubbles: true }));
  assert.equal(doc.activeElement, items[1], 'End jumps to the last item');

  // Escape closes the menu and restores focus to the opener.
  items[1].dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  assert.equal(doc.querySelector('.room-action-menu'), null, 'Escape closes the menu');
  assert.equal(doc.activeElement, anchor, 'focus returns to the opener');
  assert.equal(isRoomMenuOpenFor(anchor), false);
  assert.deepEqual(selected, []);
});

test('room action menu Tab dismisses and hands native traversal back to the opener', () => {
  const { dom, doc, anchor } = menuHarness();
  const nextFocusable = doc.createElement('button');
  nextFocusable.type = 'button';
  anchor.after(nextFocusable);

  for (const shiftKey of [false, true]) {
    const menu = openRoomActionsMenu({
      doc,
      anchor,
      restoreTarget: anchor,
      items: [{ id: 'rename', label: 'Rename', onSelect() {} }],
    });
    const item = doc.querySelector('[role="menuitem"]');
    assert.equal(doc.activeElement, item, `menu takes focus (shiftKey=${shiftKey})`);

    const event = new dom.window.KeyboardEvent('keydown', { key: 'Tab', shiftKey, bubbles: true, cancelable: true });
    item.dispatchEvent(event);
    assert.equal(event.defaultPrevented, false, 'Tab keeps its default so the browser moves to the next/previous focusable');
    assert.equal(doc.querySelector('.room-action-menu'), null, 'Tab closes the menu');
    assert.equal(doc.activeElement, anchor,
      'focus returns to the opener before traversal — otherwise the removed menuitem leaves the browser restarting from <body>');
    assert.equal(isRoomMenuOpenFor(anchor), false);
    menu.close();
  }
});

test('room action menu selects an item, closes on outside pointerdown, and clamps to the viewport', () => {
  const { dom, doc, anchor, selected } = menuHarness();
  const menuRoot = doc.querySelector('.room-action-menu');
  const items = [...menuRoot.querySelectorAll('[role="menuitem"]')];

  // Menu is fixed-positioned inside the viewport with a small margin.
  assert.ok(menuRoot.style.left !== '' && menuRoot.style.top !== '');
  assert.ok(parseFloat(menuRoot.style.left) >= 6);
  assert.ok(parseFloat(menuRoot.style.top) >= 6);
  assert.ok(parseFloat(menuRoot.style.left) <= dom.window.innerWidth - 6);
  assert.ok(parseFloat(menuRoot.style.top) <= dom.window.innerHeight - 6);

  // Selecting an item runs the action after the menu closes.
  items[1].click();
  assert.equal(doc.querySelector('.room-action-menu'), null);
  assert.deepEqual(selected, ['delete']);

  // Reopen, then a pointerdown outside dismisses without restoring focus.
  const second = openRoomActionsMenu({ doc, anchor, items: [{ id: 'rename', label: 'Rename', onSelect: () => selected.push('rename') }] });
  assert.ok(second);
  anchor.dispatchEvent(new dom.window.MouseEvent('pointerdown', { bubbles: true }));
  assert.ok(doc.querySelector('.room-action-menu'), 'pressing the anchor does not dismiss before its click can toggle');
  doc.body.dispatchEvent(new dom.window.MouseEvent('pointerdown', { bubbles: true }));
  assert.equal(doc.querySelector('.room-action-menu'), null, 'outside pointerdown closes the menu');
  assert.notEqual(doc.activeElement, anchor, 'outside dismissal does not steal focus back');
  assert.deepEqual(selected, ['delete']);
});

test('room action menu positions from a pointer point and clamps to the viewport edge', () => {
  const dom = new JSDOM('<!doctype html><body></body>');
  const doc = dom.window.document;
  const menu = openRoomActionsMenu({ doc, point: { x: 5000, y: 5000 }, items: [{ id: 'a', label: 'A', onSelect() {} }] });
  const menuRoot = doc.querySelector('.room-action-menu');

  assert.ok(menu);
  assert.ok(parseFloat(menuRoot.style.left) <= dom.window.innerWidth - 6, 'clamped inside the right edge');
  assert.ok(parseFloat(menuRoot.style.top) <= dom.window.innerHeight - 6, 'clamped inside the bottom edge');
  closeRoomMenu();
  assert.equal(doc.querySelector('.room-action-menu'), null);
});

// --- Drawer/dock toggles are Agent-route only; overlays cannot strand -------

const NON_AGENT_ROUTES = ['home', 'skills', 'learning', 'automation', 'plugins', 'providers', 'logs', 'telemetry', 'settings'];

async function surfaceHarness({ narrow = true } = {}) {
  const dom = await conversationDom();
  const { document } = dom.window;
  const { normalizePrototypeRoute } = await import('../page/conversation/route-state.js');
  const code = await extractFunctions([
    'narrowPanels', 'setDrawerOpen', 'closeOverlayPanels', 'syncPrototypeRoute', 'resetMobileSurface', 'setActiveSurface',
    'setGlobalNavOpen', 'syncGlobalNavExpand', 'syncGlobalNavChrome',
  ]);
  const dismissals = { count: 0 };
  const context = {
    document,
    window: dom.window,
    displayedSurface: '',
    matchMedia: () => ({ matches: narrow }),
    normalizePrototypeRoute,
    dismissOpenDialogs: () => { dismissals.count += 1; },
    shell: document.querySelector('.shell'),
    roomDrawer: document.querySelector('#room-drawer'),
    dock: document.querySelector('#context-dock'),
    globalNav: document.querySelector('#global-nav'),
    globalNavToggle: document.querySelector('#global-nav-toggle'),
    globalNavExpand: document.querySelector('#global-nav-expand'),
    conversationSurface: document.querySelector('#conversation-surface'),
    homeView: document.querySelector('#home-view'),
    skillsView: document.querySelector('#skills-view'),
    pluginsView: document.querySelector('#plugins-view'),
    learningView: document.querySelector('#learning-view'),
    providersView: document.querySelector('#providers-view'),
    logsView: document.querySelector('#logs-view'),
    automationView: document.querySelector('#automation-view'),
    telemetryView: document.querySelector('#telemetry-view'),
    settingsView: document.querySelector('#settings-view'),
    roomHeadingTitle: document.querySelector('.room-heading strong'),
    homeSearch: document.querySelector('#home-search'),
    activeConversation: { id: 'conv-1', title: 'Active room' },
    skillsPage: { refresh: () => {} },
    pluginsPage: { refresh: () => {}, closeOverlays: () => {} },
    learningPage: { activate: () => {} },
    providersPage: { refresh: () => {}, closeOverlays: () => {} },
    logsPage: { refresh: () => {}, closeOverlays: () => {} },
    automationPage: { activate: () => {}, deactivate: () => {} },
    telemetryPage: { activate: () => {}, deactivate: () => {} },
    settingsPage: { activate: () => {}, deactivate: () => {} },
  };
  runInNewContext(code, context);
  return { dom, document, context, dismissals };
}

test('mobile route reentry resets detail panes while same-route refresh and filters survive', async () => {
  const { document, context } = await surfaceHarness();
  for (const route of ['skills', 'providers', 'plugins', 'logs', 'learning', 'automation']) {
    context.setActiveSurface(route);
    const view = document.querySelector(`[data-view="${route}"]`);
    const pane = view.querySelector('[data-mobile-pane]') || view;
    pane.dataset.mobilePane = 'detail';
    view.scrollTop = 320;
    const input = view.querySelector('input[type="search"]');
    if (input) input.value = 'kept filter';
    context.setActiveSurface(route);
    assert.equal(pane.dataset.mobilePane, 'detail', 'same-route updates preserve the current detail');
    context.setActiveSurface('home');
    context.setActiveSurface(route);
    assert.equal(pane.dataset.mobilePane, 'list', `${route} returns to its mobile index`);
    assert.equal(view.scrollTop, 0);
    if (input) assert.equal(input.value, 'kept filter');
  }
});

test('desktop route reentry keeps its selected detail pane', async () => {
  const { document, context } = await surfaceHarness({ narrow: false });
  context.setActiveSurface('skills');
  const pane = document.querySelector('#skills-workspace');
  pane.dataset.mobilePane = 'detail';
  context.setActiveSurface('home');
  context.setActiveSurface('skills');
  assert.equal(pane.dataset.mobilePane, 'detail');
});

test('room and dock toggles only exist on the Agent route', async () => {
  const { document, context } = await surfaceHarness();
  const css = await readFile(new URL('shell.css', root), 'utf8');

  // One shell rule keeps both conversation-panel toggles off every non-Agent
  // route — each page hides the panels themselves, so a visible toggle could
  // only raise the shared backdrop over unrelated content.
  assert.match(css, /\.shell:not\(\.agent-active\)\s+\.room-toggle/);
  assert.match(css, /\.shell:not\(\.agent-active\)\s+\.dock-toggle/);
  assert.doesNotMatch(css, /\.shell:not\(\.agent-active\)\s+\.global-nav-toggle/,
    'the global navigation toggle stays reachable on every route');

  context.setActiveSurface('agent');
  assert.equal(context.shell.classList.contains('agent-active'), true);

  for (const route of NON_AGENT_ROUTES) {
    context.setActiveSurface(route);
    assert.equal(context.shell.classList.contains('agent-active'), false, `#${route}`);
    assert.equal(document.querySelector(`[data-global-route="${route}"]`).classList.contains('is-active'), true, `#${route}`);
  }

  // Back on Agent the controls are reachable again with a consistent state.
  context.setActiveSurface('agent');
  assert.equal(context.shell.classList.contains('agent-active'), true);
  assert.equal(context.conversationSurface.hidden, false);
  const drawerToggle = document.querySelector('#room-drawer-toggle');
  assert.equal(drawerToggle.getAttribute('aria-expanded'), 'false');
  context.setDrawerOpen('rooms', true);
  assert.equal(drawerToggle.getAttribute('aria-expanded'), 'true');
  assert.equal(context.roomDrawer.hidden, false);
});

test('leaving Agent while a narrow overlay panel is open clears the backdrop state', async () => {
  const { document, context } = await surfaceHarness({ narrow: true });
  const drawerToggle = document.querySelector('#room-drawer-toggle');
  const dockToggle = document.querySelector('#context-dock-toggle');

  context.setActiveSurface('agent');
  context.setDrawerOpen('rooms', true);
  assert.equal(context.roomDrawer.hidden, false);

  for (const route of NON_AGENT_ROUTES) {
    for (const panel of ['rooms', 'dock']) {
      context.setActiveSurface('agent');
      context.setDrawerOpen(panel, true);
      context.setActiveSurface(route);
      assert.equal(context.roomDrawer.hidden, true, `#${route} (open ${panel}): room drawer overlay must close`);
      assert.equal(context.dock.hidden, true, `#${route} (open ${panel}): dock overlay must close`);
      assert.equal(context.shell.classList.contains('rooms-closed'), true, `#${route} (open ${panel})`);
      assert.equal(context.shell.classList.contains('dock-closed'), true, `#${route} (open ${panel})`);
      assert.equal(drawerToggle.getAttribute('aria-expanded'), 'false', `#${route} (open ${panel})`);
      assert.equal(dockToggle.getAttribute('aria-expanded'), 'false', `#${route} (open ${panel})`);
      assert.equal(document.querySelector(`[data-global-route="${route}"]`).classList.contains('is-active'), true);
    }
  }
});

test('desktop panel state survives a route round trip', async () => {
  const { context } = await surfaceHarness({ narrow: false });

  context.setActiveSurface('agent');
  context.setDrawerOpen('rooms', true);
  context.setDrawerOpen('dock', true);
  context.setActiveSurface('home');
  // Desktop panels are in-flow columns hidden by the *-active rules, not
  // overlays — their open state must not be reset by the route change.
  assert.equal(context.roomDrawer.hidden, false);
  assert.equal(context.dock.hidden, false);
  context.setActiveSurface('agent');
  assert.equal(context.shell.classList.contains('agent-active'), true);
  assert.equal(context.roomDrawer.hidden, false, 'returning to Agent restores the still-open drawer');
});

// --- Rail footer affordance: close on the overlay, mode toggle in flow ------

test('global nav footer closes the overlay rail at narrow widths and keeps the mode toggle in flow', async () => {
  const dom = await conversationDom();
  const { document } = dom.window;
  const code = await extractFunctions(['narrowPanels', 'setDrawerOpen', 'setGlobalNavOpen', 'setGlobalNavExpanded', 'syncGlobalNavExpand', 'syncGlobalNavChrome']);
  const store = {};
  const context = {
    document,
    window: dom.window,
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
    matchMedia: () => ({ matches: true }),
  };
  runInNewContext(code, context);
  const expand = document.querySelector('#global-nav-expand');
  const label = expand.querySelector('.global-nav-expand-label');
  const nav = document.querySelector('#global-nav');
  const navToggle = document.querySelector('#global-nav-toggle');

  // The overlay rail always renders labels, so the expand/collapse mode
  // toggle has no visible effect — the footer becomes the drawer's close
  // affordance instead, with aria-expanded tracking the drawer itself.
  context.setGlobalNavOpen(true);
  assert.equal(nav.hidden, false);
  assert.equal(navToggle.getAttribute('aria-expanded'), 'true');
  assert.equal(expand.getAttribute('aria-label'), 'Close sidebar');
  assert.equal(expand.title, 'Close sidebar');
  assert.equal(label.textContent, 'Close sidebar');
  assert.equal(expand.getAttribute('aria-expanded'), 'true');

  context.setGlobalNavOpen(false);
  assert.equal(nav.hidden, true);
  assert.equal(context.shell.classList.contains('global-nav-closed'), true);
  assert.equal(expand.getAttribute('aria-expanded'), 'false');
  assert.equal(store['nusashell.sidebarMode'], undefined,
    'closing the overlay never rewrites the desktop rail preference');

  // In-flow widths keep the persisted expand/collapse affordance.
  context.matchMedia = () => ({ matches: false });
  context.setGlobalNavExpanded(true);
  assert.equal(label.textContent, 'Collapse sidebar');
  assert.equal(expand.getAttribute('aria-expanded'), 'true');
  assert.equal(store['nusashell.sidebarMode'], 'full');
  context.setGlobalNavExpanded(false);
  assert.equal(label.textContent, 'Expand sidebar');
  assert.equal(expand.getAttribute('aria-expanded'), 'false');
  assert.equal(store['nusashell.sidebarMode'], 'icons');
});

test('the rail footer click routes through the narrow overlay path', async () => {
  // The affordance labels above only hold if the click handler itself
  // branches: narrowPanels() must win before the mode toggle runs.
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const handler = app.match(/globalNavExpand\?\.addEventListener\('click'[\s\S]*?\}\);/);
  assert.ok(handler, 'the expand click handler exists');
  assert.ok(handler[0].indexOf('narrowPanels()') < handler[0].indexOf('setGlobalNavExpanded'),
    'narrow overlays close the drawer instead of toggling the rail mode');
  // The rail's [hidden] flips in exactly one place — setGlobalNavOpen — now
  // that the shell-init is-nav-open bridge is gone with the titlebar
  // hamburger. One writer means the footer aria can never drift.
  assert.equal(app.match(/globalNav\.hidden\s*=[^=]/g)?.length, 1,
    'setGlobalNavOpen is the only writer of globalNav[hidden]');
  assert.doesNotMatch(app, /observe\(globalNav/,
    'no observer watches a flip seam that no longer exists');
});

// --- Hidden-room metadata refresh on turn completion ------------------------

function lifecycleHarness() {
  const handlers = {};
  const calls = { roomRefreshes: [], activity: [], snapshots: [], settles: 0 };
  return { handlers, calls };
}

async function mountLifecycleHandlers(calls, handlers) {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const helpers = await extractFunctions(['roundFromLifecycle', 'roundKey']);
  const blocks = [];
  for (const eventName of ['agent.turn.done', 'agent.turn.error']) {
    const marker = `on('${eventName}',`;
    const start = app.indexOf(marker);
    assert.ok(start >= 0, `expected ${eventName} handler in app.js`);
    blocks.push(app.slice(start, app.indexOf('\n});', start) + 4));
  }
  const context = {
    on: (name, fn) => { handlers[name] = fn; },
    agentTurnSound: () => '',
    playComplete: () => {},
    playError: () => {},
    soundNotificationsEnabled: false,
    conversations: [{ id: 'conv-hidden', title: 'New conversation', status: 'running', updated_at: '2026-10-13T00:00:00Z' }],
    activeConversation: { id: 'conv-active' },
    stoppingTurn: false,
    settledRoundKeys: new Set(),
    syncRoomActivity: (id, status) => calls.activity.push([id, status]),
    refreshConversationRooms: async (id) => { calls.roomRefreshes.push(id); },
    refreshConversationSnapshot: async (id) => { calls.snapshots.push(id); return []; },
    settleConversation: () => { calls.settles += 1; },
    renderContextDock: () => {},
    renderRooms: () => {},
    compactionStatus: { clear: () => {} },
    hasLiveRoundStream: () => false,
    openRoundStream: () => {},
    reconcileActiveRound: () => Promise.resolve(false),
    applyTurnError: () => null,
    events: [],
    commitLiveTurn: () => {},
    flushLiveWidgets: () => {},
    queueLiveWidget: () => {},
    syncToolJobStrip: () => {},
    syncComposerAction: () => {},
    toast: () => {},
    stream: { setAttribute: () => {}, querySelectorAll: () => [] },
    providerRetryTimer: null,
    window: {},
    console,
  };
  runInNewContext(`${helpers}\n${blocks.join('\n')}`, context);
}

test('turn.done on a non-active room refreshes its sidebar metadata', async () => {
  const { handlers, calls } = lifecycleHarness();
  await mountLifecycleHandlers(calls, handlers);
  const done = handlers['agent.turn.done'];
  assert.ok(done, 'agent.turn.done handler is registered');

  done({ conversation_id: 'conv-hidden', run_id: 'run-1', message_id: 'msg-1' });
  assert.deepEqual(calls.roomRefreshes, ['conv-hidden'],
    'a hidden room must refresh title/updated_at through the list RPC');
  assert.deepEqual(calls.snapshots, [], 'the active-room snapshot path stays out of hidden rooms');
  assert.equal(calls.settles, 0);
});

test('turn.error on a non-active room refreshes its sidebar metadata', async () => {
  const { handlers, calls } = lifecycleHarness();
  await mountLifecycleHandlers(calls, handlers);
  const fail = handlers['agent.turn.error'];
  assert.ok(fail, 'agent.turn.error handler is registered');

  fail({ conversation_id: 'conv-hidden', run_id: 'run-1', message_id: 'msg-1', error: 'boom' });
  assert.deepEqual(calls.roomRefreshes, ['conv-hidden'],
    'a hidden room erroring out must also refresh its sidebar row');
  assert.deepEqual(calls.snapshots, []);
});

test('entering Agent refreshes an initialized model catalog once per route entry', async () => {
  const { dom, context } = await surfaceHarness();
  let refreshes = 0;
  context.refreshModelCatalog = async () => { refreshes += 1; };
  context.setActiveSurface('agent');
  assert.equal(refreshes, 0, 'initial boot owns the first catalog load');
  context.window.__widgetModels = [];
  context.setActiveSurface('providers');
  context.setActiveSurface('agent');
  assert.equal(refreshes, 1);
  context.setActiveSurface('agent');
  assert.equal(refreshes, 1, 'same-route updates do not refetch the catalog');
  dom.window.close();
});
