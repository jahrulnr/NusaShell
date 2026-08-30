// CSS architecture guard rails.
//
// NusaShell keeps stylesheets layered: global.css owns tokens, resets, and
// genuinely shared primitives (used by 2+ views). View-specific rules live in
// that view's own stylesheet, and shared components must not be hosted inside
// another view's file. These tests pin that contract so "helper" rules stop
// leaking into the wrong layer and silently breaking other views. The old
// parity.css/responsive.css overlay layers were dissolved into the owning
// stylesheets; these tests also guard that they do not return.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync, readdirSync, existsSync } from 'node:fs';
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

test('anonymous overlay stylesheets are dissolved into owner files', () => {
  // parity.css and responsive.css were cross-cutting overlays loaded last,
  // which made the cascade ambiguous and leaks hard to trace. They must not
  // return as files or as <link> tags in index.html.
  const html = readFileSync(join(root, '..', 'index.html'), 'utf8');
  assert.ok(!html.includes('styles/parity.css'), 'parity.css link must be removed from index.html');
  assert.ok(!html.includes('styles/responsive.css'), 'responsive.css link must be removed from index.html');
  assert.ok(!existsSync(join(root, 'parity.css')), 'parity.css file must be deleted');
  assert.ok(!existsSync(join(root, 'responsive.css')), 'responsive.css file must be deleted');
});

test('document-wide reset, focus, and reduced-motion live in global.css', () => {
  const css = read('global.css');
  assert.match(css, /:where\(button,[^)]*\):focus-visible/, 'shared focus-visible reset belongs in global.css');
  assert.match(css, /prefers-reduced-motion: reduce\)[\s\S]*animation-duration: \.001ms !important/, 'document-wide reduced-motion belongs in global.css');
});

test('shell chrome and mobile-nav drawer breakpoints live in layout.css', () => {
  const css = read('layout.css');
  assert.match(css, /@media \(max-width: 1100px\)[\s\S]*?\.sidebar \{ width: 220px/, '1100px sidebar breakpoint belongs in layout.css');
  assert.match(css, /@media \(max-width: 900px\)[\s\S]*?\.sidebar \{ width: 68px/, '900px sidebar collapse belongs in layout.css');
  assert.match(css, /@media \(max-width: 680px\)[\s\S]*?\.window \.sidebar \{/, '680px off-canvas drawer belongs in layout.css');
  assert.match(css, /\.mobile-nav-toggle \{ display: none; \}/, 'mobile-nav toggle base rule belongs in layout.css');
});

test('Agent parity and responsive rules live in agent.css', () => {
  const css = read('agent.css');
  assert.match(css, /\.agent-tool-terminal-output \{[\s\S]*?max-height: calc\(10 \* 1\.55em\)/, 'agent tool terminal cap belongs in agent.css');
  assert.match(css, /\.agent-subagent-card \{/, 'subagent card belongs in agent.css');
  assert.match(css, /@media \(max-width: 900px\)[\s\S]*?\.agent-conversations \{ display: none; \}/, '900px agent conversations hide belongs in agent.css');
});

test('view parity and responsive rules live in their owner stylesheets', () => {
  assert.match(read('mcp.css'), /@media \(max-width: 680px\)[\s\S]*?\.mcp-row \{ align-items: flex-start/, '680px mcp-row wrap belongs in mcp.css');
  assert.match(read('providers.css'), /@media \(max-width: 480px\)[\s\S]*?\.provider-detail-grid \{ grid-template-columns: 1fr/, '480px provider grid belongs in providers.css');
  assert.match(read('logs.css'), /@media \(max-width: 480px\)[\s\S]*?\.log-line \{ grid-template-columns: 76px/, '480px log-line grid belongs in logs.css');
  assert.match(read('settings.css'), /@media \(max-width: 760px\)[\s\S]*?\.settings-group \{ grid-template-columns: 1fr/, '760px settings-group collapse belongs in settings.css');
  assert.match(read('skills.css'), /@media \(max-width: 760px\)[\s\S]*?\.skills-catalog\s*\{[\s\S]*?max-height:\s*260px/, '760px skills catalog collapse belongs in skills.css');
});
