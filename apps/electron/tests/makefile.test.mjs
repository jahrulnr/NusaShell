import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import test from 'node:test';

const electronRoot = resolve(import.meta.dirname, '..');
const repoRoot = resolve(electronRoot, '..', '..');

const electronTargets = [
  'deps',
  'version',
  'version-sync',
  'version-check',
  'test',
  'ui-test',
  'build-backend',
  'dev',
  'package',
  'install-local',
  'dist',
  'release-linux',
  'release-manifest',
];

test('Electron make targets live in apps/electron, not the Go core Makefile', async () => {
  const electronMake = await readFile(join(electronRoot, 'Makefile'), 'utf8');
  const rootMake = await readFile(join(repoRoot, 'Makefile'), 'utf8');
  for (const target of electronTargets) {
    assert.match(electronMake, new RegExp(`^${target}:`, 'm'), `apps/electron/Makefile missing ${target}`);
  }
  assert.doesNotMatch(rootMake, /^electron-[a-z0-9-]+:/m, 'root Makefile still declares electron-* recipes');
});
