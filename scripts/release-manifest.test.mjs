import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { buildReleaseManifest } from './release-manifest.mjs';

const temporaryDirectories = [];

test.afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map((directory) =>
    rm(directory, { recursive: true, force: true }),
  ));
});

test('buildReleaseManifest indexes Go payloads and records SHA-256', async () => {
  const root = await mkdtemp(join(tmpdir(), 'nusashell-release-'));
  temporaryDirectories.push(root);
  await mkdir(join(root, 'nested'), { recursive: true });
  const payloads = {
    'nusashell-2.4.6-linux-x64.tar.gz': 'linux payload',
    'nusashell-2.4.6-darwin-arm64.tar.gz': 'mac payload',
    'nusashell-2.4.6-win-x64.zip': 'windows payload',
    'NusaShell-2.4.6-linux-x64.AppImage': 'ignored linux appimage',
  };
  for (const [name, contents] of Object.entries(payloads)) {
    await writeFile(join(root, name === 'nusashell-2.4.6-win-x64.zip' ? 'nested' : '', name), contents);
  }

  const manifest = await buildReleaseManifest('2.4.6', root, 'go');

  assert.deepEqual(Object.keys(manifest.files).sort(), ['darwin-arm64', 'linux-x64', 'win32-x64']);
  assert.equal(manifest.version, '2.4.6');
  assert.equal(manifest.product, 'go');
  assert.equal(manifest.files['linux-x64'].name, 'nusashell-2.4.6-linux-x64.tar.gz');
  assert.match(manifest.files['linux-x64'].sha256, /^[a-f0-9]{64}$/);
});

test('buildReleaseManifest keeps Electron payloads in a separate manifest', async () => {
  const root = await mkdtemp(join(tmpdir(), 'nusashell-electron-release-'));
  temporaryDirectories.push(root);
  await writeFile(join(root, 'nusashell-electron-2.4.6-linux-x64.tar.gz'), 'linux wrapper');
  await writeFile(join(root, 'NusaShell-Electron-2.4.6-mac-arm64.zip'), 'mac wrapper');
  await writeFile(join(root, 'NusaShell-Electron-2.4.6-win-x64.zip'), 'windows wrapper');

  const manifest = await buildReleaseManifest('2.4.6', root, 'electron');

  assert.equal(manifest.product, 'electron');
  assert.deepEqual(Object.keys(manifest.files).sort(), ['darwin-arm64', 'linux-x64', 'win32-x64']);
  assert.equal(manifest.files['linux-x64'].name, 'nusashell-electron-2.4.6-linux-x64.tar.gz');
});

test('buildReleaseManifest rejects a version with no recognized payload', async () => {
  const root = await mkdtemp(join(tmpdir(), 'nusashell-release-empty-'));
  temporaryDirectories.push(root);
  await writeFile(join(root, 'unrelated.txt'), 'not an installer');

  await assert.rejects(buildReleaseManifest('2.4.6', root, 'go'), /No release payloads found/);
});
