import { filterEvents, normalizeTodoItem, summarizeTodos } from './state.js?v=7';
import { applyAskStatus, applyRoundDelta, applyRoundDone, applyTurnError, canonicalTwins, messageEvents, providerRetryLabel } from './live-data.js?v=6';
import { acceptRoundFrame, roundDoneNeedsReplay, roundStreamURL } from './round-stream.js?v=2';
import { rpc, connectWS, on, isPairingRequiredError } from '../../js/rpc.js';
import { playAsk, playComplete, playError } from '../../js/sounds.js';
import { agentTurnSound } from './turn-sound.js?v=1';
import { createThreadFollow, stickThreadToBottom } from '../../js/thread-follow.js';
import { effectiveContextWindow, formatContextUsage, inspectAttachmentContent, isNestedScrollerEvent, isThreadAtBottom, toDataURL, updateScrollPin } from '../../js/agent-ui.js';
import { eventWidgetTag } from '../../widgets/registry.js';
import { appendVisibleCount, attachLiveHost, commitLiveHost, growVisibleCount, mountWidgetAfterLiveHost, reconcileVisibleWidgets, shiftWindowOlder, transcriptWindow, upsertVisibleWidget } from './transcript-window.js';
import { mountSubagentPanel, selectSubagentRun } from './subagent-dock.js?v=22';
import { acceptSubagentStreamFrame, enrichSubagentToolEvent, toSubagentView } from './subagent-state.js?v=5';
import { filterConversations, isConversationActive, normalizeConversationStatus, selectInitialConversation } from './room-state.js?v=2';
import { normalizePrototypeRoute } from './route-state.js?v=3';
import { bindComposerSendKeys, initComposerSelects } from './composer.js?v=12';
import { initWorkspaceBrowser } from './workspace-browser.js?v=6';
import { createAskPopover } from './ask-popover.js?v=3';
import { filesFromDataTransfer, folderAttachmentsFromDataTransfer, mergeFiles } from './attachment-input.js?v=3';
import { createLoadingController, hidePageLoading } from '../../shared/loading-layer.js?v=1';
import { widgetStylesLoaded } from '../../shared/widget-styles.js?v=7';
import { openTextPreviewPopup, resolveWorkspaceFilePath } from '../../js/media-zoom.js';
import { openPluginWindow as launchPluginWindow } from '../../js/plugin-window.js?v=1';
import { confirmDialog, dismissOpenDialogs, registerOverlayDismiss, toast } from '../../js/ui.js';
import { initInstallPrompt } from '../../js/pwa-install.js?v=1';
import { initOfflineScreen } from '../../js/offline-screen.js';
import { applyFontPreference, readFontPreference } from '../../js/font-preferences.js';
import { refreshPairingPanel } from '../../js/pairing.js';
import { mergeConversationEvents, selectConversationTurnMessages } from './conversation-sync.js?v=3';
import { contextInspectorModel, renderContextInspector } from './context-inspector.js?v=3';
import { closeRoomMenu, isRoomMenuOpenFor, openRoomActionsMenu } from './room-menu.js?v=1';
import { updateRoomInfo } from '../../js/views/agent/room-info.js';
import { createCompactionStatus, createSteerQueue, createToolJobStrip, mountPipAliases, mountRoomInfo } from './strips.js?v=3';
import { renderMarkdownBody } from '../../shared/markdown-body.js?v=7';
import { createSkillsPage } from '../skills/app.js?v=2';
import { createPluginsPage } from '../plugins/app.js?v=1';
import { createLearningPage } from '../learning/app.js?v=5';
import { createProvidersPage } from '../providers/app.js?v=4';
import { createLogsPage } from '../logs/app.js?v=2';
import { createAutomationPage } from '../automation/app.js?v=3';
import { createTelemetryPage } from '../telemetry/app.js?v=1';
import {
  filterHomeMCPApps,
  filterHomeMCPAppsByCategory,
  homeMCPCategories,
  homeMCPAppsWithUI,
  homePluginIconGlyph,
  homePluginIconSource,
  homePluginID,
  homePluginManifest,
  homePluginName,
  homeMCPEmptyCopy,
  markHomeMCPConnected,
} from '../home/mcp.js';
import '../../widgets/user-message.js?v=6';
import '../../widgets/reasoning.js?v=9';
import '../../widgets/tools/widgets.js?v=19';
import '../../widgets/agent-message.js?v=13';

applyFontPreference(readFontPreference());
window.lucide?.createIcons({ attrs: { 'stroke-width': 1.7 } });

const shell = document.querySelector('.shell');
const pageLoading = document.querySelector('#page-loading');
const conversation = document.querySelector('.conversation');
const conversationSurface = document.querySelector('#conversation-surface');
const homeView = document.querySelector('#home-view');
const skillsView = document.querySelector('#skills-view');
const pluginsView = document.querySelector('#plugins-view');
const learningView = document.querySelector('#learning-view');
const providersView = document.querySelector('#providers-view');
const logsView = document.querySelector('#logs-view');
const automationView = document.querySelector('#automation-view');
const telemetryView = document.querySelector('#telemetry-view');
const settingsView = document.querySelector('#settings-view');
const homeSearch = document.querySelector('#home-search');
const skillsSearch = document.querySelector('#skills-search');
const pluginsSearch = document.querySelector('#plugins-search');
const learningSearch = document.querySelector('#learning-search-input');
const automationSearch = document.querySelector('#automation-search');
const settingsSearch = document.querySelector('#settings-search');
const homeAppGrid = document.querySelector('#home-app-grid');
const homeAppMessage = document.querySelector('#home-app-message');
const homeAppCount = document.querySelector('#home-app-count');
const homeAppRetry = document.querySelector('#home-app-retry');
const homeCategoryTabs = document.querySelector('#home-category-tabs');
const roomHeadingTitle = document.querySelector('.room-heading strong');
const stream = document.querySelector('#event-stream');
const streamActivity = document.querySelector('#stream-activity');
const streamActivityCopy = streamActivity?.querySelector('.stream-activity-copy');
const dock = document.querySelector('#context-dock');
const roomDrawer = document.querySelector('#room-drawer');
const globalNav = document.querySelector('#global-nav');
const globalNavToggle = document.querySelector('#global-nav-toggle');
const globalNavExpand = document.querySelector('#global-nav-expand');
const globalNavInstall = document.querySelector('#global-nav-install');
const roomSearchInput = document.querySelector('#room-search-input');
const newChatButton = document.querySelector('#new-chat');
const composer = document.querySelector('#message-input');
const messageForm = document.querySelector('#message-form');
const sendButton = messageForm.querySelector('.send-button');
const stopTurnButton = messageForm.querySelector('#stop-turn-button');
const attachmentInput = document.querySelector('#attachment-input');
const attachmentTray = document.querySelector('#attachment-tray');
const askPopoverRoot = document.querySelector('#ask-popover');
initOfflineScreen();
const skillsPage = skillsView ? createSkillsPage(skillsView, { rpcClient: rpc, onEvent: on }) : null;
skillsPage?.init();
const pluginsPage = pluginsView ? createPluginsPage(pluginsView, { rpcClient: rpc }) : null;
pluginsPage?.init();
const learningPage = learningView ? createLearningPage(learningView, { rpcClient: rpc, onEvent: on }) : null;
learningPage?.init();
const providersPage = providersView ? createProvidersPage(providersView, { rpcClient: rpc }) : null;
providersPage?.init();
const logsPage = logsView ? createLogsPage(logsView, { rpcClient: rpc, onEvent: on }) : null;
logsPage?.init();
const automationPage = automationView ? createAutomationPage(automationView, { rpcClient: rpc, onEvent: on }) : null;
automationPage?.init();
const telemetryPage = telemetryView ? createTelemetryPage(telemetryView, { rpcClient: rpc }) : null;
telemetryPage?.init();
// settings page controller is mounted by page/settings/app.js when that module exists
let settingsPage = null;
// settings.set writes the store without emitting settings.applied — the file
// watcher treats that rewrite as identical to memory. Refresh from settings.get
// so the next turn uses the saved sound choice without a reload.
function rpcWithSoundRefresh(method, payload, options) {
  const pending = rpc(method, payload, options);
  if (method === 'settings.set') void Promise.resolve(pending).then(() => refreshSoundNotifications(), () => {});
  return pending;
}
import('../settings/app.js').then((mod) => {
  if (!settingsView || !mod.createSettingsPage) return;
  settingsPage = mod.createSettingsPage(settingsView, { rpcClient: rpcWithSoundRefresh, pairingRefresh: refreshPairingPanel });
  settingsPage.init();
  if (!settingsView.hidden) settingsPage.activate?.();
}).catch((error) => {
  settingsPage = null;
  console.error('Settings page failed to start', error);
});
const events = [];
const liveEventIds = new Set();
// User messages applied while this round is still live. They stay out of the
// historical prefix so a snapshot merge cannot paint them above the host.
const anchoredAfterLiveHost = new Set();
let conversations = [];
let conversationListLoadToken = 0;
let soundNotificationsEnabled = true;
let activeConversation = null;
let conversationKey = '';
let draftWorkspace = '';
let activeRoundStream = null;
let stoppingTurn = false;
let liveTodos = [];
let liveTodoBrief = '';
let liveSubagents = [];
let subagentEventSource = null;
let subagentStreamConversationID = '';
let subagentStreamReopenTimer = null;
const streamedSubagentIDs = new Set();
let attachments = [];
let activeFilter = 'all';
let roomSearchQuery = '';
let homeMCPApps = [];
let homeMCPAppsLoading = true;
let homeMCPAppsError = null;
let homeMCPLoadToken = 0;
let homeMCPCategory = 'All';
// When the backend requires device pairing, the launcher stays quiet (no
// banner, no empty state) so the pairing gate is the dominant UI — same
// contract as production's home.js pairingQuiet flag.
let homeMCPPairingQuiet = false;
let composerSelects;
let workspaceBrowser;
let askPopover;
let toolJobStrip;
let steerQueue;
let compactionStatus;
const compactingRooms = new Set();
let visibleEventCount = 48;
let visibleEventEnd = null;
let loadingOlderEvents = false;
let conversationLoadToken = 0;
let subagentLoadToken = 0;
let snapshotRequestSequence = 0;
const latestSnapshotRequest = new Map();
let liveTurnHost = null;
let liveTurnKey = '';
let liveTurnGeneration = 0;
// Scalar = generation of the latest applied steer. It is the fallback lock
// when the queue is empty and the gate for anchoredSteerElement.
let steerAnchorGeneration = 0;
// One live-turn generation lock per applied steer, in applied order. A
// merged snapshot can carry several steer rows at once (an earlier refresh
// may have been superseded); each row still anchors at its own boundary.
const steerAnchorGenerations = [];
let liveRenderFrame = 0;
let providerRetryTimer = null;
// Context meter state mirrors the production Agent view: the last
// provider-measured fill is authoritative, the preflight estimate only shows
// while no measured value exists.
let contextEstimate = 0;
let contextMeasured = 0;
// The inspector also keeps the raw preflight event (its category breakdown
// never comes from the persisted total) and the whole-turn usage plus the
// provider captured when the run started. All three are room-scoped:
// resetContextDetails drops them on every room switch and teardown.
let contextEstimateEvent = null;
let lastTurnUsage = null;
let lastTurnProviderId = '';
// Raw MessageDTO list for the active room. The room-info popover counts tool
// calls from the wire shape (tool_calls / steps), so keep it next to the
// widget-ready events rather than re-deriving it.
let snapshotMessages = [];
// A queued steer is room-scoped and survives room switches until the backend
// applies or cancels it. Hidden rooms collect theirs from lifecycle events;
// the active room restores from turns.active.queued_steer.
const steerByRoom = new Map();
const toolJobStartedAt = new Map();
const toolJobStopping = new Set();
let chunkCount = 0;
let nextChunkIndex = -1;
const loadedChunkIndexes = new Set();
let loadingChunk = false;
const pendingLiveWidgets = new Map();
const activeRoundReconciliations = new Map();
const settledRoundKeys = new Set();
const INITIAL_EVENT_WINDOW = 48;
const EVENT_WINDOW_BATCH = 32;
const MAX_MOUNTED_EVENTS = 120;
const SCROLL_TOLERANCE = 32;
const STREAM_RETRY_DELAYS = [300, 1200, 4000];
// A successful turns.start returns before the worker emits turn.started. Poll
// the authoritative active-turn DTO briefly so a delayed/lost WS lifecycle
// frame cannot leave the UI without an SSE attachment.
const ACTIVE_ROUND_RECONCILE_DELAYS = [0, 80, 240, 600, 1400, 2600];
// A permanently closed ACP run stream (non-2xx response) is reopened on one
// bounded delay — the same 5s the production scheduleRunStreamReopen uses —
// so a saturated subscriber cap cannot spin a tight reconnect loop.
const SUBAGENT_STREAM_REOPEN_MS = 5 * 1000;
const MAX_ATTACHMENTS = 4;
const followState = { pinned: true, detached: false, intent: '', pinGeom: null };

function newConversationKey() {
  const random = globalThis.crypto?.randomUUID?.()
    || Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2);
  return `draft-${Date.now().toString(36)}-${random.replace(/[^a-z0-9]/gi, '').slice(0, 8)}`;
}
let scrollbarDragging = false;
// Room changes replace the stream's custom-element hosts. Keep the transition
// veil on the stable conversation surface so it survives that replacement.
const conversationLoading = createLoadingController(conversation, {
  delay: 0,
  minimum: 240,
  label: 'Loading conversation',
});

function streamActivityLabel(activity, options = {}) {
  const { round = 1, toolName = '' } = options;
  const normalized = String(activity || '').trim().toLowerCase().replace(/[\s_-]+/g, ' ');
  const tool = String(toolName || '').trim();
  if (normalized.includes('provider retry') || normalized === 'retrying') return providerRetryLabel(options);
  if (tool === 'exec') return 'Planning exec in background…';
  if (tool || normalized === 'tool call' || normalized.includes('tool')) {
    return `Planning ${tool || 'tool'} in background…`;
  }
  if (normalized.includes('recover') || normalized.includes('reconnect')) return 'Reconnecting…';
  if (normalized.includes('plan') || Number(round) > 1) return 'Planning next step…';
  return 'Thinking…';
}

function setStreamActivity(activity = '', options = {}) {
  const label = activity ? (options.label || streamActivityLabel(activity, options)) : '';
  if (stream) {
    if (label) {
      stream.dataset.activity = activity;
      if (options.toolName) stream.dataset.activityTool = options.toolName;
      else delete stream.dataset.activityTool;
    } else {
      delete stream.dataset.activity;
      delete stream.dataset.activityTool;
    }
  }
  if (!streamActivity) return;
  streamActivity.hidden = !label;
  streamActivity.classList.toggle('is-visible', Boolean(label));
  if (streamActivityCopy) streamActivityCopy.textContent = label;
  if (label) streamActivity.setAttribute('aria-label', label);
  else streamActivity.removeAttribute('aria-label');
}

// backendContextTokens returns the server's context-fill number for the
// active room: provider-measured first, persisted provider value next, and
// the provisional server estimate last. The UI never sums message bubbles.
function backendContextTokens() {
  const measured = Number(contextMeasured);
  if (Number.isFinite(measured) && measured > 0) return { tokens: measured, estimated: false };
  const persisted = Number(activeConversation?.context_tokens);
  if (Number.isFinite(persisted) && persisted > 0) return { tokens: persisted, estimated: false };
  const live = Number(contextEstimate);
  if (Number.isFinite(live) && live > 0) return { tokens: live, estimated: true };
  // The live preflight event is the one place a numeric zero is evidence:
  // the server computed an explicit total for the next request. Wire fields
  // are omitempty, so only the retained event's own explicit finite
  // non-negative estimated_tokens qualifies — Number() would fabricate a
  // zero from null, '', or false, and a negative total is not evidence.
  const raw = contextEstimateEvent && typeof contextEstimateEvent === 'object'
    ? contextEstimateEvent.estimated_tokens
    : null;
  if (typeof raw === 'number' && Number.isFinite(raw) && raw >= 0) {
    return { tokens: raw, estimated: true };
  }
  const estimate = Number(activeConversation?.estimated_tokens);
  if (Number.isFinite(estimate) && estimate > 0) return { tokens: estimate, estimated: true };
  // Wire fields are omitempty, so an absent or explicit-0 snapshot value can
  // never prove an empty context — report unknown, not a fabricated zero.
  return { tokens: null, estimated: false };
}

// resetContextDetails drops the room-scoped inspector state before a new room
// seeds its own, and on the full teardown used by delete/new-chat. A stale
// estimate event or last-turn usage must never bleed across rooms.
function resetContextDetails() {
  contextEstimate = 0;
  contextMeasured = 0;
  contextEstimateEvent = null;
  lastTurnUsage = null;
  lastTurnProviderId = '';
}

function seedContextUsage(source = {}) {
  const measured = Number(source?.context_tokens);
  const estimate = Number(source?.estimated_tokens);
  contextMeasured = Number.isFinite(measured) && measured > 0 ? measured : 0;
  contextEstimate = Number.isFinite(estimate) && estimate > 0 ? estimate : contextMeasured;
}

function contextWindowForActiveModel() {
  const catalog = window.__widgetModels || [];
  const entry = catalog.find((model) => `${model.provider_id}:${model.id}` === activeConversation?.model)
    || catalog.find((model) => model.id === activeConversation?.model);
  return effectiveContextWindow(Number(entry?.context) || 0, 0);
}

// contextViewModel builds the shared inspector view model: the fill resolved
// from backendContextTokens, the raw estimate event, the retained whole-turn
// usage, the snapshot messages (saved-response fallback + provider lookup),
// and the model catalog for window/pricing.
function contextViewModel() {
  const { tokens, estimated } = backendContextTokens();
  return contextInspectorModel({
    tokens,
    estimated,
    windowTokens: contextWindowForActiveModel(),
    estimateEvent: contextEstimateEvent,
    turnUsage: lastTurnUsage,
    snapshotMessages,
    models: window.__widgetModels || [],
  });
}

// syncContextMeter drives the topbar meter plus the dock's usage line from
// one computed percentage so both surfaces can never disagree. Missing fill
// or window renders as unavailable — an accessible dash, never 0%.
function syncContextMeter(model = contextViewModel()) {
  const meter = document.querySelector('.context-meter');
  const { percent } = model.fill;
  // The label shows the same rounded actual percent the inspector renders —
  // an over-100% fill reads as 105% on both surfaces; only the ring clamps.
  const percentage = percent === null ? null : Math.round(percent);
  const labelText = percentage === null ? '–'
    : percent > 0 && percent < 1 ? '<1%' : `${percentage}%`;
  if (meter) {
    const label = meter.querySelector('span:last-child');
    if (percentage === null) {
      if (label) label.textContent = '–';
      meter.setAttribute('aria-label', 'Context usage unavailable');
      meter.title = 'Context usage is unavailable until the provider reports it';
      meter.querySelector('.meter-ring')?.style.setProperty('--context-fill', '0%');
      delete meter.dataset.context;
      meter.dataset.availability = 'unavailable';
    } else {
      delete meter.dataset.availability;
      if (label) label.textContent = labelText;
      meter.setAttribute('aria-label', `Context usage: ${labelText.replace('%', ' percent')}`);
      meter.title = formatContextUsage(model.fill.tokens, model.fill.window, model.fill.source === 'estimated');
      meter.querySelector('.meter-ring')?.style.setProperty('--context-fill', `${Math.min(100, Math.max(0, percentage))}%`);
      if (percentage >= 95) meter.dataset.context = 'critical';
      else if (percentage >= 80) meter.dataset.context = 'high';
      else delete meter.dataset.context;
    }
  }
  return {
    percentage,
    tokens: model.fill.tokens,
    windowSize: model.fill.window,
    estimated: model.fill.source === 'estimated',
  };
}

function syncRoomInfo() {
  updateRoomInfo(activeConversation);
}

function removeLiveRoundEvents(messageID) {
  const prefix = `live-${messageID}-`;
  for (const [id] of pendingLiveWidgets) {
    if (id.startsWith(prefix)) pendingLiveWidgets.delete(id);
  }
  for (let index = events.length - 1; index >= 0; index--) {
    const event = events[index];
    if (!event?.id?.startsWith(prefix)) continue;
    const escaped = globalThis.CSS?.escape
      ? globalThis.CSS.escape(event.id)
      : String(event.id).replace(/["\\]/g, '\\$&');
    stream.querySelector(`[data-event-id="${escaped}"]`)?.remove();
    liveTurnHost?.querySelector(`[data-event-id="${escaped}"]`)?.remove();
    liveEventIds.delete(event.id);
    events.splice(index, 1);
  }
  syncOlderEventsButton();
  updateFilterCounts();
}

// A replayed live event replaces the canonical snapshot row that was mounted
// for the same persisted step (reload or re-attach mid-round). Drop the twin
// from the event list, any queued patch, and the mounted DOM so the card
// renders once and keep updating from the live event alone.
function evictCanonicalTwins(liveEvent) {
  for (const twin of canonicalTwins(events, liveEvent)) {
    pendingLiveWidgets.delete(twin.id);
    const index = events.indexOf(twin);
    if (index >= 0) events.splice(index, 1);
    const escaped = globalThis.CSS?.escape
      ? globalThis.CSS.escape(twin.id)
      : String(twin.id).replace(/["\\]/g, '\\$&');
    stream.querySelector(`[data-event-id="${escaped}"]`)?.remove();
  }
}

function settleConversation(conversationID, messageID = '') {
  if (!conversationID || activeConversation?.id !== conversationID) return;
  commitLiveTurn();
  activeConversation = { ...activeConversation, status: 'idle' };
  syncComposerAction();
  syncRoomActivity(conversationID, 'idle');
  setStreamActivity('');
  stream.setAttribute('aria-busy', 'false');
  compactionStatus.clear();
  syncToolJobStrip();
  void refreshConversationSnapshot(conversationID, { preserveStream: true, terminal: true, messageID });
  void refreshConversationRooms(conversationID);
}

// liveToolJobs collects the in-flight tool.run events for the strip — the
// widget cards keep their own terminal rendering; the strip is only the
// overview with a per-call stop for streaming tools (exec).
function liveToolJobs() {
  if (!activeConversation?.id) return [];
  const seen = new Set();
  const jobs = [];
  for (const event of events) {
    if (event?.type !== 'tool.run' || event.status !== 'running' || !event.toolCallId) continue;
    if (seen.has(event.toolCallId)) continue;
    seen.add(event.toolCallId);
    if (!toolJobStartedAt.has(event.id)) toolJobStartedAt.set(event.id, Date.now());
    const args = event.args && typeof event.args === 'object' ? event.args : {};
    const primary = String(event.presentation?.action || args.command || args.cmd || args.path || args.query || '')
      .replace(/\s+/g, ' ')
      .trim();
    jobs.push({
      toolCallId: event.toolCallId,
      runId: event.runId || activeRoundStream?.runId || '',
      name: event.tool || 'tool',
      label: primary ? `${event.tool} · ${primary.length > 64 ? `${primary.slice(0, 63)}…` : primary}` : (event.tool || 'tool'),
      startedAt: toolJobStartedAt.get(event.id),
      stopping: toolJobStopping.has(event.toolCallId),
    });
  }
  // Prune bookkeeping for calls that settled or left the event list.
  const liveIds = new Set(events.map((event) => event.id));
  for (const id of toolJobStartedAt.keys()) if (!liveIds.has(id)) toolJobStartedAt.delete(id);
  for (const id of toolJobStopping) if (!seen.has(id)) toolJobStopping.delete(id);
  return jobs;
}

function syncToolJobStrip() {
  toolJobStrip?.sync(liveToolJobs());
}

async function stopToolJob(job) {
  const runID = job?.runId || activeRoundStream?.runId;
  if (!job?.toolCallId || !runID) throw new Error('This tool call is no longer attached to an active run.');
  try {
    await rpc('agent.tools.stop', { run_id: runID, tool_call_id: job.toolCallId });
    toolJobStopping.add(job.toolCallId);
    syncToolJobStrip();
  } catch (error) {
    toast(error.message || 'Could not stop tool call.', 'error');
    throw error;
  }
}

// cancelQueuedSteer mirrors the production strip: agent.turns.cancel-steer
// drops the queued injection and the cancelled event restores the text into
// the composer when it was discarded rather than user-cancelled.
async function cancelQueuedSteer() {
  if (!activeConversation?.id) return;
  const draft = steerByRoom.get(activeConversation.id)?.text || '';
  const cancel = document.getElementById('agent-steer-cancel');
  if (cancel) cancel.disabled = true;
  try {
    await rpc('agent.turns.cancel-steer', { conversation_id: activeConversation.id });
    steerByRoom.delete(activeConversation.id);
    steerQueue?.clear();
    if (draft && !composer.value.trim()) {
      composer.value = draft;
      composer.dispatchEvent(new Event('input'));
    }
  } catch (error) {
    if (cancel) cancel.disabled = false;
    toast(error.message || 'Could not cancel queued steer.', 'error');
  }
}

function roundKey(round = {}) {
  if (!round?.run_id || !round?.message_id) return '';
  return `${round.run_id}:${round.message_id}`;
}

function hasLiveRoundStream() {
  return Boolean(activeRoundStream?.source
    && activeRoundStream.source.readyState !== EventSource.CLOSED);
}

function attachRoundFromLifecycle(payload = {}) {
  const round = roundFromLifecycle(payload);
  if (round.conversation_id !== activeConversation?.id) return;
  if (payload?.run_id && payload?.message_id) openRoundStream(round);
  else if (round.run_id && round.message_id) openRoundStream(round);
  else if (!hasLiveRoundStream()) void reconcileActiveRound(round.conversation_id, round.run_id);
}

function rememberSettledRound(round) {
  const key = roundKey(round);
  if (!key) return;
  settledRoundKeys.add(key);
  // Keep the set bounded; round IDs are unique but a long-lived browser tab
  // should not retain every historical key forever.
  while (settledRoundKeys.size > 48) settledRoundKeys.delete(settledRoundKeys.values().next().value);
}

function syncComposerAction() {
  const steering = activeConversation?.status === 'running';
  messageForm.dataset.turnMode = steering ? 'steer' : 'send';
  sendButton.setAttribute('aria-label', steering ? 'Steer active turn' : 'Send message');
  sendButton.title = steering ? 'Steer active turn' : 'Send message';
  stopTurnButton.hidden = !steering;
  stopTurnButton.disabled = !steering || stoppingTurn;
  stopTurnButton.setAttribute('aria-busy', String(stoppingTurn));
  const compacting = Boolean(activeConversation?.id && compactingRooms.has(activeConversation.id));
  composer.placeholder = steering
    ? 'Steer the active turn…'
    : compacting
      ? 'Compacting context…'
      : 'Ask, edit, or run a command…';
}

function syncCompactAffordances() {
  const button = document.querySelector('#context-compact');
  if (!button) return;
  const id = activeConversation?.id || '';
  const running = activeConversation?.status === 'running';
  const compacting = Boolean(id && compactingRooms.has(id));
  button.disabled = !id || running || compacting;
  button.setAttribute('aria-busy', String(compacting));
  if (!id) button.title = 'Open a conversation to compact it';
  else if (running) button.title = 'Wait for the running turn to finish';
  else if (compacting) button.title = 'Compaction in progress';
  else button.title = 'Summarize older messages and keep the recent tail';
}

function releaseCompactingRoom(id) {
  if (!id) return;
  compactingRooms.delete(id);
  syncCompactAffordances();
  if (id === activeConversation?.id) syncComposerAction();
}

async function compactConversation(id) {
  if (!id || compactingRooms.has(id)) return;
  const room = conversations.find((candidate) => candidate.id === id);
  const running = room?.status === 'running' || (activeConversation?.id === id && activeConversation.status === 'running');
  if (running) {
    toast('Stop the running turn before compacting.', 'info');
    return;
  }
  compactingRooms.add(id);
  syncCompactAffordances();
  if (id === activeConversation?.id) syncComposerAction();
  try {
    const result = await rpc('agent.conversations.compact', { id });
    if (result?.status === 'nothing_to_compact') {
      releaseCompactingRoom(id);
      toast('Nothing older to compact in this conversation.', 'info');
    } else if (result?.status === 'server_managed') {
      releaseCompactingRoom(id);
      toast('This model compacts on the provider during the next turn.', 'info');
    } else if (result?.status !== 'started') {
      releaseCompactingRoom(id);
    }
  } catch (error) {
    releaseCompactingRoom(id);
    toast(error.message || 'Could not compact conversation', 'error');
  }
  syncCompactAffordances();
  if (id === activeConversation?.id) syncComposerAction();
}

async function stopActiveTurn() {
  if (!activeConversation || activeConversation.status !== 'running' || stoppingTurn) return;
  stoppingTurn = true;
  syncComposerAction();
  try {
    let runID = activeRoundStream?.runId;
    if (!runID) {
      const running = await rpc('agent.turns.active', { id: activeConversation.id });
      runID = running?.run_id;
    }
    if (!runID) throw new Error('No active turn to stop');
    await rpc('agent.turns.stop', { run_id: runID });
  } catch (error) {
    composer.setCustomValidity(error.message || 'Could not stop active turn');
    composer.reportValidity();
    setTimeout(() => composer.setCustomValidity(''), 2500);
  } finally {
    stoppingTurn = false;
    syncComposerAction();
  }
}

async function retryTurn(messageID, widget = null) {
  if (!activeConversation?.id) return;
  if (activeConversation.status === 'running') {
    toast('This conversation is still running. Wait for it to finish first.', 'info');
    return;
  }
  const config = composerSelects?.getConfig?.() || {};
  const model = config.model || activeConversation.model || '';
  if (!model) {
    toast('Choose a model first (models with a provider must be imported in Providers).', 'error');
    return;
  }
  widget?.setRetryPending?.(true);
  const conversationID = activeConversation.id;
  try {
    const result = await rpc('agent.turns.retry', {
      conversation_id: conversationID,
      model,
      effort: config.effort && config.effort !== 'auto' ? config.effort : undefined,
      provider_route: config.providerRoute || undefined,
    });

    const failed = events.find((event) => event.type === 'agent.message' && event.messageId === messageID);
    if (failed) {
      if (String(failed.markdown || '').trim()) {
        // Keep useful partial output in place while the backend creates the
        // retry message. The error is no longer actionable after the retry
        // request was accepted.
        failed.status = 'done';
        failed.error = '';
        failed.canRetry = false;
        if (widget) widget.event = failed;
      } else {
        const index = events.indexOf(failed);
        if (index >= 0) events.splice(index, 1);
        widget?.remove?.();
      }
    }
    visibleEventEnd = filterEvents(historicalEvents(), activeFilter).length;
    activeConversation = { ...activeConversation, status: 'running' };
    syncComposerAction();
    syncRoomActivity(conversationID, 'running');
    renderRooms();
    renderContextDock();
    setStreamActivity('thinking');
    void reconcileActiveRound(conversationID, result?.run_id || '');
  } catch (error) {
    widget?.setRetryPending?.(false);
    toast(error.message || 'Could not retry the failed turn.', 'error');
  }
}

const threadFollow = createThreadFollow({
  getThread: () => stream,
  isFollowing: () => followState.pinned,
  onStick: (thread) => { followState.pinGeom = { thread, scrollTop: thread.scrollTop }; },
});

function markFollowIntent(direction) {
  followState.intent = direction;
  if (direction === 'up') {
    followState.detached = true;
    followState.pinned = false;
    threadFollow.cancel();
  }
}

function createEventWidget(event) {
  const widget = document.createElement(eventWidgetTag(event.type, event));
  widget.event = event;
  widget.dataset.eventId = event.id;
  return widget;
}

function visibleEvents() {
  const filtered = filterEvents(historicalEvents(), activeFilter);
  const window = transcriptWindow(filtered.length, visibleEventCount, MAX_MOUNTED_EVENTS, visibleEventEnd ?? filtered.length);
  return { filtered, window, visible: filtered.slice(window.start, window.end) };
}

function olderEventsButton(hidden) {
  const button = document.createElement('button');
  button.className = 'load-older-events';
  button.type = 'button';
  button.textContent = hidden ? `↑ Load older · ${hidden}` : '↑ Load older messages';
  button.addEventListener('click', () => revealOlderEvents());
  return button;
}

function hasOlderHistory(windowHidden = 0) {
  return windowHidden > 0 || (nextChunkIndex >= 0 && !loadingChunk);
}

function historicalEvents() {
  return events.filter((event) => !liveEventIds.has(event?.id) && !anchoredAfterLiveHost.has(event?.id));
}

function displayEvents() {
  const live = events.filter((event) => liveEventIds.has(event?.id));
  const anchored = events.filter((event) => anchoredAfterLiveHost.has(event?.id));
  return [...historicalEvents(), ...live, ...anchored];
}

function anchoredSteerElement() {
  if (liveTurnGeneration !== steerAnchorGeneration) return null;
  for (const id of anchoredAfterLiveHost) {
    const escaped = globalThis.CSS?.escape ? CSS.escape(id) : String(id).replace(/["\\]/g, '\\$&');
    const widget = stream.querySelector(`:scope > [data-event-id="${escaped}"]`);
    if (widget) return widget;
  }
  return null;
}

function ensureLiveTurnHost() {
  if (liveTurnHost?.isConnected) return liveTurnHost;
  liveTurnHost = document.createElement('div');
  liveTurnHost.className = 'live-turn-host';
  liveTurnHost.dataset.liveTurnKey = liveTurnKey || 'active';
  attachLiveHost(stream, liveTurnHost, anchoredSteerElement());
  return liveTurnHost;
}

function mountAnchoredSteerWidgets() {
  if (!anchoredAfterLiveHost.size) return;
  for (const event of events) {
    if (!anchoredAfterLiveHost.has(event?.id)) continue;
    if (!filterEvents([event], activeFilter).length) continue;
    mountWidgetAfterLiveHost(stream, liveTurnHost, event, eventWidgetTag, MAX_MOUNTED_EVENTS);
  }
}

function placeAppliedSteerWidget(event) {
  const generation = event?.steer && steerAnchorGenerations.length
    ? steerAnchorGenerations.shift()
    : steerAnchorGeneration;
  const sameRound = liveTurnGeneration === generation;
  const host = liveTurnHost?.isConnected ? liveTurnHost : null;
  if (!sameRound && host) {
    const widget = mountWidgetAfterLiveHost(stream, null, event, eventWidgetTag, MAX_MOUNTED_EVENTS);
    if (widget) {
      // This row belongs to an already-committed round, not the live one —
      // it must never join a post-host chain.
      delete widget.dataset.afterLiveHost;
      host.parentNode.insertBefore(widget, host);
    }
    return;
  }
  if (sameRound) anchoredAfterLiveHost.add(event.id);
  mountWidgetAfterLiveHost(stream, liveTurnHost, event, eventWidgetTag, MAX_MOUNTED_EVENTS);
}

function mountLiveTurnEvents() {
  const visible = filterEvents(events.filter((event) => liveEventIds.has(event?.id)), activeFilter);
  if (!visible.length) {
    liveTurnHost?.remove();
    liveTurnHost = null;
    return;
  }
  const host = ensureLiveTurnHost();
  for (const event of visible) upsertVisibleWidget(host, event, eventWidgetTag, MAX_MOUNTED_EVENTS);
}

function commitLiveTurn() {
  if (liveTurnHost?.isConnected) commitLiveHost(liveTurnHost);
  liveTurnHost = null;
  liveEventIds.clear();
  liveTurnKey = '';
  liveTurnGeneration += 1;
  anchoredAfterLiveHost.clear();
}

function resetLiveTurnHost() {
  liveTurnHost?.remove();
  liveTurnHost = null;
  liveTurnKey = '';
  liveEventIds.clear();
  liveTurnGeneration += 1;
  anchoredAfterLiveHost.clear();
  steerAnchorGenerations.length = 0;
}

function renderEvents({ forceBottom = false, preserveExisting = false } = {}) {
  if (forceBottom) visibleEventEnd = filterEvents(historicalEvents(), activeFilter).length;
  const { window, visible } = visibleEvents();
  if (preserveExisting) {
    reconcileVisibleWidgets(stream, visible, eventWidgetTag);
    syncOlderEventsButton();
  } else {
    const fragment = document.createDocumentFragment();
    if (window.hidden || nextChunkIndex >= 0) fragment.append(olderEventsButton(window.hidden));
    for (const event of visible) fragment.append(createEventWidget(event));
    stream.replaceChildren(fragment);
  }
  mountLiveTurnEvents();
  mountAnchoredSteerWidgets();
  updateFilterCounts();
  syncToolJobStrip();
  threadFollow.attach(stream);
  if (forceBottom) pinToLatest(false);
}

function waitForNextPaint() {
  return new Promise((resolve) => {
    const view = document.defaultView;
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      clearTimeout(fallback);
      resolve();
    };
    const fallback = setTimeout(finish, 80);
    if (typeof view?.requestAnimationFrame !== 'function') return;
    view.requestAnimationFrame(() => view.requestAnimationFrame(finish));
  });
}

// syncOlderEventsButton keeps one load-older sentinel at the stream head. It
// pages through already-fetched events first; once the in-memory window is
// fully revealed, the same sentinel fetches archived pre-compaction chunks
// (agent.conversations.chunk), matching the production Agent sentinel.
function syncOlderEventsButton() {
  const { window } = visibleEvents();
  const hidden = window.hidden;
  const chunked = nextChunkIndex >= 0;
  let button = stream.querySelector('.load-older-events');
  if (!hidden && !chunked) {
    button?.remove();
    return;
  }
  if (!button) {
    button = olderEventsButton(hidden);
    stream.prepend(button);
  }
  button.disabled = loadingChunk;
  button.textContent = hidden ? `↑ Load older · ${hidden}` : '↑ Load older messages';
  button.setAttribute('aria-label', hidden
    ? `Load older conversation events, ${hidden} hidden`
    : 'Load older conversation messages');
  if (stream.firstElementChild !== button) stream.prepend(button);
}

function revealOlderEvents() {
  if (loadingOlderEvents || loadingChunk) return;
  const { filtered, window } = visibleEvents();
  if (!window.hidden) {
    void loadOlderChunk();
    return;
  }
  loadingOlderEvents = true;
  followState.detached = true;
  followState.intent = 'up';
  followState.pinned = false;
  threadFollow.cancel();
  const anchor = stream.querySelector('[data-event-id]');
  const anchorId = anchor?.dataset.eventId;
  const anchorViewportOffset = anchor ? anchor.offsetTop - stream.scrollTop : 0;
  visibleEventEnd = window.end;
  if (visibleEventCount < Math.min(filtered.length, MAX_MOUNTED_EVENTS)) {
    visibleEventCount = growVisibleCount(filtered.length, visibleEventCount, EVENT_WINDOW_BATCH, MAX_MOUNTED_EVENTS);
  } else {
    visibleEventEnd = shiftWindowOlder(filtered.length, window, EVENT_WINDOW_BATCH, MAX_MOUNTED_EVENTS).end;
  }
  renderEvents({ preserveExisting: true });
  const replacement = [...stream.querySelectorAll('[data-event-id]')]
    .find((widget) => widget.dataset.eventId === anchorId);
  if (replacement) stream.scrollTop = replacement.offsetTop - anchorViewportOffset;
  followState.pinGeom = { thread: stream, scrollTop: stream.scrollTop };
  loadingOlderEvents = false;
}

// loadOlderChunk fetches one archived pre-compaction chunk and prepends its
// events. It does not chain: each Load-older click or scroll-to-top loads one
// chunk so a large archived turn cannot auto-inflate the stream.
async function loadOlderChunk() {
  if (loadingChunk || !activeConversation?.id) return;
  // Snapshot refreshes can re-point nextChunkIndex past indexes already in
  // the transcript (chunk_count grows on compaction) — skip them instead of
  // duplicating their events.
  while (nextChunkIndex >= 0 && loadedChunkIndexes.has(nextChunkIndex)) nextChunkIndex--;
  if (nextChunkIndex < 0) {
    syncOlderEventsButton();
    return;
  }
  const conversationID = activeConversation.id;
  const chunkIndex = nextChunkIndex;
  loadingChunk = true;
  syncOlderEventsButton();
  const loadToken = conversationLoadToken;
  const previousFiltered = filterEvents(historicalEvents(), activeFilter).length;
  const previousHeight = stream.scrollHeight;
  const previousScroll = stream.scrollTop;
  try {
    const result = await rpc('agent.conversations.chunk', { id: conversationID, index: chunkIndex });
    if (loadToken !== conversationLoadToken || activeConversation?.id !== conversationID) return;
    const messages = Array.isArray(result?.messages) ? result.messages : [];
    // Chunks and the live window share message ids: a compaction that lands
    // between loads archives messages the transcript already shows. Filter
    // them so the stream can never render the same event twice.
    const knownIds = new Set(events.map((event) => event.id));
    const chunkEvents = messageEvents(messages, {
      models: window.__widgetModels || [],
      providerID: conversationProviderID(activeConversation),
    }).filter((event) => !knownIds.has(event.id));
    if (!chunkEvents.length) {
      // Empty chunk (or fully overlapped) — remember it so a later click
      // skips it, then continue one archive lower.
      loadedChunkIndexes.add(chunkIndex);
      nextChunkIndex = chunkIndex - 1;
      syncOlderEventsButton();
      return;
    }
    loadedChunkIndexes.add(chunkIndex);
    nextChunkIndex = chunkIndex - 1;
    snapshotMessages = [...messages, ...snapshotMessages];
    events.splice(0, 0, ...chunkEvents);
    // Reveal the newly loaded prefix above the anchored tail: the visible
    // window grows by however many events survive the active filter.
    const filtered = filterEvents(historicalEvents(), activeFilter);
    const addedVisible = filtered.length - previousFiltered;
    visibleEventEnd = (visibleEventEnd ?? previousFiltered) + addedVisible;
    visibleEventCount = Math.min(MAX_MOUNTED_EVENTS, visibleEventCount + Math.max(1, addedVisible));
    followState.detached = true;
    followState.intent = 'up';
    followState.pinned = false;
    threadFollow.cancel();
    renderEvents({ preserveExisting: true });
    updateFilterCounts();
    syncRoomInfo();
    // Restore the scroll position so the user does not jump.
    stream.scrollTop = previousScroll + (stream.scrollHeight - previousHeight);
    followState.pinGeom = { thread: stream, scrollTop: stream.scrollTop };
  } catch {
    if (loadToken === conversationLoadToken && activeConversation?.id === conversationID) {
      // 404 / no more chunks — stop trying.
      nextChunkIndex = -1;
    }
  } finally {
    if (loadToken === conversationLoadToken) {
      loadingChunk = false;
      syncOlderEventsButton();
    }
  }
}

function pinToLatest(smooth = false) {
  const total = filterEvents(historicalEvents(), activeFilter).length;
  if (visibleEventEnd !== null && visibleEventEnd < total) {
    visibleEventCount = Math.min(INITIAL_EVENT_WINDOW, Math.max(1, total));
    visibleEventEnd = total;
    renderEvents({ preserveExisting: true });
  } else {
    visibleEventEnd = total;
  }
  followState.detached = false;
  followState.intent = '';
  followState.pinned = true;
  if (smooth) stream.scrollTo({ top: stream.scrollHeight, behavior: 'smooth' });
  else stickThreadToBottom(stream);
  followState.pinGeom = { thread: stream, scrollTop: stream.scrollTop };
  threadFollow.nudge();
}

function updateFilterCounts() {
  for (const button of document.querySelectorAll('[data-filter]')) {
    const count = filterEvents(displayEvents(), button.dataset.filter).length;
    button.querySelector('span').textContent = count;
  }
}

function selectDockTab(tabName) {
  for (const tab of document.querySelectorAll('[role="tab"][data-tab]')) {
    const active = tab.dataset.tab === tabName;
    tab.setAttribute('aria-selected', String(active));
    tab.tabIndex = active ? 0 : -1;
  }
  for (const panel of dock.querySelectorAll('[role="tabpanel"]')) panel.hidden = panel.id !== `panel-${tabName}`;
}

function narrowPanels() {
  return matchMedia('(max-width: 720px)').matches;
}

function setDrawerOpen(kind, open) {
  const drawer = kind === 'rooms' ? roomDrawer : dock;
  const toggle = document.querySelector(kind === 'rooms' ? '#room-drawer-toggle' : '#context-dock-toggle');
  // Below the overlay breakpoint the panels and the nav rail are fixed
  // overlays that would stack over each other — keep a single active overlay
  // on narrow screens. Desktop keeps the persistent panels untouched.
  if (open && narrowPanels()) {
    const other = kind === 'rooms' ? dock : roomDrawer;
    if (other && !other.hidden) setDrawerOpen(kind === 'rooms' ? 'dock' : 'rooms', false);
    if (!globalNav.hidden) setGlobalNavOpen(false);
  }
  drawer.hidden = !open;
  toggle.setAttribute('aria-expanded', String(open));
  shell.classList.toggle(kind === 'rooms' ? 'rooms-closed' : 'dock-closed', !open);
}

// Shared dismiss path for the narrow-mode overlays: the backdrop click and
// the explicit close buttons/Escape all route here. Mutual exclusion keeps
// a single overlay open; the ordered checks stay defensive so a forced or
// stale state still unwinds one layer per gesture, nav first.
function closeOverlayPanels() {
  if (!narrowPanels()) return false;
  if (shell.classList.contains('is-nav-open')) { setGlobalNavOpen(false); return true; }
  if (!roomDrawer.hidden) { setDrawerOpen('rooms', false); return true; }
  if (!dock.hidden) { setDrawerOpen('dock', false); return true; }
  return false;
}

function panelKeydownHandler(event) {
  if (event.key !== 'Escape' || event.defaultPrevented) return;
  if (closeOverlayPanels()) event.preventDefault();
}

// syncGlobalNavChrome mirrors the drawer state onto the affordances the
// off-canvas rail needs at <=720px: the is-nav-open scrim class on .shell
// (the scrim itself only paints <=680px), inert, and aria-hidden. In-flow
// widths strip them so a phone-layout state cannot strand the desktop rail.
function syncGlobalNavChrome() {
  const overlay = narrowPanels();
  const open = !globalNav.hidden;
  shell.classList.toggle('is-nav-open', overlay && open);
  if (overlay) {
    globalNav.setAttribute('aria-hidden', String(!open));
    if (open) globalNav.removeAttribute('inert');
    else globalNav.setAttribute('inert', '');
  } else {
    globalNav.removeAttribute('inert');
    globalNav.setAttribute('aria-hidden', 'false');
  }
}

function setGlobalNavOpen(open, { focus = true } = {}) {
  const wasOpen = !globalNav.hidden;
  // The rail shares the single-overlay contract with the conversation
  // panels: below the breakpoint opening it dismisses whichever panel is
  // open so only one fixed surface owns the viewport. Closes never enter
  // this branch, so the exclusion cannot recurse.
  if (open && narrowPanels()) {
    if (!roomDrawer.hidden) setDrawerOpen('rooms', false);
    if (!dock.hidden) setDrawerOpen('dock', false);
  }
  globalNav.hidden = !open;
  globalNavToggle.setAttribute('aria-expanded', String(open));
  shell.classList.toggle('global-nav-closed', !open);
  syncGlobalNavChrome();
  // Drawer focus contract inherited from the retired mobile-nav module:
  // opening lands on the active rail item, closing returns to the toggle —
  // only while the rail is an overlay; in-flow widths never steal focus.
  if (focus && open !== wasOpen && narrowPanels()) {
    if (open) (globalNav.querySelector('.global-nav-item.is-active') || globalNav.querySelector('.global-nav-item'))?.focus();
    else globalNavToggle.focus();
  }
  syncGlobalNavExpand();
}

function setGlobalNavExpanded(expanded) {
  const open = Boolean(expanded);
  // Browser-local preference shared with the production shell and the
  // Settings view toggle: 'icons' = compact rail, 'full' = labelled rail.
  localStorage.setItem('nusashell.sidebarMode', open ? 'full' : 'icons');
  shell.classList.toggle('global-nav-expanded', open);
  globalNav.dataset.expanded = String(open);
  syncGlobalNavExpand();
}

// syncGlobalNavExpand keeps the rail footer's label and aria honest for the
// current viewport. While the rail is a fixed overlay (<=720px) it always
// renders labels, so an expand/collapse mode toggle would have no visible
// effect — the footer becomes the drawer's "Close sidebar" affordance and
// aria-expanded tracks the drawer itself. In-flow widths keep the persisted
// expand/collapse preference.
function syncGlobalNavExpand() {
  if (!globalNavExpand) return;
  const label = globalNavExpand.querySelector('.global-nav-expand-label');
  let text;
  if (narrowPanels()) {
    text = 'Close sidebar';
    globalNavExpand.setAttribute('aria-expanded', String(!globalNav.hidden));
  } else {
    const open = shell.classList.contains('global-nav-expanded');
    text = open ? 'Collapse sidebar' : 'Expand sidebar';
    globalNavExpand.setAttribute('aria-expanded', String(open));
  }
  globalNavExpand.setAttribute('aria-label', text);
  globalNavExpand.title = text;
  if (label) label.textContent = text;
}

// The room heading shows the active conversation title only while the Agent
// route owns the surface. Async conversation writes (open/rename/delete
// refreshes) resolve after the user may already have navigated away, so the
// gate derives from the same normalized hash that drives setActiveSurface —
// the previous view-by-view hidden enumeration missed Automation and
// Telemetry and leaked the room title over those page labels.
function agentRouteActive() {
  return normalizePrototypeRoute(location.hash.slice(1)) === 'agent';
}

function syncPrototypeRoute(route) {
  const activeRoute = normalizePrototypeRoute(route);
  for (const item of globalNav.querySelectorAll('[data-global-route]')) {
    const active = item.dataset.globalRoute === activeRoute;
    item.classList.toggle('is-active', active);
    if (active) item.setAttribute('aria-current', 'page');
    else item.removeAttribute('aria-current');
  }
}

let displayedSurface = '';

// A menu visit starts at the mobile index. Selection, filters and drafts stay
// with their owning controllers; in-page Back still restores reading position.
function resetMobileSurface(view) {
  if (!view) return;
  if (view.hasAttribute('data-mobile-pane')) view.dataset.mobilePane = 'list';
  for (const pane of view.querySelectorAll('[data-mobile-pane]')) {
    pane.dataset.mobilePane = 'list';
    pane.dataset.mobileListScroll = '0';
  }
  for (const pane of view.querySelectorAll('[data-mobile-file-pane]')) pane.dataset.mobileFilePane = 'files';
  view.querySelector('#providers-tab')?.click();
  view.querySelector('#learning-tab-overview')?.click();
  view.querySelector('[data-automation-mode="workflows"]')?.click();
  view.scrollTop = 0;
}

function setActiveSurface(route) {
  const activeRoute = normalizePrototypeRoute(route);
  const entering = displayedSurface !== activeRoute;
  displayedSurface = activeRoute;
  dismissOpenDialogs();
  const showAgent = activeRoute === 'agent';
  const showHome = activeRoute === 'home';
  const showSkills = activeRoute === 'skills';
  const showPlugins = activeRoute === 'plugins';
  const showLearning = activeRoute === 'learning';
  const showProviders = activeRoute === 'providers';
  const showLogs = activeRoute === 'logs';
  const showAutomation = activeRoute === 'automation';
  const showTelemetry = activeRoute === 'telemetry';
  const showSettings = activeRoute === 'settings';
  shell.classList.toggle('agent-active', showAgent);
  shell.classList.toggle('home-active', showHome);
  shell.classList.toggle('skills-active', showSkills);
  shell.classList.toggle('plugins-active', showPlugins);
  shell.classList.toggle('learning-active', showLearning);
  shell.classList.toggle('providers-active', showProviders);
  shell.classList.toggle('logs-active', showLogs);
  shell.classList.toggle('automation-active', showAutomation);
  shell.classList.toggle('telemetry-active', showTelemetry);
  shell.classList.toggle('settings-active', showSettings);
  if (conversationSurface) conversationSurface.hidden = !showAgent;
  // The room drawer and context dock belong to the conversation surface. A
  // route change away from Agent closes any narrow-viewport overlay panel so
  // the shared backdrop can never strand over a page that owns no panels;
  // desktop in-flow panels keep their open state untouched.
  if (!showAgent && narrowPanels()) {
    setDrawerOpen('rooms', false);
    setDrawerOpen('dock', false);
  }
  if (homeView) homeView.hidden = !showHome;
  if (skillsView) skillsView.hidden = !showSkills;
  if (pluginsView) pluginsView.hidden = !showPlugins;
  if (learningView) learningView.hidden = !showLearning;
  if (providersView) providersView.hidden = !showProviders;
  if (logsView) logsView.hidden = !showLogs;
  if (automationView) automationView.hidden = !showAutomation;
  if (telemetryView) telemetryView.hidden = !showTelemetry;
  if (settingsView) settingsView.hidden = !showSettings;
  if (entering && narrowPanels()) resetMobileSurface(document.querySelector(`[data-view="${activeRoute}"]`));
  if (roomHeadingTitle) {
    roomHeadingTitle.textContent = showHome
      ? 'Home'
      : showSkills
        ? 'Skills'
        : showPlugins
          ? 'Plugins'
          : showLearning
            ? 'Learning'
            : showProviders
              ? 'Providers'
              : showLogs
                ? 'Logs'
                : showAutomation
                  ? 'Automation'
                  : showTelemetry
                    ? 'Telemetry'
                    : showSettings
                      ? 'Settings'
                      : (activeConversation?.title || 'New chat');
  }
  syncPrototypeRoute(activeRoute);
  if (showAgent && entering && window.__widgetModels) {
    void refreshModelCatalog().catch(error => toast(error.message || 'Could not refresh models.', 'error'));
  }
  if (showHome) homeSearch?.setAttribute('aria-label', 'Search MCP apps');
  if (showSkills) void skillsPage?.refresh();
  if (showPlugins) void pluginsPage?.refresh();
  else pluginsPage?.closeOverlays();
  if (showLearning) learningPage?.activate();
  if (showProviders) void providersPage?.refresh();
  else providersPage?.closeOverlays();
  if (showLogs) void logsPage?.refresh();
  else logsPage?.closeOverlays();
  if (showAutomation) automationPage?.activate();
  else automationPage?.deactivate();
  if (showTelemetry) void telemetryPage?.activate();
  else telemetryPage?.deactivate();
  if (showSettings) settingsPage?.activate();
  else settingsPage?.deactivate();
}

function setHomeSurface(active) {
  setActiveSurface(active ? 'home' : 'agent');
}

function applyPrototypeRoute() {
  // Keep the URL in sync with the applied route: an empty or unknown hash
  // normalizes to #home so the nav state, visible surface, and address bar
  // can never disagree (the route map is the single source of truth).
  const route = normalizePrototypeRoute(location.hash.slice(1));
  if (location.hash !== `#${route}`) history.replaceState(null, '', `#${route}`);
  setActiveSurface(route);
}

function homeMCPKindLabel() {
  return 'MCP + UI';
}

function homeMCPStatus(plugin) {
  const value = String(plugin?.status || plugin?.state || 'idle').trim().toLocaleLowerCase();
  const labels = {
    connected: 'Connected',
    running: 'Running',
    starting: 'Starting',
    stopping: 'Stopping',
    crashed: 'Crashed',
    idle: 'Ready',
  };
  return { value: value || 'idle', label: labels[value] || (value ? value[0].toUpperCase() + value.slice(1) : 'Ready') };
}

function syncHomeMCPCardStatus(id, plugin) {
  if (!homeAppGrid) return;
  const targetID = String(id || '').trim();
  if (!targetID) return;

  let card;
  for (const candidate of homeAppGrid.querySelectorAll('[data-home-app-id]')) {
    if (candidate.dataset.homeAppId === targetID) {
      card = candidate;
      break;
    }
  }
  const status = card?.querySelector('.home-mcp-status');
  if (!status) return;
  const statusValue = homeMCPStatus(plugin);
  status.className = `home-mcp-status is-${statusValue.value}`;
  status.textContent = statusValue.label;
}

function setHomeMCPMessage(text, { error = false, hidden = false } = {}) {
  if (!homeAppMessage) return;
  homeAppMessage.replaceChildren(document.createTextNode(String(text ?? '')));
  homeAppMessage.hidden = hidden;
  homeAppMessage.setAttribute('role', error ? 'alert' : 'status');
}

function setHomeMCPEmptyMessage(total, query) {
  if (!homeAppMessage) return;
  const copy = homeMCPEmptyCopy(total, query);
  const title = document.createElement('strong');
  title.className = 'home-app-message-title';
  title.textContent = copy.title;
  const detail = document.createElement('span');
  detail.className = 'home-app-message-detail';
  detail.textContent = copy.detail;
  homeAppMessage.replaceChildren(title, detail);
  homeAppMessage.hidden = false;
  homeAppMessage.setAttribute('role', 'status');
}

function renderHomeCategoryTabs() {
  if (!homeCategoryTabs) return;
  // Tabs only exist once a real app set is loaded — same timing as
  // production's renderLauncherTabs, which runs inside renderAppGrid after
  // the pairing-quiet/loading/error early returns.
  if (homeMCPPairingQuiet || homeMCPAppsLoading || homeMCPAppsError) {
    homeCategoryTabs.replaceChildren();
    homeCategoryTabs.hidden = true;
    return;
  }
  const categories = homeMCPCategories(homeMCPApps);
  if (!categories.includes(homeMCPCategory)) homeMCPCategory = 'All';
  homeCategoryTabs.hidden = categories.length < 2;
  homeCategoryTabs.replaceChildren();
  for (const category of categories) {
    const active = category === homeMCPCategory;
    const tab = document.createElement('button');
    tab.type = 'button';
    tab.className = `home-category-tab${active ? ' is-active' : ''}`;
    tab.dataset.category = category;
    tab.setAttribute('aria-pressed', String(active));
    tab.textContent = category;
    tab.addEventListener('click', () => {
      if (homeMCPCategory === category) return;
      homeMCPCategory = category;
      renderHomeMCPApps();
    });
    homeCategoryTabs.append(tab);
  }
}

function renderHomeMCPApps() {
  if (!homeAppGrid) return;
  homeAppGrid.replaceChildren();
  homeAppGrid.setAttribute('aria-busy', String(homeMCPAppsLoading));
  if (homeAppRetry) homeAppRetry.hidden = !homeMCPAppsError;
  renderHomeCategoryTabs();

  if (homeMCPAppsLoading) {
    if (homeAppCount) homeAppCount.textContent = 'Loading…';
    setHomeMCPMessage('Loading MCP apps…');
    return;
  }
  // PAIRING_REQUIRED: keep the launcher empty and quiet (no banner, no
  // empty state) so the pairing gate is the dominant UI.
  if (homeMCPPairingQuiet) {
    if (homeAppCount) homeAppCount.textContent = '';
    setHomeMCPMessage('', { hidden: true });
    return;
  }
  if (homeMCPAppsError) {
    if (homeAppCount) homeAppCount.textContent = 'Unavailable';
    setHomeMCPMessage('Could not load MCP apps. Retry to check the connection.', { error: true });
    return;
  }

  const query = homeSearch?.value.trim() || '';
  const visible = filterHomeMCPApps(
    filterHomeMCPAppsByCategory(homeMCPApps, homeMCPCategory),
    query,
  );
  const filtering = homeMCPCategory !== 'All' || Boolean(query);
  if (homeAppCount) {
    homeAppCount.textContent = homeMCPApps.length
      ? (filtering ? `${visible.length} of ${homeMCPApps.length} MCP apps` : `${homeMCPApps.length} MCP apps`)
      : 'No MCP apps';
  }
  if (!visible.length) {
    setHomeMCPEmptyMessage(homeMCPApps.length, query || (homeMCPCategory !== 'All' ? homeMCPCategory : ''));
    return;
  }
  setHomeMCPMessage('', { hidden: true });

  for (const plugin of visible) {
    const item = document.createElement('article');
    item.className = 'home-mcp-card is-launchable';
    item.setAttribute('role', 'listitem');
    item.dataset.homeAppKind = 'ui';
    item.dataset.homeAppId = homePluginID(plugin);

    const action = document.createElement('button');
    action.className = 'home-mcp-card-action';
    action.type = 'button';
    action.addEventListener('click', () => openHomeMCPApp(plugin));
    action.setAttribute('aria-label', `Open ${homePluginName(plugin)}`);

    const mark = document.createElement('span');
    mark.className = 'home-mcp-mark app-mark-lagoon';
    const iconSource = homePluginIconSource(plugin);
    if (iconSource) {
      mark.dataset.iconType = 'image';
      const image = document.createElement('img');
      image.className = 'home-mcp-icon';
      image.alt = '';
      image.loading = 'lazy';
      image.decoding = 'async';
      image.addEventListener('error', () => {
        mark.dataset.iconType = 'glyph';
        const fallback = document.createElement('span');
        fallback.className = 'home-mcp-glyph';
        fallback.setAttribute('aria-hidden', 'true');
        fallback.textContent = homePluginIconGlyph(plugin);
        mark.replaceChildren(fallback);
      }, { once: true });
      image.src = iconSource;
      mark.append(image);
    } else {
      mark.dataset.iconType = 'glyph';
      const glyph = document.createElement('span');
      glyph.className = 'home-mcp-glyph';
      glyph.setAttribute('aria-hidden', 'true');
      glyph.textContent = homePluginIconGlyph(plugin);
      mark.append(glyph);
    }

    const copy = document.createElement('span');
    copy.className = 'home-mcp-copy';
    const name = document.createElement('strong');
    name.textContent = homePluginName(plugin);
    const meta = document.createElement('small');
    const version = plugin?.version ? `v${plugin.version}` : '';
    const category = plugin?.category || homePluginManifest(plugin).category || '';
    meta.textContent = [homeMCPKindLabel(), version || category].filter(Boolean).join(' · ');
    copy.append(name, meta);

    const status = document.createElement('span');
    const statusValue = homeMCPStatus(plugin);
    status.className = `home-mcp-status is-${statusValue.value}`;
    status.textContent = statusValue.label;

    action.append(mark, copy, status);
    item.append(action);
    homeAppGrid.append(item);
  }
  window.lucide?.createIcons({ attrs: { 'stroke-width': 1.7 } });
}

function openHomeMCPApp(plugin) {
  const id = homePluginID(plugin);
  const opened = launchPluginWindow({
    id,
    name: homePluginName(plugin),
    ui: homePluginManifest(plugin).ui,
  });
  if (!opened) {
    toast(`Allow pop-ups to open “${homePluginName(plugin)}”.`, 'error');
    return;
  }

  // Launching a UI is the explicit user intent to use this MCP. Update just
  // the affected badge immediately; replacing the whole grid here caused a
  // visible flash and left the local snapshot stale until the next reload.
  homeMCPApps = markHomeMCPConnected(homeMCPApps, id);
  const connected = homeMCPApps.find((entry) => homePluginID(entry) === id) || { ...plugin, status: 'connected' };
  syncHomeMCPCardStatus(id, connected);
}

async function loadHomeMCPApps() {
  const loadToken = ++homeMCPLoadToken;
  homeMCPAppsLoading = true;
  homeMCPAppsError = null;
  renderHomeMCPApps();
  try {
    const result = await rpc('plugin.list');
    if (loadToken !== homeMCPLoadToken) return;
    homeMCPApps = homeMCPAppsWithUI(result?.plugins);
    homeMCPPairingQuiet = false;
    homeMCPAppsError = null;
  } catch (error) {
    if (loadToken !== homeMCPLoadToken) return;
    homeMCPApps = [];
    if (isPairingRequiredError(error)) {
      homeMCPPairingQuiet = true;
      homeMCPAppsError = null;
    } else {
      homeMCPPairingQuiet = false;
      homeMCPAppsError = error;
    }
  } finally {
    if (loadToken !== homeMCPLoadToken) return;
    homeMCPAppsLoading = false;
    renderHomeMCPApps();
  }
}

function syncHomeSearch() {
  renderHomeMCPApps();
}

function renderTasksDock() {
  const panel = document.querySelector('#panel-tasks');
  if (!panel) return;
  const tasks = liveTodos.map(normalizeTodoItem);
  const brief = String(liveTodoBrief || '').trim();
  if (!tasks.length && !brief) {
    panel.innerHTML = '<p class="dock-empty">No task plan in this conversation.</p>';
    document.querySelector('#tab-tasks span').textContent = '0';
    return;
  }
  const counts = summarizeTodos(tasks);
  document.querySelector('#tab-tasks span').textContent = `${counts.completed}/${counts.total}`;
  panel.replaceChildren();
  const section = document.createElement('section');
  section.className = 'dock-section';
  section.innerHTML = `
    <header class="dock-section-head"><strong>Tasks</strong><span>${counts.completed}/${counts.total}</span></header>
    <div class="dock-progress" aria-hidden="true"><span style="width:${counts.total ? (counts.completed / counts.total) * 100 : 0}%"></span></div>
    ${brief ? '<section class="dock-task-brief" aria-label="Task brief"><div class="dock-task-brief-label">Plan</div><div class="dock-task-brief-content"></div></section>' : ''}
    <div class="dock-task-list"></div>`;
  if (brief) {
    const briefContent = section.querySelector('.dock-task-brief-content');
    void renderMarkdownBody(briefContent, brief);
  }
  const list = section.querySelector('.dock-task-list');
  for (const task of tasks) {
    const label = document.createElement('label');
    label.className = 'dock-task';
    label.dataset.status = task.status;
    label.title = task.status === 'in_progress' ? 'In progress' : task.status === 'completed' ? 'Completed' : 'Pending';
    label.innerHTML = `<input type="checkbox"><span class="dock-check" aria-hidden="true"></span><span></span>`;
    const input = label.querySelector('input');
    input.checked = task.status === 'completed';
    input.indeterminate = task.status === 'in_progress';
    // The checklist mirrors the server-owned todo list; toggling goes through
    // the todo tool, not a local event mutation.
    input.disabled = true;
    input.setAttribute('aria-label', `${task.label || 'Task'} — ${task.status.replace('_', ' ')}`);
    label.querySelector('span:last-child').textContent = task.label;
    if (task.id && activeConversation?.id) {
      const remove = document.createElement('button');
      remove.className = 'dock-task-delete';
      remove.type = 'button';
      remove.textContent = '×';
      remove.title = 'Remove task';
      remove.setAttribute('aria-label', `Remove task: ${task.label || 'task'}`);
      remove.addEventListener('click', (event) => {
        event.preventDefault();
        void deleteTodoItem(task.id, remove);
      });
      label.append(remove);
    }
    list.append(label);
  }
  panel.append(section);
}

// deleteTodoItem removes one checklist item through agent.todos.delete. The
// response carries the updated items/summary, so the dock re-renders from it
// directly.
async function deleteTodoItem(itemId, button) {
  if (!activeConversation?.id || !itemId) return;
  button.disabled = true;
  const conversationID = activeConversation.id;
  try {
    const result = await rpc('agent.todos.delete', { conversation_id: conversationID, ids: [itemId] });
    if (activeConversation?.id !== conversationID) return;
    applyTodoSnapshot(result);
    renderTasksDock();
  } catch (error) {
    button.disabled = false;
    toast(error.message || 'Failed to delete task', 'error');
  }
}

function renderContextDock() {
  const panel = document.querySelector('#panel-context');
  if (!panel) return;
  panel.innerHTML = `
    <div class="context-compact-row">
      <button class="action-btn" id="context-compact" type="button">Compact</button>
    </div>
    <ul class="context-list">
      <li><span class="context-key">Room ID</span><code class="context-value context-room-id"></code></li>
      <li><span class="context-key">Tool calls</span><span class="context-value context-tool-count"></span></li>
      <li><span class="context-key">Messages</span><span class="context-value"></span></li>
    </ul>`;
  const values = panel.querySelectorAll('.context-value');
  const roomID = activeConversation?.id || 'No room selected';
  values[0].textContent = roomID;
  values[0].title = roomID;
  values[1].textContent = String(displayEvents().filter((event) => event.type === 'tool.run').length);
  values[2].textContent = String(activeConversation?.message_count || 0);
  // One view model feeds the dock inspector and the topbar meter so the two
  // surfaces can never disagree about fill, provenance, or unavailability.
  const model = contextViewModel();
  renderContextInspector(panel, model);
  syncContextMeter(model);
  syncRoomInfo();
  panel.querySelector('#context-compact')?.addEventListener('click', () => {
    void compactConversation(activeConversation?.id || '');
  });
  syncCompactAffordances();
}

function updateContextToolCount() {
  const count = document.querySelector('#panel-context .context-tool-count');
  if (count) count.textContent = String(events.filter((event) => event.type === 'tool.run').length);
}

function renderSubagentsDock() {
  const panel = document.querySelector('#panel-subagents');
  document.querySelector('#tab-subagents span').textContent = String(liveSubagents.length);
  if (!liveSubagents.length) {
    panel.innerHTML = '<p class="dock-empty">No subagents in this conversation.</p>';
    return;
  }
  mountSubagentPanel(panel, liveSubagents);
}

function formatRoomTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return '';
  const today = new Date();
  return date.toDateString() === today.toDateString()
    ? date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : date.toLocaleDateString([], { month: 'short', day: 'numeric' });
}

function syncRoomItem(button, conversation) {
  const status = normalizeConversationStatus(conversation?.status);
  const active = isConversationActive({ status });
  const title = conversation?.title || 'New conversation';
  const row = button.matches?.('.room-item') ? button : button.closest('.room-item') || button;
  const main = row.querySelector('.room-item-main') || button;
  const marker = row.querySelector('.room-activity');
  row.classList.toggle('is-running', active);
  row.classList.toggle('is-active', conversation?.id === activeConversation?.id);
  row.dataset.status = status;
  main.setAttribute('aria-label', `${title} — ${active ? 'Agent active' : status.replace(/_/g, ' ')}`);
  if (marker) {
    marker.hidden = !active;
    marker.title = active ? 'Agent active' : '';
  }
  row.querySelector('.room-title').textContent = title;
  row.querySelector('.room-meta').textContent = `${formatRoomTime(conversation?.updated_at)} · ${status.replace(/_/g, ' ')}`;
  const actionsButton = row.querySelector('.room-actions');
  if (actionsButton) {
    actionsButton.setAttribute('aria-label', `Actions for ${title}`);
    actionsButton.title = 'Conversation actions';
  }
}

function syncRoomActivity(conversationID, status) {
  if (!conversationID) return;
  const conversation = conversations.find((candidate) => candidate.id === conversationID);
  if (!conversation) return;
  conversation.status = normalizeConversationStatus(status);
  const button = [...document.querySelectorAll('.room-item')]
    .find((candidate) => candidate.dataset.conversationId === conversationID);
  if (button) syncRoomItem(button, conversation);
}

function syncSubagentToolIdentity(run = {}) {
  const agentName = run.agent_name || run.agent_id || '';
  if (!agentName) return;
  let changed = false;
  for (const [index, event] of events.entries()) {
    const enriched = enrichSubagentToolEvent(event, [run]);
    if (enriched === event) continue;
    events[index] = enriched;
    changed = true;
    const node = [...stream.querySelectorAll('[data-event-id]')]
      .find((candidate) => candidate.dataset.eventId === enriched.id);
    if (node) node.event = enriched;
  }
  return changed;
}

function syncSubagentToolIdentities(runs = []) {
  let changed = false;
  for (const run of runs) changed = syncSubagentToolIdentity(run) || changed;
  // The list request can finish after the initial transcript mount. Reconcile
  // in place so the card picks up its friendly ACP name without losing scroll.
  if (changed) renderEvents({ preserveExisting: true });
  return changed;
}

function renderRooms() {
  // Re-rendered rows orphan any open action menu — dismiss it first.
  closeRoomMenu();
  const list = document.querySelector('.room-list');
  const visibleConversations = filterConversations(conversations, roomSearchQuery);
  if (!visibleConversations.length) {
    const empty = document.createElement('p');
    empty.className = 'room-list-empty';
    empty.setAttribute('role', 'listitem');
    empty.textContent = roomSearchQuery ? `No conversations match “${roomSearchQuery}”.` : 'No conversations yet.';
    list.replaceChildren(empty);
    return;
  }
  list.replaceChildren(...visibleConversations.map((conversation) => {
    const row = document.createElement('div');
    row.className = `room-item${conversation.id === activeConversation?.id ? ' is-active' : ''}`;
    row.role = 'listitem';
    row.dataset.conversationId = conversation.id;
    row.innerHTML = '<button class="room-item-main" type="button"><span class="room-title-line"><span class="room-title"></span><span class="room-activity" aria-hidden="true" hidden></span></span><span class="room-meta"></span></button><span class="room-item-actions"><button class="room-actions" type="button" aria-haspopup="menu" aria-expanded="false"><i data-lucide="ellipsis-vertical" aria-hidden="true"></i></button></span>';
    syncRoomItem(row, conversation);
    const actionsButton = row.querySelector('.room-actions');
    // One local menu for both gestures: the ellipsis button anchors it,
    // right-click positions it at the pointer. Both build identical items.
    const openRoomMenu = (origin = {}) => {
      let unregisterDismiss = () => {};
      const menu = openRoomActionsMenu({
        ...origin,
        restoreTarget: actionsButton,
        items: [
          { id: 'rename', label: 'Rename', icon: 'pencil', onSelect: () => startRoomRename(row, conversation) },
          { id: 'compact', label: 'Compact', icon: 'fold-vertical', onSelect: () => void compactConversation(conversation.id) },
          { id: 'delete', label: 'Delete', icon: 'trash-2', danger: true, onSelect: () => void deleteConversation(conversation.id) },
        ],
        onClose: () => {
          actionsButton.setAttribute('aria-expanded', 'false');
          unregisterDismiss();
        },
      });
      if (!menu) return;
      actionsButton.setAttribute('aria-expanded', 'true');
      unregisterDismiss = registerOverlayDismiss(() => menu.close({ restoreFocus: false }));
    };
    actionsButton.addEventListener('click', (event) => {
      event.stopPropagation();
      if (isRoomMenuOpenFor(actionsButton)) closeRoomMenu();
      else openRoomMenu({ anchor: actionsButton });
    });
    row.addEventListener('contextmenu', (event) => {
      event.preventDefault();
      openRoomMenu({ point: { x: event.clientX, y: event.clientY } });
    });
    row.querySelector('.room-title').addEventListener('dblclick', (event) => {
      event.preventDefault();
      startRoomRename(row, conversation);
    });
    row.querySelector('.room-item-main').addEventListener('click', () => void openConversation(conversation.id));
    return row;
  }));
  window.lucide?.createIcons({ attrs: { 'stroke-width': 1.7 } });
}

async function startNewChat() {
  if (!newChatButton || newChatButton.disabled) return;
  newChatButton.disabled = true;
  newChatButton.setAttribute('aria-busy', 'true');
  try {
    clearDeletedConversationView();
    conversationKey = newConversationKey();
    draftWorkspace = '';
    roomSearchQuery = '';
    if (roomSearchInput) roomSearchInput.value = '';
    syncComposerAction();
    renderRooms();
    workspaceBrowser?.setPath('');
    newChatButton.focus();
  } catch (error) {
    toast(error.message || 'Could not create a new conversation', 'error');
  } finally {
    newChatButton.disabled = false;
    newChatButton.setAttribute('aria-busy', 'false');
  }
}

function clearDeletedConversationView() {
  activeRoundStream?.close();
  closeSubagentStream();
  activeRoundStream = null;
  ++conversationLoadToken;
  ++subagentLoadToken;
  pendingLiveWidgets.clear();
  activeRoundReconciliations.clear();
  resetLiveTurnHost();
  events.splice(0, events.length);
  latestSnapshotRequest.clear();
  visibleEventCount = INITIAL_EVENT_WINDOW;
  visibleEventEnd = 0;
  loadingOlderEvents = false;
  liveTodos = [];
  liveTodoBrief = '';
  liveSubagents = [];
  attachments = [];
  resetContextDetails();
  snapshotMessages = [];
  steerByRoom.clear();
  toolJobStartedAt.clear();
  toolJobStopping.clear();
  chunkCount = 0;
  nextChunkIndex = -1;
  loadedChunkIndexes.clear();
  loadingChunk = false;
  activeConversation = null;
  conversationKey = '';
  draftWorkspace = '';
  followState.detached = false;
  followState.intent = '';
  followState.pinned = true;
  setStreamActivity('');
  stream.setAttribute('aria-busy', 'false');
  compactionStatus.clear();
  steerQueue.clear();
  toolJobStrip.clear();
  askPopover.close();
  composerSelects?.setConversation({});
  workspaceBrowser?.setPath('');
  if (roomHeadingTitle && agentRouteActive()) roomHeadingTitle.textContent = 'New chat';
  renderAttachments();
  renderEvents();
  renderTasksDock();
  renderSubagentsDock();
  renderContextDock();
  syncComposerAction();
}

function invalidateDeletedConversationLoad() {
  activeRoundStream?.close();
  closeSubagentStream();
  activeRoundStream = null;
  ++conversationLoadToken;
  ++subagentLoadToken;
  pendingLiveWidgets.clear();
  activeRoundReconciliations.clear();
}

async function deleteConversation(id) {
  const conversation = conversations.find((candidate) => candidate.id === id);
  if (!conversation) return;
  const title = conversation.title || 'New conversation';
  const ok = await confirmDialog(
    'Delete conversation',
    `"${title}" and all of its messages, attachments, and subagent runs will be removed. Workspace files are kept.`,
    'Delete',
  );
  if (!ok) return;
  try {
    await rpc('agent.conversations.delete', { id });
  } catch (error) {
    toast(error.message || 'Could not delete conversation', 'error');
    return;
  }
  const wasActive = activeConversation?.id === id;
  // Delete success is authoritative even if the follow-up list/get fails.
  // Invalidate reads started before it so they cannot restore this room.
  ++conversationListLoadToken;
  conversations = conversations.filter((candidate) => candidate.id !== id);
  steerByRoom.delete(id);
  if (wasActive) invalidateDeletedConversationLoad();
  renderRooms();
  toast('Conversation deleted', 'success');
  try {
    await refreshConversationList(wasActive ? '' : activeConversation?.id);
  } catch (error) {
    if (activeConversation?.id === id) clearDeletedConversationView();
    console.warn('[widget-conversation] post-delete conversation refresh failed:', error?.message || error);
    toast('Conversation deleted, but could not load the next conversation', 'error');
  }
}

// startRoomRename swaps the room title for an inline input — the explicit
// user-driven rename path (agent.conversations.rename). Enter/blur commits,
// Escape cancels; nothing is written without a user gesture.
function startRoomRename(row, conversation) {
  const titleEl = row.querySelector('.room-title');
  if (!titleEl || row.dataset.renaming === '1') return;
  row.dataset.renaming = '1';
  const input = document.createElement('input');
  input.className = 'room-rename-input';
  input.type = 'text';
  input.value = conversation.title || '';
  input.setAttribute('aria-label', 'Rename conversation');
  input.maxLength = 120;
  let finished = false;
  const finish = (commit) => {
    if (finished) return;
    finished = true;
    delete row.dataset.renaming;
    const title = input.value.trim();
    input.replaceWith(titleEl);
    if (!commit || !title || title === (conversation.title || '')) return;
    void renameConversation(conversation.id, title);
  };
  input.addEventListener('keydown', (event) => {
    event.stopPropagation();
    if (event.key === 'Enter') { event.preventDefault(); finish(true); }
    else if (event.key === 'Escape') { event.preventDefault(); finish(false); }
  });
  // Keep the enclosing open-room button from firing while the input is used.
  for (const type of ['click', 'dblclick', 'pointerdown']) {
    input.addEventListener(type, (event) => event.stopPropagation());
  }
  input.addEventListener('blur', () => finish(true));
  titleEl.replaceWith(input);
  input.focus();
  input.select();
}

async function renameConversation(id, title) {
  try {
    const result = await rpc('agent.conversations.rename', { id, title });
    const updated = result?.conversation || {};
    const target = conversations.find((candidate) => candidate.id === id);
    if (target) Object.assign(target, updated.id ? updated : { title });
    if (activeConversation?.id === id) {
      activeConversation = { ...activeConversation, ...(updated.id ? updated : { title }) };
      if (roomHeadingTitle && agentRouteActive()) {
        roomHeadingTitle.textContent = activeConversation.title || 'New conversation';
      }
      syncRoomInfo();
    }
    renderRooms();
    toast('Conversation renamed', 'success');
  } catch (error) {
    toast(error.message || 'Could not rename conversation', 'error');
  }
}

function applyTodoSnapshot(result = {}, { preserveBrief = false } = {}) {
  liveTodos = (Array.isArray(result?.items) ? result.items : []).map(normalizeTodoItem);
  if (typeof result?.brief === 'string') liveTodoBrief = result.brief;
  else if (!preserveBrief) liveTodoBrief = '';
}

async function loadTodos() {
  if (!activeConversation) return;
  const conversationID = activeConversation.id;
  try {
    const result = await rpc('agent.todos.get', { conversation_id: conversationID });
    // A room switch can happen while the RPC is in flight. Never let a late
    // response paint the previous room's plan over the newly selected room.
    if (activeConversation?.id !== conversationID) return;
    applyTodoSnapshot(result);
  } catch {
    if (activeConversation?.id !== conversationID) return;
    applyTodoSnapshot();
  }
  renderTasksDock();
}

function closeSubagentStream() {
  subagentEventSource?.close();
  subagentEventSource = null;
  subagentStreamConversationID = '';
  streamedSubagentIDs.clear();
  if (subagentStreamReopenTimer) {
    clearTimeout(subagentStreamReopenTimer);
    subagentStreamReopenTimer = null;
  }
}

// A permanent stream failure gets exactly one delayed reopen attempt at a
// time. Room switches cancel the timer through closeSubagentStream; the
// callback re-verifies the room and skips the reopen when a newer stream
// already replaced the dead one.
function scheduleSubagentStreamReopen(conversationID) {
  if (subagentStreamReopenTimer || !conversationID) return;
  subagentStreamReopenTimer = setTimeout(() => {
    subagentStreamReopenTimer = null;
    if (activeConversation?.id !== conversationID) return;
    if (subagentStreamConversationID !== conversationID) return;
    if (subagentEventSource && subagentEventSource.readyState !== EventSource.CLOSED) return;
    openSubagentStream(conversationID);
  }, SUBAGENT_STREAM_REOPEN_MS);
}

function openSubagentStream(conversationID) {
  if (!conversationID || typeof EventSource === 'undefined') return;
  if (subagentEventSource && subagentStreamConversationID === conversationID && subagentEventSource.readyState !== EventSource.CLOSED) return;
  closeSubagentStream();
  const source = new EventSource(`/stream/acp?conversation_id=${encodeURIComponent(conversationID)}`);
  const cursor = { lastSeq: 0 };
  let opened = false;
  subagentEventSource = source;
  subagentStreamConversationID = conversationID;
  source.onopen = () => {
    if (source !== subagentEventSource || activeConversation?.id !== conversationID) return;
    // A native reconnect can skip frames between the drop and the resumed
    // stream — the server resends a snapshot, and the list RPC bridges any
    // state the dead window missed.
    if (opened) void loadSubagents();
    opened = true;
  };
  source.onerror = () => {
    if (source !== subagentEventSource || activeConversation?.id !== conversationID) return;
    // EventSource retries network failures on its own while CONNECTING; a
    // non-2xx response (e.g. 503 at the subscriber cap) fails permanently
    // with readyState CLOSED and no retry, so the stream reopens here.
    if (source.readyState !== EventSource.CLOSED) return;
    void loadSubagents();
    scheduleSubagentStreamReopen(conversationID);
  };
  source.addEventListener('acp.run', (event) => {
    if (source !== subagentEventSource || activeConversation?.id !== conversationID) return;
    let frame;
    try { frame = JSON.parse(event.data); } catch { return; }
    const accepted = acceptSubagentStreamFrame(cursor, frame, conversationID);
    if (!accepted) return;
    if (accepted.kind === 'snapshot') {
      streamedSubagentIDs.clear();
      const seen = new Set();
      for (const run of accepted.runs) {
        seen.add(run.id);
        streamedSubagentIDs.add(run.id);
        upsertLiveSubagent(run);
      }
      liveSubagents = liveSubagents.filter((run) => run.conversationId !== conversationID
        || !['starting', 'running', 'waiting'].includes(String(run.status).toLowerCase())
        || seen.has(run.id));
      renderSubagentsDock();
      void loadSubagents();
      return;
    }
    streamedSubagentIDs.add(accepted.run.id);
    upsertLiveSubagent(accepted.run);
  });
}

async function loadSubagents() {
  if (!activeConversation) return;
  const conversationID = activeConversation.id;
  const loadToken = ++subagentLoadToken;
  try {
    const result = await rpc('acp.runs.list', { conversation_id: conversationID });
    if (loadToken !== subagentLoadToken || activeConversation?.id !== conversationID) return;
    const runs = Array.isArray(result.runs) ? result.runs : [];
    syncSubagentToolIdentities(runs);
    const loaded = runs.map((run) => {
      const previous = liveSubagents.find((candidate) => candidate.id === run.id);
      if (streamedSubagentIDs.has(run.id) && previous) return previous;
      return toSubagentView(run, previous);
    }).filter(Boolean);
    // Keep the latest SSE run snapshot if it raced with this list request.
    const loadedIDs = new Set(loaded.map((run) => run.id));
    const raced = liveSubagents.filter((run) => run.conversationId === conversationID && !loadedIDs.has(run.id));
    liveSubagents = [...loaded, ...raced];
  } catch (error) {
    if (loadToken === subagentLoadToken && activeConversation?.id === conversationID) {
      // Keep a live SSE snapshot visible if a transient list request fails.
      // A room switch clears the list before starting its own load.
      if (!liveSubagents.some((run) => run.conversationId === conversationID)) liveSubagents = [];
      console.warn('[widget-conversation] subagent list failed:', error?.message || error);
    }
  }
  renderSubagentsDock();
}

function upsertLiveSubagent(run) {
  if (!run?.id) return false;
  const identityChanged = syncSubagentToolIdentity(run);
  const previous = liveSubagents.find((candidate) => candidate.id === run.id);
  const conversationID = run.conversation_id || previous?.conversationId || '';
  if (!conversationID || conversationID !== activeConversation?.id) return false;
  const next = toSubagentView(run, previous);
  if (!next) return false;
  const index = liveSubagents.findIndex((candidate) => candidate.id === next.id);
  if (index < 0) liveSubagents = [...liveSubagents, next];
  else liveSubagents = liveSubagents.map((candidate, candidateIndex) => candidateIndex === index ? next : candidate);
  if (identityChanged) renderEvents({ preserveExisting: true });
  renderSubagentsDock();
  return true;
}

function waitForActiveRound(delay) {
  return delay > 0 ? new Promise((resolve) => setTimeout(resolve, delay)) : Promise.resolve();
}

function conversationProviderID(conversation = {}) {
  const model = String(conversation.model || '').trim();
  const separator = model.indexOf(':');
  return separator > 0 ? model.slice(0, separator) : '';
}

// persistProviderRoute writes the composer route picker's value to the active
// room. No-op for drafts (the route rides along with turns.start) and for
// programmatic syncs that resolve to the room's existing provider_route.
async function persistProviderRoute(route) {
  if (!activeConversation?.id) return;
  const next = String(route || '').trim();
  if (next === (activeConversation.provider_route || '')) return;
  const conversationID = activeConversation.id;
  try {
    const result = await rpc('agent.conversations.set-provider', { id: conversationID, provider_route: next });
    if (activeConversation?.id !== conversationID) return;
    activeConversation = { ...activeConversation, ...(result?.conversation || { provider_route: next }) };
    const target = conversations.find((candidate) => candidate.id === conversationID);
    if (target) target.provider_route = activeConversation.provider_route;
    renderRooms();
  } catch (error) {
    toast(error.message || 'Could not update the provider route', 'error');
    composerSelects?.setConversation(activeConversation);
  }
}

// Lifecycle payloads from older websocket bridges sometimes wrap the round
// reference under `last_turn`/`lastTurn` (or `round_ref`) while the canonical
// contract keeps the fields at the top level. Normalize both shapes before
// deciding whether an SSE attachment is needed so a late final-turn signal
// cannot strand the transcript without its stream.
function roundFromLifecycle(payload = {}) {
  const candidates = [
    payload?.last_turn,
    payload?.lastTurn,
    payload?.round_ref,
    typeof payload?.round === 'object' ? payload.round : null,
    payload,
  ].filter((candidate) => candidate && typeof candidate === 'object');
  const pick = (...keys) => candidates
    .map((candidate) => keys.map((key) => candidate[key]).find((value) => value !== undefined && value !== null && value !== ''))
    .find((value) => value !== undefined && value !== null && value !== '');
  return {
    ...payload,
    conversation_id: pick('conversation_id', 'conversationId') || activeConversation?.id || '',
    run_id: pick('run_id', 'runId') || '',
    message_id: pick('message_id', 'messageId') || '',
    round: pick('round') || 1,
  };
}

// Reconcile the active run through the canonical DTO instead of assuming the
// WebSocket lifecycle event arrives before the worker starts publishing. The
// short, bounded retry window covers the normal goroutine scheduling gap and
// keeps a missed WS frame recoverable without polling forever.
function reconcileActiveRound(conversationID, expectedRunID = '') {
  if (!conversationID) return Promise.resolve(false);
  const key = `${conversationID}:${expectedRunID || '*'}`;
  const existing = activeRoundReconciliations.get(key);
  if (existing) return existing;

  const run = (async () => {
    let lastRunning = null;
    for (const delay of ACTIVE_ROUND_RECONCILE_DELAYS) {
      await waitForActiveRound(delay);
      if (activeConversation?.id !== conversationID) return false;

      let running;
      try {
        // TurnActiveResult is a ConversationIDRequest: the wire key is `id`,
        // not the `conversation_id` used by turn start/steer payloads.
        running = await rpc('agent.turns.active', { id: conversationID });
      } catch {
        continue;
      }
      lastRunning = running;
      if (activeConversation?.id !== conversationID) return false;
      if (!running?.active) continue;
      if (expectedRunID && running.run_id && running.run_id !== expectedRunID) return false;

      activeConversation = { ...activeConversation, status: 'running' };
      syncComposerAction();
      renderContextDock();
      // turns.active is authoritative for a queued steer too — a reload or a
      // missed WS frame still restores the pending-steer strip.
      if (running.queued_steer) {
        steerByRoom.set(conversationID, { text: running.queued_steer, steerId: running.queued_steer_id || '' });
        steerQueue?.show(running.queued_steer, running.queued_steer_id || '');
      }
      if (!running.message_id) continue;

      stream.setAttribute('aria-busy', 'true');
      openRoundStream({ ...running, conversation_id: running.conversation_id || conversationID });
      return true;
    }

    // The run may have completed before the WS event was observed. Pull the
    // settled snapshot in place so a fast turn is still visible and does not
    // leave the composer stuck in steering mode.
    if (activeConversation?.id !== conversationID) return false;
    stream.setAttribute('aria-busy', 'false');
    await refreshConversationSnapshot(conversationID, { preserveStream: true, terminal: true, messageID: lastRunning?.message_id || '' });
    return false;
  })();
  const settled = run.finally(() => {
    if (activeRoundReconciliations.get(key) === settled) activeRoundReconciliations.delete(key);
  });
  activeRoundReconciliations.set(key, settled);
  return settled;
}

async function restorePendingAsk(conversationID) {
  askPopover.close();
  const result = await rpc('agent.ask.pending', { conversation_id: conversationID }).catch(() => ({ asks: [] }));
  if (activeConversation?.id !== conversationID) return;
  const pending = result.asks?.at(-1);
  if (pending) {
    const question = applyAskStatus(events, pending, 'running');
    if (question) upsertVisibleWidget(stream, question, eventWidgetTag, MAX_MOUNTED_EVENTS);
    askPopover.show(pending);
  }
}

async function openConversation(id, { preserveStream = false } = {}) {
  // A room switch invalidates any open row action menu.
  closeRoomMenu();
  const needsRoomTransition = Boolean(activeConversation?.id && !preserveStream);
  const loadConversation = async () => {
    const loadToken = ++conversationLoadToken;
    if (!preserveStream) {
      activeRoundStream?.close();
      closeSubagentStream();
      setStreamActivity('');
      settledRoundKeys.clear();
    }
    if (liveRenderFrame) cancelAnimationFrame(liveRenderFrame);
    liveRenderFrame = 0;
    if (providerRetryTimer) clearTimeout(providerRetryTimer);
    providerRetryTimer = null;
    pendingLiveWidgets.clear();
    resetLiveTurnHost();
    steerQueue?.clear();
    toolJobStrip?.clear();
    compactionStatus.clear();
    toolJobStartedAt.clear();
    toolJobStopping.clear();
    const result = await rpc('agent.conversations.get', { id });
    if (loadToken !== conversationLoadToken) return;
    activeConversation = result.conversation;
    conversationKey = '';
    draftWorkspace = '';
    // Room-scoped inspector state must reset before the new room seeds its
    // persisted fill — a stale estimate event or last-turn usage from the
    // previous room never leaks into this view.
    resetContextDetails();
    seedContextUsage(result.conversation);
    snapshotMessages = Array.isArray(result.messages) ? result.messages : [];
    chunkCount = Number(result.conversation?.chunk_count) || 0;
    nextChunkIndex = chunkCount - 1;
    loadedChunkIndexes.clear();
    loadingChunk = false;
    ++subagentLoadToken;
    liveSubagents = [];
    renderSubagentsDock();
    openSubagentStream(activeConversation.id);
    liveTodos = [];
    liveTodoBrief = '';
    events.splice(0, events.length, ...messageEvents(result.messages || [], {
      models: window.__widgetModels || [],
      providerID: conversationProviderID(result.conversation),
    }));
    visibleEventCount = Math.min(INITIAL_EVENT_WINDOW, Math.max(1, events.length));
    visibleEventEnd = events.length;
    followState.detached = false;
    followState.intent = '';
    followState.pinned = true;
    if (roomHeadingTitle && agentRouteActive()) {
      roomHeadingTitle.textContent = activeConversation.title || 'New conversation';
    }
    composerSelects?.setConversation(activeConversation);
    syncComposerAction();
    workspaceBrowser?.setPath(activeConversation.workspace || '');
    renderRooms();
    renderEvents({ forceBottom: true });
    renderTasksDock();
    renderContextDock();
    await Promise.all([loadTodos(), loadSubagents(), restorePendingAsk(id)]);
    if (loadToken !== conversationLoadToken || activeConversation?.id !== id) return;
    await waitForNextPaint();
    if (loadToken !== conversationLoadToken || activeConversation?.id !== id) return;
    if (activeConversation?.status === 'running') {
      const running = await rpc('agent.turns.active', { id }).catch(() => null);
      // A queued steer survives the room switch — restore the strip from the
      // authoritative active-turn DTO (or the room's saved queue when a
      // hidden-room event outpaced this read).
      const savedSteer = steerByRoom.get(id) || null;
      const queuedSteer = running?.queued_steer || savedSteer?.text || '';
      if (running?.active && queuedSteer) {
        steerByRoom.set(id, { text: queuedSteer, steerId: running?.queued_steer_id || savedSteer?.steerId || '' });
        steerQueue.show(queuedSteer, running?.queued_steer_id || savedSteer?.steerId || '');
      }
      if (running?.active && running.message_id) openRoundStream({ ...running, conversation_id: running.conversation_id || id });
      else if (running?.active) void reconcileActiveRound(id, running.run_id);
      else stream.setAttribute('aria-busy', 'false');
    } else {
      stream.setAttribute('aria-busy', 'false');
      syncToolJobStrip();
    }
    syncRoomInfo();
  };
  if (needsRoomTransition) return conversationLoading.run(loadConversation);
  return loadConversation();
}

// Refresh the authoritative transcript without replacing the stream DOM. The
// active SSE round owns live deltas; this snapshot fills in messages written
// by another client and settles optimistic user rows when the lifecycle bus
// catches up.
async function refreshConversationSnapshot(id, { preserveStream = true, terminal = false, preserveRunning = false, messageID = '', anchorNewUserAfterLiveHost = false } = {}) {
  if (!id) return [];
  const requestID = ++snapshotRequestSequence;
  latestSnapshotRequest.set(id, requestID);
  try {
    const loadToken = conversationLoadToken;
    const result = await rpc('agent.conversations.get', { id });
    if (loadToken !== conversationLoadToken || activeConversation?.id !== id) return [];
    if (latestSnapshotRequest.get(id) !== requestID) return [];

    const beforeIds = new Set(events.map((event) => event.id));
    const turnMessages = selectConversationTurnMessages(result.messages || [], messageID);
    const snapshotEvents = messageEvents(turnMessages, {
      models: window.__widgetModels || [],
      providerID: conversationProviderID(result.conversation),
    });
    const liveMessageIDs = new Set(events
      .filter((event) => liveEventIds.has(event.id) && event.messageId)
      .map((event) => event.messageId));
    const stableSnapshot = liveMessageIDs.size
      ? snapshotEvents.filter((event) => !liveMessageIDs.has(event.messageId))
      : snapshotEvents;
    const changed = mergeConversationEvents(events, stableSnapshot);
    const newlyAdded = changed.filter((event) => !beforeIds.has(event.id));
    const placedAfterHost = new Set();
    let placedSteer = false;
    for (const event of newlyAdded) {
      // The applied-steer refresh anchors every new user row: the snapshot is
      // already narrowed to the message_id SteerEvent carried, so that row is
      // the steer itself. A plain refresh only anchors persisted steer rows —
      // the applied refresh that owned them may have been superseded by
      // recovery or compaction traffic.
      if (anchorNewUserAfterLiveHost ? event?.type !== 'user.message' : !event?.steer) continue;
      if (event?.steer) placedSteer = true;
      placeAppliedSteerWidget(event);
      placedAfterHost.add(event.id);
    }
    // The applied refresh placed no steer row — a racing refresh already
    // merged it, so the lock this refresh's event pushed is orphaned.
    if (anchorNewUserAfterLiveHost && !placedSteer) steerAnchorGenerations.pop();
    if (result.conversation) {
      const keepRunning = preserveRunning && activeConversation?.status === 'running';
      activeConversation = {
        ...activeConversation,
        ...result.conversation,
        ...(keepRunning ? { status: 'running' } : {}),
      };
    }
    if (Array.isArray(result.messages)) snapshotMessages = result.messages;
    // A compaction between refreshes raises chunk_count; keep the load-older
    // sentinel pointed at the newest archive that has not been fetched.
    const refreshedChunks = Number(result.conversation?.chunk_count) || 0;
    if (refreshedChunks !== chunkCount) {
      chunkCount = refreshedChunks;
      nextChunkIndex = Math.max(nextChunkIndex, chunkCount - 1);
    }
    const roomIndex = conversations.findIndex((conversation) => conversation.id === id);
    if (roomIndex >= 0 && result.conversation) conversations[roomIndex] = { ...conversations[roomIndex], ...result.conversation };

    composerSelects?.setConversation(activeConversation);
    syncComposerAction();
    renderRooms();
    renderContextDock();

    const filtered = filterEvents(historicalEvents(), activeFilter);
    if (followState.pinned) {
      visibleEventEnd = filtered.length;
      const historicalNew = newlyAdded.filter((event) => !anchoredAfterLiveHost.has(event.id)).length;
      visibleEventCount = Math.min(MAX_MOUNTED_EVENTS, Math.max(1, visibleEventCount + historicalNew));
    }
    const visibleIds = new Set(visibleEvents().visible.map((event) => event.id));
    for (const event of changed) {
      if (placedAfterHost.has(event.id)) continue;
      if (!filterEvents([event], activeFilter).length) continue;
      if (followState.pinned || visibleIds.has(event.id)) upsertVisibleWidget(stream, event, eventWidgetTag, MAX_MOUNTED_EVENTS);
    }
    syncOlderEventsButton();
    updateFilterCounts();
    if (followState.pinned) threadFollow.nudge();

    if (terminal) {
      renderTasksDock();
      await Promise.all([loadTodos(), loadSubagents(), restorePendingAsk(id)]);
    }
    // Keep the option explicit for callers/tests: this path intentionally keeps
    // the stream mounted when preserveStream is true, avoiding completion flash.
    if (!preserveStream) renderEvents({ forceBottom: followState.pinned });
    return changed;
  } catch (error) {
    // Lifecycle refreshes run in the background. Surface a diagnostic without
    // creating an unhandled rejection or replacing a healthy live transcript.
    console.warn('[widget-conversation] conversation refresh failed:', error?.message || error);
    return [];
  }
}

function flushLiveWidgets() {
  const scheduledFrame = liveRenderFrame;
  liveRenderFrame = 0;
  if (scheduledFrame) cancelAnimationFrame(scheduledFrame);
  if (!pendingLiveWidgets.size) return;

  const updates = [...pendingLiveWidgets.values()];
  pendingLiveWidgets.clear();
  const matchingUpdates = updates.filter(({ event }) => filterEvents([event], activeFilter).length);
  const filtered = filterEvents(historicalEvents(), activeFilter);
  if (followState.pinned) {
    visibleEventEnd = filtered.length;
    for (const { event, isNew } of matchingUpdates) {
      if (isNew && !liveEventIds.has(event.id)) {
        visibleEventCount = appendVisibleCount(filtered.length, visibleEventCount, MAX_MOUNTED_EVENTS);
      }
    }
  }
  const visibleIds = new Set(visibleEvents().visible.map(({ id }) => id));
  for (const { event } of matchingUpdates) {
    if (liveEventIds.has(event.id)) {
      upsertVisibleWidget(ensureLiveTurnHost(), event, eventWidgetTag, MAX_MOUNTED_EVENTS);
      continue;
    }
    if (!followState.pinned && !visibleIds.has(event.id)) continue;
    upsertVisibleWidget(stream, event, eventWidgetTag, MAX_MOUNTED_EVENTS);
  }
  mountLiveTurnEvents();
  syncOlderEventsButton();
  if (updates.some(({ isNew }) => isNew)) updateFilterCounts();
  if (updates.some(({ event, isNew }) => isNew && event.type === 'tool.run')) {
    updateContextToolCount();
    syncRoomInfo();
  }
  syncToolJobStrip();
  threadFollow.nudge();
}

function queueLiveWidget(event, isNew) {
  if (String(event?.id || '').startsWith('live-')) {
    liveEventIds.add(event.id);
    liveTurnKey ||= event.messageId || event.id;
    evictCanonicalTwins(event);
  }
  const pending = pendingLiveWidgets.get(event.id);
  pendingLiveWidgets.set(event.id, { event, isNew: Boolean(isNew || pending?.isNew) });
  if (!liveRenderFrame) liveRenderFrame = requestAnimationFrame(flushLiveWidgets);
}

function openRoundStream(round, cursor = { lastSeq: 0, retries: 0 }) {
  const key = roundKey(round);
  if (!key || settledRoundKeys.has(key)) return;
  if (activeRoundStream?.key === key && activeRoundStream.source?.readyState !== EventSource.CLOSED) return;
  activeRoundStream?.close();
  flushLiveWidgets();
  if (liveTurnKey && round.message_id && liveTurnKey !== round.message_id) commitLiveTurn();
  stream.setAttribute('aria-busy', 'true');
  setStreamActivity(Number(round.round) > 1 ? 'planning' : 'thinking', { round: round.round });
  let sealed = false;
  let reconnectTimer = null;
  const pendingToolFrames = [];
  let toolBatchFrame = 0;
  const cancelToolBatch = () => {
    if (!toolBatchFrame) return;
    cancelAnimationFrame(toolBatchFrame);
    toolBatchFrame = 0;
  };
  const flushToolFrames = () => {
    cancelToolBatch();
    if (!pendingToolFrames.length) return;
    const batch = pendingToolFrames.splice(0);
    const touched = new Map();
    for (const frame of batch) {
      const previousLength = events.length;
      applyRoundDelta(events, frame, { messageId: round.message_id, model: activeConversation?.model || '', models: window.__widgetModels || [], runId: round.run_id });
      const event = events.find((candidate) => candidate.id === `live-${round.message_id}-tool-${frame.tool_call_id || 'tool'}`);
      if (event) touched.set(event.id, { event, isNew: events.length > previousLength || touched.get(event.id)?.isNew });
    }
    for (const update of touched.values()) queueLiveWidget(update.event, update.isNew);
  };
  const queueToolFrame = (frame) => {
    pendingToolFrames.push(frame);
    if (!toolBatchFrame) toolBatchFrame = requestAnimationFrame(flushToolFrames);
  };
  const source = new EventSource(roundStreamURL(round, cursor));
  const ownsSource = () => !sealed && activeRoundStream?.source === source;
  const close = () => {
    sealed = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    flushToolFrames();
    source.close();
    setStreamActivity('');
    if (activeRoundStream?.source === source) activeRoundStream = null;
  };
  const retry = () => {
    if (sealed) return;
    flushToolFrames();
    source.close();
    const attempt = cursor.retries || 0;
    if (attempt >= STREAM_RETRY_DELAYS.length) {
      sealed = true;
      setStreamActivity('recovering');
      if (activeRoundStream?.source === source) activeRoundStream = null;
      void recoverRoundStream(round);
      return;
    }
    setStreamActivity('reconnecting');
    cursor.retries = attempt + 1;
    reconnectTimer = setTimeout(() => {
      if (activeConversation?.id === round.conversation_id || !round.conversation_id) openRoundStream(round, cursor);
    }, STREAM_RETRY_DELAYS[attempt]);
    activeRoundStream = { key, source, runId: round.run_id, close: () => { sealed = true; clearTimeout(reconnectTimer); source.close(); } };
  };
  activeRoundStream = { key, source, runId: round.run_id, close };
  source.addEventListener('round.delta', (message) => {
    if (!ownsSource()) return;
    let frame;
    try { frame = JSON.parse(message.data); } catch { retry(); return; }
    const decision = acceptRoundFrame(cursor, frame);
    if (decision.gap) { retry(); return; }
    if (!decision.accept) return;
    cursor.retries = 0;
    if (frame.kind === 'activity') {
      setStreamActivity(frame.activity || 'tool_call', { round: round.round, toolName: frame.name || '' });
      stream.setAttribute('aria-busy', 'true');
      return;
    }
    // Tool calls often arrive as a burst of argument/output deltas. Collect
    // that burst for one animation frame before mutating the event/widget so
    // the shadow card is painted once with a coherent snapshot. Flush before
    // another semantic kind to preserve stream ordering.
    if (frame.kind === 'tool') {
      // A tool start frame means the core has accepted the streamed input and
      // execution owns the UI now. The activity copy is only a pre-execution
      // hint; clearing it here never waits for the tool's eventual output.
      setStreamActivity('');
      queueToolFrame(frame);
      return;
    }
    flushToolFrames();
    setStreamActivity('');
    const previousLength = events.length;
    applyRoundDelta(events, frame, { messageId: round.message_id, model: activeConversation?.model || '', models: window.__widgetModels || [], runId: round.run_id });
    const suffix = frame.kind === 'reasoning' ? 'reasoning'
      : frame.kind === 'text' ? 'answer'
        : `tool-${frame.tool_call_id || 'tool'}`;
    const liveEvent = events.find((event) => event.id === `live-${round.message_id}-${suffix}`);
    if (liveEvent) queueLiveWidget(liveEvent, events.length > previousLength);
  });
  source.addEventListener('round.done', (message) => {
    if (!ownsSource()) return;
    let done;
    try { done = JSON.parse(message.data); } catch { retry(); return; }
    if (roundDoneNeedsReplay(cursor, done)) { retry(); return; }
    flushToolFrames();
    const updated = applyRoundDone(events, round.message_id, done);
    for (const event of updated) queueLiveWidget(event, false);
    flushLiveWidgets();
    stream.setAttribute('aria-busy', 'false');
    setStreamActivity('');
    rememberSettledRound(round);
    close();
    if (done.next) openRoundStream({ ...done.next, conversation_id: round.conversation_id });
    else settleConversation(round.conversation_id, round.message_id);
  });
  source.onerror = () => {
    if (!sealed) retry();
  };
}

// A replay gap or a transient EventSource failure must not remount the whole
// conversation. Remounting replaces every shadow root, resets scroll geometry,
// and creates the one-frame white flash reported at turn completion. Reconcile
// the persisted snapshot in place, then attach the still-active round again if
// the backend confirms it is running.
async function recoverRoundStream(round) {
  const conversationID = round.conversation_id || activeConversation?.id;
  if (!conversationID || activeConversation?.id !== conversationID) return;
  try {
    await refreshConversationSnapshot(conversationID, { preserveStream: true, messageID: round.message_id || '' });
    if (activeConversation?.id !== conversationID) return;
    const running = await rpc('agent.turns.active', { id: conversationID });
    if (running?.active && running.message_id) {
      stream.setAttribute('aria-busy', 'true');
      openRoundStream({ ...running, conversation_id: running.conversation_id || conversationID });
    } else {
      stream.setAttribute('aria-busy', 'false');
      setStreamActivity('');
      await refreshConversationSnapshot(conversationID, { preserveStream: true, terminal: true, messageID: round.message_id || running?.message_id || '' });
    }
  } catch (error) {
    stream.setAttribute('aria-busy', 'false');
    setStreamActivity('');
    console.warn('[widget-conversation] stream recovery failed:', error?.message || error);
  }
}

function setConnectionStatus(status) {
  const meter = document.querySelector('.context-meter');
  meter.dataset.connection = status;
  meter.title = status === 'open' ? 'Backend connected' : `Backend ${status}`;
  window.dispatchEvent(new CustomEvent('nusashell:connection-status', {
    detail: { status: status === 'unavailable' ? 'offline' : status },
  }));
}

async function fileAttachment(file) {
  if (file.type === 'folder') return file;
  if (file.size > 4 * 1024 * 1024) throw new Error(`${file.name} is larger than 4 MiB`);
  const bytes = new Uint8Array(await file.arrayBuffer());
  const detected = inspectAttachmentContent(bytes);
  if (!detected) throw new Error(`${file.name} is not a supported image, PDF, or UTF-8 text file`);
  return {
    type: detected.type,
    name: file.name || 'Attachment',
    media_type: detected.mediaType,
    ...(detected.type === 'text' ? { content: detected.content } : { data_url: toDataURL(bytes, detected.mediaType) }),
  };
}

async function refreshSoundNotifications() {
  try {
    const result = await rpc('settings.get', {}, { timeoutMs: 4000 });
    const settings = result?.settings;
    soundNotificationsEnabled = settings?.sound_notifications !== false;
  } catch {
    // A failed read keeps the previous choice. The first value is enabled.
  }
}

let modelCatalogToken = 0;

async function refreshModelCatalog() {
  const token = ++modelCatalogToken;
  const { models } = await rpc('ai.models.list');
  if (token !== modelCatalogToken) return;
  const selected = activeConversation?.model || composerSelects.model.getSelected()[0] || '';
  const effort = composerSelects.effort.getSelected()[0];
  window.__widgetModels = models || [];
  composerSelects.setModels(window.__widgetModels, selected);
  if (effort && composerSelects.model.getSelected()[0] === selected) composerSelects.effort.setSelected([effort]);
}

async function initializeLiveData() {
  connectWS({ onStatus: setConnectionStatus });
  void loadHomeMCPApps();
  const [, { conversations: rooms }] = await Promise.all([
    rpc('app.info', {}, { timeoutMs: 4000 }),
    rpc('agent.conversations.list'),
    refreshModelCatalog(),
    refreshSoundNotifications(),
    widgetStylesLoaded(),
  ]);
  conversations = rooms || [];
  document.querySelector('.room-list').setAttribute('aria-busy', 'false');
  renderRooms();
  const initial = selectInitialConversation(conversations);
  if (initial) await openConversation(initial.id);
}

function syncFilters() {
  for (const button of document.querySelectorAll('[data-filter]')) {
    const active = button.dataset.filter === activeFilter;
    button.classList.toggle('is-active', active);
    button.setAttribute('aria-pressed', String(active));
  }
}

function renderAttachments() {
  attachmentTray.replaceChildren();
  attachmentTray.hidden = attachments.length === 0;
  for (const file of attachments) {
    const displayName = file.name || 'Attachment';
    const chip = document.createElement('span');
    chip.className = 'attachment-chip';
    const name = document.createElement('span');
    name.textContent = displayName;
    const remove = document.createElement('button');
    remove.type = 'button';
    remove.setAttribute('aria-label', `Remove ${displayName}`);
    remove.textContent = '×';
    remove.addEventListener('click', () => {
      attachments = attachments.filter((candidate) => candidate !== file);
      renderAttachments();
    });
    chip.append(name, remove);
    attachmentTray.append(chip);
  }
}

document.querySelector('#room-drawer-toggle').addEventListener('click', (event) => setDrawerOpen('rooms', event.currentTarget.getAttribute('aria-expanded') !== 'true'));
document.querySelector('#panel-backdrop')?.addEventListener('click', () => closeOverlayPanels());
document.querySelector('#mobile-nav-backdrop')?.addEventListener('click', () => setGlobalNavOpen(false));
document.querySelector('#room-drawer-close')?.addEventListener('click', () => setDrawerOpen('rooms', false));
document.querySelector('#dock-close')?.addEventListener('click', () => setDrawerOpen('dock', false));
document.addEventListener('keydown', panelKeydownHandler);
globalNavToggle.addEventListener('click', () => setGlobalNavOpen(globalNavToggle.getAttribute('aria-expanded') !== 'true'));
// Selecting any rail item dismisses the overlay drawer in the same gesture —
// delegated so the Install/Pet/Settings items without a route close it too.
globalNav.addEventListener('click', (event) => {
  if (narrowPanels() && event.target.closest?.('[data-nav]')) setGlobalNavOpen(false);
});
globalNavExpand?.addEventListener('click', () => {
  // On the overlay rail (<=720px) the mode toggle cannot change anything —
  // labels are always shown there — so the same footer slot closes the
  // drawer instead. Wider viewports keep the persisted expanded/compact
  // preference.
  if (narrowPanels()) { setGlobalNavOpen(false); return; }
  setGlobalNavExpanded(globalNavExpand.getAttribute('aria-expanded') !== 'true');
});
// Restore the persisted rail mode like production's app.js boot path.
setGlobalNavExpanded(localStorage.getItem('nusashell.sidebarMode') !== 'icons');
// Normalize the off-canvas affordances (scrim class, inert, aria-hidden) for
// the current viewport — hot reload can leave them stale relative to [hidden].
syncGlobalNavChrome();
for (const item of globalNav.querySelectorAll('[data-global-route]')) {
  item.addEventListener('click', (event) => {
    const route = item.dataset.globalRoute;
    if (route !== 'home' && route !== 'agent' && route !== 'skills' && route !== 'plugins' && route !== 'learning' && route !== 'providers' && route !== 'logs' && route !== 'automation' && route !== 'telemetry' && route !== 'settings') return;
    event.preventDefault();
    const nextHash = `#${route}`;
    if (location.hash === nextHash) setActiveSurface(route);
    else location.hash = nextHash;
  });
}
window.addEventListener('hashchange', applyPrototypeRoute);
homeSearch?.addEventListener('input', syncHomeSearch);
homeAppRetry?.addEventListener('click', () => { void loadHomeMCPApps(); });
document.addEventListener('keydown', (event) => {
  const search = homeView?.hidden === false
    ? homeSearch
    : skillsView?.hidden === false
      ? skillsSearch
      : pluginsView?.hidden === false
        ? pluginsSearch
        : learningView?.hidden === false
          ? learningSearch
          : providersView?.hidden === false
            ? document.querySelector('#providers-search')
            : logsView?.hidden === false
              ? document.querySelector('#logs-search')
              : automationView?.hidden === false
                ? automationSearch
                : settingsView?.hidden === false
                  ? settingsSearch
                  : null;
  if (event.key !== '/' || !search || event.metaKey || event.ctrlKey || event.altKey) return;
  const target = event.target;
  if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target?.isContentEditable) return;
  event.preventDefault();
  search.focus();
});
document.querySelector('#context-dock-toggle').addEventListener('click', (event) => setDrawerOpen('dock', event.currentTarget.getAttribute('aria-expanded') !== 'true'));
document.querySelector('.context-meter').addEventListener('click', () => {
  setDrawerOpen('dock', true);
  selectDockTab('context');
});
document.querySelector('#attachment-button').addEventListener('click', () => attachmentInput.click());
attachmentInput.addEventListener('change', () => {
  const merged = mergeFiles(attachments, [...attachmentInput.files]);
  if (merged.length > MAX_ATTACHMENTS) toast('A turn can include up to 4 attachments.', 'error');
  attachments = merged.slice(0, MAX_ATTACHMENTS);
  attachmentInput.value = '';
  renderAttachments();
});

function appendTransferFiles(transfer, transferFiles = filesFromDataTransfer(transfer)) {
  const files = transferFiles;
  if (!files.length) return false;
  const merged = mergeFiles(attachments, files);
  if (merged.length > MAX_ATTACHMENTS) toast('A turn can include up to 4 attachments.', 'error');
  attachments = merged.slice(0, MAX_ATTACHMENTS);
  renderAttachments();
  return true;
}

composer.addEventListener('paste', (event) => {
  const files = filesFromDataTransfer(event.clipboardData);
  if (!files.length) return;
  event.preventDefault();
  appendTransferFiles(event.clipboardData, files);
});

composer.addEventListener('dragover', (event) => {
  if (event.dataTransfer?.types?.includes?.('Files')) event.preventDefault();
});

composer.addEventListener('drop', (event) => {
  const transfer = event.dataTransfer;
  const files = filesFromDataTransfer(transfer);
  const hasFolders = Array.from(transfer?.items || []).some(item => item.webkitGetAsEntry?.()?.isDirectory);
  if (!files.length && !hasFolders) return;
  event.preventDefault();
  void folderAttachmentsFromDataTransfer(transfer, name => {
    toast(`Cannot attach folder "${name}" — the browser does not expose filesystem paths. Use the workspace picker instead.`, 'error', 6000);
  }).then(folders => appendTransferFiles(null, [...folders, ...files]));
});

for (const button of document.querySelectorAll('[data-filter]')) {
  button.addEventListener('click', () => {
    activeFilter = button.dataset.filter;
    const total = filterEvents(historicalEvents(), activeFilter).length;
    visibleEventCount = Math.min(INITIAL_EVENT_WINDOW, Math.max(1, total));
    visibleEventEnd = total;
    syncFilters();
    renderEvents({ forceBottom: true });
  });
}

for (const tab of document.querySelectorAll('[role="tab"][data-tab]')) {
  tab.addEventListener('click', () => selectDockTab(tab.dataset.tab));
  tab.addEventListener('keydown', (event) => {
    if (!['ArrowLeft', 'ArrowRight'].includes(event.key)) return;
    const tabs = [...document.querySelectorAll('[role="tab"][data-tab]')];
    const offset = event.key === 'ArrowRight' ? 1 : -1;
    const next = tabs[(tabs.indexOf(tab) + offset + tabs.length) % tabs.length];
    next.click();
    next.focus();
  });
}

document.querySelector('#jump-latest').addEventListener('click', () => pinToLatest(true));

stream.addEventListener('wheel', (event) => {
  if (isNestedScrollerEvent(event.target, stream)) return;
  if (event.deltaY < 0) markFollowIntent('up');
  else if (event.deltaY > 0) markFollowIntent('down');
}, { passive: true });

stream.addEventListener('keydown', (event) => {
  if (['ArrowUp', 'PageUp', 'Home'].includes(event.key) || (event.key === ' ' && event.shiftKey)) {
    markFollowIntent('up');
  }
  if (['ArrowDown', 'PageDown'].includes(event.key) || (event.key === ' ' && !event.shiftKey)) {
    markFollowIntent('down');
  }
  if (event.key === 'End') pinToLatest(false);
});

stream.addEventListener('pointerdown', (event) => {
  const bounds = stream.getBoundingClientRect();
  scrollbarDragging = event.button === 0 && event.clientX >= bounds.right - 24;
}, { passive: true });
window.addEventListener('pointerup', () => { scrollbarDragging = false; }, { passive: true });

stream.addEventListener('touchstart', (event) => {
  stream.dataset.touchY = String(event.touches?.[0]?.clientY ?? '');
}, { passive: true });

stream.addEventListener('touchmove', (event) => {
  if (isNestedScrollerEvent(event.target, stream)) return;
  const previous = Number(stream.dataset.touchY);
  const current = Number(event.touches?.[0]?.clientY);
  if (Number.isFinite(previous) && Number.isFinite(current) && current > previous + 1) {
    markFollowIntent('up');
  } else if (Number.isFinite(previous) && Number.isFinite(current) && current < previous - 1) {
    markFollowIntent('down');
  }
  stream.dataset.touchY = String(current);
}, { passive: true });

stream.addEventListener('scroll', () => {
  const previousTop = followState.pinGeom?.thread === stream ? followState.pinGeom.scrollTop : stream.scrollTop;
  const geometryDirection = stream.scrollTop < previousTop ? 'up' : stream.scrollTop > previousTop ? 'down' : '';
  const intent = followState.intent;
  const scrollbarDirection = scrollbarDragging && geometryDirection ? geometryDirection : '';
  const direction = intent || scrollbarDirection;
  const atTail = isThreadAtBottom(stream, SCROLL_TOLERANCE);
  // Keep a downward resume intent alive until the viewport actually reaches
  // the tail; one wheel event can produce several scroll events.
  followState.intent = intent === 'down' && !atTail ? 'down' : '';

  // A detached reader stays detached through layout/reflow scrolls. Only a
  // deliberate downward gesture (or a scrollbar drag) can opt back into the
  // live tail; checking geometry alone would re-pin after every delta.
  if (followState.detached && geometryDirection === 'down' && !direction) {
    followState.pinned = false;
  } else if (scrollbarDragging && geometryDirection && !atTail) {
    // A scrollbar drag has no wheel signal. Treat every intermediate move
    // away from the tail as an explicit reader detach; otherwise the resize
    // observer can immediately snap the viewport back to the live delta.
    followState.detached = true;
    followState.pinned = false;
    followState.intent = '';
    threadFollow.cancel();
  } else if (direction === 'up') {
    followState.detached = true;
    updateScrollPin(followState, stream, SCROLL_TOLERANCE, { direction: 'up' });
  } else if (direction === 'down' && atTail) {
    followState.detached = false;
    updateScrollPin(followState, stream, SCROLL_TOLERANCE, { direction: 'down' });
  } else if (!followState.detached) {
    updateScrollPin(followState, stream, SCROLL_TOLERANCE);
  }
  followState.pinGeom = { thread: stream, scrollTop: stream.scrollTop };
  if (stream.scrollTop <= 8) revealOlderEvents();
}, { passive: true });

roomSearchInput?.addEventListener('input', (event) => {
  roomSearchQuery = event.currentTarget.value;
  renderRooms();
});

if (newChatButton) newChatButton.addEventListener('click', () => { void startNewChat(); });

window.addEventListener('keydown', (event) => {
  if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'k') return;
  event.preventDefault();
  if (skillsView?.hidden === false) {
    skillsSearch?.focus();
    skillsSearch?.select();
    return;
  }
  if (learningView?.hidden === false) {
    learningSearch?.focus();
    learningSearch?.select();
    return;
  }
  if (providersView?.hidden === false) {
    const search = document.querySelector('#providers-search');
    search?.focus();
    search?.select();
    return;
  }
  if (logsView?.hidden === false) {
    const search = document.querySelector('#logs-search');
    search?.focus();
    search?.select();
    return;
  }
  setDrawerOpen('rooms', true);
  roomSearchInput?.focus();
  roomSearchInput?.select();
});

bindComposerSendKeys(composer, messageForm);

messageForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const content = composer.value;
  if (!content.trim() && !attachments.length) return;
  if (activeConversation?.status !== 'running' && activeConversation?.id && compactingRooms.has(activeConversation.id)) {
    toast('This conversation is compacting. Wait for it to finish first.', 'info');
    return;
  }
  const button = event.currentTarget.querySelector('.send-button');
  button.disabled = true;
  try {
    const outgoingAttachments = await Promise.all(attachments.map(fileAttachment));
    if (!activeConversation && !conversationKey) conversationKey = newConversationKey();
    let started;
    const steering = activeConversation?.status === 'running';
    if (steering) {
      // A queued steer stays on the strip. Painting a transcript bubble now
      // would append it before the live host exists.
      await rpc('agent.turns.steer', { conversation_id: activeConversation.id, text: content, attachments: outgoingAttachments });
    } else {
      const config = composerSelects.getConfig();
      const draftKey = activeConversation ? '' : conversationKey;
      const workspace = activeConversation ? '' : draftWorkspace;
      started = await rpc('agent.turns.start', {
        conversation_id: activeConversation?.id || undefined,
        conversation_key: draftKey || undefined,
        text: content,
        model: config.model,
        effort: config.effort === 'auto' ? undefined : config.effort,
        provider_route: config.providerRoute || undefined,
        workspace: workspace || undefined,
        attachments: outgoingAttachments,
      });
      if (!activeConversation) {
        if (started.conversation_key !== draftKey || !started.conversation_id) {
          throw new Error('Turn start returned an invalid conversation mapping');
        }
        activeConversation = {
          id: started.conversation_id,
          title: content.slice(0, 48) || 'New conversation',
          status: 'running',
          workspace,
          model: config.model,
          effort: config.effort,
          provider_route: config.providerRoute,
        };
        openSubagentStream(activeConversation.id);
        conversationKey = '';
        draftWorkspace = '';
        await refreshConversationRooms(activeConversation.id);
      }
      activeConversation.status = 'running';
      syncComposerAction();
      setStreamActivity('thinking');
      // The run worker emits turn.started asynchronously. Reconcile now as a
      // lossless fallback when that WS frame is delayed or missed entirely.
      void reconcileActiveRound(activeConversation.id, started?.run_id);
    }
    composer.value = '';
    composer.style.height = '';
    attachments = [];
    attachmentInput.value = '';
    renderAttachments();
    activeFilter = 'all';
    syncFilters();
    if (!steering) {
      const sentAt = Date.now();
      const userEvent = {
        // The row is keyed by the persisted user message id turns.start
        // returned, so the authoritative snapshot merge adopts it by identity.
        // Server-side attachment rewrites (file_path on disk save) can no
        // longer fork a second bubble the way fuzzy content matching allowed.
        id: started?.user_message_id || `event-user-${sentAt}`,
        type: 'user.message',
        time: new Date(sentAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
        createdAt: new Date(sentAt).toISOString(),
        content,
        attachments: outgoingAttachments,
        optimistic: true,
      };
      // The backend persists a user message before broadcasting
      // agent.turn.started. If that event won the race while this submit was
      // awaiting RPC, reconciliation adopts the existing row instead of adding
      // a duplicate bubble.
      const changed = mergeConversationEvents(events, [userEvent]);
      const mountedUserEvent = changed[0] || userEvent;
      visibleEventEnd = events.length;
      visibleEventCount = appendVisibleCount(events.length, visibleEventCount, MAX_MOUNTED_EVENTS);
      upsertVisibleWidget(stream, mountedUserEvent, eventWidgetTag, MAX_MOUNTED_EVENTS);
      syncOlderEventsButton();
      updateFilterCounts();
    }
    pinToLatest(false);
    renderRooms();
    renderContextDock();
  } catch (error) {
    composer.setCustomValidity(error.message || 'Could not send message');
    composer.reportValidity();
    setTimeout(() => composer.setCustomValidity(''), 2500);
  } finally {
    button.disabled = false;
    syncComposerAction();
  }
});

stopTurnButton.addEventListener('click', () => { void stopActiveTurn(); });

composer.addEventListener('input', () => {
  composer.style.height = 'auto';
  composer.style.height = `${Math.min(composer.scrollHeight, 180)}px`;
});

stream.addEventListener('widget-compose', (event) => {
  composer.value = event.detail.content;
  composer.dispatchEvent(new Event('input'));
  composer.focus();
});
stream.addEventListener('retry-turn', (event) => {
  const messageID = event.detail?.messageId;
  if (messageID) void retryTurn(messageID, event.detail?.widget || event.target);
});
stream.addEventListener('task-open', () => {
  setDrawerOpen('dock', true);
  selectDockTab('tasks');
});
stream.addEventListener('artifact-open', (event) => {
  // File details now open in the existing type-aware preview overlay. The
  // dock stays focused on tasks, subagents, and context instead of duplicating
  // a second file inspector surface.
  if (event.detail?.path) void openTextPreviewPopup(event.detail.path);
});
stream.addEventListener('tool-stop-request', async (event) => {
  const toolCallId = event.detail?.toolCallId;
  const runID = event.detail?.runId || activeRoundStream?.runId;
  const widget = event.target;
  if (!toolCallId || !runID) {
    widget?.setToolStopPending?.(false);
    toast('This exec is no longer attached to an active run.', 'error');
    return;
  }
  widget?.setToolStopPending?.(true);
  try {
    await rpc('agent.tools.stop', { run_id: runID, tool_call_id: toolCallId });
    toolJobStopping.add(toolCallId);
    syncToolJobStrip();
  } catch (error) {
    widget?.setToolStopPending?.(false);
    toast(error.message || 'Could not stop exec.', 'error');
  }
});
stream.addEventListener('local-file-open', (event) => {
  const path = event.detail?.path;
  if (!path) return;
  // Widget Markdown may emit workspace-relative file links (a href like
  // "report.md" inside reasoning). Absolute paths and data-local-path values
  // pass through untouched; relative hrefs resolve against the room (or
  // draft) workspace so they open the type-aware preview instead of a 404.
  void openTextPreviewPopup(resolveWorkspaceFilePath(path, activeConversation?.workspace || draftWorkspace));
});
stream.addEventListener('subagent-open', (event) => {
  setDrawerOpen('dock', true);
  selectDockTab('subagents');
  const runID = event.detail?.id;
  if (runID) selectSubagentRun(document.querySelector('#panel-subagents'), runID);
});
stream.addEventListener('question-open', async (event) => {
  const toolCallId = event.detail?.event?.toolCallId || '';
  const current = askPopover.current();
  if (current && (!toolCallId || current.tool_call_id === toolCallId)) {
    askPopover.show(current);
    return;
  }
  // A missed agent.ask.pending frame (or a card restored before the event
  // arrived) leaves a running question with no popover state. Recover the
  // pending ask for this call instead of swallowing the click; a settled
  // card matches nothing and stays read-only.
  if (!toolCallId || !activeConversation?.id) return;
  const result = await rpc('agent.ask.pending', { conversation_id: activeConversation.id }).catch(() => null);
  const pending = (result?.asks || []).find((ask) => ask?.tool_call_id === toolCallId);
  if (pending) askPopover.show(pending);
});

askPopover = createAskPopover(askPopoverRoot, { rpc });
composerSelects = initComposerSelects(messageForm, {
  rpc,
  // Persisted per-room provider route (agent.conversations.set-provider).
  // Programmatic resets (room open, model change) resolve to the room's own
  // route, so only a real user pick writes through.
  onRouteChange: (route) => { void persistProviderRoute(route); },
});
toolJobStrip = createToolJobStrip({ doc: document, onStop: stopToolJob, anchor: stream });
steerQueue = createSteerQueue({ doc: document, onCancel: cancelQueuedSteer, anchor: stream });
compactionStatus = createCompactionStatus({ doc: document, stream });
mountRoomInfo({ doc: document, host: conversation });
// shell-init.js owns the #mini-window-btn click handler (it defers to
// pip.js once #agent-thread exists), so app.js only feeds the section's
// visibility via updateRoomInfo(). mountPipAliases maps the production ids
// onto #event-stream / .composer-wrap so pip.js can move the live nodes
// into a Document-PiP window.
mountPipAliases({ doc: document, stream });
syncComposerAction();
initInstallPrompt(globalNavInstall, {
  onAccepted: () => toast('NusaShell installed.', 'success'),
});
workspaceBrowser = initWorkspaceBrowser(messageForm, {
  rpc,
  onApply: async (path) => {
    if (!activeConversation) {
      draftWorkspace = path;
      renderContextDock();
      return;
    }
    const result = await rpc('agent.conversations.set-workspace', { id: activeConversation.id, path });
    activeConversation = result.conversation || { ...activeConversation, workspace: path };
    renderContextDock();
  },
});
renderTasksDock();
renderSubagentsDock();
renderContextDock();
selectDockTab('tasks');
applyPrototypeRoute();
renderHomeMCPApps();
if (matchMedia('(max-width: 1040px)').matches) setDrawerOpen('dock', false);
if (matchMedia('(max-width: 720px)').matches) {
  setDrawerOpen('rooms', false);
  setGlobalNavOpen(false, { focus: false });
}
// A desktop→mobile crossing leaves the wide-screen drawer state dangling:
// reset each drawer exactly once per crossing instead of toggling on every
// resize pixel. Widening never re-opens them — that stays a user action.
let narrowViewport = matchMedia('(max-width: 720px)').matches;
let compactViewport = matchMedia('(max-width: 1040px)').matches;
window.addEventListener('resize', () => {
  const narrow = matchMedia('(max-width: 720px)').matches;
  if (narrow !== narrowViewport) {
    narrowViewport = narrow;
    if (narrow) {
      setDrawerOpen('rooms', false);
      setGlobalNavOpen(false, { focus: false });
    }
    // Crossing the overlay breakpoint swaps the rail footer's role between
    // drawer close and the persisted expand/collapse mode toggle, and the
    // off-canvas affordances (scrim class, inert, aria-hidden) re-normalize
    // in both directions.
    syncGlobalNavChrome();
    syncGlobalNavExpand();
  }
  const compact = matchMedia('(max-width: 1040px)').matches;
  if (compact !== compactViewport) {
    compactViewport = compact;
    if (compact) setDrawerOpen('dock', false);
  }
});

async function refreshConversationRooms(preferredID = activeConversation?.id) {
  const loadToken = ++conversationListLoadToken;
  try {
    const result = await rpc('agent.conversations.list');
    if (loadToken === conversationListLoadToken) {
      conversations = result.conversations || [];
      renderRooms();
    }
  } catch (error) {
    console.warn('[widget-conversation] conversation list refresh failed:', error?.message || error);
  }
  return conversations.find((conversation) => conversation.id === preferredID) || conversations[0];
}

async function refreshConversationList(preferredID = activeConversation?.id) {
  const preferred = await refreshConversationRooms(preferredID);
  const id = preferred?.id || preferredID || conversations[0]?.id;
  if (id) await openConversation(id);
  else if (activeConversation) clearDeletedConversationView();
  return id || '';
}

on('agent.turn.started', (payload) => {
  const round = roundFromLifecycle(payload);
  if (payload?.conversation_key && payload?.conversation_id
    && !activeConversation && conversationKey === payload.conversation_key) {
    activeConversation = {
      id: payload.conversation_id,
      title: 'New conversation',
      status: 'running',
      workspace: draftWorkspace,
    };
    openSubagentStream(activeConversation.id);
    conversationKey = '';
    draftWorkspace = '';
    void refreshConversationRooms(activeConversation.id);
    renderContextDock();
  }
  // Keep the canonical top-level expression visible for older bridge payloads
  // while accepting wrapped `last_turn`/`lastTurn` references as well.
  if (payload?.conversation_id) syncRoomActivity(payload?.conversation_id, 'running');
  else syncRoomActivity(round.conversation_id, 'running');
  if (round.conversation_id !== activeConversation?.id) return;
  activeConversation.status = 'running';
  // turn.done carries the bare model id and no provider_id. Capture the
  // provider that started this run now — the selected model may change before
  // the turn completes and must never be used to price it after the fact.
  lastTurnProviderId = conversationProviderID(activeConversation);
  syncComposerAction();
  renderContextDock();
  // Pull the persisted user turn immediately; another NusaShell client may
  // have authored it, so there is no local optimistic row to rely on.
  void refreshConversationSnapshot(round.conversation_id, { preserveStream: true, preserveRunning: true, messageID: round.message_id });
  if (payload?.run_id && payload?.message_id) openRoundStream(round);
  else if (round.run_id && round.message_id) openRoundStream(round);
  else {
    setStreamActivity(Number(round.round) > 1 ? 'planning' : 'thinking', { round: round.round });
    void reconcileActiveRound(round.conversation_id, round.run_id);
  }
});
// A few older WS bridges called the final-round reference `last-turn` rather
// than `agent.turn.started`. It is only a stream attachment hint, not a second
// terminal state; accepting the aliases keeps the last answer recoverable
// without changing the canonical backend event vocabulary.
for (const eventName of ['last-turn', 'last_turn', 'lastTurn']) {
  on(eventName, (payload) => {
    const round = roundFromLifecycle(payload);
    if (round.conversation_id !== activeConversation?.id) return;
    void refreshConversationSnapshot(round.conversation_id, { preserveStream: true, preserveRunning: true, messageID: round.message_id });
    attachRoundFromLifecycle(payload);
  });
}
// The steer queue is a composer strip, not a transcript bubble. The queued
// text is promoted into the thread only when the backend applies it at a
// safe boundary; until then the strip (and cancel) is the representation.
on('agent.steer.queued', (payload) => {
  const conversationID = payload?.conversation_id || '';
  if (!conversationID) return;
  steerByRoom.set(conversationID, { text: payload?.text || '', steerId: payload?.steer_id || '' });
  if (conversationID !== activeConversation?.id) return;
  steerQueue.show(payload?.text || '', payload?.steer_id || '');
});
on('agent.steer.applied', (payload) => {
  const conversationID = payload?.conversation_id || '';
  steerByRoom.delete(conversationID);
  if (conversationID !== activeConversation?.id) return;
  steerQueue.clear();
  // Capture the round generation before the snapshot await. A commit during
  // that await means the connected host is already the next round.
  const boundaryGeneration = liveEventIds.size ? liveTurnGeneration : liveTurnGeneration - 1;
  steerAnchorGeneration = boundaryGeneration;
  steerAnchorGenerations.push(boundaryGeneration);
  void refreshConversationSnapshot(conversationID, {
    preserveStream: true,
    preserveRunning: true,
    messageID: payload.message_id || payload.messageId || '',
    anchorNewUserAfterLiveHost: true,
  });
});
on('agent.steer.cancelled', (payload) => {
  const conversationID = payload?.conversation_id || '';
  steerByRoom.delete(conversationID);
  if (conversationID !== activeConversation?.id) return;
  steerQueue.clear();
  // A user cancel already restored the draft in the cancel handler.
  if (payload?.reason === 'user') return;
  // The turn ended before a safe boundary: hand the text back so it can be
  // re-sent as a normal message instead of vanishing.
  if (payload?.text && !composer.value.trim()) {
    composer.value = payload.text;
    composer.dispatchEvent(new Event('input'));
    toast('Steer was not applied (turn ended before a safe boundary). Text restored to composer.', 'info');
  }
});
on('agent.context.estimate', (payload) => {
  // Preflight estimate of the next request, not provider usage. It is kept
  // separate from the measured fill so a large estimate cannot overwrite the
  // last provider snapshot and make the meter appear to overflow.
  const { conversation_id, estimated_tokens } = payload || {};
  if (conversation_id !== activeConversation?.id) return;
  contextEstimate = Number(estimated_tokens) || 0;
  contextEstimateEvent = payload;
  renderContextDock();
});
on('agent.compacting', (payload) => {
  if (payload?.conversation_id) compactingRooms.add(payload.conversation_id);
  syncCompactAffordances();
  if (payload?.conversation_id === activeConversation?.id) syncComposerAction();
  if (payload?.conversation_id !== activeConversation?.id) return;
  compactionStatus.show(payload?.run_id || '');
});
on('agent.compacted', (payload) => {
  if (!payload?.conversation_id) return;
  releaseCompactingRoom(payload.conversation_id);
  compactionStatus.clear(payload?.run_id || '');
  if (payload.conversation_id !== activeConversation?.id) return;
  void refreshConversationSnapshot(payload.conversation_id, { preserveStream: true, preserveRunning: true });
});
on('agent.compaction.failed', (payload) => {
  if (!payload?.conversation_id) return;
  releaseCompactingRoom(payload.conversation_id);
  compactionStatus.clear(payload?.run_id || '');
  // Compaction failed but the turn continues with the un-compacted
  // conversation. Warn so the user knows context may fill up soon.
  toast(`Compaction failed: ${payload?.error || 'context compaction failed'}`, 'error');
});
on('agent.auto_continue', (payload) => {
  if (payload?.conversation_id !== activeConversation?.id) return;
  const decision = payload?.decision || {};
  const label = Number(decision.max_auto_continues) > 0
    ? `Continuing tasks… (${decision.continues_used ?? 0}/${decision.max_auto_continues})`
    : `Continuing tasks… (${decision.continues_used ?? 0})`;
  setStreamActivity('auto-continue', { label });
  // Surface the continuation notice in the transcript so the reader sees
  // what triggered the next turn — mirrors the persisted assistant tool card.
  if (payload?.continue_text) {
    const notice = {
      id: `event-announce-${payload.run_id || Date.now()}`,
      type: 'tool.run',
      tool: 'announcement',
      toolCallId: `announce-${payload.run_id || Date.now()}`,
      runId: payload.run_id || '',
      args: {
        type: 'auto_continue',
        continues_used: decision.continues_used ?? 0,
        open_todos: decision.open_todo_count ?? 0,
      },
      output: payload.continue_text,
      status: 'ok',
    };
    events.push(notice);
    queueLiveWidget(notice, true);
  }
});
on('agent.provider.retry', (payload) => {
  const conversationID = payload?.conversation_id || '';
  if (conversationID !== activeConversation?.id) return;
  activeRoundStream?.close();
  if (payload?.message_id) removeLiveRoundEvents(payload.message_id);
  if (providerRetryTimer) clearTimeout(providerRetryTimer);
  stream.setAttribute('aria-busy', 'true');
  setStreamActivity('provider-retry', payload);
  const delay = Math.max(0, Number(payload?.delay_ms) || 0);
  providerRetryTimer = setTimeout(() => {
    providerRetryTimer = null;
    if (activeConversation?.id !== conversationID) return;
    const round = {
      conversation_id: conversationID,
      run_id: payload?.run_id || '',
      message_id: payload?.message_id || '',
      round: Number(payload?.round) || 1,
    };
    if (round.run_id && round.message_id) openRoundStream(round, { lastSeq: 0, retries: 0 });
    else void reconcileActiveRound(conversationID, round.run_id);
  }, delay);
});
for (const eventName of ['agent.tool.started', 'agent.tool.completed']) {
  on(eventName, (payload) => {
    if (payload?.conversation_id !== activeConversation?.id) return;
    if (payload?.run_id && activeRoundStream?.runId && payload.run_id !== activeRoundStream.runId) return;
    if (eventName === 'agent.tool.started' && !hasLiveRoundStream() && payload?.run_id) {
      // A lifecycle signal can outlive an SSE reconnect window. Reattach from
      // the active-turn DTO instead of leaving the tool card to appear only
      // after the terminal snapshot.
      void reconcileActiveRound(payload.conversation_id, payload.run_id);
    }
    // The provider has handed the call to the executor. Keep the streamed
    // tool card responsive and remove only the pre-execution activity hint.
    setStreamActivity('');
  });
}
on('settings.applied', () => {
  void refreshSoundNotifications();
});
on('agent.turn.done', (payload) => {
  const turnSound = agentTurnSound(soundNotificationsEnabled, payload, conversations);
  if (turnSound === 'complete') playComplete(true);
  else if (turnSound === 'error') playError(true);
  const round = roundFromLifecycle(payload);
  const continuing = Boolean(payload?.auto_continue?.should_continue);
  const settled = Boolean(roundKey(round) && settledRoundKeys.has(roundKey(round)));
  // Keep the active room marked as running until its SSE terminal frame has
  // drained. Hidden rooms can settle immediately because they are not
  // rendering this stream.
  if (!continuing && (round.conversation_id !== activeConversation?.id || settled)) {
    if (payload?.conversation_id) syncRoomActivity(payload?.conversation_id, 'idle');
    else syncRoomActivity(round.conversation_id, 'idle');
  }
  if (round.conversation_id !== activeConversation?.id) {
    // Hidden rooms never take the snapshot path below, so their sidebar row
    // (title, updated_at) only goes stale without the list refresh.
    void refreshConversationRooms(round.conversation_id);
    return;
  }
  stoppingTurn = false;
  // Adopt the authoritative provider-measured context fill for the meter
  // (source of truth), so idle reflects real usage instead of an estimate.
  if (Number(payload?.context_tokens) > 0) {
    contextMeasured = Number(payload.context_tokens);
    contextEstimate = Number(payload.context_tokens);
  }
  // Whole-turn usage for the inspector: payload.usage sums every round of
  // this turn. turn.done carries no provider_id — the run message id lets the
  // persisted message resolve it once the snapshot lands, and the provider
  // captured at turn start covers the gap until then.
  lastTurnUsage = {
    usage: payload?.usage && typeof payload.usage === 'object' ? payload.usage : null,
    model: typeof payload?.model === 'string' ? payload.model : '',
    messageId: round.message_id || '',
    providerId: lastTurnProviderId,
  };
  renderContextDock();
  compactionStatus.clear(payload?.run_id);
  // turn.done is emitted before the round registry seals. Let a healthy SSE
  // source drain its terminal frame; if the source was lost, attach to the
  // authoritative final round so replayed deltas are not missed.
  if (!hasLiveRoundStream() && round?.run_id && round?.message_id && !settledRoundKeys.has(roundKey(round))) openRoundStream(round);
  else if (!hasLiveRoundStream() && !round?.message_id) void reconcileActiveRound(round.conversation_id, round.run_id);
  // Settle in place first, then refresh room metadata and docks. A full
  // openConversation remount here caused the one-frame white flash and
  // kicked readers back to the latest delta.
  void refreshConversationSnapshot(round.conversation_id, {
    preserveStream: true,
    preserveRunning: !settled,
    messageID: round.message_id,
  });
  if (settled) settleConversation(round.conversation_id);
});
on('agent.turn.error', (payload) => {
  const turnSound = agentTurnSound(soundNotificationsEnabled, {
    ...payload,
    error: payload?.error || payload?.message || true,
  }, conversations);
  if (turnSound === 'complete') playComplete(true);
  else if (turnSound === 'error') playError(true);
  const round = roundFromLifecycle(payload);
  if (payload?.conversation_id) syncRoomActivity(payload?.conversation_id, 'idle');
  else syncRoomActivity(round.conversation_id, 'idle');
  if (round.conversation_id !== activeConversation?.id) {
    void refreshConversationRooms(round.conversation_id);
    return;
  }
  const beforeLength = events.length;
  const failed = applyTurnError(events, payload, {
    model: activeConversation?.model || '',
    models: window.__widgetModels || [],
  });
  activeConversation = { ...activeConversation, status: 'idle' };
  stoppingTurn = false;
  if (providerRetryTimer) clearTimeout(providerRetryTimer);
  providerRetryTimer = null;
  activeRoundStream?.close();
  setStreamActivity('');
  stream.setAttribute('aria-busy', 'false');
  compactionStatus.clear();
  // A turn-fatal error strands any still-running tool rows; mark them
  // interrupted locally so the tool strip and cards drain immediately.
  for (const event of events) {
    if (event.type === 'tool.run' && event.status === 'running'
      && (!round.message_id || event.messageId === round.message_id)) {
      event.status = 'interrupted';
      queueLiveWidget(event, false);
    }
  }
  if (failed) queueLiveWidget(failed, events.length > beforeLength);
  flushLiveWidgets();
  commitLiveTurn();
  syncToolJobStrip();
  syncComposerAction();
  renderRooms();
  toast(payload?.message || 'Turn failed', 'error');
  void refreshConversationSnapshot(round.conversation_id, { preserveStream: true, terminal: true, messageID: round.message_id });
  void refreshConversationRooms(round.conversation_id);
});
on('agent.todo.updated', (payload) => {
  if (payload?.conversation_id !== activeConversation?.id) return;
  if (Array.isArray(payload.items)) {
    applyTodoSnapshot(payload, { preserveBrief: true });
    renderTasksDock();
    return;
  }
  void loadTodos();
});
on('agent.ask.pending', (payload) => {
  if (payload?.conversation_id !== activeConversation?.id) return;
  playAsk(soundNotificationsEnabled);
  const question = applyAskStatus(events, payload, 'running');
  if (question) upsertVisibleWidget(stream, question, eventWidgetTag, MAX_MOUNTED_EVENTS);
  askPopover.show(payload);
});
on('agent.ask.answered', (payload) => {
  if (payload?.conversation_id !== activeConversation?.id) return;
  const question = applyAskStatus(events, payload, 'ok');
  if (question) upsertVisibleWidget(stream, question, eventWidgetTag, MAX_MOUNTED_EVENTS);
  askPopover.close();
});
on('agent.ask.cancelled', (payload) => {
  if (payload?.conversation_id !== activeConversation?.id) return;
  const question = applyAskStatus(events, payload, 'interrupted');
  if (question) upsertVisibleWidget(stream, question, eventWidgetTag, MAX_MOUNTED_EVENTS);
  askPopover.close();
});

initializeLiveData().catch((error) => {
  setConnectionStatus('unavailable');
  stream.setAttribute('aria-busy', 'false');
  // error.message is backend/upstream-controlled text — build the node with
  // textContent, never innerHTML.
  const empty = document.createElement('p');
  empty.className = 'stream-empty';
  empty.textContent = error?.message || 'Backend unavailable';
  stream.replaceChildren(empty);
}).finally(() => hidePageLoading(pageLoading));
