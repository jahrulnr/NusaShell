import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const parityCSS = await readFile(new URL('./styles/electron-parity.css', import.meta.url), 'utf8');
const agentView = await readFile(new URL('./js/views/agent.js', import.meta.url), 'utf8');
const appShell = await readFile(new URL('./js/app.js', import.meta.url), 'utf8');
const agentRender = await readFile(new URL('./js/views/agent/render.js', import.meta.url), 'utf8');
const agentComposer = await readFile(new URL('./js/views/agent/composer.js', import.meta.url), 'utf8');
const agentModelPicker = await readFile(new URL('./js/views/agent/model-picker.js', import.meta.url), 'utf8');

test('Agent uses the Electron workspace shell without unsupported Todo UI', () => {
  assert.match(html, /class="agent-shell" id="agent-shell"/);
  assert.match(html, /class="agent-conversations" id="agent-conversations"/);
  assert.match(html, /class="agent-thread" id="agent-thread" role="log"/);
  assert.doesNotMatch(html, /class="agent-header"/);
  assert.doesNotMatch(html, /agent-task-strip/);
  // The todo checklist strip uses a distinct class name (agent-todo-strip).
  assert.match(html, /class="agent-todo-strip" id="agent-todo-strip"/);
  assert.match(html, /id="agent-todo-strip-list"/);
  assert.match(html, /id="agent-todo-strip-summary"/);
  const tabletRules = parityCSS.slice(
    parityCSS.indexOf('@media (max-width: 900px)'),
    parityCSS.indexOf('@media (max-width: 760px)'),
  );
  assert.match(tabletRules, /\.agent-conversations \{ display: none; \}/);
});

test('Agent tool transcripts start collapsed, cap output at ten lines, and render reasoning as Markdown', () => {
  assert.match(agentRender, /class: 'agent-tool-terminal'/);
  assert.match(agentRender, /el\('details', \{ class: 'agent-tool-terminal'/);
  assert.match(agentRender, /content\.innerHTML = renderMarkdown\(reasoning\)/);
  assert.match(agentView, /content\.innerHTML = renderMarkdown\(run\.rawReasoning\)/);
  assert.match(parityCSS, /\.agent-tool-terminal-output \{[^}]*max-height: calc\(10 \* 1\.55em\);[^}]*overflow-y: auto;/s);
});

test('Agent delegates presentation, composer, and model picker responsibilities to focused modules', () => {
  assert.match(agentView, /from '\.\/agent\/render\.js'/);
  assert.match(agentView, /from '\.\/agent\/composer\.js'/);
  assert.match(agentView, /from '\.\/agent\/model-picker\.js'/);
  assert.match(agentComposer, /export function bindComposer/);
  assert.match(agentModelPicker, /export function bindModelPicker/);
});

test('Thinking and tool markers share the same conversation rail', () => {
  assert.match(parityCSS, /\.agent-reasoning summary \{[^}]*margin-left: -12px;/s);
  assert.match(parityCSS, /\.agent-tool-terminal summary \{[^}]*margin-left: -12px;/s);
});

test('Agent surfaces an unavailable backend promptly instead of waiting for a normal RPC timeout', () => {
  assert.match(appShell, /rpc\('app\.info', \{\}, \{ timeoutMs: 4000 \}\)/);
});

test('Agent todo strip has race protection via render token and event filtering', () => {
  // Render token guards against stale async fetches (room switch during fetch)
  assert.match(agentView, /todoRenderToken/);
  assert.match(agentView, /token !== state\.todoRenderToken/);
  // Event handler filters by active conversation to prevent cross-room leaks
  assert.match(agentView, /on\('agent\.todo\.updated'/);
  assert.match(agentView, /conversation_id !== state\.activeId/);
  // Delete button is disabled during RPC to prevent double-clicks
  assert.match(agentView, /btn\.disabled = true/);
  assert.match(agentView, /btn\.disabled = false/);
  // Todos are cleared on conversation delete and create
  assert.match(agentView, /state\.todos = \{ items: \[\], summary/);
});

test('Agent todo strip render function is exported from render module', () => {
  assert.match(agentRender, /export function renderTodoItem/);
  assert.match(agentView, /renderTodoItem/);
});
