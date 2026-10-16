// Boot smoke for the promoted shell. The old representative flow drove retired
// conversation-list controls. This keeps the harness server boot and checks
// the contract that still exists at startup: the document title, the home
// data-view, and one app.info RPC round-trip.
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { spawn } from 'node:child_process';
import { test } from 'node:test';

import { createJSDOM, freePort, quiesceFrontend, waitFor } from './e2e-harness.mjs';

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const nodeFetch = globalThis.fetch.bind(globalThis);

test('embedded frontend boots with a view and completes one RPC round-trip', async (t) => {
  const port = await freePort();
  const dataDir = await mkdtemp(join(tmpdir(), 'nusashell-e2e-boot-'));
  const baseURL = `http://127.0.0.1:${port}/`;
  const binary = join(dataDir, 'nusashell-e2e');
  await new Promise((resolveBuild, rejectBuild) => {
    const build = spawn('go', ['build', '-buildvcs=false', '-o', binary, './cmd/nusashell'], {
      cwd: repo,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let output = '';
    build.stdout.on('data', (chunk) => { output += chunk; });
    build.stderr.on('data', (chunk) => { output += chunk; });
    build.once('error', rejectBuild);
    build.once('close', (code) => (code === 0 ? resolveBuild() : rejectBuild(new Error(`go build failed (${code}): ${output}`))));
  });
  const go = spawn(binary, [], {
    cwd: repo,
    env: {
      ...process.env,
      HOME: dataDir,
      USERPROFILE: dataDir,
      NUSASHELL_HOST: '127.0.0.1',
      NUSASHELL_PORT: String(port),
      NUSASHELL_DATA_DIR: dataDir,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  let rpcModule;
  t.after(async () => {
    quiesceFrontend(rpcModule);
    if (go.exitCode === null) {
      await new Promise((resolveStop) => {
        const timer = setTimeout(resolveStop, 2000);
        go.once('exit', () => {
          clearTimeout(timer);
          resolveStop();
        });
        go.kill();
      });
    }
    await rm(dataDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  });

  await waitFor(async () => {
    try {
      return (await nodeFetch(baseURL)).ok;
    } catch {
      return false;
    }
  }, 'Go server startup', 20000);

  const html = await (await nodeFetch(baseURL)).text();
  const dom = createJSDOM(html, baseURL);
  const fetchFromServer = (input, init) => nodeFetch(new URL(input, baseURL), init);
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.location = dom.window.location;
  globalThis.fetch = fetchFromServer;
  dom.window.fetch = fetchFromServer;

  rpcModule = await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'rpc.js')).href}?boot=${Date.now()}`);
  rpcModule.setAutoReconnect(false);
  const info = await rpcModule.rpc('app.info');
  assert.equal(info.name, 'NusaShell');
  assert.equal(document.title, 'NusaShell');
  const home = document.querySelector('[data-view="home"]');
  assert.ok(home, 'the shell declares a home view');
  assert.equal(home.hidden, false, 'home is the visible boot surface');
  assert.equal(document.querySelectorAll('[data-view]').length > 1, true);
});
