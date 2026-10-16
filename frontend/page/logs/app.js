import { on as defaultOn, rpc as defaultRPC } from '../../js/rpc.js';
import { bindTablistKeyboard, confirmDialog, createSelect, debounce, el, fmtClock, toast } from '../../js/ui.js';
import { filterLogEntries, logLevelLabel, normalizeLogEntry, normalizeLogLevel } from './model.js';

// Ring buffer for the live tail. The documented parity target is 2000 entries
// (ui-logs.md); production frontend/js/views/logs.js currently caps at 500.
const MAX_ENTRIES = 2000;
const LOG_LEVEL_OPTIONS = [
  { value: '', label: 'All levels' },
  { value: 'debug', label: 'Debug' },
  { value: 'info', label: 'Info' },
  { value: 'warn', label: 'Warning' },
  { value: 'error', label: 'Error' },
];

function entriesFromResult(result) {
  const seen = new Set();
  const entries = [];
  for (const value of Array.isArray(result?.entries) ? result.entries : []) {
    const entry = normalizeLogEntry(value);
    if (!entry || seen.has(entry.id)) continue;
    seen.add(entry.id);
    entries.push(entry);
  }
  return entries.slice(-MAX_ENTRIES);
}

function safeLogTime(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value || 'Unknown time') : fmtClock(value);
}

function nextFrame(callback) {
  if (typeof requestAnimationFrame === 'function') requestAnimationFrame(callback);
  else setTimeout(callback, 0);
}

export function createLogsPage(root = document, { rpcClient = defaultRPC, onEvent = defaultOn } = {}) {
  const state = {
    entries: [],
    query: '',
    level: '',
    follow: true,
    selectedId: null,
    pendingCount: 0,
    loading: true,
    clearing: false,
    error: null,
  };
  let initialized = false;
  let destroyed = false;
  let refreshToken = 0;
  let refreshPromise = null;
  let levelSelect = null;
  let unsubscribe = null;
  let suppressScroll = false;
  let mobilePaneToken = 0;
  const listeners = [];

  const find = (id) => root.querySelector(`#${id}`);
  const pageNode = () => root.nodeType === 1 ? root : root.querySelector('#logs-view');
  const isMobile = () => Number(window.innerWidth) <= 720;
  const selectedEntry = () => state.entries.find((entry) => entry.id === state.selectedId) || null;
  const visibleEntries = () => filterLogEntries(state.entries, state.query, state.level);
  const listNode = () => find('logs-list');
  const detailPanelNode = () => root.querySelector('.logs-detail-panel');

  function setMobilePane(pane) {
    const workspace = find('logs-workspace');
    const page = pageNode();
    if (!workspace) return;
    const previousPane = workspace.dataset.mobilePane || 'list';
    const changed = previousPane !== pane;
    const list = listNode();
    if (pane === 'detail' && previousPane === 'list' && isMobile()) {
      workspace.dataset.mobileListScroll = String(list?.scrollTop || 0);
    }
    workspace.dataset.mobilePane = pane;
    if (page) page.dataset.mobilePane = pane;
    if (!changed) return mobilePaneToken;

    const token = ++mobilePaneToken;
    if (!isMobile() || !page) return token;
    if (pane === 'detail') {
      const panel = detailPanelNode();
      if (panel) panel.scrollTop = 0;
    } else if (pane === 'list') {
      const top = Number(workspace.dataset.mobileListScroll) || 0;
      nextFrame(() => {
        if (destroyed || token !== mobilePaneToken || workspace.dataset.mobilePane !== 'list' || !isMobile()) return;
        const restoredList = listNode();
        if (state.follow) scrollToLatest();
        else if (restoredList) restoredList.scrollTop = top;
      });
    }
    return token;
  }

  function focusMobileControl(id, token = mobilePaneToken) {
    if (!isMobile()) return;
    nextFrame(() => {
      if (destroyed || token !== mobilePaneToken) return;
      find(id)?.focus({ preventScroll: true });
    });
  }

  function addListener(node, type, handler, options) {
    if (!node) return;
    node.addEventListener(type, handler, options);
    listeners.push(() => node.removeEventListener(type, handler, options));
  }

  function setSyncState(copy, tone = '') {
    const node = find('logs-sync-state');
    if (!node) return;
    node.textContent = copy;
    node.dataset.state = tone;
  }

  function setError(error) {
    state.error = error || null;
    const box = find('logs-error');
    const message = find('logs-error-message');
    if (box) box.hidden = !state.error;
    if (message) message.textContent = state.error?.message || '';
  }

  function updateToolbar() {
    const visible = visibleEntries();
    const total = state.entries.length;
    const count = `${visible.length} entr${visible.length === 1 ? 'y' : 'ies'}`;
    const countNode = find('logs-count');
    const visibleCount = find('logs-visible-count');
    if (countNode) countNode.textContent = state.query || state.level ? `${count} of ${total}` : count;
    if (visibleCount) visibleCount.textContent = state.query || state.level ? `${visible.length} shown` : `${total} total`;
    const follow = find('logs-follow');
    if (follow) follow.checked = state.follow;
    const latest = find('logs-latest');
    const pending = find('logs-pending-count');
    if (latest) latest.hidden = state.follow && state.pendingCount === 0;
    if (pending) pending.textContent = state.pendingCount ? `${state.pendingCount} new` : 'Latest';
    const clear = find('logs-clear');
    if (clear) {
      clear.disabled = state.clearing || !state.entries.length;
      clear.textContent = state.clearing ? 'Clearing…' : 'Clear logs';
    }
  }

  function emptyList(title, copy, action = null, error = false) {
    const children = [el('strong', { text: title }), el('span', { text: copy })];
    if (action) {
      const button = el('button', { class: 'logs-inline-action', type: 'button', text: action.label });
      button.addEventListener('click', action.onClick);
      children.push(button);
    }
    return el('div', { class: `logs-list-empty${error ? ' logs-list-empty-error' : ''}`, role: 'status' }, children);
  }

  function logRow(entry) {
    const tone = normalizeLogLevel(entry.level);
    const row = el('article', {
      class: `logs-entry${entry.id === state.selectedId ? ' is-selected' : ''}`,
      role: 'listitem',
      tabIndex: 0,
      dataset: { logId: entry.id },
      'aria-label': `${logLevelLabel(entry.level)} from ${entry.source || 'runtime'}: ${entry.message || 'empty message'}`,
    }, [
      el('span', { class: 'logs-entry-time', text: safeLogTime(entry.time), title: entry.time || '' }),
      el('span', { class: `logs-entry-level logs-level-${tone}`, text: logLevelLabel(entry.level) }),
      el('span', { class: 'logs-entry-source', text: entry.source || 'runtime' }),
      el('span', { class: 'logs-entry-message', text: entry.message || '—' }),
    ]);
    const select = () => selectEntry(entry.id);
    row.addEventListener('click', select);
    row.addEventListener('keydown', (event) => {
      if (event.key !== 'Enter' && event.key !== ' ') return;
      event.preventDefault();
      select();
    });
    return row;
  }

  function syncSelectedRows() {
    root.querySelectorAll('[data-log-id]').forEach((row) => {
      const active = row.dataset.logId === state.selectedId;
      row.classList.toggle('is-selected', active);
      row.setAttribute('aria-current', active ? 'true' : 'false');
    });
  }

  function renderList({ appendOnly = false } = {}) {
    const list = listNode();
    if (!list) return;
    const visible = visibleEntries();
    list.setAttribute('aria-busy', String(state.loading));
    updateToolbar();
    setError(state.error);

    if (!appendOnly) list.replaceChildren();
    if (state.loading && !state.entries.length) {
      list.replaceChildren(emptyList('Loading logs…', 'Reading the runtime trail.'));
      return;
    }
    if (state.error && !state.entries.length) {
      list.replaceChildren(emptyList('Logs unavailable', state.error.message || 'The runtime trail could not be read.', { label: 'Retry', onClick: () => { void refresh(); } }, true));
      return;
    }
    if (!visible.length) {
      list.replaceChildren(emptyList(
        state.query || state.level ? 'No matching logs' : 'No log entries',
        state.query || state.level ? 'Try another search or level.' : 'Runtime events will appear here as NusaShell works.',
      ));
      return;
    }

    const existing = new Set([...list.querySelectorAll('[data-log-id]')].map((row) => row.dataset.logId));
    for (const entry of visible) {
      if (appendOnly && existing.has(entry.id)) continue;
      list.append(logRow(entry));
    }
    syncSelectedRows();
  }

  function renderDetail() {
    const empty = find('logs-detail-empty');
    const detail = find('logs-detail');
    const entry = selectedEntry();
    if (!empty || !detail) return;
    empty.hidden = Boolean(entry);
    detail.hidden = !entry;
    if (!entry) return;
    const tone = normalizeLogLevel(entry.level);
    const level = find('logs-detail-level');
    if (level) {
      level.className = `logs-detail-level logs-level-${tone}`;
      level.textContent = logLevelLabel(entry.level);
    }
    const time = find('logs-detail-time');
    if (time) time.textContent = safeLogTime(entry.time);
    const source = find('logs-detail-source');
    if (source) source.textContent = entry.source || 'runtime';
    const id = find('logs-detail-id');
    if (id) id.textContent = entry.id;
    const message = find('logs-detail-message');
    if (message) message.textContent = entry.message || '';
  }

  function renderAll() {
    renderList();
    renderDetail();
    updateToolbar();
    window.lucide?.createIcons?.({ attrs: { 'stroke-width': 1.7 } });
  }

  function scrollToLatest() {
    const list = listNode();
    if (!list) return;
    suppressScroll = true;
    list.scrollTop = list.scrollHeight;
    nextFrame(() => { suppressScroll = false; });
  }

  function isAtBottom() {
    const list = listNode();
    if (!list) return true;
    return list.scrollHeight - list.scrollTop - list.clientHeight <= 24;
  }

  function selectEntry(id) {
    if (!state.entries.some((entry) => entry.id === id)) return;
    state.selectedId = id;
    const paneToken = setMobilePane('detail');
    syncSelectedRows();
    renderDetail();
    focusMobileControl('logs-mobile-back', paneToken);
  }

  function setFilter({ query = state.query, level = state.level } = {}) {
    state.query = query;
    state.level = level;
    if (state.selectedId && !visibleEntries().some((entry) => entry.id === state.selectedId)) {
      state.selectedId = null;
      setMobilePane('list');
    }
    renderList();
    renderDetail();
  }

  function setFollow(follow) {
    state.follow = Boolean(follow);
    if (state.follow) {
      state.pendingCount = 0;
      if (!(isMobile() && find('logs-workspace')?.dataset.mobilePane === 'detail')) scrollToLatest();
    }
    updateToolbar();
  }

  function handleAppend(payload) {
    const entry = normalizeLogEntry(payload?.entry ?? payload);
    if (!entry || destroyed) return;
    const existingIndex = state.entries.findIndex((candidate) => candidate.id === entry.id);
    if (existingIndex >= 0) {
      state.entries[existingIndex] = entry;
      renderList();
      renderDetail();
      return;
    }
    state.entries.push(entry);
    if (state.entries.length > MAX_ENTRIES) state.entries.splice(0, state.entries.length - MAX_ENTRIES);
    if (!state.follow) state.pendingCount += 1;
    renderList({ appendOnly: true });
    renderDetail();
    if (state.follow && !(isMobile() && find('logs-workspace')?.dataset.mobilePane === 'detail')) scrollToLatest();
    updateToolbar();
  }

  async function refresh() {
    if (refreshPromise) return refreshPromise;
    const token = ++refreshToken;
    state.loading = true;
    setError(null);
    setSyncState('Syncing…');
    renderList();
    refreshPromise = (async () => {
      try {
        const result = await rpcClient('logs.list', { limit: 300 }, { timeoutMs: 30000 });
        if (token !== refreshToken || destroyed) return;
        state.entries = entriesFromResult(result);
        state.loading = false;
        if (state.selectedId && !state.entries.some((entry) => entry.id === state.selectedId)) {
          state.selectedId = null;
          setMobilePane('list');
        }
        setSyncState('Synced', 'ready');
        renderAll();
        if (state.follow && !(isMobile() && find('logs-workspace')?.dataset.mobilePane === 'detail')) scrollToLatest();
      } catch (error) {
        if (token !== refreshToken || destroyed) return;
        state.loading = false;
        setError(error);
        setSyncState('Sync failed', 'error');
        renderAll();
      } finally {
        refreshPromise = null;
      }
    })();
    return refreshPromise;
  }

  async function clearLogs() {
    if (state.clearing || !state.entries.length) return;
    if (!await confirmDialog('Clear runtime logs', 'All stored log entries will be removed. New runtime events can still arrive afterward.', 'Clear logs')) {
      find('logs-clear')?.focus();
      return;
    }
    state.clearing = true;
    updateToolbar();
    try {
      await rpcClient('logs.clear', {}, { timeoutMs: 30000 });
      state.entries = [];
      state.selectedId = null;
      state.pendingCount = 0;
      setMobilePane('list');
      setError(null);
      setSyncState('Cleared', 'ready');
      renderAll();
      toast('Runtime logs cleared', 'success');
      find('logs-clear')?.focus();
    } catch (error) {
      setError(error);
      renderAll();
      toast(error.message || 'Could not clear runtime logs', 'error');
    } finally {
      state.clearing = false;
      updateToolbar();
    }
  }

  function jumpToLatest() {
    state.follow = true;
    state.pendingCount = 0;
    updateToolbar();
    if (isMobile() && find('logs-workspace')?.dataset.mobilePane === 'detail') setMobilePane('list');
    scrollToLatest();
  }

  function init() {
    if (initialized) return controller;
    initialized = true;
    setMobilePane('list');
    levelSelect = find('logs-level');
    if (levelSelect) {
      levelSelect.replaceChildren(...LOG_LEVEL_OPTIONS.map((option) => el('option', { value: option.value, text: option.label })));
      levelSelect = createSelect(levelSelect, {
        data: LOG_LEVEL_OPTIONS.map((option) => ({ value: option.value, text: option.label })),
        value: state.level,
        placeholder: 'All levels',
        onChange: (value) => setFilter({ level: value }),
      });
    }
    addListener(find('logs-search'), 'input', debounce((event) => setFilter({ query: event.target.value }), 120));
    addListener(find('logs-follow'), 'change', (event) => setFollow(event.target.checked));
    const handleScroll = () => {
      if (suppressScroll) return;
      if (isMobile() && find('logs-workspace')?.dataset.mobilePane === 'detail') return;
      const atBottom = isAtBottom();
      if (atBottom) {
        if (!state.follow || state.pendingCount) {
          state.follow = true;
          state.pendingCount = 0;
          updateToolbar();
        }
      } else if (state.follow) {
        state.follow = false;
        updateToolbar();
      }
    };
    addListener(find('logs-list'), 'scroll', handleScroll, { passive: true });
    addListener(find('logs-mobile-back'), 'click', () => {
      const paneToken = setMobilePane('list');
      if (isMobile()) nextFrame(() => {
        if (destroyed || paneToken !== mobilePaneToken || find('logs-workspace')?.dataset.mobilePane !== 'list') return;
        root.querySelector('.logs-entry.is-selected')?.focus({ preventScroll: true });
      });
    });
    addListener(find('logs-refresh'), 'click', () => { void refresh(); });
    addListener(find('logs-retry'), 'click', () => { void refresh(); });
    addListener(find('logs-clear'), 'click', () => { void clearLogs(); });
    addListener(find('logs-latest'), 'click', jumpToLatest);
    unsubscribe = onEvent('logs.append', handleAppend);
    bindTablistKeyboard(find('logs-level-tabs'));
    renderAll();
    return controller;
  }

  function destroy() {
    mobilePaneToken += 1;
    destroyed = true;
    refreshToken += 1;
    unsubscribe?.();
    unsubscribe = null;
    levelSelect?.destroy?.();
    levelSelect = null;
    for (const remove of listeners.splice(0)) remove();
  }

  function closeOverlays() {
    // Logs has no custom overlay; shared dialogs are dismissed by the router.
  }

  const controller = { init, refresh, closeOverlays, destroy, selectEntry, state };
  return controller;
}
