import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';
import { JSDOM } from 'jsdom';

import { contextInspectorModel, renderContextInspector } from '../page/conversation/context-inspector.js';
import { effectiveContextWindow, formatContextUsage } from '../js/agent-ui.js';

// App-level context transparency harness: the conversation page owns the raw
// agent.context.estimate event, the whole-turn UsageDTO from turn.done, and
// the provider captured when the run started. These tests drive the real
// handler/function bodies out of app.js inside a vm sandbox so behavior —
// not just source shape — is covered: room filtering, zero-vs-unknown fill,
// usage retention across snapshot refreshes, and reset on room teardown.

const root = new URL('../', import.meta.url);
const appPromise = readFile(new URL('page/conversation/app.js', root), 'utf8');

function extractFn(app, name, { asyncFn = false } = {}) {
  const marker = asyncFn ? `async function ${name}(` : `function ${name}(`;
  const start = app.indexOf(marker);
  assert.ok(start >= 0, `app.js must define ${name}`);
  return app.slice(start, app.indexOf('\n}', start) + 2);
}

function extractHandler(app, event, endMarker) {
  const start = app.indexOf(`on('${event}'`);
  assert.ok(start >= 0, `app.js must handle ${event}`);
  const end = app.indexOf(endMarker, start);
  assert.ok(end > start, `${event} extraction boundary missing`);
  return app.slice(start, end);
}

// eventHarness registers the real WS handlers for context/turn lifecycle in a
// sandbox with the minimal module state they read and write. With realSeams
// the actual refreshConversationSnapshot/settleConversation bodies are
// evaluated too, driven by the controlled rpc — proving retention on the
// real seams instead of only on the stubs they replace.
async function eventHarness({ realSeams = false, rpcResult = null } = {}) {
  const app = await appPromise;
  const listeners = {};
  const calls = { renderContextDock: 0, syncRoomActivity: [], refreshSnapshot: [], openRoundStream: [], reconcile: [], settle: [], refreshRooms: 0, rpc: [] };
  const context = {
    on: (name, cb) => { listeners[name] = cb; },
    console,
    activeConversation: { id: 'room-1', status: 'idle', model: 'prov-a:m-priced' },
    conversationKey: '',
    draftWorkspace: '',
    contextEstimate: 0,
    contextMeasured: 0,
    contextEstimateEvent: null,
    lastTurnUsage: null,
    lastTurnProviderId: '',
    snapshotMessages: [],
    events: [],
    conversations: [],
    settledRoundKeys: new Set(),
    soundNotificationsEnabled: false,
    stoppingTurn: false,
    activeRoundStream: null,
    agentTurnSound: () => 'silent',
    playComplete: () => {},
    playError: () => {},
    syncRoomActivity: (...args) => calls.syncRoomActivity.push(args),
    syncComposerAction: () => {},
    renderContextDock: () => { calls.renderContextDock += 1; },
    renderRooms: () => {},
    setStreamActivity: () => {},
    openSubagentStream: () => {},
    refreshConversationRooms: async () => { calls.refreshRooms += 1; },
    refreshConversationSnapshot: async (...args) => { calls.refreshSnapshot.push(args); return []; },
    openRoundStream: (...args) => calls.openRoundStream.push(args),
    reconcileActiveRound: async (...args) => calls.reconcile.push(args),
    settleConversation: (...args) => calls.settle.push(args),
    compactionStatus: { clear: () => {}, show: () => {} },
    hasLiveRoundStream: () => false,
    toast: () => {},
    stream: { setAttribute: () => {} },
    // Real refreshConversationSnapshot/settleConversation seams: the
    // controlled rpc serves the snapshot while every render/stream seam is
    // an observable no-op.
    window: { __widgetModels: [] },
    rpc: async (method, params) => { calls.rpc.push([method, params]); return rpcResult; },
    snapshotRequestSequence: 0,
    latestSnapshotRequest: new Map(),
    conversationLoadToken: 0,
    selectConversationTurnMessages: (messages) => (Array.isArray(messages) ? messages : []),
    messageEvents: () => [],
    mergeConversationEvents: () => [],
    liveEventIds: new Set(),
    filterEvents: (list) => (Array.isArray(list) ? list : []),
    historicalEvents: () => [],
    activeFilter: '',
    followState: { pinned: true },
    visibleEventEnd: 0,
    visibleEventCount: 0,
    visibleEvents: () => ({ visible: [] }),
    upsertVisibleWidget: () => {},
    eventWidgetTag: 'div',
    MAX_MOUNTED_EVENTS: 120,
    syncOlderEventsButton: () => {},
    updateFilterCounts: () => {},
    threadFollow: { nudge: () => {} },
    renderTasksDock: () => {},
    loadTodos: async () => {},
    loadSubagents: async () => {},
    restorePendingAsk: async () => {},
    renderEvents: () => {},
    composerSelects: null,
    commitLiveTurn: () => {},
    syncToolJobStrip: () => {},
    chunkCount: 0,
    nextChunkIndex: -1,
  };
  const sources = [
    extractFn(app, 'roundFromLifecycle'),
    extractFn(app, 'roundKey'),
    extractFn(app, 'conversationProviderID'),
    extractFn(app, 'backendContextTokens'),
    extractHandler(app, 'agent.turn.started', '// A few older WS bridges'),
    extractHandler(app, 'agent.context.estimate', "on('agent.compacting'"),
    extractHandler(app, 'agent.turn.done', "on('agent.turn.error'"),
  ];
  if (realSeams) {
    sources.push(
      extractFn(app, 'refreshConversationSnapshot', { asyncFn: true }),
      extractFn(app, 'settleConversation'),
    );
  }
  runInNewContext(sources.join('\n\n'), context);
  return { context, listeners, calls };
}

// viewHarness evaluates the real context-fill helpers and dock render against
// a JSDOM containing the meter and the Context tab panel.
async function viewHarness() {
  const app = await appPromise;
  const dom = new JSDOM(`<!doctype html>
    <button class="context-meter" id="context-meter" type="button"><span class="meter-ring"></span><span>–</span></button>
    <section class="dock-panel" id="panel-context"></section>`);
  const context = {
    document: dom.window.document,
    window: dom.window,
    contextInspectorModel,
    renderContextInspector,
    effectiveContextWindow,
    formatContextUsage,
    displayEvents: () => [],
    syncRoomInfo: () => {},
    activeConversation: null,
    contextMeasured: 0,
    contextEstimate: 0,
    contextEstimateEvent: null,
    lastTurnUsage: null,
    lastTurnProviderId: '',
    snapshotMessages: [],
    events: [],
    liveEventIds: new Set(),
    anchoredAfterLiveHost: new Set(),
  };
  context.window.__widgetModels = [];
  runInNewContext([
    extractFn(app, 'backendContextTokens'),
    extractFn(app, 'seedContextUsage'),
    extractFn(app, 'contextWindowForActiveModel'),
    extractFn(app, 'contextViewModel'),
    extractFn(app, 'syncContextMeter'),
    extractFn(app, 'historicalEvents'),
    extractFn(app, 'displayEvents'),
    extractFn(app, 'renderContextDock'),
  ].join('\n\n'), context);
  return { context, dom };
}

test('context estimate events keep the raw event and total, filtered to the active room', async () => {
  const { context, listeners, calls } = await eventHarness();
  const before = calls.renderContextDock;

  listeners['agent.context.estimate']({ conversation_id: 'other-room', estimated_tokens: 9999 });
  assert.equal(context.contextEstimate, 0, 'a hidden room cannot fill the meter');
  assert.equal(context.contextEstimateEvent, null);
  assert.equal(calls.renderContextDock, before, 'no re-render for other rooms');

  const payload = { conversation_id: 'room-1', run_id: 'r1', estimated_tokens: 5200, system_tokens: 4200, messages_tokens: 700, tools_tokens: 300 };
  listeners['agent.context.estimate'](payload);
  assert.equal(context.contextEstimate, 5200);
  assert.equal(context.contextEstimateEvent, payload, 'the inspector needs the raw event, not just the total');
  assert.equal(calls.renderContextDock, before + 1);
});

test('turn.done records whole-turn usage with the provider captured at run start', async () => {
  const { context, listeners, calls } = await eventHarness();

  listeners['agent.turn.started']({ conversation_id: 'room-1', run_id: 'r1', message_id: 'a1' });
  assert.equal(context.lastTurnProviderId, 'prov-a', 'provider captured when the run starts, not after it');

  const usage = { input_tokens: 1200, output_tokens: 30, cache_read: 200 };
  listeners['agent.turn.done']({
    conversation_id: 'room-1', run_id: 'r1', message_id: 'a1',
    model: 'm-priced', usage, context_tokens: 4300,
  });
  assert.equal(context.contextMeasured, 4300);
  assert.equal(context.lastTurnUsage?.usage, usage);
  assert.equal(context.lastTurnUsage?.model, 'm-priced');
  assert.equal(context.lastTurnUsage?.providerId, 'prov-a', 'turn.done has no provider_id — the captured route supplies it');
  assert.equal(context.lastTurnUsage?.messageId, 'a1', 'the run message lets the persisted row supply provider_id later');
  assert.ok(calls.renderContextDock >= 1);
});

test('whole-turn usage survives the real snapshot refresh and settle seams', async () => {
  const snapshot = {
    conversation: { id: 'room-1', status: 'idle', model: 'prov-a:m-priced', context_tokens: 120 },
    messages: [
      { id: 'a1', role: 'assistant', usage: { input_tokens: 5, output_tokens: 2 }, model: 'm-priced', provider_id: 'prov-a' },
    ],
  };
  const { context, listeners, calls } = await eventHarness({ realSeams: true, rpcResult: snapshot });

  listeners['agent.turn.started']({ conversation_id: 'room-1', run_id: 'r1', message_id: 'a1' });
  listeners['agent.turn.done']({
    conversation_id: 'room-1', run_id: 'r1', message_id: 'a1',
    model: 'm-priced', usage: { input_tokens: 7, output_tokens: 3 }, context_tokens: 120,
  });
  const recorded = context.lastTurnUsage;
  assert.ok(recorded);

  // Drive the actual refreshConversationSnapshot/settleConversation bodies —
  // the seams a refactor could teach to reset room-scoped detail state.
  await context.settleConversation('room-1', 'a1');
  await context.refreshConversationSnapshot('room-1', { preserveStream: true, terminal: true, messageID: 'a1' });

  assert.equal(context.lastTurnUsage, recorded, 'the real refresh/settle path never clears the whole-turn usage');
  assert.equal(context.snapshotMessages, snapshot.messages, 'the controlled snapshot actually landed');
  assert.equal(context.activeConversation.context_tokens, 120);
  assert.ok(calls.rpc.length >= 1, 'the real refresh went through rpc');
});

test('an explicit estimated_tokens: 0 live event is a known zero, not unavailable', async () => {
  const { context, listeners } = await eventHarness();

  listeners['agent.context.estimate']({ conversation_id: 'room-1', estimated_tokens: 0 });
  let fill = context.backendContextTokens();
  assert.equal(fill.tokens, 0, 'the server computed an explicit empty next request');
  assert.equal(fill.estimated, true);

  // An event without the field carries no evidence — back to unknown.
  listeners['agent.context.estimate']({ conversation_id: 'room-1', system_tokens: 500 });
  fill = context.backendContextTokens();
  assert.equal(fill.tokens, null, 'absent estimated_tokens stays unavailable');

  // A coercible non-number or negative total is not evidence either —
  // Number() would fabricate zeros from null, '', and false.
  for (const bogus of [null, '', false, -150]) {
    listeners['agent.context.estimate']({ conversation_id: 'room-1', estimated_tokens: bogus });
    fill = context.backendContextTokens();
    assert.equal(fill.tokens, null, `estimated_tokens ${JSON.stringify(bogus)} carries no evidence`);
    assert.equal(fill.estimated, false);
  }

  // A fresh explicit zero is still a known fill after the bogus totals.
  listeners['agent.context.estimate']({ conversation_id: 'room-1', estimated_tokens: 0 });
  fill = context.backendContextTokens();
  assert.equal(fill.tokens, 0);
  assert.equal(fill.estimated, true);

  // Measured fill keeps priority over a fresh zero estimate.
  context.contextMeasured = 4300;
  fill = context.backendContextTokens();
  assert.equal(fill.tokens, 4300);
  assert.equal(fill.estimated, false);
});

test('turn.done for another room leaves context state untouched', async () => {
  const { context, listeners } = await eventHarness();

  listeners['agent.turn.done']({
    conversation_id: 'other-room', run_id: 'r9', message_id: 'x9',
    model: 'm-priced', usage: { input_tokens: 9 }, context_tokens: 9000,
  });
  assert.equal(context.contextMeasured, 0);
  assert.equal(context.lastTurnUsage, null);
});

test('a turn.done without usage still updates the room and records an absent usage honestly', async () => {
  const { context, listeners } = await eventHarness();
  listeners['agent.turn.done']({ conversation_id: 'room-1', run_id: 'r1', message_id: 'a1', model: 'm-priced', context_tokens: 640 });
  assert.equal(context.contextMeasured, 640);
  assert.equal(context.lastTurnUsage?.usage, null, 'absent usage stays absent — never fabricated as zeros');
  assert.equal(context.lastTurnUsage?.model, 'm-priced');
});

test('backendContextTokens reports unknown fill as null, never a fabricated zero', async () => {
  const { context } = await viewHarness();

  // vm-realm objects fail deepStrictEqual on prototype identity — compare
  // the fields.
  let fill = context.backendContextTokens();
  assert.equal(fill.tokens, null, 'no measured, persisted, or estimate data means unknown — not 0%');
  assert.equal(fill.estimated, false);

  // The wire omits zero fields, so an explicit persisted 0 cannot prove an
  // empty context — it stays unknown.
  context.activeConversation = { id: 'r', context_tokens: 0, estimated_tokens: 0 };
  assert.equal(context.backendContextTokens().tokens, null);

  context.seedContextUsage({ context_tokens: 4800, estimated_tokens: 5200 });
  fill = context.backendContextTokens();
  assert.equal(fill.tokens, 4800);
  assert.equal(fill.estimated, false);

  // A room that only persists a measured fill seeds the estimate slot with
  // the same value — the estimate is provisional, not additional evidence.
  context.seedContextUsage({ context_tokens: 3200 });
  assert.equal(context.contextMeasured, 3200);
  assert.equal(context.contextEstimate, 3200);

  context.seedContextUsage({ estimated_tokens: 2500 });
  fill = context.backendContextTokens();
  assert.equal(fill.tokens, 2500);
  assert.equal(fill.estimated, true);
});

test('topbar meter renders an accessible unavailable state when fill is unknown', async () => {
  const { context, dom } = await viewHarness();
  const meter = dom.window.document.querySelector('.context-meter');

  const result = context.syncContextMeter();
  assert.equal(result.percentage, null);
  assert.equal(meter.querySelector('span:last-child').textContent, '–', 'unavailable reads as a dash, not 0%');
  assert.equal(meter.getAttribute('aria-label'), 'Context usage unavailable');
  assert.equal(meter.dataset.availability, 'unavailable');
  assert.equal(meter.dataset.context, undefined);

  context.window.__widgetModels = [{ id: 'm-priced', provider_id: 'prov-a', context: 200000 }];
  context.activeConversation = { id: 'room-1', model: 'prov-a:m-priced' };
  context.contextMeasured = 50000;
  const known = context.syncContextMeter();
  assert.equal(known.percentage, 25);
  assert.equal(meter.querySelector('span:last-child').textContent, '25%');
  assert.equal(meter.dataset.availability, undefined, 'availability flag clears once fill is known');
});

test('topbar meter and inspector show the same rounded percent above 100%', async () => {
  const { context, dom } = await viewHarness();
  context.window.__widgetModels = [{ id: 'm-priced', provider_id: 'prov-a', context: 100000 }];
  context.activeConversation = { id: 'room-1', model: 'prov-a:m-priced' };
  context.contextMeasured = 105000;

  context.renderContextDock();
  const meter = dom.window.document.querySelector('.context-meter');
  const inspectorPercent = dom.window.document.querySelector('#panel-context .context-usage-line span:last-child');
  assert.equal(inspectorPercent.textContent, '105%');
  assert.equal(meter.querySelector('span:last-child').textContent, '105%',
    'the topbar shows the actual percent instead of clamping to 100');
  assert.equal(meter.getAttribute('aria-label'), 'Context usage: 105 percent');
  assert.equal(meter.querySelector('.meter-ring').style.getPropertyValue('--context-fill'), '100%',
    'only the visual ring clamps at 100');
  assert.equal(meter.dataset.context, 'critical');
});

test('context dock keeps room rows and renders the inspector groups', async () => {
  const { context, dom } = await viewHarness();
  context.window.__widgetModels = [
    { id: 'm-priced', provider_id: 'prov-a', input_cost: 1, output_cost: 2, cache_read_cost: 0.1, context: 200000 },
  ];
  context.activeConversation = { id: 'room-1', model: 'prov-a:m-priced', message_count: 12 };
  context.events = [{ id: 't1', type: 'tool.run' }];
  context.contextMeasured = 4800;
  context.contextEstimateEvent = { estimated_tokens: 5200, system_tokens: 4200, messages_tokens: 700, tools_tokens: 300 };
  context.lastTurnUsage = { usage: { input_tokens: 1200, output_tokens: 30 }, model: 'm-priced', providerId: 'prov-a', messageId: 'a1' };

  context.renderContextDock();
  const panel = dom.window.document.querySelector('#panel-context');
  assert.equal(panel.querySelector('.context-room-id').textContent, 'room-1');
  assert.equal(panel.querySelector('.context-tool-count').textContent, '1');
  const inspector = panel.querySelector('.context-inspector');
  assert.ok(inspector, 'the inspector mounts inside the existing dock panel');
  const labels = [...inspector.querySelectorAll('.context-group-label')].map((el) => el.textContent);
  assert.deepEqual(labels, ['Context fill', 'Next request · estimated', 'Latest usage · measured']);
  const keys = [...inspector.querySelectorAll('.context-key')].map((el) => el.textContent);
  for (const key of ['Used', 'Source', 'Total', 'System', 'Conversation', 'Tool definitions', 'Last turn', 'Cache', 'Model', 'Est. cost']) {
    assert.ok(keys.includes(key), `missing inspector row ${key}`);
  }
});

test('resetContextDetails clears room-scoped context state on room teardown', async () => {
  const app = await appPromise;
  const context = {
    contextEstimate: 5200,
    contextMeasured: 4800,
    contextEstimateEvent: { estimated_tokens: 5200 },
    lastTurnUsage: { usage: { input_tokens: 1 }, model: 'm' },
    lastTurnProviderId: 'prov-a',
  };
  runInNewContext(extractFn(app, 'resetContextDetails'), context);
  context.resetContextDetails();
  assert.equal(context.contextEstimate, 0);
  assert.equal(context.contextMeasured, 0);
  assert.equal(context.contextEstimateEvent, null);
  assert.equal(context.lastTurnUsage, null);
  assert.equal(context.lastTurnProviderId, '');
});

test('room switch and room clear both reset context inspector state', async () => {
  const app = await appPromise;
  const openConversation = app.slice(app.indexOf('async function openConversation('), app.indexOf('// Refresh the authoritative transcript'));
  const cleared = app.slice(app.indexOf('function clearDeletedConversationView('), app.indexOf('function invalidateDeletedConversationLoad('));

  assert.match(openConversation, /resetContextDetails\(\)[\s\S]*seedContextUsage\(result\.conversation\)/,
    'the new room seeds context only after prior-room detail state is cleared');
  assert.match(cleared, /resetContextDetails\(\)/);
});

test('context meter renders one stationary hollow progress ring', async () => {
  // Progress must be a single ring: a second connection border or rotation
  // makes the displayed bright share disagree with the context percentage.
  const shell = await readFile(new URL('shell.css', root), 'utf8');
  const strips = await readFile(new URL('page/conversation/strips.css', root), 'utf8');
  const ringBase = shell.match(/\.meter-ring\s*\{[^}]*\}/);
  assert.ok(ringBase, 'shell.css keeps the base .meter-ring rule');
  const body = ringBase[0];
  assert.match(body, /conic-gradient\(var\(--meter-arc[^%]+var\(--context-fill, 0%\)[^%]*var\(--meter-track/,
    'the arc paints the resolved fill share over a dim track');
  assert.match(body, /border:\s*0\s*;/, 'no second ring competes with the progress arc');
  assert.match(body, /flex-shrink:\s*0/, 'the ring stays circular in narrow layouts');
  assert.doesNotMatch(body, /transform:|animation:/, 'the percentage starts at twelve oclock and stays still');
  assert.doesNotMatch(shell, /\.context-meter\[data-connection=[^}]*\.meter-ring/, 'connection state cannot repaint or rotate context progress');
  assert.match(body, /-webkit-mask:\s*radial-gradient\(farthest-side/, 'a radial mask keeps the center empty');
  assert.match(body, /(?<!-webkit-)mask:\s*radial-gradient\(farthest-side/, 'the standard mask property is set too');
  assert.doesNotMatch(body, /conic-gradient\([^)]*transparent 0\)/, 'the track must be a visible dim color, not transparent');

  const overrides = strips.match(/\.context-meter[^{]*\.meter-ring\s*\{[^}]*\}/g) || [];
  assert.ok(overrides.length >= 3, 'strips.css re-skins default/high/critical arcs');
  for (const rule of overrides) {
    const ruleBody = rule.slice(rule.indexOf('{'));
    assert.doesNotMatch(ruleBody, /background/, `arc override only recolors, never repaints: ${rule}`);
    assert.match(ruleBody, /--meter-arc\s*:/, `arc override swaps --meter-arc: ${rule}`);
  }
});

test('mini-mode hides the context meter alongside the other dead dock controls', async () => {
  // In mini-mode (popup fallback) .context-dock is display:none, so the meter
  // is a dead control — its only action opens the hidden dock. It belongs on
  // the same declutter list as the nav/dock toggles.
  const strips = await readFile(new URL('page/conversation/strips.css', root), 'utf8');
  const miniMode = strips.match(/body\.mini-mode[^{]*\{[^}]*display:\s*none[^}]*\}/g) || [];
  const decluttered = miniMode.find((rule) => rule.includes('.dock-toggle'));
  assert.ok(decluttered, 'mini-mode declutters the dock toggle');
  assert.match(decluttered, /\.context-meter/, 'mini-mode declutters the context meter too');
});
