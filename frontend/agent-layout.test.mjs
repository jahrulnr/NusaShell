import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const parityCSS = await readFile(new URL('./styles/electron-parity.css', import.meta.url), 'utf8');
const agentView = await readFile(new URL('./js/views/agent.js', import.meta.url), 'utf8');

test('Agent uses the Electron workspace shell without unsupported Todo UI', () => {
  assert.match(html, /class="agent-shell" id="agent-shell"/);
  assert.match(html, /class="agent-conversations" id="agent-conversations"/);
  assert.match(html, /class="agent-thread" id="agent-thread" role="log"/);
  assert.doesNotMatch(html, /class="agent-header"/);
  assert.doesNotMatch(html, /agent-task-strip/);
  const tabletRules = parityCSS.slice(
    parityCSS.indexOf('@media (max-width: 900px)'),
    parityCSS.indexOf('@media (max-width: 760px)'),
  );
  assert.match(tabletRules, /\.agent-conversations \{ display: none; \}/);
});

test('Agent tool transcripts start collapsed, cap output at ten lines, and render reasoning as Markdown', () => {
  assert.match(agentView, /class: 'agent-tool-terminal'/);
  assert.match(agentView, /el\('details', \{ class: 'agent-tool-terminal'/);
  assert.match(agentView, /content\.innerHTML = renderMarkdown\(reasoning\)/);
  assert.match(agentView, /content\.innerHTML = renderMarkdown\(run\.rawReasoning\)/);
  assert.match(parityCSS, /\.agent-tool-terminal-output \{[^}]*max-height: calc\(10 \* 1\.55em\);[^}]*overflow-y: auto;/s);
});

test('Thinking and tool markers share the same conversation rail', () => {
  assert.match(parityCSS, /\.agent-reasoning summary \{[^}]*margin-left: -12px;/s);
  assert.match(parityCSS, /\.agent-tool-terminal summary \{[^}]*margin-left: -12px;/s);
});
