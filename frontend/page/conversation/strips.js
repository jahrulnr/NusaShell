// Composer-adjacent conversation chrome ported from the production Agent
// view: the pending-steer queue, the in-flight tool job strip, and the
// transient compaction status row. The room-info mount mirrors the
// production `.agent-room-info` section so the production binder
// (frontend/js/views/agent/room-info.js) can drive it unchanged.
//
// Static contract markup wins. Every ensure() looks up the production
// element id first, so markup ported into index.html takes over without a
// code change. When the markup is absent the factories build the same
// contract-shaped nodes — same ids, classes, and ARIA wiring — inside the
// composer wrap (the steer row mounts inside the form itself), keeping
// behavior identical in both layouts. All state is room-scoped: callers
// push the active room's values on every update.

const STRIP_STYLES_ID = 'proto-conversation-strips-css';

// The conversation page is allowed to ship page-scoped CSS; loading it from
// the module keeps index.html free of strip markup dependencies while S1's
// static contract markup can still replace any of these nodes later.
function ensureStripStyles(doc) {
  if (!doc?.head || doc.getElementById(STRIP_STYLES_ID)) return;
  const link = doc.createElement('link');
  link.id = STRIP_STYLES_ID;
  link.rel = 'stylesheet';
  link.href = new URL('./strips.css', import.meta.url).href;
  doc.head.append(link);
}

function byId(doc, id) {
  return doc?.getElementById?.(id) || null;
}

// pip.js MOVES the strips (they live inside #agent-composer-stack) into the
// Document-PiP window's document, so a captured `document` stops resolving
// them. Mirror production's cached-refs approach (views/agent/domrefs.js):
// once a root is wired keep the element, and resolve every id lookup against
// the anchor's CURRENT ownerDocument so updates follow the move.
function scopeDoc(doc, anchor) {
  return anchor?.isConnected ? anchor.ownerDocument : doc;
}

function composerWrap(doc) {
  return byId(doc, 'message-form')?.parentElement || doc?.querySelector?.('.composer-wrap') || null;
}

function composerForm(doc) {
  return byId(doc, 'message-form');
}

function bindCollapseToggle(toggle, list, extra = []) {
  if (!toggle || !list || toggle.dataset.stripBound === '1') return;
  toggle.dataset.stripBound = '1';
  toggle.addEventListener('click', () => {
    const open = toggle.getAttribute('aria-expanded') === 'true';
    toggle.setAttribute('aria-expanded', String(!open));
    list.hidden = open;
    for (const node of extra) if (node) node.hidden = open;
  });
}

function formatJobElapsed(startedAt, now = Date.now()) {
  const seconds = Math.max(0, Math.floor((now - startedAt) / 1000));
  return seconds >= 60 ? `${Math.floor(seconds / 60)}m ${seconds % 60}s` : `${seconds}s`;
}

// The production policy is intentionally narrow: only exec streams output
// chunks and supports a per-call stop. `agent.turns.stop` remains the
// whole-turn cancellation owned by the composer button.
export function isStoppableTool(name) {
  return name === 'exec';
}

// createToolJobStrip owns the "in-flight tool runs" strip. Jobs arrive as
// plain descriptors collected by the caller from live tool.run events:
// { toolCallId, runId, name, label, startedAt }.
export function createToolJobStrip({ doc = globalThis.document, onStop, anchor = null } = {}) {
  let elapsedTimer = null;
  let jobs = [];
  let rootRef = null;

  function ensure() {
    const d = scopeDoc(doc, anchor);
    let root = rootRef?.isConnected ? rootRef : byId(d, 'tool-job-strip');
    if (root) {
      rootRef = root;
      wire(root, false);
      return root;
    }
    const wrap = composerWrap(d);
    const form = composerForm(d);
    if (!wrap || !form) return null;
    ensureStripStyles(d);
    root = d.createElement('div');
    root.className = 'agent-tool-job-strip';
    root.id = 'tool-job-strip';
    root.hidden = true;
    root.setAttribute('aria-label', 'Background tool runs');
    root.innerHTML = `
      <div class="agent-tool-job-strip-head">
        <button class="agent-tool-job-strip-toggle" id="tool-job-strip-toggle" type="button" aria-label="Tool runs" aria-expanded="false" aria-controls="tool-job-list" title="Collapse/expand tool runs">
          <span class="agent-tool-job-strip-chevron" aria-hidden="true">›</span>
          <span class="agent-tool-job-strip-title" id="tool-job-strip-title">0 tool runs</span>
        </button>
        <span class="agent-tool-job-strip-meta" id="tool-job-strip-meta" aria-live="polite" aria-atomic="true"></span>
      </div>
      <div class="agent-tool-job-list" id="tool-job-list" role="list" aria-live="off" hidden></div>`;
    // The steer queue lives inside the composer form; the tool strip stays
    // the floating sibling directly above it regardless of which factory ran
    // first.
    wrap.insertBefore(root, form);
    rootRef = root;
    wire(root, true);
    return root;
  }

  // Collapse toggles on static contract markup are already bound by
  // shell-init.js — binding them again would toggle twice per click. Only the
  // dynamically mounted strip (created here, after shell-init ran) needs the
  // collapse wiring; the stop delegation is always ours.
  function wire(root, created) {
    if (root.dataset.stripWired === '1') return;
    root.dataset.stripWired = '1';
    if (created) bindCollapseToggle(root.querySelector('#tool-job-strip-toggle'), root.querySelector('#tool-job-list'));
    root.querySelector('#tool-job-list')?.addEventListener('click', (event) => {
      const button = event.target.closest?.('.agent-tool-job-stop');
      if (!button || button.disabled) return;
      const item = button.closest('[data-tool-call-id]');
      const job = jobs.find((candidate) => candidate.toolCallId === item?.dataset.toolCallId);
      if (!job) return;
      button.disabled = true;
      button.textContent = 'Stopping…';
      Promise.resolve(onStop?.(job)).catch(() => {
        button.disabled = false;
        button.textContent = 'Stop';
      });
    });
  }

  function renderList(list) {
    const d = list.ownerDocument || doc;
    const nodes = jobs.map((job) => {
      const item = d.createElement('div');
      item.className = 'agent-tool-job-item';
      item.dataset.toolCallId = job.toolCallId;
      item.setAttribute('role', 'listitem');
      const name = d.createElement('span');
      name.className = 'agent-tool-job-name';
      name.textContent = job.label || job.name || 'tool';
      name.title = name.textContent;
      const elapsed = d.createElement('span');
      elapsed.className = 'agent-tool-job-elapsed';
      elapsed.textContent = formatJobElapsed(job.startedAt);
      item.append(name, elapsed);
      if (isStoppableTool(job.name)) {
        const stop = d.createElement('button');
        stop.className = 'agent-tool-job-stop';
        stop.type = 'button';
        stop.textContent = 'Stop';
        stop.setAttribute('aria-label', `Stop ${job.name}`);
        item.append(stop);
      }
      return item;
    });
    list.replaceChildren(...nodes);
  }

  function stopElapsedTimer() {
    if (!elapsedTimer) return;
    clearInterval(elapsedTimer);
    elapsedTimer = null;
  }

  function syncElapsed() {
    const root = rootRef?.isConnected ? rootRef : null;
    if (!root) return;
    const list = root.querySelector('#tool-job-list');
    if (!list) return;
    const now = Date.now();
    for (const job of jobs) {
      const row = list.querySelector(`[data-tool-call-id="${job.toolCallId}"]`);
      const elapsed = row?.querySelector('.agent-tool-job-elapsed');
      if (elapsed) elapsed.textContent = formatJobElapsed(job.startedAt, now);
    }
    const meta = root.querySelector('#tool-job-strip-meta');
    if (meta && jobs.length) {
      const oldest = jobs.reduce((min, job) => Math.min(min, job.startedAt || now), now);
      meta.textContent = `oldest ${formatJobElapsed(oldest, now)}`;
    }
  }

  function sync(next = []) {
    jobs = next.filter((job) => job?.toolCallId);
    const d = scopeDoc(doc, anchor);
    if (!jobs.length) {
      const root = rootRef?.isConnected ? rootRef : byId(d, 'tool-job-strip');
      if (root) root.hidden = true;
      stopElapsedTimer();
      return;
    }
    const root = ensure();
    if (!root) return;
    root.hidden = false;
    const title = root.querySelector('#tool-job-strip-title');
    if (title) title.textContent = `${jobs.length} tool run${jobs.length === 1 ? '' : 's'}`;
    const list = root.querySelector('#tool-job-list');
    if (list) renderList(list);
    syncElapsed();
    stopElapsedTimer();
    elapsedTimer = setInterval(syncElapsed, 1000);
  }

  return { sync, clear: () => sync([]) };
}

// createSteerQueue owns the "pending steer" row — one compact line stacked
// at the top of the composer frame, not a floating strip. The queued text
// never enters the transcript: it is only promoted to a user bubble after
// the backend applies it at a safe boundary (agent.steer.applied).
export function createSteerQueue({ doc = globalThis.document, onCancel, anchor = null } = {}) {
  let rootRef = null;

  function ensure() {
    const d = scopeDoc(doc, anchor);
    let root = rootRef?.isConnected ? rootRef : byId(d, 'agent-steer-queue');
    if (root) {
      rootRef = root;
      wire(root);
      return root;
    }
    const form = composerForm(d);
    if (!form) return null;
    ensureStripStyles(d);
    root = d.createElement('section');
    root.className = 'agent-steer-queue';
    root.id = 'agent-steer-queue';
    root.hidden = true;
    root.setAttribute('aria-label', 'Pending steer');
    root.setAttribute('aria-live', 'polite');
    root.innerHTML = `
      <div class="agent-steer-queue-item" id="agent-steer-queue-list" role="list" title="Waiting for a safe boundary">
        <svg class="agent-steer-queue-icon" viewBox="0 0 24 24" aria-hidden="true"><polyline points="9 14 4 9 9 4"/><path d="M20 20v-7a4 4 0 0 0-4-4H4"/></svg>
        <span class="agent-steer-queue-text" id="agent-steer-queue-text"></span>
        <button class="agent-steer-cancel" id="agent-steer-cancel" type="button" aria-label="Cancel queued steer" title="Cancel queued steer" hidden>×</button>
      </div>`;
    form.prepend(root);
    rootRef = root;
    wire(root);
    return root;
  }

  function wire(root) {
    if (root.dataset.stripWired === '1') return;
    root.dataset.stripWired = '1';
    root.querySelector('#agent-steer-cancel')?.addEventListener('click', () => { void onCancel?.(); });
  }

  function show(text, steerId = '') {
    const root = ensure();
    if (!root) return;
    const textEl = root.querySelector('#agent-steer-queue-text');
    const cancel = root.querySelector('#agent-steer-cancel');
    if (textEl) {
      textEl.textContent = text || '';
      textEl.title = text || '';
    }
    if (cancel) {
      cancel.hidden = false;
      cancel.disabled = false;
    }
    root.dataset.steerId = steerId || '';
    root.hidden = false;
  }

  function clear() {
    const root = rootRef?.isConnected ? rootRef : byId(scopeDoc(doc, anchor), 'agent-steer-queue');
    if (root) {
      root.hidden = true;
      delete root.dataset.steerId;
    }
    const cancel = root?.querySelector('#agent-steer-cancel');
    if (cancel) cancel.hidden = true;
  }

  return { show, clear };
}

// createCompactionStatus renders the short-lived inline "compacting" row at
// the tail of the event stream. It deliberately lives outside the persisted
// event model: the matching compacted/failed event (or a terminal/settled
// turn) removes it.
export function createCompactionStatus({ doc = globalThis.document, stream = null } = {}) {
  let node = null;
  let runId = '';

  function build() {
    const d = stream?.ownerDocument || doc;
    ensureStripStyles(d);
    node = d.createElement('div');
    node.className = 'agent-compaction-status';
    node.setAttribute('role', 'status');
    node.setAttribute('aria-live', 'polite');
    node.setAttribute('aria-atomic', 'true');
    const mark = d.createElement('span');
    mark.className = 'agent-compaction-status-mark';
    mark.setAttribute('aria-hidden', 'true');
    mark.textContent = '↻';
    const text = d.createElement('span');
    text.className = 'agent-compaction-status-text';
    text.textContent = 'Context automatically compacting';
    const dots = d.createElement('span');
    dots.className = 'agent-compaction-status-dots';
    dots.setAttribute('aria-hidden', 'true');
    dots.append(d.createElement('span'), d.createElement('span'), d.createElement('span'));
    node.append(mark, text, dots);
    return node;
  }

  function show(nextRunId = '') {
    if (!stream?.isConnected) return;
    if (nextRunId && runId && nextRunId !== runId) {
      node?.remove();
      node = null;
    }
    runId = nextRunId || runId;
    if (!node || !node.isConnected) node = build();
    if (stream.lastElementChild !== node) stream.append(node);
  }

  function clear(nextRunId = '') {
    if (nextRunId && runId && nextRunId !== runId) return;
    node?.remove();
    node = null;
    runId = '';
  }

  return { show, clear, get active() { return Boolean(node?.isConnected); } };
}

// mountPipAliases gives the prototype layout the two ids production pip.js
// moves into the Document-PiP mini window: `#agent-thread` wraps
// `#event-stream` (display:contents in strips.css keeps the normal layout
// identical) and `#agent-composer-stack` aliases the existing .composer-wrap,
// which — like production — carries the strips and the form as one unit.
// Because pip.js moves the live nodes, every stream/composer/strip listener
// keeps working inside the PiP window without extra wiring.
export function mountPipAliases({ doc = globalThis.document, stream = null } = {}) {
  if (stream && !byId(doc, 'agent-thread')) {
    const wrap = doc.createElement('div');
    wrap.id = 'agent-thread';
    stream.parentNode?.insertBefore(wrap, stream);
    wrap.appendChild(stream);
    ensureStripStyles(doc);
  }
  const wrap = composerWrap(doc);
  if (wrap && !byId(doc, 'agent-composer-stack')) wrap.id = 'agent-composer-stack';
}

// mountRoomInfo builds the production-shaped `.agent-room-info` section when
// the static contract markup is absent, then lets the caller bind the
// production module (updateRoomInfo) against the shared id. The section now
// carries only the mini-window control — room diagnostics are owned by the
// Context dock.
export function mountRoomInfo({ doc = globalThis.document, host = null } = {}) {
  if (byId(doc, 'agent-room-info')) return byId(doc, 'agent-room-info');
  const anchor = host || doc?.querySelector?.('.conversation') || null;
  if (!anchor) return null;
  ensureStripStyles(doc);
  const section = doc.createElement('section');
  section.className = 'agent-room-info';
  section.id = 'agent-room-info';
  section.setAttribute('aria-label', 'Conversation actions');
  section.hidden = true;
  section.innerHTML = `
    <div class="agent-room-info-controls">
      <button class="icon-btn agent-room-info-mini" id="mini-window-btn" type="button" aria-label="Open mini window" title="Mini window (picture-in-picture)" hidden>
        <svg viewBox="0 0 24 24" width="16" height="16" fill="none" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2" stroke="currentColor" stroke-width="1.6"/><rect x="12" y="12" width="7" height="5" rx="1" fill="currentColor"/></svg>
      </button>
    </div>`;
  anchor.prepend(section);
  return section;
}
