import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';
import { JSDOM } from 'jsdom';

import { filterEvents, normalizeTodoItem, summarizeTodos, toggleTask } from '../page/conversation/state.js';
import { eventWidgetTag, supportedEventTypes } from '../widgets/registry.js';
import { BUILTIN_TOOL_WIDGETS, toolWidgetDefinition } from '../widgets/tools/registry.js';
import { messageEvents, applyAskStatus, applyRoundDelta, applyRoundDone, applyTurnError, canonicalTwins, formatModelLabel, modelOptions, providerRetryLabel } from '../page/conversation/live-data.js';
import { mergeConversationEvents, selectConversationTurnMessages } from '../page/conversation/conversation-sync.js';
import { acceptRoundFrame, roundDoneNeedsReplay, roundStreamURL } from '../page/conversation/round-stream.js';
import { filterConversations, isConversationActive, normalizeConversationStatus } from '../page/conversation/room-state.js';
import { acceptSubagentStreamFrame, enrichSubagentToolEvent, toSubagentView } from '../page/conversation/subagent-state.js';
import {
  filterHomeMCPApps,
  filterHomeMCPAppsByCategory,
  homeMCPCategories,
  homeMCPAppsWithUI,
  homePluginIconGlyph,
  homePluginIconSource,
  homePluginIconValue,
  homeMCPEmptyCopy,
  markHomeMCPConnected,
} from '../page/home/mcp.js';
import {
  appendVisibleCount,
  attachLiveHost,
  commitLiveHost,
  growVisibleCount,
  mountWidgetAfterLiveHost,
  reconcileVisibleWidgets,
  shiftWindowOlder,
  transcriptWindow,
  upsertLiveTurnWidget,
  upsertVisibleWidget,
} from '../page/conversation/transcript-window.js';

const root = new URL('../', import.meta.url);

// The shared-styles registry lives inside the module instance: every importer
// must pin the same versioned specifier or the widgets and the PiP healer end
// up in two registries and re-adoption silently heals zero roots. Tests import
// the exact specifier the source uses instead of hardcoding a version that
// goes stale on the next cache-bust.
async function sharedStylesImport(importer) {
  const importerURL = new URL(importer, root);
  const source = await readFile(importerURL, 'utf8');
  const specifier = source.match(/from\s+'([^']*shared\/widget-styles\.js[^']*)'/)?.[1];
  assert.ok(specifier, `${importer} imports shared/widget-styles.js`);
  return import(new URL(specifier, importerURL).href);
}

test('prototype overlays style the current type-aware local-file viewer without page-level overflow', async () => {
  const css = await readFile(new URL('./overlays.css', root), 'utf8');
  const agentCSS = await readFile(new URL('./widgets/agent-message.css', root), 'utf8');
  const userCSS = await readFile(new URL('./widgets/user-message.css', root), 'utf8');

  assert.match(css, /\.agent-text-preview-frame\s*\{[\s\S]*box-sizing:\s*border-box;/);
  assert.match(css, /\.agent-text-preview-bar\s*\{[\s\S]*display:\s*grid;/);
  assert.match(css, /\.agent-text-preview-content\s*\{[\s\S]*min-width:\s*0;/);
  assert.match(css, /\.agent-text-preview-content pre\s*\{[\s\S]*width:\s*100%;[\s\S]*overflow:\s*auto;/);
  assert.match(css, /\.agent-text-preview-content\.is-code-editor\s*\{\s*display:\s*flex;\s*flex-direction:\s*column;\s*padding:\s*0;\s*overflow:\s*hidden;\s*\}/);
  assert.match(css, /\.agent-text-preview-codemirror\s*\{\s*display:\s*flex;\s*flex:\s*1;\s*min-width:\s*0;\s*min-height:\s*0;\s*\}/);
  assert.match(css, /\.agent-text-preview-codemirror \.CodeMirror\s*\{\s*width:\s*100%;\s*height:\s*100%;/);
  assert.match(css, /\.artifact-popup-frame\s*\{[\s\S]*min-height:\s*0;/);
  assert.match(css, /\.agent-audio-lightbox-frame,[\s\S]*\.agent-video-lightbox-frame\s*\{/);
  assert.match(agentCSS, /\.answer \.markdown-table-scroll\s*\{[^}]*padding-inline-end:\s*2px;[^}]*scroll-padding-inline-end:\s*2px;/);
  assert.match(userCSS, /\.message-markdown \.markdown-table-scroll\s*\{[^}]*padding-inline-end:\s*2px;[^}]*scroll-padding-inline-end:\s*2px;/);
});

test('conversation filters keep thinking with messages and leave execution in activity', () => {
  const events = [
    { id: 'u', type: 'user.message' },
    { id: 'r', type: 'agent.reasoning' },
    { id: 't', type: 'tool.run' },
    { id: 'a', type: 'agent.message' },
  ];

  assert.deepEqual(filterEvents(events, 'message').map(({ id }) => id), ['u', 'r', 'a']);
  assert.deepEqual(filterEvents(events, 'activity').map(({ id }) => id), ['t']);
});

test('conversation shell uses an aquatic wave field and deferred PWA install affordance', async () => {
  const svg = await readFile(new URL('./assets/topography.svg', root), 'utf8');
  const html = await readFile(new URL('./index.html', root), 'utf8');
  const app = await readFile(new URL('./page/conversation/app.js', root), 'utf8');

  assert.match(svg, /data-aquatic="waves"/);
  assert.match(svg, /class="wave-field"/);
  assert.match(svg, /<path\b[^>]*d="M-120 /);
  assert.match(html, /id="global-nav-install"[^>]*hidden/);
  assert.match(html, /id="global-nav-install"[\s\S]*data-lucide="download"/);
  assert.match(app, /pwa-install\.js/);
});

test('persisted RPC messages become chronological standalone widgets', () => {
  const mapped = messageEvents([
    { id: 'u1', role: 'user', content: 'Ship it', created_at: '2026-09-10T09:42:00+07:00' },
    {
      id: 'a1', role: 'assistant', model: 'gpt-5.6-luna', created_at: '2026-09-10T09:43:00+07:00',
      usage: { input_tokens: 1200, output_tokens: 340 },
      steps: [
        { type: 'reasoning', content: 'Inspect the current seam.' },
        { type: 'tool_calls', tool_calls: [{ id: 't1', name: 'exec', args: { cmd: 'make test' }, status: 'ok', output: 'PASS', presentation: { variant: 'terminal' } }] },
        { type: 'text', content: 'Done.' },
      ],
    },
  ]);

  assert.deepEqual(mapped.map(({ type }) => type), ['user.message', 'agent.reasoning', 'tool.run', 'agent.message']);
  assert.equal(mapped[2].command, '{\n  "cmd": "make test"\n}');
  assert.equal(mapped[2].presentation.variant, 'terminal');
  assert.equal(mapped[2].toolCallId, 't1');
  assert.equal(mapped[3].markdown, 'Done.');
  assert.equal(mapped[3].usage, '↑ 1.2k  ↓ 340');
});

test('usage shows optional cached input and cache-read counters only when positive', () => {
  const [withCache] = messageEvents([{
    id: 'cached', role: 'assistant', model: 'gpt-5.6-luna', created_at: '2026-09-10T09:43:00+07:00',
    usage: { input_tokens: 1200, output_tokens: 340, cache_write: 16, cache_read: 800 },
    content: 'Cached response.',
  }]);
  const [, withoutCache] = messageEvents([
    { id: 'plain-user', role: 'user', content: 'Hi' },
    { id: 'plain', role: 'assistant', model: 'gpt-5.6-luna', usage: { input_tokens: 10, output_tokens: 2, cache_write: 0, cache_read: 0 }, content: 'Plain response.' },
  ]);

  assert.match(withoutCache.usage, /↑ 10  ↓ 2/);
  assert.doesNotMatch(withoutCache.usage, /cached input|cache read/);
  assert.match(withCache.usage, /CW 16/);
  assert.match(withCache.usage, /CR 800/);
  assert.doesNotMatch(withCache.usage, /cached input|cache read/);
  assert.match(withCache.usageTitle, /Cache write 16/);
  assert.match(withCache.usageTitle, /Cache read 800/);
});

test('collapsed navigation keeps the sidebar control below Settings', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const nav = document.querySelector('#global-nav');
  const settings = nav?.querySelector('[data-global-route="settings"]');
  const expand = nav?.querySelector('#global-nav-expand');
  assert.ok(settings);
  assert.equal(settings.getAttribute('href'), '#settings');
  assert.ok(expand);
  assert.ok([...nav.children].indexOf(expand) > [...nav.children].indexOf(settings));
});

test('failed assistant messages stay visible with a retry affordance only for the latest failure', () => {
  const failed = messageEvents([
    { id: 'u1', role: 'user', content: 'Run the checks' },
    { id: 'a1', role: 'assistant', status: 'error', error: 'provider unavailable', model: 'openai:gpt-5.6-sol' },
  ]);
  assert.equal(failed.length, 2, 'an empty failed answer still needs a visible event');
  assert.equal(failed[1].status, 'error');
  assert.equal(failed[1].error, 'provider unavailable');
  assert.equal(failed[1].canRetry, true);

  const recovered = messageEvents([
    { id: 'u1', role: 'user', content: 'Run the checks' },
    { id: 'a1', role: 'assistant', status: 'error', error: 'provider unavailable' },
    { id: 'a2', role: 'assistant', status: 'done', content: 'Recovered.' },
  ]);
  assert.equal(recovered.find((event) => event.messageId === 'a1')?.canRetry, false,
    'a recovered historical error must not expose a retry action');
});

test('turn errors patch a live answer in place and create one when no text arrived', () => {
  const events = [{ id: 'live-m1-answer', messageId: 'm1', type: 'agent.message', markdown: 'Partial answer.' }];
  const patched = applyTurnError(events, {
    conversation_id: 'c1', run_id: 'run-1', message_id: 'm1', message: 'provider failed',
  }, { model: 'openai:gpt-5.6-sol' });
  assert.equal(patched, events[0]);
  assert.equal(events.length, 1);
  assert.equal(patched.status, 'error');
  assert.equal(patched.error, 'provider failed');
  assert.equal(patched.markdown, 'Partial answer.');

  const empty = [];
  const created = applyTurnError(empty, {
    conversation_id: 'c1', run_id: 'run-2', message_id: 'm2', message: 'no provider configured',
  }, { model: 'openai:gpt-5.6-sol' });
  assert.equal(empty.length, 1);
  assert.equal(created.id, 'live-m2-answer');
  assert.equal(created.status, 'error');
  assert.equal(created.canRetry, true);

  // A repeated lifecycle error for the same message folds into the row it
  // already owns — live dedup must match the single persisted row a reload
  // renders.
  const again = applyTurnError(events, {
    conversation_id: 'c1', run_id: 'run-1', message_id: 'm1', message: 'provider failed again',
  });
  assert.equal(again, events[0]);
  assert.equal(events.filter((event) => event.messageId === 'm1' || event.id === 'live-m1-answer').length, 1);
  const repeated = applyTurnError(empty, {
    conversation_id: 'c1', run_id: 'run-3', message_id: 'm2', message: 'no provider configured',
  });
  assert.equal(repeated, empty[0]);
  assert.equal(empty.length, 1);
});

test('model labels share provider/model format across catalog and fallback values', () => {
  const catalog = [{ id: 'deepseek-v4-flash', provider_id: 'opencode', provider_name: 'OpenCode', display_name: 'DeepSeek V4.1 Flash' }];
  assert.equal(formatModelLabel('opencode:deepseek-v4-flash', catalog), 'OpenCode/deepseek-v4-flash');
  assert.equal(formatModelLabel({ id: 'deepseek-v4-flash', provider_id: 'opencode' }, catalog), 'OpenCode/deepseek-v4-flash');
  assert.equal(formatModelLabel('codex:gpt-5.6-luna'), 'codex/gpt-5.6-luna');
  assert.equal(formatModelLabel('', catalog), '');

  const duplicateModelCatalog = [
    { id: 'gpt-5.6-sol', provider_id: 'openai', provider_name: 'OpenAI' },
    { id: 'gpt-5.6-sol', provider_id: 'codex', provider_name: 'Codex' },
  ];
  assert.equal(formatModelLabel({ id: 'gpt-5.6-sol', provider_id: 'codex' }, duplicateModelCatalog), 'Codex/gpt-5.6-sol');
  assert.equal(formatModelLabel({ id: 'gpt-5.6-sol', provider_id: 'openai' }, duplicateModelCatalog), 'OpenAI/gpt-5.6-sol');
  const [legacyMessage] = messageEvents([
    { id: 'legacy', role: 'assistant', model: 'gpt-5.6-sol', content: 'From the room model.' },
  ], { models: duplicateModelCatalog, providerID: 'codex' });
  assert.equal(legacyMessage.model, 'Codex/gpt-5.6-sol', 'legacy messages use the room provider when their own provider is absent');
});

test('persisted user attachments remain owned by the user message widget', () => {
  const [event] = messageEvents([{
    id: 'u1', role: 'user', content: 'Review this map', created_at: '2026-09-10T09:42:00+07:00',
    attachments: [{ type: 'image', name: 'map.svg', file_path: '/tmp/map.svg' }],
  }]);
  assert.deepEqual(event.attachments, [{ type: 'image', name: 'map.svg', file_path: '/tmp/map.svg' }]);
});

test('todo view normalizes backend statuses and keeps in-progress work visible', () => {
  const items = [
    { id: 'pending', content: 'Queue', status: 'pending' },
    { id: 'active', content: 'Running', status: 'in_progress' },
    { id: 'done', content: 'Ship', status: 'completed' },
    { id: 'legacy', label: 'Legacy done', done: true },
  ].map(normalizeTodoItem);

  assert.deepEqual(items.map((item) => item.status), ['pending', 'in_progress', 'completed', 'completed']);
  assert.deepEqual(items.map((item) => item.label), ['Queue', 'Running', 'Ship', 'Legacy done']);
  assert.deepEqual(summarizeTodos(items), { total: 4, pending: 1, in_progress: 1, completed: 2 });
});

test('SSE round deltas update one live widget per semantic stream', () => {
  const events = [];
  applyRoundDelta(events, { seq: 1, kind: 'reasoning', text: 'Check contracts. ' }, { messageId: 'm1' });
  applyRoundDelta(events, { seq: 2, kind: 'reasoning', text: 'Then wire UI.' }, { messageId: 'm1' });
  applyRoundDelta(events, { seq: 3, kind: 'tool', tool_call_id: 't1', name: 'exec', args: { cmd: 'go test' } }, { messageId: 'm1' });
  applyRoundDelta(events, { seq: 4, kind: 'tool', tool_call_id: 't1', text: 'ok' }, { messageId: 'm1' });
  applyRoundDelta(events, { seq: 5, kind: 'text', text: 'Ready.' }, { messageId: 'm1', model: 'Astra' });

  assert.deepEqual(events.map(({ type }) => type), ['agent.reasoning', 'tool.run', 'agent.message']);
  assert.equal(events[0].summary, 'Check contracts. Then wire UI.');
  assert.equal(events[1].output, 'ok');
  assert.equal(events[2].markdown, 'Ready.');
});

test('live tool events retain the round run id for per-call cancellation', () => {
  const events = [];
  applyRoundDelta(events, {
    seq: 1, kind: 'tool', tool_call_id: 'call-1', name: 'exec', args: { command: 'sleep 5' },
  }, { messageId: 'm1', runId: 'run-1' });
  assert.equal(events[0].runId, 'run-1');
  assert.equal(events[0].toolCallId, 'call-1');
});

test('streamed reasoning keeps a bounded summary while retaining the full disclosure source', () => {
  const events = [];
  const first = 'I need to inspect the provider boundary before changing anything. ';
  const second = 'This is deliberately long context that must stay inside the disclosure rather than stretching the transcript row.';
  applyRoundDelta(events, { seq: 1, kind: 'reasoning', text: first }, { messageId: 'm1' });
  applyRoundDelta(events, { seq: 2, kind: 'reasoning', text: second }, { messageId: 'm1' });
  assert.equal(events[0].details[0], `${first}${second}`);
  assert.ok(events[0].summary.length < events[0].details[0].length);
  assert.match(events[0].summary, /^I need to inspect/);
});

test('round stream cursor rejects duplicates, detects gaps, and resumes from the last contiguous sequence', () => {
  const cursor = { lastSeq: 0 };
  assert.deepEqual(acceptRoundFrame(cursor, { seq: 1 }), { accept: true, gap: false });
  assert.equal(cursor.lastSeq, 1);
  assert.deepEqual(acceptRoundFrame(cursor, { seq: 1 }), { accept: false, gap: false });
  assert.deepEqual(acceptRoundFrame(cursor, { seq: 3 }), { accept: false, gap: true });
  assert.equal(cursor.lastSeq, 1);
  assert.equal(roundStreamURL({ run_id: 'run 1', message_id: 'msg/1' }, cursor), '/stream?run_id=run+1&message_id=msg%2F1&after=1');
  assert.equal(roundDoneNeedsReplay(cursor, { last_seq: 3 }), true);
});

test('round completion settles live tool state and attaches authoritative usage', () => {
  const events = [
    { id: 'live-m1-tool-t1', type: 'tool.run', status: 'running' },
    { id: 'live-m1-answer', type: 'agent.message', markdown: 'Done', usage: '' },
  ];
  const updated = applyRoundDone(events, 'm1', { state: 'done', usage: { input_tokens: 1200, output_tokens: 44 } });
  assert.deepEqual(updated.map(({ id }) => id), ['live-m1-tool-t1', 'live-m1-answer']);
  assert.equal(events[0].status, 'complete');
  assert.equal(events[1].usage, '↑ 1.2k  ↓ 44');
});

test('round error keeps partial content and exposes the terminal error', () => {
  const events = [{ id: 'live-m1-answer', messageId: 'm1', type: 'agent.message', markdown: 'partial' }];
  const updated = applyRoundDone(events, 'm1', { state: 'error', error: 'context window exceeded' });
  assert.equal(updated.length, 1);
  assert.equal(events[0].status, 'error');
  assert.equal(events[0].error, 'context window exceeded');

  const empty = [];
  const created = applyRoundDone(empty, 'm2', { state: 'error', error: 'provider failed' });
  assert.equal(created.length, 1);
  assert.equal(created[0].id, 'live-m2-answer');
  assert.equal(created[0].error, 'provider failed');
});

test('provider retry status is concise and does not expose an unbounded error payload', () => {
  assert.equal(providerRetryLabel({ attempt: 2, max_attempts: 3, delay_ms: 1200, error: 'provider returned HTTP 429: slow down' }),
    'Retrying request (2/3) in 1.2s — slow down');
  assert.ok(providerRetryLabel({ attempt: 1, max_attempts: 3, error: 'x'.repeat(500) }).length < 180);
});

test('subagent protocol bookkeeping folds into the primary delegation widget', () => {
  const mapped = messageEvents([{
    id: 'a1', role: 'assistant', steps: [{ type: 'tool_calls', tool_calls: [
      { id: 'spawn', name: 'subagent', args: { op: 'spawn', title: 'Audit widgets' }, status: 'ok' },
      { id: 'wait', name: 'subagent_wait', args: { id: 'run-1' }, status: 'ok' },
      { id: 'result', name: 'subagent_result', args: { id: 'run-1' }, status: 'ok' },
    ] }],
  }]);
  assert.deepEqual(mapped.map(({ tool }) => tool), ['subagent']);
});

test('ask lifecycle updates the matching question widget instead of leaving a stale status', () => {
  const events = [{ id: 'question', type: 'tool.run', tool: 'ask_question', toolCallId: 'call-1', status: 'ok', output: '' }];
  assert.equal(applyAskStatus(events, { tool_call_id: 'call-1' }, 'running').status, 'running');
  assert.equal(applyAskStatus(events, { tool_call_id: 'call-1', answer: 'Santai' }, 'ok').output, 'Santai');
  assert.equal(applyAskStatus(events, { tool_call_id: 'missing' }, 'failed'), null);
});

test('authoritative snapshots merge external user messages without duplicating live events', () => {
  // The optimistic row is keyed by the persisted user message id returned by
  // turns.start, so adoption is by identity — not by content comparison.
  const events = [
    { id: 'msg-user-1', type: 'user.message', content: 'Review **this**', optimistic: true },
    { id: 'live-msg-1-answer', type: 'agent.message', markdown: 'partial' },
  ];
  const snapshot = [
    { id: 'msg-user-1', type: 'user.message', content: 'Review **this**', attachments: [] },
    { id: 'msg-1-answer', type: 'agent.message', markdown: 'settled', usage: '↑ 1k  ↓ 2' },
  ];

  const changed = mergeConversationEvents(events, snapshot);

  assert.deepEqual(changed.map(({ id }) => id), ['msg-user-1', 'live-msg-1-answer']);
  assert.equal(events[0].optimistic, false, 'the optimistic user bubble is adopted by the persisted message');
  assert.equal(events[0].id, 'msg-user-1', 'mounted DOM identity stays stable');
  assert.equal(events[1].markdown, 'settled', 'the live answer receives authoritative content in place');
  assert.equal(events.length, 2, 'snapshot reconciliation does not duplicate the turn');
});

test('authoritative snapshots do not mark unchanged widgets for a second render', () => {
  const events = [
    { id: 'msg-1-answer', type: 'agent.message', messageId: 'msg-1', markdown: 'settled', usage: '↑ 1k  ↓ 2' },
    { id: 'msg-1-tool-call-1', type: 'tool.run', messageId: 'msg-1', toolCallId: 'call-1', output: 'done', status: 'complete' },
  ];
  const snapshot = events.map((event) => ({ ...event }));

  const changed = mergeConversationEvents(events, snapshot);

  assert.deepEqual(changed, [], 'a lifecycle snapshot with no display changes should be a no-op');
});

test('stale authoritative snapshots cannot rewind a live event', () => {
  const events = [{
    id: 'live-m1-answer', messageId: 'm1', type: 'agent.message',
    markdown: 'newest streamed answer', status: 'running',
  }];
  const snapshot = [{
    id: 'm1-answer', messageId: 'm1', type: 'agent.message',
    markdown: 'old', status: 'running',
  }];

  mergeConversationEvents(events, snapshot);

  assert.equal(events[0].markdown, 'newest streamed answer');
});

test('lifecycle turn refresh selects only the new turn instead of replaying history', () => {
  const messages = [
    { id: 'old-user', role: 'user', content: 'Old' },
    { id: 'old-answer', role: 'assistant', content: 'Old answer' },
    { id: 'new-user', role: 'user', content: 'New' },
    { id: 'new-answer', role: 'assistant', content: 'New answer' },
  ];

  assert.deepEqual(
    selectConversationTurnMessages(messages, 'new-answer').map((message) => message.id),
    ['new-user', 'new-answer'],
  );
});

test('optimistic user insertion adopts a persisted message that raced the turn-start event', () => {
  const sentAt = Date.parse('2026-09-11T09:00:00Z');
  const events = messageEvents([{
    id: 'persisted-user', role: 'user', content: 'Ship **this**',
    created_at: new Date(sentAt).toISOString(), attachments: [],
  }]);
  const optimistic = {
    id: 'persisted-user', type: 'user.message', content: 'Ship **this**', attachments: [],
    optimistic: true, createdAt: new Date(sentAt).toISOString(),
  };
  const changed = mergeConversationEvents(events, [optimistic]);
  assert.equal(events.length, 1);
  assert.equal(changed[0].id, 'persisted-user');
  assert.equal(Boolean(changed[0].optimistic), false);
});

// Regression: attaching an image used to paint the user message twice. The
// backend persists the row and fills attachment file_path when it saves the
// image to disk — a field the client cannot know at send time. The optimistic
// row is keyed by the persisted message id (turns.start user_message_id), so
// the merge adopts it by identity and server-side payload rewrites can never
// fork a second bubble.
test('an image send settles onto the persisted row that gained a server file_path', () => {
  const events = [{
    id: 'msg_u9', type: 'user.message', content: 'look', optimistic: true,
    attachments: [{ type: 'image', name: 'cat.png', media_type: 'image/png', data_url: 'data:image/png;base64,AAA' }],
  }];
  mergeConversationEvents(events, [{
    id: 'msg_u9', type: 'user.message', content: 'look', createdAt: '2026-10-14T01:00:00Z',
    attachments: [{ type: 'image', name: 'cat.png', media_type: 'image/png', data_url: 'data:image/png;base64,AAA', file_path: '/data/attachments/c1/cat.png' }],
  }]);
  assert.equal(events.length, 1, 'the persisted copy must not append a second bubble');
  assert.equal(events[0].optimistic, false);
  assert.equal(events[0].attachments[0].file_path, '/data/attachments/c1/cat.png');
});

// The persisted snapshot can win the race with the turns.start response (the
// WS turn.started frame beats the HTTP reply). When the optimistic row lands
// second it must not overwrite authoritative fields — in particular the
// file_path the server added — with its pre-send attachment payload.
test('a late optimistic echo cannot strip a persisted attachment file_path', () => {
  const events = [{
    id: 'msg_u9', type: 'user.message', content: 'look',
    attachments: [{ type: 'image', name: 'cat.png', media_type: 'image/png', file_path: '/data/attachments/c1/cat.png' }],
  }];
  mergeConversationEvents(events, [{
    id: 'msg_u9', type: 'user.message', content: 'look', optimistic: true,
    attachments: [{ type: 'image', name: 'cat.png', media_type: 'image/png', data_url: 'data:image/png;base64,AAA' }],
  }]);
  assert.equal(events.length, 1);
  assert.equal(events[0].optimistic, false);
  assert.equal(events[0].attachments[0].file_path, '/data/attachments/c1/cat.png');
});

// The dedup contract needs the wire id, so the submit handler must key the
// optimistic row by the persisted user_message_id returned by turns.start —
// not by a client-local event id that only fuzzy content matching could join.
test('the optimistic send row is keyed by the persisted user message id', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const submitStart = app.indexOf("messageForm.addEventListener('submit'");
  const submitEnd = app.indexOf('stopTurnButton.addEventListener', submitStart);
  assert.ok(submitStart >= 0 && submitEnd > submitStart);
  const submitBody = app.slice(submitStart, submitEnd);
  const userEventStart = submitBody.indexOf('const userEvent');
  assert.ok(userEventStart >= 0, 'submit still builds an optimistic user row');
  assert.match(submitBody.slice(userEventStart), /user_message_id/);
});

test('authoritative assistant steps settle live semantic widgets by message id', () => {
  const events = [
    { id: 'live-m1-reasoning', type: 'agent.reasoning', messageId: 'm1', details: ['partial'] },
    { id: 'live-m1-tool-call-1', type: 'tool.run', messageId: 'm1', toolCallId: 'call-1', output: 'part' },
    { id: 'live-m1-answer', type: 'agent.message', messageId: 'm1', markdown: 'partial' },
  ];
  const snapshot = messageEvents([{
    id: 'm1', role: 'assistant', content: 'settled', reasoning: 'full thought',
    tool_calls: [{ id: 'call-1', name: 'exec', args: { command: 'pwd' }, output: 'done', status: 'ok' }],
  }]);

  mergeConversationEvents(events, snapshot);

  assert.equal(events.length, 3, 'persisted assistant steps do not duplicate live widgets');
  assert.equal(events.find((event) => event.type === 'agent.message').markdown, 'settled');
  assert.equal(events.find((event) => event.type === 'tool.run').output, 'done');
  assert.deepEqual(events.find((event) => event.type === 'agent.reasoning').details, ['full thought']);
});

test('a re-attached round stream claims its canonical snapshot twins instead of duplicating them', () => {
  // Reload while a turn waits on ask_question: the persisted message already
  // carries the round's reasoning and the pending tool call (Execute persists
  // the round before tools run), then the round stream replays the same steps
  // as live-<messageId> events. Each replayed frame must claim its canonical
  // twin so the step renders exactly once.
  const events = messageEvents([
    { id: 'msg-u', role: 'user', content: 'ask me' },
    {
      id: 'msg-a', role: 'assistant', steps: [
        { type: 'reasoning', content: 'Call ask_question once' },
        { type: 'tool_calls', tool_calls: [{ id: 'call_00_x', name: 'ask_question', args: { question: 'Pick one' }, status: 'running' }] },
      ],
    },
  ]);
  applyRoundDelta(events, { kind: 'reasoning', text: 'Call ask_question once' }, { messageId: 'msg-a', runId: 'run-1' });
  applyRoundDelta(events, { kind: 'tool', tool_call_id: 'call_00_x', name: 'ask_question', args: '{"question":"Pick one"}' }, { messageId: 'msg-a', runId: 'run-1' });

  const liveReasoning = events.find((event) => event.id === 'live-msg-a-reasoning');
  const liveTool = events.find((event) => event.id === 'live-msg-a-tool-call_00_x');
  assert.deepEqual(canonicalTwins(events, liveReasoning).map(({ id }) => id), ['msg-a-reasoning-0']);
  assert.deepEqual(canonicalTwins(events, liveTool).map(({ id }) => id), ['msg-a-tool-call_00_x']);
  // A sibling tool call of the same round and a non-live candidate are never twins.
  assert.deepEqual(canonicalTwins(events, { ...liveTool, toolCallId: 'call_00_other' }), []);
  assert.deepEqual(canonicalTwins(events, { id: 'msg-a-tool-call_00_x', type: 'tool.run', messageId: 'msg-a' }), []);
});

test('a replayed ask card stays the single settled row through answer and terminal merge', () => {
  const persisted = (status, output = '') => messageEvents([
    { id: 'msg-u', role: 'user', content: 'ask me' },
    {
      id: 'msg-a', role: 'assistant', steps: [
        { type: 'reasoning', content: 'Call ask_question once' },
        { type: 'tool_calls', tool_calls: [{ id: 'call_00_x', name: 'ask_question', args: { question: 'Pick one' }, status, output }] },
        { type: 'text', content: 'Done.' },
      ],
    },
  ]);
  const events = persisted('running');
  // queueLiveWidget drops each canonical twin the moment its live counterpart
  // is created; mirror that seam here so the whole lifecycle runs at data level.
  const evict = (id) => {
    const liveEvent = events.find((event) => event.id === id);
    for (const twin of canonicalTwins(events, liveEvent)) events.splice(events.indexOf(twin), 1);
  };
  applyRoundDelta(events, { kind: 'reasoning', text: 'Call ask_question once' }, { messageId: 'msg-a', runId: 'run-1' });
  evict('live-msg-a-reasoning');
  applyRoundDelta(events, { kind: 'tool', tool_call_id: 'call_00_x', name: 'ask_question', args: '{"question":"Pick one"}' }, { messageId: 'msg-a', runId: 'run-1' });
  evict('live-msg-a-tool-call_00_x');
  applyRoundDelta(events, { kind: 'text', text: 'Done.' }, { messageId: 'msg-a', runId: 'run-1' });
  evict('live-msg-a-answer');

  assert.equal(events.filter((event) => event.type === 'tool.run').length, 1, 'no canonical/live ask pair remains');

  const answered = applyAskStatus(events, { tool_call_id: 'call_00_x', answer: 'Yes' }, 'ok');
  assert.equal(answered?.id, 'live-msg-a-tool-call_00_x', 'the live row is the only match for the answer');
  applyRoundDone(events, 'msg-a', { state: 'done' });

  // After commitLiveTurn clears liveEventIds the terminal snapshot folds the
  // persisted rows into the live events in place — canonical ids never remount.
  mergeConversationEvents(events, persisted('ok', 'Yes'));

  const tools = events.filter((event) => event.type === 'tool.run');
  assert.equal(tools.length, 1, 'the answered question settles on one card');
  assert.equal(tools[0].id, 'live-msg-a-tool-call_00_x');
  assert.equal(tools[0].sourceId, 'msg-a-tool-call_00_x', 'the mounted id keeps its persisted source');
  assert.equal(tools[0].output, 'Yes');
  assert.ok(['ok', 'complete'].includes(tools[0].status), 'terminal status survives the merge');
  assert.equal(events.filter((event) => event.type === 'agent.reasoning').length, 1);
  assert.equal(events.filter((event) => event.type === 'agent.message').length, 1);
});

test('queueLiveWidget evicts the mounted canonical twin when its live event appears', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const extract = (name) => {
    const start = app.indexOf(`function ${name}(`);
    assert.ok(start >= 0, `app.js must define ${name}`);
    return app.slice(start, app.indexOf('\n}', start) + 2);
  };
  const dom = new JSDOM(`<section id="stream">
    <x-widget data-event-id="msg-u"></x-widget>
    <x-widget data-event-id="msg-a-reasoning-0"></x-widget>
    <x-widget data-event-id="msg-a-tool-call_00_x"></x-widget>
    <x-widget data-event-id="msg-a-tool-call_00_other"></x-widget>
  </section>`);
  const stream = dom.window.document.querySelector('#stream');
  const liveReasoning = { id: 'live-msg-a-reasoning', messageId: 'msg-a', type: 'agent.reasoning' };
  const liveTool = { id: 'live-msg-a-tool-call_00_x', messageId: 'msg-a', type: 'tool.run', tool: 'ask_question', toolCallId: 'call_00_x', status: 'running' };
  const context = {
    events: [
      { id: 'msg-u', type: 'user.message' },
      { id: 'msg-a-reasoning-0', messageId: 'msg-a', type: 'agent.reasoning' },
      { id: 'msg-a-tool-call_00_x', messageId: 'msg-a', type: 'tool.run', tool: 'ask_question', toolCallId: 'call_00_x', status: 'running' },
      { id: 'msg-a-tool-call_00_other', messageId: 'msg-a', type: 'tool.run', tool: 'exec', toolCallId: 'call_00_other', status: 'running' },
      liveReasoning,
      liveTool,
    ],
    pendingLiveWidgets: new Map(),
    liveEventIds: new Set(),
    liveTurnKey: '',
    liveRenderFrame: 0,
    requestAnimationFrame: () => 1,
    cancelAnimationFrame: () => {},
    flushLiveWidgets: () => {},
    stream,
    canonicalTwins,
    CSS: dom.window.CSS,
  };
  runInNewContext(`${extract('evictCanonicalTwins')}\n\n${extract('queueLiveWidget')}`, context);

  context.queueLiveWidget(liveTool, true);
  assert.equal(stream.querySelector('[data-event-id="msg-a-tool-call_00_x"]'), null, 'the canonical ask card leaves the DOM once its live twin exists');
  assert.ok(stream.querySelector('[data-event-id="msg-a-tool-call_00_other"]'), 'a different tool call keeps its row');
  assert.ok(stream.querySelector('[data-event-id="msg-a-reasoning-0"]'), 'the reasoning twin waits for its own live event');
  assert.ok(context.liveEventIds.has('live-msg-a-tool-call_00_x'));

  context.queueLiveWidget(liveReasoning, true);
  assert.equal(stream.querySelector('[data-event-id="msg-a-reasoning-0"]'), null);
  assert.deepEqual(context.events.map(({ id }) => id),
    ['msg-u', 'msg-a-tool-call_00_other', 'live-msg-a-reasoning', 'live-msg-a-tool-call_00_x'],
    'evicted canonical rows leave the event list without disturbing other messages');
});

test('question-open recovers the pending ask matched by toolCallId when the popover was never shown', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const start = app.indexOf("stream.addEventListener('question-open'");
  assert.ok(start >= 0, 'app.js must handle question-open');
  const end = app.indexOf('\n});', start);
  assert.ok(end > start, 'question-open extraction boundary missing');
  const source = app.slice(start, end + '\n});'.length);

  const pendingB = { run_id: 'run-1', tool_call_id: 'call-b', question: 'Pick one', options: [] };
  for (const scenario of ['recovered', 'kept-current', 'settled', 'rpc-failed']) {
    const calls = { rpc: [], shown: [] };
    const listeners = {};
    let current = scenario === 'kept-current' ? pendingB : null;
    const context = {
      stream: { addEventListener: (name, cb) => { listeners[name] = cb; } },
      askPopover: { current: () => current, show: (ask) => calls.shown.push(ask) },
      activeConversation: { id: 'room-1' },
      rpc: async (method, params) => {
        calls.rpc.push([method, params]);
        if (scenario === 'rpc-failed') throw new Error('backend down');
        return { asks: [{ run_id: 'run-1', tool_call_id: 'call-a', question: 'Other', options: [] }, pendingB] };
      },
    };
    runInNewContext(source, context);
    const toolCallId = scenario === 'settled' ? 'call-settled' : 'call-b';
    await listeners['question-open']({ detail: { event: { toolCallId } } });

    if (scenario === 'kept-current') {
      assert.deepEqual(calls.rpc, [], 'a matching cached popover re-shows without an RPC');
      assert.deepEqual(calls.shown, [pendingB]);
    } else if (scenario === 'recovered') {
      assert.equal(calls.rpc.length, 1, 'a missed pending event is recovered from the backend');
      assert.equal(calls.rpc[0][0], 'agent.ask.pending');
      assert.equal(calls.rpc[0][1]?.conversation_id, 'room-1');
      assert.deepEqual(calls.shown, [pendingB], 'the popover opens the ask for this card');
    } else {
      assert.deepEqual(calls.shown, [], `${scenario}: an answered card never reopens the popover`);
    }
  }

  // An answered historic card stays read-only: its call id is no longer
  // pending, so no match is found and nothing can resubmit or cancel it.
  const calls = { rpc: [], shown: [] };
  const listeners = {};
  runInNewContext(source, {
    stream: { addEventListener: (name, cb) => { listeners[name] = cb; } },
    askPopover: { current: () => null, show: (ask) => calls.shown.push(ask) },
    activeConversation: { id: 'room-1' },
    rpc: async () => { return { asks: [] }; },
  });
  await listeners['question-open']({ detail: { event: { toolCallId: 'call-done' } } });
  assert.deepEqual(calls.shown, [], 'no pending ask means the click is a no-op');
});

test('long transcripts mount a bounded tail and reveal older events in batches', () => {
  assert.deepEqual(transcriptWindow(1_000, 48, 120), { start: 952, end: 1_000, hidden: 952 });
  assert.equal(growVisibleCount(1_000, 48, 32, 120), 80);
  assert.equal(growVisibleCount(1_000, 112, 32, 120), 120);
  assert.equal(appendVisibleCount(1_001, 120, 120), 120, 'streaming cannot grow the mounted window forever');
  assert.deepEqual(
    shiftWindowOlder(1_280, { start: 1_160, end: 1_280 }, 32, 120),
    { start: 1_128, end: 1_248, hidden: 1_128, newer: 32 },
    'a full window pages backward instead of turning the Load older control into a dead end',
  );
});

test('older transcript windows preserve mounted widget identities while adding only the new head', () => {
  const dom = new JSDOM('<section id="stream"><x-widget data-event-id="middle"></x-widget><x-widget data-event-id="tail"></x-widget></section>');
  const stream = dom.window.document.querySelector('#stream');
  const middle = stream.querySelector('[data-event-id="middle"]');
  const tail = stream.querySelector('[data-event-id="tail"]');
  const visible = [{ id: 'older', type: 'agent.message' }, { id: 'middle', type: 'agent.message' }, { id: 'tail', type: 'agent.message' }];

  reconcileVisibleWidgets(stream, visible, () => 'x-widget');

  assert.equal(stream.querySelector('[data-event-id="middle"]'), middle);
  assert.equal(stream.querySelector('[data-event-id="tail"]'), tail);
  assert.deepEqual([...stream.querySelectorAll('[data-event-id]')].map((node) => node.dataset.eventId), ['older', 'middle', 'tail']);
});

test('transcript reconciliation leaves widgets inside the live turn host mounted', () => {
  const dom = new JSDOM('<section id="stream"><x-widget data-event-id="u1"></x-widget><div class="live-turn-host"><x-widget data-event-id="live-tool"></x-widget></div></section>');
  const stream = dom.window.document.querySelector('#stream');
  const host = stream.querySelector('.live-turn-host');
  const live = host.firstElementChild;

  reconcileVisibleWidgets(stream, [{ id: 'u1', type: 'user.message' }, { id: 'a1', type: 'agent.message' }], () => 'x-widget');

  assert.equal(host.firstElementChild, live, 'the live widget keeps its node and shadow state');
  assert.equal(live.isConnected, true);
  assert.deepEqual([...stream.children].map((node) => node.dataset.eventId || node.className), ['u1', 'a1', 'live-turn-host']);
});

test('older-window paging uses identity-preserving reconciliation instead of replacing the stream', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  assert.match(app, /function renderEvents\(\{ forceBottom = false, preserveExisting = false \}/);
  assert.match(app, /renderEvents\(\{ preserveExisting: true \}\)/);
  assert.match(app, /reconcileVisibleWidgets\(stream, visible, eventWidgetTag\)/);
});

test('streaming patches the live widget in place and caps mounted event nodes', () => {
  const dom = new JSDOM('<section id="stream"><x-widget data-event-id="old"></x-widget><x-widget data-event-id="live"></x-widget></section>');
  const stream = dom.window.document.querySelector('#stream');
  const live = stream.lastElementChild;

  upsertVisibleWidget(stream, { id: 'live', type: 'agent.message', markdown: 'next token' }, () => 'x-widget', 2);
  assert.equal(stream.lastElementChild, live, 'an existing stream node is updated rather than replaced');
  assert.equal(live.event.markdown, 'next token');

  upsertVisibleWidget(stream, { id: 'new', type: 'tool.run' }, () => 'x-widget', 2);
  assert.deepEqual([...stream.querySelectorAll('[data-event-id]')].map((node) => node.dataset.eventId), ['live', 'new']);
});

test('live rounds use one stable host instead of appending deltas to the transcript tail', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  assert.match(app, /const liveEventIds = new Set\(\)/);
  assert.match(app, /function ensureLiveTurnHost\(\)/);
  assert.match(app, /function commitLiveTurn\(\)/);
  assert.match(app, /upsertLiveTurnWidget\(stream, ensureLiveTurnHost\(\), event, eventWidgetTag/);
  assert.match(app, /commitLiveTurn\(\);[\s\S]*refreshConversationSnapshot/);
});

test('a committed live round joins the visible tail instead of inventing older history', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const extract = (name) => {
    const start = app.indexOf(`function ${name}(`);
    assert.ok(start >= 0, `app.js must define ${name}`);
    return app.slice(start, app.indexOf('\n}', start) + 2);
  };
  const liveAnswer = { id: 'live-m1-answer', messageId: 'm1', type: 'agent.message', status: 'done' };
  const context = {
    liveTurnHost: null,
    liveEventIds: new Set([liveAnswer.id]),
    anchoredAfterLiveHost: new Set(),
    liveTurnKey: 'm1',
    liveTurnGeneration: 0,
    followState: { pinned: true },
    // appendVisibleCount collapses the window to total when the first message
    // sends — on a fresh room that leaves the tail one row short once the
    // live answer commits into history.
    events: [{ id: 'm1-user', type: 'user.message' }, liveAnswer],
    filterEvents,
    activeFilter: 'all',
    visibleEventCount: 1,
    visibleEventEnd: 1,
    MAX_MOUNTED_EVENTS: 120,
    commitLiveHost: () => {},
  };
  runInNewContext(`${extract('historicalEvents')}\n\n${extract('commitLiveTurn')}`, context);

  assert.equal(transcriptWindow(1, context.visibleEventCount, 120, context.visibleEventEnd).hidden, 0);
  context.commitLiveTurn();
  assert.equal(context.visibleEventEnd, 2, 'the window end follows history once the live row joins it');
  assert.deepEqual(
    transcriptWindow(2, context.visibleEventCount, 120, context.visibleEventEnd),
    { start: 0, end: 2, hidden: 0 },
    'no phantom Load older rows after the first exchange',
  );

  // A detached reader keeps their window: committing below the fold must not
  // move the rows they are reading.
  context.followState.pinned = false;
  context.events.push(
    { id: 'm2-user', type: 'user.message' },
    { id: 'live-m2-answer', messageId: 'm2', type: 'agent.message', status: 'done' },
  );
  context.liveEventIds.add('live-m2-answer');
  context.commitLiveTurn();
  assert.equal(context.visibleEventCount, 2);
  assert.equal(context.visibleEventEnd, 2);
});

test('a live error row re-queued after commit adopts its mounted widget instead of duplicating it', () => {
  const dom = new JSDOM('<section id="stream"><x-widget data-event-id="u1"></x-widget><x-widget data-event-id="live-m1-answer"></x-widget><div class="live-turn-host"></div></section>');
  const stream = dom.window.document.querySelector('#stream');
  const host = stream.querySelector('.live-turn-host');
  const committed = stream.querySelector('[data-event-id="live-m1-answer"]');

  // round.done committed the card to the stream; a late agent.turn.error
  // re-queues the same event id. The row must move into the fresh host, not
  // clone — reload dedups by message id, so live must not render two rows.
  const errorEvent = { id: 'live-m1-answer', messageId: 'm1', type: 'agent.message', status: 'error', error: 'boom' };
  const widget = upsertLiveTurnWidget(stream, host, errorEvent, () => 'x-widget', 120);

  assert.equal(widget, committed, 'the committed row is adopted, keeping its shadow state');
  assert.equal(widget.event.status, 'error');
  assert.equal(widget.parentNode, host);
  assert.equal(stream.querySelectorAll('[data-event-id="live-m1-answer"]').length, 1);
});

test('commitLiveHost keeps a finished round in place above a later steer bubble', () => {
  const dom = new JSDOM('<section id="stream"></section>');
  const { document } = dom.window;
  const stream = document.querySelector('#stream');
  const user = document.createElement('div');
  user.dataset.eventId = 'u1';
  const host = document.createElement('div');
  host.className = 'live-turn-host';
  const tool = document.createElement('div');
  tool.dataset.eventId = 'r1-tool';
  host.append(tool);
  const steer = document.createElement('div');
  steer.dataset.eventId = 'steer';
  stream.append(user, host, steer);

  commitLiveHost(host);

  assert.deepEqual([...stream.querySelectorAll('[data-event-id]')].map((node) => node.dataset.eventId), ['u1', 'r1-tool', 'steer']);
  assert.equal(host.isConnected, false);

  const nextHost = document.createElement('div');
  nextHost.className = 'live-turn-host';
  const nextTool = document.createElement('div');
  nextTool.dataset.eventId = 'r2-tool';
  nextHost.append(nextTool);
  stream.append(nextHost);
  assert.deepEqual([...stream.querySelectorAll('[data-event-id]')].map((node) => node.dataset.eventId), ['u1', 'r1-tool', 'steer', 'r2-tool']);

  const empty = document.createElement('div');
  empty.className = 'live-turn-host';
  stream.append(empty);
  commitLiveHost(empty);
  assert.equal(empty.isConnected, false);
  assert.equal(stream.contains(empty), false);

  assert.doesNotThrow(() => commitLiveHost(null));
});

test('an applied steer mounts below the live host and stays below that round after commit', () => {
  const dom = new JSDOM('<section id="event-stream"></section>');
  const { document } = dom.window;
  const stream = document.querySelector('#event-stream');
  const tag = () => 'x-widget';
  const steer = {
    id: 'msg_97708698afa6731a',
    type: 'user.message',
    content: 'Also reply with the single word STEERED.',
  };

  const user = document.createElement('div');
  user.dataset.eventId = 'u1';
  const host = document.createElement('div');
  host.className = 'live-turn-host';
  const reasoning = document.createElement('div');
  reasoning.dataset.eventId = 'r1-reasoning';
  host.append(reasoning);
  stream.append(user, host);

  mountWidgetAfterLiveHost(stream, host, steer, tag, 48);

  assert.deepEqual(
    [...stream.children].map((node) => node.dataset.eventId || node.className),
    ['u1', 'live-turn-host', 'msg_97708698afa6731a'],
    'before commitLiveHost the applied steer is the next sibling after the live host',
  );
  assert.equal(host.querySelector('[data-event-id="r1-reasoning"]'), reasoning);

  commitLiveHost(host);

  assert.deepEqual(
    [...stream.querySelectorAll('[data-event-id]')].map((node) => node.dataset.eventId),
    ['u1', 'r1-reasoning', 'msg_97708698afa6731a'],
    'after commitLiveHost the finished round stays above the steer bubble',
  );

  const nextHost = document.createElement('div');
  nextHost.className = 'live-turn-host';
  const nextReasoning = document.createElement('div');
  nextReasoning.dataset.eventId = 'r2-reasoning';
  nextHost.append(nextReasoning);
  stream.append(nextHost);
  assert.deepEqual(
    [...stream.children].map((node) => node.dataset.eventId || node.className),
    ['u1', 'r1-reasoning', 'msg_97708698afa6731a', 'live-turn-host'],
    'the next round host mounts after the steer bubble',
  );

  // slow_down: the applied bubble can land before this round's host exists.
  // The host must be inserted above that bubble, not appended below it.
  const late = new JSDOM('<section id="event-stream"></section>').window.document;
  const lateStream = late.querySelector('#event-stream');
  const prior = late.createElement('div');
  prior.dataset.eventId = 'u1';
  lateStream.append(prior);
  const earlySteer = mountWidgetAfterLiveHost(lateStream, null, steer, tag, 48);
  assert.deepEqual(
    [...lateStream.children].map((node) => node.dataset.eventId || node.className),
    ['u1', 'msg_97708698afa6731a'],
  );
  const lateHost = late.createElement('div');
  lateHost.className = 'live-turn-host';
  const lateReasoning = late.createElement('div');
  lateReasoning.dataset.eventId = 'r1-reasoning';
  lateHost.append(lateReasoning);
  attachLiveHost(lateStream, lateHost, earlySteer);
  assert.deepEqual(
    [...lateStream.children].map((node) => node.dataset.eventId || node.className),
    ['u1', 'live-turn-host', 'msg_97708698afa6731a'],
    'before commitLiveHost a late host still sits above the applied steer',
  );
  commitLiveHost(lateHost);
  assert.deepEqual(
    [...lateStream.querySelectorAll('[data-event-id]')].map((node) => node.dataset.eventId),
    ['u1', 'r1-reasoning', 'msg_97708698afa6731a'],
    'after commitLiveHost the late round stays above the steer bubble',
  );
});

test('a queued steer does not mount a transcript bubble before the live host exists', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const submitStart = app.indexOf("messageForm.addEventListener('submit'");
  const submitEnd = app.indexOf('stopTurnButton.addEventListener', submitStart);
  assert.ok(submitStart >= 0 && submitEnd > submitStart);
  const submitBody = app.slice(submitStart, submitEnd);
  const steeringAt = submitBody.indexOf('if (steering)');
  const elseAt = submitBody.indexOf('} else {', steeringAt);
  const clearAt = submitBody.indexOf("composer.value = ''");
  const optimisticAt = submitBody.indexOf('if (!steering)');
  assert.ok(steeringAt >= 0, 'submit distinguishes a queued steer from a new turn');
  assert.ok(elseAt > steeringAt);
  const steerBranch = submitBody.slice(steeringAt, elseAt);
  assert.match(steerBranch, /agent\.turns\.steer/);
  assert.doesNotMatch(steerBranch, /upsertVisibleWidget/, 'a queued steer must not append a user-message widget');
  assert.doesNotMatch(steerBranch, /userEvent/);
  assert.ok(clearAt > elseAt, 'the composer still clears after a successful steer RPC');
  assert.ok(optimisticAt > clearAt, 'only agent.turns.start mounts the optimistic user bubble');
  assert.match(submitBody.slice(optimisticAt), /upsertVisibleWidget\(stream, mountedUserEvent/);
  assert.match(app, /historicalEvents\(\) \{\s*return events\.filter\(\(event\) => !liveEventIds\.has\(event\?\.id\) && !anchoredAfterLiveHost\.has\(event\?\.id\)\)/);
  assert.match(app, /mountWidgetAfterLiveHost\(stream, liveTurnHost/);
  assert.match(app, /attachLiveHost\(stream, liveTurnHost/);
  assert.match(app, /on\('agent\.steer\.cancelled'/);
  assert.match(app, /composer\.value = payload\.text/);
});

test('each applied steer keeps its own round boundary when snapshots merge several', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  // Every applied event locks the generation that was live at its own
  // boundary. Locks queue in applied order so a superseded or merged refresh
  // still places steer A at boundary 1 and steer B at boundary 2.
  const appliedStart = app.indexOf("on('agent.steer.applied'");
  const appliedEnd = app.indexOf("on('agent.steer.cancelled'", appliedStart);
  const appliedBody = app.slice(appliedStart, appliedEnd);
  assert.match(appliedBody, /steerAnchorGenerations\.push\(boundaryGeneration\)/);
  // The applied event carries the persisted message id (SteerEvent
  // message_id), so the refresh narrows to exactly that row instead of
  // merging the full transcript and hoping the steer is the only new user
  // message.
  assert.match(appliedBody, /messageID: payload\.message_id/);

  const placeStart = app.indexOf('function placeAppliedSteerWidget(');
  const placeEnd = app.indexOf('\nfunction ', placeStart + 1);
  const placeBody = app.slice(placeStart, placeEnd);
  assert.match(placeBody, /event\?\.steer && steerAnchorGenerations\.length\s*\?\s*steerAnchorGenerations\.shift\(\)\s*:\s*steerAnchorGeneration/);

  const resetStart = app.indexOf('function resetLiveTurnHost()');
  const resetBody = app.slice(resetStart, app.indexOf('\nfunction ', resetStart + 1));
  assert.match(resetBody, /steerAnchorGenerations\.length = 0/);

  // A plain snapshot that merges a steer-flagged message still anchors it —
  // the applied refresh may have been superseded by an SSE recovery or a
  // compaction refresh.
  const refreshStart = app.indexOf('async function refreshConversationSnapshot(');
  const refreshBody = app.slice(refreshStart, app.indexOf('\nasync function ', refreshStart + 1));
  assert.match(refreshBody, /event\?\.steer/);
});

test('an applied steer locks the committed round before a next round that has no live content yet', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const appliedStart = app.indexOf("on('agent.steer.applied'");
  const appliedEnd = app.indexOf("on('agent.steer.cancelled'", appliedStart);
  const appliedBody = app.slice(appliedStart, appliedEnd);
  assert.match(
    appliedBody,
    /const boundaryGeneration = liveEventIds\.size \? liveTurnGeneration : liveTurnGeneration - 1;/,
    'the steer precedes the next round, whose host has not opened any live content when the applied event arrives',
  );
  assert.match(appliedBody, /steerAnchorGeneration = boundaryGeneration;/);
  assert.doesNotMatch(appliedBody, /steerAnchorGeneration = liveTurnGeneration;/, 'a commit that already opened the next round must not adopt the steer into it');
});

test('applied steers keep applied order when several mount after the same live host', () => {
  const dom = new JSDOM('<section id="event-stream"></section>');
  const { document } = dom.window;
  const stream = document.querySelector('#event-stream');
  const tag = () => 'x-widget';
  const steerA = { id: 'msg_steer_a', type: 'user.message', content: 'first' };
  const steerB = { id: 'msg_steer_b', type: 'user.message', content: 'second' };

  const user = document.createElement('div');
  user.dataset.eventId = 'u1';
  const host = document.createElement('div');
  host.className = 'live-turn-host';
  const reasoning = document.createElement('div');
  reasoning.dataset.eventId = 'r1-reasoning';
  host.append(reasoning);
  stream.append(user, host);

  mountWidgetAfterLiveHost(stream, host, steerA, tag, 48);
  mountWidgetAfterLiveHost(stream, host, steerB, tag, 48);

  assert.deepEqual(
    [...stream.children].map((node) => node.dataset.eventId || node.className),
    ['u1', 'live-turn-host', 'msg_steer_a', 'msg_steer_b'],
    'a second applied steer mounts below the first, not between it and the host',
  );
});

test('a steer queued before the host exists still orders ahead of a later applied steer', () => {
  const dom = new JSDOM('<section id="event-stream"></section>');
  const { document } = dom.window;
  const stream = document.querySelector('#event-stream');
  const tag = () => 'x-widget';
  const steerA = { id: 'msg_steer_a', type: 'user.message', content: 'first' };
  const steerB = { id: 'msg_steer_b', type: 'user.message', content: 'second' };

  const user = document.createElement('div');
  user.dataset.eventId = 'u1';
  stream.append(user);

  // Slow down: steer A lands while no live host is connected; the host then
  // attaches above it. Steer B applies after the host exists and must join
  // below steer A, preserving backend applied order.
  const earlySteer = mountWidgetAfterLiveHost(stream, null, steerA, tag, 48);
  const host = document.createElement('div');
  host.className = 'live-turn-host';
  const reasoning = document.createElement('div');
  reasoning.dataset.eventId = 'r1-reasoning';
  host.append(reasoning);
  attachLiveHost(stream, host, earlySteer);
  mountWidgetAfterLiveHost(stream, host, steerB, tag, 48);

  assert.deepEqual(
    [...stream.children].map((node) => node.dataset.eventId || node.className),
    ['u1', 'live-turn-host', 'msg_steer_a', 'msg_steer_b'],
    'the later applied steer keeps applied order behind the early one',
  );
});

test('messageEvents keeps the steer flag so snapshots can anchor applied steers', () => {
  const [event] = messageEvents([{
    id: 'msg_steer', role: 'user', content: 'go', steer: true,
    created_at: '2026-10-14T01:00:00Z', attachments: [],
  }]);
  assert.equal(event.type, 'user.message');
  assert.equal(event.steer, true, 'the persisted steer marker survives into the widget event');
});

test('a new message id commits the live host in place before the next round opens', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const openStart = app.indexOf('function openRoundStream(');
  const openNext = app.slice(openStart + 1).search(/\n(?:async )?function /);
  const openBody = app.slice(openStart, openStart + 1 + openNext);
  assert.match(openBody, /liveTurnKey && round\.message_id && liveTurnKey !== round\.message_id/);
  assert.match(
    openBody,
    /activeRoundStream\?\.close\(\);\s*flushLiveWidgets\(\);\s*if \(liveTurnKey && round\.message_id && liveTurnKey !== round\.message_id\) commitLiveTurn\(\)/,
  );

  const commitStart = app.indexOf('function commitLiveTurn()');
  const commitBody = app.slice(commitStart, app.indexOf('\nfunction ', commitStart + 1));
  assert.match(commitBody, /if \(liveTurnHost\?\.isConnected\) commitLiveHost\(liveTurnHost\)/);
  assert.doesNotMatch(commitBody, /stream\.append\(child\)/);
});

test('RPC model catalog is grouped by configured provider and keeps stable wire values', () => {
  const data = modelOptions([
    { id: 'gpt-x', provider_id: 'p1', provider_name: 'OpenRouter', display_name: 'GPT X' },
    { id: 'claude-y', provider_id: 'p1', provider_name: 'OpenRouter', display_name: 'Claude Y' },
    { id: 'codex-z', provider_id: 'p2', provider_name: 'Codex', display_name: 'Codex Z', kind: 'chat' },
    { id: 'image-only', provider_id: 'p1', provider_name: 'OpenRouter', kind: 'image' },
  ]);

  assert.deepEqual(data.map(group => ({ ...group, options: group.options.map(({ html, ...option }) => option) })), [
    { label: 'OpenRouter', options: [{ text: 'GPT X', value: 'p1:gpt-x' }, { text: 'Claude Y', value: 'p1:claude-y' }] },
    { label: 'Codex', options: [{ text: 'Codex Z', value: 'p2:codex-z' }] },
  ]);
});

test('conversation prototype exposes compact, labelled work surfaces', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html);
  const { document } = dom.window;

  assert.equal(document.querySelector('main')?.getAttribute('aria-label'), 'Conversation workspace');
  assert.equal(document.querySelector('#room-drawer-toggle')?.getAttribute('aria-controls'), 'room-drawer');
  assert.equal(document.querySelector('#context-dock-toggle')?.getAttribute('aria-controls'), 'context-dock');
  assert.equal(document.querySelector('#context-dock-toggle')?.getAttribute('aria-label'), 'Toggle conversation dock');
  assert.equal(document.querySelector('#context-dock')?.getAttribute('aria-label'), 'Conversation context dock');
  assert.equal(document.querySelector('.room-heading span'), null, 'the topbar does not repeat the NusaShell brand');
  assert.doesNotMatch(document.querySelector('.room-heading')?.textContent || '', /NusaShell/);
  assert.equal(document.querySelectorAll('.dock-tabs [role="tab"]').length, 3);
  assert.equal(document.querySelectorAll('.learning-tabs [role="tab"]').length, 4);
  assert.ok(document.querySelector('[data-filter="activity"]'));
  assert.equal(document.querySelector('[data-filter="tool"]'), null);
  assert.equal(document.querySelector('[data-filter="work"]'), null);
  assert.ok(document.querySelector('#panel-subagents'));
  assert.equal(document.querySelectorAll('select[data-composer-select]').length, 3);
  assert.equal(document.querySelectorAll('#model-config-popover select[data-composer-select]').length, 3);
  assert.equal(document.querySelectorAll('[data-composer-picker]').length, 0);
  assert.ok(document.querySelector('#attachment-input'));
  assert.ok(document.querySelector('#attachment-button'));
  assert.equal(document.querySelectorAll('select.slim-select').length, 4);
  assert.ok(document.querySelector('#logs-level'));
  assert.equal(document.querySelector('#model-config-trigger')?.getAttribute('aria-controls'), 'model-config-popover');
  assert.equal(document.querySelector('#model-config-trigger')?.getAttribute('aria-expanded'), 'false');
  assert.equal(document.querySelector('#model-config-popover')?.hidden, true);
  assert.equal(document.querySelector('.activity-bar'), null, 'prototype does not reserve space for dead global controls');
  assert.equal(document.querySelector('#tab-files'), null, 'file previews are opened from event actions, not a redundant dock tab');
  assert.equal(document.querySelector('#panel-files'), null, 'the dock keeps only actionable task, subagent, and context surfaces');
  assert.ok(document.querySelector('#message-input'));
  assert.ok(document.querySelector('#event-stream'));
});

test('room activity normalizes active backend statuses for the sidebar indicator', () => {
  assert.equal(normalizeConversationStatus('in progress'), 'in_progress');
  assert.equal(normalizeConversationStatus('WORKING'), 'working');
  assert.equal(isConversationActive({ status: 'running' }), true);
  assert.equal(isConversationActive({ status: 'in_progress' }), true);
  assert.equal(isConversationActive({ status: 'queued' }), true);
  assert.equal(isConversationActive({ status: 'idle' }), false);
});

test('conversation search filters room metadata without mutating the source list', () => {
  const rooms = [
    { id: 'one', title: 'Review frontend changes', status: 'idle', model: 'gpt-5.6-luna' },
    { id: 'two', title: 'Release checklist', status: 'in_progress', model: 'deepseek-v4' },
    { id: 'three', title: 'Provider audit', status: 'complete', model: 'claude-sonnet' },
  ];

  assert.deepEqual(filterConversations(rooms, '  RELEASE '), [rooms[1]]);
  assert.deepEqual(filterConversations(rooms, 'IN PROGRESS'), [rooms[1]]);
  assert.deepEqual(filterConversations(rooms, 'missing'), []);
  assert.deepEqual(filterConversations(rooms, ''), rooms);
  assert.equal(rooms.length, 3, 'search must not remove rooms from the authoritative list');
});

test('room list exposes a live active-agent marker and updates it for any room lifecycle event', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('shell.css', root), 'utf8');

  assert.match(app, /room-activity/);
  assert.match(app, /function syncRoomActivity\(/);
  assert.match(app, /syncRoomActivity\(payload\?\.conversation_id, 'running'\)/);
  assert.match(app, /syncRoomActivity\(payload\?\.conversation_id, 'idle'\)/);
  assert.match(css, /\.room-activity\s*\{[^}]*border-radius:\s*50%;[^}]*animation:/s);
  assert.match(css, /\.room-item\.is-running\s+\.room-activity/);
  assert.match(css, /prefers-reduced-motion/);
});

test('room search is stateful and provides an explicit empty result', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const { document } = new JSDOM(html).window;

  assert.equal(document.querySelector('#room-search-input')?.getAttribute('aria-controls'), 'room-list');
  assert.equal(document.querySelector('#room-list')?.getAttribute('role'), 'list');
  assert.match(app, /filterConversations\(conversations, roomSearchQuery\)/);
  assert.match(app, /room-list-empty/);
  assert.match(app, /room-search-input[\s\S]*addEventListener\('input'/);
  assert.match(app, /event\.metaKey \|\| event\.ctrlKey/);
});

test('prototype route defaults to Home when the URL has no known hash', async () => {
  const { normalizePrototypeRoute } = await import(`../page/conversation/route-state.js?test=${Date.now()}`);

  assert.equal(normalizePrototypeRoute(''), 'home');
  assert.equal(normalizePrototypeRoute('#unknown'), 'home');
  assert.equal(normalizePrototypeRoute('#agent'), 'agent');
  assert.equal(normalizePrototypeRoute('PROVIDERS'), 'providers');
  assert.equal(normalizePrototypeRoute('#settings'), 'settings');
});

test('initial room selection keeps the newest backend room even when it is running', async () => {
  const { selectInitialConversation } = await import(`../page/conversation/room-state.js?test=${Date.now()}`);
  const newest = { id: 'newest', status: 'running', message_count: 240 };
  const older = { id: 'older', status: 'idle', message_count: 4 };

  assert.equal(selectInitialConversation([newest, older]), newest);
  assert.equal(selectInitialConversation([]), null);
});

test('composer keeps keyboard shortcuts without rendering a send hint', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const composer = await readFile(new URL('page/conversation/composer.js', root), 'utf8');
  const html = await readFile(new URL('index.html', root), 'utf8');
  const { document } = new JSDOM(html).window;

  assert.ok(document.querySelector('#message-form'));
  assert.equal(document.querySelector('.send-hint'), null);
  assert.match(app, /bindComposerSendKeys\(composer, messageForm\)/);
  assert.match(composer, /addEventListener\('keydown'/);
  assert.match(composer, /event\.key\.toLowerCase\(\)\s*!==\s*['"]enter['"]/);
  assert.match(composer, /event\.metaKey\s*\|\|\s*event\.ctrlKey/);
  assert.match(composer, /event\.preventDefault\(\)/);
  assert.match(composer, /form\.requestSubmit\(\)/);
  assert.doesNotMatch(app, /send-hint/);
  assert.doesNotMatch(composer, /send-hint/);
});

test('Alt+Enter inserts one newline and does not submit a steer or a send', async () => {
  const { bindComposerSendKeys } = await import(`../page/conversation/composer.js?keys=${Date.now()}`);
  const dom = new JSDOM('<form id="message-form" data-turn-mode="steer"><textarea id="message-input">hello</textarea><button class="send-button" type="submit"></button></form>');
  const { window } = dom;
  const form = window.document.querySelector('#message-form');
  const composer = window.document.querySelector('#message-input');
  composer.setSelectionRange(composer.value.length, composer.value.length);
  const submits = [];
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    submits.push(form.dataset.turnMode);
  });
  bindComposerSendKeys(composer, form);
  let inputs = 0;
  composer.addEventListener('input', () => { inputs += 1; });

  const alt = new window.KeyboardEvent('keydown', { key: 'Enter', altKey: true, bubbles: true, cancelable: true });
  composer.dispatchEvent(alt);

  assert.equal(alt.defaultPrevented, true, 'Alt+Enter must not fall through to a second form submit');
  assert.equal(composer.value, 'hello\n');
  assert.equal(inputs, 1, 'the newline still resizes through the composer input listener');
  assert.deepEqual(submits, []);

  composer.value = 'hello';
  composer.setSelectionRange(2, 2);
  composer.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Enter', altKey: true, bubbles: true, cancelable: true }));
  assert.equal(composer.value, 'he\nllo');
  assert.deepEqual(submits, []);

  for (const init of [{}, { shiftKey: true }]) {
    composer.value = 'hello';
    const plain = new window.KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true, ...init });
    composer.dispatchEvent(plain);
    assert.equal(plain.defaultPrevented, false);
  }
  assert.deepEqual(submits, [], 'Enter and Shift+Enter stay browser newlines');

  composer.value = 'hello';
  composer.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true, cancelable: true }));
  composer.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Enter', metaKey: true, bubbles: true, cancelable: true }));
  assert.deepEqual(submits, ['steer', 'steer'], 'Ctrl/Cmd+Enter still submits the shared steer and send form once per key');
});

test('a send snapshot adopts the optimistic row by message id even with a trailing newline', () => {
  const text = '"Sisa slice yang masih terbuka " ini yang belum kita kerjakan kan?';
  const sentAt = Date.parse('2026-10-10T16:54:08.000Z');
  const persisted = messageEvents([{
    id: 'msg_3ccc97292984a14b',
    role: 'user',
    content: text,
    created_at: '2026-10-10T23:54:08+07:00',
  }]);
  const optimistic = {
    id: 'msg_3ccc97292984a14b',
    type: 'user.message',
    content: `${text}\n`,
    attachments: [],
    optimistic: true,
    createdAt: new Date(sentAt).toISOString(),
  };

  const snapshotFirst = persisted.map((event) => ({ ...event }));
  mergeConversationEvents(snapshotFirst, [optimistic]);
  assert.equal(snapshotFirst.length, 1, 'the raced row is adopted instead of painted twice');
  assert.equal(snapshotFirst[0].id, 'msg_3ccc97292984a14b');
  assert.equal(snapshotFirst[0].optimistic, false);

  const optimisticFirst = [];
  mergeConversationEvents(optimisticFirst, [optimistic]);
  mergeConversationEvents(optimisticFirst, persisted);
  assert.equal(optimisticFirst.length, 1);
  assert.equal(optimisticFirst[0].id, 'msg_3ccc97292984a14b');
  assert.equal(optimisticFirst[0].content.trim(), text);
});

test('new chat is a persistent pencil item directly below conversation search', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const search = document.querySelector('.room-search');
  const newChat = document.querySelector('#new-chat');

  assert.ok(newChat, 'new chat action should be present in the room drawer');
  assert.equal(newChat.tagName, 'BUTTON');
  assert.equal(newChat.type, 'button');
  assert.equal(newChat.getAttribute('aria-label'), 'New chat');
  assert.ok(newChat.querySelector('[data-lucide="pencil"]'));
  assert.equal(search?.nextElementSibling, newChat, 'new chat stays directly below search');
  assert.equal(newChat.nextElementSibling?.id, 'room-list');
  assert.match(app, /newChatButton\.addEventListener\('click'/);
  assert.match(app, /startNewChat\(\)/);
  assert.match(app, /conversationKey\s*=\s*newConversationKey\(\)/);
  assert.match(app, /conversation_key:\s*draftKey/);
  assert.doesNotMatch(app, /agent\.conversations\.create/);
});

test('conversation rows expose a discoverable delete action', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('shell.css', root), 'utf8');
  const menu = await readFile(new URL('page/conversation/room-menu.js', root), 'utf8');

  // Rename/Delete are discoverable through the shared action menu: the row's
  // ellipsis affordance and right-click both open it.
  assert.match(app, /openRoomActionsMenu\(/);
  assert.match(app, /ellipsis-vertical/);
  assert.match(app, /aria-haspopup="menu"/);
  assert.match(app, /'contextmenu'/);
  assert.match(app, /\{\s*id:\s*'rename',\s*label:\s*'Rename'/);
  assert.match(app, /\{\s*id:\s*'delete',\s*label:\s*'Delete'.*danger:\s*true/s);
  assert.doesNotMatch(app, /room-delete/);
  assert.doesNotMatch(css, /\.room-delete|\.room-rename\b/);
  // The delete path itself is unchanged by the menu move.
  assert.match(app, /agent\.conversations\.delete/);
  assert.match(app, /confirmDialog\(/);
  assert.match(app, /refreshConversationList\(/);
  assert.doesNotMatch(app, /if \(wasActive\) clearDeletedConversationView\(\);/);
  assert.match(app, /function invalidateDeletedConversationLoad\(\)/);
  assert.match(app, /if \(wasActive\) invalidateDeletedConversationLoad\(\);/);
  // The affordance stays visible on row hover/focus and while its menu is
  // open, and the danger item reads as destructive in the popup.
  assert.match(css, /\.room-item:hover \.room-actions/);
  assert.match(css, /\.room-actions:focus-visible/);
  assert.match(css, /\.room-actions\[aria-expanded="true"\]/);
  assert.match(css, /\.room-action-menu-item\.is-danger/);
  assert.match(menu, /role",\s*"menu"|role',\s*'menu'/);
  assert.match(menu, /role.,\s*.menuitem./);
});

async function conversationDeletionHarness(rpc, { active = true, openConversation = async () => {} } = {}) {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const extract = (name) => {
    const start = app.indexOf(`async function ${name}(`);
    assert.ok(start >= 0);
    return app.slice(start, app.indexOf('\n}', start) + 2);
  };
  const renders = [];
  const toasts = [];
  const context = {
    conversations: [{ id: 'deleted', title: 'Deleted room' }, { id: 'remaining', title: 'Remaining room' }],
    activeConversation: active ? { id: 'deleted' } : { id: 'remaining' },
    conversationListLoadToken: 0,
    steerByRoom: new Map(),
    confirmDialog: async () => true,
    rpc,
    openConversation,
    console: { warn() {} },
    toast: (...args) => toasts.push(args),
    renderRooms: () => renders.push(context.conversations.map(({ id }) => id)),
    clearDeletedConversationView: () => { context.activeConversation = null; },
    invalidateDeletedConversationLoad: () => {},
  };
  runInNewContext(['deleteConversation', 'refreshConversationRooms', 'refreshConversationList'].map(extract).join('\n'), context);
  return { context, renders, toasts };
}

for (const active of [true, false]) {
  test(`successful deletion removes the ${active ? 'active' : 'inactive'} row even when the list refresh fails`, async () => {
    const { context, renders, toasts } = await conversationDeletionHarness(async (method) => {
      if (method === 'agent.conversations.delete') return { ok: true };
      throw new Error('List unavailable');
    }, { active });
    await context.deleteConversation('deleted');
    assert.deepEqual(context.conversations.map(({ id }) => id), ['remaining']);
    assert.ok(renders.length > 0, 'delete success immediately renders the updated sidebar');
    assert.deepEqual(renders.at(-1), ['remaining']);
    assert.ok(toasts.some(([message, kind]) => message === 'Conversation deleted' && kind === 'success'));
  });
}

test('a room list requested before deletion cannot restore the deleted row', async () => {
  let resolveOldList;
  let calls = 0;
  const { context } = await conversationDeletionHarness(async (method) => {
    if (method === 'agent.conversations.delete') return { ok: true };
    if (++calls === 1) return new Promise((resolve) => { resolveOldList = resolve; });
    return { conversations: [{ id: 'remaining' }] };
  });
  const oldRefresh = context.refreshConversationRooms();
  await context.deleteConversation('deleted');
  resolveOldList({ conversations: [{ id: 'deleted' }, { id: 'remaining' }] });
  await oldRefresh;
  assert.deepEqual(Array.from(context.conversations, ({ id }) => id), ['remaining']);
});

test('failure opening the next room does not report a successful delete as failed', async () => {
  const { context, toasts } = await conversationDeletionHarness(async (method) => {
    if (method === 'agent.conversations.delete') return { ok: true };
    return { conversations: [{ id: 'remaining' }] };
  }, { openConversation: async () => { throw new Error('Snapshot unavailable'); } });
  await context.deleteConversation('deleted');
  assert.ok(toasts.some(([message, kind]) => message === 'Conversation deleted' && kind === 'success'));
  assert.equal(toasts.some(([message]) => message === 'Snapshot unavailable'), false);
});

test('a rejected delete retains the room and reports the backend error', async () => {
  const { context, renders, toasts } = await conversationDeletionHarness(async () => { throw new Error('Delete rejected'); });
  await context.deleteConversation('deleted');
  assert.deepEqual(context.conversations.map(({ id }) => id), ['deleted', 'remaining']);
  assert.deepEqual(renders, []);
  assert.deepEqual(toasts, [['Delete rejected', 'error']]);
});

test('tasks and context dock expose the todo brief, in-progress state, and room identity', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('dock.css', root), 'utf8');

  assert.match(app, /liveTodoBrief/);
  assert.match(app, /renderMarkdownBody\(brief/);
  assert.match(app, /in_progress/);
  assert.match(app, /applyTodoSnapshot\(payload, \{ preserveBrief: true \}\)/);
  assert.match(app, /Room ID/);
  assert.match(app, /context-room-id/);
  assert.match(app, /Tool calls/);
  assert.match(app, /context-tool-count/);
  assert.doesNotMatch(app, /<span class="context-key">Status<\/span>/);
  assert.doesNotMatch(app, /<span class="context-key">Workspace<\/span>/);
  assert.doesNotMatch(app, /<span class="context-key">Model<\/span>/);
  assert.doesNotMatch(app, /renderFilesDock/);
  assert.doesNotMatch(app, /selectDockTab\('files'\)/);
  assert.match(css, /\.dock-task\[data-status="in_progress"\]/);
  assert.match(css, /\.dock-task\[data-status="in_progress"\] \.dock-check \{[^}]*border-top-color:\s*var\(--lagoon\);[^}]*animation:\s*dock-task-spin/s);
  assert.match(css, /@keyframes dock-task-spin/);
  assert.doesNotMatch(css, /\.dock-task\[data-status="in_progress"\] \.dock-check \{[^}]*linear-gradient/s,
    'in-progress state must read as a spinner, not a static half-filled box');
  assert.match(css, /\.context-room-id/);
});

test('conversation prototype keeps global navigation separate from the room drawer', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('shell.css', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const nav = document.querySelector('#global-nav');

  assert.ok(nav);
  assert.equal(nav.getAttribute('aria-label'), 'NusaShell navigation');
  assert.deepEqual([...nav.querySelectorAll('[data-global-route]')].map((item) => item.dataset.globalRoute), [
    'home', 'agent', 'skills', 'learning', 'automation', 'plugins', 'providers', 'logs', 'telemetry', 'settings',
  ]);
  assert.equal(document.querySelector('#global-nav-toggle')?.getAttribute('aria-controls'), 'global-nav');
  assert.equal(document.querySelector('#global-nav-expand')?.getAttribute('aria-controls'), 'global-nav');
  assert.equal(document.querySelector('[data-global-route="settings"]')?.getAttribute('href'), '#settings');
  assert.equal(document.querySelector('#global-nav-pet')?.getAttribute('href'), '#settings');
  assert.equal(document.querySelector('#global-nav-pet')?.hasAttribute('data-global-route'), false);
  assert.equal(document.querySelector('#global-nav-pet')?.getAttribute('aria-label'), 'Pet');
  assert.equal(document.querySelector('#global-nav-pet [data-lucide="paw-print"]') !== null, true);
  const settingsView = document.querySelector('#settings-view');
  assert.ok(settingsView);
  assert.equal(settingsView.hidden, true);
  assert.equal(document.querySelector('.shell')?.contains(document.querySelector('#pairing-approval-overlay')), false);
  assert.equal(document.body.contains(document.querySelector('#pairing-approval-overlay')), true);
  for (const id of ['settings-remote-access-enabled', 'pairing-panel', 'pairing-create-btn', 'pairing-approve-btn', 'pairing-qr-img', 'pairing-host-status']) {
    assert.ok(document.getElementById(id), id);
  }
  const pairingGate = document.getElementById('pairing-gate');
  assert.ok(pairingGate, 'the production pairing gate is carried over');
  assert.equal(pairingGate.hidden, true, 'the gate stays hidden while pairing is not required');
  assert.equal(document.body.contains(pairingGate), true);
  assert.equal(document.querySelector('.shell')?.contains(pairingGate), false);
  assert.match(app, /function setGlobalNavOpen\(/);
  assert.match(app, /function setGlobalNavExpanded\(/);
  assert.match(app, /global-nav-expanded/);
  assert.match(css, /grid-template: 50px minmax\(0, 1fr\) \/ 62px 244px minmax\(420px, 1fr\) 306px/);
  assert.match(css, /\.global-nav\[hidden\] \{ display: none; \}/);
  assert.match(css, /\.shell\.global-nav-expanded/);
});

// Top-level @media blocks with brace matching — enough to inspect which band
// a shell grid rule lives in without pulling in a CSS parser.
function mediaBlocks(css) {
  const blocks = [];
  const re = /@media[^{]+\{/g;
  let match;
  while ((match = re.exec(css))) {
    let depth = 1;
    let i = re.lastIndex;
    while (i < css.length && depth > 0) {
      if (css[i] === '{') depth += 1;
      else if (css[i] === '}') depth -= 1;
      i += 1;
    }
    blocks.push({
      prelude: match[0].replace(/^@media/, '').replace(/\{$/, '').trim(),
      body: css.slice(re.lastIndex, i - 1),
    });
    re.lastIndex = i;
  }
  return blocks;
}

// Pure `.shell.<view>-active.global-nav-expanded` rules (never the
// `global-nav-closed` combos) from media bodies and the top level.
function expandedNavRules(css, view) {
  const re = new RegExp(`\\.shell\\.${view}-active\\.global-nav-expanded\\s*\\{[^}]*\\}`, 'g');
  const rules = [];
  for (const { prelude, body } of mediaBlocks(css)) {
    for (const m of body.matchAll(re)) rules.push({ prelude, rule: m[0] });
  }
  for (const m of css.matchAll(new RegExp(`^\\.shell\\.${view}-active\\.global-nav-expanded\\s*\\{[^}]*\\}`, 'gm'))) {
    rules.push({ prelude: '(top level)', rule: m[0] });
  }
  return rules;
}

const NAV_VIEW_SHEETS = {
  home: 'home.css',
  skills: 'skills.css',
  learning: 'learning.css',
  automation: 'automation.css',
  plugins: 'plugins.css',
  providers: 'providers.css',
  logs: 'logs.css',
  telemetry: 'telemetry.css',
  settings: 'settings.css',
};

test('expanded navigation rail widens its column on every routed surface', async () => {
  // Regression: per-view sheets kept the rail at 62px inside their mid-width
  // band even while .global-nav-expanded showed labels — expansion never
  // pushed the page sideways and labels clipped inside the icon rail.
  // Wherever the rail is in flow, expansion must allocate the 196px nav
  // track; the only exceptions are nav-closed combos and the <=720px
  // single-column mobile shell where the rail is an overlay.
  for (const [view, file] of Object.entries(NAV_VIEW_SHEETS)) {
    const css = await readFile(new URL(file, root), 'utf8');
    const rules = expandedNavRules(css, view);
    assert.ok(rules.length >= 2, `${file} keeps expanded rail rules at both wide and narrow bands`);
    for (const { prelude, rule } of rules) {
      assert.match(rule, /grid-template-columns:\s*(196px 0 minmax\(0, 1fr\)|minmax\(0, 1fr\)\s*;)/,
        `${file} expanded rail must widen to 196px or collapse to the <=720px overlay shell (${prelude}: ${rule})`);
    }
  }
});

test('per-view single-column shell defers to the shared 720px nav overlay breakpoint', async () => {
  // The rail only leaves flow at <=720px. A *-active sheet that switches the
  // shell to one column above that buries the still-in-flow rail under its
  // own page — the rail was unreachable at 721-850px (settings/telemetry)
  // and 721-760px (logs/learning/automation/plugins/providers).
  for (const [view, file] of Object.entries(NAV_VIEW_SHEETS)) {
    const css = await readFile(new URL(file, root), 'utf8');
    const singleCol = [];
    for (const { prelude, body } of mediaBlocks(css)) {
      const rules = body.match(new RegExp(`\\.shell\\.${view}-active[^{]*\\{[^}]*grid-template-columns:\\s*minmax\\(0,\\s*1fr\\)\\s*;`, 'g')) || [];
      for (const rule of rules) singleCol.push(prelude);
    }
    assert.ok(singleCol.length > 0, `${file} keeps a single-column shell for the overlay breakpoint`);
    for (const prelude of singleCol) {
      assert.match(prelude, /max-width:\s*720px/,
        `${file} single-column shell must wait for <=720px, not ${prelude}`);
    }
  }
});

test('experimental prototype keeps Home local with an accessible MCP launcher surface', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('home.css', root), 'utf8');
  const { document } = new JSDOM(html).window;

  const home = document.querySelector('#home-view');
  assert.ok(home, 'the experimental page should include a Home surface');
  assert.equal(home?.getAttribute('aria-label'), 'MCP launcher');
  assert.equal(home?.hidden, false, 'Home is the default surface');
  assert.equal(document.querySelector('#conversation-surface')?.hidden, true, 'conversation waits for an explicit Agent route');
  assert.ok(document.querySelector('#home-search'));
  assert.equal(document.querySelector('#home-search')?.type, 'search');
  assert.equal(document.querySelector('#home-search')?.getAttribute('aria-controls'), 'home-app-grid');
  assert.equal(document.querySelector('#home-app-grid')?.getAttribute('aria-label'), 'MCP apps');
  assert.equal(document.querySelectorAll('[data-home-route]').length, 0);
  assert.doesNotMatch(home?.textContent || '', /Pinned surfaces|Workspace shortcuts|Navigate/);
  assert.equal(document.querySelector('[data-global-route="home"]')?.getAttribute('href'), '#home');
  assert.equal(document.querySelector('[data-global-route="agent"]')?.getAttribute('href'), '#agent');
  assert.equal(document.querySelector('[data-global-route="home"]')?.classList.contains('is-active'), true);
  assert.equal(document.querySelector('[data-global-route="agent"]')?.classList.contains('is-active'), false);
  assert.equal(document.querySelector('#conversation-surface')?.querySelector('#event-stream') !== null, true);

  assert.match(app, /function setHomeSurface\(/);
  assert.match(app, /location\.hash/);
  assert.match(app, /home-active/);
  assert.match(app, /loadHomeMCPApps\(/);
  assert.match(app, /renderHomeMCPApps\(/);
  assert.match(app, /rpc\('plugin\.list'\)/);
  assert.match(app, /homeMCPAppsWithUI\(/);
  assert.match(app, /homePluginIconSource\(/);
  assert.match(app, /filterHomeMCPApps\(/);
  assert.match(app, /markHomeMCPConnected\(/);
  assert.match(app, /syncHomeMCPCardStatus\(/);
  assert.match(css, /\.home-view\s*\{/);
  assert.match(css, /background-image:\s*url\(['"]?\.\/assets\/topography\.svg/);
  assert.match(css, /\.home-mcp-card\s*\{/);
  assert.match(css, /\.home-mcp-icon\s*\{/);
  assert.match(css, /\.home-mcp-glyph\s*\{/);
  assert.match(css, /\.shell\.home-active/);
  assert.match(css, /prefers-reduced-motion/);
});

test('experimental Home centers only the search and MCP grid', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const css = await readFile(new URL('home.css', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const home = document.querySelector('#home-view');

  assert.ok(home?.querySelector('.home-launcher'));
  assert.equal(home?.querySelector('.home-hero'), null);
  assert.equal(home?.querySelector('h1'), null);
  assert.equal(home?.querySelector('.home-section-heading'), null);
  assert.equal(home?.querySelector('#home-search')?.closest('.home-launcher') !== null, true);
  assert.equal(home?.querySelector('#home-app-grid')?.closest('.home-launcher') !== null, true);
  assert.match(css, /\.home-view\s*\{[\s\S]*display:\s*grid;[\s\S]*place-items:\s*center;/);
  assert.match(css, /\.home-launcher\s*\{[\s\S]*display:\s*flex;[\s\S]*flex-direction:\s*column;/);
});

test('experimental Home keeps the search visible above a scrollable MCP surface and polishes empty states', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('home.css', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const homeAppsBody = document.querySelector('.home-apps-body');

  assert.ok(homeAppsBody, 'the MCP list needs its own bounded scroll surface');
  assert.equal(homeAppsBody?.querySelector('#home-app-grid') !== null, true);
  assert.equal(homeAppsBody?.querySelector('#home-app-message') !== null, true);
  assert.match(app, /homeMCPEmptyCopy\(/);
  assert.match(css, /\.home-view\s*\{[\s\S]*overflow:\s*hidden;/);
  assert.match(css, /\.home-launcher\s*\{[\s\S]*align-self:\s*stretch;[\s\S]*height:\s*100%;[\s\S]*min-height:\s*0;/);
  assert.match(css, /\.home-search-form\s*\{[\s\S]*position:\s*sticky;[\s\S]*top:\s*0;[\s\S]*z-index:/);
  assert.match(css, /\.home-apps-section\s*\{[\s\S]*display:\s*flex;[\s\S]*flex-direction:\s*column;[\s\S]*min-height:\s*0;/);
  assert.match(css, /\.home-apps-body\s*\{[\s\S]*flex:\s*1;[\s\S]*min-height:\s*0;[\s\S]*overflow-y:\s*auto;[\s\S]*scrollbar-gutter:\s*stable;/);
  assert.match(css, /\.home-app-message\s*\{[\s\S]*display:\s*grid;[\s\S]*gap:/);
  assert.match(css, /\.home-app-message\s*\{[\s\S]*text-align:\s*center;/);
  assert.deepEqual(homeMCPEmptyCopy(0, ''), {
    title: 'No MCP apps available',
    detail: 'Install an MCP app with a UI to see it here.',
  });
  assert.deepEqual(homeMCPEmptyCopy(4, 'test'), {
    title: 'No MCP apps match “test”',
    detail: 'Try a different name, tool, or category.',
  });
});

test('experimental prototype uses a full-screen offline overlay with the original pet and a retry action', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('home.css', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const overlay = document.querySelector('#offline-screen');

  assert.ok(overlay, 'the prototype should include a body-level offline overlay');
  assert.equal(overlay?.parentElement, document.body, 'the overlay must cover the shell from the body level');
  assert.equal(overlay?.hidden, true, 'the overlay starts hidden while the backend is reachable');
  const actions = [...(overlay?.querySelectorAll('button, a, input, textarea, select') || [])];
  assert.deepEqual(actions.map((el) => el.id), ['offline-retry-btn'], 'offline state exposes only the retry action');
  assert.match(actions[0]?.textContent || '', /try again/i);
  assert.equal(overlay?.querySelector('.offline-mascot')?.getAttribute('alt'), '');
  assert.match(overlay?.querySelector('.offline-mascot')?.getAttribute('src') || '', /agent-offline-mascot\.png/);
  assert.match(overlay?.querySelector('h2')?.textContent || '', /offline/i);
  assert.match(overlay?.querySelector('p')?.textContent || '', /reachable/i);
  assert.match(app, /offline-screen\.js/);
  assert.match(app, /rpc\('app\.info',\s*\{\},\s*\{\s*timeoutMs:\s*4000\s*\}\)/);
  assert.match(app, /status === 'unavailable' \? 'offline'/);
  assert.match(css, /\.offline-screen\s*\{[\s\S]*position:\s*fixed;[\s\S]*inset:\s*0;[\s\S]*z-index:\s*200;/);
  assert.match(css, /\.offline-mascot\s*\{[\s\S]*object-fit:\s*contain;/);
});

test('experimental Home filters headless MCP entries and preserves each icon source', () => {
  const ui = {
    id: 'nusashell.notes',
    name: 'Notes',
    plugin: true,
    hasUI: true,
    icon: '📝',
  };
  const headless = {
    id: 'plugin_shell',
    name: 'Shell',
    plugin: false,
    hasUI: false,
    icon: '🖥️',
  };
  const rawFileIcon = {
    id: 'nusashell.assets',
    name: 'Assets',
    plugin: true,
    hasUI: true,
    installPath: '/tmp/plugins/nusashell.assets',
    icon: 'file://icons/app.png',
  };
  const remoteIcon = { ...ui, icon: 'https://cdn.example.test/notes.png' };

  assert.deepEqual(homeMCPAppsWithUI([headless, ui]).map((entry) => entry.id), ['nusashell.notes']);
  assert.deepEqual(filterHomeMCPApps([headless, ui], 'notes').map((entry) => entry.id), ['nusashell.notes']);

  // Search must cover every declared tool name — the card only renders a
  // bounded set, but a query for the fifth tool still has to find the app.
  const manyTools = {
    id: 'nusashell.toolbox',
    name: 'Toolbox',
    plugin: true,
    hasUI: true,
    tools: ['alpha', 'beta', 'gamma', 'delta', { name: 'epsilon_probe' }, { id: 'zeta_fetch' }],
  };
  assert.deepEqual(filterHomeMCPApps([manyTools], 'epsilon_probe').map((entry) => entry.id), ['nusashell.toolbox']);
  assert.deepEqual(filterHomeMCPApps([manyTools], 'zeta_fetch').map((entry) => entry.id), ['nusashell.toolbox']);
  assert.equal(homePluginIconValue(ui), '📝');
  assert.equal(homePluginIconGlyph(ui), '📝');
  assert.equal(homePluginIconSource(ui), null);
  assert.equal(homePluginIconSource(remoteIcon), 'https://cdn.example.test/notes.png');
  assert.equal(
    homePluginIconSource(rawFileIcon),
    '/local-file?path=%2Ftmp%2Fplugins%2Fnusashell.assets%2Ficons%2Fapp.png',
  );
  assert.equal(homePluginIconGlyph(rawFileIcon), '🧩');
});

test('experimental Home marks only the launched MCP app connected', () => {
  const apps = [
    { id: 'nusashell.notes', status: 'idle', hasUI: true },
    { id: 'nusashell.telegram', status: 'connected', hasUI: true },
  ];

  const next = markHomeMCPConnected(apps, 'nusashell.notes');

  assert.notEqual(next, apps, 'status updates should not mutate the RPC snapshot');
  assert.equal(next[0].status, 'connected');
  assert.equal(next[1], apps[1], 'unlaunched app entries should retain their identity');
  assert.equal(apps[0].status, 'idle');
  assert.deepEqual(markHomeMCPConnected(apps, 'missing.app'), apps);
});

test('composer actions are borderless and shell scrollbars use a slim Nusa palette', async () => {
  const css = await readFile(new URL('shell.css', root), 'utf8');

  assert.match(css, /\.send-button, \.stop-turn-button\s*\{[^}]*border:\s*0;/s);
  assert.match(css, /\.send-button:hover\s*\{[^}]*background:/s);
  assert.doesNotMatch(css, /\.send-button:hover\s*\{[^}]*border-color:/s);
  assert.match(css, /\.send-button:focus-visible, \.stop-turn-button:focus-visible\s*\{[^}]*outline:/s);
  assert.match(css, /scrollbar-width:\s*thin/);
  assert.match(css, /\.shell \*::-webkit-scrollbar\s*\{[^}]*width:\s*7px;[^}]*height:\s*7px;/s);
  assert.match(css, /\.shell \*::-webkit-scrollbar-thumb\s*\{[^}]*rgba\(88, 209, 195/);
});

test('page and async widget loading layers are delayed, accessible, and dismissible', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('loading-layer.css', root), 'utf8');
  const { createLoadingController } = await import(`../shared/loading-layer.js?test=${Date.now()}`);
  const document = new JSDOM('<main></main>', { url: 'http://localhost/' }).window.document;
  const host = document.querySelector('main');
  const controller = createLoadingController(host, { delay: 0, minimum: 0, label: 'Loading preview' });

  assert.ok(new JSDOM(html).window.document.querySelector('#page-loading'));
  assert.match(app, /hidePageLoading\(/);
  assert.match(css, /\.page-loading-layer\s*\{/);
  assert.match(css, /\.widget-loading-layer\s*\{/);

  const task = controller.run(async () => {
    await new Promise((resolve) => setTimeout(resolve, 15));
    return 'ready';
  });
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(host.getAttribute('aria-busy'), 'true');
  assert.equal(host.querySelector('.widget-loading-layer')?.getAttribute('role'), 'status');
  assert.equal(await task, 'ready');
  assert.equal(host.querySelector('.widget-loading-layer'), null);
  assert.equal(host.hasAttribute('aria-busy'), false);
});

test('loading layers remount after a widget replaces its host children mid-render', async () => {
  const { createLoadingController } = await import(`../shared/loading-layer.js?detached=${Date.now()}`);
  const document = new JSDOM('<main></main>', { url: 'http://localhost/' }).window.document;
  const host = document.querySelector('main');
  const controller = createLoadingController(host, { delay: 0, minimum: 0, label: 'Rendering subagent' });

  controller.show();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.ok(host.querySelector('.widget-loading-layer'));
  host.replaceChildren();

  // A refresh can detach the first veil while its render promise is pending.
  // The next show must not treat the stale reference as an active layer.
  controller.show();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.ok(host.querySelector('.widget-loading-layer'));
  await controller.hide();
  await controller.hide();
  assert.equal(host.querySelector('.widget-loading-layer'), null);
  assert.equal(host.hasAttribute('aria-busy'), false);
});

test('room switches keep full stream replacement behind a stable dark veil until the next paint', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const base = await readFile(new URL('widgets/widget-base.css', root), 'utf8');
  const css = await readFile(new URL('loading-layer.css', root), 'utf8');

  assert.match(app, /createLoadingController/);
  assert.match(app, /const conversationLoading = createLoadingController\(conversation,/);
  assert.match(app, /const needsRoomTransition = Boolean\(activeConversation\?\.id && !preserveStream\)/);
  assert.match(app, /if \(needsRoomTransition\) return conversationLoading\.run\(loadConversation\);/);
  assert.match(app, /await waitForNextPaint\(\)/);
  assert.doesNotMatch(app, /widgetStylesReady|waitForRoomPaint/, 'adopted sheets make style polling obsolete');
  assert.match(app, /async function initializeLiveData\(\) \{[\s\S]*?widgetStylesLoaded\(\),[\s\S]*?await openConversation/);
  assert.doesNotMatch(base, /--widget-style-ready/);
  assert.match(css, /\.widget-loading-layer\s*\{[^}]*z-index:\s*8;/s);
  assert.match(css, /\.widget-loading-layer\s*\{[^}]*background:\s*rgba\(5, 8, 17, \.86\)/s);
});

test('widget CSS is flat so constructed sheets carry every rule', async () => {
  const files = [
    'widgets/widget-base.css', 'widgets/agent-message.css', 'widgets/user-message.css', 'widgets/reasoning.css',
    'widgets/tools/tool-widgets.css', 'loading-layer.css', 'overlays.css',
  ];
  for (const file of files) assert.doesNotMatch(await readFile(new URL(file, root), 'utf8'), /@import/, file);
});

test('shared widget sheets are created once per URL and filled once before the first render', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    const { adoptWidgetStyles, widgetStyles, widgetStylesLoaded } = await import(`../shared/widget-styles.js?load=${Date.now()}`);
    assert.deepEqual(Object.keys(widgetStyles).sort(), ['agent', 'reasoning', 'tool', 'user']);
    const [loadingLayer, base] = widgetStyles.agent;
    for (const sheets of Object.values(widgetStyles)) {
      assert.equal(sheets[0], loadingLayer, 'every widget shares one loading-layer sheet');
      assert.equal(sheets[1], base, 'every widget shares one base sheet');
    }
    assert.equal(widgetStyles.agent[2], widgetStyles.tool[2], 'highlight.js theme is one shared sheet');

    const fetched = [];
    const fetchText = async (url) => {
      fetched.push(url);
      return `:host { --sheet: "${new URL(url).pathname.split('/').pop()}"; }`;
    };
    const loaded = widgetStylesLoaded(fetchText);
    assert.equal(widgetStylesLoaded(fetchText), loaded, 'loading is memoized');
    await loaded;
    assert.equal(fetched.length, new Set(fetched).size, 'each sheet URL is fetched once');
    // The highlight theme path resolves inside the mirrored vendor/ tree
    // (../vendor/ from shared/) and keeps the same specifier after promotion,
    // so resolve the specifier the module uses instead of hardcoding a layout.
    const stylesSource = await readFile(new URL('shared/widget-styles.js', root), 'utf8');
    const highlightSpec = stylesSource.match(/sheet\('([^']*github-dark\.min\.css)'\)/)?.[1];
    assert.ok(highlightSpec, 'shared sheets keep one highlight.js theme');
    const moduleURL = new URL('shared/widget-styles.js', root);
    assert.ok(fetched.includes(new URL(highlightSpec, moduleURL).href));
    assert.ok(fetched.every((url) => url.startsWith('file:')), 'sheet URLs resolve from the module, not the document base');
    assert.match(base.cssRules[0].cssText, /widget-base\.css/);

    const host = document.createElement('div');
    const shadow = host.attachShadow({ mode: 'open' });
    adoptWidgetStyles(shadow, 'user');
    widgetStyles.user.forEach((sheet, index) => assert.equal(shadow.adoptedStyleSheets[index], sheet));
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('every widget adopts its shared sheets instead of mounting a stylesheet link', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    const stamp = Date.now();
    for (const module of ['agent-message', 'user-message', 'reasoning', 'tools/widgets']) {
      await import(`../widgets/${module}.js?adopt=${stamp}`);
    }
    const { widgetStyles } = await sharedStylesImport('widgets/agent-message.js');
    const cases = [
      ['agent-message-widget', 'agent', { id: 'live-a', markdown: 'Hi' }],
      ['user-message-widget', 'user', { id: 'u', content: 'Hi', time: '' }],
      ['reasoning-widget', 'reasoning', { id: 'r', summary: 'Plan', details: [], duration: '' }],
      ['terminal-tool-widget', 'tool', { id: 't', type: 'tool.run', tool: 'exec', args: { command: 'ls' }, status: 'complete' }],
    ];
    for (const [tag, name, event] of cases) {
      const widget = document.createElement(tag);
      widget.event = event;
      document.body.append(widget);
      assert.equal(widget.shadowRoot.querySelector('link'), null, tag);
      assert.equal(widget.shadowRoot.adoptedStyleSheets.length, widgetStyles[name].length, tag);
      widgetStyles[name].forEach((sheet, index) => assert.equal(widget.shadowRoot.adoptedStyleSheets[index], sheet, tag));
    }
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('every widget-styles importer pins the same canonical specifier', async () => {
  // The adoption registry is module-instance state: two different ?v=
  // specifiers load the module twice, and pip.js readopt then heals zero
  // roots while widgets keep stale sheet objects. A version bump must move
  // every importer at once.
  const importers = [
    'widgets/agent-message.js',
    'widgets/user-message.js',
    'widgets/reasoning.js',
    'widgets/tools/widgets.js',
    'js/pip.js',
    'page/conversation/app.js',
  ];
  const specifiers = new Map();
  for (const importer of importers) {
    const importerURL = new URL(importer, root);
    const source = await readFile(importerURL, 'utf8');
    for (const match of source.matchAll(/from\s+'([^']*shared\/widget-styles\.js[^']*)'/g)) {
      const resolved = new URL(match[1], importerURL).href;
      specifiers.set(resolved, [...(specifiers.get(resolved) ?? []), importer]);
    }
  }
  assert.equal(specifiers.size, 1,
    `widget-styles must load through one canonical specifier, got ${JSON.stringify([...specifiers])}`);
});

test('conversation atmosphere is decorative and cannot block widget interaction', async () => {
  const css = await readFile(new URL('shell.css', root), 'utf8');
  assert.match(css, /\.conversation::before\s*\{/);
  assert.match(css, /pointer-events:\s*none/);
  assert.match(css, /topography\.svg/);
  assert.match(css, /--deep-water:\s*#070b14/);
  assert.match(css, /\.conversation\s*\{[^}]*rgba\(84, 105, 174, \.08\)/s);
  assert.match(css, /\.conversation::before\s*\{[^}]*opacity:\s*\.72/);
  assert.match(css, /filter:\s*brightness\(1\.2\)\s+contrast\(1\.08\)/);
});

test('stream widgets do not reserve a decorative timeline marker gutter', async () => {
  const base = await readFile(new URL('widgets/widget-base.css', root), 'utf8');
  assert.match(base, /:host\s*\{[^}]*padding-left:\s*0/s);
  assert.doesNotMatch(base, /:host::before/);
  for (const name of ['user-message', 'agent-message', 'reasoning']) {
    const css = await readFile(new URL(`widgets/${name}.css`, root), 'utf8');
    assert.doesNotMatch(css, /:host::before/, `${name} must rely on its own functional styling`);
  }
  const toolCSS = await readFile(new URL('widgets/tools/tool-widgets.css', root), 'utf8');
  assert.doesNotMatch(toolCSS, /:host::before/, 'semantic tool widgets do not add a decorative timeline marker');
});

test('every prototype event resolves to its own widget element', () => {
  assert.deepEqual(supportedEventTypes, [
    'user.message',
    'agent.reasoning',
    'tool.run',
    'agent.message',
  ]);

  assert.equal(eventWidgetTag('tool.run', { tool: 'exec' }), 'terminal-tool-widget');
  assert.equal(eventWidgetTag('tool.run', { tool: 'ask_question' }), 'question-tool-widget');
  // No producer emits task.plan/artifact.created — the wire vocabulary and
  // live-data projections never create them, so unknown types fall back.
  assert.equal(eventWidgetTag('artifact.created'), 'unknown-event-widget');
  assert.equal(eventWidgetTag('unknown.event'), 'unknown-event-widget');
});

test('every NusaShell built-in tool maps to a semantic widget with an unknown fallback', () => {
  const expected = {
    exec: 'terminal-tool-widget',
    ask_question: 'question-tool-widget',
    file_patch: 'file-tool-widget',
    grep: 'search-tool-widget',
    web_search: 'web-tool-widget',
    show: 'media-tool-widget',
    todo: 'task-tool-widget',
    subagent: 'subagent-tool-widget',
    automation: 'automation-tool-widget',
    memory: 'knowledge-tool-widget',
    mcp_list: 'connector-tool-widget',
  };
  for (const [name, tag] of Object.entries(expected)) assert.equal(toolWidgetDefinition(name).tag, tag, name);
  assert.equal(toolWidgetDefinition('plugin_future_tool').tag, 'generic-tool-widget');
  assert.ok(BUILTIN_TOOL_WIDGETS.length >= 40, 'the registry covers the current built-in roster and historical transcript aliases');
  assert.equal(new Set(BUILTIN_TOOL_WIDGETS.map(({ name }) => name)).size, BUILTIN_TOOL_WIDGETS.length);
});

test('backend presentation variants refine generic tool boxes without changing special widgets', () => {
  assert.equal(toolWidgetDefinition('future_reader', { variant: 'file-content' }).tag, 'file-tool-widget');
  assert.equal(toolWidgetDefinition('future_search', { variant: 'search-results' }).tag, 'search-tool-widget');
  assert.equal(toolWidgetDefinition('future_media', { variant: 'media' }).tag, 'media-tool-widget');
  assert.equal(toolWidgetDefinition('ask_question', { variant: 'terminal' }).tag, 'question-tool-widget');
});

test('event filters merge tool and work events into one chronological activity stream', () => {
  const events = [
    { id: '1', type: 'user.message' },
    { id: '2', type: 'tool.run' },
    { id: '3', type: 'agent.message' },
    { id: '4', type: 'tool.run' },
  ];

  events.splice(2, 0, { id: 'reason', type: 'agent.reasoning' });
  assert.deepEqual(filterEvents(events, 'activity').map(({ id }) => id), ['2', '4']);
  assert.deepEqual(filterEvents(events, 'message').map(({ id }) => id), ['1', 'reason', '3']);
  assert.deepEqual(filterEvents(events, 'all'), events);
});

test('parallel subagents use an accessible custom switcher and replace detail in place', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?test=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  const subagents = [
    { id: 'registry', name: 'Widget registry', agentName: 'Cursor', status: 'running', elapsed: '1m', objective: 'Map widget contracts.', current: 'Reviewing event registry.' },
    { id: 'responsive', name: 'Responsive QA', agentName: 'Devin', status: 'complete', elapsed: '42s', objective: 'Check narrow layouts.', current: 'Viewport sweep complete.' },
  ];

  mountSubagentPanel(panel, subagents);

  const trigger = panel.querySelector('[role="combobox"]');
  assert.equal(panel.querySelector('select'), null);
  assert.equal(trigger.getAttribute('aria-expanded'), 'false');
  trigger.click();
  assert.equal(trigger.getAttribute('aria-expanded'), 'true');
  assert.equal(panel.querySelectorAll('[role="option"]').length, 2);
  panel.querySelector('[role="option"][data-subagent-id="responsive"]').click();
  assert.equal(trigger.textContent.includes('Responsive QA'), true);
  assert.equal(trigger.textContent.includes('ACP · Devin'), true);
  assert.equal(panel.querySelector('[role="option"][data-subagent-id="responsive"] small').textContent.includes('ACP · Devin'), true);
  assert.equal(panel.querySelector('.subagent-agent').textContent, 'ACP · Devin');
  assert.equal(panel.querySelector('.subagent-objective').textContent, 'Check narrow layouts.');
  assert.equal(trigger.getAttribute('aria-expanded'), 'false');
});

test('subagent UI carries ACP identity from the run metadata into the timeline card', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const state = await readFile(new URL('page/conversation/subagent-state.js', root), 'utf8');
  const widget = await readFile(new URL('widgets/tools/widgets.js', root), 'utf8');
  assert.match(app, /syncSubagentToolIdentity/);
  assert.match(state, /parent_tool_call_id/);
  assert.match(state, /agent_name/);
  assert.match(state, /parentToolCallId/);
  assert.match(widget, /subagent-agent/);
  assert.match(widget, /ACP ·/);
});

test('ACP run metadata enriches a persisted subagent tool event by parent call', () => {
  const event = {
    type: 'tool.run',
    tool: 'subagent',
    toolCallId: 'call-42',
    args: { agent_id: 'acp-old', title: 'Ship the slice' },
  };
  const run = {
    agent_id: 'acp-devin',
    agent_name: 'Devin',
    parent_tool_call_id: 'call-42',
    title: 'Ship the slice',
  };

  const enriched = enrichSubagentToolEvent(event, [run]);

  assert.notEqual(enriched, event, 'matching metadata should produce an updated event');
  assert.equal(enriched.args.agent_name, 'Devin');
  assert.equal(enriched.args.agent_id, 'acp-devin');
  assert.equal(event.args.agent_name, undefined, 'the persisted event object must remain immutable');
});

test('subagent selection closes the switcher before reconciling a large transcript', async () => {
  const source = await readFile(new URL('page/conversation/subagent-dock.js', root), 'utf8');
  const close = source.indexOf('if (followTail) {');
  const transcript = source.indexOf('syncAgentTranscript(state, active.transcript || []);');
  assert.ok(close >= 0 && close < transcript, 'selection feedback must not wait for transcript reconciliation');
  assert.match(source, /if \(followTail\) \{\s*setOpen\(state, false\);\s*state\.trigger\.focus\(\);\s*\}/s);
});

test('subagent switcher accepts pointer selection on an option child before click dispatch', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?pointer-selection=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  mountSubagentPanel(panel, [
    { id: 'current', name: 'Current run', status: 'complete', startedAt: '2026-09-14T02:00:00Z', objective: 'Current.' },
    { id: 'next', name: 'Next run', status: 'running', startedAt: '2026-09-14T01:00:00Z', objective: 'Next.' },
  ]);

  const trigger = panel.querySelector('.subagent-trigger');
  trigger.click();
  const label = panel.querySelector('[role="option"][data-subagent-id="next"] span:nth-of-type(2)');
  label.dispatchEvent(new dom.window.MouseEvent('pointerdown', { bubbles: true, button: 0 }));

  assert.equal(trigger.getAttribute('aria-expanded'), 'false');
  assert.equal(trigger.querySelector('strong').textContent, 'Next run');
});

test('subagent switcher keeps the newest run at the top', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?newest-first=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');

  mountSubagentPanel(panel, [
    { id: 'older', name: 'Older run', status: 'complete', startedAt: '2026-09-11T08:00:00Z', objective: 'Older.' },
    { id: 'newer', name: 'Newest run', status: 'running', startedAt: '2026-09-11T09:00:00Z', objective: 'Newest.' },
    { id: 'legacy-older', name: 'Legacy older', status: 'complete', objective: 'Legacy older.' },
    { id: 'legacy-newer', name: 'Legacy newer', status: 'complete', objective: 'Legacy newer.' },
  ]);

  assert.deepEqual(
    [...panel.querySelectorAll('[role="option"]')].map((option) => option.dataset.subagentId),
    ['newer', 'older', 'legacy-newer', 'legacy-older'],
  );
  assert.equal(panel.querySelector('.subagent-trigger strong').textContent, 'Newest run');
});

test('selected subagent exposes its rich transcript without duplicating it in the parent timeline', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?transcript=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  mountSubagentPanel(panel, [{
    id: 'cursor', name: 'Cursor', status: 'complete', elapsed: '12s', objective: 'Draft options.', current: 'Finished.',
    transcript: [
      { kind: 'prompt', text: '**Delegation**\n\n- Write three options.' },
      { kind: 'text', text: '**Island shell** is ready.' },
      { kind: 'thought', text: '**Plan**\n\n- Compare the options.' },
      { kind: 'tool', tool_title: 'Read file', text: '/workspace/profile.html' },
    ],
  }]);
  assert.equal(panel.querySelector('.subagent-current'), null, 'the dock does not repeat a subagent\'s last output above its transcript');
  assert.equal(panel.querySelector('.subagent-transcript .acp-prompt-message strong').textContent, 'Delegation');
  assert.equal(panel.querySelector('.subagent-transcript .acp-run-message .agent-bubble-text strong').textContent, 'Island shell');
  const thought = panel.querySelector('.subagent-transcript .agent-reasoning');
  assert.equal(thought.querySelector('strong'), null, 'collapsed thinking remains lazy');
  thought.open = true;
  thought.dispatchEvent(new dom.window.Event('toggle'));
  assert.equal(thought.querySelector('strong').textContent, 'Plan');
  assert.match(panel.querySelector('.subagent-transcript .acp-prompt-message').textContent, /Write three options/);
  assert.match(panel.querySelector('.subagent-transcript .agent-tool-event').textContent, /Read file/);
});

test('subagent transcript constrains long content to a vertical, wrapping reader', async () => {
  const css = await readFile(new URL('dock.css', root), 'utf8');
  const toolCSS = await readFile(new URL('widgets/tools/tool-widgets.css', root), 'utf8');
  const shellCSS = await readFile(new URL('shell.css', root), 'utf8');
  assert.match(css, /\.subagent-objective \{[^}]*font-size:\s*11px;[^}]*line-height:\s*1\.5;/s);
  assert.match(css, /\.subagent-transcript > \.agent-message\.assistant \.agent-bubble-text \{[^}]*font-size:\s*10px;[^}]*line-height:\s*1\.5;/s);
  assert.match(toolCSS, /\.subagent-title \{[^}]*font-size:\s*10px;/s);
  assert.match(css, /\.subagent-objective \{[^}]*max-height:\s*calc\(10 \* 1\.5em\);[^}]*overflow-y:\s*auto;/s);
  assert.match(css, /\.subagent-transcript \{[^}]*overflow-x:\s*hidden;/s);
  assert.match(shellCSS, /\.dock-panel\s*\{[^}]*display:\s*flex;[^}]*flex-direction:\s*column;[^}]*overflow-x:\s*hidden;/s);
  assert.match(shellCSS, /\.dock-panel\[hidden\]\s*\{\s*display:\s*none;\s*\}/);
  assert.match(css, /\.subagent-section\s*\{[^}]*display:\s*flex;[^}]*min-height:\s*0;/s);
  assert.match(css, /\.subagent-detail\s*\{[^}]*display:\s*flex;[^}]*min-height:\s*0;[^}]*flex-direction:\s*column;/s);
  assert.match(css, /\.subagent-transcript \{[^}]*flex:\s*1;[^}]*min-height:\s*0;[^}]*max-height:\s*none;/s);
  assert.match(css, /\.subagent-transcript > \.agent-message \{[^}]*min-width:\s*0;/s);
  assert.match(css, /\.subagent-transcript > \.agent-message \.agent-bubble \{[^}]*overflow-wrap:\s*anywhere;/s);
  assert.match(css, /\.subagent-transcript \.agent-bubble-text pre \{[^}]*white-space:\s*pre-wrap;/s);
  assert.match(css, /\.subagent-transcript \.agent-reasoning-content \{[^}]*max-height:\s*calc\(8 \* 1\.8em\);[^}]*overflow-y:\s*auto;/s);
  assert.match(css, /\.subagent-transcript > \.agent-message \.agent-bubble \.markdown-table-scroll \{[^}]*max-width:\s*400px;[^}]*overflow-x:\s*auto;/s);
  assert.match(css, /\.subagent-transcript > \.agent-message \.agent-bubble \.markdown-table-scroll table \{[^}]*width:\s*max-content;/s);
});

test('subagent transcript follows live output until the reader scrolls upward', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?follow=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  const run = [{ id: 'cursor', name: 'Cursor', status: 'running', objective: 'Inspect.', transcript: [{ kind: 'text', text: 'Update.' }] }];
  const mount = () => {
    mountSubagentPanel(panel, run);
    const transcript = panel.querySelector('.subagent-transcript');
    Object.defineProperties(transcript, {
      clientHeight: { value: 100, configurable: true },
      scrollHeight: { value: 1000, configurable: true },
    });
    return transcript;
  };

  let transcript = mount();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(transcript.scrollTop, 1000, 'a live subagent begins at the newest output');

  transcript.dispatchEvent(new dom.window.WheelEvent('wheel', { deltaY: -48 }));
  transcript.scrollTop = 900;
  transcript.dispatchEvent(new dom.window.Event('scroll'));
  transcript = mount();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(transcript.scrollTop, 900, 'an upward reader gesture unpins the next live update without losing reader position');

  transcript.scrollTop = 900;
  transcript.dispatchEvent(new dom.window.WheelEvent('wheel', { deltaY: 48 }));
  transcript.dispatchEvent(new dom.window.Event('scroll'));
  transcript = mount();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(transcript.scrollTop, 1000, 'reaching the tail opts back into live follow');
});

test('subagent prompt and transcript keep independent scroll positions across deltas', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?prompt-scroll=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  const run = [{
    id: 'prompt-scroll', name: 'Cursor', status: 'running', objective: 'A long delegation prompt.',
    transcript: [{ kind: 'text', text: 'First live chunk.' }],
  }];

  mountSubagentPanel(panel, run);
  await new Promise((resolve) => setTimeout(resolve, 0));
  const objective = panel.querySelector('.subagent-objective');
  const transcript = panel.querySelector('.subagent-transcript');
  objective.scrollTop = 120;
  objective.dispatchEvent(new dom.window.Event('scroll'));
  Object.defineProperties(transcript, {
    clientHeight: { value: 100, configurable: true },
    scrollHeight: { value: 1000, configurable: true },
  });
  transcript.dispatchEvent(new dom.window.WheelEvent('wheel', { deltaY: -48 }));
  transcript.scrollTop = 600;
  transcript.dispatchEvent(new dom.window.Event('scroll'));

  mountSubagentPanel(panel, [{ ...run[0], transcript: [
    { kind: 'text', text: 'First live chunk.' },
    { kind: 'text', text: 'Second live chunk.' },
  ] }]);
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.equal(panel.querySelector('.subagent-objective').scrollTop, 120, 'the prompt reader position survives a transcript delta');
  assert.equal(panel.querySelector('.subagent-transcript').scrollTop, 600, 'the transcript reader keeps its own position');
});

test('subagent deltas reconcile in place without closing reasoning or resetting readers', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?append=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  const initial = [{
    id: 'append-only', name: 'Cursor', status: 'running', objective: '**Long objective**\n\nKeep this reader position.',
    transcript: [
      { kind: 'thought', text: '**Inspect** the current state.' },
      { kind: 'text', text: 'First live chunk.' },
    ],
  }];

  mountSubagentPanel(panel, initial);
  await new Promise((resolve) => setTimeout(resolve, 0));
  const section = panel.querySelector('.subagent-section');
  const objective = panel.querySelector('.subagent-objective');
  const transcript = panel.querySelector('.subagent-transcript');
  const reasoning = transcript.querySelector('.agent-reasoning');
  reasoning.open = true;
  reasoning.dispatchEvent(new dom.window.Event('toggle'));
  objective.scrollTop = 90;
  objective.dispatchEvent(new dom.window.Event('scroll'));
  Object.defineProperties(transcript, {
    clientHeight: { value: 100, configurable: true },
    scrollHeight: { value: 1200, configurable: true },
  });
  transcript.dispatchEvent(new dom.window.WheelEvent('wheel', { deltaY: -48 }));
  transcript.scrollTop = 480;
  transcript.dispatchEvent(new dom.window.Event('scroll'));

  mountSubagentPanel(panel, [{ ...initial[0], transcript: [
    { kind: 'thought', text: '**Inspect** the current state, then verify the delta.' },
    { kind: 'text', text: 'First live chunk.' },
    { kind: 'thought', text: '**Next** check the result.' },
  ] }]);
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.equal(panel.querySelector('.subagent-section'), section, 'live updates keep the mounted detail shell');
  assert.equal(panel.querySelector('.subagent-objective'), objective, 'live updates keep the objective reader');
  assert.equal(panel.querySelector('.subagent-transcript'), transcript, 'live updates keep the transcript reader');
  assert.equal(panel.querySelector('.agent-reasoning'), reasoning, 'the existing reasoning disclosure is patched, not replaced');
  assert.equal(panel.querySelector('.agent-reasoning').open, true, 'an open reasoning disclosure stays open across a delta');
  assert.equal(panel.querySelector('.subagent-objective').scrollTop, 90, 'objective scroll stays independent');
  assert.equal(panel.querySelector('.subagent-transcript').scrollTop, 480, 'transcript scroll stays independent');
  assert.equal(panel.querySelectorAll('.agent-reasoning').length, 2, 'the appended reasoning event is added once');
});

test('subagent tool updates clear stale lifecycle output while keeping the card mounted', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?tool-append=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  const base = {
    id: 'tool-append', name: 'Cursor', status: 'running', objective: 'Inspect tool output.',
    transcript: [{ kind: 'tool', tool_id: 'call-tool-append', tool_kind: 'tool', tool_status: 'in_progress', text: 'in progress' }],
  };

  mountSubagentPanel(panel, [base]);
  const card = panel.querySelector('.agent-tool-event');
  assert.match(card.textContent, /in progress/);

  mountSubagentPanel(panel, [{ ...base, transcript: [{
    kind: 'tool', tool_id: 'call-tool-append', tool_kind: 'tool', tool_status: 'completed', text: '',
  }] }]);

  const updated = panel.querySelector('.agent-tool-event');
  assert.equal(updated, card, 'tool status updates patch the existing card');
  assert.equal(updated.classList.contains('is-success'), true);
  assert.equal(updated.querySelector('.agent-tool-event-head-summary').textContent, 'completed');
  assert.equal(updated.querySelector('.agent-tool-event-output'), null, 'a status-only update does not create a duplicate output line');
  assert.doesNotMatch(updated.textContent, /in progress/);

  updated.open = true;
  mountSubagentPanel(panel, [{ ...base, transcript: [{
    kind: 'tool', tool_id: 'call-tool-append', tool_kind: 'tool', tool_status: 'completed', text: 'final output',
  }] }]);
  assert.equal(panel.querySelector('.agent-tool-event'), updated, 'later updates keep the same tool card');
  assert.equal(panel.querySelector('.agent-tool-event').open, true, 'a user-opened tool stays open across later deltas');
});

test('subagent tools stay collapsed and code keeps a plain default with a CodeMirror action', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?compact-tools=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  const css = await readFile(new URL('dock.css', root), 'utf8');
  mountSubagentPanel(panel, [{
    id: 'compact-tools', name: 'Cursor', status: 'running', objective: 'Inspect.',
    transcript: [
      { kind: 'text', text: '```java\nclass Demo {\n  void run() {}\n}\n```' },
      { kind: 'tool', tool_id: 'call-compact', tool_kind: 'read_file', tool_status: 'running', text: 'waiting' },
    ],
  }]);

  const tool = panel.querySelector('.agent-tool-event');
  const pre = panel.querySelector('.agent-bubble-text pre');
  const editorTrigger = pre?.querySelector('.code-editor-trigger');
  const request = panel.querySelector('.agent-tool-request');
  assert.equal(tool?.open, false, 'running tool cards remain collapsed until the reader opens them');
  assert.equal(tool?.classList.contains('acp-terminal-card'), true);
  assert.equal(tool?.querySelector('.acp-tool-kind')?.textContent, 'TERMINAL');
  assert.ok(pre?.querySelector('code'), 'the initial code surface remains a lightweight code block');
  assert.equal(pre?.querySelector('.CodeMirror'), null, 'CodeMirror is not mounted into every live code block');
  assert.equal(editorTrigger?.getAttribute('aria-label'), 'Open Plain text code in editor');
  assert.equal(request?.tagName, 'BUTTON', 'ACP input is an expandable command control');
  request?.click();
  assert.equal(request?.getAttribute('aria-expanded'), 'true');
  assert.match(css, /\.subagent-transcript \.agent-tool-event-head \{[^}]*min-height:\s*26px;/s);
  assert.match(css, /\.subagent-transcript \.agent-tool-event-body \{[^}]*gap:\s*7px;[^}]*padding:\s*7px 6px 8px 24px;/s);
  assert.match(css, /\.subagent-transcript \.agent-tool-event \.agent-tool-request,\s*\.subagent-transcript \.agent-tool-event \.agent-tool-event-output,\s*\.subagent-transcript \.agent-tool-event \.agent-tool-terminal-output \{[^}]*border-radius:\s*6px;[^}]*background:/s);
  assert.match(css, /\.subagent-transcript \.agent-tool-event \.agent-tool-request::before \{[^}]*display:\s*block;/s);
  assert.match(css, /\.subagent-transcript \.agent-tool-event \.agent-tool-request \{[^}]*overflow:\s*hidden;[^}]*-webkit-line-clamp:\s*3;/s);
  assert.match(css, /\.subagent-transcript \.agent-tool-event \.agent-tool-request\.is-expanded \{[^}]*overflow:\s*auto;[^}]*-webkit-line-clamp:\s*unset;/s);
  assert.match(css, /\.subagent-transcript \.agent-tool-terminal-output::before \{[^}]*content:\s*"output";/s);
  assert.match(css, /\.subagent-transcript \.agent-reasoning-content \{[^}]*line-height:\s*1\.8;/s);
  assert.match(css, /\.subagent-transcript \.agent-tool-event-title \{[^}]*min-width:\s*0;/s);
  assert.match(css, /\.subagent-transcript details\.agent-tool-event\[data-tool\] \{[^}]*border:\s*0;/s);
  assert.match(css, /\.subagent-transcript details\.agent-tool-event\[data-tool\]\.is-running \{[^}]*border-color:\s*transparent;/s);
});

test('subagent thinking code gets a CodeMirror action when the disclosure opens', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?thinking-code=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  mountSubagentPanel(panel, [{
    id: 'thinking-code', name: 'Devin', status: 'running', objective: 'Inspect.',
    transcript: [{ kind: 'thought', text: 'Reasoning\n\n```js\nfunction policy() { return true; }\n```' }],
  }]);

  const thought = panel.querySelector('.subagent-transcript details.agent-reasoning');
  assert.ok(thought);
  thought.open = true;
  thought.dispatchEvent(new dom.window.Event('toggle'));
  assert.equal(thought.querySelector('.code-editor-trigger')?.getAttribute('aria-label'), 'Open JavaScript code in editor');
});

test('subagent narrow content labels change sets and bounds table/code surfaces', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?narrow-content=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  const css = await readFile(new URL('dock.css', root), 'utf8');
  mountSubagentPanel(panel, [{
    id: 'narrow-content', name: 'Cursor', status: 'complete', objective: 'Inspect.',
    transcript: [{ kind: 'text', text: [
      'New: domain/network/GuestSshEndpoint.java',
      'Modify: infrastructure/proot/GuestSshDaemon.java (drop port arg)',
      'Delete: infrastructure/proot/LoopbackPortCandidates.java',
      '',
      '| ID | Case |',
      '| --- | --- |',
      '| HOME-01 | Fresh launch |',
      '',
      '```java',
      'class Demo {}',
      '```',
    ].join('\n') }],
  }]);

  const changeSummary = [...panel.querySelectorAll('.agent-bubble-text p')]
    .find((node) => node.textContent.trim().startsWith('New:'));
  const table = panel.querySelector('.markdown-table-scroll');
  const pre = panel.querySelector('.agent-bubble-text pre');
  assert.ok(changeSummary?.classList.contains('subagent-change-summary'));
  assert.equal(pre?.querySelector('.code-language-label')?.textContent, 'Plain text · text/plain');
  assert.ok(table, 'markdown tables remain in their bounded scroller');
  assert.match(css, /\.subagent-transcript \.agent-bubble-text \.subagent-change-summary \{[^}]*overflow-x:\s*auto;[^}]*white-space:\s*pre;/s);
  assert.match(css, /\.subagent-transcript > \.agent-message\.assistant \.agent-bubble \.markdown-table-scroll \{[^}]*border:\s*1px solid var\(--line-strong\);/s);
  assert.match(css, /\.subagent-transcript > \.agent-message\.assistant \.agent-bubble \.markdown-table-scroll :is\(th, td\) \{[^}]*border:\s*1px solid var\(--line\);/s);
  assert.match(css, /\.subagent-transcript > \.agent-message\.assistant \.agent-bubble-text pre \{[^}]*border:\s*1px solid var\(--line-strong\);[^}]*overflow-x:\s*auto;/s);
});

test('live ACP snapshots preserve existing transcript data when lifecycle payloads are partial', () => {
  const previous = toSubagentView({
    id: 'run-1', agent_name: 'Cursor', agent_id: 'acp_cursor', parent_tool_call_id: 'call-subagent', conversation_id: 'conv-1', status: 'running',
    prompt: '**Inspect** the stream', started_at: '2026-09-11T09:00:00Z',
    transcript: [{ kind: 'text', text: 'First chunk' }],
  }, null, Date.parse('2026-09-11T09:00:04Z'));
  const next = toSubagentView({
    id: 'run-1', conversation_id: 'conv-1', status: 'completed', ended_at: '2026-09-11T09:00:08Z',
  }, previous, Date.parse('2026-09-11T09:00:08Z'));

  assert.equal(next.status, 'complete');
  assert.equal(next.elapsed, '8s');
  assert.equal(next.objective, '**Inspect** the stream');
  assert.equal(next.agentName, 'Cursor');
  assert.equal(next.agentId, 'acp_cursor');
  assert.equal(next.parentToolCallId, 'call-subagent');
  assert.deepEqual(next.transcript, previous.transcript);
});

test('ACP run SSE snapshots reset the cursor and updates stay sequence- and room-scoped', () => {
  const cursor = { lastSeq: 9 };
  const run = { id: 'run-1', conversation_id: 'conv-1', status: 'running' };
  const snapshot = acceptSubagentStreamFrame(cursor, {
    type: 'acp.run.snapshot', conversation_id: 'conv-1', seq: 2,
    runs: [run, { id: 'other', conversation_id: 'conv-2', status: 'running' }],
  }, 'conv-1');
  assert.equal(cursor.lastSeq, 2, 'a reconnect snapshot starts a new cursor epoch');
  assert.deepEqual(snapshot, { kind: 'snapshot', seq: 2, runs: [run] });
  assert.equal(acceptSubagentStreamFrame(cursor, { type: 'acp.run.updated', seq: 2, run }, 'conv-1'), null, 'duplicate sequence is ignored');
  assert.equal(acceptSubagentStreamFrame(cursor, { type: 'acp.run.updated', seq: 3, run: { ...run, conversation_id: 'conv-2' } }, 'conv-1'), null, 'other-room updates are ignored');
  assert.equal(cursor.lastSeq, 2, 'rejected frames do not advance the cursor');
  assert.deepEqual(acceptSubagentStreamFrame(cursor, { type: 'acp.run.updated', seq: 3, run: { ...run, activity: 'tool' } }, 'conv-1'), {
    kind: 'update', seq: 3, run: { ...run, activity: 'tool' },
  });
});

test('subagent scrollbar movement away from the tail releases follow until the tail is reached', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?pointer-follow=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  const run = [{ id: 'cursor-pointer', name: 'Cursor', status: 'running', objective: 'Inspect.', transcript: [{ kind: 'text', text: 'Update.' }] }];
  mountSubagentPanel(panel, run);
  const transcript = panel.querySelector('.subagent-transcript');
  Object.defineProperties(transcript, {
    clientHeight: { value: 100, configurable: true },
    scrollHeight: { value: 1000, configurable: true },
    getBoundingClientRect: { value: () => ({ right: 200 }), configurable: true },
  });
  transcript.scrollTop = 1000;
  transcript.dispatchEvent(new dom.window.Event('scroll'));
  transcript.dispatchEvent(new dom.window.MouseEvent('pointerdown', { button: 0, clientX: 199 }));
  transcript.scrollTop = 700;
  transcript.dispatchEvent(new dom.window.Event('scroll'));

  mountSubagentPanel(panel, run);
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(panel.querySelector('.subagent-transcript').scrollTop, 700);
});

test('running subagent transcript skips the loading veil while chunks stream', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?stream-loading=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  mountSubagentPanel(panel, [{
    id: 'cursor-live', name: 'Cursor', status: 'running', objective: 'Stream updates.',
    transcript: [{ kind: 'text', text: 'A live chunk.' }],
  }]);

  await new Promise((resolve) => setTimeout(resolve, 180));
  assert.equal(panel.querySelector('.widget-loading-layer'), null);
  assert.match(panel.querySelector('.subagent-transcript').textContent, /A live chunk/);
});

test('composer selects use the shared Slim Select integration instead of a custom listbox', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const source = await readFile(new URL('page/conversation/composer.js', root), 'utf8');
  const css = await readFile(new URL('shell.css', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const document = new JSDOM(html).window.document;

  // Path stays tree-relative so the assertion holds both in the prototype
  // (vendor/..., self-contained mirror) and after promotion (vendor/...).
  assert.match(html, /vendor\/slim-select\/slimselect\.css/);
  assert.match(source, /js\/ui\.js/);
  assert.match(source, /createSelect\(/);
  assert.match(source, /search:\s*true/);
  assert.match(app, /initComposerSelects/);
  assert.doesNotMatch(app, /initComposerPickers/);
  assert.equal(document.querySelector('#provider-select'), null, 'the selected model already owns provider identity');
  assert.equal(document.querySelectorAll('#model-select option').length, 0, 'production model choices come from the backend catalog');
  assert.ok(document.querySelector('#route-select'));
  assert.equal(document.querySelector('#workspace-select'), null, 'workspace is a file browser, not a select');
  assert.equal(document.querySelector('#workspace-browser-trigger')?.getAttribute('aria-controls'), 'workspace-browser-view');
  assert.equal(document.querySelector('#workspace-browser-view')?.hidden, true);
  assert.ok(document.querySelector('#model-config-popover #effort-select'));
  assert.match(source, /modelConfigSummary/);
  assert.match(source, /const path = event\.composedPath\(\)[\s\S]*path\.includes\(popover\)/,
    'rerendering a clicked file-browser row must not make the model panel look like an outside click');
  assert.match(css, /\.ss-content\.model-catalog\.ss-dir-above\s*\{[^}]*flex-direction:\s*column/s,
    'model search stays above results even when the floating menu opens upward');
});

test('workspace browser navigates host RPC folders with distinct panel-back and parent-up actions', async () => {
  const { initWorkspaceBrowser } = await import(`../page/conversation/workspace-browser.js?test=${Date.now()}`);
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const rootElement = dom.window.document.querySelector('#message-form');

  const calls = [];
  const listings = {
    '': { path: '/home/dev', parent: '/home', entries: [{ name: 'workspace', path: '/home/dev/workspace' }] },
    '/home': { path: '/home', parent: '/', entries: [{ name: 'dev', path: '/home/dev' }] },
    '/home/dev/workspace': { path: '/home/dev/workspace', parent: '/home/dev', entries: [{ name: 'NusaShell', path: '/home/dev/workspace/NusaShell' }] },
    '/home/dev/workspace/NusaShell': { path: '/home/dev/workspace/NusaShell', parent: '/home/dev/workspace', entries: [] },
  };
  const rpc = async (method, payload) => {
    calls.push([method, payload]);
    return listings[payload.path] || listings[''];
  };
  let applied = '';
  initWorkspaceBrowser(rootElement, { rpc, onApply: (path) => { applied = path; } });
  rootElement.querySelector('#workspace-browser-trigger').click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(rootElement.querySelector('#model-config-view').hidden, true);
  assert.equal(rootElement.querySelector('#workspace-browser-view').hidden, false);
  assert.deepEqual([...rootElement.querySelectorAll('[data-breadcrumb-path]')].map((item) => item.textContent), ['/', 'home', 'dev']);

  rootElement.querySelector('[data-folder-path="/home/dev/workspace"]').click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  rootElement.querySelector('[data-folder-path="/home/dev/workspace/NusaShell"]').click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(rootElement.querySelector('#workspace-current-path').textContent, '/home/dev/workspace/NusaShell');
  assert.equal(rootElement.querySelector('#workspace-up').disabled, false);
  assert.equal(rootElement.querySelector('#workspace-empty').hidden, false);
  assert.equal(rootElement.querySelector('#workspace-empty').textContent, 'No folders here');
  rootElement.querySelector('#workspace-up').click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(rootElement.querySelector('#workspace-current-path').textContent, '/home/dev/workspace');
  rootElement.querySelector('#workspace-use-folder').click();
  assert.equal(applied, '/home/dev/workspace');
  assert.equal(rootElement.querySelector('#workspace-browser-trigger strong').textContent, 'workspace');
  assert.equal(rootElement.querySelector('[data-workspace-path]').textContent, '/home/dev/workspace');
  assert.equal(rootElement.querySelector('#workspace-browser-view').hidden, true);
  assert.equal(rootElement.querySelector('#model-config-view').hidden, false);
  assert.deepEqual(calls[0], ['agent.workspace.list-dirs', { path: '' }]);
});

test('workspace browser accepts Windows drive listings from the host without a fake platform switch', async () => {
  const { initWorkspaceBrowser } = await import(`../page/conversation/workspace-browser.js?test=${Date.now()}`);
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const rootElement = dom.window.document.querySelector('#message-form');

  const rpc = async (_method, { path }) => path === 'D:\\'
    ? { path: 'D:\\', parent: 'windows:computer', entries: [{ name: 'workspace', path: 'D:\\workspace' }] }
    : { path: 'windows:computer', entries: [{ name: 'Local Disk (C:)', path: 'C:\\' }, { name: 'Data (D:)', path: 'D:\\' }] };
  initWorkspaceBrowser(rootElement, { rpc });
  rootElement.querySelector('#workspace-browser-trigger').click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.deepEqual([...rootElement.querySelectorAll('.workspace-folder strong')].map((item) => item.textContent), ['Local Disk (C:)', 'Data (D:)']);
  assert.equal(rootElement.querySelector('#workspace-up').disabled, true);

  const folderAt = (path) => [...rootElement.querySelectorAll('[data-folder-path]')]
    .find((folder) => folder.dataset.folderPath === path);
  folderAt('D:\\').click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.deepEqual([...rootElement.querySelectorAll('[data-breadcrumb-path]')].map((item) => item.textContent), ['This PC', 'D:']);
});

test('composer route context follows the selected model provider', async () => {
  const { routeContextForModel } = await import(`../page/conversation/composer.js?test=${Date.now()}`);

  assert.deepEqual(routeContextForModel('openrouter:anthropic/claude-sonnet-4.5'), {
    label: 'Upstream',
    visible: true,
    options: [
      { text: 'Auto route', value: '' },
      { text: 'Anthropic', value: 'anthropic' },
      { text: 'Google Vertex', value: 'google-vertex' },
      { text: 'Amazon Bedrock', value: 'amazon-bedrock' },
    ],
  });
  assert.equal(routeContextForModel('codex:gpt-5.4-codex').label, 'Account');
  assert.equal(routeContextForModel('codex:gpt-5.4-codex').options[0].text, 'Auto rotation');
  assert.deepEqual(routeContextForModel('9router:deepseek/deepseek-v4'), {
    label: 'Route',
    visible: false,
    options: [],
  });
});

test('agent answers delegate markdown, code, and diagrams to shared renderers', async () => {
  const source = await readFile(new URL('shared/markdown-body.js', root), 'utf8');
  const agentCSS = await readFile(new URL('widgets/agent-message.css', root), 'utf8');
  // Resolve the shared mermaid renderer through markdown-body's own import so
  // the assertion holds before and after promotion rewrites the specifier.
  const markdownBodyURL = new URL('shared/markdown-body.js', root);
  const mermaidSpec = source.match(/from '([^']*mermaid-render\.js)'/)?.[1];
  assert.ok(mermaidSpec, 'markdown-body.js must import the shared mermaid renderer');
  const mermaidRenderer = await readFile(new URL(mermaidSpec, markdownBodyURL), 'utf8');

  assert.match(source, /js\/markdown\.js/);
  assert.match(source, /js\/highlight-render\.js/);
  assert.match(source, /js\/mermaid-render\.js/);
  assert.match(source, /vendor\/highlightjs\/highlight\.min\.js/);
  assert.match(source, /vendor\/mermaid\/mermaid\.min\.js/);
  assert.match(agentCSS, /\.markdown-table-scroll \{[^}]*overflow-x: auto/);
  assert.match(agentCSS, /\.answer :is\(th, td\) \{[^}]*max-width: 400px/);
  assert.match(agentCSS, /\.answer \.mermaid-block \{[^}]*background: linear-gradient\(/s);
  assert.match(agentCSS, /\.answer \.mermaid-block svg \{[^}]*shape-rendering: geometricPrecision/s);
  assert.match(mermaidRenderer, /themeVariables:\s*\{/);
  assert.match(mermaidRenderer, /primaryColor:\s*['"]#121b2e['"]/);
});

test('tool delta bursts are batched per frame before mutating live widgets', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  assert.match(app, /const pendingToolFrames = \[\]/);
  assert.match(app, /let toolBatchFrame = 0/);
  assert.match(app, /toolBatchFrame = requestAnimationFrame\(flushToolFrames\)/);
  assert.match(app, /frame\.kind === 'tool'[\s\S]*queueToolFrame\(frame\)/);
  assert.match(app, /flushToolFrames\(\);[\s\S]*applyRoundDone/);
});

test('code blocks open a lazy CodeMirror surface with the vendored Dracula theme', async () => {
  const surface = await readFile(new URL('shared/code-surface.js', root), 'utf8');
  const markdownBody = await readFile(new URL('shared/markdown-body.js', root), 'utf8');
  const agentCSS = await readFile(new URL('widgets/agent-message.css', root), 'utf8');
  const { attachCodeEditorButtons, codeModeFor } = await import(`../shared/code-surface.js?test=${Date.now()}`);
  const dom = new JSDOM('<div id="answer"><pre><code class="language-go">package main</code></pre></div>', { url: 'http://localhost/' });
  const answer = dom.window.document.querySelector('#answer');

  attachCodeEditorButtons(answer);
  attachCodeEditorButtons(answer);

  assert.equal(answer.querySelectorAll('.code-editor-trigger').length, 1, 'code controls are idempotent during streamed markdown updates');
  assert.equal(answer.querySelector('.code-editor-trigger').getAttribute('aria-label'), 'Open Go code in editor');
  assert.equal(answer.querySelector('.code-language-label').textContent, 'Go', 'code blocks expose their language without wasting a control slot');
  assert.equal(codeModeFor('go'), 'text/x-go');
  assert.equal(codeModeFor('typescript'), 'text/typescript');
  assert.equal(codeModeFor('unknown'), 'text/plain');
  // CodeMirror ships lazily: ensureCodeMirror appends the vendored bundle's
  // <script> tags in order only when a reader opens a source surface, so no
  // eager codemirror tags belong in index.html.
  assert.match(surface, /vendor\/codemirror/);
  assert.match(surface, /function ensureCodeMirror|ensureCodeMirror\s*=/);
  assert.match(surface, /script\.async = false/);
  assert.match(surface, /theme:\s*'dracula'/);
  assert.match(markdownBody, /attachCodeEditorButtons/);
  assert.match(agentCSS, /code-editor-trigger/);
});

test('CodeMirror source surface stays read-only, searchable, and dismissible', async () => {
  const { openCodeSurface } = await import(`../shared/code-surface.js?surface=${Date.now()}`);
  const dom = new JSDOM('<button id="origin">Open source</button>', { url: 'http://localhost/' });
  const { document, CodeMirror } = dom.window;
  const calls = [];
  dom.window.CodeMirror = (host, options) => {
    calls.push(options);
    const editor = document.createElement('div');
    editor.className = 'CodeMirror';
    host.append(editor);
    return { getWrapperElement: () => editor, focus: () => {} };
  };
  document.querySelector('#origin').focus();

  const surface = openCodeSurface({ document, source: 'package main', language: 'go', title: 'main.go' });
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.equal(surface.overlay.querySelector('[role="dialog"]')?.getAttribute('aria-label'), 'main.go');
  assert.deepEqual(calls[0], {
    value: 'package main', mode: 'text/x-go', theme: 'dracula', readOnly: true,
    lineNumbers: true, styleActiveLine: true, matchBrackets: true, lineWrapping: false, viewportMargin: 12,
  });
  assert.match(surface.overlay.querySelector('.CodeMirror').getAttribute('aria-label'), /Go source/);
  surface.close();
  assert.equal(document.querySelector('.code-surface-backdrop'), null);
  assert.equal(document.activeElement.id, 'origin');
});

test('vendored CodeMirror registers the source modes used by the code surface', async () => {
  // The lazy loader in shared/code-surface.js owns the bundle file list; the
  // test evaluates the same files so drift between the two is impossible. Its
  // vendorBase is document-relative, which maps onto this suite's root in both
  // the prototype tree and the promoted frontend/.
  const surfaceSource = await readFile(new URL('shared/code-surface.js', root), 'utf8');
  const vendorBase = surfaceSource.match(/vendorBase = '([^']+)'/)?.[1];
  assert.ok(vendorBase, 'code-surface pins the vendored CodeMirror base path');
  const scriptsBlock = surfaceSource.match(/const scripts = \[([\s\S]*?)\]/)?.[1] || '';
  const resources = [...scriptsBlock.matchAll(/'([^']+\.js)'/g)].map((item) => item[1]);
  assert.ok(resources.length >= 2, 'the lazy loader lists the core bundle plus its modes');

  const dom = new JSDOM('', { url: 'http://localhost/', runScripts: 'outside-only' });
  const vendorURL = new URL(`${vendorBase}/`, root);
  for (const resource of resources) dom.window.eval(await readFile(new URL(resource, vendorURL), 'utf8'));

  assert.equal(typeof dom.window.CodeMirror, 'function');
  for (const mode of ['text/javascript', 'text/typescript', 'text/html', 'text/x-markdown', 'text/x-sh', 'text/x-python', 'text/x-go', 'text/x-yaml']) {
    assert.notEqual(dom.window.CodeMirror.getMode({ indentUnit: 2 }, mode).name, 'null', mode);
  }
});

test('agent copy action uses the source markdown instead of code-surface control text', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  const copied = [];
  const originalNavigator = Object.getOwnPropertyDescriptor(globalThis, 'navigator');
  try {
    Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
    Object.defineProperty(globalThis.navigator, 'clipboard', { configurable: true, value: { writeText: async (value) => copied.push(value) } });
    await import(`../widgets/agent-message.js?copy=${Date.now()}`);
    const widget = document.createElement('agent-message-widget');
    widget.event = { id: 'copy-code', markdown: '```go\npackage main\n```' };
    document.body.append(widget);
    widget.shadowRoot.querySelector('.copy-answer').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.deepEqual(copied, ['```go\npackage main\n```']);
  } finally {
    if (originalNavigator) Object.defineProperty(globalThis, 'navigator', originalNavigator);
    else delete globalThis.navigator;
    restoreDOMGlobals(previous);
  }
});

test('failed agent message renders an accessible error and dispatches retry', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/agent-message.js?error=${Date.now()}`);
    const widget = document.createElement('agent-message-widget');
    widget.event = {
      id: 'failed-answer', messageId: 'message-1', type: 'agent.message', model: 'OpenAI/gpt-5.6-sol',
      status: 'error', error: 'Provider unavailable', canRetry: true, markdown: 'Partial answer.',
    };
    document.body.append(widget);

    const error = widget.shadowRoot.querySelector('.agent-error-state');
    assert.equal(error.getAttribute('role'), 'alert');
    assert.match(error.textContent, /Provider unavailable/);
    const retry = widget.shadowRoot.querySelector('.retry-answer');
    assert.ok(retry);
    const retried = new Promise((resolve) => widget.addEventListener('retry-turn', resolve, { once: true }));
    retry.click();
    assert.equal((await retried).detail.messageId, 'message-1');

    widget.event = { ...widget.event, status: 'done', error: '', canRetry: false };
    assert.equal(widget.shadowRoot.querySelector('.agent-error-state').hidden, true);
    assert.equal(widget.shadowRoot.querySelector('.retry-answer').hidden, true);
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('agent message routes local links to preview, external links outward, and makes inline media zoomable', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/agent-message.js?test=${Date.now()}`);
    const widget = document.createElement('agent-message-widget');
    widget.event = {
      id: 'answer-rich', type: 'agent.message', model: 'Luna', time: '', usage: '',
      markdown: '[local](/tmp/report.md:12) [external](https://example.com/docs)\n\n![map](file:///tmp/map.svg)',
    };
    document.body.append(widget);

    const local = widget.shadowRoot.querySelector('a[data-local-path]');
    const external = widget.shadowRoot.querySelector('a[href="https://example.com/docs"]');
    assert.equal(local.dataset.localPath, '/tmp/report.md:12');
    assert.match(local.href, /\/local-file\?path=%2Ftmp%2Freport\.md/);
    assert.equal(external.target, '_blank');
    assert.match(external.rel, /noopener/);
    const opened = new Promise((resolve) => widget.addEventListener('local-file-open', resolve, { once: true }));
    local.click();
    assert.equal((await opened).detail.path, '/tmp/report.md:12');
    assert.ok(widget.shadowRoot.querySelector('.media-zoom-trigger[aria-label="Zoom image"]'));
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('streamed markdown preserves settled blocks instead of rebuilding the answer shadow tree', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/agent-message.js?stream=${Date.now()}`);
    const widget = document.createElement('agent-message-widget');
    widget.event = { id: 'live', type: 'agent.message', model: 'Luna', markdown: 'Settled paragraph.' };
    document.body.append(widget);
    const settled = widget.shadowRoot.querySelector('.answer > p');
    widget.event = { id: 'live', type: 'agent.message', model: 'Luna', markdown: 'Settled paragraph.\n\nNew streaming paragraph.' };
    assert.equal(widget.shadowRoot.querySelector('.answer > p'), settled);
    assert.equal(widget.shadowRoot.querySelectorAll('.answer > p').length, 2);
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('user message renders persisted image, media, and file attachments with useful actions', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/user-message.js?test=${Date.now()}`);
    const widget = document.createElement('user-message-widget');
    widget.event = {
      id: 'user-rich', content: 'Attached', time: '', attachments: [
        { type: 'image', name: 'island.svg', file_path: '/tmp/island.svg' },
        { type: 'audio', name: 'waves.ogg', file_path: '/tmp/waves.ogg' },
        { type: 'file', name: 'notes.md', file_path: '/tmp/notes.md' },
      ],
    };
    document.body.append(widget);
    assert.equal(widget.shadowRoot.querySelectorAll('[data-attachment]').length, 3);
    const body = widget.shadowRoot.querySelector('.message-markdown');
    assert.equal(body.nextElementSibling?.className, 'message-attachments', 'attachments stay directly below the user content');
    assert.match(widget.shadowRoot.querySelector('img').src, /\/local-file\?path=%2Ftmp%2Fisland\.svg/);
    assert.equal(widget.shadowRoot.querySelector('audio').preload, 'metadata');
    const opened = new Promise((resolve) => widget.addEventListener('local-file-open', resolve, { once: true }));
    widget.shadowRoot.querySelector('[data-file-path="/tmp/notes.md"]').click();
    assert.equal((await opened).detail.path, '/tmp/notes.md');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('user message updates patch the mounted card and keep unchanged attachment media', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/user-message.js?patch=${Date.now()}`);
    const { widgetStyles } = await sharedStylesImport('widgets/user-message.js');
    const widget = document.createElement('user-message-widget');
    const event = { id: 'user-patch', content: 'Look', time: '09:41 PM', attachments: [{ type: 'image', name: 'a.png', file_path: '/tmp/a.png' }] };
    widget.event = event;
    document.body.append(widget);
    const article = widget.shadowRoot.querySelector('article');
    const image = widget.shadowRoot.querySelector('img');
    widgetStyles.user.forEach((sheet, index) => assert.equal(widget.shadowRoot.adoptedStyleSheets[index], sheet));

    widget.event = { ...event, time: '09:42 PM', attachments: event.attachments.map((item) => ({ ...item })) };
    assert.equal(widget.shadowRoot.querySelector('article'), article, 'snapshot merges must not rebuild the shadow tree');
    assert.equal(widget.shadowRoot.querySelector('img'), image, 'unchanged attachments keep their loaded media');
    assert.equal(widget.shadowRoot.querySelector('.widget-time').textContent, '09:42 PM');
    assert.equal(widget.shadowRoot.querySelector('link'), null);

    widget.event = { ...event, content: 'Look again', attachments: [{ type: 'file', name: 'b.md', file_path: '/tmp/b.md' }] };
    assert.equal(widget.shadowRoot.querySelector('article'), article);
    assert.equal(widget.shadowRoot.querySelector('img'), null, 'changed attachments rebuild only the gallery');
    assert.equal(widget.shadowRoot.querySelectorAll('.message-attachments').length, 1);
    assert.match(widget.shadowRoot.querySelector('.message-body').textContent, /Look again/);
    const composed = new Promise((resolve) => widget.addEventListener('widget-compose', resolve, { once: true }));
    widget.shadowRoot.querySelector('.edit-message').click();
    assert.equal((await composed).detail.content, 'Look again', 'edit uses the latest event');

    widget.event = { ...event, attachments: [] };
    assert.equal(widget.shadowRoot.querySelector('.message-attachments'), null);
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('user messages render Markdown while preserving intentional line breaks', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/user-message.js?markdown=${Date.now()}`);
    const widget = document.createElement('user-message-widget');
    widget.event = {
      id: 'user-markdown',
      content: 'First line\nsecond line\n\n*emphasis*\n\n- one\n- two\n\n```js\nconst value = 1;\n```',
      time: '',
    };
    document.body.append(widget);

    const body = widget.shadowRoot.querySelector('.message-markdown');
    assert.equal(body.querySelector('em').textContent, 'emphasis');
    assert.deepEqual([...body.querySelectorAll('li')].map((item) => item.textContent), ['one', 'two']);
    assert.equal(body.querySelector('code').className, 'language-js');
    assert.match(body.querySelector('p').textContent, /First line\nsecond line/);
    const css = await readFile(new URL('widgets/user-message.css', root), 'utf8');
    assert.match(css, /\.message-markdown p[^}]*white-space:\s*pre-wrap/);
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('user messages use a deliberate compact identity treatment', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/user-message.js?identity=${Date.now()}`);
    const widget = document.createElement('user-message-widget');
    widget.event = { id: 'user-identity', content: 'A short request.', time: '09:42 PM' };
    document.body.append(widget);

    assert.equal(widget.shadowRoot.querySelector('.user-mark')?.textContent, 'Y');
    assert.equal(widget.shadowRoot.querySelector('.widget-label')?.textContent, 'You');
    const css = await readFile(new URL('widgets/user-message.css', root), 'utf8');
    assert.match(css, /\.user-mark\s*\{[^}]*width:\s*21px;[^}]*border-radius:\s*50%;/s);
    assert.match(css, /\.user-widget\s*\{[^}]*border-right:\s*2px/s);
    assert.match(css, /\.message-body\s*\{[^}]*font-size:\s*13px;[^}]*line-height:\s*1\.6;/s);
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('clipboard files merge into the attachment queue without duplicate paste entries', async () => {
  const { filesFromDataTransfer, mergeFiles } = await import(`../page/conversation/attachment-input.js?test=${Date.now()}`);
  const image = { name: 'island.png', size: 42, lastModified: 10, type: 'image/png' };
  const text = { name: 'notes.md', size: 7, lastModified: 11, type: 'text/markdown' };
  const transfer = {
    items: [
      { kind: 'string', getAsFile: () => null },
      { kind: 'file', getAsFile: () => image },
      { kind: 'file', getAsFile: () => image },
    ],
    files: [image, text],
  };
  assert.deepEqual(filesFromDataTransfer(transfer), [image, text]);
  assert.deepEqual(mergeFiles([image], [image, text]), [image, text]);
});

test('send and steering keep serialized attachments on the optimistic user event', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  assert.match(app, /const displayName = file\.name \|\| 'Attachment'/);
  assert.match(app, /const outgoingAttachments = await Promise\.all\(attachments\.map\(fileAttachment\)\)/);
  assert.match(app, /inspectAttachmentContent/);
  assert.match(app, /toDataURL/);
  assert.match(app, /const MAX_ATTACHMENTS = 4/);
  assert.match(app, /attachments:\s*outgoingAttachments/);
  assert.match(app, /const userEvent = \{[\s\S]*attachments:\s*outgoingAttachments/);
  assert.match(app, /composer\.addEventListener\('paste'/);
});

test('shell actions use a vendored icon library instead of authored SVG paths', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const document = new JSDOM(html).window.document;

  assert.match(html, /vendor\/lucide\.min\.js/);
  assert.match(html, /data-lucide="paperclip"/);
  assert.match(html, /data-lucide="send"/);
  assert.match(html, /id="stop-turn-button"[\s\S]*data-lucide="square"/);
  assert.equal(document.querySelector('.attachment-button svg'), null);
  assert.equal(document.querySelector('.send-button svg'), null);
  await readFile(new URL('vendor/lucide.min.js', root), 'utf8');
  await readFile(new URL('vendor/LUCIDE-LICENSE', root), 'utf8');
});

test('composer sends a steer without sacrificing a distinct active-turn stop control', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('shell.css', root), 'utf8');
  const document = new JSDOM(html).window.document;

  assert.equal(document.querySelector('#stop-turn-button').type, 'button');
  assert.equal(document.querySelector('#stop-turn-button').hidden, true);
  assert.match(app, /agent\.turns\.steer/);
  assert.match(app, /agent\.turns\.stop/);
  assert.match(app, /function syncComposerAction\(/);
  assert.match(css, /\.send-button, \.stop-turn-button/);
  assert.match(css, /\.stop-turn-button\[hidden\] \{ display: none; \}/);
  assert.doesNotMatch(css, /#message-form\[data-turn-mode="steer"\] \.send-button/);
});

test('running composer exposes a restrained rotating border without extra runtime updates', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('shell.css', root), 'utf8');

  assert.match(app, /messageForm\.dataset\.turnMode\s*=\s*steering\s*\?\s*['"]steer['"]\s*:\s*['"]send['"]/);
  assert.match(css, /@property\s+--composer-border-angle\s*\{[\s\S]*syntax:\s*["']<angle>["'][\s\S]*initial-value:\s*0deg/s);
  assert.match(css, /\.composer\[data-turn-mode="steer"\]\s*\{[^}]*border-color:\s*transparent[^}]*background:\s*linear-gradient\([^;]+\)\s*padding-box,\s*conic-gradient\([^;]+\)\s*border-box[^}]*animation:\s*composer-border-spin\s+5\.5s\s+linear\s+infinite/s);
  assert.match(css, /@keyframes\s+composer-border-spin\s*\{[\s\S]*--composer-border-angle:\s*1turn/);
  assert.match(css, /@media\s*\(prefers-reduced-motion:\s*reduce\)[\s\S]*\.composer\[data-turn-mode="steer"\]\s*\{[^}]*animation:\s*none/s);
  assert.match(css, /\.composer\[data-turn-mode="steer"\]:focus-within\s*\{[^}]*border-color:\s*transparent/s);
});

test('experimental Agent surfaces turn failures, provider retries, and retry RPCs', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const liveData = await readFile(new URL('page/conversation/live-data.js', root), 'utf8');
  const message = await readFile(new URL('widgets/agent-message.js', root), 'utf8');
  const css = await readFile(new URL('widgets/agent-message.css', root), 'utf8');

  assert.match(app, /applyTurnError/);
  assert.match(app, /on\('agent\.turn\.error'/);
  assert.match(app, /retry-turn/);
  assert.match(app, /agent\.turns\.retry/);
  assert.match(app, /on\('agent\.provider\.retry'/);
  assert.match(app, /providerRetryLabel/);
  assert.match(liveData, /function providerRetryLabel\(/);
  assert.match(liveData, /function applyTurnError\(/);
  assert.match(message, /agent-error-state/);
  assert.match(message, /retry-turn/);
  assert.match(css, /\.agent-error-state\s*\{/);
  assert.match(css, /\.retry-answer\s*\{/);
});

test('exec stop requests stay scoped to one streamed tool call', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const liveData = await readFile(new URL('page/conversation/live-data.js', root), 'utf8');
  assert.match(app, /stream\.addEventListener\('tool-stop-request'/);
  assert.match(app, /rpc\('agent\.tools\.stop',\s*\{ run_id: runID, tool_call_id: toolCallId \}\)/);
  assert.match(liveData, /runId: context\.runId \|\| frame\.run_id/);
});

test('task widget state toggles only the selected task', () => {
  const tasks = [
    { id: 'inspect', label: 'Inspect current UI', done: true },
    { id: 'prototype', label: 'Build prototype', done: false },
  ];

  assert.deepEqual(toggleTask(tasks, 'prototype'), [
    tasks[0],
    { ...tasks[1], done: true },
  ]);
  assert.deepEqual(tasks.map(({ done }) => done), [true, false], 'input stays immutable');
});

test('terminal tool widget patches a stable live-output viewport from structured args', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?test=${Date.now()}`);
    const widget = document.createElement('terminal-tool-widget');
    widget.event = {
      id: 'tool-1',
      type: 'tool.run',
      tool: 'exec',
      args: { command: 'node -c dark', cwd: '/workspace' },
      output: 'viewport: 1440x900\nconsole: clean',
      status: 'complete',
      duration: '1.8s',
    };
    document.body.append(widget);

    const article = widget.shadowRoot.querySelector('article');
    const { widgetStyles } = await sharedStylesImport('widgets/tools/widgets.js');
    assert.equal(widget.shadowRoot.querySelector('link'), null, 'shadow roots never load CSS through <link>');
    assert.deepEqual(widget.shadowRoot.adoptedStyleSheets, widgetStyles.tool);
    const command = widget.shadowRoot.querySelector('.terminal-command');
    assert.equal(command.textContent, 'node -c dark');
    assert.equal(command.type, 'button');
    command.click();
    assert.equal(command.classList.contains('is-expanded'), true, 'the full command is available on demand');
    assert.equal(widget.shadowRoot.querySelector('.terminal-cwd').textContent, '/workspace');
    assert.match(widget.shadowRoot.querySelector('.terminal-live-output pre').textContent, /console: clean/);

    widget.event = { ...widget.event, output: 'viewport: 1440x900\nconsole: clean\nexec: streamed' };
    assert.equal(widget.shadowRoot.querySelector('article'), article, 'tool deltas must not remount the shadow tree');
    document.body.append(document.createElement('section'));
    document.querySelector('section').append(widget);
    widget.shadowRoot.adoptedStyleSheets.forEach((sheet, index) => {
      assert.equal(sheet, widgetStyles.tool[index], 'deltas and moves keep the same shared sheet objects');
    });
    assert.match(widget.shadowRoot.querySelector('.terminal-live-output pre').textContent, /exec: streamed/);
    const terminalCSS = await readFile(new URL('widgets/tools/tool-widgets.css', root), 'utf8');
    const styles = await readFile(new URL('shared/widget-styles.js', root), 'utf8');
    assert.match(terminalCSS, /\.terminal-command\.is-expanded/);
    assert.match(terminalCSS, /\.terminal-command\.hljs/);
    assert.match(styles, /vendor\/highlightjs\/github-dark\.min\.css/);
    assert.match(terminalCSS, /\.terminal-live-output pre \{[^}]*max-height: calc\(5 \* 1\.6em\)/);
    assert.match(terminalCSS, /overflow-y: auto/);
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('exec command highlighting recognizes Bash, PowerShell, and CMD without remounting', async () => {
  const { commandLanguage, highlightCommand } = await import(`../widgets/tools/command-highlight.js?test=${Date.now()}`);
  assert.equal(commandLanguage({ shell: '/bin/bash' }, 'echo ready'), 'bash');
  assert.equal(commandLanguage({ shell: 'sh' }, 'printf ready'), 'bash');
  assert.equal(commandLanguage({ shell: 'pwsh' }, 'Get-ChildItem'), 'powershell');
  assert.equal(commandLanguage({ shell: 'cmd.exe' }, 'dir'), 'dos');
  assert.equal(commandLanguage({}, 'pwsh -NoProfile -Command "$env:TEMP"'), 'powershell');
  assert.equal(commandLanguage({}, 'cmd.exe /c echo %TEMP%'), 'dos');

  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  const calls = [];
  dom.window.hljs = {
    getLanguage: () => null,
    registerLanguage: () => {},
    highlight(source, options) {
      calls.push({ source, language: options.language });
      return { value: '<span class="hljs-built_in">Get-ChildItem</span>' };
    },
  };
  try {
    const result = await highlightCommand('Get-ChildItem', { shell: 'pwsh' });
    assert.deepEqual(result, {
      html: '<span class="hljs-built_in">Get-ChildItem</span>',
      language: 'powershell',
      highlighted: true,
    });
    assert.deepEqual(calls, [{ source: 'Get-ChildItem', language: 'powershell' }]);

    await import(`../widgets/tools/widgets.js?highlight-widget=${Date.now()}`);
    const widget = document.createElement('terminal-tool-widget');
    widget.event = {
      id: 'tool-highlight', tool: 'exec', args: { command: 'Get-ChildItem', shell: 'pwsh' },
      output: 'ready', status: 'complete',
    };
    document.body.append(widget);
    await new Promise((resolve) => setTimeout(resolve, 0));
    const article = widget.shadowRoot.querySelector('article');
    const command = widget.shadowRoot.querySelector('.terminal-command');
    assert.equal(command.classList.contains('hljs'), true);
    assert.equal(command.querySelector('.hljs-built_in')?.textContent, 'Get-ChildItem');
    assert.equal(command.dataset.commandLanguage, 'powershell');

    widget.event = { ...widget.event, output: 'updated' };
    assert.equal(widget.shadowRoot.querySelector('article'), article, 'output deltas must not remount the highlighted command');
    assert.equal(widget.shadowRoot.querySelector('.terminal-command'), command, 'output deltas must preserve command markup');
    assert.equal(calls.length, 2, 'command source is highlighted once per command, not once per output delta');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('running exec widgets expose a per-call stop action with run context', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?stop=${Date.now()}`);
    const widget = document.createElement('terminal-tool-widget');
    widget.event = {
      id: 'tool-running', type: 'tool.run', tool: 'exec', toolCallId: 'call-7', runId: 'run-7',
      args: { command: 'sleep 30' }, output: '', status: 'running', duration: '',
    };
    document.body.append(widget);

    const stop = widget.shadowRoot.querySelector('.tool-stop');
    assert.ok(stop);
    assert.equal(stop.hidden, false);
    const request = new Promise((resolve) => widget.addEventListener('tool-stop-request', resolve, { once: true }));
    stop.click();
    assert.deepEqual((await request).detail, { runId: 'run-7', toolCallId: 'call-7' });

    widget.event = { ...widget.event, status: 'complete' };
    assert.equal(widget.shadowRoot.querySelector('.tool-stop')?.hidden, true);
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('file, search, task, and subagent tools expose actions matching their domain', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?test=${Date.now()}`);
    const cases = [
      ['file-tool-widget', { id: 'f', tool: 'file_patch', args: { path: '/workspace/app.js' }, status: 'ok' }, '.tool-file-path', '/workspace/app.js', 'artifact-open'],
      ['search-tool-widget', { id: 's', tool: 'grep', args: { pattern: 'widget', path: '/workspace' }, status: 'ok' }, '.search-query', 'widget', null],
      ['task-tool-widget', { id: 't', tool: 'todo', args: { mode: 'add', items: [{ id: 'a', content: 'Map tools' }] }, status: 'ok' }, '.task-count', '1 item', 'task-open'],
      ['subagent-tool-widget', {
        id: 'a', tool: 'subagent', args: { title: 'Widget audit', agent_id: 'cursor' }, status: 'running',
        output: '---\nruns:\n  - id: acprun_abc123\n    title: Widget audit\n    status: running\nasync: true\ncount: 1\n---',
      }, '.subagent-title', 'Widget audit', 'subagent-open'],
      ['automation-tool-widget', { id: 'pause', tool: 'sleep', args: { seconds: 90 }, status: 'running' }, '.simple-primary', 'Pause · 1m 30s', null],
    ];
    for (const [tag, event, selector, text, emitted] of cases) {
      const widget = document.createElement(tag);
      widget.event = event;
      document.body.append(widget);
      assert.match(widget.shadowRoot.querySelector(selector).textContent, new RegExp(text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
      if (tag === 'automation-tool-widget') {
        assert.equal(widget.shadowRoot.querySelector('.instrument-kind').textContent, 'Pause');
        assert.equal(widget.shadowRoot.querySelector('.instrument-title').textContent, '');
      }
      if (tag === 'subagent-tool-widget') assert.equal(widget.shadowRoot.querySelector('.subagent-agent').textContent, 'ACP · cursor');
      if (emitted) {
        const action = new Promise((resolve) => widget.addEventListener(emitted, resolve, { once: true }));
        widget.shadowRoot.querySelector('[data-primary-action]').click();
        const emission = await action;
        assert.ok(emission);
        if (tag === 'subagent-tool-widget') assert.equal(emission.detail.id, 'acprun_abc123');
      }
      widget.remove();
    }
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('subagent open action deep-links the child run id carried by the tool result', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?subagent-open-id=${Date.now()}`);
    const open = async (event) => {
      const widget = document.createElement('subagent-tool-widget');
      widget.event = event;
      document.body.append(widget);
      const action = new Promise((resolve) => widget.addEventListener('subagent-open', resolve, { once: true }));
      widget.shadowRoot.querySelector('[data-primary-action]').click();
      const emission = await action;
      widget.remove();
      return emission.detail.id;
    };

    // The spawn result yamlmd block is the surface that carries the child run
    // id — never the args. A multi-run spawn targets the first listed run.
    assert.equal(await open({
      id: 's1', tool: 'subagent', args: { title: 'Audit', agent_id: 'cursor' }, status: 'ok',
      output: '---\nruns:\n  - id: acprun_abc123\n    title: Audit\n    status: running\n  - id: acprun_def456\n    title: Second\n    status: running\nasync: true\ncount: 2\n---',
    }), 'acprun_abc123');

    // Internal delegate runs use the run_* prefix; a completed call carries
    // only the brief terminal line in presentation.result.text.
    assert.equal(await open({
      id: 's2', tool: 'delegate', args: { title: 'Summarize' }, status: 'ok',
      presentation: { result: { text: 'Subagent run run_9f2 completed. Full result delivered in the subagent_result tool call.' } },
    }), 'run_9f2');

    // A synthetic subagent_result call keeps its explicit args.id priority.
    assert.equal(await open({
      id: 's3', tool: 'subagent_result', args: { id: 'acprun_result1' }, status: 'ok',
      output: '---\nstatus: done\n---',
    }), 'acprun_result1');

    // No result at all: nothing to deep-link, the handler just opens the dock.
    assert.equal(await open({
      id: 's4', tool: 'subagent', args: { title: 'Audit', agent_id: 'cursor' }, status: 'running',
    }), '');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('file tool cards only offer file preview when the target can resolve in /local-file', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?test=${Date.now()}`);
    const mount = (event) => {
      const widget = document.createElement('file-tool-widget');
      widget.event = event;
      document.body.append(widget);
      return widget;
    };
    const previewButton = (widget) =>
      [...widget.shadowRoot.querySelectorAll('button')].find((button) => button.textContent === 'Preview file');

    // Directory targets and deleted paths can never resolve in /local-file.
    for (const tool of ['file_list', 'file_mkdir', 'file_delete']) {
      const widget = mount({ id: `f-${tool}`, tool, args: { path: '/workspace/docs' }, status: 'ok' });
      assert.equal(widget.shadowRoot.querySelector('[data-primary-action]'), null, `${tool} must not expose a primary action`);
      assert.equal(previewButton(widget), undefined, `${tool} must not render a Preview file button`);
      widget.remove();
    }

    // file_info reports the stat flags in its yamlmd result block.
    for (const result of ['exists: true\nname: docs\ndir: true\nmode: drwxr-xr-x', 'exists: false']) {
      const widget = mount({
        id: 'f-info-skip', tool: 'file_info', args: { path: '/workspace/docs' }, status: 'ok',
        presentation: { result: { text: result } },
      });
      assert.equal(widget.shadowRoot.querySelector('[data-primary-action]'), null, `file_info must not preview: ${result}`);
      assert.equal(previewButton(widget), undefined);
      widget.remove();
    }
    const info = mount({
      id: 'f-info-file', tool: 'file_info', args: { path: '/workspace/app.js' }, status: 'ok',
      presentation: { result: { text: 'exists: true\nname: app.js\ndir: false\nmode: -rw-r--r--' } },
    });
    const infoOpen = new Promise((resolve) => info.addEventListener('artifact-open', resolve, { once: true }));
    info.shadowRoot.querySelector('[data-primary-action]').click();
    assert.equal((await infoOpen).detail.path, '/workspace/app.js');
    info.remove();

    // Real file targets keep the preview action.
    const reader = mount({ id: 'f-read', tool: 'file_read', args: { path: '/workspace/app.js' }, status: 'ok' });
    const action = reader.shadowRoot.querySelector('[data-primary-action]');
    assert.equal(action.textContent, 'Preview file');
    const readOpen = new Promise((resolve) => reader.addEventListener('artifact-open', resolve, { once: true }));
    action.click();
    assert.equal((await readOpen).detail.path, '/workspace/app.js');
    reader.remove();
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('media widget owns a sandboxed inline preview and keeps file preview as a secondary action', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?test=${Date.now()}`);
    const widget = document.createElement('media-tool-widget');
    widget.event = { id: 'm', tool: 'show', args: { op: 'html', path: '/tmp/profile.html' }, status: 'ok' };
    document.body.append(widget);

    assert.equal(widget.shadowRoot.querySelector('.media-name').textContent, 'profile.html');
    const preview = widget.shadowRoot.querySelector('.media-preview');
    assert.equal(preview.hidden, true);
    const previewButton = widget.shadowRoot.querySelector('[data-primary-action]');
    previewButton.click();
    assert.equal(preview.hidden, false);
    assert.equal(previewButton.getAttribute('aria-expanded'), 'true');
    const frame = preview.querySelector('iframe');
    assert.match(frame.getAttribute('sandbox'), /allow-scripts/);
    assert.match(frame.src, /\/local-file\?path=%2Ftmp%2Fprofile\.html/);

    const opened = new Promise((resolve) => widget.addEventListener('artifact-open', resolve, { once: true }));
    widget.shadowRoot.querySelector('.media-open-preview').click();
    assert.equal((await opened).detail.path, '/tmp/profile.html');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('question tool renders choices as an answer affordance instead of a terminal', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?test=${Date.now()}`);
    const widget = document.createElement('question-tool-widget');
    widget.event = {
      id: 'q', tool: 'ask_question', status: 'running',
      args: { question: 'Choose the island?', options: [{ id: 'bali', label: 'Bali' }, { id: 'flores', label: 'Flores' }] },
    };
    document.body.append(widget);
    assert.equal(widget.shadowRoot.querySelector('.question-copy').textContent, 'Choose the island?');
    assert.deepEqual([...widget.shadowRoot.querySelectorAll('.question-option')].map((node) => node.textContent), ['Bali', 'Flores']);
    assert.equal(widget.shadowRoot.querySelector('.terminal-command'), null);
    const opened = new Promise((resolve) => widget.addEventListener('question-open', resolve, { once: true }));
    widget.shadowRoot.querySelector('[data-primary-action]').click();
    assert.ok(await opened);
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('question tool renders the prompt and completed answer as Markdown', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?question-markdown=${Date.now()}`);
    const widget = document.createElement('question-tool-widget');
    widget.event = {
      id: 'q-markdown', tool: 'ask_question', status: 'complete',
      args: { question: '**Plan**\n\n- keep the answer compact' },
      output: '**Answer**\n\n- use the dark route',
    };
    document.body.append(widget);

    const question = widget.shadowRoot.querySelector('.question-copy');
    assert.equal(question.querySelector('strong').textContent, 'Plan');
    assert.equal(question.querySelector('li').textContent, 'keep the answer compact');
    const toggle = widget.shadowRoot.querySelector('.instrument-toggle');
    toggle.click();
    const answer = widget.shadowRoot.querySelector('.instrument-markdown-detail');
    assert.equal(answer.querySelector('strong').textContent, 'Answer');
    assert.equal(answer.querySelector('li').textContent, 'use the dark route');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('settled question card reveals its recorded answer instead of re-entering the pending-ask flow', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?question-view-answer=${Date.now()}`);
    const widget = document.createElement('question-tool-widget');
    widget.event = {
      id: 'q-done', tool: 'ask_question', status: 'complete',
      args: { question: 'Pick one' },
      output: '**Bali**',
    };
    document.body.append(widget);

    const action = widget.shadowRoot.querySelector('[data-primary-action]');
    const toggle = widget.shadowRoot.querySelector('.instrument-toggle');
    const panel = widget.shadowRoot.querySelector('.instrument-detail');
    assert.equal(action.textContent, 'View answer');
    assert.equal(panel.hidden, true);

    let emitted = false;
    widget.addEventListener('question-open', () => { emitted = true; });
    action.click();
    assert.equal(emitted, false, 'a settled card never re-enters the pending-ask flow');
    assert.equal(panel.hidden, false, 'View answer reveals the existing answer disclosure');
    assert.equal(toggle.getAttribute('aria-expanded'), 'true');
    assert.equal(action.getAttribute('aria-expanded'), 'true');
    assert.equal(action.getAttribute('aria-controls'), panel.id);
    assert.equal(panel.querySelector('.instrument-markdown-detail strong').textContent, 'Bali');

    toggle.click();
    assert.equal(panel.hidden, true, 'the existing Answer disclosure still collapses the panel');
    assert.equal(action.getAttribute('aria-expanded'), 'false');
    assert.equal(emitted, false);

    action.click();
    assert.equal(panel.hidden, false, 'View answer can reopen the same disclosure');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('subagent tool renders its delegation prompt as Markdown', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?subagent-markdown=${Date.now()}`);
    const widget = document.createElement('subagent-tool-widget');
    widget.event = {
      id: 'subagent-markdown', tool: 'subagent', status: 'running',
      args: { title: 'Cursor', prompt: '**Review**\n\n- inspect the stream' },
    };
    document.body.append(widget);
    const prompt = widget.shadowRoot.querySelector('.subagent-prompt');
    assert.equal(prompt.querySelector('strong').textContent, 'Review');
    assert.equal(prompt.querySelector('li').textContent, 'inspect the stream');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('subagent dock renders the objective as Markdown', async () => {
  const { mountSubagentPanel } = await import(`../page/conversation/subagent-dock.js?objective-markdown=${Date.now()}`);
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  const panel = dom.window.document.querySelector('#panel');
  mountSubagentPanel(panel, [{
    id: 'cursor-objective', name: 'Cursor', status: 'running', elapsed: '1s',
    objective: '**Goal**\n\n- keep the reader pinned only when requested', transcript: [],
  }]);
  const objective = panel.querySelector('.subagent-objective');
  assert.equal(objective.querySelector('strong').textContent, 'Goal');
  assert.equal(objective.querySelector('li').textContent, 'keep the reader pinned only when requested');
});

test('pending ask_question opens above the composer and answers through RPC', async () => {
  const { createAskPopover } = await import(`../page/conversation/ask-popover.js?test=${Date.now()}`);
  const dom = new JSDOM('<section id="ask" hidden></section>', { url: 'http://localhost/' });
  const rootElement = dom.window.document.querySelector('#ask');
  const calls = [];
  const controller = createAskPopover(rootElement, { rpc: async (...args) => { calls.push(args); return { ok: true }; } });
  controller.show({
    run_id: 'run-1', tool_call_id: 'call-1', question: 'Choose a route',
    options: [{ id: 'fast', label: 'Fast' }, { id: 'deep', label: 'Deep', default: true }],
    allow_free_text: true, multi_select: false,
  });

  assert.equal(rootElement.hidden, false);
  assert.equal(rootElement.getAttribute('role'), 'dialog');
  assert.equal(rootElement.querySelector('[data-option-id="deep"]').getAttribute('aria-pressed'), 'true');
  rootElement.querySelector('[data-option-id="fast"]').click();
  rootElement.querySelector('[data-ask-submit]').click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.deepEqual(calls[0], ['agent.ask.answer', { run_id: 'run-1', tool_call_id: 'call-1', via: 'option', option_ids: ['fast'] }]);
  assert.equal(rootElement.hidden, true);
});

test('pending ask popover renders the question body as Markdown', async () => {
  const { createAskPopover } = await import(`../page/conversation/ask-popover.js?markdown=${Date.now()}`);
  const dom = new JSDOM('<section id="ask" hidden></section>', { url: 'http://localhost/' });
  const rootElement = dom.window.document.querySelector('#ask');
  const controller = createAskPopover(rootElement, { rpc: async () => ({ ok: true }) });
  controller.show({
    run_id: 'run-md', tool_call_id: 'call-md', question: '**Plan**\n\n- keep the prompt readable',
    options: [{ id: 'yes', label: 'Continue' }], allow_free_text: false,
  });
  assert.equal(rootElement.querySelector('.ask-popover-question strong').textContent, 'Plan');
  assert.equal(rootElement.querySelector('.ask-popover-question li').textContent, 'keep the prompt readable');
  controller.close();
});

test('conversation restores and follows pending questions in the composer layer', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const document = new JSDOM(html).window.document;
  assert.ok(document.querySelector('#ask-popover'));
  assert.ok(document.querySelector('#message-form > #ask-popover'), 'question floats from the composer instead of living in a terminal row');
  assert.match(app, /agent\.ask\.pending/);
  assert.match(app, /agent\.ask\.answered/);
  assert.match(app, /agent\.ask\.pending'\s*,\s*\{\s*conversation_id:/);
});

test('prototype pulls persisted user turns on lifecycle events without remounting the stream', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  assert.match(app, /function refreshConversationSnapshot\(/);
  assert.match(app, /mergeConversationEvents\(events,/);
  assert.match(app, /agent\.turn\.started[\s\S]*refreshConversationSnapshot\(/);
  assert.match(app, /agent\.steer\.applied[\s\S]*refreshConversationSnapshot\(/);
  assert.match(app, /preserveStream:\s*true/);
  assert.doesNotMatch(app, /agent\.turn\.done[\s\S]*refreshConversationList\(activeConversation\.id\)/);
  assert.match(app, /snapshotRequestSequence/);
  assert.match(app, /latestSnapshotRequest\.get\(id\) !== requestID/);
  assert.match(app, /selectConversationTurnMessages\(result\.messages \|\| \[\], messageID\)/);
  assert.match(app, /messageID: round\.message_id/);
});

test('prototype follow pin ignores layout scrolls after a reader detaches', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  assert.match(app, /detached:\s*false/);
  assert.match(app, /markFollowIntent\('up'\)/);
  assert.match(app, /followState\.detached\s*&&[\s\S]*geometryDirection\s*===\s*'down'/);
  assert.doesNotMatch(app, /isThreadAtBottom\(stream,\s*SCROLL_TOLERANCE\)\s*\?\s*'down'/);
  assert.match(app, /scrollbarDragging[\s\S]*!atTail[\s\S]*followState\.detached\s*=\s*true/);
});

test('prototype consumes ACP run snapshots over a room-scoped SSE stream', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  // README.md documents the route contract at the prototype root; the
  // promoted copy lands inside frontend/ where the file does not ship.
  let readme = '';
  try {
    readme = await readFile(new URL('README.md', root), 'utf8');
  } catch (err) {
    if (err?.code !== 'ENOENT') throw err;
  }
  const openConversation = app.slice(app.indexOf('async function openConversation'), app.indexOf('// Refresh the authoritative transcript'));
  assert.match(app, /new EventSource\(/);
  assert.match(app, /\/stream\/acp\?conversation_id=/);
  assert.match(app, /addEventListener\('acp\.run'/);
  assert.match(app, /acceptSubagentStreamFrame/);
  assert.match(app, /function closeSubagentStream\(/);
  assert.match(openConversation, /closeSubagentStream\(\)/, 'room switches close the previous SSE connection');
  assert.doesNotMatch(app, /for \(const eventName of \['acp\.run\.(started|updated|done)'/);
  assert.doesNotMatch(app, /run received from the WS/);
  if (readme) assert.match(readme, /GET \/stream\/acp\?conversation_id=/);
});

test('widget hosts stay transparent so the conversation surface owns the background', async () => {
  const base = await readFile(new URL('widgets/widget-base.css', root), 'utf8');
  const tools = await readFile(new URL('widgets/tools/tool-widgets.css', root), 'utf8');
  const shell = await readFile(new URL('shell.css', root), 'utf8');
  assert.match(base, /:host\s*\{[^}]*background:\s*transparent/s);
  assert.match(tools, /:host\s*\{[^}]*background:\s*transparent/s);
  assert.doesNotMatch(base, /:host\s*\{[^}]*#050811/s);
  assert.doesNotMatch(tools, /:host\s*\{[^}]*#050811/s);
  assert.match(shell, /body\s*\{[^}]*background:\s*var\(--ink\)/s);
});

test('turn stream reconciliation uses the active-turn DTO key and survives a late WS lifecycle event', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  assert.match(app, /function reconcileActiveRound\(/);
  assert.match(app, /agent\.turns\.active',\s*\{\s*id:/);
  assert.doesNotMatch(app, /agent\.turns\.active',\s*\{\s*conversation_id:/);
  assert.match(app, /started = await rpc\('agent\.turns\.start'/);
  assert.match(app, /reconcileActiveRound\(activeConversation\.id, started\?\.run_id\)/);
  assert.match(app, /payload\?\.run_id && payload\?\.message_id/);
});

test('late lifecycle turns reattach the final SSE round without closing a healthy stream', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');

  assert.match(app, /function roundFromLifecycle\(/);
  assert.match(app, /last_turn/);
  assert.match(app, /lastTurn/);
  assert.match(app, /\['last-turn', 'last_turn', 'lastTurn'\]/);
  assert.match(app, /function attachRoundFromLifecycle\(/);
  assert.match(app, /const round = roundFromLifecycle\(payload\)/);
  assert.match(app, /if \(!hasLiveRoundStream\(\) && round\?\.run_id && round\?\.message_id[\s\S]*openRoundStream\(round\)/);
  assert.match(app, /if \(!hasLiveRoundStream\(\) && !round\?\.message_id\) void reconcileActiveRound\(/);
  assert.match(app, /auto_continue\?\.should_continue/);

  const doneHandler = app.slice(app.indexOf("on('agent.turn.done'"), app.indexOf("on('agent.turn.error'"));
  assert.doesNotMatch(doneHandler, /activeRoundStream\?\.close\(\)/,
    'turn.done must let the SSE terminal frame drain before refreshing the snapshot');
});

test('live rounds expose a non-blocking lazy activity status with reduced-motion dots', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('shell.css', root), 'utf8');
  const document = new JSDOM(html).window.document;
  const activity = document.querySelector('#stream-activity');

  assert.ok(activity, 'the activity status must survive stream child replacement');
  assert.equal(activity.getAttribute('role'), 'status');
  assert.equal(activity.hidden, true);
  assert.equal(activity.querySelectorAll('.stream-activity-dot').length, 3);
  assert.match(app, /function setStreamActivity\(/);
  assert.match(app, /Thinking…/);
  assert.match(app, /Planning next step…/);
  assert.match(app, /Planning exec in background…/);
  assert.match(app, /frame\.kind === 'activity'[\s\S]*setStreamActivity\(/);
  assert.match(app, /frame\.kind === 'tool'[\s\S]*setStreamActivity\(''\)/);
  assert.match(app, /round\.done[\s\S]*setStreamActivity\(''\)/);
  assert.match(css, /\.stream-activity\s*\{[\s\S]*pointer-events:\s*none/);
  assert.match(css, /@keyframes stream-activity-dot/);
  assert.match(css, /prefers-reduced-motion/);
});

test('reasoning widget reveals details without expanding the whole timeline', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/reasoning.js?test=${Date.now()}`);
    const widget = document.createElement('reasoning-widget');
    widget.event = {
      id: 'reason-1',
      type: 'agent.reasoning',
      summary: 'Mapped event boundaries and reusable seams.',
      details: ['Keep messages readable.', 'Move tools into disclosures.'],
      duration: '12s',
    };
    document.body.append(widget);

    const button = widget.shadowRoot.querySelector('button');
    const details = widget.shadowRoot.querySelector('.reasoning-details');
    assert.equal(details.hidden, true);
    button.click();
    assert.equal(details.hidden, false);
    assert.equal(button.getAttribute('aria-expanded'), 'true');
    assert.ok(details.querySelector('strong') === null, 'plain details stay plain when source has no Markdown');
    button.click();
    button.click();
    assert.match(details.textContent, /Keep messages readable/, 'closing and reopening keeps the lazy source renderable');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('reasoning widget defers large streamed details until the user expands it', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/reasoning.js?lazy=${Date.now()}`);
    const widget = document.createElement('reasoning-widget');
    widget.event = { id: 'reason-lazy', type: 'agent.reasoning', summary: 'Inspecting provider boundary…', details: ['a'.repeat(10_000)] };
    document.body.append(widget);
    assert.equal(widget.shadowRoot.querySelector('.reasoning-details p'), null);
    widget.shadowRoot.querySelector('button').click();
    assert.equal(widget.shadowRoot.querySelector('.reasoning-details p').textContent.length, 10_000);
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('reasoning widget renders expanded Markdown through the shared markdown body', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/reasoning.js?markdown=${Date.now()}`);
    const widget = document.createElement('reasoning-widget');
    widget.event = { id: 'reason-markdown', summary: 'Formatting', details: ['**Decision**\n\n- keep the mounted widget'] };
    document.body.append(widget);
    widget.shadowRoot.querySelector('button').click();
    const details = widget.shadowRoot.querySelector('.reasoning-details');
    assert.equal(details.querySelector('strong').textContent, 'Decision');
    assert.equal(details.querySelector('li').textContent, 'keep the mounted widget');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('reasoning summaries render Markdown to plain preview text', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/reasoning.js?summary=${Date.now()}`);
    const widget = document.createElement('reasoning-widget');
    widget.event = {
      id: 'reason-summary',
      type: 'agent.reasoning',
      summary: '**Clarifying** <em>SVG support</em> and path',
      details: [],
    };
    document.body.append(widget);

    const summary = widget.shadowRoot.querySelector('.reasoning-summary');
    assert.equal(summary.textContent, 'Clarifying SVG support and path');
    assert.doesNotMatch(summary.textContent, /[*_~`]/, 'Markdown markers must not leak into the collapsed preview');
    assert.doesNotMatch(summary.textContent, /<[^>]+>/, 'HTML tags must not leak into the collapsed preview');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('experimental Home derives launcher category tabs in first-seen order', () => {
  const apps = [
    { id: 'a.notes', hasUI: true, category: 'Productivity' },
    { id: 'a.feed', hasUI: true, category: 'News' },
    { id: 'a.misc', hasUI: true },
    { id: 'a.notes2', hasUI: true, category: 'Productivity' },
  ];

  assert.deepEqual(homeMCPCategories(apps), ['All', 'Productivity', 'News', 'Uncategorized']);
  assert.deepEqual(homeMCPCategories([]), ['All']);
  assert.deepEqual(homeMCPCategories([{ id: 'a.manifest', hasUI: true, manifest: { category: 'Tools' } }]), ['All', 'Tools']);
});

test('experimental Home filters the launcher by category with Uncategorized fallback', () => {
  const apps = [
    { id: 'a.notes', hasUI: true, category: 'Productivity' },
    { id: 'a.feed', hasUI: true, category: 'News' },
    { id: 'a.misc', hasUI: true },
  ];

  assert.deepEqual(filterHomeMCPAppsByCategory(apps, 'All').map((entry) => entry.id), apps.map((entry) => entry.id));
  assert.deepEqual(filterHomeMCPAppsByCategory(apps, 'News').map((entry) => entry.id), ['a.feed']);
  assert.deepEqual(filterHomeMCPAppsByCategory(apps, 'Uncategorized').map((entry) => entry.id), ['a.misc']);
  assert.deepEqual(filterHomeMCPAppsByCategory(apps, 'Missing'), []);
});

test('experimental Home wires category tabs, pairing-quiet, and manifest window sizing', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const css = await readFile(new URL('home.css', root), 'utf8');
  const { document } = new JSDOM(html).window;

  const tabs = document.querySelector('#home-category-tabs');
  assert.ok(tabs, 'the launcher needs a category tab row like production #launcher-tabs');
  assert.equal(tabs?.getAttribute('role'), 'group');
  assert.match(app, /homeMCPCategories\(/);
  assert.match(app, /filterHomeMCPAppsByCategory\(/);
  assert.match(app, /renderHomeCategoryTabs\(/);
  assert.match(app, /homeMCPPairingQuiet/);
  assert.match(app, /isPairingRequiredError\(/);
  assert.match(app, /launchPluginWindow\(\{[\s\S]*ui:\s*homePluginManifest\(plugin\)\.ui/);
  assert.match(css, /\.home-category-tabs\s*\{/);
  assert.match(css, /\.home-category-tab\.is-active/);
});

test('experimental shell persists the sidebar rail mode like production', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');

  assert.match(app, /localStorage\.setItem\('nusashell\.sidebarMode',\s*open\s*\?\s*'full'\s*:\s*'icons'\)/);
  assert.match(app, /setGlobalNavExpanded\(localStorage\.getItem\('nusashell\.sidebarMode'\)\s*!==\s*'icons'\)/);
});

function installDOMGlobals(window) {
  const keys = ['window', 'document', 'HTMLElement', 'customElements', 'CustomEvent', 'CSSStyleSheet'];
  const previous = Object.fromEntries(keys.map((key) => [key, globalThis[key]]));
  for (const key of keys) globalThis[key] = window[key];
  return previous;
}

function restoreDOMGlobals(previous) {
  for (const [key, value] of Object.entries(previous)) {
    if (value === undefined) delete globalThis[key];
    else globalThis[key] = value;
  }
}

test('model modality badges use advertised flags and preserve the plain searchable name', () => {
  const data = modelOptions([
    { id: 'text', provider_id: 'p', display_name: 'Text model' },
    { id: 'multi', provider_id: 'p', display_name: 'A < B', vision: true, audio: true, video: true },
  ])[0].options;
  assert.match(modelOptions([{ id: 'actual-model-id', display_name: 'Friendly name', provider_id: 'p' }])[0].options[0].html, /model-option-id">actual-model-id<\/small>/);
  assert.equal(data[1].text, 'A < B');
  assert.equal(data[1].value, 'p:multi');
  assert.match(data[1].html, /A &lt; B/);
  for (const label of ['Text', 'Vision input', 'Audio input', 'Video input']) assert.ok(data[1].html.includes(`aria-label="${label}"`));
  assert.doesNotMatch(data[0].html, /Vision input|Audio input|Video input/);
  assert.equal((data[1].html.match(/<svg /g) || []).length, 4);
  assert.equal((data[0].html.match(/<svg /g) || []).length, 1);
  assert.doesNotMatch(data[1].html, /▧|♫|▷/);
});

test('desktop directory drops preserve folder paths without becoming byte files', async () => {
  const { folderAttachmentsFromDataTransfer, filesFromDataTransfer, mergeFiles } = await import('../page/conversation/attachment-input.js');
  const previous = globalThis.window;
  globalThis.window = { nusashellDesktop: { getPathForFile: file => file.nativePath } };
  try {
    const file = { name: 'same', nativePath: '/one/same' };
    const transfer = { items: [{ kind: 'file', getAsFile: () => file, webkitGetAsEntry: () => ({ isDirectory: true, name: 'same' }) }], files: [file] };
    assert.deepEqual(filesFromDataTransfer(transfer), []);
    const folders = await folderAttachmentsFromDataTransfer(transfer);
    assert.deepEqual(folders, [{ type: 'folder', name: 'same', media_type: 'inode/directory', file_path: '/one/same' }]);
    assert.equal(mergeFiles(folders, [{ ...folders[0], file_path: '/two/same' }]).length, 2);
    const missing = [];
    globalThis.window = {};
    assert.deepEqual(await folderAttachmentsFromDataTransfer(transfer, name => missing.push(name)), []);
    assert.deepEqual(missing, ['same']);
  } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
});
