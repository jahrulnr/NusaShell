import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { createServer as createHTTPServer } from 'node:http';
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

// fakeLLM starts a minimal OpenAI-compatible HTTP server that serves
// /v1/models and /v1/chat/completions (streaming + non-streaming).
// Non-streaming requests (used by compaction) return the configured
// completeText; streaming requests return the next script from the queue.
//
// Script step shape: { text, reasoning, toolCall: { id, name, arguments } }
// When sendDone is false, the stream closes without `data: [DONE]` to
// simulate an incomplete SSE stream (BH-AI-01).
function fakeLLM(port) {
  let completeText = 'compaction summary: user likes Go.';
  let scripts = [];
  let sendDone = true;
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
        if (!parsed.stream) {
          // Non-streaming: used by compaction summary requests.
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({
            choices: [{ message: { role: 'assistant', content: completeText }, finish_reason: 'stop' }],
            usage: { prompt_tokens: 10, completion_tokens: 5 },
          }));
          return;
        }
        // Streaming: pop next script, default to a short reply.
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
        res.write(`data: ${JSON.stringify({ choices: [{ delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 5 } })}\n\n`);
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
    url: `http://127.0.0.1:${port}`,
  };
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

    window.location.hash = '#settings';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('settings-max-tool-rounds').value = '2';
    document.getElementById('settings-save-btn').click();
    await waitFor(() => document.getElementById('settings-save-status')?.textContent === 'Saved on this device.', 'settings save through the UI');
    assert.equal((await rpcModule.rpc('settings.get')).settings.max_tool_rounds, 2);

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

    server.go.kill();
    await waitFor(
      () => !document.getElementById('agent-offline-state')?.hidden,
      'friendly agent offline state after the local backend stops',
    );
    assert.match(document.getElementById('agent-offline-state').textContent, /Sorry, it looks like your agent is offline\./);
    assert.equal(document.getElementById('agent-composer-stack').hidden, true);
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
    const dom = new JSDOM(html, { url: baseURL, pretendToBeVisual: true });
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
      await rpcModule.rpc('agent.turns.start', {
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
    llm.setComplete('SUMMARY: user explored compaction e2e test.');
    llm.setScripts([[{ text: 'Turn after compaction.' }]]);

    // Trigger compaction with a new user message.
    await rpcModule.rpc('agent.turns.start', {
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

    // Wait for the turn to finish.
    await waitFor(async () => {
      const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
      return c.conversation?.status === 'idle';
    }, 'post-compaction turn done', 15000);

    // Verify the conversation has the compaction marker in persisted state.
    const gotten = await rpcModule.rpc('agent.conversations.get', { id: convID });
    const systemMsgs = gotten.messages?.filter((m) => m.role === 'system') || [];
    assert.ok(systemMsgs.length > 0, 'at least one system (compaction) message exists');
    assert.ok(
      systemMsgs.some((m) => m.content.toLowerCase().includes('compacted')),
      `compaction marker not found in system messages: ${JSON.stringify(systemMsgs.map((m) => m.content.slice(0, 80)))}`,
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

// BH-AI-01: When a provider SSE stream accumulates tool-call deltas but
// closes without the terminator ([DONE] / message_stop / response.completed),
// isIncompleteEmptyStream checks result.ToolCalls — which is still empty
// because tool calls are in the local accumulator map (toolAcc/toolByIndex)
// and only moved to result.ToolCalls AFTER the completed check. The check
// misclassifies the stream as "empty" and falls back to non-streaming,
// silently discarding the accumulated tool calls.
//
// This test demonstrates the bug: a stream with a tool-call delta and no
// [DONE] silently produces the non-streaming fallback text instead of
// either preserving the tool call or surfacing the error.
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
    const dom = new JSDOM(html, { url: baseURL, pretendToBeVisual: true });
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

    // Script: stream a tool-call delta for the built-in "skill_list" tool,
    // then close the connection WITHOUT sending [DONE].
    llm.setSendDone(false);
    llm.setScripts([[
      { toolCall: { id: 'call_bh01', name: 'skill_list', arguments: '{}' } },
    ]]);
    // Non-streaming fallback returns this text — if the bug is present,
    // the turn will silently produce this instead of the tool call.
    llm.setComplete('FALLBACK_TEXT_FROM_NON_STREAMING');

    await rpcModule.rpc('agent.turns.start', {
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

    // After the fix: the tool calls are finalized into result.ToolCalls
    // BEFORE the incomplete-stream check, so isIncompleteEmptyStream sees
    // them and does NOT trigger the non-streaming fallback. The adapter
    // returns incompleteSSEError, the retry loop exhausts (fake LLM always
    // closes without [DONE]), and the turn fails with status=error.
    // The fallback text must NOT appear — the tool calls must not be
    // silently discarded.
    assert.ok(lastAssistant, 'an assistant message must exist');
    assert.notEqual(
      lastAssistant.content, 'FALLBACK_TEXT_FROM_NON_STREAMING',
      'BH-AI-01: must not silently fall back to non-streaming when tool calls were accumulated',
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
    const dom = new JSDOM(html, { url: baseURL, pretendToBeVisual: true });
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
