import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderConversation, renderEmptyThread, renderToolJob, renderToolCallCard, appendToolJobDelta, bindToolStop, STARTER_PROMPTS } from './js/views/agent/render.js';

function renderTranscript(messages) {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const thread = document.getElementById('thread');
    thread.append(renderConversation(messages));
    return thread;
  } finally {
    globalThis.document = previousDocument;
  }
}

// exec tool output persisted on the conversation is rendered verbatim when
// the thread is reloaded from the snapshot (no live run in memory), so users
// see the streamed output after a refresh / room switch.
test('exec tool output is rendered from the persisted conversation', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Run a long command', created_at: '2026-08-25T00:00:00Z' },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-25T00:00:01Z',
      steps: [
        { type: 'tool_calls', tool_calls: [
          { id: 'tool_1', name: 'exec', args: { command: 'sleep 5' }, status: 'ok',
            output: 'exit_code: 0\nduration_ms: 5000\n---\nPING reply 1\nPING reply 2\n' },
        ] },
      ],
    },
  ]);
  const terminal = thread.querySelector('.agent-tool-terminal');
  assert.ok(terminal, 'exec card rendered from snapshot');
  assert.equal(terminal.classList.contains('is-success'), true, 'card is success');
  const out = terminal.querySelector('.agent-tool-terminal-output');
  assert.match(out.textContent, /PING reply 1/);
  assert.match(out.textContent, /PING reply 2/);
  // The streaming-only Stop button must not survive the reload.
  assert.equal(terminal.querySelector('.agent-tool-stop').hidden, true);
});

// exec tool with interrupted status keeps the persisted partial output so
// users see the streamed lines after a reload, not just "interrupted by user".
test('exec tool with interrupted status renders persisted partial output', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Cancel a long command', created_at: '2026-08-25T00:00:00Z' },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-25T00:00:01Z',
      steps: [
        { type: 'tool_calls', tool_calls: [
          { id: 'tool_1', name: 'exec', args: { command: 'sleep 60' }, status: 'interrupted',
            output: 'error: exec cancelled: context canceled\npartial output:\nfirst chunk\nsecond chunk\n' },
        ] },
      ],
    },
  ]);
  const terminal = thread.querySelector('.agent-tool-terminal');
  assert.equal(terminal.classList.contains('is-error'), true, 'interrupted status flagged as error styling');
  const out = terminal.querySelector('.agent-tool-terminal-output');
  assert.match(out.textContent, /first chunk/);
  assert.match(out.textContent, /second chunk/);
});

test('renders one model and usage summary for all assistant rounds in a user turn', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Run the checks', created_at: '2026-08-13T18:00:00Z' },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-13T18:00:01Z',
      usage: { input_tokens: 100, output_tokens: 20 },
      steps: [
        { type: 'reasoning', content: 'I will inspect the workspace.' },
        { type: 'tool_calls', tool_calls: [
          { id: 'tool_1', name: 'memory_list', args: {}, status: 'ok', output: '[]' },
          { id: 'tool_2', name: 'memory_search', args: {}, status: 'ok', output: '[]' },
        ] },
      ],
    },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-13T18:00:02Z',
      usage: { input_tokens: 200, output_tokens: 40, cache_read: 8 },
      steps: [{ type: 'text', content: 'The checks are complete.' }],
    },
    { role: 'user', content: 'Do one more thing', created_at: '2026-08-13T18:01:00Z' },
    {
      role: 'assistant', model: 'luna', created_at: '2026-08-13T18:01:01Z',
      usage: { input_tokens: 50, output_tokens: 10 },
      steps: [{ type: 'tool_calls', tool_calls: [{ id: 'tool_3', name: 'skill', args: { op: 'list' }, status: 'ok', output: '[]' }] }],
    },
  ]);

  const assistantTurns = thread.querySelectorAll('.agent-message.assistant');
  assert.equal(assistantTurns.length, 2);
  assert.equal(assistantTurns[0].querySelectorAll('.agent-turn-meta').length, 1);
  assert.equal(assistantTurns[0].querySelectorAll('.agent-tool-terminal').length, 2);
  assert.match(assistantTurns[0].querySelector('.agent-turn-meta').textContent, /deepseek/);
  assert.match(assistantTurns[0].querySelector('.agent-turn-meta').textContent, /↑300 ↓60/);
  assert.match(assistantTurns[0].querySelector('.agent-turn-meta').textContent, /cache 8/);
  assert.match(assistantTurns[1].querySelector('.agent-turn-meta').textContent, /luna/);
});

test('usage badges render compact token units instead of raw counts', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Run checks', created_at: '2026-08-13T18:00:00Z' },
    {
      role: 'assistant', model: 'luna', created_at: '2026-08-13T18:00:01Z',
      usage: { input_tokens: 27085560, output_tokens: 137158, cache_read: 25944438 },
      steps: [{ type: 'text', content: 'done' }],
    },
  ]);
  const meta = thread.querySelector('.agent-turn-meta').textContent;
  assert.match(meta, /↑27\.09M ↓137\.16k/);
  assert.match(meta, /cache 25\.94M/);
  assert.doesNotMatch(meta, /27085560|137158|25944438/);
});

test('tool job summary includes elapsed span before chevron', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const job = renderToolJob({ id: 'c1', name: 'exec', args: { command: 'ls' }, status: 'running' });
    const summary = job.querySelector('summary');
    const classes = [...summary.children].map((node) => node.className);
    const elapsedIdx = classes.indexOf('agent-tool-elapsed');
    const chevronIdx = classes.indexOf('agent-tool-terminal-chevron');
    assert.ok(elapsedIdx >= 0, 'elapsed span present');
    assert.ok(chevronIdx >= 0, 'chevron span present');
    assert.ok(elapsedIdx < chevronIdx, 'elapsed sits left of chevron');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('empty thread renders starter chips that fill the composer', () => {
  const dom = new JSDOM(`
    <div id="agent-thread"></div>
    <div id="tool-job-strip"></div>
    <div id="agent-todo-strip"></div>
    <textarea id="composer-input"></textarea>
  `);
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    assert.ok(STARTER_PROMPTS.length >= 3);
    renderEmptyThread();
    const chips = [...document.querySelectorAll('[data-starter-prompt]')];
    assert.equal(chips.length, STARTER_PROMPTS.length);
    chips[0].click();
    assert.equal(document.getElementById('composer-input').value, STARTER_PROMPTS[0].prompt);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('generate_image renders a proof card instead of a tool terminal', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const running = renderToolCallCard({
      id: 'tc1', name: 'generate_image', args: { prompt: 'a red harbor boat' }, status: 'running',
    });
    assert.equal(running.classList.contains('agent-genimage-card'), true);
    assert.equal(running.classList.contains('is-running'), true);
    assert.match(running.textContent, /Developing|Emulsion|a red harbor boat/);

    const done = renderToolCallCard({
      id: 'tc1',
      name: 'generate_image',
      args: { prompt: 'a red harbor boat' },
      status: 'ok',
      output: '---\nstatus: completed\nprovider: openai\nmodel: gpt-image-1\nsize: 1024x1024\ncost_usd: 0.04\nfile_path: /tmp/gen-tc1.png\n---\nImage saved.',
      output_attachments: [{ type: 'image', name: 'gen-tc1.png', media_type: 'image/png', file_path: '/tmp/gen-tc1.png' }],
    });
    assert.equal(done.classList.contains('is-success'), true);
    const img = done.querySelector('img');
    assert.ok(img);
    assert.match(img.getAttribute('src'), /\/local-file\?path=/);
    assert.match(done.textContent, /gpt-image-1/);
    assert.match(done.textContent, /Download/);
    assert.equal(done.querySelectorAll('.agent-tool-terminal').length, 0);
    assert.ok(done.querySelector('.agent-genimage-open'));

    const failed = renderToolCallCard({
      id: 'tc1', name: 'generate_image', args: { prompt: 'a boat' }, status: 'fail',
      output: 'error: No image generation model is configured. Ask the user to pick an image model in Settings → Image generation.',
    });
    assert.equal(failed.classList.contains('is-error'), true);
    assert.match(failed.textContent, /Settings/);
    assert.equal(failed.querySelectorAll('img').length, 0);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('exec tool terminal renders a stop button only while running', () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const running = renderToolJob({ id: 't1', name: 'exec', args: { command: 'sleep 5' }, status: 'running' });
    assert.equal(running.classList.contains('is-running'), true);
    let stop = running.querySelector('.agent-tool-stop');
    assert.ok(stop, 'streaming exec card has a stop button');
    assert.equal(stop.hidden, false, 'stop button visible while running');

    const done = renderToolJob({ id: 't2', name: 'exec', args: { command: 'ls' }, status: 'ok', output: 'ok\n' });
    assert.equal(done.classList.contains('is-success'), true);
    assert.equal(done.querySelector('.agent-tool-stop').hidden, true, 'stop hidden after done');

    // Non-streaming tools never show a stop button.
    const other = renderToolJob({ id: 't3', name: 'grep', args: {}, status: 'running' });
    assert.equal(other.querySelector('.agent-tool-stop'), null);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('appendToolJobDelta accumulates streamed output and clears placeholder', () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const job = renderToolJob({ id: 't1', name: 'exec', args: { command: 'ping' }, status: 'running' });
    const output = job.querySelector('.agent-tool-terminal-output');
    // Streaming exec starts with the "waiting" placeholder.
    assert.match(output.textContent, /waiting/);
    appendToolJobDelta(job, 'PING 8.8.8.8 (8.8.8.8)\n');
    assert.equal(output.textContent, 'PING 8.8.8.8 (8.8.8.8)\n');
    appendToolJobDelta(job, '64 bytes from 8.8.8.8\n');
    assert.equal(output.textContent, 'PING 8.8.8.8 (8.8.8.8)\n64 bytes from 8.8.8.8\n');
    // Placeholder must not reappear.
    assert.doesNotMatch(output.textContent, /waiting/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('bindToolStop calls agent.turns.stop with the run id until satisfied', async () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  let stopped = 0;
  const calls = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, opts) => {
    calls.push({ url, body: opts?.body });
    stopped++;
    if (stopped === 1) {
      throw new Error('transport failed');
    }
    return { ok: true, json: async () => ({ result: { ok: true } }) };
  };
  try {
    const job = renderToolJob({ id: 't1', name: 'exec', args: { command: 'sleep 5' }, status: 'running' });
    bindToolStop(job, () => 'run-123');
    const stop = job.querySelector('.agent-tool-stop');
    assert.equal(stop.hidden, false);
    // First click: transport fails → button re-enabled.
    stop.click();
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(stop.disabled, false);
    assert.equal(stop.textContent, '■ Stop');
    // Second click: succeeds → button is retired.
    stop.click();
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(stopped, 2);
    assert.equal(stop.isConnected, false, 'stop button removed after successful stop');
    assert.ok(calls.every((c) => c.url.includes('agent/turns/stop')));
    const payload = JSON.parse(calls[1].body).payload;
    assert.equal(payload.run_id, 'run-123');
  } finally {
    globalThis.fetch = originalFetch;
    globalThis.document = previousDocument;
  }
});
