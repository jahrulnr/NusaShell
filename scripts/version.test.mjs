import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { readVersion, syncElectronVersion } from './version.mjs';

const temporaryDirectories = [];

test.afterEach(async () => {

  await Promise.all(temporaryDirectories.splice(0).map((directory) =>
    rm(directory, { recursive: true, force: true }),
  ));
});

test('readVersion trims a VERSION file and validates semantic versions', async () => {
  const root = await mkdtemp(join(tmpdir(), 'nusashell-version-'));
  temporaryDirectories.push(root);
  const versionPath = join(root, 'VERSION');

  await writeFile(versionPath, ' 1.2.3\r\n');
  assert.equal(await readVersion(versionPath), '1.2.3');

  await writeFile(versionPath, 'v1.2.3\n');
  await assert.rejects(readVersion(versionPath), /valid semantic version/);
});

test('syncElectronVersion updates package and lock metadata from the Electron VERSION', async () => {
  const root = await mkdtemp(join(tmpdir(), 'nusashell-version-sync-'));
  temporaryDirectories.push(root);
  await writeFile(join(root, 'VERSION'), '9.9.9\n');
  const electronVersionPath = join(root, 'apps-electron-VERSION');
  await writeFile(electronVersionPath, '2.4.6\n');
  await writeFile(join(root, 'package.json'), '{}\n');
  await writeFile(join(root, 'apps-electron-package.json'), JSON.stringify({ version: '0.1.0' }) + '\n');
  await writeFile(join(root, 'apps-electron-package-lock.json'), JSON.stringify({
    version: '0.1.0',
    packages: { '': { version: '0.1.0' } },
  }) + '\n');

  const result = await syncElectronVersion(root, {
    versionPath: electronVersionPath,
    packagePath: join(root, 'apps-electron-package.json'),
    lockPath: join(root, 'apps-electron-package-lock.json'),
  });

  assert.equal(result.version, '2.4.6');
  assert.equal(result.changed, true);
  assert.equal(JSON.parse(await readFile(join(root, 'apps-electron-package.json'), 'utf8')).version, '2.4.6');
  const lock = JSON.parse(await readFile(join(root, 'apps-electron-package-lock.json'), 'utf8'));
  assert.equal(lock.version, '2.4.6');
  assert.equal(lock.packages[''].version, '2.4.6');
  await syncElectronVersion(root, {
    check: true,
    versionPath: electronVersionPath,
    packagePath: join(root, 'apps-electron-package.json'),
    lockPath: join(root, 'apps-electron-package-lock.json'),
  });
});

test('syncElectronVersion check fails when package metadata drifts', async () => {
  const root = await mkdtemp(join(tmpdir(), 'nusashell-version-drift-'));
  temporaryDirectories.push(root);
  await writeFile(join(root, 'VERSION'), '9.9.9\n');
  const electronVersionPath = join(root, 'apps-electron-VERSION');
  await writeFile(electronVersionPath, '2.4.6\n');
  await writeFile(join(root, 'apps-electron-package.json'), JSON.stringify({ version: '0.1.0' }) + '\n');
  await writeFile(join(root, 'apps-electron-package-lock.json'), JSON.stringify({
    version: '0.1.0',
    packages: { '': { version: '0.1.0' } },
  }) + '\n');

  await assert.rejects(syncElectronVersion(root, {
    check: true,
    versionPath: electronVersionPath,
    packagePath: join(root, 'apps-electron-package.json'),
    lockPath: join(root, 'apps-electron-package-lock.json'),
  }), /version drift/);
});
