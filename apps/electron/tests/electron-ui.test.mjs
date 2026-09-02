import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';

import { electronDevArgs } from '../src/runtime.cjs';

const runUI = process.env.NUSASHELL_ELECTRON_UI === '1';

test('Electron loads the real web shell and preserves composer/workspace interactions', { skip: !runUI ? 'set NUSASHELL_ELECTRON_UI=1 or run npm run test:ui' : false }, async (t) => {
  const { _electron: electron } = await import('playwright-core');
  const repositoryRoot = resolve(import.meta.dirname, '..', '..', '..');
  const electronRoot = join(repositoryRoot, 'apps', 'electron');
  const electronExecutable = join(
    electronRoot,
    'node_modules',
    'electron',
    'dist',
    process.platform === 'win32' ? 'electron.exe' : 'electron',
  );
  const backend = process.env.NUSASHELL_ELECTRON_BACKEND
    || join(electronRoot, 'runtime', process.platform === 'win32' ? 'nusashell.exe' : 'nusashell');

  assert.ok(existsSync(electronExecutable), `Electron binary missing: ${electronExecutable}`);
  assert.ok(existsSync(backend), `Go backend missing: ${backend}; run make electron-build-backend`);

  const temporaryDirectory = await mkdtemp(join(tmpdir(), 'nusashell-electron-ui-'));
  const dataDirectory = join(temporaryDirectory, 'data');
  const userDataDirectory = join(temporaryDirectory, 'electron-user-data');
  const attachmentPath = join(temporaryDirectory, 'electron-note.txt');
  await writeFile(attachmentPath, 'attachment through the native composer');
  let electronApp;

  t.after(async () => {
    if (electronApp) await electronApp.close();
    await rm(temporaryDirectory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  });

  electronApp = await electron.launch({
    executablePath: electronExecutable,
    args: [...electronDevArgs(), electronRoot],
    env: {
      ...process.env,
      NUSASHELL_ELECTRON_BACKEND: backend,
      NUSASHELL_DATA_DIR: dataDirectory,
      NUSASHELL_ELECTRON_USER_DATA: userDataDirectory,
      NUSASHELL_DEV: '1',
    },
  });

  const page = await electronApp.firstWindow();
  const hasApplicationMenu = await electronApp.evaluate(({ Menu }) => Menu.getApplicationMenu() !== null);
  assert.equal(hasApplicationMenu, false);
  await page.waitForFunction(
    () => document.querySelector('#conn-status')?.textContent === 'Connected',
    undefined,
    { timeout: 20000 },
  );
  assert.equal(
    await page.locator('#mini-window-btn').evaluate((button) => button.hidden),
    true,
    'Electron should hide the unsupported mini-window control',
  );
  await page.evaluate(() => {
    window.location.hash = '#agent';
    window.dispatchEvent(new Event('hashchange'));
  });
  await page.locator('#composer-input').waitFor({ state: 'visible', timeout: 10000 });

  // This is the same native composer used by the browser build.
  const composer = page.locator('#composer-input');
  await composer.fill('hello from the Electron renderer');
  assert.equal(await composer.inputValue(), 'hello from the Electron renderer');

  // Observe the native path before the composer clears the input after
  // reading it. This verifies the modern Electron bridge, not the removed
  // File.path property.
  const nativePath = page.evaluate(() => new Promise((resolve) => {
    const input = document.querySelector('#agent-file-input');
    input.addEventListener('change', () => {
      window.__electronSmokeFile = input.files[0];
      resolve(window.nusashellDesktop.getPathForFile(input.files[0]));
    }, { once: true });
  }));
  await page.locator('#agent-file-input').setInputFiles(attachmentPath);
  assert.equal(await nativePath, attachmentPath);
  await page.locator('.agent-attachment').waitFor({ state: 'visible', timeout: 10000 });
  assert.match(await page.locator('.agent-attachment').first().textContent(), /electron-note\.txt/);

  // Feed the same native File through the directory-entry branch used by a
  // real OS folder drop. The entry is synthetic only to avoid pointer-driven
  // drag automation in CI; path resolution still crosses the actual preload
  // bridge and the composer creates a path-only folder chip.
  await page.evaluate(() => {
    const entry = {
      isDirectory: true,
      name: 'electron-project',
      file(success) {
        success(window.__electronSmokeFile);
      },
    };
    const dataTransfer = {
      items: [{ kind: 'file', webkitGetAsEntry: () => entry }],
      files: [],
    };
    const event = new Event('drop', { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'dataTransfer', { value: dataTransfer });
    document.querySelector('#agent-conversation').dispatchEvent(event);
  });
  await page.waitForFunction(() => document.querySelectorAll('.agent-attachment').length === 2);
  assert.match(await page.locator('.agent-attachment').nth(1).textContent(), /electron-project/);

  // The Go backend's native zenity call is already covered by transport tests;
  // intercept only this RPC here so the renderer interaction can be tested
  // deterministically without opening a host folder dialog in CI.
  await page.locator('#new-conversation-btn').click();
  await page.locator('#conversation-list .agent-conversation-item').first().waitFor({ state: 'visible', timeout: 10000 });
  let workspaceRPC;
  await page.route('**/rpc/agent/conversations/pick-workspace', async (route) => {
    const request = JSON.parse(route.request().postData() || '{}');
    workspaceRPC = request;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ok: true,
        result: {
          conversation: {
            id: request.payload?.id,
            workspace: '/tmp/electron-workspace',
          },
        },
      }),
    });
  });
  await page.locator('#agent-workspace-btn').click();
  await page.waitForFunction(() => document.querySelector('#agent-workspace-label')?.textContent === 'electron-workspace');
  assert.equal(workspaceRPC?.method, 'agent.conversations.pick-workspace');
  assert.ok(workspaceRPC?.payload?.id, 'workspace RPC must target the active conversation');

  const bridgeState = await page.evaluate(() => ({
    bridgeAvailable: typeof window.nusashellDesktop?.getPathForFile === 'function',
    rendererHasNodeRequire: typeof window.require === 'function',
    bridgeKeys: Object.keys(window.nusashellDesktop || {}),
    syntheticFilePath: window.nusashellDesktop?.getPathForFile(new File(['x'], 'synthetic.txt')),
  }));
  assert.equal(bridgeState.bridgeAvailable, true);
  assert.equal(bridgeState.rendererHasNodeRequire, false);
  assert.deepEqual(bridgeState.bridgeKeys, ['getPathForFile']);
  assert.equal(bridgeState.syntheticFilePath, null);
});
