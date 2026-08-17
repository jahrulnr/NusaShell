import assert from 'node:assert/strict';
import { test } from 'node:test';

import { openPluginWindow, windowFeatures } from './js/plugin-window.js';

test('windowFeatures sizes panel/widget and defers fullscreen to the browser', () => {
  const panel = windowFeatures({ mode: 'panel', defaultSize: { width: 820, height: 640 } });
  assert.match(panel, /popup=yes/);
  assert.match(panel, /width=820/);
  assert.match(panel, /height=640/);

  assert.match(windowFeatures({ mode: 'widget' }), /width=380/);
  assert.match(windowFeatures(), /width=700/); // default is panel 700x700
  // Fullscreen opens a normal browser window/tab — no popup size features.
  assert.equal(windowFeatures({ mode: 'fullscreen' }), '');
});

test('openPluginWindow opens the plugin URL in a separate window (not an overlay)', () => {
  const calls = [];
  const fakeWin = { focus: () => calls.push('focus'), document: {} };
  globalThis.window = { open: (url, target, features) => { calls.push({ url, target, features }); return fakeWin; } };
  try {
    const ok = openPluginWindow({ id: 'nusashell.notes', name: 'Notes', ui: { window: { mode: 'panel' } } });
    assert.equal(ok, true);
    assert.equal(calls[0].url, '/plugins/nusashell.notes/');
    assert.equal(calls[0].target, 'nusashell-plugin:nusashell.notes');
    assert.match(calls[0].features, /popup=yes/);
    assert.ok(calls.includes('focus'), 'the opened window should be focused');
    assert.equal(fakeWin.document.title, 'Notes');
  } finally {
    delete globalThis.window;
  }
});

test('openPluginWindow reports failure when the pop-up is blocked', () => {
  globalThis.window = { open: () => null };
  try {
    assert.equal(openPluginWindow({ id: 'p1', name: 'P', ui: {} }), false);
  } finally {
    delete globalThis.window;
  }
});

test('openPluginWindow ignores an empty id', () => {
  let opened = false;
  globalThis.window = { open: () => { opened = true; return {}; } };
  try {
    assert.equal(openPluginWindow({ id: '', name: 'x' }), false);
    assert.equal(opened, false);
  } finally {
    delete globalThis.window;
  }
});
