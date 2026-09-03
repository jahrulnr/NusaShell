import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const settingsView = await readFile(new URL('../js/views/settings.js', import.meta.url), 'utf8');
const rpc = await readFile(new URL('../js/rpc.js', import.meta.url), 'utf8');

test('Settings exposes the Go-supported Electron parity controls', () => {
  for (const id of [
    'settings-max-tool-rounds',
    'settings-max-parallel-tools',
    'settings-preferred-model',
    'settings-auto-reconnect',
    'settings-check-connection-btn',
    'settings-data-dir',
    'settings-sidebar-compact',
    'settings-image-model',
    'settings-delegate-model',
  ]) {
    assert.match(html, new RegExp(`id="${id}"`));
  }
  assert.match(settingsView, /rpc\('app\.info'/);
  assert.match(settingsView, /rpc\('ai\.models\.list'/);
  assert.match(settingsView, /max_tool_rounds/);
  assert.match(settingsView, /max_parallel_tools/);
  assert.match(settingsView, /plugin_contract_mode/);
  assert.match(settingsView, /delegate_model/);
  assert.match(settingsView, /delegateSelect\.getSelected/);
});

test('WebSocket reconnect is an explicit persisted preference', () => {
  assert.match(rpc, /export function setAutoReconnect\(enabled\)/);
  assert.match(rpc, /if \(!autoReconnect \|\| reconnectTimer/);
  assert.match(rpc, /lsSet\('nusashell\.autoReconnect'/);
});

test('Skill nudge interval is exposed in settings UI', () => {
  assert.match(html, /id="settings-skill-nudge-interval"/);
  assert.match(settingsView, /skill_nudge_interval/);
  assert.match(settingsView, /settings-skill-nudge-interval/);
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
