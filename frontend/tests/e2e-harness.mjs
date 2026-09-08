// Shared Go + FakeLLM harness for frontend E2E tests.
// Every agent-facing E2E boots an OpenAI-compatible FakeLLM and registers it
// as the only enabled provider so turns never hit a real upstream.

import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { createServer as createHTTPServer } from 'node:http';
import { JSDOM, VirtualConsole } from 'jsdom';

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const nodeFetch = globalThis.fetch.bind(globalThis);
export const NativeWebSocket = globalThis.WebSocket;

const silentConsole = new VirtualConsole();
silentConsole.on('error', () => {});
silentConsole.on('warn', () => {});
silentConsole.on('jsdomError', () => {});

export function createJSDOM(html, baseURL) {
  return new JSDOM(html, {
    url: baseURL,
    pretendToBeVisual: true,
    virtualConsole: silentConsole,
  });
}

export async function freePort() {
  return new Promise((resolvePort, reject) => {
    const server = createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      server.close(() => resolvePort(port));
    });
  });
}

export async function waitFor(check, label, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await check()) return;
    await new Promise((resolveWait) => setTimeout(resolveWait, 40));
  }
  throw new Error(`Timed out waiting for ${label}`);
}

export async function startTurn(rpc, params, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (true) {
    try {
      return await rpc('agent.turns.start', params);
    } catch (err) {
      if (!String(err?.message || err).includes('conversation is busy') || Date.now() >= deadline) {
        throw err;
      }
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
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
    build.once('close', (code) => (code === 0 ? resolveBuild() : rejectBuild(new Error(`go build failed (${code}): ${output}`))));
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
  globalThis.Element = window.Element;
  globalThis.HTMLElement = window.HTMLElement;
  globalThis.SVGElement = window.SVGElement;
  globalThis.requestAnimationFrame = window.requestAnimationFrame.bind(window);
  globalThis.fetch = fetchFromServer;
  window.fetch = fetchFromServer;
  window.confirm = () => true;
  const MutationObserverPolyfill = class {
    constructor() { this.callback = null; }
    observe() {}
    disconnect() {}
    takeRecords() { return []; }
  };
  globalThis.MutationObserver = window.MutationObserver ?? MutationObserverPolyfill;
  window.MutationObserver = globalThis.MutationObserver;
  if (NativeWebSocket) {
    globalThis.WebSocket = NativeWebSocket;
    window.WebSocket = NativeWebSocket;
  }
}

// fakeLLM: OpenAI-compatible /v1/models + /v1/chat/completions.
export const LONG_COMPACTION_SUMMARY = 'SUMMARY: user explored compaction e2e test. '
  + 'They seeded four large turns about Go concurrency, verified token estimates, '
  + 'and confirmed the rolling summarizer folds each chunk into a running checkpoint. ';

export function fakeLLM(port, { contextLength = 1000 } = {}) {
  let completeText = 'compaction summary: user likes Go.';
  let scripts = [];
  let sendDone = true;
  let sendFinish = true;
  const requests = [];
  const server = createHTTPServer((req, res) => {
    const url = new URL(req.url, `http://127.0.0.1:${port}`);
    if (req.method === 'GET' && url.pathname.endsWith('/models')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        data: [
          { id: 'tiny-model', context_length: contextLength, description: 'Tiny test model' },
        ],
      }));
      return;
    }
    if (req.method === 'POST' && url.pathname.endsWith('/chat/completions')) {
      let body = '';
      req.on('data', (chunk) => { body += chunk; });
      req.on('end', () => {
        const parsed = JSON.parse(body);
        requests.push({ stream: !!parsed.stream, body: parsed });
        if (!parsed.stream) {
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({
            choices: [{ message: { role: 'assistant', content: completeText }, finish_reason: 'stop' }],
            usage: { prompt_tokens: 10, completion_tokens: 5 },
          }));
          return;
        }
        const steps = scripts.shift() || [{ text: 'ok' }];
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        for (const step of steps) {
          const delta = {};
          if (step.text) delta.content = step.text;
          if (step.reasoning) delta.reasoning_content = step.reasoning;
          if (step.toolCall) {
            delta.tool_calls = [{
              index: step.toolCall.index ?? 0,
              id: step.toolCall.id,
              type: 'function',
              function: { name: step.toolCall.name, arguments: step.toolCall.arguments ?? '{}' },
            }];
          }
          res.write(`data: ${JSON.stringify({ choices: [{ delta }] })}\n\n`);
        }
        if (sendFinish) {
          res.write(`data: ${JSON.stringify({ choices: [{ delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 5 } })}\n\n`);
        }
        if (sendDone) {
          res.write('data: [DONE]\n\n');
        }
        res.end();
      });
      return;
    }
    res.writeHead(404);
    res.end('not found');
  });
  return {
    server,
    setComplete: (text) => { completeText = text; },
    setScripts: (s) => { scripts = s; },
    setSendDone: (v) => { sendDone = v; },
    setSendFinish: (v) => { sendFinish = v; },
    requests: () => requests,
    url: `http://127.0.0.1:${port}`,
  };
}

/**
 * Boot FakeLLM + Go nusashell + jsdom frontend with FakeLLM registered.
 * Callers get a connected UI and an enabled provider pointing at FakeLLM.
 */
export async function startE2EHarness(t, {
  prefix = 'nusashell-e2e',
  llmOptions = {},
  registerProvider = true,
} = {}) {
  assert.ok(NativeWebSocket, 'Node WebSocket support is required for the E2E event stream');

  const llmPort = await freePort();
  const llm = fakeLLM(llmPort, llmOptions);
  await new Promise((resolveListen) => llm.server.listen(llmPort, '127.0.0.1', resolveListen));

  const port = await freePort();
  const dataDir = await mkdtemp(join(tmpdir(), `${prefix}-`));
  const baseURL = `http://127.0.0.1:${port}/`;
  const server = await buildAndStartServer(port, dataDir);
  let rpcModule;

  t.after(async () => {
    quiesceFrontend(rpcModule);
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
    await new Promise((r) => llm.server.close(r));
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
  installBrowserGlobals(dom, baseURL);
  // Import rpc without a cache-bust query so app.js's `./rpc.js` resolves to the
  // same module instance — otherwise closeWS() cannot stop the live reconnect timer.
  rpcModule = await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'rpc.js')).href}`);
  // Prior tests may have quiesced the shared module; re-enable reconnect.
  rpcModule.setAutoReconnect?.(true);
  await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'app.js')).href}?e2e=${Date.now()}`);
  await waitFor(() => document.getElementById('conn-status')?.textContent === 'Connected', 'WebSocket connection');

  let providerID = '';
  if (registerProvider) {
    const saveRes = await rpcModule.rpc('ai.providers.save', {
      kind: 'chat',
      name: 'FakeLLM',
      base_url: `${llm.url}/v1`,
      api_key: 'test-key',
      enabled: true,
    });
    providerID = saveRes.providers[0].id;
    await rpcModule.rpc('ai.providers.import-models', { id: providerID });
  }

  return {
    llm,
    rpc: rpcModule.rpc.bind(rpcModule),
    rpcModule,
    providerID,
    server,
    dataDir,
    baseURL,
    port,
    repo,
  };
}

/** Stop UI polling/reconnect before killing the Go process (avoids Backend unreachable spam). */
export function quiesceFrontend(rpcModule) {
  try { rpcModule?.setAutoReconnect?.(false); } catch { /* ignore */ }
  try { rpcModule?.closeWS(); } catch { /* ignore */ }
  try {
    if (globalThis.document?.documentElement) {
      globalThis.document.documentElement.dataset.backendStatus = 'offline';
    }
  } catch { /* ignore */ }
}
