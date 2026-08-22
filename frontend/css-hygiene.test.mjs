// CSS architecture guard rails.
//
// NusaShell keeps stylesheets layered: global.css owns tokens, resets, and
// genuinely shared primitives (used by 2+ views). View-specific rules live in
// that view's own stylesheet, and shared components must not be hosted inside
// another view's file. These tests pin that contract so "helper" rules stop
// leaking into the wrong layer and silently breaking other views (the final
// cascade also includes the parity overlay, which made such leaks very hard
// to trace).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), 'styles');
const read = (file) => readFileSync(join(root, file), 'utf8');

const countOccurrences = (haystack, needle) => haystack.split(needle).length - 1;

test('global.css is a primitives layer: no single-view component rules', () => {
  const css = read('global.css');
  // Settings is the only consumer of these selectors; they belong in settings.css.
  assert.ok(!css.includes('.settings-field'), '.settings-field must live in settings.css');
  assert.ok(!css.includes('.settings-body'), '.settings-body overrides must live in settings.css');
  assert.ok(!css.includes('.view.active.settings-view'), 'settings view layout must live in settings.css');
});

test('shell chrome stylesheets do not host other views\u2019 rules', () => {
  // layout.css owns the app frame (titlebar, sidebar, content); it must not
  // accumulate per-view sections like the Settings workspace once did.
  const layout = read('layout.css');
  assert.ok(!layout.includes('.settings-'), '.settings-* rules belong in settings.css, not layout.css');
});

test('shared components are not hosted inside a single view stylesheet', () => {
  const home = read('home.css');
  assert.ok(!home.match(/\.tab\s*[{,:]/), '.tab is shared (Home + Learning); define it once in global.css');
});

// The Learning search bar used to fake a "connected card" by zeroing the
// border radius of the search bar and cutting the workspace top border.
// Split components must keep their own borders/radius and be spaced with gaps.
test('learning.css keeps split components self-contained (no connected-card hacks)', () => {
  const css = read('learning.css');
  assert.ok(!css.match(/border-(top|bottom)-(left|right)-radius:\s*0/), 'do not zero component corners to glue panels together');
  assert.ok(!css.match(/\.learning-workspace[^}]*border-top:\s*none/s), 'workspace keeps its own top border');
});

test('.tab is defined exactly once, in the shared layer', () => {
  let total = 0;
  for (const file of ['global.css', 'home.css', 'learning.css']) {
    total += countOccurrences(read(file), '.tab {');
  }
  assert.equal(total, 1, `expected exactly one .tab definition, found ${total}`);
});

const cssFiles = readdirSync(root).filter((f) => f.endsWith('.css'));

test('every stylesheet declares its layer in a leading header', () => {
  for (const file of cssFiles) {
    const css = read(file);
    assert.ok(css.trimStart().startsWith('/*'), `${file} must open with a header comment`);
    assert.ok(css.slice(0, 500).includes('Layer:'), `${file} header must declare "Layer:" so the cascade contract is readable without browser inspection`);
  }
});

test('parity overlay stays the last NusaShell-authored stylesheet', () => {
  // parity.css intentionally re-skins shared primitives; that contract only
  // holds while it loads after every other authored sheet.
  const html = readFileSync(join(root, '..', 'index.html'), 'utf8');
  const order = [...html.matchAll(/styles\/([\w-]+)\.css/g)].map((m) => m[1]);
  assert.ok(order.length > 0, 'expected stylesheet links in index.html');
  assert.equal(order[order.length - 1], 'parity', `parity.css must be last, found order: ${order.join(' -> ')}`);
});
