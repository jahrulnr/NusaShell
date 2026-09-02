import assert from 'node:assert/strict';
import test from 'node:test';

import { isElectronRuntime, resolveDroppedFilePath } from '../js/desktop-file-path.js';

test('isElectronRuntime recognizes the narrow Electron preload bridge', () => {
  assert.equal(isElectronRuntime({ getPathForFile() {} }), true);
  assert.equal(isElectronRuntime({}), false);
  assert.equal(isElectronRuntime(null), false);
});

test('resolveDroppedFilePath uses the Electron preload bridge', () => {
  const file = { name: 'project', path: undefined };
  let received;
  const desktopApi = {
    getPathForFile(value) {
      received = value;
      return '/home/user/project';
    },
  };

  assert.equal(resolveDroppedFilePath(file, desktopApi), '/home/user/project');
  assert.equal(received, file);
});

test('resolveDroppedFilePath keeps the legacy File.path fallback', () => {
  const file = { name: 'project', path: '/home/user/legacy-project' };
  assert.equal(resolveDroppedFilePath(file, { getPathForFile: () => '' }), '/home/user/legacy-project');
  assert.equal(resolveDroppedFilePath(file, null), '/home/user/legacy-project');
});

test('resolveDroppedFilePath ignores bridge failures and malformed paths', () => {
  const file = { name: 'project', path: 42 };
  const desktopApi = {
    getPathForFile() {
      throw new Error('bridge unavailable');
    },
  };

  assert.equal(resolveDroppedFilePath(file, desktopApi), '');
  assert.equal(resolveDroppedFilePath(null, desktopApi), '');
});
