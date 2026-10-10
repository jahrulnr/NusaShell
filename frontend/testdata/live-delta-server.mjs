// Fake NusaShell backend for observing live agent rounds in a real browser.
//
//   node frontend/testdata/live-delta-server.mjs [--port 8787] [--rounds 10] [--speed 15]
//
// Serves frontend/ statically, answers the handful of RPC calls the UI makes
// at boot, exposes lifecycle notifications over WebSocket /ws, and serves
// replayable per-round agent deltas over GET /stream. The synthetic turn
// (reasoning → tool output → message text) lets a developer inspect the live
// conversation without a Go backend or real provider. Open the page, send a
// message, scroll up mid-stream, and confirm earlier rounds remain mounted.
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

// One second of 8 kHz mono 8-bit silence: fixture sound requests resolve and
// decode without the console noise of 404s or decode errors, and nothing plays.
const SILENT_WAV = (() => {
  const wav = Buffer.alloc(44 + 8000, 0x80);
  wav.write('RIFF', 0);
  wav.writeUInt32LE(36 + 8000, 4);
  wav.write('WAVE', 8);
  wav.write('fmt ', 12);
  wav.writeUInt32LE(16, 16);
  wav.writeUInt16LE(1, 20);
  wav.writeUInt16LE(1, 22);
  wav.writeUInt32LE(8000, 24);
  wav.writeUInt32LE(8000, 28);
  wav.writeUInt16LE(1, 32);
  wav.writeUInt16LE(8, 34);
  wav.write('data', 36);
  wav.writeUInt32LE(8000, 40);
  return wav;
})();

export function startLiveDeltaServer({
  port = 8787,
  rounds = 10,
  speedMs = 15,
  chunkSize = 24,
  // Inter-round pause in ms: a number, or a "min-max" range that is rolled
  // randomly per round (e.g. "1000-5000" reads like a ~10 tok/s agent).
  roundDelay = 120,
  frontendDir = REPO_FRONTEND,
  // 'default' streams the single-tool demo turn. 'ordering' opt-in streams a
  // three-round turn with a subagent card, a delayed completion after a steer,
  // and an ACP run stream whose completion is sent with a sequence gap.
  scenario = 'default',
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
  const roundStreams = new Map();
  let activeStream = null; // { cancelled: boolean }
  let pendingSteer = null; // { id, conversationId, text }
  // Synthetic persisted transcript (mirrors what the real backend saves at
  // turn end) so refreshActiveConversation after turn.done renders the
  // finished turn instead of an empty thread.
  const transcript = [];
  const CONV_ID = 'conv_live_delta';
  const MODEL_ID = 'fake-live-model';

  // ---- ACP run stream (ordering scenario only) -------------------------
  // Mirrors the conversation-scoped /stream/acp contract: each connection gets
  // a snapshot first, then one monotonic seq per frame. A frame published with
  // gap=true skips one seq, which the browser must treat as a missed event.
  const acp = { seq: 0, runs: new Map(), clients: new Set() };

  function writeACPFrame(res, frame) {
    res.write(`id: ${frame.seq}\nevent: acp.run\ndata: ${JSON.stringify(frame)}\n\n`);
  }

  function publishACPRun(type, run, { gap = false } = {}) {
    acp.seq += gap ? 2 : 1;
    acp.runs.set(run.id, { ...run });
    const frame = { type, seq: acp.seq, run: { ...run } };
    for (const res of acp.clients) writeACPFrame(res, frame);
  }

  function serveACPStream(req, res) {
    res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no' });
    res.write(': connected\n\n');
    writeACPFrame(res, { type: 'acp.run.snapshot', seq: acp.seq, runs: [...acp.runs.values()] });
    acp.clients.add(res);
    res.on('close', () => acp.clients.delete(res));
  }

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

  function beginRoundStream(runID, messageID, round) {
    const key = `${runID}|${messageID}`;
    const stream = { runID, messageID, round, seq: 0, frames: [], clients: new Set(), done: null };
    roundStreams.set(key, stream);
    return stream;
  }

  function writeSSE(res, event, payload, id = '') {
    if (id) res.write(`id: ${id}\n`);
    res.write(`event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`);
  }

  function publishRoundDelta(runID, messageID, kind, text, extra = {}) {
    const stream = roundStreams.get(`${runID}|${messageID}`);
    if (!stream || stream.done) return;
    const frame = { seq: ++stream.seq, kind, ...extra };
    if (text) frame.text = text;
    stream.frames.push(frame);
    for (const res of stream.clients) writeSSE(res, 'round.delta', frame, String(frame.seq));
  }

  function sealRoundStream(runID, messageID, { state = 'done', next = null, usage = null } = {}) {
    const stream = roundStreams.get(`${runID}|${messageID}`);
    if (!stream || stream.done) return;
    stream.done = {
      state, run_id: runID, message_id: messageID, round: stream.round,
      last_seq: stream.seq, usage, ...(next ? { next } : {}),
    };
    for (const res of stream.clients) {
      writeSSE(res, 'round.done', stream.done);
      res.end();
    }
    stream.clients.clear();
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
    let currentMessageID = '';
    try {
      for (let round = 1; round <= rounds; round++) {
        if (stream.cancelled) return runId;
        currentRound = round;
        const messageId = `msg_r${round}`;
        currentMessageID = messageId;
        beginRoundStream(runId, messageId, round);
        broadcast('agent.turn.started', { run_id: runId, conversation_id: conversationId, round, message_id: messageId });
        if (round > 1) await sleep(nextRoundDelay());
        if (pendingSteer && pendingSteer.conversationId === conversationId) {
          broadcast('agent.steer.applied', { conversation_id: conversationId, steer_id: pendingSteer.id, text: pendingSteer.text });
          pendingSteer = null;
        }

        const thought = `Round ${round}: I will demonstrate that every round stays mounted while deltas stream.`;
        for (let i = 0; i < thought.length; i += chunkSize) {
          if (stream.cancelled) return runId;
          publishRoundDelta(runId, messageId, 'reasoning', thought.slice(i, i + chunkSize));
          await sleep(speedMs);
        }

        const toolCallId = `tool_${round}_${randomUUID().slice(0, 6)}`;
        const toolArgs = { command: 'echo "live delta smoke"' };
        broadcast('agent.tool.started', { run_id: runId, conversation_id: conversationId, tool_call_id: toolCallId, name: 'exec', args: toolArgs });
        publishRoundDelta(runId, messageId, 'tool', '', {
          tool_call_id: toolCallId,
          name: 'exec',
          args: toolArgs,
          presentation: { variant: 'terminal', action: 'Running command', request: 'exec(...)', result: { format: 'terminal' } },
        });
        const toolOut = [...Array(4).keys()].map((n) => `line ${n + 1}: streaming output lands in the tool terminal`).join('\n');
        for (let i = 0; i < toolOut.length; i += chunkSize) {
          if (stream.cancelled) return runId;
          publishRoundDelta(runId, messageId, 'tool', toolOut.slice(i, i + chunkSize), { tool_call_id: toolCallId });
          await sleep(speedMs);
        }
        broadcast('agent.tool.completed', { run_id: runId, conversation_id: conversationId, tool_call_id: toolCallId, name: 'exec', status: 'ok', output: `${toolOut}\nexit_code: 0` });
        const roundSteps = [{ type: 'reasoning', content: thought }, { type: 'tool_calls', tool_calls: [{ id: toolCallId, name: 'exec', args: toolArgs, status: 'ok', output: `${toolOut}\nexit_code: 0` }] }];

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
          publishRoundDelta(runId, messageId, 'text', body.slice(i, i + chunkSize));
          await sleep(speedMs);
        }
        roundSteps.push({ type: 'text', content: body });
        transcript.push({ role: 'assistant', id: messageId, model: MODEL_ID, steps: roundSteps, created_at: new Date().toISOString() });
        const next = round < rounds ? { run_id: runId, message_id: `msg_r${round + 1}`, round: round + 1 } : null;
        sealRoundStream(runId, messageId, { next, usage: { input_tokens: 1024, output_tokens: 4096 } });
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
        if (currentMessageID) {
          sealRoundStream(runId, currentMessageID, { state: 'interrupted', usage: { input_tokens: 0, output_tokens: 0 } });
        }
        broadcast('agent.turn.done', { run_id: runId, conversation_id: conversationId, message_id: currentMessageID, model: MODEL_ID, usage: { input_tokens: 0, output_tokens: 0 } });
      }
    }
    return runId;
  }

  // streamOrderingTurn: round 1 starts a subagent (tool only, card stays in
  // round 1). A steer queued during round 1 applies at the round 2 boundary.
  // The subagent completion arrives during round 2, after that steer, and the
  // ACP completion frame is sent with a sequence gap.
  const ACP_RUN_ID = 'acprun_fxrun1';
  const SUBAGENT_CALL_ID = 'call_fx_subagent';
  const SUBAGENT_ARGS = { prompt: 'Audit the frontend transcript ordering', agent_id: 'acp_dev' };
  const SUBAGENT_OUTPUT = `Subagent run ${ACP_RUN_ID} completed. Full result delivered in the subagent_result tool call.`;

  async function streamChunks(runId, messageId, kind, text) {
    for (let i = 0; i < text.length; i += chunkSize) {
      publishRoundDelta(runId, messageId, kind, text.slice(i, i + chunkSize));
      await sleep(speedMs);
    }
  }

  async function streamOrderingTurn(conversationId, runId = `run_${randomUUID().slice(0, 8)}`) {
    const stream = { cancelled: false };
    activeStream = stream;
    try {
      for (let round = 1; round <= 3; round++) {
        if (stream.cancelled) return runId;
        const messageId = `msg_r${round}`;
        const steps = [];
        beginRoundStream(runId, messageId, round);
        broadcast('agent.turn.started', { run_id: runId, conversation_id: conversationId, round, message_id: messageId });
        if (round > 1) await sleep(nextRoundDelay());
        if (pendingSteer && pendingSteer.conversationId === conversationId) {
          broadcast('agent.steer.applied', { conversation_id: conversationId, steer_id: pendingSteer.id, text: pendingSteer.text });
          transcript.push({ role: 'user', content: pendingSteer.text, steer: true, created_at: new Date().toISOString() });
          pendingSteer = null;
        }

        const thought = `Round ${round}: ordering scenario step ${round}.`;
        await streamChunks(runId, messageId, 'reasoning', thought);
        steps.push({ type: 'reasoning', content: thought });

        if (round === 1) {
          broadcast('agent.tool.started', { run_id: runId, conversation_id: conversationId, tool_call_id: SUBAGENT_CALL_ID, name: 'subagent', args: SUBAGENT_ARGS });
          publishRoundDelta(runId, messageId, 'tool', '', { tool_call_id: SUBAGENT_CALL_ID, name: 'subagent', args: SUBAGENT_ARGS });
          publishACPRun('acp.run.started', { id: ACP_RUN_ID, conversation_id: CONV_ID, agent_id: 'acp_dev', status: 'running', prompt: SUBAGENT_ARGS.prompt, created_at: new Date().toISOString(), updated_at: new Date().toISOString() });
          steps.push({ type: 'tool_calls', tool_calls: [{ id: SUBAGENT_CALL_ID, name: 'subagent', args: SUBAGENT_ARGS, status: 'running', output: '' }] });
        } else {
          const toolCallId = `tool_${round}_${randomUUID().slice(0, 6)}`;
          const toolArgs = { command: `echo "ordering round ${round}"` };
          const toolOut = `ordering round ${round} exec output`;
          broadcast('agent.tool.started', { run_id: runId, conversation_id: conversationId, tool_call_id: toolCallId, name: 'exec', args: toolArgs });
          publishRoundDelta(runId, messageId, 'tool', '', { tool_call_id: toolCallId, name: 'exec', args: toolArgs });
          await streamChunks(runId, messageId, 'tool', toolOut);
          broadcast('agent.tool.completed', { run_id: runId, conversation_id: conversationId, tool_call_id: toolCallId, name: 'exec', status: 'ok', output: toolOut });
          steps.push({ type: 'tool_calls', tool_calls: [{ id: toolCallId, name: 'exec', args: toolArgs, status: 'ok', output: toolOut }] });
        }

        if (round === 2) {
          // Delayed completion for the round-1 subagent call, sent after the round-2 steer applied.
          broadcast('agent.tool.completed', { run_id: runId, conversation_id: conversationId, tool_call_id: SUBAGENT_CALL_ID, name: 'subagent', args: SUBAGENT_ARGS, status: 'ok', output: SUBAGENT_OUTPUT });
          const settled = transcript.find((entry) => entry.id === 'msg_r1');
          for (const step of settled?.steps || []) {
            for (const call of step.tool_calls || []) {
              if (call.id === SUBAGENT_CALL_ID) Object.assign(call, { status: 'ok', output: SUBAGENT_OUTPUT });
            }
          }
          publishACPRun('acp.run.done', { id: ACP_RUN_ID, conversation_id: CONV_ID, agent_id: 'acp_dev', status: 'completed', prompt: SUBAGENT_ARGS.prompt, created_at: new Date().toISOString(), updated_at: new Date().toISOString() }, { gap: true });
        }

        const body = `**Round ${round}** — ordering scenario text for round ${round}.`;
        await streamChunks(runId, messageId, 'text', body);
        steps.push({ type: 'text', content: body });
        transcript.push({ id: messageId, role: 'assistant', model: MODEL_ID, steps, created_at: new Date().toISOString() });
        const next = round < 3 ? { run_id: runId, message_id: `msg_r${round + 1}`, round: round + 1 } : null;
        sealRoundStream(runId, messageId, { next, usage: { input_tokens: 512, output_tokens: 256 } });
      }
      broadcast('agent.turn.done', { run_id: runId, conversation_id: conversationId, model: MODEL_ID, usage: { input_tokens: 512, output_tokens: 256 } });
    } finally {
      if (activeStream === stream) activeStream = null;
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
      if (scenario === 'ordering') void streamOrderingTurn(conversationId, runId);
      else void streamTurn(conversationId, runId);
      return { run_id: runId };
    },
    'ai.models.list': () => ({ models: [{ id: MODEL_ID, name: 'Fake Live Model', provider: 'fake', context: 128000 }] }),
    'settings.get': () => ({ settings: {} }),
    'acp.runs.list': (payload) => ({ runs: [...acp.runs.values()].filter((run) => !payload?.conversation_id || run.conversation_id === payload.conversation_id) }),
    'acp.runs.get': (payload) => {
      const run = acp.runs.get(payload?.id);
      if (!run) throw new Error(`ACP run ${payload?.id} not found`);
      return { run };
    },
    'agent.tools.contracts': () => ({ version: 1, tools: [] }),
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

  function serveRoundStream(req, res, url) {
    const runID = url.searchParams.get('run_id') || '';
    const messageID = url.searchParams.get('message_id') || '';
    const after = Number(url.searchParams.get('after') || 0);
    if (!runID || !messageID || !Number.isSafeInteger(after) || after < 0) {
      res.writeHead(400, { 'Content-Type': 'text/plain' });
      res.end('invalid round stream request');
      return;
    }
    const stream = roundStreams.get(`${runID}|${messageID}`);
    if (!stream) {
      res.writeHead(404, { 'Content-Type': 'text/plain' });
      res.end('round stream not found');
      return;
    }
    res.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      'X-Accel-Buffering': 'no',
    });
    res.flushHeaders?.();
    res.write(': connected\n\n');
    for (const frame of stream.frames) {
      if (frame.seq > after) writeSSE(res, 'round.delta', frame, String(frame.seq));
    }
    if (stream.done) {
      writeSSE(res, 'round.done', stream.done);
      res.end();
      return;
    }
    stream.clients.add(res);
    res.on('close', () => stream.clients.delete(res));
  }

  // ---- HTTP ---------------------------------------------------------------

  const server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://localhost');
    if (url.pathname === '/stream') {
      serveRoundStream(req, res, url);
      return;
    }
    if (url.pathname === '/stream/acp' && scenario === 'ordering') {
      serveACPStream(req, res);
      return;
    }
    if (url.pathname.startsWith('/sounds/') && url.pathname.endsWith('.wav')) {
      res.writeHead(200, { 'Content-Type': 'audio/wav', 'Cache-Control': 'no-store' });
      res.end(SILENT_WAV);
      return;
    }
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
  const scenario = flag('scenario', 'default');
  const { port: actualPort } = await startLiveDeltaServer({ port, rounds, speedMs, roundDelay, scenario, log: (line) => console.log(line) });
  console.log(`live-delta-server ready → http://127.0.0.1:${actualPort}  (scenario=${scenario}, rounds=${rounds}, speed=${speedMs}ms/chunk, roundDelay=${roundDelay}ms)`);
  console.log('send a message in the UI to start the fake multi-round turn; Ctrl+C to stop');
}
