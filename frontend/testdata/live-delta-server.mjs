// Fake NusaShell backend for observing the live-delta DOM in a real browser.
//
//   node frontend/testdata/live-delta-server.mjs [--port 8787] [--rounds 10] [--speed 15]
//
// Serves frontend/ statically, answers the handful of RPC calls the UI makes
// at boot, exposes a minimal WebSocket /ws endpoint, and — once the composer
// sends a turn — streams a synthetic multi-round agent turn
// (reasoning deltas → tool call with streamed output → message deltas,
// repeated for --rounds rounds) so the conversation thread can be watched
// live without a Go backend or a real provider. Built to prove that the live
// thread keeps EVERY round mounted (no "earlier rounds trimmed" stub) while
// staying smooth: open the page, send a message, scroll up mid-stream and
// confirm the earlier rounds are still there.
//
// Zero dependencies: the WebSocket framing (RFC 6455) is implemented inline.

import { createServer } from 'node:http';
import { createHash, randomUUID } from 'node:crypto';
import { readFile, stat } from 'node:fs/promises';
import { extname, join, normalize, resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const REPO_FRONTEND = resolve(dirname(fileURLToPath(import.meta.url)), '..');

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.woff2': 'font/woff2',
  '.woff': 'font/woff',
  '.map': 'application/json; charset=utf-8',
  '.webmanifest': 'application/manifest+json; charset=utf-8',
  '.ico': 'image/x-icon',
};

const WS_GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11';

export function startLiveDeltaServer({
  port = 8787,
  rounds = 10,
  speedMs = 15,
  chunkSize = 24,
  // Inter-round pause in ms: a number, or a "min-max" range that is rolled
  // randomly per round (e.g. "1000-5000" reads like a ~10 tok/s agent).
  roundDelay = 120,
  frontendDir = REPO_FRONTEND,
  log = () => {},
} = {}) {
  const parseDelay = (spec) => {
    if (typeof spec === 'number') return () => spec;
    const [min, max] = String(spec).split('-').map(Number);
    if (!max || max < min) return () => min || 120;
    return () => min + Math.floor(Math.random() * (max - min + 1));
  };
  const nextRoundDelay = parseDelay(roundDelay);
  const clients = new Set();
  let activeStream = null; // { cancelled: boolean }
  let pendingSteer = null; // { id, conversationId, text }
  // Synthetic persisted transcript (mirrors what the real backend saves at
  // turn end) so refreshActiveConversation after turn.done renders the
  // finished turn instead of an empty thread.
  const transcript = [];
  const CONV_ID = 'conv_live_delta';
  const MODEL_ID = 'fake-live-model';

  // ---- WebSocket plumbing -------------------------------------------------

  function wsFrame(payload, opcode = 0x1) {
    const data = Buffer.from(payload, 'utf8');
    const length = data.length;
    let header;
    if (length < 126) {
      header = Buffer.from([0x80 | opcode, length]);
    } else if (length < 65536) {
      header = Buffer.alloc(4);
      header[0] = 0x80 | opcode;
      header[1] = 126;
      header.writeUInt16BE(length, 2);
    } else {
      header = Buffer.alloc(10);
      header[0] = 0x80 | opcode;
      header[1] = 127;
      header.writeBigUInt64BE(BigInt(length), 2);
    }
    return Buffer.concat([header, data]);
  }

  function broadcast(type, payload) {
    const frame = wsFrame(JSON.stringify({ type, payload }));
    for (const socket of clients) {
      if (socket.writable) socket.write(frame);
    }
    log(`→ ${type}`);
  }

  // Parses masked client frames far enough to read text frames and answer
  // close/ping; enough for a test/dev server.
  function handleClientData(socket, state, chunk) {
    state.buffer = state.buffer ? Buffer.concat([state.buffer, chunk]) : chunk;
    while (true) {
      const buf = state.buffer;
      if (buf.length < 2) return;
      const opcode = buf[0] & 0x0f;
      const masked = (buf[1] & 0x80) !== 0;
      let length = buf[1] & 0x7f;
      let offset = 2;
      if (length === 126) {
        if (buf.length < 4) return;
        length = buf.readUInt16BE(2);
        offset = 4;
      } else if (length === 127) {
        if (buf.length < 10) return;
        const big = buf.readBigUInt64BE(2);
        if (big > 32n * 1024n * 1024n) { socket.destroy(); return; }
        length = Number(big);
        offset = 10;
      }
      const maskLen = masked ? 4 : 0;
      if (buf.length < offset + maskLen + length) return;
      const mask = masked ? buf.subarray(offset, offset + 4) : null;
      let body = buf.subarray(offset + maskLen, offset + maskLen + length);
      if (mask) {
        body = Buffer.from(body);
        for (let i = 0; i < body.length; i++) body[i] ^= mask[i % 4];
      }
      state.buffer = buf.subarray(offset + maskLen + length);
      if (opcode === 0x8) { socket.end(wsFrame('', 0x8)); return; }
      if (opcode === 0x9) { socket.write(wsFrame(body.toString('utf8'), 0xA)); continue; }
      if (opcode === 0x1) {
        try {
          const msg = JSON.parse(body.toString('utf8'));
          if (msg && msg.id !== undefined) {
            // WS RPC: answer through the same handlers as HTTP /rpc.
            Promise.resolve(rpcHandlers[msg.method]?.(msg.payload ?? {}) ?? {})
              .then((result) => {
                socket.write(wsFrame(JSON.stringify({ id: msg.id, ok: true, result })));  // envelope: contracts/rpc.go WSResponse
              })
              .catch((err) => {
                socket.write(wsFrame(JSON.stringify({ id: msg.id, ok: false, error: { code: 'fake_server', message: String(err?.message || err) } })));
              });
          }
        } catch { /* ignore malformed frames */ }
      }
    }
  }

  // ---- Fake turn stream ---------------------------------------------------

  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  const PARAGRAPHS = [
    'Live rounds stay fully mounted in the DOM — nothing is parked behind a stub while the stream is running.',
    'Off-screen rounds skip layout and paint through `content-visibility: auto`, which is why the thread stays smooth.',
    'Enhancement (mermaid, highlighting, zoom buttons) only touches the blocks this delta just rendered.',
    'Scroll up while the stream runs: every earlier round is still right here, exactly where it was.',
  ];

  async function streamTurn(conversationId, runId = `run_${randomUUID().slice(0, 8)}`) {
    const stream = { cancelled: false };
    activeStream = stream;
    const steps = [];
    let currentRound = 1;
    try {
    broadcast('agent.turn.started', { run_id: runId, conversation_id: conversationId, round: 1, message_id: 'msg_r1' });

    for (let round = 1; round <= rounds; round++) {
      if (stream.cancelled) return runId;
      currentRound = round;
      const messageId = `msg_r${round}`;
      if (round > 1) {
        const pause = nextRoundDelay();
        broadcast('agent.turn.started', { run_id: runId, conversation_id: conversationId, round, message_id: messageId });
        await sleep(pause);
      }
      if (pendingSteer && pendingSteer.conversationId === conversationId) {
        broadcast('agent.steer.applied', { conversation_id: conversationId, steer_id: pendingSteer.id, text: pendingSteer.text });
        pendingSteer = null;
      }

      // Reasoning stream
      const thought = `Round ${round}: I will demonstrate that every round stays mounted while deltas stream.`;
      for (let i = 0; i < thought.length; i += chunkSize) {
        if (stream.cancelled) return runId;
        broadcast('agent.reasoning.delta', { run_id: runId, conversation_id: conversationId, message_id: messageId, text: thought.slice(i, i + chunkSize) });
        await sleep(speedMs);
      }

      // Tool call with streamed output
      const toolCallId = `tool_${round}_${randomUUID().slice(0, 6)}`;
      broadcast('agent.tool.started', { run_id: runId, conversation_id: conversationId, tool_call_id: toolCallId, name: 'exec', args: { command: 'echo "live delta smoke"' } });
      const toolOut = [...Array(4).keys()].map((n) => `line ${n + 1}: streaming output lands in the tool terminal`).join('\n');
      for (let i = 0; i < toolOut.length; i += chunkSize) {
        if (stream.cancelled) return runId;
        broadcast('agent.tool.delta', { run_id: runId, conversation_id: conversationId, tool_call_id: toolCallId, text: toolOut.slice(i, i + chunkSize) });
        await sleep(speedMs);
      }
      broadcast('agent.tool.completed', { run_id: runId, conversation_id: conversationId, tool_call_id: toolCallId, name: 'exec', status: 'ok', output: `${toolOut}\nexit_code: 0` });
      const roundSteps = [{ type: 'reasoning', content: thought }, { type: 'tool_calls', tool_calls: [{ id: toolCallId, name: 'exec', args: { command: 'echo "live delta smoke"' }, status: 'ok', output: `${toolOut}\nexit_code: 0` }] }];

      // Message stream: two paragraphs + a code fence per round.
      const body = [
        `**Round ${round}** — ${PARAGRAPHS[round % PARAGRAPHS.length]}`,
        PARAGRAPHS[(round + 1) % PARAGRAPHS.length],
        '```js',
        `// round ${round}: fences close mid-stream, then lock`,
        `console.log("round ${round} rendered while streaming");`,
        '```',
      ].join('\n\n');
      for (let i = 0; i < body.length; i += chunkSize) {
        if (stream.cancelled) return runId;
        broadcast('agent.message.delta', { run_id: runId, conversation_id: conversationId, message_id: messageId, text: body.slice(i, i + chunkSize) });
        await sleep(speedMs);
      }
      roundSteps.push({ type: 'text', content: body });
      transcript.push({ role: 'assistant', id: messageId, model: MODEL_ID, steps: roundSteps, created_at: new Date().toISOString() });
    }

    broadcast('agent.turn.done', {
      run_id: runId,
      conversation_id: conversationId,
      model: MODEL_ID,
      usage: { input_tokens: 1024, output_tokens: 4096 },
    });
    } finally {
      if (activeStream === stream) activeStream = null;
      if (stream.cancelled) {
        // Terminal event on cancel — without it the frontend keeps the room
        // "running" and routes every later submit into steer forever.
        broadcast('agent.turn.done', { run_id: runId, conversation_id: conversationId, message_id: `msg_r${currentRound}`, model: MODEL_ID, usage: { input_tokens: 0, output_tokens: 0 } });
      }
    }
    return runId;
  }

  // ---- RPC handlers -------------------------------------------------------

  const conversation = () => ({
    id: CONV_ID,
    title: 'Live Delta Playground',
    model: MODEL_ID,
    workspace: '',
    updated_at: new Date().toISOString(),
  });

  const rpcHandlers = {
    'agent.conversations.list': () => ({ conversations: [conversation()] }),
    'agent.conversations.get': () => ({ conversation: conversation(), messages: [...transcript] }),
    'agent.conversations.create': () => ({ conversation: conversation() }),
    'agent.conversations.rename': () => ({ conversation: conversation() }),
    'agent.todos.get': () => ({ items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 }, brief: '' }),
    'agent.turns.active': () => ({}),
    'agent.turns.stop': () => { if (activeStream) activeStream.cancelled = true; return {}; },
    'agent.turns.steer': async (payload) => {
      if (!activeStream) throw new Error('no running turn to steer');
      const steerId = `steer_${randomUUID().slice(0, 8)}`;
      pendingSteer = { id: steerId, conversationId: payload?.conversation_id || CONV_ID, text: String(payload?.text || '') };
      broadcast('agent.steer.queued', { conversation_id: pendingSteer.conversationId, steer_id: steerId, text: pendingSteer.text });
      return { steer_id: steerId };
    },
    'agent.turns.start': async (payload) => {
      if (activeStream) throw new Error('conversation is busy');
      const conversationId = payload?.conversation_id || CONV_ID;
      transcript.push({ role: 'user', content: String(payload?.text || payload?.message || '(fake turn)'), created_at: new Date().toISOString() });
      // Fire-and-forget: the HTTP response returns immediately; events flow
      // over the WebSocket, exactly like the real backend. The response run_id
      // MUST be the same one the event stream uses — the composer registers
      // its run entry from the response.
      const runId = `run_${randomUUID().slice(0, 8)}`;
      void streamTurn(conversationId, runId);
      return { run_id: runId };
    },
    'ai.models.list': () => ({ models: [{ id: MODEL_ID, name: 'Fake Live Model', provider: 'fake', context: 128000 }] }),
    'settings.get': () => ({ settings: {} }),
    'acp.runs.list': () => ({ runs: [] }),
    'acp.agents.list': () => ({ agents: [] }),
    // Views other than the agent view also boot-RPC; empty-but-shaped results
    // keep their init paths happy so the whole UI mounts cleanly.
    'ai.providers.list': () => ({ providers: [] }),
    'app.info': () => ({ version: 'fake', goos: 'test', goarch: 'test' }),
    'learning.graph': () => ({ points: [], summary: {} }),
    'learning.log': () => ({ entries: [] }),
    'learning.search': () => ({ results: [] }),
    'memory.list': () => ({ entries: [] }),
    'skills.list': () => ({ skills: [] }),
  };

  // ---- HTTP ---------------------------------------------------------------

  const server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://localhost');
    if (url.pathname.startsWith('/rpc/')) {
      const method = decodeURIComponent(url.pathname.slice('/rpc/'.length)).replace(/\//g, '.');
      let body = '';
      for await (const chunk of req) body += chunk;
      let payload = {};
      try { payload = body ? JSON.parse(body).payload ?? {} : {}; } catch { /* empty body */ }
      log(`← rpc ${method}`);
      const handler = rpcHandlers[method];
      if (!handler) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ ok: true, result: {} }));
        return;
      }
      try {
        const result = await handler(payload);
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ ok: true, result }));
      } catch (err) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ ok: false, error: { code: 'fake_server', message: String(err?.message || err) } }));
      }
      return;
    }

    // Static frontend serving (with traversal protection).
    let pathname = url.pathname === '/' ? '/index.html' : decodeURIComponent(url.pathname);
    const filePath = normalize(join(frontendDir, pathname));
    if (!filePath.startsWith(resolve(frontendDir))) {
      res.writeHead(403); res.end('forbidden'); return;
    }
    try {
      const info = await stat(filePath);
      const target = info.isDirectory() ? join(filePath, 'index.html') : filePath;
      const data = await readFile(target);
      res.writeHead(200, { 'Content-Type': MIME[extname(target)] || 'application/octet-stream', 'Cache-Control': 'no-store' });
      res.end(data);
    } catch {
      res.writeHead(404, { 'Content-Type': 'text/plain' });
      res.end('not found');
    }
  });

  server.on('upgrade', (req, socket) => {
    const url = new URL(req.url, 'http://localhost');
    if (url.pathname !== '/ws') { socket.destroy(); return; }
    const key = req.headers['sec-websocket-key'];
    if (!key) { socket.destroy(); return; }
    const accept = createHash('sha1').update(key + WS_GUID).digest('base64');
    socket.write(
      'HTTP/1.1 101 Switching Protocols\r\n' +
      'Upgrade: websocket\r\n' +
      'Connection: Upgrade\r\n' +
      `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
    );
    socket.setNoDelay(true);
    const state = { buffer: Buffer.alloc(0) };
    clients.add(socket);
    log('ws client connected');
    socket.on('data', (chunk) => handleClientData(socket, state, chunk));
    const remove = () => clients.delete(socket);
    socket.on('close', remove);
    socket.on('error', remove);
  });

  return new Promise((resolveListen) => {
    server.listen(port, '127.0.0.1', () => resolveListen({
      server,
      port: server.address().port,
      broadcast,
      streamTurn,
      rpcHandlers,
      clients,
      // Fully tears down the server: closes the listener and destroys every
      // open connection (upgraded WebSockets keep the process alive — the
      // smoke test and Ctrl+C both need them gone).
      close: () => {
        for (const socket of clients) socket.destroy();
        clients.clear();
        server.closeAllConnections?.();
        return new Promise((done) => server.close(() => done()));
      },
    }));
  });
}

// ---- CLI entrypoint -------------------------------------------------------

const isMain = process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href;

if (isMain) {
  const flag = (name, fallback) => {
    const i = process.argv.indexOf(`--${name}`);
    return i >= 0 ? process.argv[i + 1] : fallback;
  };
  const port = Number(flag('port', 8787));
  const rounds = Number(flag('rounds', 10));
  const speedMs = Number(flag('speed', 15));
  const roundDelay = flag('round-delay', '120');
  const { port: actualPort } = await startLiveDeltaServer({ port, rounds, speedMs, roundDelay, log: (line) => console.log(line) });
  console.log(`live-delta-server ready → http://127.0.0.1:${actualPort}  (rounds=${rounds}, speed=${speedMs}ms/chunk, roundDelay=${roundDelay}ms)`);
  console.log('send a message in the UI to start the fake multi-round turn; Ctrl+C to stop');
}
