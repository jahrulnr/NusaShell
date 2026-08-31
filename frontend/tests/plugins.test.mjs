import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

import { hasPluginUI, pluginKind, canAutoUpdate, hasUpdate, hasContract } from '../js/views/plugins-model.js';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const pluginsCSS = await readFile(new URL('../styles/plugins.css', import.meta.url), 'utf8');

test('Plugins view uses the Electron catalog name and controls', () => {
  assert.match(html, /data-view="plugins"/);
  assert.match(html, /id="plugins-add-mcp"/);
  assert.match(html, /id="plugin-table"/);
});

test('Plugins mobile layout gives the header and row metadata room to breathe', () => {
  const mobileRules = pluginsCSS.slice(pluginsCSS.indexOf('@media (max-width: 620px)'));
  assert.match(mobileRules, /.plugins-view \.view-header\s*\{[\s\S]*flex-direction:\s*column/);
  assert.match(mobileRules, /.plugins-view \.view-header-actions\s*\{[\s\S]*width:\s*100%/);
  assert.match(mobileRules, /.plugins-view \.view-header-actions \.action-btn\s*\{[\s\S]*flex:\s*1 1 160px/);
  assert.match(mobileRules, /\.plugin-row-name\s*\{[\s\S]*flex-wrap:\s*wrap/);
  assert.match(mobileRules, /\.plugin-row-badge,[\s\S]*\.plugin-row-contract\s*\{[\s\S]*white-space:\s*nowrap/);
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

test('Usage contract badge is driven by the contractEntry DTO field', () => {
  assert.equal(hasContract({ contractEntry: 'CONTRACT.md' }), true);
  assert.equal(hasContract({ contractEntry: 'docs/RULES.md' }), true);
  assert.equal(hasContract({}), false);
  assert.equal(hasContract({ contractEntry: '' }), false);
});

test('Plugin drawer exposes autostart, auto-update, and a manual update button', () => {
  assert.match(html, /id="plugin-drawer-autostart"/);
  assert.match(html, /id="plugin-autostart-toggle"/);
  assert.match(html, /id="plugin-drawer-autoupdate"/);
  assert.match(html, /id="plugin-autoupdate-toggle"/);
  assert.match(html, /id="plugin-btn-update"/);
});
