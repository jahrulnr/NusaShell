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

const root = join(dirname(fileURLToPath(import.meta.url)), '..', 'styles');
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
  // layout.css owns the app frame (mobile bar, sidebar, content); it must not
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
    total += read(file).match(/^\.tab(?![\w-])\s*\{/gm)?.length ?? 0;
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

test('desktop shell chrome lives in the sidebar while mobile keeps a navigation bar', () => {
  const css = read('layout.css');
  assert.match(css, /\.titlebar \{\s*display: none;/,
    'desktop should reclaim the empty titlebar row');
  assert.match(css, /@media \(max-width: 680px\)[\s\S]*?\.titlebar \{[\s\S]*?height: 58px;[\s\S]*?display: flex;/,
    'mobile titlebar keeps a compact touch-friendly navigation bar');
  assert.match(css, /\.sidebar-brand \{/, 'sidebar owns the shell identity');
  assert.match(css, /\.sidebar-actions \{/, 'sidebar owns global utility controls');
});

test('Agent parity and responsive rules live in agent.css', () => {
  const css = read('agent.css');
  assert.match(css, /\.agent-subagent-card \{/, 'subagent card belongs in agent.css');
  assert.match(css, /@media \(max-width: 900px\)[\s\S]*?\.agent-conversations \{[\s\S]*?display: none;/, '900px agent conversations hide belongs in agent.css');
});

test('built-in tool cards use the contract boundary for shared dressing and per-tool identity', () => {
  const agent = read('agent.css');
  const tools = read('agent-tools.css');
  assert.match(tools, /details\.agent-tool-event\[data-tool\] \{/, 'tool event chrome is owned by the tool stylesheet');
  assert.match(tools, /\.agent-tool-event \.agent-tool-request \{/, 'request styling uses the canonical request hook');
  assert.match(tools, /\.agent-tool-event \.agent-tool-result \{/, 'result styling uses the canonical result hook');
  assert.match(tools, /\.agent-tool-card\[data-tool="file_read"\] \{/, 'file_read has an isolated contract accent');
  assert.match(tools, /\.agent-tool-card\[data-tool="grep"\] \{/, 'grep has an isolated contract accent');
  assert.match(tools, /\.agent-tool-card\[data-tool="exec"\] \{/, 'exec has an isolated contract accent');
  assert.match(tools, /\.agent-tool-card\[data-tool="todo"\] \{/, 'todo has an isolated contract accent');
  const builtinRoster = [
    'ask_question', 'automation', 'automation_schedule',
    'contract_read', 'delegate', 'docs', 'exec', 'file_copy', 'file_delete',
    'file_info', 'file_list', 'file_mkdir', 'file_move', 'file_patch', 'file_read',
    'file_write', 'find_file', 'generate_media', 'grep', 'mcp_call', 'mcp_disable',
    'mcp_enable', 'mcp_install', 'mcp_list', 'mcp_register', 'mcp_search',
    'mcp_server_add', 'mcp_unregister', 'memory', 'memory_project', 'read_media',
    'show', 'skill', 'sleep', 'subagent',
    'subagent_steer', 'subagent_stop', 'subagent_wait', 'todo', 'tool_list',
    'tool_schema', 'wait_until', 'web_answer', 'web_fetch', 'web_search',
  ];
  for (const name of builtinRoster) {
    assert.match(tools, new RegExp(`\\.agent-tool-card\\[data-tool="${name}"\\] \\{`),
      `${name} has an explicit contract accent owner`);
  }
  assert.doesNotMatch(agent, /\.agent-tool-terminal \{/, 'obsolete terminal component styles must not compete with agent-tool-event');
  assert.doesNotMatch(agent, /\.agent-tool-job-card \{/, 'obsolete job card styles must not compete with agent-tool-event');
  assert.doesNotMatch(tools, /\.agent-tool-file-read-path \{/, 'old per-tool path tabs are removed in favor of the contract boundary');
  const html = readFileSync(join(root, '..', 'index.html'), 'utf8');
  assert.match(html, /styles\/agent-tools\.css/, 'agent-tools.css is loaded after agent.css');
});

test('view parity and responsive rules live in their owner stylesheets', () => {
  assert.match(read('mcp.css'), /@media \(max-width: 680px\)[\s\S]*?\.mcp-row \{ align-items: flex-start/, '680px mcp-row wrap belongs in mcp.css');
  assert.match(read('providers.css'), /@media \(max-width: 480px\)[\s\S]*?\.provider-detail-grid \{ grid-template-columns: 1fr/, '480px provider grid belongs in providers.css');
  assert.match(read('logs.css'), /@media \(max-width: 480px\)[\s\S]*?\.log-line \{ grid-template-columns: 76px/, '480px log-line grid belongs in logs.css');
  assert.match(read('settings.css'), /@media \(max-width: 760px\)[\s\S]*?\.settings-group \{ grid-template-columns: 1fr/, '760px settings-group collapse belongs in settings.css');
  assert.match(read('skills.css'), /@media \(max-width: 760px\)[\s\S]*?\.skills-catalog\s*\{[\s\S]*?max-height:\s*260px/, '760px skills catalog collapse belongs in skills.css');
});
