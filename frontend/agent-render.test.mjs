import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderConversation, renderToolJob } from './js/views/agent/render.js';

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
      steps: [{ type: 'tool_calls', tool_calls: [{ id: 'tool_3', name: 'skill_list', args: {}, status: 'ok', output: '[]' }] }],
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
