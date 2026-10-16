/* ACP subagent transcript reader. Delegation cards open the popup; the
 * conversation dock syncs transcripts through syncTranscript. */

import { rpc } from '../../rpc.js';
import { el, fmtTime } from '../../ui.js';
import { updateScrollPin, isNestedScrollerEvent } from '../../agent-ui.js';
import { renderMarkdown } from '../../markdown.js';
import { incrementalRender } from '../../incremental-render.js';
import { highlightCode } from '../../highlight-render.js';
import { attachZoomButtons } from '../../media-zoom.js';
import { renderToolJob, reasoningDisclosure, setReasoningSource, setToolTerminalOutput, toolEventRequestLine } from './render.js';
import { createSubagentPromptCard, syncSubagentPromptText } from './subagent-prompt.js?v=1';

const LIVE = new Set(['starting', 'running', 'waiting_permission']);
const TERMINAL = new Set(['completed', 'failed', 'cancelled']);
const RECENT_MS = 2 * 60 * 1000;

const state = {
  runs: new Map(),
  conversationId: '',
  popupRunId: '',
  runLoads: new Map(),
  runLoadErrors: new Map(),
  activityTimer: null,
  streamedRunIDs: new Set(),
  // connectionEpoch and streamRevision let a historical load discard itself
  // when a newer generation has already replaced the run.
  connectionEpoch: 0,
  streamRevision: 0,
  streamRevisions: new Map(),
  popupBound: false,
  popupReturnFocus: null,
};

// Per-run follow pins for ACP transcripts. Independent from the parent Agent
// conversation pin (state.pinned / savedRooms) and from other subagent runs.
const followByRunId = new Map();
const boundFollowScrollers = new WeakSet();

// Delegation cards look up agent_id → name. Nothing in this prototype fills
// the map, so unresolved ids fall back in the card renderer.
const agentNames = new Map();

export function agentNameForId(id) {
  if (!id) return '';
  return agentNames.get(id) || '';
}

// A persisted subagent card can open before that run is in the local map.
// Load the exact run through acp.runs.get instead of depending on a
// process-local cache. ensureRun coalesces concurrent loads of one run within
// a single generation (epoch + room). A response or error is applied only
// while that generation is still current; otherwise the cached run (or null)
// is returned and nothing is shown as a failure. Invalidated loads re-render,
// which requests the run again only if it is still selected, so a genuine
// failure is shown once and not retried.
function ensureRun(runId) {
  if (!runId || state.runs.has(runId)) return Promise.resolve(state.runs.get(runId));
  const epoch = state.connectionEpoch;
  const conversationID = state.conversationId;
  const pending = state.runLoads.get(runId);
  if (pending && pending.epoch === epoch && pending.conversationID === conversationID) return pending.promise;
  const startRevision = state.streamRevision;
  const invalidated = () => state.connectionEpoch !== epoch || state.conversationId !== conversationID;
  const superseded = () => invalidated() || streamedSince(runId, startRevision);
  const entry = { epoch, conversationID, promise: null };
  entry.promise = rpc('acp.runs.get', { id: runId })
    .then((result) => {
      const run = result?.run || result;
      if (!run?.id) throw new Error('ACP run response did not include an id');
      if (!superseded()) upsertRun(run);
      return state.runs.get(runId) ?? null;
    })
    .catch((error) => {
      if (superseded()) return state.runs.get(runId) ?? null;
      state.runLoadErrors.set(runId, error);
      renderAll();
      return null;
    })
    .finally(() => {
      if (state.runLoads.get(runId) !== entry) return;
      state.runLoads.delete(runId);
      if (!state.runs.has(runId) && superseded()) renderAll();
    });
  state.runLoads.set(runId, entry);
  return entry.promise;
}

// runReplaces decides whether an incoming run may replace the stored one.
// Terminal status is never reverted, a strictly older stamp never wins, and
// an HTTP response can replace a stream-owned run only when it is strictly
// fresher or explicitly advances it to a terminal status. Equal or unknown
// stamps are ties: the stream remains authoritative for nonterminal state.
function runReplaces(existing, incoming, { streamed, storedFromStream }) {
  if (!existing) return true;
  const incomingAt = runStamp(incoming);
  const existingAt = runStamp(existing);
  if (incomingAt && existingAt && incomingAt < existingAt) return false;
  const incomingTerminal = TERMINAL.has(incoming.status);
  if (TERMINAL.has(existing.status) && !incomingTerminal) return false;
  if (streamed || !storedFromStream) return true;
  if (incomingAt && existingAt && incomingAt > existingAt) return true;
  return incomingTerminal && !TERMINAL.has(existing.status);
}

function runStamp(run) {
  return Date.parse(run?.updated_at || '') || 0;
}

function upsertRun(run, { silent, streamed = false } = {}) {
  if (!run?.id) return;
  const storedFromStream = state.streamedRunIDs.has(run.id);
  if (!runReplaces(state.runs.get(run.id), run, { streamed, storedFromStream })) return;
  if (streamed) state.streamedRunIDs.add(run.id);
  else state.streamedRunIDs.delete(run.id);
  state.runLoadErrors.delete(run.id);
  state.runs.set(run.id, run);
  pruneRuns();
  if (!silent) renderAll();
}

function streamedSince(runID, startRevision) {
  return (state.streamRevisions.get(runID) ?? 0) > startRevision;
}

// pruneRuns drops terminal runs that fell out of the recent window and are
// not part of the active conversation.
// Without this the maps grow for the lifetime of the session even though
// nothing can ever render those runs again. A run mounted in the reader is
// exempt — pruning it would strand the open transcript on a permanent
// loading state.
function pruneRuns() {
  const now = Date.now();
  const active = activeConversationId();
  for (const [id, run] of state.runs) {
    if (id === state.popupRunId) continue;
    if (LIVE.has(run.status)) continue;
    if (active && run.conversation_id === active) continue;
    const ended = Date.parse(run.ended_at || run.updated_at || '') || 0;
    if (!ended || now - ended > RECENT_MS) {
      state.runs.delete(id);
      state.streamedRunIDs.delete(id);
      state.streamRevisions.delete(id);
      followByRunId.delete(id);
    }
  }
}

function activeConversationId() {
  return state.getActiveConversationId?.() || state.conversationId;
}

// Recent runs of the active conversation: live runs, plus runs that settled
// within RECENT_MS. Newest first.
function visibleRuns() {
  const now = Date.now();
  const active = activeConversationId();
  return sortRunsNewestFirst([...state.runs.values()]
    .filter((run) => {
      if (active && run.conversation_id !== active) return false;
      if (LIVE.has(run.status)) return true;
      const ended = Date.parse(run.ended_at || run.updated_at || '') || 0;
      return ended && now - ended < RECENT_MS;
    }));
}

// Every run of the active conversation, settled included, so a completed
// card can still open its transcript after it leaves the recent window.
function conversationRuns() {
  const active = activeConversationId();
  return sortRunsNewestFirst([...state.runs.values()]
    .filter((run) => !active || run.conversation_id === active)
  );
}

function startedAtMs(run) {
  return Date.parse(run.started_at || run.created_at || run.updated_at || '') || 0;
}

// sortRunsNewestFirst also handles old persisted ACP records without a
// timestamp. The index tie-breaker preserves the newest insertion at the top
// for those records, matching the backend's historical ascending list order.
export function sortRunsNewestFirst(runs) {
  return (runs || [])
    .map((run, index) => ({ run, index, startedAt: startedAtMs(run) }))
    .sort((a, b) => b.startedAt - a.startedAt || b.index - a.index)
    .map(({ run }) => run);
}

export function firstVisibleRunId() {
  const visible = visibleRuns();
  if (visible.length) return visible[0].id;
  // Fall back to any run of the active conversation (settled, not recent)
  // so a completed subagent card can still open its transcript.
  return conversationRuns()[0]?.id || '';
}

function renderAll() {
  const popupOverlay = document.getElementById('acp-popup-overlay');
  if (popupOverlay && !popupOverlay.hidden) renderPopup();
  syncActivityTimer();
}

function hasLiveRuns() {
  return [...state.runs.values()].some((run) => LIVE.has(run.status));
}

// Keep elapsed status useful during provider thinking gaps. The backend also
// emits live snapshots, but this local ticker makes the UI continue to tell
// the truth when no transcript delta arrives yet or an SSE update is
// temporarily delayed.
function syncActivityTimer() {
  if (hasLiveRuns()) {
    if (state.activityTimer) return;
    state.activityTimer = setInterval(() => {
      if (!hasLiveRuns()) {
        clearInterval(state.activityTimer);
        state.activityTimer = null;
        return;
      }
      refreshLiveStatusText();
    }, 1000);
    state.activityTimer?.unref?.();
    return;
  }
  if (state.activityTimer) {
    clearInterval(state.activityTimer);
    state.activityTimer = null;
  }
}

function refreshLiveStatusText() {
  for (const panel of document.querySelectorAll('.acp-run-panel[data-run-id]')) {
    const run = state.runs.get(panel.dataset.runId);
    const pill = panel.querySelector('.acp-status-pill');
    if (run && pill) {
      pill.className = `acp-status-pill is-${run.status}`;
      pill.textContent = runStatusText(run);
    }
  }
}

function runDisplayName(run) {
  const title = typeof run?.title === 'string' ? run.title.trim() : '';
  if (title) return title;
  return run?.agent_name || 'ACP';
}

export { runDisplayName };

// Delegation cards call this name. It opens the single-run popup reader.
// Historical runs load on demand through ensureRun / acp.runs.get.
export function openDrawer(runId) {
  openPopup(runId || firstVisibleRunId());
}

function openPopup(runId) {
  const overlay = document.getElementById('acp-popup-overlay');
  if (!overlay) return;
  state.popupRunId = runId;
  state.runLoadErrors.delete(runId);
  // Learning cards and tests open the reader directly, so its controls are
  // wired on first open.
  bindPopupControls(overlay);
  state.popupReturnFocus = document.activeElement;
  overlay.hidden = false;
  renderPopup();
  if (runId && !state.runs.has(runId)) void ensureRun(runId);
  document.getElementById('acp-popup-close')?.focus();
}

function bindPopupControls(overlay) {
  if (state.popupBound) return;
  state.popupBound = true;
  document.getElementById('acp-popup-close')?.addEventListener('click', closePopup);
  overlay.addEventListener('click', (event) => {
    if (event.target === overlay) closePopup();
  });
  overlay.ownerDocument?.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && !overlay.hidden) closePopup();
  });
}

function closePopup() {
  const overlay = document.getElementById('acp-popup-overlay');
  if (!overlay) return;
  overlay.hidden = true;
  state.popupRunId = '';
  const target = state.popupReturnFocus;
  state.popupReturnFocus = null;
  if (target?.isConnected) target.focus();
}

function renderPopup() {
  const run = state.runs.get(state.popupRunId);
  const title = document.getElementById('acp-popup-title');
  const body = document.getElementById('acp-popup-body');
  if (!run) {
    title.textContent = 'Subagent';
    body.replaceChildren(el('p', {
      class: 'acp-empty',
      text: !state.popupRunId
        ? 'No subagent selected.'
        : state.runLoadErrors.has(state.popupRunId)
          ? 'Unable to load this transcript. Close and try again.'
          : 'Loading transcript…',
    }));
    if (state.popupRunId && !state.runLoadErrors.has(state.popupRunId)) void ensureRun(state.popupRunId);
    return;
  }
  title.textContent = runDisplayName(run) || 'Subagent';
  // Same run: patch in place so the popup transcript doesn't reset.
  const existing = body.querySelector(`.acp-run-panel[data-run-id="${run.id}"]`);
  if (!existing) {
    mountRunPanel(body, run);
  } else {
    bindTranscriptFollow(existing);
    patchRunPanel(existing, run);
  }
}

function buildRunPanel(run) {
  const panel = el('div', { class: 'acp-run-panel', 'data-run-id': run.id },
    el('div', { class: 'acp-run-meta' },
      el('span', { class: `acp-status-pill is-${run.status}`, text: runStatusText(run) }),
      el('span', { class: `acp-risk-pill is-${run.risk_tier || 'read_only'}`, text: riskLabel(run.risk_tier) }),
      run.current_model_id ? el('span', { class: 'acp-model-pill', text: modelLabel(run) }) : null,
    ),
    el('div', { class: 'acp-run-workspace', title: run.workspace || '' },
      el('span', { text: 'Workspace' }),
      el('code', { text: run.workspace || '—' }),
    ),
    el('div', {
      class: 'agent-thread acp-transcript',
      role: 'log',
      'aria-label': 'Subagent transcript',
    }),
  );

  if (run.error) {
    panel.append(el('p', { class: 'acp-run-error', text: run.error }));
  }
  syncTranscript(panel, run.transcript || [], run.prompt, run.started_at);
  return panel;
}

// A freshly opened panel scrolls to the live tail only when that run's own
// follow pin is still armed. Pin state is per runId, so one reader does not
// inherit another run's detached/follow lock or the parent conversation pin.
function mountRunPanel(container, run) {
  const panel = buildRunPanel(run);
  container.replaceChildren(panel);
  bindTranscriptFollow(panel);
  const scroller = transcriptScroller(panel);
  if (scroller && getSubagentFollow(run.id).pinned) {
    scroller.scrollTop = scroller.scrollHeight;
  }
  return panel;
}

// patchRunPanel updates a live panel in place: status pill + model pill +
// error, then patches the transcript in place. Auto-follows the bottom only
// when that run's pin is armed — reading earlier output is never interrupted.
function patchRunPanel(panel, run) {
  const pill = panel.querySelector('.acp-status-pill');
  if (pill) {
    pill.className = `acp-status-pill is-${run.status}`;
    pill.textContent = runStatusText(run);
  }
  const modelPill = panel.querySelector('.acp-model-pill');
  if (run.current_model_id) {
    if (modelPill) {
      modelPill.textContent = modelLabel(run);
    } else {
      panel.querySelector('.acp-run-meta')?.append(el('span', { class: 'acp-model-pill', text: modelLabel(run) }));
    }
  }
  const errEl = panel.querySelector('.acp-run-error');
  if (run.error && !errEl) {
    panel.append(el('p', { class: 'acp-run-error', text: run.error }));
  }
  syncTranscriptWithFollow(panel, run.transcript || [], run.prompt, run.started_at);
}

// Per-run follow helpers. Keyed by runId so the popup scroller keeps each
// run's own follow/detach preference and never touches the parent pin.
export function getSubagentFollow(runId) {
  if (!runId) return { pinned: true };
  let follow = followByRunId.get(runId);
  if (!follow) {
    follow = { pinned: true };
    followByRunId.set(runId, follow);
  }
  return follow;
}

export function applySubagentFollowIntent(runId, scroller, direction) {
  if (!runId || !scroller) return getSubagentFollow(runId).pinned;
  return updateScrollPin(getSubagentFollow(runId), scroller, 24, { direction });
}

export function resetSubagentFollowForTests() {
  followByRunId.clear();
}

function transcriptScroller(panel) {
  return panel?.querySelector('.acp-transcript')?.closest('.acp-popup') || null;
}

function runIdFromScroller(scroller) {
  return scroller?.querySelector?.('.acp-run-panel')?.dataset?.runId || '';
}

export function bindTranscriptFollow(panel) {
  const scroller = transcriptScroller(panel);
  if (!scroller || boundFollowScrollers.has(scroller)) return;
  boundFollowScrollers.add(scroller);
  // Resolve the active runId on each gesture so the popup scroller does not
  // share one pin object across runs.
  const applyIntent = (direction) => {
    const runId = runIdFromScroller(scroller);
    if (runId) applySubagentFollowIntent(runId, scroller, direction);
  };
  scroller.addEventListener('wheel', (event) => {
    if (isNestedScrollerEvent(event.target, scroller)) return;
    let dir = '';
    if (event.deltaY < 0) dir = 'up';
    else if (event.deltaY > 0) dir = 'down';
    if (!dir) return;
    applyIntent(dir);
  }, { passive: true });
  // Touch scrolls never emit wheel events; track the finger so a downward
  // drag (reading older output) releases the pin like a wheel scroll-up does.
  let touchY = null;
  scroller.addEventListener('touchstart', (event) => {
    touchY = event.touches?.[0]?.clientY ?? null;
  }, { passive: true });
  scroller.addEventListener('touchmove', (event) => {
    if (isNestedScrollerEvent(event.target, scroller)) return;
    const y = event.touches?.[0]?.clientY;
    if (!Number.isFinite(y) || !Number.isFinite(touchY)) return;
    const delta = y - touchY;
    touchY = y;
    if (Math.abs(delta) >= 1) applyIntent(delta < 0 ? 'down' : 'up');
  }, { passive: true });
  const resetTouch = () => { touchY = null; };
  scroller.addEventListener('touchend', resetTouch, { passive: true });
  scroller.addEventListener('touchcancel', resetTouch, { passive: true });
  scroller.addEventListener('keydown', (event) => {
    if (event.target?.closest?.('input, textarea, [contenteditable="true"]')) return;
    const up = event.key === 'ArrowUp' || event.key === 'PageUp' || event.key === 'Home';
    const down = event.key === 'ArrowDown' || event.key === 'PageDown' || event.key === 'End';
    if (up) applyIntent('up');
    else if (down) applyIntent('down');
  });
  // A scrollbar drag produces scroll events with no gesture signal, so
  // geometry direction is trusted only while the pointer is on the gutter.
  let scrollbarDragging = false;
  let lastScrollTop = scroller.scrollTop;
  scroller.addEventListener('pointerdown', (event) => {
    if (event.button !== 0) return;
    const rect = scroller.getBoundingClientRect?.();
    if (rect && event.clientX >= rect.right - 24) scrollbarDragging = true;
  }, { passive: true });
  scroller.ownerDocument?.defaultView?.addEventListener('pointerup', () => { scrollbarDragging = false; }, { passive: true });
  scroller.addEventListener('scroll', () => {
    const geometryDirection = scroller.scrollTop < lastScrollTop ? 'up' : scroller.scrollTop > lastScrollTop ? 'down' : '';
    lastScrollTop = scroller.scrollTop;
    const runId = runIdFromScroller(scroller);
    if (!runId) return;
    // No direction: only re-pin when geometry says we are at the bottom.
    // Detach requires an explicit upward wheel/touch/keyboard intent or an
    // active scrollbar drag — content growth alone can clamp scrollTop and
    // must not be mistaken for the user reading up.
    updateScrollPin(getSubagentFollow(runId), scroller, 24, { direction: scrollbarDragging ? geometryDirection : '' });
  }, { passive: true });
}

// followTranscript scrolls to the newest output for this panel's run only.
function followTranscript(panel) {
  const scroller = transcriptScroller(panel);
  const runId = panel?.dataset?.runId || '';
  if (!scroller || !runId) return;
  if (!getSubagentFollow(runId).pinned) return;
  scroller.scrollTop = scroller.scrollHeight;
}

function syncTranscriptWithFollow(panel, transcript, prompt, promptAt) {
  syncTranscript(panel, transcript, prompt, promptAt);
  followTranscript(panel);
}

export function syncTranscript(panel, transcript, initialPrompt = '', initialPromptAt = '') {
  const box = panel?.querySelector('.acp-transcript');
  if (!box) return;
  const { chunks, usage } = normalizeTranscript(transcript);
  syncUsagePill(panel, usage);
  const segments = transcriptSegments(chunks, initialPrompt, initialPromptAt);
  if (!segments.length) {
    box.replaceChildren(el('p', { class: 'acp-empty', text: 'Waiting for the first update…' }));
    return;
  }
  box.querySelector('.acp-empty')?.remove();
  const messages = [...box.querySelectorAll(':scope > .agent-message')];
  for (let index = 0; index < segments.length; index++) {
    const segment = segments[index];
    let message = messages[index];
    if (message?.dataset.transcriptSegmentKey !== segment.key) {
      const replacement = transcriptMessage(segment);
      if (message) message.replaceWith(replacement);
      else box.append(replacement);
      message = replacement;
    } else {
      updateTranscriptMessage(message, segment);
    }
  }
  for (const message of messages.slice(segments.length)) message.remove();
}

// ACP exposes the initial delegation as run.prompt and follow-up steering as
// transcript chunks. Split the transcript at each prompt so every prompt is
// a real user message and the assistant output after it gets its own normal
// agent bubble, matching the main conversation structure.
function transcriptSegments(chunks, initialPrompt, initialPromptAt) {
  const segments = [];
  const prompt = typeof initialPrompt === 'string' ? initialPrompt.trim() : '';
  if (prompt) segments.push({
    kind: 'prompt',
    key: 'prompt:initial',
    text: initialPrompt,
    at: initialPromptAt,
  });

  let assistantChunks = [];
  let assistantIndex = 0;
  let promptIndex = 0;
  const flushAssistant = () => {
    if (!assistantChunks.length) return;
    segments.push({ kind: 'assistant', key: `assistant:${assistantIndex++}`, chunks: assistantChunks });
    assistantChunks = [];
  };

  for (const [index, chunk] of chunks.entries()) {
    if (chunk.kind === 'prompt') {
      flushAssistant();
      segments.push({
        kind: 'prompt',
        key: `prompt:${++promptIndex}`,
        text: chunk.text || '',
        at: chunk.at,
      });
      continue;
    }
    assistantChunks.push({ chunk, index });
  }
  flushAssistant();
  return segments;
}

function transcriptMessage(segment) {
  if (segment.kind === 'prompt') return transcriptPromptMessage(segment);
  const message = el('div', {
    class: 'agent-message assistant acp-run-message',
    'data-transcript-segment-key': segment.key,
  }, el('div', { class: 'agent-bubble acp-transcript-body' }));
  syncAssistantMessage(message, segment.chunks);
  return message;
}

function transcriptPromptMessage(segment) {
  const card = createSubagentPromptCard({
    label: segment.key === 'prompt:initial' ? 'Delegation' : 'Steering',
    className: 'agent-bubble',
    bodyClass: 'agent-bubble-text',
  });
  syncSubagentPromptText(card, segment.text, renderPromptContent);
  const message = el('div', {
    class: 'agent-message acp-prompt-message',
    'data-transcript-segment-key': segment.key,
  }, card.root);
  if (segment.at) {
    message.append(el('div', { class: 'agent-message-meta', text: fmtTime(segment.at) }));
  }
  return message;
}

function updateTranscriptMessage(message, segment) {
  if (segment.kind === 'prompt') {
    // The prompt card owns its header and expand control; only the dedicated
    // body may be re-rendered or the controls would be overwritten.
    const card = message.querySelector(':scope > .agent-bubble > .subagent-prompt-body')?._subagentPromptCard;
    if (card) syncSubagentPromptText(card, segment.text, renderPromptContent);
    const meta = message.querySelector(':scope > .agent-message-meta');
    if (segment.at && meta) meta.textContent = fmtTime(segment.at);
    return;
  }
  syncAssistantMessage(message, segment.chunks);
}

function renderPromptContent(body, text) {
  if (!body) return;
  const raw = typeof text === 'string' ? text : '';
  body.innerHTML = raw ? renderMarkdown(raw) : '';
  if (!body.innerHTML && raw) body.textContent = raw;
}

function syncAssistantMessage(message, entries) {
  const body = message.querySelector(':scope > .acp-transcript-body');
  if (!body) return;
  const lines = [...body.querySelectorAll(':scope > .agent-round')];
  for (let index = 0; index < entries.length; index++) {
    const { chunk, index: sourceIndex } = entries[index];
    const key = transcriptChunkKey(chunk, sourceIndex ?? index);
    let line = lines[index];
    if (line?.dataset.transcriptKey !== key) {
      const replacement = transcriptLine(chunk, key);
      if (line) line.replaceWith(replacement);
      else body.append(replacement);
    } else {
      updateTranscriptLine(line, chunk);
    }
  }
  for (const line of lines.slice(entries.length)) line.remove();
}

function normalizeTranscript(transcript) {
  const chunks = [];
  const toolIndexes = new Map();
  let usage = '';
  for (const original of transcript || []) {
    const chunk = { ...original };
    if (chunk.kind === 'usage') {
      usage = chunk.text || usage;
      continue;
    }
    if (chunk.kind === 'tool' && chunk.tool_id) {
      const existingIndex = toolIndexes.get(chunk.tool_id);
      if (existingIndex !== undefined) {
        chunks[existingIndex] = { ...chunks[existingIndex], ...withoutEmptyValues(chunk) };
        continue;
      }
      toolIndexes.set(chunk.tool_id, chunks.length);
    }
    const previous = chunks.at(-1);
    if ((chunk.kind === 'text' || chunk.kind === 'thought') && previous?.kind === chunk.kind) {
      previous.text = (previous.text || '') + (chunk.text || '');
      continue;
    }
    chunks.push(chunk);
  }
  return { chunks, usage };
}

function withoutEmptyValues(chunk) {
  return Object.fromEntries(Object.entries(chunk).filter(([, value]) => value !== '' && value != null));
}

function syncUsagePill(panel, usage) {
  const meta = panel.querySelector('.acp-run-meta');
  if (!meta) return;
  let pill = meta.querySelector('.acp-usage-pill');
  if (!usage) {
    pill?.remove();
    return;
  }
  if (!pill) {
    pill = el('span', { class: 'acp-usage-pill' });
    meta.append(pill);
  }
  pill.textContent = usage;
}

function transcriptChunkKey(chunk, index) {
  if (chunk.kind === 'tool' && chunk.tool_id) return `tool:${chunk.tool_id}`;
  return `${chunk.kind || 'text'}:${index}`;
}

function transcriptLine(chunk, key) {
  const kind = chunk.kind || 'text';
  const line = el('div', {
    class: `agent-round acp-transcript-round is-${kind}`,
    'data-transcript-key': key,
  });
  if (kind === 'thought') line.append(reasoningDisclosure(chunk.text || ''));
  else if (kind === 'tool') line.append(el('div', { class: 'agent-tool-stack' }, acpToolCard(chunk)));
  else line.append(el('div', { class: 'agent-bubble-text' }));
  updateTranscriptLine(line, chunk);
  return line;
}

function updateTranscriptLine(line, chunk) {
  if (chunk.kind === 'tool') {
    const card = line.querySelector('.agent-tool-event');
    if (!card) return;
    const status = normalizeAcpToolStatus(chunk.tool_status);
    const name = chunk.tool_kind || chunk.tool_id || chunk.tool_title || 'tool';
    const meta = acpToolMeta(chunk, status);
    const input = acpToolInput(chunk);
    const output = acpToolOutput(chunk);
    // setToolTerminalOutput repaints summary/result/raw; setToolTerminalStatus
    // inside it recomputes the head line from card._toolArgs/_toolOutput, so
    // refresh those (and the presentation) before calling it. That keeps a
    // status-only delta from leaving the previous "in progress" label visible
    // and lets a late rawInput/rawOutput patch land on the same row.
    const previousPresentation = card._toolPresentation || {};
    card._toolArgs = input || card._toolArgs || {};
    card._toolName = card._toolName || name;
    card._toolPresentation = {
      ...previousPresentation,
      action: chunk.tool_title || previousPresentation.action || name,
      request: chunk.tool_input || previousPresentation.request || '',
      result: {
        ...(previousPresentation.result || {}),
        summary: meta,
        text: output,
      },
    };
    setToolTerminalOutput(card, output, status, meta);
    if (!String(output).trim()) card.querySelector('.agent-tool-event-output')?.remove();
    const eventTitle = card.querySelector('.agent-tool-event-title');
    if (eventTitle) eventTitle.textContent = chunk.tool_title || name;
    const pathLine = card.querySelector('.agent-tool-event-path');
    if (pathLine) pathLine.textContent = toolEventRequestLine(card._toolName, card._toolArgs, card._toolPresentation);
    return;
  }
  if (chunk.kind === 'thought') {
    setReasoningSource(line.querySelector('.agent-reasoning'), chunk.text || '');
    return;
  }
  const body = line.querySelector('.agent-bubble-text');
  if (!body) return;
  const text = chunk.text || chunk.kind || '';
  if (chunk.kind === 'text' || chunk.kind === 'plan' || chunk.kind === 'status') {
    const previous = body._transcriptRaw || '';
    if (previous && !text.startsWith(previous)) body.replaceChildren();
    body._transcriptRaw = text;
    const changed = incrementalRender(body, text);
    for (const node of changed) {
      void highlightCode(node);
      attachZoomButtons(node);
    }
    return;
  }
  body.textContent = text;
}

function acpToolCard(chunk) {
  const status = normalizeAcpToolStatus(chunk.tool_status);
  const actionText = chunk.tool_title || chunk.tool_kind || chunk.tool_id || 'Tool';
  const input = acpToolInput(chunk);
  const output = acpToolOutput(chunk);
  const card = renderToolJob({
    name: chunk.tool_kind || chunk.tool_title || 'tool',
    args: input || {},
    status,
    output,
    presentation: {
      variant: 'terminal',
      action: actionText,
      request: chunk.tool_input || '',
      result: {
        format: 'terminal',
        summary: acpToolMeta(chunk, status),
        text: output,
      },
    },
  });
  // ACP transcripts are a narrow live reader. Keep tool bodies closed until a
  // reader explicitly opens one; lifecycle updates patch the same <details>
  // node and therefore preserve that choice.
  card.open = false;
  card.classList.add('acp-terminal-card');
  const head = card.querySelector('.agent-tool-event-head');
  const headText = card.querySelector('.agent-tool-event-head-text');
  if (head && headText && !head.querySelector('.acp-tool-kind')) {
    const kind = document.createElement('span');
    kind.className = 'acp-tool-kind';
    kind.textContent = 'TERMINAL';
    head.insertBefore(kind, headText);
  }
  const request = card.querySelector('.agent-tool-request');
  if (request && request.tagName !== 'BUTTON') {
    const control = document.createElement('button');
    control.type = 'button';
    control.className = request.className;
    control.textContent = request.textContent;
    control.setAttribute('aria-expanded', 'false');
    control.setAttribute('aria-label', 'Show full tool input');
    control.title = 'Show full tool input';
    control.addEventListener('click', () => {
      const expanded = control.classList.toggle('is-expanded');
      control.setAttribute('aria-expanded', String(expanded));
      control.setAttribute('aria-label', expanded ? 'Collapse tool input' : 'Show full tool input');
      control.title = expanded ? 'Collapse tool input' : 'Show full tool input';
    });
    request.replaceWith(control);
  }
  const title = card.querySelector('.agent-tool-event-title');
  if (title) title.textContent = actionText;
  if (chunk.tool_kind) {
    if (title) title.textContent = chunk.tool_kind;
    const headSummary = card.querySelector('.agent-tool-event-head-summary');
    if (headSummary) headSummary.textContent = actionText;
  }
  if (chunk.tool_id) card.dataset.acpToolId = chunk.tool_id;
  return card;
}

// acpToolInput parses the agent-provided rawInput for the tool card. Returns
// null when the agent never sent one, or sent a shape without scannable
// arguments.
function acpToolInput(chunk) {
  const raw = typeof chunk?.tool_input === 'string' ? chunk.tool_input.trim() : '';
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

// acpToolOutput prefers the agent-provided output; legacy persisted records
// (saved before tool IO capture) only carry text.
function acpToolOutput(chunk) {
  const output = typeof chunk?.tool_output === 'string' ? chunk.tool_output : '';
  return output || (typeof chunk?.text === 'string' ? chunk.text : '');
}

function normalizeAcpToolStatus(status) {
  switch (status) {
    case 'completed':
    case 'success':
    case 'ok':
      return 'ok';
    case 'failed':
    case 'error':
    case 'fail':
    case 'cancelled':
    case 'interrupted':
      return 'fail';
    default:
      return 'running';
  }
}

function acpToolMeta(chunk, status) {
  if (chunk.tool_status) return chunk.tool_status.replaceAll('_', ' ');
  if (status === 'running') return 'Running';
  if (status === 'fail') return 'Failed';
  return 'Completed';
}

export function runStatusText(run, now = Date.now()) {
  const status = statusLabel(run?.status);
  if (!LIVE.has(run?.status)) return status;
  const activity = typeof run?.activity === 'string' && run.activity.trim()
    ? run.activity.trim()
    : 'working';
  const started = startedAtMs(run);
  const elapsed = started ? formatElapsed(Math.max(0, now - started)) : '';
  const lastUpdate = Date.parse(run?.updated_at || '');
  const stale = lastUpdate ? Math.max(0, now - lastUpdate) : 0;
  const parts = [status, activity];
  if (elapsed) parts.push(elapsed);
  if (stale >= 15_000) parts.push(`last event ${formatElapsed(stale)} ago`);
  return parts.join(' · ');
}

function formatElapsed(ms) {
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  if (minutes < 60) return `${minutes}m ${remainder}s`;
  const hours = Math.floor(minutes / 60);
  const minuteRemainder = minutes % 60;
  if (hours < 24) return `${hours}h ${minuteRemainder}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

function statusLabel(status) {
  switch (status) {
    case 'starting': return 'starting';
    case 'running': return 'running';
    case 'completed': return 'done';
    case 'failed': return 'failed';
    case 'cancelled': return 'stopped';
    default: return status || 'idle';
  }
}

function riskLabel(tier) {
  switch (tier) {
    case 'bypass': return 'bypass';
    case 'edit_confirmed': return 'edits ok';
    default: return 'read-only';
  }
}

function modelLabel(run) {
  const status = run.model_selection_status;
  if (status === 'rejected') return `${run.current_model_id} (rejected)`;
  if (status === 'unverified') return `${run.current_model_id} (unverified)`;
  return run.current_model_id;
}
