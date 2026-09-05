import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderLogEntry } from '../js/views/learning.js';

const learningCSS = await readFile(new URL('../styles/learning.css', import.meta.url), 'utf8');
const globalCSS = await readFile(new URL('../styles/global.css', import.meta.url), 'utf8');
const learningView = await readFile(new URL('../js/views/learning.js', import.meta.url), 'utf8');
const learningHTML = await readFile(new URL('../index.html', import.meta.url), 'utf8');

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

test('About You is the first learning tab and exposes editable user memory', () => {
  const doc = new JSDOM(learningHTML).window.document;
  const tabs = [...doc.querySelectorAll('.learning-tabs [data-learning-tab]')];
  assert.equal(tabs[0]?.dataset.learningTab, 'about');
  assert.equal(tabs[0]?.textContent, 'About You');
  assert.equal(tabs[0]?.getAttribute('aria-selected'), 'true');
  assert.ok(doc.querySelector('#learning-panel-about'));
  assert.ok(doc.querySelector('#learning-user-memory'));
  assert.equal(doc.querySelector('#learning-panel-about')?.hidden, false);
});

test('About Agent is an editable learning tab with its own document panel', () => {
  const doc = new JSDOM(learningHTML).window.document;
  const tab = doc.querySelector('#learning-tab-agent');
  const panel = doc.querySelector('#learning-panel-agent');

  assert.equal(tab?.dataset.learningTab, 'agent');
  assert.equal(tab?.textContent, 'About Agent');
  assert.equal(tab?.getAttribute('aria-controls'), 'learning-panel-agent');
  assert.equal(panel?.getAttribute('aria-labelledby'), 'learning-tab-agent');
  assert.ok(doc.querySelector('#learning-agent-memory'));
  assert.ok(doc.querySelector('#learning-agent-save'));
});

test('user memory editor uses the dedicated update RPC', () => {
  assert.match(learningView, /memory\.user\.update/);
  assert.match(learningView, /learning-user-memory/);
});

test('soul memory editor uses the dedicated update RPC', () => {
  assert.match(learningView, /memory\.agent\.update/);
  assert.match(learningView, /learning-agent-memory/);
});

test('Experience tab lists episodes and memory records', () => {
  const doc = new JSDOM(learningHTML).window.document;
  const tab = doc.querySelector('#learning-tab-experience');
  assert.equal(tab?.dataset.learningTab, 'experience');
  assert.ok(doc.querySelector('#learning-experience-list'));
  assert.ok(doc.querySelector('#learning-records-list'));
  assert.ok(doc.querySelector('#learning-record-retire'));
  assert.match(learningView, /experience\.list/);
  assert.match(learningView, /experience\.get/);
  assert.match(learningView, /memory\.retire/);
  assert.doesNotMatch(learningView, /memory\.delete/);
});

test('Learning subscribes to jobs and recorded experience, not review events', () => {
  assert.match(learningView, /experience\.recorded/);
  assert.match(learningView, /learning\.job\.started/);
  assert.match(learningView, /learning\.job\.done/);
  assert.match(learningView, /learning\.job\.error/);
  assert.doesNotMatch(learningView, /learning\.review\.started/);
});

test('Search and graph copy talks about records, not fragments', () => {
  assert.match(learningHTML, /Search records and skills/);
  assert.match(learningHTML, />Record</);
  assert.doesNotMatch(learningHTML, />Fragment</);
});

test('About You uses the available desktop width while retaining a comfortable mobile inset', () => {
  assert.match(learningCSS, /\.learning-about-card\s*\{[\s\S]*?width:\s*min\(1180px,\s*calc\(100%\s*-\s*32px\)\);/);
  assert.match(learningCSS, /\.learning-about-card\s*\{[\s\S]*?box-sizing:\s*border-box;/);
  assert.match(learningCSS, /\.learning-memory-editor\s*\{[\s\S]*?min-height:\s*clamp\(300px,\s*42vh,\s*520px\);/);
});

test('Learning tabs are a self-contained control with breathing room before its panel', () => {
  assert.match(learningHTML, /class="tabs learning-tabs" role="tablist"/);
  assert.match(globalCSS, /\.tabs\s*\{[\s\S]*?width:\s*fit-content;[\s\S]*?padding:\s*3px;[\s\S]*?border:\s*1px solid var\(--border-soft\);/);
  assert.match(learningCSS, /\.learning-about-panel\s*\{[\s\S]*?padding-top:\s*12px;/);
});

// The Learning log button is only real when there is a transcript behind it.
// It used to render for any review entry with no handler and nothing to
// fetch: it looked alive and did nothing, with no error to explain why.
test('a job entry with a transcript exposes the LLM log button', () => {
  const node = withDocument(() => renderLogEntry({
    type: 'consolidate',
    status: 'done',
    llm_conversation_id: 'conv_llm_1',
    mutations: [{ kind: 'memory.upsert', snippet: 'run gofmt before commit' }],
  }));

  const btn = node.querySelector('.learning-log-open');
  assert.ok(btn, 'expected an LLM log button');
  assert.equal(btn.textContent, 'View LLM log');
  assert.equal(btn.type, 'button', 'a bare button inside a view must not submit anything');
  assert.equal(btn.dataset.llmConversationId, 'conv_llm_1', 'the button must carry the transcript id');
});

test('a source conversation line is shown without leaking the raw id', () => {
  const node = withDocument(() => renderLogEntry({
    type: 'consolidate',
    status: 'done',
    conversation_id: 'conv_1234567890',
    conversation_title: 'Refactor the learning log',
    llm_conversation_id: 'conv_llm_1',
  }));

  const conv = node.querySelector('.learning-log-conv');
  assert.ok(conv, 'expected a source-conversation line');
  assert.equal(conv.querySelector('.learning-log-conv-label')?.textContent, 'Source');
  assert.ok(conv.textContent.includes('Refactor the learning log'));
  assert.ok(!conv.textContent.includes('conv_1234567890'), 'raw conversation id must not be displayed');
});

test('an entry without a transcript renders no button instead of a dead one', () => {
  for (const entry of [
    // A legacy review entry (recorded before job transcripts existed) and a
    // job that ran without a model: neither has anything to open.
    { type: 'review', status: 'done' },
    { type: 'consolidate', status: 'done' },
    { type: 'consolidate', status: 'error' },
  ]) {
    const node = withDocument(() => renderLogEntry(entry));
    assert.equal(
      node.querySelector('.learning-log-open'),
      null,
      `entry ${JSON.stringify(entry)} must not render a button it cannot fill`,
    );
  }
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

test('failed review stays concise and has no manual retry action', () => {
  const node = withDocument(() => renderLogEntry({
    type: 'review',
    status: 'error',
    error: 'provider returned a verbose 429 payload with internal details',
  }));
  assert.ok(node.textContent.includes('Background review failed during automatic processing.'));
  assert.ok(!node.textContent.includes('verbose 429 payload'));
  assert.equal(node.querySelector('.learning-log-open'), null);
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
