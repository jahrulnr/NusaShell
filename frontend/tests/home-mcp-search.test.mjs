import assert from 'node:assert/strict';
import { test } from 'node:test';

import { filterHomeMCPApps, homePluginIconGlyph, homePluginIconValue } from '../page/home/mcp.js';

test('Home search matches plugin name, id, manifest description, and a tool past the fourth', () => {
  const plugins = [
    { id: 'notes.app', name: 'Notes', manifest: { description: 'Capture ideas' }, hasUI: true },
    { id: 'mail.app', name: 'Mail', manifest: { description: 'Read messages' }, hasUI: true },
  ];
  assert.deepEqual(filterHomeMCPApps(plugins, 'capture').map((plugin) => plugin.id), ['notes.app']);
  assert.deepEqual(filterHomeMCPApps(plugins, 'mail.app').map((plugin) => plugin.id), ['mail.app']);
  assert.deepEqual(filterHomeMCPApps(plugins, 'NOTES').map((plugin) => plugin.id), ['notes.app']);

  const manyTools = {
    id: 'nusashell.toolbox',
    name: 'Toolbox',
    hasUI: true,
    tools: ['alpha', 'beta', 'gamma', 'delta', { name: 'epsilon_probe' }],
  };
  assert.deepEqual(filterHomeMCPApps([manyTools], 'epsilon_probe').map((plugin) => plugin.id), ['nusashell.toolbox']);
});

test('Home accepts a resolved icon, falls back to the manifest icon, and uses the puzzle glyph when none remains', () => {
  const resolved = { icon: 'data:image/png;base64,abc', manifest: { icon: '📝' } };
  const legacy = { manifest: { icon: '📝' } };
  assert.equal(homePluginIconValue(resolved), resolved.icon);
  assert.equal(homePluginIconValue(legacy), '📝');
  assert.equal(homePluginIconGlyph({}), '🧩');
  assert.equal(homePluginIconGlyph(legacy), '📝');
});
