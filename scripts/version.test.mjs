import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

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

// The 0.9.4 commit wrote its CHANGELOG section without bumping VERSION, so the
// Go stream found its tag already published and skipped the release instead of
// failing: users stayed on 0.9.3 while the changelog advertised the fix. A
// release commit advances VERSION with its notes; docs/CI-only changes keep
// their entry under [Unreleased].
test('the newest CHANGELOG section is the current VERSION or Unreleased', async () => {
  const repoRoot = fileURLToPath(new URL('..', import.meta.url));
  const version = await readVersion(join(repoRoot, 'VERSION'));
  const changelog = await readFile(join(repoRoot, 'CHANGELOG.md'), 'utf8');
  const heading = changelog.replace(/\r\n/g, '\n').split('\n').find((line) => /^##\s+/.test(line)) ?? '';
  const newest = /^##\s+\[([^\]]+)\]/.exec(heading)?.[1]?.trim();

  assert.ok(newest, `CHANGELOG.md has no version heading, first heading: ${JSON.stringify(heading)}`);
  assert.ok(
    newest === 'Unreleased' || newest === version,
    `CHANGELOG.md newest section [${newest}] must be [Unreleased] or the current VERSION ${version}`
      + '; bump VERSION in the same commit that publishes the notes',
  );
});
