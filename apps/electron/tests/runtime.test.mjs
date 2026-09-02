import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildBackendEnvironment,
  electronDevArgs,
  isExternalHTTPURL,
  isSameOriginURL,
  normalizeLoopbackURL,
  resolveBackendPath,
} from '../src/runtime.cjs';

test('electronDevArgs uses the Linux no-sandbox fallback only for an unconfigured helper', () => {
  const configuredStat = () => ({
    isFile: () => true,
    mode: 0o1004755,
    uid: 0,
  });
  const unconfiguredStat = () => ({
    isFile: () => true,
    mode: 0o1000777,
    uid: 1000,
  });

  assert.deepEqual(electronDevArgs({
    platform: 'linux',
    sandboxPath: '/configured/chrome-sandbox',
    statSync: configuredStat,
  }), []);
  assert.deepEqual(electronDevArgs({
    platform: 'linux',
    sandboxPath: '/unconfigured/chrome-sandbox',
    statSync: unconfiguredStat,
  }), ['--no-sandbox']);
  assert.deepEqual(electronDevArgs({
    platform: 'darwin',
    sandboxPath: '/unconfigured/chrome-sandbox',
    statSync: unconfiguredStat,
  }), []);
});

test('normalizeLoopbackURL accepts local HTTP URLs and rejects remote content', () => {
  assert.equal(normalizeLoopbackURL('http://127.0.0.1:9999').toString(), 'http://127.0.0.1:9999/');
  assert.equal(normalizeLoopbackURL('http://localhost:9999/app').pathname, '/app');
  assert.throws(() => normalizeLoopbackURL('https://example.com'), /loopback URL/);
  assert.throws(() => normalizeLoopbackURL('file:///tmp/index.html'), /http or https/);
  assert.throws(() => normalizeLoopbackURL('http://user:pass@127.0.0.1:9999'), /credentials/);
});

test('navigation policy allows only the backend origin', () => {
  const appURL = 'http://127.0.0.1:9999/';
  assert.equal(isSameOriginURL('http://127.0.0.1:9999/plugins/demo/', appURL), true);
  assert.equal(isSameOriginURL('http://127.0.0.1:10000/', appURL), false);
  assert.equal(isSameOriginURL('https://127.0.0.1:9999/', appURL), false);
  assert.equal(isExternalHTTPURL('https://www.example.com/docs'), true);
  assert.equal(isExternalHTTPURL('javascript:alert(1)'), false);
});

test('backend environment forces a loopback listener and clears remote access', () => {
  const environment = buildBackendEnvironment({
    NUSASHELL_HOST: '0.0.0.0',
    NUSASHELL_PORT: '1',
    NUSASHELL_ALLOW_REMOTE: '1',
    NUSASHELL_DEV: '1',
    KEEP_ME: 'yes',
  }, 43210, true);

  assert.equal(environment.NUSASHELL_HOST, '127.0.0.1');
  assert.equal(environment.NUSASHELL_PORT, '43210');
  assert.equal(environment.NUSASHELL_ALLOW_REMOTE, undefined);
  assert.equal(environment.NUSASHELL_DEV, undefined);
  assert.equal(environment.KEEP_ME, 'yes');
});

test('resolveBackendPath prefers an explicit external binary', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'nusashell-electron-test-'));
  try {
    const explicit = join(directory, 'custom-backend');
    await writeFile(explicit, 'binary');

    assert.equal(resolveBackendPath({
      explicitPath: explicit,
      packaged: true,
      resourcesPath: join(directory, 'resources'),
      repositoryRoot: directory,
    }), explicit);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('resolveBackendPath finds the separately installed Go backend for packaged Electron', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'nusashell-electron-external-'));
  try {
    const home = join(directory, 'home');
    const external = join(home, '.local', 'share', 'nusashell', 'current', 'nusashell');
    const embedded = join(directory, 'resources', 'runtime', 'nusashell');
    await mkdir(join(home, '.local', 'share', 'nusashell', 'current'), { recursive: true });
    await mkdir(join(directory, 'resources', 'runtime'), { recursive: true });
    await writeFile(external, 'go binary');
    await writeFile(embedded, 'stale embedded binary');

    assert.equal(resolveBackendPath({
      packaged: true,
      resourcesPath: join(directory, 'resources'),
      repositoryRoot: directory,
      platform: 'linux',
      environment: { HOME: home, PATH: '' },
    }), external);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('resolveBackendPath finds the Windows user-local Go backend for packaged Electron', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'nusashell-electron-windows-'));
  try {
    const localAppData = join(directory, 'local-app-data');
    const external = join(localAppData, 'Programs', 'NusaShell', 'current', 'nusashell.exe');
    await mkdir(join(localAppData, 'Programs', 'NusaShell', 'current'), { recursive: true });
    await writeFile(external, 'go binary');

    assert.equal(resolveBackendPath({
      packaged: true,
      platform: 'win32',
      environment: { LOCALAPPDATA: localAppData, PATH: '' },
      repositoryRoot: directory,
    }), external);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('Electron removes the application menu and does not embed the Go runtime', async () => {
  const mainSource = await readFile(new URL('../src/main.cjs', import.meta.url), 'utf8');
  const packageJSON = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'));

  assert.match(mainSource, /Menu\.setApplicationMenu\(null\)/);
  assert.match(mainSource, /autoHideMenuBar:\s*true/);
  assert.equal(packageJSON.build.executableName, 'nusashell-desktop');
  assert.match(packageJSON.build.artifactName, /NusaShell-Electron/);
  assert.equal(packageJSON.build.extraResources.some((entry) => entry.from === 'runtime'), false);
});
