import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const nodeFetch = globalThis.fetch.bind(globalThis);
const NativeWebSocket = globalThis.WebSocket;

async function freePort() {
  return new Promise((resolvePort, reject) => {
    const server = createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      server.close(() => resolvePort(port));
    });
  });
}

async function waitFor(check, label, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await check()) return;
    await new Promise((resolveWait) => setTimeout(resolveWait, 40));
  }
  throw new Error(`Timed out waiting for ${label}`);
}

async function buildAndStartServer(port, dataDir) {
  const binary = join(dataDir, process.platform === 'win32' ? 'nusashell-e2e.exe' : 'nusashell-e2e');
  await new Promise((resolveBuild, rejectBuild) => {
    const build = spawn('go', ['build', '-buildvcs=false', '-o', binary, './cmd/nusashell'], {
      cwd: repo,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let output = '';
    build.stdout.on('data', (chunk) => { output += chunk; });
    build.stderr.on('data', (chunk) => { output += chunk; });
    build.once('error', rejectBuild);
    build.once('close', (code) => code === 0 ? resolveBuild() : rejectBuild(new Error(`go build failed (${code}): ${output}`)));
  });
  const go = spawn(binary, [], {
    cwd: repo,
    env: {
      ...process.env,
      NUSASHELL_HOST: '127.0.0.1',
      NUSASHELL_PORT: String(port),
      NUSASHELL_DATA_DIR: dataDir,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let output = '';
  go.stdout.on('data', (chunk) => { output += chunk; });
  go.stderr.on('data', (chunk) => { output += chunk; });
  return { go, output: () => output };
}

function installBrowserGlobals(dom, baseURL) {
  const { window } = dom;
  const fetchFromServer = (input, init) => nodeFetch(new URL(input, baseURL), init);
  globalThis.window = window;
  globalThis.document = window.document;
  globalThis.location = window.location;
  globalThis.history = window.history;
  globalThis.localStorage = window.localStorage;
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: window.navigator });
  globalThis.CustomEvent = window.CustomEvent;
  globalThis.Node = window.Node;
  globalThis.requestAnimationFrame = window.requestAnimationFrame.bind(window);
  globalThis.fetch = fetchFromServer;
  window.fetch = fetchFromServer;
  window.confirm = () => true;
  if (NativeWebSocket) {
    globalThis.WebSocket = NativeWebSocket;
    window.WebSocket = NativeWebSocket;
  }
}

test('embedded frontend completes one representative flow through the Go backend', async (t) => {
  assert.ok(NativeWebSocket, 'Node WebSocket support is required for the E2E event stream');
  const port = await freePort();
  const dataDir = await mkdtemp(join(tmpdir(), 'nusashell-light-e2e-'));
  const baseURL = `http://127.0.0.1:${port}/`;
  const server = await buildAndStartServer(port, dataDir);
  let rpcModule;
  t.after(async () => {
    rpcModule?.closeWS();
    if (server.go.exitCode === null) {
      await new Promise((resolveStop) => {
        const timer = setTimeout(resolveStop, 2000);
        server.go.once('exit', () => {
          clearTimeout(timer);
          resolveStop();
        });
        server.go.kill();
      });
    }
    await rm(dataDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  });

  try {
    await waitFor(async () => {
      try {
        return (await nodeFetch(baseURL)).ok;
      } catch {
        return false;
      }
    }, 'Go server startup', 20000);

    const html = await (await nodeFetch(baseURL)).text();
    const dom = new JSDOM(html, { url: baseURL, pretendToBeVisual: true });
    installBrowserGlobals(dom, baseURL);
    rpcModule = await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'rpc.js')).href}`);
    await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'app.js')).href}?e2e=${Date.now()}`);

    await waitFor(() => document.getElementById('conn-status')?.textContent === 'Connected', 'WebSocket connection');
    await waitFor(() => document.getElementById('skills-count')?.textContent === '0 skills', 'initial skills view');

    document.getElementById('new-skill-btn').click();
    document.getElementById('skill-name').value = 'e2e-skill';
    document.getElementById('skill-description').value = 'Cross-layer smoke skill';
    document.getElementById('skill-content').value = '# E2E\n\nKeep the flow intact.';
    document.getElementById('skill-name').dispatchEvent(new window.Event('input', { bubbles: true }));
    document.getElementById('skill-content').dispatchEvent(new window.Event('input', { bubbles: true }));
    document.getElementById('skill-save-btn').click();
    await waitFor(() => document.querySelector('#skills-list .skills-list-item strong')?.textContent === 'e2e-skill', 'skill save through the UI');

    window.location.hash = '#logs';
    window.dispatchEvent(new window.Event('hashchange'));
    await waitFor(() => document.querySelector('#log-tail .log-line .log-msg')?.textContent.includes('skill saved'), 'live log event in the UI');

    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(() => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((node) => node.textContent === 'Untitled'), 'new conversation through the UI');

    assert.equal(document.querySelectorAll('.view.active').length, 1);
    assert.match(document.getElementById('log-count').textContent, /entries/);
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});
