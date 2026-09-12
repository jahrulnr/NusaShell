import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { bindSettingsSectionNavigation } from '../js/views/settings.js';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const settingsView = await readFile(new URL('../js/views/settings.js', import.meta.url), 'utf8');
const agentView = await readFile(new URL('../js/views/agent.js', import.meta.url), 'utf8');
const rpc = await readFile(new URL('../js/rpc.js', import.meta.url), 'utf8');

test('Settings exposes the Go-supported Electron parity controls', () => {
  for (const id of [
    'settings-max-tool-rounds',
    'settings-max-parallel-tools',
    'settings-preferred-model',
    'settings-pets-auto-start',
    'settings-pet-action-btn',
    'settings-pet-dot',
    'settings-data-dir',
    'settings-sidebar-compact',
    'settings-image-model',
    'settings-delegate-model',
  ]) {
    assert.match(html, new RegExp(`id="${id}"`));
  }
  assert.match(settingsView, /rpc\('app\.info'/);
  assert.match(settingsView, /rpc\('ai\.models\.list'/);
  assert.match(settingsView, /settings\.pets_status/);
  assert.match(settingsView, /pets_auto_start/);
  assert.match(settingsView, /Stop pet/);
  // settings.set is a flat pointer DTO, not a nested { settings } blob.
  // Wrapping GET's settings object silently drops pets_auto_start.
  assert.match(settingsView, /rpc\('settings\.set', \{ pets_auto_start: desired \}\)/);
  assert.match(settingsView, /max_tool_rounds/);
  assert.match(settingsView, /max_parallel_tools/);
  assert.match(settingsView, /plugin_contract_mode/);
  assert.match(settingsView, /delegate_model/);
  assert.match(settingsView, /delegateSelect\.getSelected/);
});


test('Context compaction exposes the opt-in reuse workflow and sends it to settings.set', () => {
  assert.match(html, /id="settings-compaction-workflow"/);
  assert.match(html, /value="dedicated"/);
  assert.match(html, /value="reuse"/);
  assert.match(settingsView, /compaction_workflow/);
  assert.match(settingsView, /settings-compaction-workflow/);
  assert.match(settingsView, /CompactionWorkflowReuse|reuse/);
});

test('Settings refresh preserves the compaction enabled toggle before save', () => {
  const refresh = settingsView.slice(
    settingsView.indexOf('export async function refresh()'),
    settingsView.indexOf('function setOptionalNumber'),
  );
  assert.match(
    refresh,
    /getElementById\('settings-compaction-enabled'\)\.checked = settings\.compaction_enabled !== false/,
  );
});


test('Context fallback window keeps the full settings-field wrapper', () => {
  const dom = new JSDOM(html);
  const input = dom.window.document.querySelector('#settings-max-input-tokens');
  const field = input?.closest('.settings-field');
  assert.ok(field, 'fallback context window must stay inside a settings field');
  assert.equal(field?.getAttribute('for'), 'settings-max-input-tokens');
  assert.ok(field?.querySelector('small'), 'fallback context window needs its helper text inside the field');
  dom.window.close();
});

test('Agent room model selection never overwrites the global Settings preference', () => {
  const selectModel = agentView.slice(
    agentView.indexOf('function selectModel(modelID)'),
    agentView.indexOf('function selectProviderRoute(route)'),
  );
  const refreshModels = agentView.slice(
    agentView.indexOf('async function refreshModels()'),
    agentView.indexOf('function updateModelTrigger()'),
  );
  assert.doesNotMatch(selectModel, /localStorage\.(?:setItem|removeItem)\('nusashell\.model'/,
    'Terra selected in a room must not replace Luna selected globally in Settings');
  assert.doesNotMatch(refreshModels, /localStorage\.removeItem\('nusashell\.model'/,
    'an unavailable room model must not erase the independent global preference');
  assert.doesNotMatch(agentView, /nusashell:preferred-model/,
    'saving a global default must not retarget the active room');
  assert.match(settingsView, /localStorage\.setItem\('nusashell\.model', model\)/,
    'Settings remains the sole writer of the global preferred model');
});


test('WebSocket auto-reconnect is no longer exposed as a setting (every UI must reconnect)', () => {
  // The auto-reconnect toggle was retired: every UI must auto-reconnect, so
  // exposing a disable-toggle was friction without value. The settings view
  // must not surface it.
  assert.doesNotMatch(html, /id="settings-auto-reconnect"/);
  assert.doesNotMatch(html, /id="settings-check-connection-btn"/);
  assert.doesNotMatch(settingsView, /saveReconnectPreference/);
  assert.doesNotMatch(settingsView, /setAutoReconnect/);
  assert.doesNotMatch(settingsView, /autoReconnectEnabled/);
});

test('Learning review threshold and skill nudge interval are gone', () => {
  assert.doesNotMatch(html, /id="settings-learning-threshold"/);
  assert.doesNotMatch(html, /id="settings-skill-nudge-interval"/);
  assert.doesNotMatch(settingsView, /learning_review_threshold/);
  assert.doesNotMatch(settingsView, /skill_nudge_interval/);
});

test('Periodic learner nudge interval is exposed in Memory & search settings', () => {
  assert.match(html, /id="settings-learner-nudge-interval"/);
  assert.match(html, /id="settings-learner-nudge-interval" type="number" min="0" max="100"/);
  assert.match(settingsView, /learner_nudge_interval/);
  assert.match(settingsView, /settings-learner-nudge-interval/);
  assert.match(settingsView, /learnerNudgeInterval < 0 \|\| learnerNudgeInterval > 100/);
});

test('Slow Down per-round delay is exposed in settings UI', () => {
  assert.match(html, /id="settings-slow-down"/);
  assert.match(settingsView, /slow_down/);
  assert.match(settingsView, /settings-slow-down/);
  // Range matches the backend validation (0 = off .. 60s cap).
  assert.match(html, /id="settings-slow-down" type="number" min="0" max="60"/);
  assert.match(settingsView, /slowDown < 0 \|\| slowDown > 60/);
});

test('Plugin usage contracts select is slim-enhanced like the model pickers', () => {
  assert.match(settingsView, /createSelect\(document\.getElementById\('settings-plugin-contract-mode'\)/);
  assert.match(settingsView, /contractModeSelect\.setSelected/);
  assert.match(settingsView, /contractModeSelect\.getSelected/);
  // Once SlimSelect owns the element, native .value access must be gone:
  // writes desync the rendered control and reads bypass the slim instance.
  assert.doesNotMatch(settingsView, /'settings-plugin-contract-mode'\)\.value/);
});

test('STT language select is slim-enhanced like the model pickers', () => {
  assert.match(settingsView, /createSelect\(document\.getElementById\('settings-stt-language'\)/);
  assert.match(settingsView, /sttLanguageSelect\.setSelected/);
  assert.match(settingsView, /sttLanguageSelect\.getSelected/);
  assert.doesNotMatch(settingsView, /'settings-stt-language'\)\.value/);
});

test('Interface font picker is slim-enhanced and browser-local', () => {
  assert.match(html, /id="settings-font-family"/);
  assert.match(html, /id="settings-font-preview"/);
  assert.match(settingsView, /createSelect\(document\.getElementById\('settings-font-family'\)/);
  assert.match(settingsView, /readFontPreference/);
  assert.match(settingsView, /setFontPreference/);
  assert.match(settingsView, /handleFontPreferenceChange/);
  assert.doesNotMatch(settingsView, /settings.set[sS]{0,500}font/i);
});

test('Settings exposes a compact section rail for its long-form groups', () => {
  assert.match(html, /id="settings-section-nav"[^>]+aria-label="Settings sections"/);
  for (const section of ['agent', 'context', 'memory', 'understand', 'generate', 'web', 'workspace']) {
    assert.match(html, new RegExp(`id="settings-jump-${section}"[^>]+data-settings-section="settings-group-${section}"`));
  }
  assert.match(settingsView, /bindSettingsSectionNavigation/);
});

test('Settings section rail marks the destination and transfers focus to its heading', () => {
  const dom = new JSDOM(`<body>
    <nav id="settings-section-nav">
      <button data-settings-section="settings-group-agent" aria-current="location">Agent</button>
      <button data-settings-section="settings-group-web">Web</button>
    </nav>
    <h2 id="settings-group-agent" tabindex="-1">Agent</h2>
    <h2 id="settings-group-web" tabindex="-1">Web</h2>
  </body>`, { pretendToBeVisual: true });
  let scrollOptions;
  dom.window.document.getElementById('settings-group-web').scrollIntoView = (options) => { scrollOptions = options; };

  bindSettingsSectionNavigation(dom.window.document);
  dom.window.document.querySelector('[data-settings-section="settings-group-web"]')
    .dispatchEvent(new dom.window.Event('click', { bubbles: true }));

  assert.equal(dom.window.document.activeElement?.id, 'settings-group-web');
  assert.equal(dom.window.document.querySelector('[aria-current="location"]')?.textContent, 'Web');
  assert.deepEqual(scrollOptions, { behavior: 'auto', block: 'start' });
  dom.window.close();
});

test('Offline STT platform tabs expose keyboard navigation and owned panels', () => {
  for (const os of ['linux', 'windows', 'macos']) {
    assert.match(html, new RegExp(`id="stt-guide-tab-${os}"[^>]+aria-controls="stt-guide-${os}"`));
  }
  assert.match(settingsView, /bindTablistKeyboard\(document\.querySelector\('\.stt-guide-tabs'\)\)/);
});
