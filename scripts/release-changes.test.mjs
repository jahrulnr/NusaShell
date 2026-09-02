import assert from 'node:assert/strict';
import test from 'node:test';

import { detectReleaseStreams } from './release-changes.mjs';

test('detectReleaseStreams routes Go core changes without rebuilding the wrapper', () => {
  assert.deepEqual(detectReleaseStreams([
    'application/agent.go',
    'frontend/js/views/agent/composer.js',
    'resources/agent/docs/tools.md',
  ]), {
    goChanged: true,
    electronChanged: false,
    hasChanges: true,
  });
});

test('detectReleaseStreams routes Electron wrapper changes independently', () => {
  assert.deepEqual(detectReleaseStreams([
    'apps/electron/src/main.cjs',
    'apps/electron/package-lock.json',
  ]), {
    goChanged: false,
    electronChanged: true,
    hasChanges: true,
  });
});

test('icon changes rebuild both products because the icon is shared', () => {
  assert.deepEqual(detectReleaseStreams(['frontend/icons/nusashell.png']), {
    goChanged: true,
    electronChanged: true,
    hasChanges: true,
  });
});

test('documentation and workflow-only changes do not create a product release', () => {
  assert.deepEqual(detectReleaseStreams([
    'README.md',
    'docs/electron.md',
    '.github/workflows/ci.yml',
    'scripts/release-index.mjs',
  ]), {
    goChanged: false,
    electronChanged: false,
    hasChanges: false,
  });
});

test('workflow dispatch can explicitly release every stream', () => {
  assert.deepEqual(detectReleaseStreams([], { all: true }), {
    goChanged: true,
    electronChanged: true,
    hasChanges: true,
  });
});

test('detectReleaseStreams retries a stream whose version is ahead of its release pointer', () => {
  const result = detectReleaseStreams(['README.md'], {
    currentVersions: { go: '1.1.0', electron: '2.0.0' },
    releaseIndex: {
      go: { version: '1.0.0' },
      electron: { version: '2.0.0' },
    },
  });

  assert.deepEqual(result, {
    goChanged: true,
    electronChanged: false,
    hasChanges: true,
  });
});

test('detectReleaseStreams does not infer a release when a stream has no pointer yet', () => {
  assert.deepEqual(detectReleaseStreams(['README.md'], {
    currentVersions: { go: '0.1.0', electron: '0.1.0' },
    releaseIndex: { go: null, electron: null },
  }), {
    goChanged: false,
    electronChanged: false,
    hasChanges: false,
  });
});
