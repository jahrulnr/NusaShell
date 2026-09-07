import assert from 'node:assert/strict';
import test from 'node:test';

import { buildContextMenuTemplate } from '../src/context-menu.cjs';

function roles(template) {
  return template.filter((item) => item.role).map((item) => item.role);
}

test('Electron context menu exposes browser edit actions for selected text', () => {
  const template = buildContextMenuTemplate({ isEditable: true, selectionText: 'hello' });

  assert.deepEqual(roles(template), ['undo', 'redo', 'cut', 'copy', 'paste', 'selectAll']);
  assert.equal(template.find((item) => item.role === 'cut').enabled, true);
  assert.equal(template.find((item) => item.role === 'copy').enabled, true);
  assert.equal(template.find((item) => item.role === 'paste').enabled, true);
});

test('Electron context menu keeps copy available for read-only agent messages', () => {
  const template = buildContextMenuTemplate({ isEditable: false, selectionText: 'agent output' });

  assert.equal(template.find((item) => item.role === 'cut').enabled, false);
  assert.equal(template.find((item) => item.role === 'copy').enabled, true);
  assert.equal(template.find((item) => item.role === 'paste').enabled, false);
  assert.equal(template.find((item) => item.role === 'selectAll').enabled, undefined);
});

test('Electron context menu disables selection actions when nothing is selected', () => {
  const template = buildContextMenuTemplate({ isEditable: true, selectionText: '' });

  assert.equal(template.find((item) => item.role === 'cut').enabled, false);
  assert.equal(template.find((item) => item.role === 'copy').enabled, false);
  assert.equal(template.find((item) => item.role === 'paste').enabled, true);
});
