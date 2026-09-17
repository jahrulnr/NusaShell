import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';

import { electronDevArgs } from '../src/runtime.cjs';

const runUI = process.env.NUSASHELL_ELECTRON_UI === '1';

// startFakeUpstream serves the minimal OpenAI-compatible surface the backend
// needs to register a provider and create a durable conversation through the
// lazy first-turn flow. A fresh data directory has no provider, and the room
// only becomes durable when a turn starts, so the smoke test drives the real
// RPC path against this upstream instead of seeding store files.
async function startFakeUpstream() {
  const server = createServer((req, res) => {
    const url = new URL(req.url, 'http://127.0.0.1');
    if (req.method === 'GET' && url.pathname.endsWith('/models')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        // A wide window keeps this room far below the compaction trigger;
        // the smoke test only needs the first turn to create the room.
        data: [{ id: 'tiny-model', context_length: 200000, description: 'Electron smoke upstream' }],
      }));
      return;
    }
    if (req.method === 'POST' && url.pathname.endsWith('/chat/completions')) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.write(`data: ${JSON.stringify({ choices: [{ delta: { content: 'smoke reply' } }] })}\n\n`);
      res.write(`data: ${JSON.stringify({
        choices: [{ delta: {}, finish_reason: 'stop' }],
        usage: { prompt_tokens: 5, completion_tokens: 2 },
      })}\n\n`);
      res.write('data: [DONE]\n\n');
      res.end();
      return;
    }
    res.writeHead(404);
    res.end('not found');
  });
  await new Promise((resolveListen, rejectListen) => {
    server.once('error', rejectListen);
    server.listen(0, '127.0.0.1', resolveListen);
  });
  return { server, baseURL: `http://127.0.0.1:${server.address().port}` };
}

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
  assert.ok(existsSync(backend), `Go backend missing: ${backend}; run make -C apps/electron build-backend`);

  const temporaryDirectory = await mkdtemp(join(tmpdir(), 'nusashell-electron-ui-'));
  const dataDirectory = join(temporaryDirectory, 'data');
  const userDataDirectory = join(temporaryDirectory, 'electron-user-data');
  const attachmentPath = join(temporaryDirectory, 'electron-note.txt');
  await writeFile(attachmentPath, 'attachment through the native composer');
  let electronApp;
  let page;
  let smokeProviderID = '';
  let smokeConversationID = '';

  // Cleanup mirrors the test's own fixtures: when a local core already
  // listens on the default port the renderer talks to it instead of the
  // staged backend, so the smoke provider/room must not be left behind.
  t.after(async () => {
    if (electronApp && page) {
      try {
        await page.evaluate(async ({ providerID, conversationID }) => {
          const call = async (method, payload) => {
            const response = await fetch(`/rpc/${method.replace(/\./g, '/')}`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ method, payload }),
            });
            if (!response.ok) throw new Error(`${method} failed (${response.status})`);
            return response.json().catch(() => ({}));
          };
          if (conversationID) await call('agent.conversations.delete', { id: conversationID });
          if (providerID) await call('ai.providers.delete', { id: providerID });
        }, { providerID: smokeProviderID, conversationID: smokeConversationID });
      } catch {
        // The core may already be stopping; cleanup is best effort.
      }
    }
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

  page = await electronApp.firstWindow();
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

  // A durable room only exists after the first user turn (lazy conversation
  // creation), so create one through the renderer's own RPC against a local
  // fake upstream and open it from the room list. The workspace assertions
  // below then target a real conversation id instead of a browser-only draft.
  await page.locator('#new-conversation-btn').click();
  const upstream = await startFakeUpstream();
  t.after(() => new Promise((resolveClose) => upstream.server.close(resolveClose)));
  await page.evaluate(() => {
    window.__electronSmokeRPC = async (method, payload) => {
      const response = await fetch(`/rpc/${method.replace(/\./g, '/')}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ method, payload }),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error?.message || `${method} failed (${response.status})`);
      return body.result ?? {};
    };
  });
  const saved = await page.evaluate(
    (baseURL) => window.__electronSmokeRPC('ai.providers.save', {
      kind: 'chat',
      name: 'Electron smoke upstream',
      base_url: `${baseURL}/v1`,
      api_key: 'smoke',
      enabled: true,
    }),
    upstream.baseURL,
  );
  smokeProviderID = saved.providers[0].id;
  await page.evaluate((id) => window.__electronSmokeRPC('ai.providers.import-models', { id }), smokeProviderID);
  // One unique draft key per run: the backend dedupes lazy turns by key for
  // its TTL, so a reused key would resolve to the previous run's conversation.
  const conversationKey = `electron-smoke-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
  const started = await page.evaluate(
    ({ model, conversationKey }) => window.__electronSmokeRPC('agent.turns.start', {
      conversation_key: conversationKey,
      text: 'hello from the Electron renderer',
      model,
    }),
    { model: `${smokeProviderID}:tiny-model`, conversationKey },
  );
  smokeConversationID = started.conversation_id;
  assert.ok(smokeConversationID, 'the first turn must create a durable conversation');

  // The backend writes the room (the renderer's draft has no id), so reload
  // the Agent view to fetch the list, then open the room from it.
  await page.evaluate(() => {
    window.location.hash = '#logs';
    window.dispatchEvent(new Event('hashchange'));
  });
  await page.evaluate(() => {
    window.location.hash = '#agent';
    window.dispatchEvent(new Event('hashchange'));
  });
  const room = page.locator(`#conversation-list .agent-conversation-item[data-conversation-id="${smokeConversationID}"]`);
  await room.waitFor({ state: 'visible', timeout: 20000 });
  await room.locator('.agent-conversation-open').click();

  // The in-app workspace browser replaced the native zenity call. Both RPCs
  // are intercepted here so the renderer interaction can be tested
  // deterministically without touching the host filesystem in CI.
  const workspaceRPCs = [];
  await page.route('**/rpc/agent/workspace/list-dirs', async (route) => {
    const request = JSON.parse(route.request().postData() || '{}');
    workspaceRPCs.push(request);
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ok: true,
        result: {
          path: '/tmp/electron-workspace',
          parent: '/tmp',
          entries: [],
          truncated: false,
        },
      }),
    });
  });
  await page.route('**/rpc/agent/conversations/set-workspace', async (route) => {
    const request = JSON.parse(route.request().postData() || '{}');
    workspaceRPCs.push(request);
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ok: true,
        result: {
          conversation: {
            id: request.payload?.id,
            workspace: request.payload?.path,
          },
        },
      }),
    });
  });
  await page.locator('#agent-workspace-btn').click();
  // The picker popup lists the folder, then enables "Use this folder".
  await page.waitForFunction(() => {
    const buttons = [...document.querySelectorAll('.ui-dialog-actions button')];
    const select = buttons[buttons.length - 1];
    return select && !select.disabled;
  });
  await page.locator('.ui-dialog-actions button').last().click();
  await page.waitForFunction(() => document.querySelector('#agent-workspace-label')?.textContent === 'electron-workspace');
  assert.equal(workspaceRPCs[0]?.method, 'agent.workspace.list-dirs');
  assert.equal(workspaceRPCs[1]?.method, 'agent.conversations.set-workspace');
  assert.equal(workspaceRPCs[1]?.payload?.path, '/tmp/electron-workspace');
  assert.equal(workspaceRPCs[1]?.payload?.id, smokeConversationID, 'workspace RPC must target the created conversation');

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
