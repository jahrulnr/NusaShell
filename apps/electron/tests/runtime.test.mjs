import { createServer } from 'node:http';
import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildBackendEnvironment,
  defaultCoreURL,
  electronDevArgs,
  installCommand,
  installDocsURL,
  isExternalHTTPURL,
  isSameOriginURL,
  normalizeLoopbackURL,
  probeCoreURL,
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
  assert.equal(normalizeLoopbackURL('http://127.0.0.1:10994').toString(), 'http://127.0.0.1:10994/');
  assert.equal(normalizeLoopbackURL('http://localhost:10994/app').pathname, '/app');
  assert.throws(() => normalizeLoopbackURL('https://example.com'), /loopback URL/);
  assert.throws(() => normalizeLoopbackURL('file:///tmp/index.html'), /http or https/);
  assert.throws(() => normalizeLoopbackURL('http://user:pass@127.0.0.1:10994'), /credentials/);
});

test('navigation policy allows only the backend origin', () => {
  const appURL = 'http://127.0.0.1:10994/';
  assert.equal(isSameOriginURL('http://127.0.0.1:10994/plugins/demo/', appURL), true);
  assert.equal(isSameOriginURL('http://127.0.0.1:10000/', appURL), false);
  assert.equal(isSameOriginURL('https://127.0.0.1:10994/', appURL), false);
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
  assert.equal(environment.NUSASHELL_SERVICE, undefined);
  assert.equal(environment.NUSASHELL_WS_URL, undefined);
  assert.equal(environment.NUSASHELL_CORE_OWNER, 'electron');
  assert.equal(environment.KEEP_ME, 'yes');
});

test('default core URL and install commands are platform-specific', () => {
  assert.equal(defaultCoreURL().toString(), 'http://127.0.0.1:10994/');
  assert.match(installCommand('linux'), /install\.sh/);
  assert.match(installCommand('darwin'), /install\.sh/);
  assert.match(installCommand('win32'), /install\.ps1/);
  assert.match(installDocsURL(), /github\.com/);
});

test('core health probe accepts the new healthz identity', async () => {
  const server = createServer((req, res) => {
    assert.equal(req.url, '/healthz');
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ ok: true, service: 'nusashell-core', pid: 1 }));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  try {
    const address = server.address();
    const health = await probeCoreURL(`http://127.0.0.1:${address.port}/`);
    assert.equal(health.service, 'nusashell-core');
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
});

test('core health probe accepts the legacy app.info identity during upgrade', async () => {
  const server = createServer((req, res) => {
    if (req.url === '/healthz') {
      res.writeHead(200, { 'Content-Type': 'text/html' });
      res.end('<!doctype html>');
      return;
    }
    if (req.url === '/rpc/app/info') {
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify({ ok: true, result: { name: 'NusaShell', version: 'old' } }));
      return;
    }
    res.writeHead(404);
    res.end();
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  try {
    const address = server.address();
    const health = await probeCoreURL(`http://127.0.0.1:${address.port}/`);
    assert.equal(health.legacy, true);
    assert.equal(health.version, 'old');
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
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
