import assert from 'node:assert/strict';
import { test } from 'node:test';

import { openPluginWindow, windowFeatures } from '../js/plugin-window.js';

function feat(str, key) {
  const m = new RegExp(`${key}=(\\d+)`).exec(str);
  return m ? Number(m[1]) : null;
}

test('windowFeatures always requests a separate popup window (never a bare tab)', () => {
  const screen = { width: 1600, height: 900 };
  // Every mode yields a real window: popup=yes with explicit size + position.
  for (const cfg of [{ mode: 'fullscreen' }, { mode: 'panel' }, { mode: 'widget' }, {}]) {
    const f = windowFeatures(cfg, screen);
    assert.match(f, /popup=yes/, `popup for ${JSON.stringify(cfg)}`);
    assert.ok(feat(f, 'width') > 0 && feat(f, 'height') > 0, 'has explicit size');
    assert.ok(Number.isInteger(feat(f, 'left')) && Number.isInteger(feat(f, 'top')), 'centered');
  }
});

test('windowFeatures honors a declared size and clamps it to the screen', () => {
  const f = windowFeatures({ mode: 'panel', defaultSize: { width: 820, height: 640 } }, { width: 1600, height: 900 });
  assert.equal(feat(f, 'width'), 820);
  assert.equal(feat(f, 'height'), 640);
  // Oversized declaration is clamped to the screen.
  const big = windowFeatures({ mode: 'panel', defaultSize: { width: 5000, height: 4000 } }, { width: 1280, height: 800 });
  assert.equal(feat(big, 'width'), 1280);
  assert.equal(feat(big, 'height'), 800);
});

test('windowFeatures picks a dynamic panel size that scales with the screen', () => {
  const small = windowFeatures({ mode: 'panel' }, { width: 1280, height: 800 });
  const large = windowFeatures({ mode: 'panel' }, { width: 2560, height: 1440 });
  // No declared size → the default width scales with the screen (capped at 1100).
  assert.ok(feat(large, 'width') >= feat(small, 'width'), 'wider screen → wider default');
  assert.ok(feat(small, 'width') <= 1100 && feat(large, 'width') <= 1100, 'capped');
  // Fullscreen uses the whole screen.
  const fs = windowFeatures({ mode: 'fullscreen' }, { width: 1600, height: 900 });
  assert.equal(feat(fs, 'width'), 1600);
  assert.equal(feat(fs, 'height'), 900);
  // Widget stays compact.
  assert.equal(feat(windowFeatures({ mode: 'widget' }, { width: 1600, height: 900 }), 'width'), 380);
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
