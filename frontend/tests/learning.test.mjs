import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderLogEntry, renderTranscript } from '../js/views/learning.js';

const learningCSS = await readFile(new URL('../styles/learning.css', import.meta.url), 'utf8');
const learningView = await readFile(new URL('../js/views/learning.js', import.meta.url), 'utf8');

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
  // Skip reasons follow the split taxonomy (detail.reason):
  // cooldown_active = deferred by retry cooldown, already_running = coalesced.
  const node = withDocument(() => renderLogEntry({ type: 'review', status: 'skipped', detail: { reason: 'cooldown_active' } }));
  assert.ok(node.querySelector('.learning-log-status-skipped'));
  assert.ok(node.querySelector('.learning-log-skipped')?.textContent.includes('cooldown'));
  assert.ok(!node.textContent.includes('Nothing to save'));

  const coalesced = withDocument(() => renderLogEntry({ type: 'review', status: 'skipped', detail: { reason: 'already_running' } }));
  assert.ok(coalesced.querySelector('.learning-log-skipped')?.textContent.includes('Coalesced'));
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
      tool_calls: [{ id: 'tc_1', name: 'memory', args: { op: 'search', query: 'Indonesian' } }],
    },
    { role: 'tool', tool_result: { tool_call_id: 'tc_1', name: 'memory', content: '3 entries for "docker", none match.' } },
    {
      role: 'assistant',
      reasoning: 'Not stored yet. Saving now.',
      tool_calls: [{ id: 'tc_2', name: 'memory', args: { op: 'save', content: 'User prefers Indonesian' } }],
    },
    { role: 'tool', tool_result: { tool_call_id: 'tc_2', name: 'memory', content: 'ok, saved fragment mem_abc123' } },
    { role: 'assistant', content: 'Saved one memory fragment about language preference.' },
  ],
};

test('details show thinking, tool cards, and the final note - but never the replayed transcript', () => {
  const view = withDocument(() => renderTranscript(transcript));

  // Agent flow is visible: reasoning disclosures + terminal-style tool cards.
  const disclosures = view.querySelectorAll('details.agent-reasoning');
  assert.equal(disclosures.length, 2, 'each reasoning-bearing round keeps a thinking disclosure');
  for (const details of disclosures) {
    const EventCtor = details.ownerDocument?.defaultView?.Event || Event;
    details.open = true;
    details.dispatchEvent(new EventCtor('toggle'));
  }
  const text = view.textContent;
  assert.ok(text.includes('The user stated a durable preference'));
  assert.ok(text.includes('Not stored yet. Saving now.'));

  const cards = view.querySelectorAll('.agent-tool-event');
  assert.equal(cards.length, 2);
  assert.ok(cards[0].open, 'event results are visible by default');
  assert.match(cards[0].querySelector('.agent-tool-event-title')?.textContent || '', /memory/i);
  // Meta summarizes the args (single-arg tools show just the value).
  assert.match(cards[0].querySelector('.agent-tool-event-summary-text')?.textContent || '', /Indonesian/, 'tool meta summarizes args');
  assert.match(cards[1].querySelector('.agent-tool-event-summary-text')?.textContent || '', /Indonesian/, 'memory save meta shows the stored snippet');
  assert.ok(cards[0].querySelector('.agent-tool-event-details'), 'raw request/output fold exists');
  assert.ok(!cards[0].querySelector('.agent-tool-event-details').open, 'raw fold is collapsed by default');

  // The final assistant summary renders as a plain conclusion line.
  const conclusion = view.querySelector('.learning-log-conclusion');
  assert.ok(conclusion, 'final summary present');
  assert.match(conclusion.textContent, /Saved one memory fragment/);

  // The replayed user transcript never appears.
  assert.ok(!text.includes('[user]'), 'replayed user message must not be rendered');
  assert.ok(!text.includes('please remember I prefer Indonesian'));
  assert.equal(view.querySelectorAll('.learning-log-tc-msg').length, 0);
});

test('assistant pre-tool text uses the same collapsed Thinking disclosure as Agent', () => {
  const thinking = [
    'Now I have a clear picture. Let me identify what is durable knowledge worth saving.',
    '',
    '1. Keep `table` output readable',
    '2. Keep fenced output intact',
    '',
    '| Area | Value |',
    '| --- | --- |',
    '| Inline | `safe` |',
    '',
    '```go',
    'func main() {}',
    '```',
  ].join('\n');
  const view = withDocument(() => renderTranscript({
    ...transcript,
    messages: [
      { role: 'assistant', content: thinking, tool_calls: [{ id: 'tc_thinking', name: 'memory', args: { op: 'search', query: 'user' } }] },
      { role: 'tool', tool_result: { tool_call_id: 'tc_thinking', name: 'memory', content: 'No matching memories.' } },
    ],
  }));

  const details = view.querySelector('details.agent-reasoning');
  assert.ok(details, 'pre-tool assistant text should be a Thinking disclosure');
  assert.equal(details.querySelector('.agent-reasoning-title')?.textContent, 'Thinking');
  assert.equal(view.querySelectorAll('.learning-log-note').length, 0);

  const EventCtor = details.ownerDocument?.defaultView?.Event || Event;
  details.open = true;
  details.dispatchEvent(new EventCtor('toggle'));
  const content = details.querySelector('.agent-reasoning-content');
  assert.ok(content?.textContent.includes('Keep table output readable'));
  assert.ok(content?.querySelector('ol'), 'ordered list must render inside Thinking');
  assert.ok(content?.querySelector('.markdown-table-scroll > table'), 'table must render inside its scroll wrapper');
  assert.ok(content?.querySelector('.markdown-table-scroll code:not(pre code)'), 'inline backticks must stay inline in table cells');
  assert.ok(content?.querySelector('pre[data-complete="true"] > code.language-go'), 'triple-backtick fence must render as a complete code block');
  assert.doesNotMatch(content?.textContent || '', /```/, 'fence markers must stay hidden');
});

test('assistant pre-tool narration is collapsed as Thinking between steps', () => {
  const view = withDocument(() => renderTranscript({
    ...transcript,
    messages: [
      ...transcript.messages.slice(0, 4),
      { role: 'tool', tool_result: { tool_call_id: 'tc_2', name: 'memory_save', content: 'ok' } },
      { role: 'assistant', content: 'Now let me double check duplicates.', tool_calls: [{ id: 'tc_3', name: 'skill_search', args: { query: 'x' } }] },
    ],
  }));
  const disclosures = view.querySelectorAll('details.agent-reasoning');
  assert.equal(disclosures.length, 3);
  assert.ok([...disclosures].some((details) => details._reasoningRaw.includes('double check')));
  assert.equal(view.querySelectorAll('.learning-log-note').length, 0);
});

test('Learning ignores ACP wait/result bookkeeping without appending null nodes', () => {
  const view = withDocument(() => renderTranscript({
    ...transcript,
    messages: [{
      role: 'assistant',
      tool_calls: [
        { id: 'spawn-1', name: 'subagent', args: { agent_id: 'acp_dev', prompt: 'Inspect the CSS' } },
        { id: 'wait-1', name: 'subagent_wait', args: { id: 'acprun_1' } },
        { id: 'result-1', name: 'subagent_result', args: { id: 'acprun_1' } },
      ],
    }],
  }));

  assert.equal(view.querySelectorAll('.agent-subagent-card').length, 1);
  assert.equal(view.querySelectorAll('.agent-tool-terminal').length, 0);
});

test('empty transcript still shows the banner and quiet empty state', () => {
  const view = withDocument(() => renderTranscript({ ...transcript, messages: [] }));
  assert.ok(view.querySelector('.learning-log-detail'));
  assert.equal(view.querySelectorAll('details.agent-tool-terminal').length, 0);
});

test('Learning mobile layout gives search controls and both panes room to scroll', () => {
  const mobileRules = learningCSS.slice(learningCSS.indexOf('@media (max-width: 900px)'));
  assert.match(mobileRules, /flex-wrap:\s*wrap/);
  assert.match(mobileRules, /grid-template-rows:\s*minmax\(150px,\s*1fr\)\s+minmax\(220px,\s*1fr\)/);
  assert.match(mobileRules, /min-height:\s*0/);
  assert.match(mobileRules, /overflow-y:\s*auto/);
});

test('Learning splitter does not leave desktop inline columns active on narrow screens', () => {
  assert.match(learningView, /removeProperty\(['"]grid-template-columns['"]\)/);
});
