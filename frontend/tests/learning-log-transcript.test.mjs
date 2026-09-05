// The Learning log's "View LLM log" button must do real work: fetch the
// background conversation that holds a job's LLM transcript and render it.
// The old button rendered fine and did nothing, which is exactly the failure
// a text-only assertion cannot catch — so these tests drive the click.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderLogEntry, toggleLearningTranscript } from '../js/views/learning.js';

const learningView = await readFile(new URL('../js/views/learning.js', import.meta.url), 'utf8');

// withDoc swaps in a JSDOM document for the duration of fn. The learning view
// builds nodes from the global document, so it must be in place before any
// render call.
async function withDoc(fn) {
  const dom = new JSDOM('<body></body>');
  const previousDocument = globalThis.document;
  const previousWindow = globalThis.window;
  globalThis.document = dom.window.document;
  globalThis.window = dom.window;
  try {
    return await fn(dom.window.document);
  } finally {
    globalThis.document = previousDocument;
    globalThis.window = previousWindow;
  }
}

const jobEntry = {
  type: 'consolidate',
  status: 'done',
  llm_conversation_id: 'conv_llm_9',
  mutations: [{ kind: 'memory.upsert', snippet: 'run gofmt before commit' }],
};

const transcriptMessages = [
  { id: 'm1', role: 'user', content: 'EXPERIENCE PACKET: goal=run gofmt before commit', created_at: '2026-09-05T10:00:00Z' },
  { id: 'm2', role: 'assistant', content: 'Saved one durable constraint.', created_at: '2026-09-05T10:00:05Z' },
];

test('the LLM log is fetched with agent.conversations.get', () => {
  assert.match(learningView, /agent\.conversations\.get/);
});

test('clicking the LLM log button renders the transcript inline', async () => {
  const calls = [];
  const load = async (convID) => {
    calls.push(convID);
    return transcriptMessages;
  };

  await withDoc(async (doc) => {
    const node = renderLogEntry(jobEntry);
    doc.body.appendChild(node);
    const btn = node.querySelector('.learning-log-open');
    assert.ok(btn, 'expected the LLM log button');

    await toggleLearningTranscript(btn, load);

    assert.deepEqual(calls, ['conv_llm_9'], 'the click must load the conversation named by the button');
    const panel = node.querySelector('.learning-log-activity');
    assert.ok(panel, 'transcript panel must be appended below the entry');
    const text = panel.textContent;
    assert.ok(text.includes('EXPERIENCE PACKET'), 'the packet the model was shown must be visible');
    assert.ok(text.includes('Saved one durable constraint.'), 'the model answer must be visible');
    assert.equal(btn.textContent, 'Hide LLM log');
    assert.equal(btn.disabled, false, 'the button must be usable again after loading');
  });
});

test('clicking again collapses the transcript without refetching', async () => {
  const calls = [];
  const load = async (convID) => {
    calls.push(convID);
    return transcriptMessages;
  };

  await withDoc(async (doc) => {
    const node = renderLogEntry(jobEntry);
    doc.body.appendChild(node);
    const btn = node.querySelector('.learning-log-open');

    await toggleLearningTranscript(btn, load);
    assert.ok(node.querySelector('.learning-log-activity'), 'first click opens');

    await toggleLearningTranscript(btn, load);
    assert.equal(node.querySelector('.learning-log-activity'), null, 'second click collapses');
    assert.equal(btn.textContent, 'View LLM log');
    assert.deepEqual(calls, ['conv_llm_9'], 'collapsing must not refetch');
  });
});

test('a failed load reports it instead of failing silently', async () => {
  const load = async () => {
    throw new Error('conversation not found');
  };

  await withDoc(async (doc) => {
    const node = renderLogEntry(jobEntry);
    doc.body.appendChild(node);
    const btn = node.querySelector('.learning-log-open');

    await toggleLearningTranscript(btn, load);

    const panel = node.querySelector('.learning-log-activity');
    assert.ok(panel, 'the failure must be shown, not swallowed');
    assert.ok(panel.textContent.includes('LLM log unavailable'), 'expected an explicit failure line');
    assert.ok(panel.textContent.includes('conversation not found'), 'the reason should be surfaced');
    assert.equal(btn.textContent, 'View LLM log', 'the button must return to its idle label');
    assert.equal(btn.disabled, false, 'the button must not stay disabled after a failure');
  });
});
