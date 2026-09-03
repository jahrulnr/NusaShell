import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFile, spawn } from 'node:child_process';
import { chmod, mkdir, mkdtemp, readFile, realpath, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';
import test from 'node:test';

const execFileAsync = promisify(execFile);
const script = (name) => new URL(`./${name}`, import.meta.url);
const temporaryDirectories = [];

test.afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map((directory) =>
    rm(directory, { recursive: true, force: true }),
  ));
});

test('release and local Bash installers pass shell syntax checks', async () => {
  if (process.platform === 'win32') return;
  for (const name of ['install.sh', 'install-local.sh']) {
    await execFileAsync('bash', ['-n', script(name).pathname]);
  }
});

test('installers preserve the release manifest, checksum, and version activation contract', async () => {
  const releaseInstaller = await readFile(script('install.sh'), 'utf8');
  const localInstaller = await readFile(script('install-local.sh'), 'utf8');
  const windowsInstaller = await readFile(script('install.ps1'), 'utf8');
  const localWindowsInstaller = await readFile(script('install-local.ps1'), 'utf8');

  for (const source of [releaseInstaller, windowsInstaller]) {
    assert.match(source, /latest\.json/);
    assert.match(source, /electron-latest\.json/);
    assert.match(source, /release-versions\.json/);
    assert.match(source, /NUSASHELL_ELECTRON_VERSION/);
    assert.match(source, /SHA-?256|sha256/i);
    assert.match(source, /current/);
    assert.match(source, /versions/);
    assert.match(source, /NUSASHELL_INSTALL_ELECTRON/);
    assert.match(source, /NUSASHELL_INSTALL_MCP/);
    assert.match(source, /NusaShell-mcp/);
  }
  assert.match(releaseInstaller, /unshare -Ur true/);
  assert.match(releaseInstaller, /mv -Tf/);
  assert.match(releaseInstaller, /tar -tzf/);
  assert.match(releaseInstaller, /Release archive entry is unsafe/);
  assert.match(releaseInstaller, /\.local\/share\/nusashell/);
  assert.match(releaseInstaller, /\.local\/share\/nusashell-electron/);
  assert.match(releaseInstaller, /nusashell-desktop/);
  assert.match(releaseInstaller, /Install Electron/);
  assert.match(releaseInstaller, /Install MCP/);
  assert.match(releaseInstaller, /pets-latest\.json/);
  assert.match(releaseInstaller, /NUSASHELL_INSTALL_PETS/);
  assert.match(releaseInstaller, /Install desktop pet \(Linux only\)/);
  assert.match(releaseInstaller, /\.local\/share\/nusashell-pets/);
  assert.match(releaseInstaller, /nusashell-pets/);
  assert.doesNotMatch(windowsInstaller, /pets-latest\.json/);
  assert.doesNotMatch(windowsInstaller, /NUSASHELL_INSTALL_PETS/);
  assert.match(windowsInstaller, /nusashell\.exe/);
  assert.match(windowsInstaller, /LOCALAPPDATA.*Programs.*NusaShell/s);
  assert.match(windowsInstaller, /New-Item -ItemType Junction/);
  assert.match(windowsInstaller, /GetFolderPath\('Desktop'\)/);
  assert.match(windowsInstaller, /if \(\(Test-Path -LiteralPath \$Target\) -and/);
  assert.match(localInstaller, /apps\/electron\/VERSION/);
  assert.match(localWindowsInstaller, /apps\\electron\\VERSION/);

  for (const source of [releaseInstaller, localInstaller, windowsInstaller, localWindowsInstaller]) {
    assert.doesNotMatch(source, /Application Support[\\/]nusashell-desktop/);
    assert.doesNotMatch(source, /\.config[\\/]nusashell-desktop/);
  }
});

test('local Linux Electron installer activates a versioned wrapper without touching app data', async () => {
  if (process.platform !== 'linux') return;
  const root = await mkdtemp(join(tmpdir(), 'nusashell-local-install-'));
  temporaryDirectories.push(root);
  const build = join(root, 'build');
  const home = join(root, 'home');
  const installRoot = join(root, 'program');
  await mkdir(build, { recursive: true });
  await mkdir(home, { recursive: true });
  await writeFile(join(build, 'nusashell-desktop'), '#!/usr/bin/env sh\nexit 0\n');
  await writeFile(join(build, 'chrome-sandbox'), 'helper');
  await mkdir(join(build, 'resources'), { recursive: true });
  await writeFile(join(build, 'resources', 'nusashell.png'), 'icon');
  await chmod(join(build, 'nusashell-desktop'), 0o755);

  await execFileAsync('bash', [script('install-local.sh').pathname], {
    env: {
      ...process.env,
      HOME: home,
      NUSASHELL_BUILD_DIR: build,
      NUSASHELL_ELECTRON_INSTALL_ROOT: installRoot,
      NUSASHELL_NON_INTERACTIVE: '1',
    },
  });

  assert.equal(await realpath(join(installRoot, 'current')), join(installRoot, 'versions', '0.1.0'));
  const launcher = await readFile(join(home, '.local', 'bin', 'nusashell-desktop'), 'utf8');
  assert.equal(
    launcher.includes('--no-sandbox') || await fileExists(join(installRoot, 'current', 'chrome-sandbox.disabled')),
    true,
  );
  assert.equal((await readFile(join(home, '.local', 'share', 'applications', 'nusashell-desktop.desktop'), 'utf8')).includes('nusashell-desktop'), true);
  assert.equal(await realpath(join(installRoot, 'current', 'nusashell-desktop')), join(installRoot, 'versions', '0.1.0', 'nusashell-desktop'));
});

test('release Linux installer verifies and activates a local fixture archive', async () => {
  if (process.platform !== 'linux') return;
  const root = await mkdtemp(join(tmpdir(), 'nusashell-release-install-'));
  temporaryDirectories.push(root);
  const home = join(root, 'home');
  const fakeBin = join(root, 'bin');
  const payloadRoot = join(root, 'payload');
  const releaseRoot = join(root, 'release');
  const installRoot = join(root, 'program');
  const payloadName = 'nusashell-0.1.0-linux-x64.tar.gz';
  const archive = join(releaseRoot, payloadName);
  const manifest = join(releaseRoot, 'latest.json');
  const releaseIndex = join(releaseRoot, 'release-versions.json');
  await mkdir(home, { recursive: true });
  await mkdir(fakeBin, { recursive: true });
  await mkdir(payloadRoot, { recursive: true });
  await mkdir(releaseRoot, { recursive: true });
  await writeFile(join(payloadRoot, 'nusashell'), '#!/usr/bin/env sh\nexit 0\n');
  await chmod(join(payloadRoot, 'nusashell'), 0o755);
  await execFileAsync('tar', ['-C', payloadRoot, '-czf', archive, 'nusashell']);
  const sha256 = createHash('sha256').update(await readFile(archive)).digest('hex');
  await writeFile(manifest, `${JSON.stringify({
    version: '0.1.0',
    files: {
      'linux-x64': { name: payloadName, sha256 },
    },
  }, null, 2)}\n`);
  await writeFile(releaseIndex, `${JSON.stringify({
    schemaVersion: 1,
    go: { version: '0.1.0', tag: 'go-v0.1.0', manifest: 'latest.json', releasedAt: '2026-01-01T00:00:00Z' },
    electron: null,
  }, null, 2)}\n`);
  await writeFile(join(fakeBin, 'curl'), `#!/usr/bin/env sh
set -eu
url=''
destination=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) destination="$2"; shift 2 ;;
    https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  */release-versions.json) cp '${releaseIndex}' "$destination" ;;
  */latest.json) cp '${manifest}' "$destination" ;;
  *) cp '${archive}' "$destination" ;;
esac
`);
  await chmod(join(fakeBin, 'curl'), 0o755);

  await execFileAsync('bash', [script('install.sh').pathname], {
    env: {
      ...process.env,
      HOME: home,
      PATH: `${fakeBin}:${process.env.PATH}`,
      NUSASHELL_RELEASE_BASE: 'https://fixture.invalid/releases',
      NUSASHELL_RELEASE_INDEX: 'https://fixture.invalid/releases/release-versions.json',
      NUSASHELL_GO_INSTALL_ROOT: installRoot,
      NUSASHELL_VERSION: '',
      NUSASHELL_NON_INTERACTIVE: '1',
    },
  });

  assert.equal(await realpath(join(installRoot, 'current')), join(installRoot, 'versions', '0.1.0'));
  assert.equal(await realpath(join(installRoot, 'current', 'nusashell')), join(installRoot, 'versions', '0.1.0', 'nusashell'));
  const launcher = await readFile(join(home, '.local', 'bin', 'nusashell'), 'utf8');
  assert.match(launcher, /current\/nusashell/);
  assert.equal(await fileExists(join(home, '.config', 'nusashell-desktop')), false);
});

test('release Linux installer can opt into Electron as a separate payload', async () => {
  if (process.platform !== 'linux') return;
  const root = await mkdtemp(join(tmpdir(), 'nusashell-release-electron-'));
  temporaryDirectories.push(root);
  const home = join(root, 'home');
  const fakeBin = join(root, 'bin');
  const corePayload = join(root, 'core-payload');
  const electronPayload = join(root, 'electron-payload');
  const releaseRoot = join(root, 'release');
  const goInstallRoot = join(root, 'go-program');
  const electronInstallRoot = join(root, 'electron-program');
  const coreName = 'nusashell-0.1.0-linux-x64.tar.gz';
  const electronName = 'nusashell-electron-0.2.0-linux-x64.tar.gz';
  await mkdir(home, { recursive: true });
  await mkdir(fakeBin, { recursive: true });
  await mkdir(corePayload, { recursive: true });
  await mkdir(electronPayload, { recursive: true });
  await mkdir(releaseRoot, { recursive: true });
  await writeFile(join(corePayload, 'nusashell'), '#!/usr/bin/env sh\nexit 0\n');
  await writeFile(join(electronPayload, 'nusashell-desktop'), '#!/usr/bin/env sh\nexit 0\n');
  await chmod(join(corePayload, 'nusashell'), 0o755);
  await chmod(join(electronPayload, 'nusashell-desktop'), 0o755);
  await execFileAsync('tar', ['-C', corePayload, '-czf', join(releaseRoot, coreName), 'nusashell']);
  await execFileAsync('tar', ['-C', electronPayload, '-czf', join(releaseRoot, electronName), 'nusashell-desktop']);
  const coreSha = createHash('sha256').update(await readFile(join(releaseRoot, coreName))).digest('hex');
  const electronSha = createHash('sha256').update(await readFile(join(releaseRoot, electronName))).digest('hex');
  await writeFile(join(releaseRoot, 'latest.json'), `${JSON.stringify({
    product: 'go',
    version: '0.1.0',
    files: { 'linux-x64': { name: coreName, sha256: coreSha } },
  }, null, 2)}\n`);
  await writeFile(join(releaseRoot, 'electron-latest.json'), `${JSON.stringify({
    product: 'electron',
    version: '0.2.0',
    files: { 'linux-x64': { name: electronName, sha256: electronSha } },
  }, null, 2)}\n`);
  await writeFile(join(releaseRoot, 'release-versions.json'), `${JSON.stringify({
    schemaVersion: 1,
    go: { version: '0.1.0', tag: 'go-v0.1.0', manifest: 'latest.json', releasedAt: '2026-01-01T00:00:00Z' },
    electron: { version: '0.2.0', tag: 'electron-v0.2.0', manifest: 'electron-latest.json', releasedAt: '2026-02-01T00:00:00Z' },
  }, null, 2)}\n`);
  await writeFile(join(fakeBin, 'curl'), `#!/usr/bin/env sh
set -eu
url=''
destination=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) destination="$2"; shift 2 ;;
    https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  */release-versions.json) cp '${releaseRoot}/release-versions.json' "$destination" ;;
  */electron-latest.json) cp '${releaseRoot}/electron-latest.json' "$destination" ;;
  *electron-v0.2.0/nusashell-electron-0.2.0-linux-x64.tar.gz) cp '${releaseRoot}/${electronName}' "$destination" ;;
  */latest.json) cp '${releaseRoot}/latest.json' "$destination" ;;
  *) cp '${releaseRoot}/${coreName}' "$destination" ;;
esac
`);
  await chmod(join(fakeBin, 'curl'), 0o755);

  const previousElectron = join(electronInstallRoot, 'versions', '0.0.9');
  const runningElectron = join(electronInstallRoot, 'versions', '0.0.8');
  await mkdir(previousElectron, { recursive: true });
  await mkdir(runningElectron, { recursive: true });
  await writeFile(join(previousElectron, 'nusashell-desktop'), '#!/usr/bin/env sh\nexit 0\n');
  await writeFile(join(runningElectron, 'nusashell-desktop'), '#!/usr/bin/env sh\nwhile :; do sleep 1; done\n');
  await chmod(join(previousElectron, 'nusashell-desktop'), 0o755);
  await chmod(join(runningElectron, 'nusashell-desktop'), 0o755);
  await symlink(previousElectron, join(electronInstallRoot, 'current'));
  const runningProcess = spawn(join(runningElectron, 'nusashell-desktop'), [], { detached: true, stdio: 'ignore' });
  await new Promise((resolve, reject) => {
    runningProcess.once('spawn', resolve);
    runningProcess.once('error', reject);
  });

  try {
    await execFileAsync('bash', [script('install.sh').pathname, '--install-electron', '--no-mcp'], {
      env: {
        ...process.env,
        HOME: home,
        PATH: `${fakeBin}:${process.env.PATH}`,
        NUSASHELL_RELEASE_BASE: 'https://fixture.invalid/releases',
        NUSASHELL_RELEASE_INDEX: 'https://fixture.invalid/releases/release-versions.json',
        NUSASHELL_GO_INSTALL_ROOT: goInstallRoot,
        NUSASHELL_ELECTRON_INSTALL_ROOT: electronInstallRoot,
        NUSASHELL_NON_INTERACTIVE: '1',
      },
    });
  } finally {
    try { process.kill(-runningProcess.pid, 'SIGTERM'); } catch {}
  }

  assert.equal(await realpath(join(goInstallRoot, 'current')), join(goInstallRoot, 'versions', '0.1.0'));
  assert.equal(await realpath(join(electronInstallRoot, 'current')), join(electronInstallRoot, 'versions', '0.2.0'));
  assert.equal(await realpath(join(electronInstallRoot, 'current', 'nusashell-desktop')), join(electronInstallRoot, 'versions', '0.2.0', 'nusashell-desktop'));
  assert.equal(await fileExists(join(runningElectron, 'nusashell-desktop')), true);
  assert.match(await readFile(join(home, '.local', 'bin', 'nusashell'), 'utf8'), /go-program\/current\/nusashell/);
  assert.match(await readFile(join(home, '.local', 'bin', 'nusashell-desktop'), 'utf8'), /electron-program\/current\/nusashell-desktop/);
});

test('release Linux installer can opt into the desktop pet as a separate Linux-only payload', async () => {
  if (process.platform !== 'linux') return;
  const root = await mkdtemp(join(tmpdir(), 'nusashell-release-pets-'));
  temporaryDirectories.push(root);
  const home = join(root, 'home');
  const fakeBin = join(root, 'bin');
  const corePayload = join(root, 'core-payload');
  const petsPayload = join(root, 'pets-payload');
  const releaseRoot = join(root, 'release');
  const goInstallRoot = join(root, 'go-program');
  const petsInstallRoot = join(root, 'pets-program');
  const coreName = 'nusashell-0.1.0-linux-x64.tar.gz';
  const petsName = 'nusashell-pets-0.1.3-linux-x64.tar.gz';
  await mkdir(home, { recursive: true });
  await mkdir(fakeBin, { recursive: true });
  await mkdir(corePayload, { recursive: true });
  await mkdir(join(petsPayload, 'assets', 'pets'), { recursive: true });
  await mkdir(releaseRoot, { recursive: true });
  await writeFile(join(corePayload, 'nusashell'), '#!/usr/bin/env sh\nexit 0\n');
  await writeFile(join(petsPayload, 'nusashell-pets'), '#!/usr/bin/env sh\nexit 0\n');
  await writeFile(join(petsPayload, 'assets', 'pets', 'config.json'), JSON.stringify({ name: 'nusa-shell-pet' }) + '\n');
  await writeFile(join(petsPayload, 'assets', 'pets', 'spritesheet.webp'), 'atlas');
  await chmod(join(corePayload, 'nusashell'), 0o755);
  await chmod(join(petsPayload, 'nusashell-pets'), 0o755);
  await execFileAsync('tar', ['-C', corePayload, '-czf', join(releaseRoot, coreName), 'nusashell']);
  await execFileAsync('tar', ['-C', petsPayload, '-czf', join(releaseRoot, petsName), 'nusashell-pets', 'assets']);
  const coreSha = createHash('sha256').update(await readFile(join(releaseRoot, coreName))).digest('hex');
  const petsSha = createHash('sha256').update(await readFile(join(releaseRoot, petsName))).digest('hex');
  await writeFile(join(releaseRoot, 'latest.json'), `${JSON.stringify({
    product: 'go',
    version: '0.1.0',
    files: { 'linux-x64': { name: coreName, sha256: coreSha } },
  }, null, 2)}\n`);
  await writeFile(join(releaseRoot, 'pets-latest.json'), `${JSON.stringify({
    product: 'pets',
    version: '0.1.3',
    files: { 'linux-x64': { name: petsName, sha256: petsSha } },
  }, null, 2)}\n`);
  await writeFile(join(releaseRoot, 'release-versions.json'), `${JSON.stringify({
    schemaVersion: 1,
    go: { version: '0.1.0', tag: 'go-v0.1.0', manifest: 'latest.json', releasedAt: '2026-01-01T00:00:00Z' },
    electron: null,
    pets: { version: '0.1.3', tag: 'pets-v0.1.3', manifest: 'pets-latest.json', releasedAt: '2026-03-01T00:00:00Z' },
  }, null, 2)}\n`);
  await writeFile(join(fakeBin, 'curl'), `#!/usr/bin/env sh
set -eu
url=''
destination=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) destination="$2"; shift 2 ;;
    https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  */release-versions.json) cp '${releaseRoot}/release-versions.json' "$destination" ;;
  */pets-latest.json) cp '${releaseRoot}/pets-latest.json' "$destination" ;;
  *pets-v0.1.3/nusashell-pets-0.1.3-linux-x64.tar.gz) cp '${releaseRoot}/${petsName}' "$destination" ;;
  */latest.json) cp '${releaseRoot}/latest.json' "$destination" ;;
  *) cp '${releaseRoot}/${coreName}' "$destination" ;;
esac
`);
  await chmod(join(fakeBin, 'curl'), 0o755);

  await execFileAsync('bash', [script('install.sh').pathname, '--no-electron', '--install-pets', '--no-mcp'], {
    env: {
      ...process.env,
      HOME: home,
      PATH: `${fakeBin}:${process.env.PATH}`,
      NUSASHELL_RELEASE_BASE: 'https://fixture.invalid/releases',
      NUSASHELL_RELEASE_INDEX: 'https://fixture.invalid/releases/release-versions.json',
      NUSASHELL_GO_INSTALL_ROOT: goInstallRoot,
      NUSASHELL_PETS_INSTALL_ROOT: petsInstallRoot,
      NUSASHELL_NON_INTERACTIVE: '1',
    },
  });

  assert.equal(await realpath(join(goInstallRoot, 'current')), join(goInstallRoot, 'versions', '0.1.0'));
  assert.equal(await realpath(join(petsInstallRoot, 'current')), join(petsInstallRoot, 'versions', '0.1.3'));
  assert.equal(await realpath(join(petsInstallRoot, 'current', 'nusashell-pets')), join(petsInstallRoot, 'versions', '0.1.3', 'nusashell-pets'));
  assert.equal(await fileExists(join(petsInstallRoot, 'current', 'assets', 'pets', 'spritesheet.webp')), true);
  const launcher = await readFile(join(home, '.local', 'bin', 'nusashell-pets'), 'utf8');
  assert.match(launcher, /pets-program\/current\/nusashell-pets/);
  assert.match(launcher, /--assets ".*pets-program\/current\/assets\/pets"/);
});

test('release Linux installer installs an opted-in NusaShell-mcp plugin into Go app data', async () => {
  if (process.platform !== 'linux') return;
  const root = await mkdtemp(join(tmpdir(), 'nusashell-release-mcp-'));
  temporaryDirectories.push(root);
  const home = join(root, 'home');
  const fakeBin = join(root, 'bin');
  const payload = join(root, 'payload');
  const mcpPayload = join(root, 'mcp-payload');
  const releaseRoot = join(root, 'release');
  const installRoot = join(root, 'program');
  const dataRoot = join(root, 'data');
  const coreName = 'nusashell-0.1.0-linux-x64.tar.gz';
  const mcpName = 'notes-2.0.2.tar.gz';
  await mkdir(home, { recursive: true });
  await mkdir(fakeBin, { recursive: true });
  await mkdir(payload, { recursive: true });
  await mkdir(join(mcpPayload, 'mcp'), { recursive: true });
  await mkdir(releaseRoot, { recursive: true });
  await writeFile(join(payload, 'nusashell'), '#!/usr/bin/env sh\nexit 0\n');
  await chmod(join(payload, 'nusashell'), 0o755);
  await writeFile(join(mcpPayload, 'manifest.json'), `${JSON.stringify({ id: 'nusashell.notes', name: 'Notes', version: '2.0.2' }, null, 2)}\n`);
  await writeFile(join(mcpPayload, 'mcp', 'server'), '#!/usr/bin/env sh\nexit 0\n');
  await chmod(join(mcpPayload, 'mcp', 'server'), 0o755);
  await execFileAsync('tar', ['-C', payload, '-czf', join(releaseRoot, coreName), 'nusashell']);
  await execFileAsync('tar', ['-C', mcpPayload, '-czf', join(releaseRoot, mcpName), '.']);
  const coreSha = createHash('sha256').update(await readFile(join(releaseRoot, coreName))).digest('hex');
  await writeFile(join(releaseRoot, 'latest.json'), `${JSON.stringify({
    product: 'go',
    version: '0.1.0',
    files: { 'linux-x64': { name: coreName, sha256: coreSha } },
  }, null, 2)}\n`);
  await writeFile(join(releaseRoot, 'release-versions.json'), `${JSON.stringify({
    schemaVersion: 1,
    go: { version: '0.1.0', tag: 'go-v0.1.0', manifest: 'latest.json', releasedAt: '2026-01-01T00:00:00Z' },
    electron: null,
  }, null, 2)}\n`);
  await writeFile(join(releaseRoot, 'versions.json'), JSON.stringify({
    notes: { version: '2.0.2', tag: 'notes-v2.0.2' },
  }, null, 2) + '\n');
  await writeFile(join(fakeBin, 'curl'), `#!/usr/bin/env sh
set -eu
url=''
destination=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) destination="$2"; shift 2 ;;
    https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  */release-versions.json) cp '${releaseRoot}/release-versions.json' "$destination" ;;
  */versions.json) cp '${releaseRoot}/versions.json' "$destination" ;;
  */notes-2.0.2.tar.gz) cp '${releaseRoot}/${mcpName}' "$destination" ;;
  */latest.json) cp '${releaseRoot}/latest.json' "$destination" ;;
  *) cp '${releaseRoot}/${coreName}' "$destination" ;;
esac
`);
  await chmod(join(fakeBin, 'curl'), 0o755);

  await execFileAsync('bash', [script('install.sh').pathname, '--no-electron', '--install-mcp'], {
    env: {
      ...process.env,
      HOME: home,
      PATH: `${fakeBin}:${process.env.PATH}`,
      NUSASHELL_RELEASE_BASE: 'https://fixture.invalid/releases',
      NUSASHELL_RELEASE_INDEX: 'https://fixture.invalid/releases/release-versions.json',
      NUSASHELL_GO_INSTALL_ROOT: installRoot,
      NUSASHELL_DATA_DIR: dataRoot,
      NUSASHELL_MCP_REPOSITORY: 'fixture/NusaShell-mcp',
      NUSASHELL_MCP_PLUGINS: 'notes',
      NUSASHELL_MCP_RAW_BASE: 'https://fixture.invalid/NusaShell-mcp',
      NUSASHELL_MCP_RELEASE_BASE: 'https://fixture.invalid/NusaShell-mcp/releases/download',
      NUSASHELL_NON_INTERACTIVE: '1',
    },
  });

  assert.equal(await fileExists(join(dataRoot, 'plugins', 'nusashell.notes', 'manifest.json')), true);
  assert.equal(await fileExists(join(dataRoot, 'plugins', 'nusashell.notes', 'mcp', 'server')), true);
});

async function fileExists(path) {
  try {
    await readFile(path);
    return true;
  } catch {
    return false;
  }
}
