import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

import { hasPluginUI, pluginKind, canAutoUpdate, hasUpdate } from './js/views/plugins-model.js';

const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');

test('Plugins view uses the Electron catalog name and controls', () => {
  assert.match(html, /data-view="plugins"/);
  assert.match(html, /id="plugins-add-mcp"/);
  assert.match(html, /id="plugin-table"/);
});

test('Plugins distinguishes native MCP, headless plugin, and MCP plus UI', () => {
  assert.equal(pluginKind({ plugin: false }), 'mcp');
  assert.equal(pluginKind({ plugin: true, hasUI: false }), 'plugin');
  assert.equal(pluginKind({ plugin: true, hasUI: true }), 'plugin-ui');
  assert.equal(hasPluginUI({ plugin: true, hasUI: true }), true);
  assert.equal(hasPluginUI({ plugin: true, hasUI: false }), false);
  assert.equal(hasPluginUI({ plugin: false, hasUI: true }), false);
});

test('Plugins marks UI entry from plugin manifest when the list DTO has no flag', () => {
  assert.equal(hasPluginUI({ plugin: true, manifest: { ui: { entry: 'ui/index.html' } } }), true);
  assert.equal(hasPluginUI({ plugin: true, manifest: { ui: { entry: '' } } }), false);
});

test('Auto-update applies only to catalog-managed plugins', () => {
  assert.equal(canAutoUpdate({ catalog: true }), true);
  assert.equal(canAutoUpdate({ catalog: false }), false);
  assert.equal(canAutoUpdate({}), false);
});

test('Update availability is driven by the updateAvailable version', () => {
  assert.equal(hasUpdate({ updateAvailable: '1.4.0' }), true);
  assert.equal(hasUpdate({ updateAvailable: '' }), false);
  assert.equal(hasUpdate({}), false);
});

test('Plugin drawer exposes autostart, auto-update, and a manual update button', () => {
  assert.match(html, /id="plugin-drawer-autostart"/);
  assert.match(html, /id="plugin-autostart-toggle"/);
  assert.match(html, /id="plugin-drawer-autoupdate"/);
  assert.match(html, /id="plugin-autoupdate-toggle"/);
  assert.match(html, /id="plugin-btn-update"/);
});
