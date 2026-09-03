import assert from 'node:assert/strict';
import test from 'node:test';

import { createReleaseIndex, releaseUpdatesFromEnvironment, updateReleaseIndex } from './release-index.mjs';

const initialIndex = {
  schemaVersion: 1,
  go: {
    version: '0.1.0',
    tag: 'go-v0.1.0',
    manifest: 'latest.json',
    releasedAt: '2026-01-01T00:00:00Z',
  },
  electron: {
    version: '0.1.0',
    tag: 'electron-v0.1.0',
    manifest: 'electron-latest.json',
    releasedAt: '2026-01-01T00:00:00Z',
  },
};

test('createReleaseIndex returns an empty, valid stream pointer document', () => {
  assert.deepEqual(createReleaseIndex(), {
    schemaVersion: 1,
    go: null,
    electron: null,
    pets: null,
  });
});

test('updateReleaseIndex updates one stream and preserves the other stream', () => {
  const updated = updateReleaseIndex(initialIndex, {
    go: {
      version: '1.2.3',
      tag: 'go-v1.2.3',
      releasedAt: '2026-02-03T04:05:06Z',
    },
  });

  assert.deepEqual(updated.go, {
    version: '1.2.3',
    tag: 'go-v1.2.3',
    manifest: 'latest.json',
    releasedAt: '2026-02-03T04:05:06Z',
  });
  assert.deepEqual(updated.electron, initialIndex.electron);
  assert.deepEqual(initialIndex.go, {
    version: '0.1.0',
    tag: 'go-v0.1.0',
    manifest: 'latest.json',
    releasedAt: '2026-01-01T00:00:00Z',
  });
});

test('updateReleaseIndex supports independent Electron versioning', () => {
  const updated = updateReleaseIndex(initialIndex, {
    electron: {
      version: '2.0.0',
      tag: 'electron-v2.0.0',
      releasedAt: '2026-03-04T05:06:07Z',
    },
  });

  assert.equal(updated.go.version, '0.1.0');
  assert.equal(updated.electron.version, '2.0.0');
});

test('updateReleaseIndex supports the Linux-only pets stream', () => {
  const updated = updateReleaseIndex(initialIndex, {
    pets: {
      version: '0.1.3',
      tag: 'pets-v0.1.3',
      releasedAt: '2026-03-05T06:07:08Z',
    },
  });

  assert.deepEqual(updated.pets, {
    version: '0.1.3',
    tag: 'pets-v0.1.3',
    manifest: 'pets-latest.json',
    releasedAt: '2026-03-05T06:07:08Z',
  });
  assert.equal(updated.go.version, '0.1.0');
  assert.equal(updated.electron.version, '0.1.0');
});

test('updateReleaseIndex preserves valid prerelease/build versions in stream tags', () => {
  const updated = updateReleaseIndex(initialIndex, {
    go: { version: '1.2.3-rc.1+linux', tag: 'go-v1.2.3-rc.1+linux' },
  });

  assert.equal(updated.go.version, '1.2.3-rc.1+linux');
  assert.equal(updated.go.tag, 'go-v1.2.3-rc.1+linux');
});

test('updateReleaseIndex rejects invalid stream metadata', () => {
  assert.throws(() => updateReleaseIndex(initialIndex, {
    go: { version: 'v1.2.3', tag: 'go-v1.2.3' },
  }), /valid semantic version/);
  assert.throws(() => updateReleaseIndex(initialIndex, {
    electron: { version: '1.2.3', tag: '../electron-v1.2.3' },
  }), /safe electron-v<version> tag/);
});

test('releaseUpdatesFromEnvironment only includes changed streams', () => {
  assert.deepEqual(releaseUpdatesFromEnvironment({
    GO_CHANGED: 'false',
    ELECTRON_CHANGED: 'true',
    PETS_CHANGED: 'true',
    GO_VERSION: '1.2.3',
    ELECTRON_VERSION: '2.0.0',
    PETS_VERSION: '0.1.3',
  }), {
    electron: { version: '2.0.0', tag: 'electron-v2.0.0' },
    pets: { version: '0.1.3', tag: 'pets-v0.1.3' },
  });
});
