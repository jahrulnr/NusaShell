import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const settingsView = await readFile(new URL('./js/views/settings.js', import.meta.url), 'utf8');
const rpc = await readFile(new URL('./js/rpc.js', import.meta.url), 'utf8');

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
  ]) {
    assert.match(html, new RegExp(`id="${id}"`));
  }
  assert.match(settingsView, /rpc\('app\.info'/);
  assert.match(settingsView, /rpc\('ai\.models\.list'/);
  assert.match(settingsView, /max_tool_rounds/);
  assert.match(settingsView, /max_parallel_tools/);
});

test('WebSocket reconnect is an explicit persisted preference', () => {
  assert.match(rpc, /export function setAutoReconnect\(enabled\)/);
  assert.match(rpc, /if \(!autoReconnect \|\| reconnectTimer/);
  assert.match(rpc, /lsSet\('nusashell\.autoReconnect'/);
});
