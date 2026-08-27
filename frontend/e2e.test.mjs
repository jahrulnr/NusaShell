import assert from 'node:assert/strict';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { createServer as createHTTPServer } from 'node:http';
import { test } from 'node:test';
import { JSDOM, VirtualConsole } from 'jsdom';

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const nodeFetch = globalThis.fetch.bind(globalThis);
const NativeWebSocket = globalThis.WebSocket;

// Silenced virtual console: JSDOM prints "Not implemented: ..." warnings for
// canvas getContext and other browser-only APIs that vis-network probes. These
// are expected in a headless test environment and add noise without value.
const silentConsole = new VirtualConsole();
silentConsole.on('error', () => {});
silentConsole.on('warn', () => {});
silentConsole.on('jsdomError', () => {});

function createJSDOM(html, baseURL) {
  return new JSDOM(html, {
    url: baseURL,
    pretendToBeVisual: true,
    virtualConsole: silentConsole,
  });
}

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

// startTurn wraps agent.turns.start with a retry on "conversation is busy".
// On slow CI runners, there is a harmless race between finishTurn setting
// status=idle (visible via RPC) and the deferred cleanup deleting the run
// from the active runs map. Without a retry, the next turns.start hits
// "conversation is busy" even though the previous turn is done.
async function startTurn(rpc, params, timeoutMs = 10000) {
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

// fakeLLM starts a minimal OpenAI-compatible HTTP server that serves
// /v1/models and /v1/chat/completions (streaming + non-streaming).
// Non-streaming requests (used by compaction) return the configured
// completeText; streaming requests return the next script from the queue.
//
// Stub queues are separated per consumer role so background subsystems
// cannot steal each other's scripted replies:
//   - compaction: non-streaming requests advertising tools:["summary"]
//     -> completeText (must satisfy the backend quality guard:
//     summaries shorter than compactionSummaryMinChars (200) are retried
//     and ultimately fail the turn with EventCompactionFailed).
//   - autolearn review agent: streaming requests advertising
//     tools:["review_transcript"] -> reviewScripts queue.
//   - main chat: every other streaming request -> scripts queue.
const LONG_COMPACTION_SUMMARY = 'SUMMARY: user explored compaction e2e test. '
  + 'They seeded four large turns about Go concurrency, verified token estimates, '
  + 'and confirmed the rolling summarizer folds each chunk into a running checkpoint. ';
//
// Script step shape: { text, reasoning, toolCall: { id, name, arguments } }
// When sendDone is false, the stream closes without `data: [DONE]` to
// simulate an incomplete SSE stream (BH-AI-01).
function fakeLLM(port) {
  let completeText = 'compaction summary: user likes Go.';
  let scripts = [];
  // Autolearn review-agent scripts live in their own queue: the review
  // agent fires concurrently with compaction/post-compaction turns and
  // used to pop from `scripts`, racing the main chat for the next stub.
  let reviewScripts = [];
  let sendDone = true;
  // sendFinish controls the trailing finish_reason chunk. Compat streams
  // treat finish_reason without [DONE] as a normal termination (many
  // OpenAI-compatible gateways omit the sentinel), so simulating a true
  // mid-stream cut requires disabling both sendFinish and sendDone.
  let sendFinish = true;
  // requests captures every parsed chat/completions request body alongside
  // whether it was a streaming request. Used by hydration E2E tests to
  // verify the synthetic runtime-hydration transcript is present in the
  // messages array sent to the provider.
  const requests = [];
  const server = createHTTPServer((req, res) => {
    const url = new URL(req.url, `http://127.0.0.1:${port}`);
    if (req.method === 'GET' && url.pathname.endsWith('/models')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        data: [
          { id: 'tiny-model', context_length: 1000, description: 'Tiny test model' },
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
          // Non-streaming: used by compaction summary requests.
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({
            choices: [{ message: { role: 'assistant', content: completeText }, finish_reason: 'stop' }],
            usage: { prompt_tokens: 10, completion_tokens: 5 },
          }));
          return;
        }
        // Streaming: route by consumer role so background subsystems
        // cannot steal each other's scripted replies.
        const toolNames = new Set((parsed.tools || []).map((t) => t?.function?.name));
        const pool = toolNames.has('review_transcript') ? reviewScripts : scripts;
        const steps = pool.shift() || [{ text: 'ok' }];
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
    setReviewScripts: (s) => { reviewScripts = s; },
    setSendDone: (v) => { sendDone = v; },
    setSendFinish: (v) => { sendFinish = v; },
    requests: () => requests,
    url: `http://127.0.0.1:${port}`,
  };
}

test('embedded frontend completes one representative flow through the Go backend', async (t) => {
  assert.ok(NativeWebSocket, 'Node WebSocket support is required for the E2E event stream');
  const port = await freePort();
  const dataDir = await mkdtemp(join(tmpdir(), 'nusashell-e2e-'));
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
    const dom = createJSDOM(html, baseURL);
    installBrowserGlobals(dom, baseURL);
    rpcModule = await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'rpc.js')).href}`);
    await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'app.js')).href}?e2e=${Date.now()}`);

    await waitFor(() => document.getElementById('conn-status')?.textContent === 'Connected', 'WebSocket connection');
    await waitFor(() => /skills$/.test(document.getElementById('skills-count')?.textContent || ''), 'initial skills view');

    window.location.hash = '#settings';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('settings-max-tool-rounds').value = '2';
    document.getElementById('settings-save-btn').click();
    await waitFor(() => document.getElementById('settings-save-status')?.textContent === 'Saved on this device.', 'settings save through the UI');
    assert.equal((await rpcModule.rpc('settings.get')).settings.max_tool_rounds, 2);

    // Skills CRUD is now install/delete only (no inline create form), so the
    // representative flow skips skill creation. Verify the logs view renders
    // live events from the conversation creation below instead.

    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(() => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((node) => node.textContent === 'Untitled'), 'new conversation through the UI');

    window.location.hash = '#logs';
    window.dispatchEvent(new window.Event('hashchange'));
    await waitFor(() => [...document.querySelectorAll('#log-tail .log-line .log-msg')].some((el) => el.textContent.includes('conversation created')), 'live log event in the UI');

    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    assert.equal(document.querySelectorAll('.view.active').length, 1);
    assert.match(document.getElementById('log-count').textContent, /entries/);

    server.go.kill();
    await waitFor(
      () => !document.getElementById('offline-screen')?.hidden,
      'full-window offline state after the local backend stops',
      20000,
    );
    assert.match(document.getElementById('offline-screen').textContent, /Sorry, it looks like your agent is offline\./);
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

test('compaction triggers and renders a marker when conversation exceeds threshold', async (t) => {
  assert.ok(NativeWebSocket, 'Node WebSocket support is required for the E2E event stream');
  const llmPort = await freePort();
  const llm = fakeLLM(llmPort);
  await new Promise((resolveListen) => llm.server.listen(llmPort, '127.0.0.1', resolveListen));
  t.after(() => new Promise((r) => llm.server.close(r)));

  const port = await freePort();
  const dataDir = await mkdtemp(join(tmpdir(), 'nusashell-compaction-e2e-'));
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
    const dom = createJSDOM(html, baseURL);
    installBrowserGlobals(dom, baseURL);
    rpcModule = await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'rpc.js')).href}`);
    await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'app.js')).href}?e2e=${Date.now()}`);

    await waitFor(() => document.getElementById('conn-status')?.textContent === 'Connected', 'WebSocket connection');

    // Save a provider pointing at the fake LLM.
    const saveRes = await rpcModule.rpc('ai.providers.save', {
      kind: 'chat', name: 'FakeLLM', base_url: `${llm.url}/v1`, api_key: 'test-key', enabled: true,
    });
    const providerID = saveRes.providers[0].id;

    // Import models from the fake LLM (tiny-model with 1k context).
    await rpcModule.rpc('ai.providers.import-models', { id: providerID });

    // Seed history with compaction disabled, then enable with small context.
    await rpcModule.rpc('settings.set', { compaction_enabled: false });

    // Create a conversation.
    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(
      () => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((n) => n.textContent === 'Untitled'),
      'new conversation through the UI',
    );
    const conversations = await rpcModule.rpc('agent.conversations.list');
    const convID = conversations.conversations[0].id;

    // Seed 4 turns with large messages (~4000 tokens total).
    const bigMsg = 'x'.repeat(2000);
    for (let i = 0; i < 4; i++) {
      llm.setScripts([[{ text: bigMsg }]]);
      await startTurn(rpcModule.rpc, {
        conversation_id: convID, text: bigMsg, model: 'tiny-model',
      });
      await waitFor(async () => {
        const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
        return c.conversation?.status === 'idle';
      }, `turn ${i + 1} done`, 15000);
    }

    // Enable compaction with a small context window (trigger = 800 tokens).
    // The seeded history (~4000 tokens) is well above the trigger.
    await rpcModule.rpc('settings.set', { max_input_tokens: 1000, compaction_enabled: true });

    // Set the compaction summary response.
    llm.setComplete(LONG_COMPACTION_SUMMARY);
    llm.setScripts([[{ text: 'Turn after compaction.' }]]);

    // Trigger compaction with a new user message.
    await startTurn(rpcModule.rpc, {
      conversation_id: convID, text: 'continue', model: 'tiny-model',
    });

    // Wait for the compaction marker to appear in the UI.
    await waitFor(
      () => [...document.querySelectorAll('#agent-thread .agent-compaction-marker')].some(
        (n) => n.textContent.includes('Compacted'),
      ),
      'compaction marker in the UI',
      15000,
    );
    // Verify it rendered as an assistant bubble, not a standalone pill.
    assert.ok(
      document.querySelector('#agent-thread .agent-message.agent-compaction-marker'),
      'compaction marker should be in an agent-message assistant bubble',
    );

    // Wait for the turn to finish.
    await waitFor(async () => {
      const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
      return c.conversation?.status === 'idle';
    }, 'post-compaction turn done', 15000);

    // Verify the conversation has the compaction marker in persisted state.
    // Compaction summaries carry role=user with a "Compacted context handover:"
    // prefix so they appear in the provider request's messages array.
    const gotten = await rpcModule.rpc('agent.conversations.get', { id: convID });
    const compactionMsgs = gotten.messages?.filter(
      (m) => m.role === 'user' && m.content?.startsWith('Compacted context handover:'),
    ) || [];
    assert.ok(compactionMsgs.length > 0, 'at least one compaction summary (user) message exists');
    assert.ok(
      compactionMsgs.some((m) => m.content.includes('compacted') || m.content.includes('Compacted')),
      `compaction marker not found: ${JSON.stringify(compactionMsgs.map((m) => m.content.slice(0, 80)))}`,
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

// BH-AI-01: A stream cut mid-stream (no finish_reason, no [DONE]) that had
// accumulated tool-call deltas must surface as an error after the retry
// loop exhausts — never silently fall back to non-streaming, and never
// silently discard the accumulated tool calls.
//
// Architecture note: the old TS fallback layering (isIncompleteEmptyStream
// -> non-streaming retry) was removed; errors surface explicitly to the
// retry loop (see AGENTS.md). Compat streams intentionally treat
// finish_reason without [DONE] as a normal termination (many OpenAI-
// compatible gateways omit the sentinel), so this test simulates a TRUE
// mid-stream cut by omitting both the finish_reason chunk and [DONE].
test('BH-AI-01: incomplete stream with tool-call deltas must not silently fall back to non-streaming', async (t) => {
  assert.ok(NativeWebSocket, 'Node WebSocket support is required for the E2E event stream');
  const llmPort = await freePort();
  const llm = fakeLLM(llmPort);
  await new Promise((resolveListen) => llm.server.listen(llmPort, '127.0.0.1', resolveListen));
  t.after(() => new Promise((r) => llm.server.close(r)));

  const port = await freePort();
  const dataDir = await mkdtemp(join(tmpdir(), 'nusashell-bh-ai-01-'));
  const baseURL = `http://127.0.0.1:${port}/`;
  const server = await buildAndStartServer(port, dataDir);
  let rpcModule;
  t.after(async () => {
    rpcModule?.closeWS();
    if (server.go.exitCode === null) {
      await new Promise((resolveStop) => {
        const timer = setTimeout(resolveStop, 2000);
        server.go.once('exit', () => { clearTimeout(timer); resolveStop(); });
        server.go.kill();
      });
    }
    await rm(dataDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  });

  try {
    await waitFor(async () => {
      try { return (await nodeFetch(baseURL)).ok; } catch { return false; }
    }, 'Go server startup', 20000);

    const html = await (await nodeFetch(baseURL)).text();
    const dom = createJSDOM(html, baseURL);
    installBrowserGlobals(dom, baseURL);
    rpcModule = await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'rpc.js')).href}`);
    await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'app.js')).href}?e2e=${Date.now()}`);

    await waitFor(() => document.getElementById('conn-status')?.textContent === 'Connected', 'WebSocket connection');

    // Save a provider pointing at the fake LLM.
    const saveRes = await rpcModule.rpc('ai.providers.save', {
      kind: 'chat', name: 'FakeLLM', base_url: `${llm.url}/v1`, api_key: 'test-key', enabled: true,
    });
    const providerID = saveRes.providers[0].id;
    await rpcModule.rpc('ai.providers.import-models', { id: providerID });

    // Create a conversation.
    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(
      () => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((n) => n.textContent === 'Untitled'),
      'new conversation through the UI',
    );
    const conversations = await rpcModule.rpc('agent.conversations.list');
    const convID = conversations.conversations[0].id;

    // Script: stream a tool-call delta for the built-in "skill" family tool,
    // then cut the connection mid-stream: no finish_reason chunk and no
    // [DONE]. (finish_reason alone would be a normal termination for
    // compat streams — see the test header.)
    llm.setSendDone(false);
    llm.setSendFinish(false);
    llm.setScripts([[
      { toolCall: { id: 'call_bh01', name: 'skill', arguments: '{"op":"list"}' } },
    ]]);
    // Non-streaming fallback returns this text — if a fallback layer were
    // (re)introduced, the turn would silently produce this instead of
    // surfacing the error.
    llm.setComplete('FALLBACK_TEXT_FROM_NON_STREAMING');

    await startTurn(rpcModule.rpc, {
      conversation_id: convID, text: 'list skills', model: 'tiny-model',
    });

    // Wait for the turn to settle (either done or error).
    await waitFor(async () => {
      const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
      return c.conversation?.status === 'idle';
    }, 'turn settle', 15000);

    const gotten = await rpcModule.rpc('agent.conversations.get', { id: convID });
    const assistantMsgs = gotten.messages?.filter((m) => m.role === 'assistant') || [];
    const lastAssistant = assistantMsgs[assistantMsgs.length - 1];

    // Every streaming attempt is cut mid-stream (no finish_reason, no
    // [DONE]), so the compat stream surfaces a provider error on each
    // attempt. The retry loop exhausts (the fake LLM always cuts), the
    // partial-stream continuation fires once and fails too, and the turn
    // ends with status=error. The fallback text must NOT appear — there is
    // no non-streaming fallback layer, and the tool calls must not be
    // silently discarded.
    assert.ok(lastAssistant, 'an assistant message must exist');
    assert.notEqual(
      lastAssistant.content, 'FALLBACK_TEXT_FROM_NON_STREAMING',
      'BH-AI-01: must not silently fall back to non-streaming when tool calls were accumulated',
    );
    // The main chat must never receive a non-streaming request: the only
    // non-streaming consumer is compaction, and this test has none.
    const nonStreamReqs = llm.requests().filter((r) => !r.stream);
    assert.equal(
      nonStreamReqs.length, 0,
      'BH-AI-01: no non-streaming fallback request may be made for the main chat',
    );
    assert.equal(
      lastAssistant.status, 'error',
      `BH-AI-01: incomplete stream with tool calls must surface as an error, not silent success. ` +
        `status=${lastAssistant.status}, content=${JSON.stringify(lastAssistant.content)}`,
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

// BH-SETTINGS-01: SettingsSetRequest uses *float64 with omitempty for
// sampling parameters (temperature, top_p, etc.). JSON null and JSON-absent
// both decode to nil, which handleSettingsSet treats as "don't change."
// There is no way to distinguish "clear to nil" from "leave unchanged" —
// once a sampling parameter is set, it cannot be cleared.
test('BH-SETTINGS-01: sampling parameters cannot be cleared to null once set', async (t) => {
  assert.ok(NativeWebSocket, 'Node WebSocket support is required for the E2E event stream');
  const port = await freePort();
  const dataDir = await mkdtemp(join(tmpdir(), 'nusashell-bh-settings-01-'));
  const baseURL = `http://127.0.0.1:${port}/`;
  const server = await buildAndStartServer(port, dataDir);
  let rpcModule;
  t.after(async () => {
    rpcModule?.closeWS();
    if (server.go.exitCode === null) {
      await new Promise((resolveStop) => {
        const timer = setTimeout(resolveStop, 2000);
        server.go.once('exit', () => { clearTimeout(timer); resolveStop(); });
        server.go.kill();
      });
    }
    await rm(dataDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  });

  try {
    await waitFor(async () => {
      try { return (await nodeFetch(baseURL)).ok; } catch { return false; }
    }, 'Go server startup', 20000);

    const html = await (await nodeFetch(baseURL)).text();
    const dom = createJSDOM(html, baseURL);
    installBrowserGlobals(dom, baseURL);
    rpcModule = await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'rpc.js')).href}`);
    await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'app.js')).href}?e2e=${Date.now()}`);

    await waitFor(() => document.getElementById('conn-status')?.textContent === 'Connected', 'WebSocket connection');

    // Step 1: Set temperature to 0.7.
    await rpcModule.rpc('settings.set', { temperature: 0.7 });
    const afterSet = await rpcModule.rpc('settings.get');
    assert.equal(afterSet.settings.temperature, 0.7, 'temperature must be 0.7 after setting');

    // Step 2: Try to clear temperature by sending null.
    // With the bug, null is treated as "don't change" because *float64 + omitempty
    // makes null indistinguishable from absent, and handleSettingsSet skips nil values.
    await rpcModule.rpc('settings.set', { temperature: null });

    // Step 3: After the fix, temperature is cleared (null/undefined in the
    // JSON response — *float64 with omitempty drops the field when nil).
    const afterClear = await rpcModule.rpc('settings.get');
    assert.equal(
      afterClear.settings.temperature, undefined,
      `BH-SETTINGS-01: temperature must be cleared after sending null (got ${afterClear.settings.temperature}). ` +
        `The settings.set contract must distinguish null (clear) from absent (don't change).`,
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

// findHydration inspects a parsed chat/completions request body for the
// synthetic runtime-hydration transcript. Returns the hydration messages
// (assistant toolCalls + matching tool results) when present, or null when
// the request has no hydration exchange.
//
// Shape in the OpenAI request:
//   - assistant message with tool_calls whose ids start with "hydrate-"
//   - followed by tool messages with tool_call_id matching those ids.
// The transcript is DYNAMIC: slots whose real tool reports nothing (no
// plugins, no todos, empty primary memory) are omitted entirely. In this
// harness (fresh data dir, seeded primary, embedded skills, no plugins)
// the visible slots are runtime_context, memory, skill_list.
function findHydration(messages) {
  for (let i = 0; i < messages.length; i++) {
    const m = messages[i];
    if (m.role !== 'assistant' || !Array.isArray(m.tool_calls) || m.tool_calls.length === 0) continue;
    const hydrateCalls = m.tool_calls.filter((c) => c.id?.startsWith('hydrate-'));
    if (hydrateCalls.length === 0) continue;
    const ids = new Set(hydrateCalls.map((c) => c.id));
    const results = [];
    for (let j = i + 1; j < messages.length; j++) {
      const t = messages[j];
      if (t.role !== 'tool') break;
      if (ids.has(t.tool_call_id)) results.push(t);
    }
    if (results.length > 0) return { assistant: m, results, calls: hydrateCalls };
  }
  return null;
}

// hydrationSlotNames returns the tool-call function names from a hydration
// exchange, in order. Used to assert the full 6-slot transcript is present.
function hydrationSlotNames(hydration) {
  return hydration.calls.map((c) => c.function?.name);
}

// HYDR-NEW-ROOM: A brand-new conversation's first turn must inject the
// runtime-hydration transcript (dynamic: only slots with real content) into
// the provider request. The transcript is ephemeral — never persisted — so
// it must appear in the request body but NOT in the persisted conversation
// messages.
test('HYDR-NEW-ROOM: first turn of a new conversation injects the hydration transcript', async (t) => {
  assert.ok(NativeWebSocket, 'Node WebSocket support is required for the E2E event stream');
  const llmPort = await freePort();
  const llm = fakeLLM(llmPort);
  await new Promise((resolveListen) => llm.server.listen(llmPort, '127.0.0.1', resolveListen));
  t.after(() => new Promise((r) => llm.server.close(r)));

  const port = await freePort();
  const dataDir = await mkdtemp(join(tmpdir(), 'nusashell-hydr-new-room-'));
  const baseURL = `http://127.0.0.1:${port}/`;
  const server = await buildAndStartServer(port, dataDir);
  let rpcModule;
  t.after(async () => {
    rpcModule?.closeWS();
    if (server.go.exitCode === null) {
      await new Promise((resolveStop) => {
        const timer = setTimeout(resolveStop, 2000);
        server.go.once('exit', () => { clearTimeout(timer); resolveStop(); });
        server.go.kill();
      });
    }
    await rm(dataDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  });

  try {
    await waitFor(async () => {
      try { return (await nodeFetch(baseURL)).ok; } catch { return false; }
    }, 'Go server startup', 20000);

    const html = await (await nodeFetch(baseURL)).text();
    const dom = createJSDOM(html, baseURL);
    installBrowserGlobals(dom, baseURL);
    rpcModule = await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'rpc.js')).href}`);
    await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'app.js')).href}?e2e=${Date.now()}`);

    await waitFor(() => document.getElementById('conn-status')?.textContent === 'Connected', 'WebSocket connection');

    // Save a provider pointing at the fake LLM.
    const saveRes = await rpcModule.rpc('ai.providers.save', {
      kind: 'chat', name: 'FakeLLM', base_url: `${llm.url}/v1`, api_key: 'test-key', enabled: true,
    });
    const providerID = saveRes.providers[0].id;
    await rpcModule.rpc('ai.providers.import-models', { id: providerID });

    // Seed one primary memory entry so the memory slot is non-empty.
    // Hydration reads from memory/primary.md (primary memory), so we write
    // directly to the file in the data dir.
    await mkdir(join(dataDir, 'memory'), { recursive: true });
    await writeFile(join(dataDir, 'memory', 'primary.md'), '---\nlast_updated: 2026-08-19T12:00:00Z\nversion: 1\n---\n\n- [frag_test] User prefers concise answers.\n');

    // Create a new conversation.
    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(
      () => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((n) => n.textContent === 'Untitled'),
      'new conversation through the UI',
    );
    const conversations = await rpcModule.rpc('agent.conversations.list');
    const convID = conversations.conversations[0].id;

    // Script: a single short reply so the turn finishes quickly.
    llm.setScripts([[{ text: 'Hello from the assistant.' }]]);

    // Start the first turn.
    await startTurn(rpcModule.rpc, {
      conversation_id: convID, text: 'hi', model: 'tiny-model',
    });

    // Wait for the turn to finish.
    await waitFor(async () => {
      const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
      return c.conversation?.status === 'idle';
    }, 'first turn done', 15000);

    // Verify the streaming request contains the hydration transcript.
    const streamingReqs = llm.requests().filter((r) => r.stream);
    assert.ok(streamingReqs.length > 0, 'at least one streaming request must have been sent');
    const lastStream = streamingReqs[streamingReqs.length - 1];
    const hydration = findHydration(lastStream.body.messages);
    assert.ok(hydration, 'HYDR-NEW-ROOM: hydration transcript must be present in the first turn request');

    // Dynamic transcript: this harness has no plugins and no todos, so the
    // mcp_list / tool_list / todo_list slots are hidden. Seeded primary
    // memory and the embedded skill library keep memory + skill alive.
    const slots = hydrationSlotNames(hydration);
    assert.deepEqual(
      slots,
      ['runtime_context', 'memory', 'skill'],
      `HYDR-NEW-ROOM: hydration slots must be the dynamic transcript in order, got ${JSON.stringify(slots)}`,
    );

    // The memory slot must contain the seeded entry.
    const memCall = hydration.calls.find((c) => c.function?.name === 'memory');
    const memResult = hydration.results.find((r) => r.tool_call_id === memCall?.id);
    assert.ok(memResult, 'HYDR-NEW-ROOM: memory tool result must exist');
    assert.ok(
      memResult.content.includes('User prefers concise answers.'),
      `HYDR-NEW-ROOM: memory slot must contain the seeded entry, got: ${memResult.content}`,
    );

    // The hydration transcript is persisted to the conversation store for
    // prompt-cache stability but must be hidden from the UI (filtered out
    // of the conversations.get response).
    const gotten = await rpcModule.rpc('agent.conversations.get', { id: convID });
    const visibleHydration = findHydration(
      (gotten.messages || []).map((m) => ({
        role: m.role,
        tool_calls: m.tool_calls,
        tool_call_id: m.tool_call_id,
      })),
    );
    assert.equal(
      visibleHydration, null,
      'HYDR-NEW-ROOM: hydration transcript must be hidden from the UI (not present in conversations.get response)',
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

// HYDR-POST-COMPACTION: After compaction runs, the next turn must re-inject
// the hydration transcript. Compaction replaces durable history with a
// summary; the model loses all runtime facts (date, workspace, memory,
// skills, MCP catalog, tool catalog) unless hydration is re-injected.
// This test seeds enough history to trigger compaction, then verifies the
// post-compaction streaming request contains a fresh hydration transcript.
test('HYDR-POST-COMPACTION: turn after compaction re-injects the hydration transcript', async (t) => {
  assert.ok(NativeWebSocket, 'Node WebSocket support is required for the E2E event stream');
  const llmPort = await freePort();
  const llm = fakeLLM(llmPort);
  await new Promise((resolveListen) => llm.server.listen(llmPort, '127.0.0.1', resolveListen));
  t.after(() => new Promise((r) => llm.server.close(r)));

  const port = await freePort();
  const dataDir = await mkdtemp(join(tmpdir(), 'nusashell-hydr-post-compaction-'));
  const baseURL = `http://127.0.0.1:${port}/`;
  const server = await buildAndStartServer(port, dataDir);
  let rpcModule;
  t.after(async () => {
    rpcModule?.closeWS();
    if (server.go.exitCode === null) {
      await new Promise((resolveStop) => {
        const timer = setTimeout(resolveStop, 2000);
        server.go.once('exit', () => { clearTimeout(timer); resolveStop(); });
        server.go.kill();
      });
    }
    await rm(dataDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  });

  try {
    await waitFor(async () => {
      try { return (await nodeFetch(baseURL)).ok; } catch { return false; }
    }, 'Go server startup', 20000);

    const html = await (await nodeFetch(baseURL)).text();
    const dom = createJSDOM(html, baseURL);
    installBrowserGlobals(dom, baseURL);
    rpcModule = await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'rpc.js')).href}`);
    await import(`${pathToFileURL(join(repo, 'frontend', 'js', 'app.js')).href}?e2e=${Date.now()}`);

    await waitFor(() => document.getElementById('conn-status')?.textContent === 'Connected', 'WebSocket connection');

    // Save a provider pointing at the fake LLM.
    const saveRes = await rpcModule.rpc('ai.providers.save', {
      kind: 'chat', name: 'FakeLLM', base_url: `${llm.url}/v1`, api_key: 'test-key', enabled: true,
    });
    const providerID = saveRes.providers[0].id;
    await rpcModule.rpc('ai.providers.import-models', { id: providerID });

    // Seed one primary memory entry so the memory slot is non-empty.
    // Hydration reads from memory/primary.md (primary memory), so we write
    // directly to the file in the data dir.
    await mkdir(join(dataDir, 'memory'), { recursive: true });
    await writeFile(join(dataDir, 'memory', 'primary.md'), '---\nlast_updated: 2026-08-19T12:00:00Z\nversion: 1\n---\n\n- [frag_test] User is testing compaction hydration.\n');

    // Disable compaction while seeding history.
    await rpcModule.rpc('settings.set', { compaction_enabled: false });

    // Create a conversation.
    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(
      () => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((n) => n.textContent === 'Untitled'),
      'new conversation through the UI',
    );
    const conversations = await rpcModule.rpc('agent.conversations.list');
    const convID = conversations.conversations[0].id;

    // Seed 4 turns with large messages (~4000 tokens total) to exceed the
    // compaction trigger once it is enabled.
    const bigMsg = 'x'.repeat(2000);
    for (let i = 0; i < 4; i++) {
      llm.setScripts([[{ text: bigMsg }]]);
      await startTurn(rpcModule.rpc, {
        conversation_id: convID, text: bigMsg, model: 'tiny-model',
      });
      await waitFor(async () => {
        const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
        return c.conversation?.status === 'idle';
      }, `seed turn ${i + 1} done`, 15000);
    }

    // Clear captured requests so we only inspect the post-compaction turn.
    llm.requests().length = 0;

    // Enable compaction with a small context window (trigger = 800 tokens).
    // The seeded history (~4000 tokens) is well above the trigger.
    await rpcModule.rpc('settings.set', { max_input_tokens: 1000, compaction_enabled: true });

    // Set the compaction summary response (non-streaming).
    llm.setComplete(LONG_COMPACTION_SUMMARY);
    // Set the streaming reply for the post-compaction turn.
    llm.setScripts([[{ text: 'Turn after compaction with hydration.' }]]);

    // Trigger compaction + the next turn with a new user message.
    await startTurn(rpcModule.rpc, {
      conversation_id: convID, text: 'continue', model: 'tiny-model',
    });

    // Wait for the compaction marker to appear in the UI.
    await waitFor(
      () => [...document.querySelectorAll('#agent-thread .agent-compaction-marker')].some(
        (n) => n.textContent.includes('Compacted'),
      ),
      'compaction marker in the UI',
      15000,
    );

    // Wait for the post-compaction turn to finish.
    await waitFor(async () => {
      const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
      return c.conversation?.status === 'idle';
    }, 'post-compaction turn done', 15000);

    // Verify the post-compaction streaming request contains a fresh
    // hydration transcript. The non-streaming compaction request must NOT
    // contain hydration (it summarizes durable history only).
    //
    // Filter out autolearn background-review requests: compaction triggers
    // a fire-and-forget learning review (subscribeCompactionReview) that
    // streams to the same fake LLM. The review agent sends its own system
    // prompt plus a synthetic transcript (tool call ids prefixed
    // "synthetic_"), and its request can land after the main turn's —
    // masking the post-compaction request we need to inspect.
    const allReqs = llm.requests();
    const nonStreamReqs = allReqs.filter((r) => !r.stream);
    const isAutolearnReview = (r) => {
      const sys = r.body.messages?.find((m) => m.role === 'system');
      if (typeof sys?.content === 'string' && sys.content.includes('background review agent')) return true;
      return (r.body.messages || []).some((m) =>
        Array.isArray(m.tool_calls) && m.tool_calls.some((c) => c.id?.startsWith('synthetic_')));
    };
    const streamReqs = allReqs.filter((r) => r.stream && !isAutolearnReview(r));

    // Non-streaming compaction request must not carry hydration.
    for (const ns of nonStreamReqs) {
      const nsHyd = findHydration(ns.body.messages);
      assert.equal(
        nsHyd, null,
        'HYDR-POST-COMPACTION: compaction summary request must not contain hydration transcript',
      );
    }

    // The streaming post-compaction request must carry hydration.
    assert.ok(streamReqs.length > 0, 'a streaming request must have been sent after compaction');
    const lastStream = streamReqs[streamReqs.length - 1];
    const hydration = findHydration(lastStream.body.messages);
    assert.ok(
      hydration,
      'HYDR-POST-COMPACTION: hydration transcript must be re-injected after compaction',
    );

    // Dynamic transcript (see HYDR-NEW-ROOM): mcp/tool/todo slots are
    // hidden in this harness; memory + skill survive.
    const slots = hydrationSlotNames(hydration);
    assert.deepEqual(
      slots,
      ['runtime_context', 'memory', 'skill'],
      `HYDR-POST-COMPACTION: hydration slots must be the dynamic transcript in order, got ${JSON.stringify(slots)}`,
    );

    // The memory slot must contain the seeded entry (proves the transcript
    // was rebuilt fresh from live stores, not reused from before compaction).
    const memCall = hydration.calls.find((c) => c.function?.name === 'memory');
    const memResult = hydration.results.find((r) => r.tool_call_id === memCall?.id);
    assert.ok(memResult, 'HYDR-POST-COMPACTION: memory tool result must exist');
    assert.ok(
      memResult.content.includes('User is testing compaction hydration.'),
      `HYDR-POST-COMPACTION: memory slot must contain the seeded entry, got: ${memResult.content}`,
    );

    // The hydration transcript is persisted to the conversation store for
    // prompt-cache stability but must be hidden from the UI (filtered out
    // of the conversations.get response).
    const gotten = await rpcModule.rpc('agent.conversations.get', { id: convID });
    const visibleHydration = findHydration(
      (gotten.messages || []).map((m) => ({
        role: m.role,
        tool_calls: m.tool_calls,
        tool_call_id: m.tool_call_id,
      })),
    );
    assert.equal(
      visibleHydration, null,
      'HYDR-POST-COMPACTION: hydration transcript must be hidden from the UI (not present in conversations.get response)',
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});
