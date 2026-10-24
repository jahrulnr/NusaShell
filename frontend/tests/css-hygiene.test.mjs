// CSS architecture guard rails.
//
// The widget shell loads per-view stylesheets from the repository root
// (skills.css, plugins.css, …) plus the shell chrome in shell.css; the
// styles/ subtree hosts only the ported legacy sheets that index.html still
// links (agent.css, agent-tools.css, acp.css, modals.css, pairing.css).
// Anything else under styles/ is unreachable dead code — these tests pin the
// link graph so unreferenced sheets cannot accumulate again.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync, readdirSync, existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', 'styles');
const frontendRoot = join(root, '..');
const read = (file) => readFileSync(join(root, file), 'utf8');
const html = readFileSync(join(frontendRoot, 'index.html'), 'utf8');

// Stylesheet hrefs in index.html: "styles/x.css?v=N", "./x.css?v=N", or
// bare "x.css" — normalize to a frontend-relative path.
function linkedStylesheets() {
  const hrefs = [];
  for (const m of html.matchAll(/<link[^>]+rel="stylesheet"[^>]+href="([^"]+)"/g)) {
    const href = m[1].split('?')[0];
    if (/^https?:/.test(href) || !href.endsWith('.css')) continue;
    hrefs.push(href.replace(/^\.\//, ''));
  }
  return hrefs;
}

test('every stylesheet linked by index.html exists on disk', () => {
  for (const href of linkedStylesheets()) {
    assert.ok(existsSync(join(frontendRoot, href)), `linked stylesheet is missing: ${href}`);
  }
});

test('styles/ contains only sheets that index.html links', () => {
  const linked = new Set(linkedStylesheets());
  for (const file of readdirSync(root).filter((f) => f.endsWith('.css'))) {
    assert.ok(linked.has(`styles/${file}`), `styles/${file} is not linked by index.html — orphan dead code`);
  }
});

test('anonymous overlay stylesheets are dissolved into owner files', () => {
  // parity.css and responsive.css were cross-cutting overlays loaded last,
  // which made the cascade ambiguous and leaks hard to trace. They must not
  // return as files or as <link> tags in index.html.
  assert.ok(!html.includes('parity.css'), 'parity.css link must not return to index.html');
  assert.ok(!html.includes('responsive.css'), 'responsive.css link must not return to index.html');
  for (const dir of [root, frontendRoot]) {
    assert.ok(!existsSync(join(dir, 'parity.css')), 'parity.css file must not return');
    assert.ok(!existsSync(join(dir, 'responsive.css')), 'responsive.css file must not return');
  }
});

test('every stylesheet under styles/ declares its layer in a leading header', () => {
  for (const file of readdirSync(root).filter((f) => f.endsWith('.css'))) {
    const css = read(file);
    assert.ok(css.trimStart().startsWith('/*'), `${file} must open with a header comment`);
    assert.ok(css.slice(0, 500).includes('Layer:'), `${file} header must declare "Layer:" so the cascade contract is readable without browser inspection`);
  }
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
  assert.match(html, /styles\/agent-tools\.css/, 'agent-tools.css is loaded after agent.css');
});

test('view parity and responsive rules live in their owner stylesheets', () => {
  assert.match(readFileSync(join(frontendRoot, 'providers.css'), 'utf8'), /@media \(max-width: 980px\)[\s\S]*?\.providers-detail-grid \{ grid-template-columns: minmax\(0, 1fr\)/, '980px provider grid belongs in providers.css');
});
