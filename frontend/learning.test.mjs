import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderLogEntry, renderTranscript } from './js/views/learning.js';

function withDocument(fn) {
  const dom = new JSDOM('<body></body>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    return fn(dom.window.document);
  } finally {
    globalThis.document = previousDocument;
  }
}

test('review log entry shows a compact source line and a details button', () => {
  const node = withDocument(() => renderLogEntry({
    type: 'review',
    status: 'done',
    conversation_id: 'conv_1234567890',
    conversation_title: 'Refactor the learning log',
    review_id: 'review_1',
  }));

  const conv = node.querySelector('.learning-log-conv');
  assert.ok(conv, 'expected a source-conversation line');
  assert.equal(conv.querySelector('.learning-log-conv-label')?.textContent, 'Source');
  assert.ok(conv.textContent.includes('Refactor the learning log'));
  assert.ok(!conv.textContent.includes('conv_1234567890'), 'raw conversation id must not be displayed');

  const btn = node.querySelector('.learning-log-open');
  assert.equal(btn?.textContent, 'View review details');
});

test('review log entry lists saved mutations as kind + snippet rows', () => {
  const node = withDocument(() => renderLogEntry({
    type: 'review',
    status: 'done',
    mutations: [
      { kind: 'memory', tool: 'memory_save', snippet: 'Roles: use tuan/aku address style' },
      { kind: 'skills', tool: 'skill_save', snippet: 'learning-ui-log' },
    ],
  }));

  const rows = node.querySelectorAll('.learning-log-outcome-row');
  assert.equal(rows.length, 2);
  assert.equal(rows[0].querySelector('.learning-mut-kind')?.textContent, 'memory');
  assert.ok(rows[1].textContent.includes('learning-ui-log'));
});

test('finished review with zero mutations says so explicitly', () => {
  const node = withDocument(() => renderLogEntry({ type: 'review', status: 'done', mutations: [] }));
  assert.ok(node.textContent.toLowerCase().includes('nothing to save'));
});

test('cooldown skip is distinct from a completed review', () => {
  const node = withDocument(() => renderLogEntry({ type: 'review', status: 'skipped', reason: 'cooldown_or_inflight' }));
  assert.ok(node.querySelector('.learning-log-status-skipped'));
  assert.ok(node.querySelector('.learning-log-skipped')?.textContent.includes('cooldown'));
  assert.ok(!node.textContent.includes('Nothing to save'));
});

const transcript = {
  id: 'review_1',
  conversation_id: 'conv_1',
  model: 'luna',
  created_at: '2026-08-19T10:00:00Z',
  messages: [
    // Replayed user transcript — must never be rendered.
    { role: 'user', content: '[user] please remember I prefer Indonesian' },
    {
      role: 'assistant',
      reasoning: 'The user stated a durable preference; checking memory first.',
      tool_calls: [{ id: 'tc_1', name: 'memory_search', args: { query: 'Indonesian' } }],
    },
    { role: 'tool', tool_result: { tool_call_id: 'tc_1', name: 'memory_search', content: '3 entries for "docker", none match.' } },
    {
      role: 'assistant',
      reasoning: 'Not stored yet. Saving now.',
      tool_calls: [{ id: 'tc_2', name: 'memory_save', args: { content: 'User prefers Indonesian' } }],
    },
    { role: 'tool', tool_result: { tool_call_id: 'tc_2', name: 'memory_save', content: 'ok, saved fragment mem_abc123' } },
    { role: 'assistant', content: 'Saved one memory fragment about language preference.' },
  ],
};

test('details show thinking, tool cards, and the final note - but never the replayed transcript', () => {
  const view = withDocument(() => renderTranscript(transcript));
  const text = view.textContent;

  // Agent flow is visible: reasoning disclosures + terminal-style tool cards.
  const disclosures = view.querySelectorAll('details.agent-reasoning');
  assert.equal(disclosures.length, 2, 'each reasoning-bearing round keeps a thinking disclosure');
  assert.ok(text.includes('The user stated a durable preference'));
  assert.ok(text.includes('Not stored yet. Saving now.'));

  const cards = view.querySelectorAll('details.agent-tool-terminal');
  assert.equal(cards.length, 2);
  assert.match(cards[0].querySelector('.agent-tool-terminal-title')?.textContent || '', /memory_search/);
  // Meta summarizes the args (single-arg tools show just the value).
  assert.match(cards[0].querySelector('.agent-tool-terminal-meta')?.textContent || '', /Indonesian/, 'tool meta summarizes args');
  assert.match(cards[1].querySelector('.agent-tool-terminal-meta')?.textContent || '', /Indonesian/, 'memory_save meta shows the stored snippet');
  assert.ok(cards[0].querySelector('.agent-tool-terminal-output'), 'output panel exists');
  assert.ok(!cards[0].open, 'tool details are collapsed by default');

  // The final assistant summary renders as a plain conclusion line.
  const conclusion = view.querySelector('.learning-log-conclusion');
  assert.ok(conclusion, 'final summary present');
  assert.match(conclusion.textContent, /Saved one memory fragment/);

  // The replayed user transcript never appears.
  assert.ok(!text.includes('[user]'), 'replayed user message must not be rendered');
  assert.ok(!text.includes('please remember I prefer Indonesian'));
  assert.equal(view.querySelectorAll('.learning-log-tc-msg').length, 0);
});

test('assistant interstitial narration renders between steps', () => {
  const view = withDocument(() => renderTranscript({
    ...transcript,
    messages: [
      ...transcript.messages.slice(0, 4),
      { role: 'tool', tool_result: { tool_call_id: 'tc_2', name: 'memory_save', content: 'ok' } },
      { role: 'assistant', content: 'Now let me double check duplicates.', tool_calls: [{ id: 'tc_3', name: 'skill_search', args: { query: 'x' } }] },
    ],
  }));
  const notes = view.querySelectorAll('.learning-log-note');
  assert.ok(notes.length >= 1);
  assert.ok(notes[0].textContent.includes('double check'));
});

test('empty transcript still shows the banner and quiet empty state', () => {
  const view = withDocument(() => renderTranscript({ ...transcript, messages: [] }));
  assert.ok(view.querySelector('.learning-log-detail'));
  assert.equal(view.querySelectorAll('details.agent-tool-terminal').length, 0);
});